package rtsp_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/rtsp"
)

// TestCaptureReproducesTheStreamByteForByte is the round trip that matters: a
// real socket, a real handshake, a real repacketisation, and a comparison
// against the original bytes.
func TestCaptureReproducesTheStreamByteForByte(t *testing.T) {
	nals := loadNALs(t)
	srv := newServer(t, nals)
	ctx := context.Background()

	c, err := rtsp.Dial(ctx, srvURL(t, srv), rtsp.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	if _, err := c.Options(ctx); err != nil {
		t.Fatalf("OPTIONS: %v", err)
	}
	if _, err := c.Describe(ctx); err != nil {
		t.Fatalf("DESCRIBE: %v", err)
	}
	desc := c.Description()
	if desc.PayloadType != 96 {
		t.Errorf("payload type %d, want 96", desc.PayloadType)
	}
	if desc.ClockRate != 90000 {
		t.Errorf("clock rate %d, want 90000", desc.ClockRate)
	}
	if desc.FPS != 25 {
		t.Errorf("framerate %d, want 25", desc.FPS)
	}
	if desc.Width != 640 || desc.Height != 360 {
		t.Errorf("framesize %dx%d, want 640x360", desc.Width, desc.Height)
	}
	if desc.Control != "trackID=2" {
		t.Errorf("control %q, want trackID=2", desc.Control)
	}
	if desc.PacketizationMode != 1 {
		t.Errorf("packetization mode %d, want 1", desc.PacketizationMode)
	}
	if len(desc.SPS) == 0 || len(desc.PPS) == 0 {
		t.Fatal("the description's sprop-parameter-sets did not decode")
	}
	wantSPS, wantPPS := parameterSets(t, nals)
	if string(desc.SPS) != string(wantSPS) || string(desc.PPS) != string(wantPPS) {
		t.Errorf("parameter sets did not survive the description:\n sps got %x want %x\n pps got %x want %x",
			desc.SPS, wantSPS, desc.PPS, wantPPS)
	}

	if err := c.Play(ctx); err != nil {
		t.Fatalf("PLAY: %v", err)
	}
	// The session id is whatever the last handshake step said it was, not
	// whatever an earlier one guessed.
	if c.Description().Session != "12345678" {
		t.Errorf("session %q after PLAY, want 12345678", c.Description().Session)
	}
	var got [][]byte
	for {
		nal, _, _, err := c.ReadNAL()
		if err != nil {
			break
		}
		got = append(got, append([]byte(nil), nal...))
	}
	if len(got) != len(nals) {
		t.Fatalf("recovered %d NAL units from %d sent", len(got), len(nals))
	}
	for i := range nals {
		if string(got[i]) != string(nals[i]) {
			t.Fatalf("NAL %d differs:\n got %x\nwant %x", i, got[i], nals[i])
		}
	}
	// SETUP must address the media control URI, not the session URL. A server
	// that accepts either hides the difference, so it is asserted on what the
	// server saw.
	var sawSetupOnTrack bool
	for _, r := range srv.Seen() {
		if strings.HasPrefix(r, "SETUP ") && strings.HasSuffix(r, "/live") {
			sawSetupOnTrack = true
		}
	}
	if !sawSetupOnTrack {
		t.Errorf("SETUP did not address the media control URI; requests were %v", srv.Seen())
	}
}

// TestAPayloadTypeOfZeroIsNotMistakenForNoVideo pins a distinction the parser
// used to collapse.
//
// Payload type 0 is a legal value in RTP. Carrying it as the "nothing was
// declared" sentinel refused a working camera with "the session description has
// no video media section" - a message about a missing section, on a description
// whose video section is right there. The other half of the same control is that
// a description with no video section is still refused, because accepting one
// would hand the caller a stream that cannot exist.
func TestAPayloadTypeOfZeroIsNotMistakenForNoVideo(t *testing.T) {
	const head = "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=Camera\r\nc=IN IP4 0.0.0.0\r\nt=0 0\r\na=control:*\r\n"

	t.Run("payload type zero is accepted", func(t *testing.T) {
		srv := newServer(t, nil, withSDP(head+
			"m=video 0 RTP/AVP 0\r\n"+
			"a=rtpmap:0 H264/90000\r\n"+
			"a=framerate:25\r\n"+
			"a=control:trackID=1\r\n"))
		ctx := context.Background()
		c, err := rtsp.Dial(ctx, srvURL(t, srv), rtsp.Options{Timeout: 3 * time.Second})
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer func() { _ = c.Close() }()
		if _, err := c.Describe(ctx); err != nil {
			t.Fatalf("a camera offering payload type 0 was refused: %v", err)
		}
		desc := c.Description()
		if desc.PayloadType != 0 {
			t.Errorf("payload type %d, want 0", desc.PayloadType)
		}
		if desc.ClockRate != 90000 {
			t.Errorf("clock rate %d, want the 90000 the description declared", desc.ClockRate)
		}
	})

	t.Run("a description with no video section is still refused", func(t *testing.T) {
		srv := newServer(t, nil, withSDP(head+
			"m=audio 0 RTP/AVP 97\r\n"+
			"a=rtpmap:97 mpeg4-generic/44100/2\r\n"+
			"a=control:trackID=1\r\n"))
		ctx := context.Background()
		c, err := rtsp.Dial(ctx, srvURL(t, srv), rtsp.Options{Timeout: 3 * time.Second})
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer func() { _ = c.Close() }()
		if _, err := c.Describe(ctx); !errors.Is(err, rtsp.ErrNoMediaSection) {
			t.Fatalf("got %v, want ErrNoMediaSection", err)
		}
	})
}

func TestRefusesWhatItCannotCarry(t *testing.T) {
	cases := []struct {
		raw  string
		want error
	}{
		{"http://example.com/stream", rtsp.ErrNotRTSP},
		{"rtsps://example.com/stream", rtsp.ErrUnsupportedTransport},
		{"rtsp:///stream", rtsp.ErrNotRTSP},
		{"rtsp://example.com:99999/stream", rtsp.ErrNotRTSP},
	}
	for _, tc := range cases {
		_, err := rtsp.ParseURL(tc.raw)
		if err == nil {
			t.Errorf("%s was accepted", tc.raw)
			continue
		}
		if !strings.Contains(err.Error(), strings.TrimPrefix(tc.want.Error(), "rtsp: ")) {
			t.Errorf("%s: got %v, want %v", tc.raw, err, tc.want)
		}
	}
}

func TestCameraCredentialsNeverAppearInADiagnostic(t *testing.T) {
	// A camera password that reaches a log line is a camera password in a file
	// somebody attaches to a ticket. Every string this package renders for a
	// human goes through the redaction path, so it is asserted here rather than
	// reviewed by eye.
	u, err := rtsp.ParseURL("rtsp://admin:hunter2@192.0.2.10:554/live")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, s := range []string{u.String(), u.Request()} {
		if strings.Contains(s, "hunter2") {
			t.Errorf("%q carries the camera password", s)
		}
		if !strings.Contains(s, "192.0.2.10:554/live") {
			t.Errorf("%q lost the endpoint it is supposed to identify", s)
		}
	}
	// And the same holds for a failure that quotes the URL back.
	srv := newServer(t, nil, withBasicAuth("", "", 401))
	raw := strings.Replace(srvURL(t, srv), "rtsp://", "rtsp://admin:hunter2@", 1)
	c, err := rtsp.Dial(context.Background(), raw, rtsp.Options{Timeout: 2 * time.Second})
	if err != nil {
		if strings.Contains(err.Error(), "hunter2") {
			t.Fatalf("the dial error carries the camera password: %v", err)
		}
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	_, err = c.Describe(context.Background())
	if err == nil {
		t.Fatal("expected a 401")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the failure carries the camera password: %v", err)
	}
}

func TestAuthenticationFailureIsNamedSeparatelyFromAConnectionFailure(t *testing.T) {
	// "connection failed" cannot be acted on. A camera that wants a password
	// and a camera that is not there need different responses from an operator.
	srv := newServer(t, nil, withBasicAuth("admin", "hunter2", 401))

	ctx := context.Background()
	c, err := rtsp.Dial(ctx, srvURL(t, srv), rtsp.Options{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	_, err = c.Describe(ctx)
	if err == nil {
		t.Fatal("a 401 was accepted")
	}
	if !strings.Contains(err.Error(), "authentication") {
		t.Errorf("401 produced %q, which does not say the camera wanted credentials", err)
	}
}

func TestAServerThatNeverAnswersIsBoundedByTheTimeout(t *testing.T) {
	// A camera that accepts a connection and then says nothing must not hold a
	// capture worker forever.
	srv := newServer(t, nil, withFaults(rtsp.ServerFaults{HangResponse: true}))
	ctx := context.Background()
	c, err := rtsp.Dial(ctx, srvURL(t, srv), rtsp.Options{Timeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	start := time.Now()
	if _, err := c.Options(ctx); err == nil {
		t.Fatal("a silent server answered OPTIONS")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("the request took %v; the timeout is not bounding the wait", elapsed)
	}
}

func TestAServerThatNeverEndsItsHeadersIsRefusedRatherThanBuffered(t *testing.T) {
	srv := newServer(t, nil, withFaults(rtsp.ServerFaults{OversizeHead: true}))
	ctx := context.Background()
	c, err := rtsp.Dial(ctx, srvURL(t, srv), rtsp.Options{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Options(ctx); err == nil {
		t.Fatal("an endless header block was accepted")
	}
}

func TestUDPIsRefusedByName(t *testing.T) {
	// A client that quietly picked UDP and hung would cost more to diagnose than
	// this refusal does to write.
	_, err := rtsp.Dial(context.Background(), "rtsp://192.0.2.10/live",
		rtsp.Options{Transport: "UDP"})
	if err == nil {
		t.Fatal("UDP was accepted")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("UDP produced %q, which does not name the problem", err)
	}
}

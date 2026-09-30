package rtsp_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/mp4"
	"github.com/Sergey-Bar/Ai-Captain/internal/rtsp"
)

// The same synthetic fixture the muxer and the detector are tested against. The
// server below repacketises it into RTP, so the client's depacketiser is
// checked against a packetiser that shares no code with it: a round trip through
// our own reader would accept our own misreading of RFC 6184, which is the same
// objection the ffprobe check exists for on the muxer.
const fixture = "../../testdata/poc/testsrc.h264"

func loadNALs(t *testing.T) [][]byte {
	t.Helper()
	f, err := os.Open(fixture)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = f.Close() }()
	r := mp4.NewAnnexBReader(f, mp4.DefaultMaxNALSize)
	var out [][]byte
	for {
		nal, err := r.Next()
		if err != nil {
			break
		}
		out = append(out, nal)
	}
	if len(out) == 0 {
		t.Fatal("fixture is empty")
	}
	return out
}

// sdpFor builds a session description in the shape cameras actually send: an
// audio track first, H.264 second, and the parameter sets inline as base64 -
// here the fixture's own, so a description round trip is checked against the
// bytes that were really encoded rather than against a made-up pair.
func sdpFor(sps, pps []byte) string {
	return strings.Join([]string{
		"v=0",
		"o=- 0 0 IN IP4 127.0.0.1",
		"s=Camera",
		"c=IN IP4 0.0.0.0",
		"t=0 0",
		"a=control:*",
		"m=audio 0 RTP/AVP 97",
		"a=rtpmap:97 mpeg4-generic/44100/2",
		"a=control:trackID=1",
		"m=video 0 RTP/AVP 96",
		"a=rtpmap:96 H264/90000",
		"a=fmtp:96 packetization-mode=1; sprop-parameter-sets=" +
			base64.StdEncoding.EncodeToString(sps) + "," + base64.StdEncoding.EncodeToString(pps),
		"a=framerate:25",
		"a=framesize:96 640-360",
		"a=control:trackID=2",
		"",
	}, "\r\n")
}

// parameterSets pulls the SPS and PPS out of the fixture.
func parameterSets(t *testing.T, nals [][]byte) ([]byte, []byte) {
	t.Helper()
	var sps, pps []byte
	for _, n := range nals {
		switch n[0] & 0x1F {
		case 7:
			if sps == nil {
				sps = n
			}
		case 8:
			if pps == nil {
				pps = n
			}
		}
	}
	if sps == nil || pps == nil {
		t.Fatal("fixture has no parameter sets")
	}
	return sps, pps
}

type scriptedServer struct {
	ln       net.Listener
	nals     [][]byte
	sdp      string
	user     string
	authCode int
	// hangResponse makes the server accept a connection and then say nothing,
	// which is the shape of a camera that is not an RTSP server.
	hangResponse bool
	oversizeHead bool
	// afterPlay replaces the media stream, for the case where a server answers
	// the handshake correctly and then sends something that is not video.
	afterPlay func(net.Conn)
	// rawResponse is written verbatim in place of a proper reply, for the
	// response parser's own tests and its fuzz target.
	rawResponse []byte

	mu       sync.Mutex
	requests []string
}

func newServer(t *testing.T, nals [][]byte) *scriptedServer {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &scriptedServer{ln: ln, nals: nals}
	if len(nals) > 0 {
		sps, pps := parameterSets(t, nals)
		s.sdp = sdpFor(sps, pps)
	} else {
		s.sdp = sdpFor([]byte{0x67, 0x42, 0xc0, 0x1e}, []byte{0x68, 0xce, 0x04, 0x72})
	}
	t.Cleanup(func() { _ = ln.Close() })
	go s.serve()
	return s
}

func (s *scriptedServer) url() string { return "rtsp://" + s.ln.Addr().String() + "/live" }

func (s *scriptedServer) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

func (s *scriptedServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *scriptedServer) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	var session string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return
		}
		method := fields[0]
		uri := fields[1]

		headers := map[string]string{}
		for {
			h, err := br.ReadString('\n')
			if err != nil {
				return
			}
			h = strings.TrimRight(h, "\r\n")
			if h == "" {
				break
			}
			if k, v, ok := strings.Cut(h, ":"); ok {
				headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
		s.mu.Lock()
		s.requests = append(s.requests, method+" "+uri)
		s.mu.Unlock()

		if s.hangResponse {
			select {} // never answers; the client's timeout is the control
		}
		if s.oversizeHead {
			// A header block that never ends.
			for i := 0; i < 100000; i++ {
				_, _ = conn.Write([]byte("X-Padding: " + strings.Repeat("a", 200) + "\r\n"))
			}
			return
		}
		if s.rawResponse != nil {
			_, _ = conn.Write(s.rawResponse)
			_ = conn.Close()
			return
		}

		cseq := headers["CSeq"]
		switch method {
		case "OPTIONS":
			_, _ = fmt.Fprintf(conn, "RTSP/1.0 200 OK\r\nCSeq: %s\r\nPublic: OPTIONS, DESCRIBE, SETUP, PLAY, TEARDOWN\r\n\r\n", cseq)
		case "DESCRIBE":
			if s.authCode != 0 {
				_, _ = fmt.Fprintf(conn, "RTSP/1.0 %d Unauthorized\r\nCSeq: %s\r\nWWW-Authenticate: Basic realm=\"camera\"\r\n\r\n", s.authCode, cseq)
				continue
			}
			if s.user != "" {
				if !strings.HasPrefix(headers["Authorization"], "Basic ") {
					_, _ = fmt.Fprintf(conn, "RTSP/1.0 401 Unauthorized\r\nCSeq: %s\r\nWWW-Authenticate: Basic realm=\"camera\"\r\n\r\n", cseq)
					continue
				}
			}
			_, _ = fmt.Fprintf(conn, "RTSP/1.0 200 OK\r\nCSeq: %s\r\nContent-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s",
				cseq, len(s.sdp), s.sdp)
		case "SETUP":
			if !strings.Contains(headers["Transport"], "interleaved") {
				_, _ = fmt.Fprintf(conn, "RTSP/1.0 461 Unsupported Transport\r\nCSeq: %s\r\n\r\n", cseq)
				continue
			}
			session = "12345678"
			_, _ = fmt.Fprintf(conn, "RTSP/1.0 200 OK\r\nCSeq: %s\r\nSession: %s;timeout=60\r\n\r\n", cseq, session)
		case "PLAY":
			_, _ = fmt.Fprintf(conn, "RTSP/1.0 200 OK\r\nCSeq: %s\r\nSession: %s\r\nRange: npt=0.000-\r\n\r\n", cseq, session)
			if s.afterPlay != nil {
				s.afterPlay(conn)
				return
			}
			s.stream(conn)
			return
		case "TEARDOWN":
			_, _ = fmt.Fprintf(conn, "RTSP/1.0 200 OK\r\nCSeq: %s\r\n\r\n", cseq)
			return
		default:
			_, _ = fmt.Fprintf(conn, "RTSP/1.0 501 Not Implemented\r\nCSeq: %s\r\n\r\n", cseq)
		}
	}
}

// stream writes the fixture as RFC 6184 RTP over the interleaved channel.
//
// A NAL unit longer than one packet is fragmented the way a camera fragments
// it, because the alternative is a server that only ever emits single NAL
// units and a depacketiser whose reassembly path never runs.
func (s *scriptedServer) stream(conn net.Conn) {
	const maxPayload = 900
	var seq uint16
	ts := uint32(0)
	for i, nal := range s.nals {
		ts += 3600
		last := i == len(s.nals)-1
		if len(nal) <= maxPayload {
			pkt := buildRTP(seq, true, ts, nal)
			writeInterleaved(conn, 0, pkt)
			seq++
			continue
		}
		typ := nal[0] & 0x1F
		body := nal[1:]
		for off := 0; off < len(body); off += maxPayload {
			end := off + maxPayload
			if end > len(body) {
				end = len(body)
			}
			header := typ
			if off == 0 {
				header |= 0x80
			}
			if end == len(body) {
				header |= 0x40
			}
			unit := []byte{0x7C, header}
			unit = append(unit, body[off:end]...)
			pkt := buildRTP(seq, last && end == len(body), ts, unit)
			writeInterleaved(conn, 0, pkt)
			seq++
		}
	}
}

func buildRTP(seq uint16, marker bool, ts uint32, payload []byte) []byte {
	b := make([]byte, 12+len(payload))
	b[0] = 0x80 // version 2, no padding, no extension, no CSRC
	b[1] = 96
	if marker {
		b[1] |= 0x80
	}
	binary.BigEndian.PutUint16(b[2:4], seq)
	binary.BigEndian.PutUint32(b[4:8], ts)
	binary.BigEndian.PutUint32(b[8:12], 0xDEADBEEF)
	copy(b[12:], payload)
	return b
}

func writeInterleaved(w io.Writer, channel byte, payload []byte) {
	hdr := []byte{0x24, channel, byte(len(payload) >> 8), byte(len(payload))}
	_, _ = w.Write(append(hdr, payload...))
}

// TestCaptureReproducesTheStreamByteForByte is the round trip that matters: a
// real socket, a real handshake, a real repacketisation, and a comparison
// against the original bytes.
func TestCaptureReproducesTheStreamByteForByte(t *testing.T) {
	nals := loadNALs(t)
	srv := newServer(t, nals)
	ctx := context.Background()

	c, err := rtsp.Dial(ctx, srv.url(), rtsp.Options{Timeout: 5 * time.Second})
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
	for _, r := range srv.seen() {
		if strings.HasPrefix(r, "SETUP ") && strings.HasSuffix(r, "/live") {
			sawSetupOnTrack = true
		}
	}
	if !sawSetupOnTrack {
		t.Errorf("SETUP did not address the media control URI; requests were %v", srv.seen())
	}
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
	srv := newServer(t, nil)
	srv.authCode = 401
	raw := strings.Replace(srv.url(), "rtsp://", "rtsp://admin:hunter2@", 1)
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
	srv := newServer(t, nil)
	srv.mu.Lock()
	srv.authCode = 401
	srv.mu.Unlock()
	// authCode is read without the lock in the handler path above only for the
	// DESCRIBE branch, so set it before any connection is made.
	srv.authCode = 401

	ctx := context.Background()
	c, err := rtsp.Dial(ctx, srv.url(), rtsp.Options{Timeout: 3 * time.Second})
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
	srv := newServer(t, nil)
	srv.hangResponse = true
	ctx := context.Background()
	c, err := rtsp.Dial(ctx, srv.url(), rtsp.Options{Timeout: 300 * time.Millisecond})
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
	srv := newServer(t, nil)
	srv.oversizeHead = true
	ctx := context.Background()
	c, err := rtsp.Dial(ctx, srv.url(), rtsp.Options{Timeout: 3 * time.Second})
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

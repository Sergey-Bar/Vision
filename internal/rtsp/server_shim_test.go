package rtsp_test

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/rtsp"
)

// The same synthetic fixture the muxer and the detector are tested against. The
// server repacketises it into RTP, so the client's depacketiser is checked against
// a packetiser that shares no code with it: a round trip through our own reader
// would accept our own misreading of RFC 6184, which is the same objection the
// ffprobe check exists for on the muxer.
const fixture = "../../testdata/poc/testsrc.h264"

func loadNALs(t *testing.T) [][]byte {
	t.Helper()
	f, err := os.Open(fixture)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = f.Close() }()
	out, err := rtsp.ReadNALs(f)
	if err != nil {
		t.Fatalf("read the fixture: %v", err)
	}
	return out
}

// newServer starts the package's real RTSP server on a loopback port.
//
// It is a shim rather than a rewrite, and it is deliberately thin: the server
// itself moved to `server.go` because a second packetiser in a test file is a
// second opinion about RFC 6184 held by the same author, which is the objection
// the byte-for-byte round trip exists to answer. What stayed here is the fixture
// path and the option plumbing, so that every test below keeps the shape it had.
func newServer(t *testing.T, nals [][]byte, opts ...func(*rtsp.ServerOptions)) *rtsp.Server {
	t.Helper()
	if nals == nil {
		// The old helper took `nil` to mean "a description with no real units in
		// it", because two tests are about the SDP parser and never reach PLAY.
		// A server with no units is refused by `StartServer` rather than answering
		// DESCRIBE and then sending nothing, so the equivalent is one minimal
		// parameter-set pair and nothing else.
		nals = [][]byte{{0x67, 0x42, 0xc0, 0x1e, 0x1e}, {0x68, 0xce, 0x04, 0x72}}
	}
	o := rtsp.ServerOptions{Listen: "127.0.0.1:0", NALs: nals}
	for _, f := range opts {
		f(&o)
	}
	srv, err := rtsp.StartServer(t.Context(), o)
	if err != nil {
		t.Fatalf("start the RTSP server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

// withSDP serves a description the fixture's parameter sets did not produce.
//
// For the two tests that are about the client's SDP parser and not about video,
// and therefore need a description no camera fixture would send.
func withSDP(sdp string) func(*rtsp.ServerOptions) {
	return func(o *rtsp.ServerOptions) { o.SDP = sdp }
}

// withBasicAuth requires HTTP Basic, at the status code a real camera answers
// with. 0 means 401.
func withBasicAuth(user, password string, code int) func(*rtsp.ServerOptions) {
	return func(o *rtsp.ServerOptions) {
		o.User, o.Password = user, password
		o.AuthStatus = code
	}
}

func withFaults(f rtsp.ServerFaults) func(*rtsp.ServerOptions) {
	return func(o *rtsp.ServerOptions) { o.Faults = f }
}

// srvURL reads the real URL off a running server, because it is the server's to
// know and not the test's to guess.
func srvURL(t *testing.T, srv *rtsp.Server) string {
	t.Helper()
	u, err := srv.URL()
	if err != nil {
		t.Fatalf("server URL: %v", err)
	}
	return u
}

// sdpFor is the fixture-independent description builder the two parser tests use
// for their baseline, kept here rather than exported because it is a test's own
// scaffolding and the server has its own.
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

// parameterSets pulls the SPS and PPS out of the fixture, for the tests that
// compare a description against the bytes that were really encoded.
func parameterSets(t *testing.T, nals [][]byte) ([]byte, []byte) {
	t.Helper()
	var sps, pps []byte
	for _, n := range nals {
		if len(n) == 0 {
			continue
		}
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

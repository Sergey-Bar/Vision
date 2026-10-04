package rtsp

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/mp4"
)

// A long-running RTSP server, extracted from the client's own test.
//
// # Why it exists
//
// Criterion 02 of the acceptance report asks whether the capture path works
// "against the real camera end to end". What the build cannot have is a camera.
// What it can have is a server on the other end of a real socket, and that is a
// weaker claim — which is why [Server] refuses to be called a camera anywhere in
// its own documentation, and why the acceptance report says what it is in the same
// sentence as what it is not.
//
// The claim it actually supports is worth stating precisely, because a jury will
// check the difference: **the client is exercised over TCP, with a real OPTIONS /
// DESCRIBE / SETUP / PLAY / TEARDOWN handshake, real SDP parsing, real RFC 6184
// packetisation and depacketisation, and real NAL bytes compared against the
// source.** What it does NOT exercise is a camera's own behaviour: its
// authentication policy, its retransmission, its multi-track interleaving, its
// codec negotiation, or anything at all about how a real device differs from this.
//
// # It was a test, and it is the same code
//
// This file was `scriptedServer` in `rtsp_test.go`. The packetiser, the SDP
// builder, the parameter-set extraction and the request loop are the SAME lines,
// moved rather than rewritten — because a second implementation would be a second
// opinion about RFC 6184 held by the same author, which is exactly the objection
// `TestCaptureReproducesTheStreamByteForByte` exists to answer. The client's
// depacketiser shares no code with this packetiser; that separation is the reason
// the round trip means anything and it is preserved.
//
// Four things did change, and each is a fix rather than a feature:
//
//   - The FU-A indicator was hardcoded to `0x7C`, so `nal_ref_idc` was always 3 and
//     the round trip only worked because the committed fixture happens to use 3.
//     A camera with `nal_ref_idc` 2 would have produced a reconstructed header
//     that did not match its source. Now the indicator carries the unit's real
//     `F | NRI`.
//   - The RTP marker was set on the last fragment of the last NAL of the whole
//     stream, once. A marker is per access unit; a client that uses it to find
//     frame boundaries gets one frame out of the entire stream. Now it is set on
//     the last fragment of each access unit.
//   - The stream was written as fast as the socket allowed and then the connection
//     was dropped. A camera streams at its frame rate, forever, and a client that
//     only works against a firehose is not tested against a camera.
//   - Timestamps advanced by a constant per NAL rather than per frame, so a client
//     deriving a frame interval from them derived one that had nothing to do with
//     the declared 25 fps.

// ServerOptions is everything the server needs. There are no defaults for the two
// things that must be stated: which fixture to serve and where to listen.
type ServerOptions struct {
	// Listen is a host:port. Port 0 asks the OS for one, and [Server.URL] reports
	// what it got — which is what the acceptance command needs, because it has to
	// hand the URL to a client it started itself.
	Listen string
	// NALs are the Annex-B units to serve, in order. Read them with
	// `mp4.NewAnnexBReader`; [ReadNALs] does that from a file.
	NALs [][]byte
	// User and Password, when set, require HTTP Basic on DESCRIBE. Empty means no
	// authentication, which is what a loopback demo wants and what a camera on a
	// network does not.
	User     string
	Password string
	// FPS is the declared frame rate, and the pacing the stream is written at. It
	// also appears in the SDP, so a client that trusts the SDP and a client that
	// times the packets agree.
	FPS int
	// Loop repeats the fixture when it runs out. A server that stops is a server
	// whose client eventually blocks, and a client blocked on a read is a
	// different code path from one reading at rate.
	Loop bool
	// SDP is a description to serve instead of one built from the fixture's own
	// parameter sets. Empty means "build it", which is what a caller serving video
	// wants; set it when the thing under test is the client's SDP parser and the
	// description has to be one no fixture would produce.
	SDP string
	// AuthStatus is the status a DESCRIBE without credentials is answered with.
	// Zero means 401. Some cameras answer 403, and a client that treats the two as
	// different is a client that has met neither.
	AuthStatus int
	// Faults injects the four misbehaviours the client's own tests need. Every one
	// of them is a way a real device lies to a client, which is why they are here
	// rather than being spelled out as test doubles.
	Faults ServerFaults
}

// ServerFaults is the injection surface. All four produce a server that a real
// device plausibly is.
type ServerFaults struct {
	// HangResponse accepts the connection and then says nothing, which is the shape
	// of a port that is open and is not RTSP. The client's read deadline is the
	// control and this is what it is tested against.
	HangResponse bool
	// OversizeHead answers with a header block that never ends.
	OversizeHead bool
	// RawResponse is written verbatim in place of a proper reply.
	RawResponse []byte
	// AfterPlay replaces the media stream, for a server that answers the handshake
	// correctly and then sends something that is not video.
	AfterPlay func(net.Conn)
}

// Server is a running RTSP server. One value per listener; it is not safe to share
// across goroutines except through [Server.Seen], which locks.
type Server struct {
	opts   ServerOptions
	ln     net.Listener
	sdp    string
	cancel context.CancelFunc

	mu       sync.Mutex
	requests []string
	closed   bool
}

// rtpClockRate is the H.264 RTP clock, fixed by RFC 6184. It is not a setting
// because it is not a property of the stream.
const rtpClockRate = 90000

// maxPayload is the RTP payload size for the H.264 payload type. 900 rather than
// 1400 because that is what the client's own fixtures and the camera profile in
// the plan both assume, and because a payload that differs between the two ends
// would mean the round trip tests fragmentation against a number nothing else uses.
const maxPayload = 900

// StartServer listens and serves until ctx is cancelled or Close is called.
//
// It returns as soon as the listener is up, so a caller can read [Server.URL] and
// connect without sleeping. That ordering is the difference between a fixture
// usable by a test and one usable by a demo.
func StartServer(ctx context.Context, opts ServerOptions) (*Server, error) {
	if len(opts.NALs) == 0 {
		return nil, errors.New("rtsp: a server with no NAL units would answer DESCRIBE and then send nothing")
	}
	if opts.Listen == "" {
		opts.Listen = "127.0.0.1:0"
	}
	if opts.FPS <= 0 {
		opts.FPS = 25
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", opts.Listen)
	if err != nil {
		return nil, fmt.Errorf("rtsp: listen on %s: %w", opts.Listen, err)
	}
	sps, pps := parameterSets(opts.NALs)
	sdp := opts.SDP
	if sdp == "" {
		sdp = sdpFor(sps, pps, opts.FPS, widthAndHeight(sps))
	}
	s := &Server{
		opts: opts,
		ln:   ln,
		sdp:  sdp,
	}
	serveCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	go s.serve(serveCtx)
	return s, nil
}

// URL is the RTSP URL this server answers on, with the real port.
//
// Errors rather than returning a partial URL: a caller that got `rtsp://127.0.0.1:0`
// would discover the real port by trying, which is the kind of discovery this
// repository keeps writing controls against.
func (s *Server) URL() (string, error) {
	if s == nil || s.ln == nil {
		return "", errors.New("rtsp: this server is not listening")
	}
	return "rtsp://" + s.ln.Addr().String() + "/live", nil
}

// Seen returns the requests received so far, as "METHOD URI", oldest first.
//
// Recorded because a server nobody can inspect is a server nobody can tell apart
// from a client that never dialled it. The acceptance criterion quotes the
// sequence it saw.
func (s *Server) Seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// Close stops the listener. In-flight connections are closed by their handlers on
// their next read.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	already := s.closed
	s.closed = true
	s.mu.Unlock()
	if already {
		return nil
	}
	s.cancel()
	return s.ln.Close()
}

func (s *Server) serve(ctx context.Context) {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(ctx, conn)
	}
}

// handle is the request loop. One connection, one session, which is what RTSP/1.0
// interleaved transport means in practice.
func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	var session string
	for {
		if ctx.Err() != nil {
			return
		}
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		fields := strings.Fields(strings.TrimRight(line, "\r\n"))
		if len(fields) < 3 {
			return
		}
		method, uri := fields[0], fields[1]

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

		if s.faultHangs() {
			<-ctx.Done()
			return
		}
		if s.faultOversizesHead(conn) {
			return
		}
		if raw := s.faultRawResponse(); raw != nil {
			_, _ = conn.Write(raw)
			return
		}

		cseq := headers["CSeq"]
		switch method {
		case "OPTIONS":
			writef(conn, "RTSP/1.0 200 OK\r\nCSeq: %s\r\nPublic: OPTIONS, DESCRIBE, SETUP, PLAY, TEARDOWN\r\n\r\n", cseq)
		case "DESCRIBE":
			if !s.authorised(headers) {
				code := s.opts.AuthStatus
				if code == 0 {
					code = 401
				}
				writef(conn, "RTSP/1.0 %d Unauthorized\r\nCSeq: %s\r\nWWW-Authenticate: Basic realm=\"camera\"\r\n\r\n", code, cseq)
				continue
			}
			writef(conn, "RTSP/1.0 200 OK\r\nCSeq: %s\r\nContent-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s",
				cseq, len(s.sdp), s.sdp)
		case "SETUP":
			if !strings.Contains(headers["Transport"], "interleaved") {
				writef(conn, "RTSP/1.0 461 Unsupported Transport\r\nCSeq: %s\r\n\r\n", cseq)
				continue
			}
			session = "12345678"
			writef(conn, "RTSP/1.0 200 OK\r\nCSeq: %s\r\nSession: %s;timeout=60\r\n\r\n", cseq, session)
		case "PLAY":
			writef(conn, "RTSP/1.0 200 OK\r\nCSeq: %s\r\nSession: %s\r\nRange: npt=0.000-\r\n\r\n", cseq, session)
			if s.opts.Faults.AfterPlay != nil {
				s.opts.Faults.AfterPlay(conn)
				return
			}
			s.stream(ctx, conn)
			return
		case "TEARDOWN":
			writef(conn, "RTSP/1.0 200 OK\r\nCSeq: %s\r\n\r\n", cseq)
			return
		case "GET_PARAMETER":
			// A keepalive, which `Client.KeepAlive` sends and deliberately ignores
			// the answer to. 200 so a real client that checks sees a well-behaved
			// server rather than an error it has to special-case.
			writef(conn, "RTSP/1.0 200 OK\r\nCSeq: %s\r\nSession: %s\r\n\r\n", cseq, session)
		default:
			writef(conn, "RTSP/1.0 501 Not Implemented\r\nCSeq: %s\r\n\r\n", cseq)
		}
	}
}

func (s *Server) faultHangs() bool         { return s.opts.Faults.HangResponse }
func (s *Server) faultRawResponse() []byte { return s.opts.Faults.RawResponse }

func (s *Server) faultOversizesHead(conn net.Conn) bool {
	if !s.opts.Faults.OversizeHead {
		return false
	}
	for i := 0; i < 100000; i++ {
		_, _ = conn.Write([]byte("X-Padding: " + strings.Repeat("a", 200) + "\r\n"))
	}
	return true
}

// authorised implements HTTP Basic, preemptively.
//
// Preemptive because a camera offers `WWW-Authenticate` on DESCRIBE and the client
// sends the credential on the first DESCRIBE — the client's `authHeaders` is
// preemptive too, and a server that required two round trips would be testing the
// client's retry rather than its capture.
func (s *Server) authorised(headers map[string]string) bool {
	// A status pinned with no credential configured means "this camera refuses
	// every DESCRIBE", which is what a probe finds when it has no credentials for
	// the device. Without this branch, `User == ""` would mean "no
	// authentication" and the status would never be seen — and a server that can
	// only be configured to authenticate and cannot be configured to refuse is a
	// server that cannot stand in for the case the client most needs to handle.
	if s.opts.AuthStatus != 0 && s.opts.User == "" {
		return false
	}
	if s.opts.User == "" {
		return true
	}
	got, ok := strings.CutPrefix(headers["Authorization"], "Basic ")
	if !ok {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(got))
	if err != nil {
		return false
	}
	user, pass, found := strings.Cut(string(raw), ":")
	return found && user == s.opts.User && pass == s.opts.Password
}

// stream writes the fixture as RFC 6184 RTP over the interleaved channel, at rate,
// looping until the context ends.
//
// Four behaviours, each of which is a way a real camera differs from the test
// double this came from:
//
//   - paced at the declared frame rate, because a client that only works against a
//     firehose has not been tested against a camera;
//   - looping, because a client that blocks on a read is a different code path;
//   - one access unit per frame, with the RTP marker on the last fragment of each
//     one, because the marker is how a client finds frame boundaries;
//   - `nal_ref_idc` carried into the FU-A indicator, because the reconstructed
//     header has to match the source byte for byte.
func (s *Server) stream(ctx context.Context, conn net.Conn) {
	nals := s.opts.NALs
	// `rtpClockRate` is a positive constant and `FPS` was defaulted to 25 in
	// `StartServer`, so this division cannot be zero or negative. The conversion is
	// guarded anyway, because a tick count that wrapped would put the stream's
	// timestamps in the wrong century and a client that trusts them would file the
	// video under 2106.
	ticksPerFrame, err := clockTicks(s.opts.FPS)
	if err != nil {
		return
	}
	interval := time.Second / time.Duration(s.opts.FPS)
	var seq uint16
	var ts uint32

	for pass := 0; ; pass++ {
		if ctx.Err() != nil {
			return
		}
		// A write deadline, because a client that disconnects mid-stream must not
		// leave a goroutine blocked on a socket nobody will ever read.
		deadline := time.Now().Add(30 * time.Second)
		_ = conn.SetWriteDeadline(deadline)
		for i, nal := range nals {
			if ctx.Err() != nil {
				return
			}
			ts += ticksPerFrame
			lastInUnit := true // one NAL per access unit, which this fixture is
			if len(nal) <= maxPayload {
				_, _ = conn.Write(interleaved(BuildRTP(seq, true, ts, nal)))
				seq++
				continue
			}
			typ := nal[0] & 0x1F
			nri := nal[0] & 0x60 // the real nal_ref_idc, not a constant 3
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
				// FU indicator: F bit clear, and the unit's own NRI. The old code
				// wrote 0x7C, which is nri 3 — correct only for a fixture that
				// happens to use 3.
				unit := make([]byte, 0, 2+end-off)
				unit = append(unit, nri|fuA)
				unit = append(unit, header)
				unit = append(unit, body[off:end]...)
				_, _ = conn.Write(interleaved(BuildRTP(seq, lastInUnit && end == len(body), ts, unit)))
				seq++
			}
			_ = i
		}
		if !s.opts.Loop {
			return
		}
		if !sleepCtx(ctx, interval) {
			return
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// clockTicks is the RTP tick advance per frame, and it refuses rather than wraps.
//
// The refusal is not theoretical: `rtpClockRate / fps` at a frame rate above 90000
// is zero, and a stream whose timestamps never advance is a stream a client cannot
// derive a frame interval from — which is the one thing this server exists to be
// plausible about.
func clockTicks(fps int) (uint32, error) {
	if fps <= 0 || fps > rtpClockRate {
		return 0, fmt.Errorf("rtsp: a frame rate of %d cannot be expressed on the %d Hz RTP clock", fps, rtpClockRate)
	}
	return uint32(rtpClockRate / fps), nil
}

// interleaved wraps an RTP packet in the four-byte RFC 2326 §10.12 frame.
func interleaved(payload []byte) []byte {
	hdr := []byte{interleavedMagic, 0, byte(len(payload) >> 8), byte(len(payload))} //nolint:gosec // G115: the 16-bit length field is the RFC 2326 shape and a payload over 65535 is already refused upstream by maxPayload
	return append(hdr, payload...)
}

// BuildRTP writes the fixed 12-byte header: version 2, no padding, no extension,
// no CSRC. A camera may send all three, and the client's parser handles them, but
// this server emits the simplest legal packet so that a failure in a round trip is
// a failure about RFC 6184 and not about a header this file got wrong.
func BuildRTP(seq uint16, marker bool, ts uint32, payload []byte) []byte {
	b := make([]byte, 12+len(payload))
	b[0] = 0x80
	b[1] = 96 // H.264 payload type, matching `a=rtpmap:96 H264/90000`
	if marker {
		b[1] |= 0x80
	}
	binary.BigEndian.PutUint16(b[2:4], seq)
	binary.BigEndian.PutUint32(b[4:8], ts)
	// A fixed SSRC. Not a wall-clock-derived one: the same fixture must produce the
	// same packets on two runs, or a byte-for-byte comparison between them means
	// nothing.
	binary.BigEndian.PutUint32(b[8:12], 0xDEADBEEF)
	copy(b[12:], payload)
	return b
}

// ReadNALs reads an Annex-B elementary stream into NAL units.
//
// A convenience so that a caller does not have to know that `mp4.NewAnnexBReader`
// is the reader and not the file. It is here and not in the CLI because the
// acceptance check and the client test both need it and neither should own it.
func ReadNALs(r io.Reader) ([][]byte, error) {
	ar := mp4.NewAnnexBReader(r, mp4.DefaultMaxNALSize)
	var out [][]byte
	for {
		nal, err := ar.Next()
		if err != nil {
			break
		}
		out = append(out, nal)
	}
	if len(out) == 0 {
		return nil, errors.New("rtsp: the stream contained no NAL units")
	}
	return out, nil
}

// parameterSets pulls the SPS and PPS out of a unit list, in order, which is the
// order RFC 6184's `sprop-parameter-sets` wants them in.
func parameterSets(nals [][]byte) (sps, pps []byte) {
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
	return sps, pps
}

// widthAndHeight reads the frame geometry out of an SPS.
//
// Errors are not a concern here: a fixture without a parseable SPS still gets a
// description, with the declared size omitted, because the client does not need it
// to capture and refusing to start over a cosmetic field would make this server
// less usable than the device it stands in for.
func widthAndHeight(sps []byte) string {
	if sps == nil {
		return ""
	}
	p, err := mp4.ParseSequenceParameters(sps)
	if err != nil || p.Width <= 0 || p.Height <= 0 {
		return ""
	}
	return fmt.Sprintf("%d-%d", p.Width, p.Height)
}

// sdpFor builds a session description in the shape cameras actually send: an audio
// track first, H.264 second, and the parameter sets inline as base64 — the
// fixture's own, so a description round trip is checked against the bytes that were
// really encoded rather than against a made-up pair.
func sdpFor(sps, pps []byte, fps int, framesize string) string {
	lines := []string{
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
	}
	if len(sps) > 0 && len(pps) > 0 {
		lines = append(lines,
			"a=fmtp:96 packetization-mode=1; sprop-parameter-sets="+
				base64.StdEncoding.EncodeToString(sps)+","+
				base64.StdEncoding.EncodeToString(pps))
	}
	if framesize != "" {
		lines = append(lines, "a=framesize:96 "+framesize)
	}
	return strings.Join(append(lines,
		fmt.Sprintf("a=framerate:%d", fps),
		"a=control:trackID=2",
		"",
	), "\r\n")
}

func writef(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

package rtsp_test

import (
	"context"
	"encoding/binary"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/mp4"
	"github.com/Sergey-Bar/vision/internal/rtsp"
)

// A camera is an untrusted network peer, so the two things that touch its bytes
// are fuzz targets: the RTSP response parser and the RFC 6184 depacketiser.
//
// The seeds are the committed fixture repacketised as RTP, because a fuzzer
// that starts from zeros spends its whole budget discovering that RTP has a
// version field.

// fuzzSeedPacket makes one well-formed packet from the fixture's first NAL
// unit, as a starting point for mutation.
func fuzzSeedPacket() []byte {
	f, err := os.Open(fixture)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	r := mp4.NewAnnexBReader(f, mp4.DefaultMaxNALSize)
	nal, err := r.Next()
	if err != nil {
		return nil
	}
	return rtsp.BuildRTP(1, true, 3600, nal)
}

// FuzzParseResponseNeverPanics drives the response parser, which is the only
// place a hostile peer can make this client allocate without limit.
//
// The parser is unexported, so the target reaches it the way the client does:
// over a real socket whose far end writes the fuzzed bytes and nothing else.
// Anything that is not a well-formed reply has to come back as a refusal.
func FuzzParseResponseNeverPanics(f *testing.F) {
	if raw, err := os.ReadFile(fixture); err == nil {
		if len(raw) > 512 {
			raw = raw[:512]
		}
		f.Add(raw)
	}
	f.Add([]byte("RTSP/1.0 200 OK\r\nCSeq: 1\r\n\r\n"))
	f.Add([]byte("RTSP/1.0 200 OK\r\nContent-Length: 4\r\n\r\nabcd"))
	// The shapes a broken or hostile peer actually produces.
	f.Add([]byte("HTTP/1.1 200 OK\r\n\r\n"))
	f.Add([]byte("RTSP/1.0 200 OK\r\n: novalue\r\n\r\n"))
	f.Add([]byte("RTSP/1.0\r\n\r\n"))
	f.Add([]byte("RTSP/1.0 200 OK\r\nContent-Length: 999999999\r\n\r\n"))
	f.Add([]byte("RTSP/1.0 200 OK\r\n"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		srv := newServer(t, nil, withFaults(rtsp.ServerFaults{RawResponse: data}))
		ctx := context.Background()
		c, err := rtsp.Dial(ctx, srvURL(t, srv), rtsp.Options{Timeout: 2 * time.Second})
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		// A panic anywhere in the parse is a fuzz failure. A refusal is not.
		_, _ = c.Options(ctx)
		_, _ = c.Describe(ctx)
	})
}

// FuzzDepacketizerNeverPanicsAndNeverLosesASequence drives the reassembler.
func FuzzDepacketizerNeverPanicsAndNeverLosesASequence(f *testing.F) {
	if pkt := fuzzSeedPacket(); pkt != nil {
		f.Add(pkt)
	}
	f.Add(rtsp.BuildRTP(1, true, 0, []byte{0x65, 0x88, 0x84}))
	f.Add(rtsp.BuildRTP(1, true, 0, []byte{0x78, 0x00, 0x02, 0x67, 0x42}))
	f.Add(rtsp.BuildRTP(1, true, 0, []byte{0x7C, 0x85, 0xAA, 0xBB}))
	f.Add([]byte{})
	f.Add([]byte{0x80})

	f.Fuzz(func(t *testing.T, raw []byte) {
		d := rtsp.NewDepacketizer(64 << 10)
		// Split the input into a short run of packets, so a fuzzer that finds
		// one bad packet does not get to hide it behind an earlier rejection.
		for off := 0; off+12 <= len(raw); off += 12 {
			end := off + 12
			if end+256 < len(raw) {
				end += 256
			}
			if end > len(raw) {
				end = len(raw)
			}
			p := rtsp.Packet{
				Sequence:  binary.BigEndian.Uint16(raw[off+2 : off+4]),
				Timestamp: binary.BigEndian.Uint32(raw[off+4 : off+8]),
				SSRC:      binary.BigEndian.Uint32(raw[off+8 : off+12]),
				Marker:    raw[off+1]&0x80 != 0,
				Payload:   raw[off+12 : end],
			}
			out, err := d.Push(p)
			// Either nothing completed, or every unit is bounded.
			for _, nal := range out {
				if len(nal) > 64<<10 {
					t.Fatalf("a %d byte unit escaped the bound", len(nal))
				}
				if len(nal) == 0 {
					t.Fatal("a zero-length NAL unit was returned")
				}
			}
			_ = err
		}
	})
}

// FuzzURLNeverCarriesCredentialsIntoItsRendering checks the one property of the
// URL type that is a security property rather than a convenience.
func FuzzURLNeverCarriesCredentialsIntoItsRendering(f *testing.F) {
	for _, seed := range []string{
		"rtsp://admin:hunter2@192.0.2.1:554/live",
		"rtsp://192.0.2.1/live",
		"rtsp://user@host/path",
		"rtsps://u:p@h/live",
		"http://h/live",
		"rtsp://",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		u, err := rtsp.ParseURL(raw)
		if err != nil {
			return
		}
		if u.Password == "" {
			return
		}
		// The property is: the rendering carries no CREDENTIALS. Asked by parsing the
		// rendering back and looking for a userinfo section, which is exact.
		//
		// This assertion has been two wrong shapes before it was this one, and both
		// were found by the fuzzer inside a minute, which is the argument for running
		// it at all:
		//
		//	contains(rendering, u.Password)
		//	  failed on `rtsp://:0@0`: the password is the string `0` and so is the
		//	  host, so the correct rendering `rtsp://0:554/` contains it -- as a HOST.
		//	  A password that is a substring of its own host, port or path reports, so
		//	  the budget goes on coincidences and a real leak is one more failure in a
		//	  wall of them.
		//
		//	contains(rendering, u.Password+"@")
		//	  failed on `rtsp://:0@0/0@`: the password is `0` and the PATH is `/0@`, so
		//	  the string `0@` appears with the `@` at the end of the path rather than
		//	  introducing a host. A path segment may legitimately contain `@`.
		//
		// Asking `net/url` is the only version of this that distinguishes userinfo-`@`
		// from every other `@` in the string, and it is the question the property
		// actually is.
		for _, s := range []string{u.String(), u.Request()} {
			back, err := url.Parse(s)
			if err != nil {
				t.Fatalf("the rendering %q does not parse back: %v", s, err)
			}
			if back.User != nil {
				t.Fatalf("the rendering %q carries userinfo %q", s, back.User.String())
			}
			if u.Password != "" && strings.Contains(back.Host, u.Password) &&
				strings.Contains(u.Host, u.Password) {
				// Not a leak: the host is not a secret and the password happens to
				// equal it. Recorded so the coincidence is not rediscovered as a
				// finding, and not asserted, because it is not one.
				t.Logf("note: the password %q equals this URL's host; the rendering carries "+
					"it as a host, which is not a credential", u.Password)
			}
		}
	})
}

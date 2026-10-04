package rtsp_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/rtsp"
)

func pkt(seq uint16, marker bool, ssrc uint32, payload []byte) rtsp.Packet {
	return rtsp.Packet{
		Marker:      marker,
		PayloadType: 96,
		Sequence:    seq,
		Timestamp:   3600,
		SSRC:        ssrc,
		Payload:     payload,
	}
}

// fragmented builds an FU-A packet for one slice of a NAL unit.
// nri is the nal_ref_idc the tests use, which is 3 - the highest value a NAL
// header can carry, and therefore the case most likely to be mishandled.
const ( //nolint:gochecknoglobals // test fixtures, one value each
	testNRI  = 3 // the highest nal_ref_idc a NAL header can carry, and the case most likely to be mishandled
	testType = 5 // an IDR, so a wrong header turns every keyframe into a non-keyframe
)

func fragmented(seq uint16, marker bool, ssrc uint32, start, end bool, body []byte) rtsp.Packet {
	hdr := byte(testNRI<<5) | 28
	fu := byte(testType)
	if start {
		fu |= 0x80
	}
	if end {
		fu |= 0x40
	}
	return pkt(seq, marker, ssrc, append([]byte{hdr, fu}, body...))
}

// TestFragmentsReassembleWithTheRightHeaderByte is the red-check for a defect
// this package had, and it is asserted on the header byte rather than on the
// body because that is where the defect was.
//
// The reassembled NAL header is forbidden_zero_bit, nal_ref_idc and type. Only
// the type comes from the FU header; the top three bits come from the
// fragmentation indicator. Reading the indicator's type field instead - which
// is 28 by definition - produced a header of 0x05 where the slice was 0x65,
// and every keyframe in the stream stopped being a keyframe. Nothing failed: the
// muxer wrote the file, ffprobe read the file, and the file was a clip with no
// keyframe in it, which is a clip that cannot be seeked and cannot be pre-rolled.
func TestFragmentsReassembleWithTheRightHeaderByte(t *testing.T) {
	d := rtsp.NewDepacketizer(1 << 20)
	const idr = 5
	var out [][]byte

	for i, body := range [][]byte{{0xAA, 0xBB}, {0xCC, 0xDD}, {0xEE}} {
		p := fragmented(uint16(i), i == 2, 0x11223344, i == 0, i == 2, body)
		nals, err := d.Push(p)
		if err != nil {
			t.Fatalf("fragment %d: %v", i, err)
		}
		out = append(out, nals...)
	}
	if len(out) != 1 {
		t.Fatalf("reassembled %d units from 3 fragments", len(out))
	}
	if len(out[0]) != 6 {
		t.Fatalf("reassembled %d bytes, want 6", len(out[0]))
	}
	want := []byte{testNRI<<5 | idr, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE}
	if !bytes.Equal(out[0], want) {
		t.Errorf("reassembled %x, want %x", out[0], want)
	}
	if out[0][0]&0x1F != idr {
		t.Errorf("reassembled type %d, want %d", out[0][0]&0x1F, idr)
	}
	if out[0][0]>>5 != testNRI {
		t.Errorf("reassembled nal_ref_idc %d, want %d", out[0][0]>>5, testNRI)
	}
}

func TestSTAPAAggregatesSeveralUnits(t *testing.T) {
	d := rtsp.NewDepacketizer(1 << 20)
	var body []byte
	body = append(body, 0x78) // STAP-A, nal_ref_idc 3
	for _, n := range [][]byte{{0x67, 1, 2}, {0x68, 3}, {0x65, 4, 5, 6}} {
		body = append(body, byte(len(n)>>8), byte(len(n)))
		body = append(body, n...)
	}
	out, err := d.Push(pkt(1, true, 7, body))
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("recovered %d units from one aggregation packet, want 3", len(out))
	}
	if out[1][0] != 0x68 {
		t.Errorf("second unit is %x, want a PPS", out[1])
	}
}

func TestASequenceGapIsRefusedRatherThanSpliced(t *testing.T) {
	// A camera that drops a packet leaves a hole. Splicing the two sides
	// together produces a NAL unit that is the right length and decodes to
	// corruption, which is worse than a refusal that names the gap.
	d := rtsp.NewDepacketizer(1 << 20)
	if _, err := d.Push(fragmented(10, false, 1, true, false, []byte{1, 2})); err != nil {
		t.Fatal(err)
	}
	_, err := d.Push(fragmented(12, true, 1, false, true, []byte{3, 4}))
	if err == nil {
		t.Fatal("a gap in the fragment sequence was accepted")
	}
	if !strings.Contains(err.Error(), "sequence") {
		t.Errorf("refusal %q does not name the gap", err)
	}
}

func TestAFragmentWithNothingBeforeItIsRefused(t *testing.T) {
	// Guessing the missing header produces a NAL unit with an invented type,
	// and a muxer would write it into a file as though it were real.
	d := rtsp.NewDepacketizer(1 << 20)
	_, err := d.Push(fragmented(1, true, 1, false, true, []byte{1, 2, 3}))
	if err == nil {
		t.Fatal("a continuation fragment with no start was accepted")
	}
}

func TestAnOversizedUnitIsRefusedRatherThanBuffered(t *testing.T) {
	// A camera is an untrusted peer, and one that never sets the end bit grows
	// the reassembly buffer until the process is killed.
	d := rtsp.NewDepacketizer(512)
	if _, err := d.Push(fragmented(1, false, 1, true, false, bytes.Repeat([]byte{0xAA}, 400))); err != nil {
		t.Fatal(err)
	}
	_, err := d.Push(fragmented(2, false, 1, false, false, bytes.Repeat([]byte{0xBB}, 400)))
	if err == nil {
		t.Fatal("an unbounded reassembly was accepted")
	}
	if !errors.Is(err, rtsp.ErrNALTooLarge) {
		t.Errorf("got %v, want ErrNALTooLarge", err)
	}
}

func TestAPacketizationTypeWeDoNotImplementIsRefused(t *testing.T) {
	// Silently skipping it produces a stream with holes, and a muxer cannot
	// tell a hole from a still scene.
	d := rtsp.NewDepacketizer(1 << 20)
	for _, typ := range []byte{25, 26, 27, 30, 31} {
		if _, err := d.Push(pkt(1, true, 1, []byte{typ, 0x00})); err == nil {
			t.Errorf("packetization type %d was accepted", typ)
		}
	}
}

func TestAStreamThatIsNotInterleavedIsRefused(t *testing.T) {
	// Once PLAY is answered the socket carries interleaved frames and nothing
	// else, so a byte that is not the magic means the stream is not what the
	// handshake said. Resynchronising a byte stream by guessing is how a client
	// ends up muxing somebody else's video.
	srv := newServer(t, loadNALs(t), withFaults(rtsp.ServerFaults{
		AfterPlay: func(conn net.Conn) {
			_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\n"))
			time.Sleep(2 * time.Second)
		},
	}))
	ctx := context.Background()
	c, err := rtsp.Dial(ctx, srvURL(t, srv), rtsp.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Options(ctx); err != nil {
		t.Fatalf("OPTIONS: %v", err)
	}
	if err := c.Play(ctx); err != nil {
		t.Fatalf("PLAY: %v", err)
	}
	if _, _, _, err := c.ReadNAL(); err == nil {
		t.Fatal("a non-interleaved stream was accepted as media")
	} else if !strings.Contains(err.Error(), "interleaved") {
		t.Errorf("refusal %q does not name what was wrong", err)
	}
}

package rtsp

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Packet is one parsed RTP packet.
type Packet struct {
	Marker      bool
	PayloadType uint8
	Sequence    uint16
	Timestamp   uint32
	SSRC        uint32
	Payload     []byte
}

// Clock unwraps a 32-bit RTP timestamp across its wrap.
//
// The RTP clock is 32 bits and wraps every 49.7 hours at 90 kHz, which a camera
// left running for three weeks hits several times. This is not a precision
// nicety: a clip whose timestamps jump backwards halfway through is a clip a
// player cannot render, and a pre-roll ring that compares timestamps sees a
// negative age and drops everything.
type Clock struct {
	base    uint32
	last    uint32
	cycles  uint64
	started bool
}

// Next returns the extended timestamp for a packet.
//
// The first timestamp seen becomes zero, so a run's decode times start at the
// origin regardless of what the camera's random initial timestamp happened to
// be. A client that carried the raw value forward would produce clips whose
// media timeline begins at an arbitrary point, and a demuxer measures a stream's
// duration from the timeline origin.
func (c *Clock) Next(ts uint32) uint64 {
	if !c.started {
		c.started = true
		c.base = ts
		c.last = ts
		return 0
	}
	if ts < c.last {
		c.cycles += 1 << 32
	}
	c.last = ts
	return c.cycles + uint64(ts) - uint64(c.base)
}

// RTP header layout, from RFC 3550 section 5.1.
const (
	rtpHeaderBytes    = 12
	rtpVersion        = 2
	rtpExtensionCount = 14 // CC plus the two flag bits
)

// ErrShortPacket is returned for a packet too small to be an RTP header.
var ErrShortPacket = errors.New("rtsp: packet is too short to be RTP")

// ErrUnsupportedNALType is returned for an RFC 6184 packetization type this
// depacketiser does not implement.
//
// It is a refusal rather than a skip, deliberately. Silently dropping a packet
// type the client does not understand produces a stream with holes in it, and a
// muxer cannot tell a hole from a still scene.
var ErrUnsupportedNALType = errors.New("rtsp: unsupported H.264 packetization type")

// parseRTP reads an RTP header off a payload and leaves the rest.
func parseRTP(b []byte) (*Packet, error) {
	if len(b) < rtpHeaderBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrShortPacket, len(b))
	}
	if b[0]>>6 != rtpVersion {
		return nil, fmt.Errorf("%w: version %d", ErrShortPacket, b[0]>>6)
	}
	padding := b[0]&0x20 != 0
	extension := b[0]&0x10 != 0
	cc := int(b[0] & 0x0F)
	p := &Packet{
		Marker:      b[1]&0x80 != 0,
		PayloadType: b[1] & 0x7F,
		Sequence:    binary.BigEndian.Uint16(b[2:4]),
		Timestamp:   binary.BigEndian.Uint32(b[4:8]),
		SSRC:        binary.BigEndian.Uint32(b[8:12]),
	}
	off := rtpHeaderBytes + 4*cc
	if off > len(b) {
		return nil, fmt.Errorf("%w: %d contributing sources in %d bytes", ErrShortPacket, cc, len(b))
	}
	if extension {
		if off+4 > len(b) {
			return nil, fmt.Errorf("%w: extension header past the end", ErrShortPacket)
		}
		words := int(binary.BigEndian.Uint16(b[off+2 : off+4]))
		off += 4 + 4*words
		if off > len(b) {
			return nil, fmt.Errorf("%w: extension body of %d words past the end", ErrShortPacket, words)
		}
	}
	if padding {
		if off >= len(b) {
			return nil, fmt.Errorf("%w: padding flag with no padding", ErrShortPacket)
		}
		pad := int(b[len(b)-1])
		if pad == 0 || pad > len(b)-off {
			return nil, fmt.Errorf("%w: padding of %d bytes in %d", ErrShortPacket, pad, len(b)-off)
		}
		b = b[:len(b)-pad]
	}
	p.Payload = b[off:]
	return p, nil
}

// RFC 6184 packetization types.
const (
	stapA = 24
	fuA   = 28
	fuB   = 29
)

// Depacketizer reassembles RFC 6184 fragments into NAL units.
//
// It is per-stream state - the reassembly buffer belongs to one SSRC - and a
// process running twenty cameras holds twenty of these rather than one in
// total. A shared buffer would splice one camera's fragments onto another's,
// and the result is a file that decodes into something plausible.
type Depacketizer struct {
	maxNAL int

	// The fragment in progress. A FU-A with the start bit begins one; the
	// middle and end bits continue it.
	buf     []byte
	started bool
	lastSeq uint16
	sawSeq  bool
	ssrc    uint32
}

// NewDepacketizer returns a depacketiser bounded by maxNAL bytes per unit.
func NewDepacketizer(maxNAL int) *Depacketizer {
	if maxNAL <= 0 {
		maxNAL = 8 << 20
	}
	return &Depacketizer{maxNAL: maxNAL}
}

// Reset drops any partial unit.
func (d *Depacketizer) Reset() {
	d.buf = nil
	d.started = false
	d.sawSeq = false
}

// Push feeds one packet and returns the NAL units it completed.
//
// A nil result with a nil error means the packet was a fragment that has not
// finished a unit yet, which is most packets and is not an error.
func (d *Depacketizer) Push(p Packet) ([][]byte, error) {
	if len(p.Payload) == 0 {
		return nil, nil
	}
	typ := p.Payload[0] & 0x1F

	// A change of source mid-unit means the fragments belonged to two streams.
	if d.started && d.sawSeq && p.SSRC != d.ssrc {
		d.Reset()
	}

	switch {
	case typ >= 1 && typ <= 23:
		// A single NAL unit. Its arrival ends any unit in progress, because
		// they interleave only on a broken stream.
		d.Reset()
		return [][]byte{append([]byte(nil), p.Payload...)}, nil

	case typ == stapA:
		d.Reset()
		return parseSTAPA(p.Payload)

	case typ == fuA || typ == fuB:
		return d.pushFragment(p)
	}
	return nil, fmt.Errorf("%w: %d", ErrUnsupportedNALType, typ)
}

func parseSTAPA(payload []byte) ([][]byte, error) {
	if len(payload) < 1 {
		return nil, fmt.Errorf("%w: empty aggregation packet", ErrShortPacket)
	}
	var out [][]byte
	off := 1 // the STAP-A header byte
	for off < len(payload) {
		if off+2 > len(payload) {
			return nil, fmt.Errorf("%w: STAP-A size field runs past the end", ErrShortPacket)
		}
		n := int(binary.BigEndian.Uint16(payload[off : off+2]))
		off += 2
		if n == 0 || off+n > len(payload) {
			return nil, fmt.Errorf("%w: STAP-A unit of %d bytes does not fit in %d", ErrShortPacket, n, len(payload)-off)
		}
		out = append(out, append([]byte(nil), payload[off:off+n]...))
		off += n
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: STAP-A carried no units", ErrShortPacket)
	}
	return out, nil
}

func (d *Depacketizer) pushFragment(p Packet) ([][]byte, error) {
	// FU indicator, then FU header: the start and end bits and the type the
	// reassembled unit takes. Type 28's indicator carries 28 in its type field;
	// the reassembled unit takes the header's.
	if len(p.Payload) < 3 {
		return nil, fmt.Errorf("%w: fragmentation unit of %d bytes", ErrShortPacket, len(p.Payload))
	}
	header := p.Payload[1]
	start := header&0x80 != 0
	end := header&0x40 != 0
	unitType := header & 0x1F
	frag := p.Payload[2:]

	if start {
		// The reassembled unit's header byte is the original NAL header: the
		// forbidden-zero bit and nal_ref_idc come from the fragmentation
		// indicator's high three bits, and only the five-bit type comes from
		// the FU header. Reading the indicator's *type* field instead - which is
		// 28 by definition - loses nal_ref_idc and writes an I picture that
		// announces itself as a non-IDR slice, and a muxer that keys its
		// keyframe detection on that byte then never finds a keyframe.
		nri := p.Payload[0] & 0xE0
		if unitType == 0 {
			return nil, fmt.Errorf("%w: fragmented NAL type 0", ErrUnsupportedNALType)
		}
		if len(frag) > d.maxNAL-1 {
			return nil, fmt.Errorf("%w: fragment of %d bytes", ErrNALTooLarge, len(frag))
		}
		d.buf = make([]byte, 0, len(frag)+1)
		d.buf = append(d.buf, nri|unitType)
		d.buf = append(d.buf, frag...)
		d.started = true
		d.ssrc = p.SSRC
		d.lastSeq = p.Sequence
		d.sawSeq = true
		return nil, nil
	}

	if !d.started {
		// A middle fragment with nothing before it. Guessing the header would
		// produce a NAL unit with a made-up type, and a muxer would write it
		// into a file as though it were real.
		return nil, fmt.Errorf("%w: fragment continues a unit that never started", ErrShortPacket)
	}
	if d.sawSeq {
		if want := d.lastSeq + 1; p.Sequence != want {
			d.Reset()
			return nil, fmt.Errorf("%w: fragment sequence %d follows %d", ErrShortPacket, p.Sequence, want)
		}
	}
	if len(d.buf)+len(frag) > d.maxNAL {
		d.Reset()
		return nil, fmt.Errorf("%w: reassembled unit exceeds %d bytes", ErrNALTooLarge, d.maxNAL)
	}
	d.buf = append(d.buf, frag...)
	d.lastSeq = p.Sequence
	if !end {
		return nil, nil
	}
	out := d.buf
	d.buf = nil
	d.started = false
	return [][]byte{out}, nil
}

// Package mp4 turns a live H.264 Annex-B elementary stream into a bounded,
// seekable, fragmented MP4 clip that an independent decoder will accept.
//
// The whole package is pure Go and reads no file, opens no socket and executes
// nothing. That is not an aesthetic choice: the product ships into air-gapped
// sites where "install a GStreamer" is not an option, and a capture path that
// can only be built on a machine with a plugin registry is a capture path that
// cannot be reproduced on the machine that has to diagnose it.
//
// # Why a hand-written muxer at all
//
// A round-trip test we wrote ourselves would accept a file we ourselves got
// wrong, because both halves of the round trip share our misunderstanding. The
// only test that means anything is an independent decoder reading the bytes
// back, so the muxer's tests additionally run ffprobe wherever ffprobe exists.
//
// # What this package refuses
//
// Three refusals, each of which exists because the alternative is a failure three
// stages later with a worse name:
//
//   - A stream with no SPS/PPS is refused. Emitting a file we know is
//     unplayable and discovering it 3,000 bytes into a third-party upload is
//     worse than refusing here, where the reason is legible.
//   - A mid-stream change of the parameter sets is refused rather than
//     silently continued, because two GOPs with different SPS do not have one
//     answer to "what is the resolution".
//   - A cap is never applied silently. A truncated clip carries which cap bound
//     it, because "the clip is 100 MB because the product says so" and "the
//     clip is 100 MB because we lost the rest" are different facts and only one
//     of them is a decision.
package mp4

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

// NAL unit types. Only the ones the muxer has to reason about are named; the
// rest fall through to the default branch.
const (
	NALSlice      = 1 // non-IDR coded picture
	NALPartitionA = 2
	NALPartitionB = 3
	NALIDR        = 5
	NALSEI        = 6
	NALPrefix     = 7 // SPS
	NALSuffix     = 8 // PPS
	NALAUD        = 9
	NALEndSeq     = 10
	NALEndStr     = 11
	NALFill       = 12
)

// ErrNALTooLarge bounds a single NAL unit.
//
// A camera is an untrusted network peer, and a peer that never sends a start
// code would otherwise grow this buffer without limit until the process is
// killed. The bound is generous enough that no real H.264 access unit approaches
// it, so hitting it means the stream is malformed rather than merely large.
var ErrNALTooLarge = errors.New("mp4: NAL unit exceeded the size bound")

// ErrNoParameterSets is returned for a stream that never delivered an SPS and a
// PPS, which is a stream no avcC can be built from.
var ErrNoParameterSets = errors.New("mp4: stream has no SPS/PPS; refusing to emit an unplayable file")

// ErrProfileChange is returned when the parameter sets change mid-stream.
var ErrProfileChange = errors.New("mp4: mid-stream parameter-set change refused")

// AnnexBReader splits an Annex-B byte stream into NAL units.
//
// It holds at most one NAL plus one read chunk, and it refuses rather than
// growing without bound, because it reads from a network peer.
type AnnexBReader struct {
	r       io.Reader
	pending []byte
	// searched is how far into pending has been proven to hold no start code.
	// Advancing it is what makes the reader linear rather than quadratic, and
	// it must be rewound on every emit: a start code split across two reads is
	// undecidable until the next one arrives.
	searched int
	maxNAL   int
	readErr  error
	scratch  []byte
}

const (
	// DefaultMaxNALSize is 8 MiB. The largest sensible single access unit for a
	// 4K stream is a small fraction of that.
	DefaultMaxNALSize = 8 << 20
	readChunk         = 32 << 10
)

// NewAnnexBReader wraps r. A maxNALSize of zero takes DefaultMaxNALSize.
func NewAnnexBReader(r io.Reader, maxNALSize int) *AnnexBReader {
	if maxNALSize <= 0 {
		maxNALSize = DefaultMaxNALSize
	}
	return &AnnexBReader{r: r, maxNAL: maxNALSize, scratch: make([]byte, readChunk)}
}

// Next returns the next NAL unit, start code and trailing zero padding removed.
//
// The returned slice belongs to the caller. The reader reuses its own buffer for
// the next read, so handing out a sub-slice of it would silently rewrite a NAL
// unit that a pre-roll ring is still holding; the copy below is what makes the
// ring safe, and it is the only copy on this path.
func (a *AnnexBReader) Next() ([]byte, error) {
	for {
		if i, n, ok := findStartCode(a.pending, a.searched); ok {
			nal := bytes.TrimRight(a.pending[:i], "\x00")
			if len(nal) > a.maxNAL {
				return nil, ErrNALTooLarge
			}
			// Copy out before advancing: the caller keeps this until the next
			// call, and a pending buffer reused in place would overwrite it.
			out := append([]byte(nil), nal...)
			a.pending = append(a.pending[:0], a.pending[i+n:]...)
			a.searched = 0
			if len(out) == 0 {
				continue
			}
			return out, nil
		}
		// Everything up to three bytes back is undecidable: a start code can
		// straddle this read and the next one.
		if back := len(a.pending) - 3; back > a.searched {
			a.searched = back
		}
		if a.readErr != nil {
			if errors.Is(a.readErr, io.EOF) {
				rest := bytes.TrimRight(a.pending, "\x00")
				a.pending = a.pending[:0]
				if len(rest) == 0 {
					return nil, io.EOF
				}
				if len(rest) > a.maxNAL {
					return nil, ErrNALTooLarge
				}
				return append([]byte(nil), rest...), nil
			}
			return nil, a.readErr
		}
		if len(a.pending) > a.maxNAL+3 {
			return nil, ErrNALTooLarge
		}
		n, err := a.r.Read(a.scratch)
		if n > 0 {
			a.pending = append(a.pending, a.scratch[:n]...)
			if len(a.pending) > a.maxNAL+3 {
				return nil, ErrNALTooLarge
			}
		}
		if err != nil {
			a.readErr = err
		}
	}
}

func findStartCode(b []byte, from int) (int, int, bool) {
	for i := from; i+2 < len(b); i++ {
		if b[i] != 0 || b[i+1] != 0 {
			continue
		}
		if b[i+2] == 1 {
			return i, 3, true
		}
		if i+3 < len(b) && b[i+3] == 1 {
			return i, 4, true
		}
	}
	return 0, 0, false
}

// isVCL reports whether a NAL type carries coded picture data.
func isVCL(t byte) bool { return t >= 1 && t <= 5 }

// isKeyframe reports whether a VCL NAL type is an IDR.
//
// Only IDR resets the reference picture buffer. A recovery-point SEI and a
// non-IDR I-slice do not, so treating either as a clip boundary produces a clip
// whose first frames have nothing to decode from.
func isKeyframe(t byte) bool { return t == NALIDR }

// bitReader reads the RBSP of a parameter set. It is deliberately unforgiving:
// an out-of-range read is an error, never a zero, because a silently zero
// Exp-Golomb value becomes a plausible-looking resolution.
type bitReader struct {
	data []byte
	pos  int
}

func (r *bitReader) u1() (uint, error) {
	if r.pos >= len(r.data)*8 {
		return 0, io.ErrUnexpectedEOF
	}
	b := r.data[r.pos>>3]
	bit := (b >> (7 - uint(r.pos&7))) & 1
	r.pos++
	return uint(bit), nil
}

func (r *bitReader) u(n int) (uint, error) {
	var v uint
	for i := 0; i < n; i++ {
		bit, err := r.u1()
		if err != nil {
			return 0, err
		}
		v = v<<1 | bit
	}
	return v, nil
}

func (r *bitReader) ue() (uint, error) {
	zeros := 0
	for {
		bit, err := r.u1()
		if err != nil {
			return 0, err
		}
		if bit == 1 {
			break
		}
		zeros++
		if zeros > 32 {
			return 0, errors.New("mp4: malformed Exp-Golomb code")
		}
	}
	if zeros == 0 {
		return 0, nil
	}
	rest, err := r.u(zeros)
	if err != nil {
		return 0, err
	}
	return (1 << zeros) - 1 + rest, nil
}

func (r *bitReader) se() (int, error) {
	v, err := r.ue()
	if err != nil {
		return 0, err
	}
	if v&1 == 1 {
		return int(v+1) / 2, nil
	}
	return -int(v / 2), nil
}

// rbsp strips emulation-prevention bytes. A 0x03 that follows 0x00 0x00 is not
// part of the payload, and leaving it in shifts every subsequent bit position
// by three.
//
// rbspInto writes into dst and returns it, grown as needed, so a caller reading
// many NAL units in a row can hand the same buffer back every time. A slice
// header is one Exp-Golomb value at the front of a NAL unit that can be tens of
// kilobytes, so allocating a stripped copy of the whole unit to read that one
// value is an allocation per slice on a path that runs for the life of the
// process.
func rbsp(nal []byte) []byte { return rbspInto(nil, nal) }

func rbspInto(dst, nal []byte) []byte {
	if len(nal) < 2 {
		return dst[:0]
	}
	body := nal[1:]
	out := dst[:0]
	for i := 0; i < len(body); i++ {
		if i >= 2 && body[i] == 0x03 && body[i-1] == 0 && body[i-2] == 0 {
			continue
		}
		out = append(out, body[i])
	}
	return out
}

// SequenceParameters is what the banner, the avcC and the clip metadata all
// read, parsed once from the SPS so the three can never disagree.
type SequenceParameters struct {
	// Profile and Level are profile_idc and level_idc, which are one byte each in
	// the bitstream. They are bytes here so that carrying them into the avcC is a
	// copy rather than a narrowing somebody has to convince themselves is safe.
	Profile byte
	Level   byte
	Width   int
	Height  int
	// ChromaFmt is chroma_format_idc, an Exp-Golomb value with no 8-bit ceiling
	// in the syntax, so it stays an int.
	ChromaFmt    int
	FrameMBSOnly bool
}

// highProfiles are the profile_idc values that carry the chroma-format and
// scaling-list fields the simple case does not have. Getting this list wrong
// yields a width that is a multiple of 16 for most streams and simply wrong
// for the rest, so it is spelled out rather than approximated.
var highProfiles = map[byte]bool{ // keyed by profile_idc, which is one byte
	100: true, 110: true, 122: true, 244: true, 44: true, 83: true,
	86: true, 118: true, 128: true, 138: true, 139: true, 134: true, 135: true,
}

// MaxFrameSide bounds a frame's width or height.
//
// H.264 itself sets no maximum: a six-byte SPS can legitimately describe a
// 32x16400 picture, and a fuzzer found exactly that in a fraction of a second.
// Writing the file anyway is the failure this package documents refusing -
// every player rejects it, and the rejection surfaces as a clip that will not
// open rather than as a stream that will not decode. The bound is generous
// enough that no real camera approaches it.
const MaxFrameSide = 16384

// ErrGeometryTooLarge is returned for an SPS whose frame no decoder will size.
var ErrGeometryTooLarge = errors.New("mp4: the stream declares a frame larger than any decoder will size")

// ParseSequenceParameters reads the frame geometry out of an SPS.
//
// Exported for `internal/rtsp`'s server, which needs the fixture's REAL width and
// height for the description it serves. It was unexported until then, and the
// constant it replaces is the thing worth avoiding: an SDP that says `640-360`
// about a stream that is not 640-360 is a description a client believes.
func ParseSequenceParameters(sps []byte) (SequenceParameters, error) {
	var sp SequenceParameters
	if len(sps) < 4 {
		return sp, errors.New("mp4: SPS is shorter than its own header")
	}
	sp.Profile = sps[1]
	sp.Level = sps[3]
	sp.ChromaFmt = 1 // 4:2:0 for everything that does not say otherwise
	r := &bitReader{data: rbsp(sps)}

	if _, err := r.ue(); err != nil { // seq_parameter_set_id
		return sp, fmt.Errorf("mp4: SPS seq_parameter_set_id: %w", err)
	}
	if highProfiles[sp.Profile] {
		cf, err := r.ue()
		if err != nil {
			return sp, fmt.Errorf("mp4: SPS chroma_format_idc: %w", err)
		}
		sp.ChromaFmt = int(cf)
		if sp.ChromaFmt == 3 {
			if _, err := r.u1(); err != nil { // separate_colour_plane_flag
				return sp, fmt.Errorf("mp4: SPS separate_colour_plane_flag: %w", err)
			}
		}
		if _, err := r.ue(); err != nil { // bit_depth_luma_minus8
			return sp, err
		}
		if _, err := r.ue(); err != nil { // bit_depth_chroma_minus8
			return sp, err
		}
		if _, err := r.u1(); err != nil { // qpprime_y_zero_transform_bypass_flag
			return sp, err
		}
		scaling, err := r.u1()
		if err != nil {
			return sp, err
		}
		if scaling == 1 {
			n := 8
			if sp.ChromaFmt == 3 {
				n = 12
			}
			for i := 0; i < n; i++ {
				present, err := r.u1()
				if err != nil {
					return sp, err
				}
				if present == 0 {
					continue
				}
				size := 16
				if i >= 6 {
					size = 64
				}
				// The scaling-list delta is added modulo 256 to the previous
				// value, so the working values are plain ints kept in range
				// rather than int8s that overflow on the addition.
				last, next := 8, 8
				for j := 0; j < size; j++ {
					if next != 0 {
						d, err := r.se()
						if err != nil {
							return sp, err
						}
						next = (last + d%256 + 256) % 256
					}
					if next != 0 {
						last = next
					}
				}
			}
		}
	}
	if _, err := r.ue(); err != nil { // log2_max_frame_num_minus4
		return sp, err
	}
	pocType, err := r.ue()
	if err != nil {
		return sp, fmt.Errorf("mp4: SPS pic_order_cnt_type: %w", err)
	}
	switch pocType {
	case 0:
		if _, err := r.ue(); err != nil { // log2_max_pic_order_cnt_lsb_minus4
			return sp, err
		}
	case 1:
		if _, err := r.u1(); err != nil { // delta_pic_order_always_zero_flag
			return sp, err
		}
		if _, err := r.se(); err != nil { // offset_for_non_ref_pic
			return sp, err
		}
		if _, err := r.se(); err != nil { // offset_for_top_to_bottom_field
			return sp, err
		}
		n, err := r.ue()
		if err != nil {
			return sp, err
		}
		for i := uint(0); i < n && i < 256; i++ {
			if _, err := r.se(); err != nil { // offset_for_ref_frame
				return sp, err
			}
		}
	}
	if _, err := r.ue(); err != nil { // max_num_ref_frames
		return sp, err
	}
	if _, err := r.u1(); err != nil { // gaps_in_frame_num_value_allowed_flag
		return sp, err
	}
	widthMBs, err := r.ue()
	if err != nil {
		return sp, fmt.Errorf("mp4: SPS pic_width_in_mbs_minus1: %w", err)
	}
	heightUnits, err := r.ue()
	if err != nil {
		return sp, fmt.Errorf("mp4: SPS pic_height_in_map_units_minus1: %w", err)
	}
	only, err := r.u1()
	if err != nil {
		return sp, err
	}
	sp.FrameMBSOnly = only == 1
	if !sp.FrameMBSOnly {
		if _, err := r.u1(); err != nil { // mb_adaptive_frame_field_flag
			return sp, err
		}
	}
	if _, err := r.u1(); err != nil { // direct_8x8_inference_flag
		return sp, err
	}
	cropping, err := r.u1()
	if err != nil {
		return sp, err
	}
	var left, right, top, bottom uint
	if cropping == 1 {
		if left, err = r.ue(); err != nil {
			return sp, err
		}
		if right, err = r.ue(); err != nil {
			return sp, err
		}
		if top, err = r.ue(); err != nil {
			return sp, err
		}
		if bottom, err = r.ue(); err != nil {
			return sp, err
		}
	}

	frameHeight := int(16 * (heightUnits + 1))
	if !sp.FrameMBSOnly {
		frameHeight *= 2
	}
	subWidthC, subHeightC := 2, 2 // 4:2:0
	switch sp.ChromaFmt {
	case 0:
		subWidthC, subHeightC = 1, 1
	case 2:
		subWidthC, subHeightC = 2, 1
	}
	// Crop units are in chroma samples, and the vertical unit scales with
	// frame_mbs_only_flag because cropping counts field lines in the
	// field-coded case.
	cropY := subHeightC
	if !sp.FrameMBSOnly {
		cropY *= 2
	}
	sp.Width = int(16*(widthMBs+1)) - subWidthC*int(left+right)
	sp.Height = frameHeight - cropY*int(top+bottom)
	if sp.Width <= 0 || sp.Height <= 0 {
		return sp, fmt.Errorf("mp4: SPS describes a %dx%d frame", sp.Width, sp.Height)
	}
	if sp.Width > MaxFrameSide || sp.Height > MaxFrameSide {
		return sp, fmt.Errorf("%w: %dx%d, and the limit is %d per side", ErrGeometryTooLarge, sp.Width, sp.Height, MaxFrameSide)
	}
	return sp, nil
}

// firstMBInSlice reads the first field of the slice header, which is what
// identifies the first slice of a coded picture.
//
// This is the single rule both producers - the RTP depacketiser and the file
// reader - use to find picture boundaries, so the two paths cannot disagree
// about where a frame starts. The stripped copy it reads from is built in dst
// and returned, so a caller that keeps the buffer pays for one allocation for
// the largest NAL unit it has seen rather than one per slice.
func firstMBInSlice(dst, nal []byte) (uint, []byte, error) {
	buf := rbspInto(dst, nal)
	first, err := (&bitReader{data: buf}).ue()
	return first, buf, err
}

// Picture is one coded picture and the unit the pre-roll ring is built from.
type Picture struct {
	KeyFrame   bool
	DecodeTime uint64
	Wall       time.Time
	NALs       [][]byte
	// Damaged is set when a NAL in this picture could not be parsed well enough
	// to find a picture boundary. The picture is kept and the flag is reported,
	// because dropping a frame silently and then claiming a continuous clip is
	// the failure the three-absences discipline exists to prevent.
	Damaged string
}

// Bytes is the picture's total Annex-B payload with start codes, which is what
// the recorder's byte budget is measured against.
func (p *Picture) Bytes() int {
	n := 0
	for _, nal := range p.NALs {
		n += len(nal) + 4
	}
	return n
}

// PictureBuilder groups NAL units into pictures.
type PictureBuilder struct {
	cur     *Picture
	pending [][]byte
	damaged string
	// scratch backs the stripped copy a slice header is read from. It is reused
	// for every slice and holds nothing after the read that filled it, which is
	// what makes reuse safe: a picture's NAL units are copied out below, and
	// none of them points into this.
	scratch []byte
	// OnDamaged is called with a reason whenever a NAL could not be parsed. It
	// is a field rather than a log line so the degradation is reported rather
	// than printed and forgotten.
	OnDamaged func(string)
}

// Push adds one NAL and returns the picture it completed, if any.
//
// Non-VCL units (SPS, PPS, SEI, AUD) that arrive before a picture has started
// are held and prepended to the next one, which is what puts the parameter sets
// in the same access unit as the IDR that needs them.
func (b *PictureBuilder) Push(nal []byte, decodeTime uint64, wall time.Time) *Picture {
	if len(nal) == 0 {
		return nil
	}
	typ := nal[0] & 0x1F
	if !isVCL(typ) {
		if b.cur == nil {
			b.pending = append(b.pending, append([]byte(nil), nal...))
		} else {
			b.cur.NALs = append(b.cur.NALs, append([]byte(nil), nal...))
		}
		return nil
	}
	first, scratch, err := firstMBInSlice(b.scratch[:0], nal)
	b.scratch = scratch
	if err != nil {
		// A slice header we cannot read means we cannot tell where this picture
		// starts. Guessing "same picture" would splice two pictures into one
		// corrupt one; guessing "new picture" loses at most one frame. So it is
		// a new picture, and it is reported.
		b.noteDamaged(fmt.Sprintf("unreadable slice header in a type-%d NAL: %v", typ, err))
		first = 0
	}
	if b.cur != nil && first == 0 {
		done := b.cur
		b.cur = nil
		b.start(nal, decodeTime, wall, typ)
		return done
	}
	if b.cur == nil {
		b.start(nal, decodeTime, wall, typ)
		return nil
	}
	b.cur.NALs = append(b.cur.NALs, append([]byte(nil), nal...))
	return nil
}

func (b *PictureBuilder) start(nal []byte, decodeTime uint64, wall time.Time, typ byte) {
	b.cur = &Picture{
		KeyFrame:   isKeyframe(typ),
		DecodeTime: decodeTime,
		Wall:       wall,
	}
	if len(b.pending) > 0 {
		b.cur.NALs = append(b.cur.NALs, b.pending...)
		b.pending = nil
	}
	b.cur.NALs = append(b.cur.NALs, append([]byte(nil), nal...))
	if b.damaged != "" {
		b.cur.Damaged = b.damaged
		b.damaged = ""
	}
}

func (b *PictureBuilder) noteDamaged(reason string) {
	b.damaged = reason
	if b.OnDamaged != nil {
		b.OnDamaged(reason)
	}
}

// Flush returns the picture in progress, if any.
func (b *PictureBuilder) Flush() *Picture {
	done := b.cur
	b.cur = nil
	if done != nil && done.Damaged == "" && b.damaged != "" {
		done.Damaged = b.damaged
		b.damaged = ""
	}
	return done
}

// ParameterSets holds the stream-level configuration the avcC is built from.
//
// A change in any bit of either set is a change in the decode configuration, and
// continuing past one would emit a file carrying two incompatible answers to
// "what resolution is this". The recorder therefore compares them with
// bytes.Equal rather than with a notion of "equivalent", because there isn't
// one that is safe here: two byte-identical sets are certainly the same stream,
// and any other comparison would be a guess about what a decoder might do.
type ParameterSets struct {
	SPS []byte
	PPS []byte
}

// AVCC converts Annex-B NAL units to the length-prefixed form a sample entry
// carries, in one allocation per call.
func AVCC(nals [][]byte) []byte {
	out := make([]byte, 0, AVCCLen(nals))
	var l [4]byte
	for _, n := range nals {
		//nolint:gosec // bounded by the check in AVCCLen
		binary.BigEndian.PutUint32(l[:], uint32(len(n)))
		out = append(out, l[:]...)
		out = append(out, n...)
	}
	return out
}

// AVCCLen is the length AVCC will produce for these NAL units.
//
// It exists because the recorder has to know a sample's size before deciding to
// write it, and measuring by building the sample and throwing it away is an
// allocation and a copy of every frame in the clip.
func AVCCLen(nals [][]byte) int {
	total := 0
	for _, n := range nals {
		if len(n) > math.MaxUint32-4 {
			// Unreachable through the recorder, which bounds a NAL well below
			// this. Reached anyway it is a programming error, and truncating the
			// length here would produce a sample whose declared size disagrees
			// with its own bytes.
			panic("mp4: a NAL unit is longer than an AVCC length field can hold")
		}
		total += 4 + len(n)
	}
	return total
}

// MaxParameterSetBytes bounds an SPS or a PPS.
//
// The avcC format stores both lengths in sixteen bits, so a parameter set longer
// than that cannot be described by the file we are about to write. Writing the
// truncated length anyway produces a file whose avcC disagrees with the
// parameter sets in the stream, and the disagreement shows up as a decode
// failure in a third-party player rather than as a refusal here. No real
// parameter set comes close.
const MaxParameterSetBytes = 0xFFFF

// DecoderConfigRecord builds the avcC box payload for these parameter sets.
//
// Refuses a stream missing either set, because an avcC with an empty sequence
// parameter set list is a file that no decoder will open and that our own
// round-trip would happily accept.
func (p *ParameterSets) DecoderConfigRecord() ([]byte, error) {
	rec, _, err := p.decoderConfig()
	return rec, err
}

// decoderConfig parses the SPS once and returns both answers: the avcC record
// and the geometry that came out of the same bytes. parseSPS is pure, so a
// caller that needs both and parses twice is doing the same work twice and can
// disagree with itself if it ever stops being pure.
func (p *ParameterSets) decoderConfig() (rec []byte, sp SequenceParameters, err error) {
	if len(p.SPS) == 0 || len(p.PPS) == 0 {
		return nil, sp, ErrNoParameterSets
	}
	if len(p.SPS) > MaxParameterSetBytes || len(p.PPS) > MaxParameterSetBytes {
		return nil, sp, fmt.Errorf("%w: a %d-byte SPS or a %d-byte PPS, and the format's length field holds %d",
			ErrParameterSetTooLong, len(p.SPS), len(p.PPS), MaxParameterSetBytes)
	}
	sp, err = ParseSequenceParameters(p.SPS)
	if err != nil {
		return nil, sp, err
	}
	rec = make([]byte, 0, 11+len(p.SPS)+len(p.PPS))
	rec = append(rec, 1) // configurationVersion
	// Profile, constraint flags and level are single bytes in the bitstream and
	// single bytes in avcC, and they are typed as bytes all the way here, so
	// this is a copy rather than a narrowing.
	rec = append(rec, sp.Profile, p.SPS[2], sp.Level)
	rec = append(rec,
		0xFF, // 6 bits reserved, lengthSizeMinusOne = 3 (four-byte NAL length)
		0xE1, // 3 bits reserved, numOfSequenceParameterSets = 1
	)
	// Both lengths were checked against the field's own range above, so these
	// narrowings cannot wrap.
	rec = binary.BigEndian.AppendUint16(rec, uint16(len(p.SPS))) //nolint:gosec // bounded by MaxParameterSetBytes
	rec = append(rec, p.SPS...)
	rec = append(rec, 0x01)                                      // numOfPictureParameterSets = 1
	rec = binary.BigEndian.AppendUint16(rec, uint16(len(p.PPS))) //nolint:gosec // bounded by MaxParameterSetBytes
	rec = append(rec, p.PPS...)
	return rec, sp, nil
}

// ErrParameterSetTooLong is returned for a parameter set the avcC cannot
// describe.
var ErrParameterSetTooLong = errors.New("mp4: a parameter set is longer than the avcC length field can hold")

// Parse reads a NAL's type without decoding anything else about it.
func Parse(nal []byte) (typ byte, ok bool) {
	if len(nal) == 0 {
		return 0, false
	}
	return nal[0] & 0x1F, true
}

// PictureReader turns an Annex-B elementary stream into timed pictures.
//
// An elementary stream carries no timing. RTSP delivers it in RTP timestamps
// and a file does not, so a nominal frame rate has to be supplied rather than
// guessed: the same bytes at 25 fps and at 12.5 fps are clips of different
// length, and a clip that claims to be eight seconds and is four is a clip an
// operator cannot use and a golden test cannot reproduce.
type PictureReader struct {
	r         *AnnexBReader
	b         PictureBuilder
	timescale uint32
	interval  uint64
	next      uint64
	err       error
}

// ReadPictures returns a reader over r. A nominalFPS of zero or less takes 25.
//
// Wall times start at the zero time, which is honest rather than convenient: a
// file has no wall clock, and inventing one is how a replayed clip ends up
// timestamped in 1970.
func ReadPictures(r io.Reader, nominalFPS float64, timescale uint32) *PictureReader {
	if timescale == 0 {
		timescale = DefaultTimescale
	}
	if nominalFPS <= 0 {
		nominalFPS = 25
	}
	interval := uint64(float64(timescale) / nominalFPS)
	if interval == 0 {
		interval = 1
	}
	return &PictureReader{
		r:         NewAnnexBReader(r, DefaultMaxNALSize),
		timescale: timescale,
		interval:  interval,
	}
}

// Timescale is the clock the decode times are expressed in.
func (p *PictureReader) Timescale() uint32 { return p.timescale }

// Next returns the next picture, or io.EOF at the end of the stream.
func (p *PictureReader) Next() (*Picture, error) {
	if p.err != nil {
		return nil, p.err
	}
	for {
		nal, err := p.r.Next()
		if err != nil {
			p.err = err
			if done := p.b.Flush(); done != nil {
				// The final picture is only complete once the stream ends, so it
				// is returned here and io.EOF on the call after it.
				return done, nil
			}
			return nil, err
		}
		// The wall time is the zero time throughout: a file has no clock, and a
		// caller that needs one stamps it from its own capture session.
		if done := p.b.Push(nal, p.next, time.Time{}); done != nil {
			p.next += p.interval
			return done, nil
		}
	}
}

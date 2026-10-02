package mp4

import (
	"encoding/binary"
)

// boxWriter accumulates the bytes of an MP4 file.
//
// Everything is built in memory and handed to the evidence store as one reader.
// The clip is capped at 100 MB by the third party we upload to, and a capture
// path that spilled fragments to a temporary directory would need a cleanup
// path for every way the process can die.
type boxWriter struct{ b []byte }

func (w *boxWriter) u8(v byte) { w.b = append(w.b, v) }
func (w *boxWriter) u16(v uint16) {
	w.b = binary.BigEndian.AppendUint16(w.b, v)
}
func (w *boxWriter) i16(v int16) {
	//nolint:gosec // a signed 16-bit field written as its own two's complement
	w.b = binary.BigEndian.AppendUint16(w.b, uint16(v))
}
func (w *boxWriter) u32(v uint32) { w.b = binary.BigEndian.AppendUint32(w.b, v) }
func (w *boxWriter) i32(v int32) {
	//nolint:gosec // a signed 32-bit field written as its own two's complement
	w.b = binary.BigEndian.AppendUint32(w.b, uint32(v))
}

// zeroPad backs zeros(). Every call site asks for a fixed, small count, so
// make() per call is a per-box allocation on a path that runs once per sample.
var zeroPad [32]byte

func (w *boxWriter) u64(v uint64) { w.b = binary.BigEndian.AppendUint64(w.b, v) }
func (w *boxWriter) zeros(n int) {
	if n <= len(zeroPad) {
		w.b = append(w.b, zeroPad[:n]...)
		return
	}
	w.b = append(w.b, make([]byte, n)...)
}
func (w *boxWriter) raw(p []byte)  { w.b = append(w.b, p...) }
func (w *boxWriter) cstr(s string) { w.b = append(w.b, s...); w.b = append(w.b, 0) }

// MaxBoxBytes is the largest box the 32-bit size field can describe, and so the
// largest file this writer can produce.
//
// The cap is checked rather than assumed because a box whose size field wraps is
// a file whose box tree ends in the middle of nowhere, and the first decoder to
// read it disagrees with us about where the next box is.
const MaxBoxBytes = 0xFFFFFFFF - 8

// header writes a box's eight-byte size-and-type prefix for a payload of the
// given length.
func (w *boxWriter) header(typ string, payload int) {
	if payload+8 > MaxBoxBytes {
		// Unreachable through the recorder, which caps a clip far below this.
		// Reached anyway, it is a programming error rather than a bad stream, so
		// it stops the process instead of writing a file that cannot be read.
		panic("mp4: box " + typ + " is larger than the format can describe")
	}
	w.u32(uint32(payload + 8)) //nolint:gosec // bounded by MaxBoxBytes above
	w.raw([]byte(typ))
}

// box writes a box of the given type around an already-built payload.
func (w *boxWriter) box(typ string, payload []byte) {
	w.header(typ, len(payload))
	w.raw(payload)
}

// fullBox writes a box whose payload begins with a version and a 24-bit flag
// field, which is the shape every box under moov has.
func (w *boxWriter) fullBox(typ string, version byte, flags uint32, build func(*boxWriter)) {
	inner := &boxWriter{}
	inner.u8(version)
	// A 24-bit field taken apart into three bytes; each shift is in range by the
	// type of flags, which is itself a 32-bit value.
	inner.u8(byte(flags >> 16)) //nolint:gosec // the top byte of a 24-bit field
	inner.u8(byte(flags >> 8))  //nolint:gosec // the middle byte of a 24-bit field
	inner.u8(byte(flags))       //nolint:gosec // the low byte of a 24-bit field
	build(inner)
	w.box(typ, inner.b)
}

var unityMatrix = []uint32{0x00010000, 0, 0, 0, 0x00010000, 0, 0, 0, 0x40000000}

// fragmenter writes a fragmented MP4: ftyp, a single moov carrying the codec
// configuration, then one moof/mdat pair per sample.
//
// One fragment per sample rather than a fragment per second: the overhead is
// tens of bytes, the bookkeeping is a straight line, and a clip that ends on a
// sample boundary is what makes the byte cap land where a reader expects it.
type fragmenter struct {
	ts        uint32
	seq       uint32
	avcC      []byte
	width     int
	height    int
	w         boxWriter
	started   bool
	firstTime uint64
	lastTime  uint64
	samples   int
	keyframes int
}

func newFragmenter(ts uint32, params *ParameterSets) (*fragmenter, error) {
	rec, sp, err := params.decoderConfig()
	if err != nil {
		return nil, err
	}
	if ts == 0 {
		ts = DefaultTimescale
	}
	return &fragmenter{ts: ts, avcC: rec, width: sp.Width, height: sp.Height}, nil
}

func (f *fragmenter) init() {
	if f.started {
		return
	}
	f.started = true
	var w boxWriter
	var ftyp boxWriter
	ftyp.raw([]byte("isom"))
	ftyp.u32(512)
	ftyp.raw([]byte("isom"))
	ftyp.raw([]byte("iso6"))
	ftyp.raw([]byte("avc1"))
	ftyp.raw([]byte("mp41"))
	w.box("ftyp", ftyp.b)
	w.box("moov", f.movieHeader())
	f.w.raw(w.b)
}

func (f *fragmenter) movieHeader() []byte {
	var moov boxWriter
	moov.fullBox("mvhd", 0, 0, func(m *boxWriter) {
		m.u32(0) // creation_time
		m.u32(0) // modification_time
		m.u32(f.ts)
		m.u32(0) // duration: assembled from the fragments
		m.u32(0x00010000)
		m.u16(0x0100)
		m.u16(0)
		m.u32(0)
		m.u32(0)
		for _, v := range unityMatrix {
			m.u32(v)
		}
		m.zeros(24)
		m.u32(2) // next_track_ID
	})
	moov.box("trak", f.track())
	// mvex/trex is what tells a fragmented reader the defaults for a track it
	// has not seen a sample for yet. Without it, a demuxer that reads the
	// sample table first and the fragments second has no way to match a trun to
	// the track it names, and reports the file as unreadable while the file is
	// in fact fine.
	moov.box("mvex", f.movieExtends())
	return moov.b
}

func (f *fragmenter) movieExtends() []byte {
	var mvex boxWriter
	mvex.fullBox("trex", 0, 0, func(t *boxWriter) {
		t.u32(1) // track_ID
		t.u32(1) // default_sample_description_index
		t.u32(0) // default_sample_duration: every trun carries its own
		t.u32(0) // default_sample_size
		t.u32(0) // default_sample_flags
	})
	return mvex.b
}

// track describes the single video track. Width and height are written as
// 16.16 fixed point because that is the box's shape, and a player that reads a
// zero here shows the clip at no size at all.
func (f *fragmenter) track() []byte {
	var trak boxWriter
	trak.fullBox("tkhd", 0, 0x000007, func(t *boxWriter) { // enabled, in movie, in preview
		t.u32(0)
		t.u32(0)
		t.u32(1) // track_ID
		t.u32(0)
		t.u32(0) // duration
		t.u32(0)
		t.u32(0)
		t.i16(0) // layer
		t.i16(0) // alternate_group
		t.i16(0) // volume: zero for video
		t.u16(0)
		for _, v := range unityMatrix {
			t.u32(v)
		}
		// 16.16 fixed point. The dimensions were bounds-checked against
		// MaxFrameSide when the parameter set was parsed, so both fit.
		t.u32(uint32(f.width) << 16)  //nolint:gosec // bounded by MaxFrameSide
		t.u32(uint32(f.height) << 16) //nolint:gosec // bounded by MaxFrameSide
	})
	trak.box("mdia", f.media())
	return trak.b
}

func (f *fragmenter) media() []byte {
	var mdia boxWriter
	mdia.fullBox("mdhd", 0, 0, func(m *boxWriter) {
		m.u32(0)
		m.u32(0)
		m.u32(f.ts)
		m.u32(0)
		m.u16(0x55C4) // 'und'
		m.u16(0)
	})
	mdia.fullBox("hdlr", 0, 0, func(h *boxWriter) {
		h.u32(0)
		h.raw([]byte("vide"))
		h.zeros(12)
		h.cstr("VideoHandler")
	})
	mdia.box("minf", f.mediaInformation())
	return mdia.b
}

func (f *fragmenter) mediaInformation() []byte {
	var minf boxWriter
	minf.fullBox("vmhd", 0, 1, func(m *boxWriter) {
		m.u16(0)
		m.u16(0)
		m.u16(0)
		m.u16(0)
	})
	minf.box("dinf", dataInformation())
	minf.box("stbl", f.sampleTable())
	return minf.b
}

func dataInformation() []byte {
	var dinf boxWriter
	dinf.fullBox("dref", 0, 0, func(d *boxWriter) {
		d.u32(1)
		d.fullBox("url ", 0, 1, func(*boxWriter) {}) // self-contained
	})
	return dinf.b
}

func (f *fragmenter) sampleTable() []byte {
	var stbl boxWriter
	stbl.fullBox("stsd", 0, 0, func(s *boxWriter) {
		s.u32(1)
		s.box("avc1", f.avc1())
	})
	// The four tables below are empty by design: in a fragmented file every
	// sample lives in a fragment, and a chunk-offset table that disagrees with
	// them is worse than one that is empty.
	stbl.fullBox("stts", 0, 0, func(s *boxWriter) { s.u32(0) })
	stbl.fullBox("stsc", 0, 0, func(s *boxWriter) { s.u32(0) })
	stbl.fullBox("stsz", 0, 0, func(s *boxWriter) { s.u32(0); s.u32(0) })
	stbl.fullBox("stco", 0, 0, func(s *boxWriter) { s.u32(0) })
	return stbl.b
}

func (f *fragmenter) avc1() []byte {
	var e boxWriter
	e.zeros(6) // reserved
	e.u16(1)   // data_reference_index
	e.u16(0)   // pre_defined
	e.u16(0)   // reserved
	e.zeros(12)
	// Bounded by MaxFrameSide, so both fit a 16-bit field.
	e.u16(uint16(f.width))  //nolint:gosec // bounded by MaxFrameSide
	e.u16(uint16(f.height)) //nolint:gosec // bounded by MaxFrameSide
	e.u32(0x00480000)       // horizresolution, 72 dpi
	e.u32(0x00480000)       // vertresolution
	e.u32(0)                // reserved
	e.u16(1)                // frame_count
	e.zeros(32)             // compressorname
	e.u16(0x0018)           // depth
	e.i16(-1)               // pre_defined
	e.box("avcC", f.avcC)
	return e.b
}

// appendSample writes one sample as a moof/mdat pair.
func (f *fragmenter) appendSample(p *Picture, duration uint32) {
	f.init()
	if f.samples == 0 {
		f.firstTime = p.DecodeTime
	}
	f.lastTime = p.DecodeTime + uint64(duration)
	f.samples++
	if p.KeyFrame {
		f.keyframes++
	}
	data := AVCC(p.NALs)

	var traf boxWriter
	traf.fullBox("tfhd", 0, 0x020000, func(t *boxWriter) { t.u32(1) }) // default-base-is-moof
	traf.fullBox("tfdt", 1, 0, func(t *boxWriter) { t.u64(p.DecodeTime) })
	// sample_depends_on is the only per-sample flag a decoder needs here:
	// 2 for a picture that does not depend on others, 1 for one that does.
	// Writing 0 would assert "unknown" and make a player refuse to seek.
	flags := uint32(0x01000000)
	if p.KeyFrame {
		flags = 0x02000000
	}
	const trunFlags = 0x000001 | 0x000100 | 0x000200 | 0x000400
	trunAt := len(traf.b)
	traf.fullBox("trun", 0, trunFlags, func(t *boxWriter) {
		t.u32(1)        // sample_count
		t.i32(0)        // data_offset, patched once the moof is sized
		t.u32(duration) // sample_duration
		//nolint:gosec // a sample is bounded by the NAL bound, far below 4 GiB
		t.u32(uint32(len(data))) // sample_size
		t.u32(flags)             // sample_flags
	})

	var moof boxWriter
	moof.fullBox("mfhd", 0, 0, func(m *boxWriter) { m.u32(f.seq) })
	// The traf box contributes eight bytes of its own header before its payload,
	// and the field being patched sits sixteen bytes into the trun box: eight
	// of box header, four of version and flags, four of sample count.
	offAt := len(moof.b) + 8 + trunAt + 16
	moof.box("traf", traf.b)
	// tfhd says the base is the start of this moof, so data_offset is measured
	// from there to the first byte after the mdat header: this moof's own eight
	// header bytes, its payload, and the mdat's eight. The field is patched
	// inside the finished moof rather than in traf, because box() copies.
	//nolint:gosec // a moof is tens of bytes and a box is bounded by MaxBoxBytes
	binary.BigEndian.PutUint32(moof.b[offAt:], uint32(len(moof.b)+16))

	f.w.box("moof", moof.b)
	// The mdat is written in place rather than through box(). box() takes an
	// already-built payload, so routing the sample through it would copy every
	// byte of every frame into a scratch buffer before copying them again into
	// the file - twice the memory traffic, on the largest thing in the clip.
	f.w.header("mdat", len(data))
	f.w.raw(data)
	f.seq++
}

// duration is the clip's presentation length in media timescale units.
func (f *fragmenter) duration() uint32 {
	if f.lastTime <= f.firstTime {
		return 0
	}
	d := f.lastTime - f.firstTime
	if d > 0xFFFFFFFF {
		d = 0xFFFFFFFF
	}
	return uint32(d)
}

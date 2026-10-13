package main

import (
	"bytes"

	"time"

	"github.com/Sergey-Bar/vision/internal/mp4"
)

// Small helpers the poc tests need, kept here so the test file reads as
// assertions rather than as plumbing.

// zeroHash is the documented placeholder a degraded observation carries, and the
// test that names it needs the literal rather than a recomputation: the point is
// that this exact string means "nothing ran".
const zeroHash = "0000000000000000000000000000000000000000000000000000000000000000"

// oneFrameTolerance is the slack a clip's reported duration is allowed against an
// independent decoder's, in seconds' worth of a Duration.
const oneFrameTolerance = time.Second

const maxBytesDefault = mp4.DefaultMaxBytes

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// nalUnits splits an Annex-B stream using the product's own reader. A test that
// hand-rolled the framing would be testing its own framing.
func nalUnits(raw []byte) [][]byte {
	r := mp4.NewAnnexBReader(bytes.NewReader(raw), mp4.DefaultMaxNALSize)
	var out [][]byte
	for {
		nal, err := r.Next()
		if err != nil {
			return out
		}
		out = append(out, nal)
	}
}

func countNALs(raw []byte) int { return len(nalUnits(raw)) }

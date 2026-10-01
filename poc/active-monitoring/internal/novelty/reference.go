package novelty

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
)

// writeLumaJPEG writes a luma grid out as a grayscale JPEG.
//
// The export is what makes a pinned reference ATTRIBUTABLE: a reviewer has to be
// able to be handed the exact picture every future measurement was made against.
// A reference that exists only as a downscaled float grid inside this process is
// not evidence of anything, and it is gone the moment the data root is moved.
//
// It is written from the grid rather than from the source frame on purpose. The
// grid is what the measurement used, so exporting the grid means the exported
// picture and the measurement cannot disagree about what was compared.
func writeLumaJPEG(g *grid, path string) error {
	if g == nil {
		return fmt.Errorf("novelty: there is nothing to write")
	}
	img := image.NewGray(image.Rect(0, 0, g.W, g.H))
	for i, v := range g.cells {
		c := int(v + 0.5)
		if c < 0 {
			c = 0
		}
		if c > 255 {
			c = 255
		}
		img.Pix[i] = uint8(c)
	}
	return encodeJPEG(img, path, 92)
}

func encodeJPEG(img image.Image, path string, quality int) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("novelty: cannot write %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			return
		}
	}()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: quality}); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("novelty: cannot encode %s: %w", path, err)
	}
	return nil
}

// WriteRGBJPEG writes an RGB frame out as a JPEG, for a caller that wants the
// stored reference to be the frame rather than the grid.
//
// It is what the pipeline uses when it stores the reference view as evidence: the
// evidence store takes a file, and the file should be the picture an operator can
// look at.
func WriteRGBJPEG(rgb []byte, w, h int, path string) error {
	if w <= 0 || h <= 0 || len(rgb) != w*h*3 {
		return fmt.Errorf("novelty: cannot write %s: a %dx%d frame needs %d bytes of RGB and this one carries %d",
			path, w, h, w*h*3, len(rgb))
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	i := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: rgb[i], G: rgb[i+1], B: rgb[i+2], A: 255})
			i += 3
		}
	}
	return encodeJPEG(img, path, 92)
}

// fileSHA is the content hash of a stored frame, so a pinned reference can name
// the bytes it was built from.
func fileSHA(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

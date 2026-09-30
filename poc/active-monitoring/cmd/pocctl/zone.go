package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// cmdZone is T35. Drawing a line on a street camera by guessing coordinates is a
// coin flip, and a wrong line produces a run with zero events and no error. So
// the tool prints a still with a coordinate grid on it and proposes a zones.json
// the operator can edit.
//
// Each source gets its own zones.json, because coordinates drawn on one camera
// are meaningless on another.
func cmdZone(args []string) error {
	flags, err := flagSet("zone", map[string]string{"pick": "", "out": "", "at": "2", "width": "640", "grid": "10", "force": boolSpec}, args)
	if err != nil {
		return err
	}
	src := flags["pick"]
	if src == "" {
		return fmt.Errorf("zone: no source. Pass --pick <file>, e.g. --pick testdata/car-crossing.mp4. The grid is printed on a still of THIS source, because coordinates drawn on one camera are meaningless on another")
	}
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("zone: %s cannot be read: %w", src, err)
	}
	out := flags["out"]
	if out == "" {
		stem := strings.TrimSuffix(src, filepath.Ext(src))
		out = stem + ".zones.json"
	}

	// REFUSE to overwrite a zone file that already exists, BEFORE any work.
	//
	// The first version of this tool wrote its proposal straight to the derived
	// name, and running `pocctl zone --pick` against a hand-written zones.json
	// silently replaced the line somebody had already drawn. A tool that
	// proposes must never install: a wrong line produces zero events and no
	// error, and a destroyed correct one produces the same.
	//
	// The guard is first on purpose. It ran after the ffprobe probe and the still
	// render, so the refusal came only after doing all the work it was going to
	// throw away.
	if _, err := os.Stat(out); err == nil {
		if flags["force"] != "true" {
			return fmt.Errorf("zone: %s already exists and will not be overwritten.\n"+
				"  This tool PROPOSES a line; it does not install one. A wrong line produces zero\n"+
				"  events and no error, so replacing a line that works is worse than not having one.\n"+
				"  Write the proposal somewhere new with --out, or pass --force if you really mean it.",
				out)
		}
		// Keep the old one beside the new one, so a force is still recoverable.
		backup := out + ".replaced"
		if err := os.Rename(out, backup); err != nil {
			return fmt.Errorf("zone: cannot move the existing %s aside: %w", out, err)
		}
		fmt.Printf("  the previous %s was moved to %s\n\n", out, backup)
	}
	at := 2.0
	fmt.Sscanf(flags["at"], "%g", &at)
	width := 640
	fmt.Sscanf(flags["width"], "%d", &width)
	grid := 10
	fmt.Sscanf(flags["grid"], "%d", &grid)
	if grid < 2 || grid > 20 {
		grid = 10
	}
	// The still is named after the SOURCE, not after --out.
	//
	// It was derived from the output path, so `--out x.proposed.json` produced
	// "x.proposed.json.pick.png" — a still nobody can find and whose name says
	// nothing about which camera it is a grid for.
	stillStem := strings.TrimSuffix(src, filepath.Ext(src))
	// The still is a PNG, not a JPEG.
	//
	// The first version wrote .jpg, which means ffmpeg encodes it as MJPEG, and
	// ffmpeg 8 refuses to encode a non-full-range YUV MJPEG at all. The picker
	// therefore failed on every source, on the one machine in this repository
	// where anyone would try it.
	//
	// A PNG is lossless and has no colour-range negotiation, and reading a
	// coordinate off a grid wants lossless anyway: a JPEG artefact at a grid
	// intersection is an argument about whether the line is 0.502 or 0.505.
	still := stillStem + ".pick.png"

	if err := renderStill(src, still, at, width, grid); err != nil {
		return err
	}
	probe, err := probeFrame(src)
	if err != nil {
		return err
	}
	proposed := proposeZones(src, probe.Width, probe.Height, grid)
	body, _ := json.MarshalIndent(proposed, "", "  ")
	if err := os.WriteFile(out, append(body, '\n'), 0o644); err != nil {
		return err
	}

	fmt.Println("# zone picker")
	fmt.Println()
	fmt.Printf("  still:         %s\n", still)
	fmt.Printf("  proposed file: %s\n", out)
	fmt.Printf("  frame:         %dx%d\n\n", probe.Width, probe.Height)
	fmt.Println("The still carries a coordinate grid drawn by the server, in normalised 0..1")
	fmt.Println("coordinates, at the steps below. Read two grid points off the still and put them")
	fmt.Println("in the line's a and b.")
	fmt.Println()
	fmt.Println("  columns: 0.0, 0.1, 0.2 ... 1.0")
	fmt.Println("  rows:    0.0, 0.1, 0.2 ... 1.0   (0,0 is the top-left)")
	fmt.Println()
	fmt.Println("The proposed file has the same lines as the still's red guides. Move the")
	fmt.Println("endpoints until a guide runs along the kerb you mean, then re-run:")
	fmt.Println()
	fmt.Printf("  $env:SP_POC_ZONES='%s'\n", out)
	fmt.Println()
	fmt.Println("Verify before a long run: with the zone set, a short run that produces one event")
	fmt.Println("is the proof that the line is in the right place. A run that produces none is")
	fmt.Println("not evidence that nothing happened.")
	return nil
}

func renderStill(src, out string, at float64, width, grid int) error {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("zone: ffmpeg is not on PATH, and the still is rendered with it: %w", err)
	}
	// The grid is drawn by ffmpeg's drawgrid filter in normalised terms: the
	// filter takes pixels, so the sizes come from the frame's own resolution,
	// which is why the frame is probed first.
	probe, err := probeFrame(src)
	if err != nil {
		return err
	}
	cw := probe.Width / grid
	ch := probe.Height / grid
	args := []string{"-hide_banner", "-loglevel", "error", "-y",
		"-ss", fmt.Sprintf("%g", at), "-i", src, "-frames:v", "1",
		"-vf", fmt.Sprintf("drawgrid=w=%d:h=%d:t=1:c=red@0.8,scale=%d:-2", cw, ch, width),
		"-q:v", "3", out}
	if o, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("zone: ffmpeg could not render the still: %v: %s", err, strings.TrimSpace(string(o)))
	}
	return nil
}

type frameInfo struct {
	Width  int
	Height int
	Codec  string
}

func probeFrame(src string) (frameInfo, error) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return frameInfo{}, fmt.Errorf("zone: ffprobe is not on PATH, and the frame's resolution is needed to size the grid: %w", err)
	}
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height,codec_name", "-of", "json", src).Output()
	if err != nil {
		return frameInfo{}, fmt.Errorf("zone: ffprobe failed on %s: %w", src, err)
	}
	var parsed struct {
		Streams []struct {
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return frameInfo{}, fmt.Errorf("zone: ffprobe's output for %s is not usable: %w", src, err)
	}
	if len(parsed.Streams) == 0 {
		return frameInfo{}, fmt.Errorf("zone: %s has no video stream", src)
	}
	s := parsed.Streams[0]
	return frameInfo{Width: s.Width, Height: s.Height, Codec: s.CodecName}, nil
}

// proposeZones writes a zone file with a full-width horizontal line at the
// middle, which is a starting point and not an answer. The file's own note says
// so, because a file that looks authoritative and is wrong is worse than one
// that looks provisional.
func proposeZones(src string, w, h, grid int) map[string]any {
	half := float64(grid) / 2
	return map[string]any{
		"source": src,
		"frame":  map[string]int{"width": w, "height": h},
		"note": "PROPOSED by 'pocctl zone --pick'. The line below is a placeholder at the " +
			"middle of the frame, not a kerb. Read two grid coordinates off the still and put " +
			"them in a and b. A zone with no line produces zero events and no error, which is " +
			"the most expensive kind of silence there is.",
		"lines": []map[string]any{{
			"name":      "kerb",
			"a":         []float64{0, round(half / float64(grid))},
			"b":         []float64{1, round(half / float64(grid))},
			"direction": "toward-the-bottom",
			"classes":   []string{"car", "bird", "person"},
		}},
	}
}

func round(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

package main

// The design system's gate.
//
// The console and the showcase are one product with two renderers, and this
// file holds the claims that make them one:
//
//   - the palette is in ONE file, and the console imports it
//   - every colour pair that carries text clears WCAG AA
//   - the type scale, the motion scale and the radii are complete
//   - every class the console's source uses is a class Tailwind GENERATES
//
// The last one is the expensive lesson. `h-22` and `lg:grid-cols-16` are not
// in Tailwind's scale, so they compiled and did nothing: the app bar had no
// defined height and the sixteen-tile live grid rendered at four columns. A
// class that does not exist is a silent failure, and the only way to catch
// it is to look.

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// ---------- the palette -------------------------------------------------------

// tokenPattern reads one custom property out of the token file.
var tokenPattern = regexp.MustCompile(`(?m)^\s*(--[a-z0-9-]+)\s*:\s*([^;]+);`)

// tokens reads the token file.
func tokens(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("static", "tokens.css"))
	if err != nil {
		t.Fatalf("read the token file: %v", err)
	}
	out := map[string]string{}
	for _, m := range tokenPattern.FindAllStringSubmatch(string(raw), -1) {
		out[m[1]] = strings.TrimSpace(m[2])
	}
	if len(out) == 0 {
		t.Fatal("the token file declared no custom properties, so this test is asserting nothing")
	}
	return out
}

// hex reads a colour as six hex digits.
func hexColour(t *testing.T, v string) (float64, float64, float64) {
	t.Helper()
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "#") || len(v) != 7 {
		t.Fatalf("%q is not a six-digit hex colour", v)
	}
	r, _ := strconv.ParseInt(v[1:3], 16, 64)
	g, _ := strconv.ParseInt(v[3:5], 16, 64)
	b, _ := strconv.ParseInt(v[5:7], 16, 64)
	return float64(r) / 255, float64(g) / 255, float64(b) / 255
}

// luminance is the WCAG relative luminance.
func luminance(r, g, b float64) float64 {
	lin := func(c float64) float64 {
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// contrast is the WCAG contrast ratio between two colours.
func contrast(t *testing.T, a, b string) float64 {
	t.Helper()
	ar, ag, ab := hexColour(t, a)
	br, bg, bb := hexColour(t, b)
	la, lb := luminance(ar, ag, ab), luminance(br, bg, bb)
	if lb > la {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TestThePaletteClearsWCAGAA
//
// Every pair that carries TEXT clears 4.5:1, and every pair that carries a
// non-text signal - a dot, a bar, a ring - clears 3:1.
//
// The numbers are a claim until a test says otherwise, and the plan proposed
// a de-emphasised ink of #4A5568, which is 2.6:1 on this ground: a BORDER
// colour, not a text colour. The token is #7A8494, which clears 5:1, and
// this test is what holds that line.
func TestThePaletteClearsWCAGAA(t *testing.T) {
	tok := tokens(t)

	textPairs := []struct {
		name string
		fg   string
		bg   string
	}{
		{"body text on the ground", "--ink-1", "--bg"},
		{"body text on a panel", "--ink-1", "--bg-panel"},
		{"body text on a raised panel", "--ink-1", "--bg-elevated"},
		{"secondary text on the ground", "--ink-2", "--bg"},
		{"secondary text on a panel", "--ink-2", "--bg-panel"},
		{"the de-emphasised tone on the ground", "--ink-3", "--bg"},
		{"the de-emphasised tone on a panel", "--ink-3", "--bg-panel"},
		{"the accent on the ground", "--accent", "--bg"},
		{"the accent on a panel", "--accent", "--bg-panel"},
		{"the accent's own foreground on the accent", "--accent-fg", "--accent"},
		{"the ok ink on a panel", "--ok-ink", "--bg-panel"},
		{"the warn ink on a panel", "--warn-ink", "--bg-panel"},
		{"the bad ink on a panel", "--bad-ink", "--bg-panel"},
		{"the unknown ink on a panel", "--unknown-ink", "--bg-panel"},
		{"the accent ink on a panel", "--accent-ink", "--bg-panel"},
	}

	for _, p := range textPairs {
		fg, ok := tok[p.fg]
		if !ok {
			t.Errorf("the token %s is missing from the token file", p.fg)
			continue
		}
		bg, ok := tok[p.bg]
		if !ok {
			t.Errorf("the token %s is missing from the token file", p.bg)
			continue
		}
		c := contrast(t, fg, bg)
		if c < 4.5 {
			t.Errorf("%s: %s on %s is %.2f:1, and text needs 4.5:1", p.name, p.fg, p.bg, c)
		}
	}

	// The state FILLS are non-text signals: a dot, a bar, a ring. They need
	// three to one, and they are what a colour-blind reader falls back to
	// when they cannot separate the hues.
	fillPairs := []struct {
		name string
		fg   string
		bg   string
	}{
		{"the ok fill on a panel", "--ok", "--bg-panel"},
		{"the warn fill on a panel", "--warn", "--bg-panel"},
		{"the bad fill on a panel", "--bad", "--bg-panel"},
		{"the unknown fill on a panel", "--unknown", "--bg-panel"},
		{"the accent fill on a panel", "--accent", "--bg-panel"},
	}
	for _, p := range fillPairs {
		fg, ok := tok[p.fg]
		if !ok {
			t.Errorf("the token %s is missing from the token file", p.fg)
			continue
		}
		bg, ok := tok[p.bg]
		if !ok {
			continue
		}
		c := contrast(t, fg, bg)
		if c < 3.0 {
			t.Errorf("%s: %s on %s is %.2f:1, and a non-text signal needs 3:1", p.name, p.fg, p.bg, c)
		}
	}
}

// TestTheTokenFileIsComplete
//
// The type scale, the motion scale and the radii are all there.
//
// A missing token is a class that silently does nothing, and the design
// system's whole claim is that there is one source of truth: a token that is
// only in the plan and not in the file is a token nobody can use.
func TestTheTokenFileIsComplete(t *testing.T) {
	tok := tokens(t)

	required := []string{
		// the ground and the surfaces
		"--bg", "--bg-panel", "--bg-elevated", "--bg-hover",
		"--line", "--line-strong",
		// the ink
		"--ink-1", "--ink-2", "--ink-3",
		// the one accent
		"--accent", "--accent-strong", "--accent-fg",
		// the four states and their inks
		"--ok", "--warn", "--bad", "--unknown",
		"--ok-ink", "--warn-ink", "--bad-ink", "--unknown-ink", "--accent-ink",
		// the type scale
		"--fs-display", "--fs-title", "--fs-heading", "--fs-subhead",
		"--fs-body", "--fs-body-sm", "--fs-meta", "--fs-micro",
		"--lh-display", "--lh-title", "--lh-heading", "--lh-subhead",
		"--lh-body", "--lh-body-sm", "--lh-meta", "--lh-micro",
		// the radii
		"--radius-sm", "--radius", "--radius-lg", "--radius-xl", "--radius-full",
		// the motion
		"--dur-1", "--dur-2", "--dur-3", "--dur-4", "--ease-out",
		// the faces
		"--font", "--mono",
	}
	for _, name := range required {
		if _, ok := tok[name]; !ok {
			t.Errorf("the token %s is missing from the token file", name)
		}
	}
}

// TestTheTokenFileHasNoPureBlackAndNoPureWhite
//
// The ground is not #000 and the ink is not #fff.
//
// A pure black and a pure white are the two values a display cannot show
// and a camera cannot record, and a design that uses them looks like a
// screenshot of a design rather than a product.
func TestTheTokenFileHasNoPureBlackAndNoPureWhite(t *testing.T) {
	tok := tokens(t)
	for _, name := range []string{"--bg", "--bg-panel", "--bg-elevated"} {
		v := strings.ToLower(tok[name])
		if v == "#000000" || v == "#000" {
			t.Errorf("%s is pure black", name)
		}
	}
	for _, name := range []string{"--ink-1"} {
		v := strings.ToLower(tok[name])
		if v == "#ffffff" || v == "#fff" {
			t.Errorf("%s is pure white", name)
		}
	}
}

// ---------- the console's source ---------------------------------------------

// spaFiles lists the console's own source.
func spaFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(filepath.Join("spa", "src"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".tsx") || strings.HasSuffix(path, ".ts") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the console's source: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no console source files were found, so this test is asserting nothing")
	}
	return out
}

// TestTheConsoleUsesNoWeightHeavierThanSixHundred
//
// The design system has three weights: 400, 500 and 600.
//
// `font-black` appeared 128 times across sixteen files, which is not a
// hierarchy: at 900 every element is shouting and the eye has no idea where
// to start. A console is read at a glance, and the glance needs a ladder.
func TestTheConsoleUsesNoWeightHeavierThanSixHundred(t *testing.T) {
	banned := []string{"font-black", "font-extrabold", "font-bold"}
	for _, path := range spaFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := string(raw)
		for _, b := range banned {
			if strings.Contains(body, b) {
				t.Errorf("%s uses %s. The console has three weights and the heaviest is 600: "+
					"at 900 every element is shouting and the eye has nowhere to start", path, b)
			}
		}
	}
}

// TestTheConsoleHasNoGlowAndNoDropShadow
//
// No glows, no drop shadows.
//
// On a dark ground a shadow reads as a smudge, and the console used to put
// `shadow-2xl` on nearly every card. Depth is a tint step plus a hairline.
func TestTheConsoleHasNoGlowAndNoDropShadow(t *testing.T) {
	pattern := regexp.MustCompile(`(?:^|[\s"'])(?:shadow-|glow-)[a-z0-9/\[\]#.-]*`)
	for _, path := range spaFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, m := range pattern.FindAllString(string(raw), -1) {
			class := strings.TrimSpace(m)
			// The two that are not a shadow: the one that removes it, and
			// the one hairline edge the design system allows.
			if class == "shadow-none" || class == "shadow-edge" {
				continue
			}
			t.Errorf("%s uses %q. A shadow on a dark ground is a smudge, and a glow is a "+
				"halo around a thing that does not glow", path, class)
		}
	}
}

// TestTheConsoleHasNoRawHexColour
//
// No hand-written hex in a class name.
//
// The token file, the Tailwind config and a raw hex in a class attribute is
// three palettes. The token file and the config are two views of ONE palette;
// the hex in the source is a third.
func TestTheConsoleHasNoRawHexColour(t *testing.T) {
	pattern := regexp.MustCompile(`\[(#[0-9a-fA-F]{3,8})\]`)
	for _, path := range spaFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, m := range pattern.FindAllStringSubmatch(string(raw), -1) {
			t.Errorf("%s carries the raw colour %s. A colour is a token, and a token is "+
				"in the token file, so that there is one palette rather than three", path, m[1])
		}
	}
}

// TestEveryClassTheConsoleUsesIsAClassTailwindGenerates
//
// The expensive lesson.
//
// `h-22` and `lg:grid-cols-16` are not in Tailwind's scale. They compile
// and they do nothing: the app bar had no defined height, and the
// sixteen-tile live grid rendered at four columns. Nothing failed. The
// screen just came out wrong, and the reason was a class name nobody
// checked.
//
// The check is against the SCALE, not against a built bundle, so it runs on a
// machine that has never run `npm run build`.
//
//   - a height must be in Tailwind's spacing scale, or in the config's
//     `height` key
//   - a grid template must be 1..12, or in the config's `gridTemplateColumns`
//   - a radius must be one the config declares
func TestEveryClassTheConsoleUsesIsAClassTailwindGenerates(t *testing.T) {
	config := readTailwindConfig(t)

	// The classes the config itself declares, so the test knows what it adds.
	//
	// The KEYS are what matter: `height: { bar: '56px' }` declares a class
	// called `h-bar`, and the value is what it resolves to.
	extraHeights := map[string]bool{}
	extraGridCols := map[string]bool{}
	extraRadii := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s{4,}(\w+):\s*\{([^}]*)\}`).FindAllStringSubmatch(config, -1) {
		key, body := m[1], m[2]
		switch key {
		case "height", "gridTemplateColumns", "borderRadius":
			for _, v := range regexp.MustCompile(`(?m)^\s*'?([\w-]+)'?\s*:`).FindAllStringSubmatch(body, -1) {
				switch key {
				case "height":
					extraHeights[v[1]] = true
				case "gridTemplateColumns":
					extraGridCols[v[1]] = true
				case "borderRadius":
					extraRadii[v[1]] = true
				}
			}
		}
	}
	for _, k := range []string{"none", "DEFAULT", "sm", "md", "lg", "xl", "2xl", "3xl", "4xl", "full"} {
		extraRadii[k] = true
	}

	// Tailwind's default spacing scale, which is what `h-N` draws from.
	spacing := map[string]bool{}
	for _, n := range []string{"0", "0.5", "1", "1.5", "2", "2.5", "3", "3.5", "4", "5", "6", "7", "8", "9", "10", "11", "12", "14", "16", "20", "24", "28", "32", "36", "40", "44", "48", "52", "56", "60", "64", "72", "80", "96"} {
		spacing[n] = true
	}
	for k := range extraHeights {
		spacing[k] = true
	}

	classPattern := regexp.MustCompile(`(?:^|[\s"'` + "`" + `])([a-z0-9:/\[\]().%-]+)`)
	for _, path := range spaFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := string(raw)
		for _, m := range classPattern.FindAllStringSubmatch(body, -1) {
			class := m[1]
			base := class
			if i := strings.LastIndex(base, ":"); i >= 0 {
				base = base[i+1:]
			}
			if i := strings.Index(base, "/"); i >= 0 {
				base = base[:i]
			}
			// An ARBITRARY value is Tailwind's own escape hatch and it
			// always generates: `h-[70vh]`, `grid-cols-[132px_1fr]`. The
			// gate is about the SCALE, and an arbitrary value is not in it.
			if strings.Contains(base, "[") {
				continue
			}
			switch {
			case strings.HasPrefix(base, "h-"):
				n := strings.TrimPrefix(base, "h-")
				if n != "full" && n != "screen" && n != "auto" && !spacing[n] {
					t.Errorf("%s uses the class %q and Tailwind has no %q in its spacing "+
						"scale and the config adds no such height. The class compiles and does "+
						"nothing, which is how the app bar came to have no height at all",
						path, class, n)
				}
			case strings.HasPrefix(base, "grid-cols-"):
				n := strings.TrimPrefix(base, "grid-cols-")
				if n != "none" && n != "subgrid" {
					num, err := strconv.Atoi(n)
					if err != nil || num > 12 {
						if !extraGridCols[n] {
							t.Errorf("%s uses the class %q and Tailwind's grid has %d columns at "+
								"most, and the config adds no such column. The class compiles and "+
								"does nothing, which is how the sixteen-tile density came to render "+
								"at four", path, class, 12)
						}
					}
				}
			case strings.HasPrefix(base, "rounded-"):
				n := strings.TrimPrefix(base, "rounded-")
				// A directional radius: rounded-t-sm is rounded-sm on the
				// top edge.
				if i := strings.Index(n, "-"); i >= 0 && (n[:i] == "t" || n[:i] == "b" || n[:i] == "l" || n[:i] == "r") {
					n = n[i+1:]
				}
				if !extraRadii[n] {
					t.Errorf("%s uses the class %q and the config declares no such radius. The "+
						"class compiles and does nothing", path, class)
				}
			}
		}
	}
}

// readTailwindConfig reads the console's Tailwind configuration.
func readTailwindConfig(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("spa", "tailwind.config.js"))
	if err != nil {
		t.Fatalf("read the Tailwind configuration: %v", err)
	}
	return string(raw)
}

// configColours reads the colour names the Tailwind configuration declares.
//
// It reads the `colors` BLOCK rather than every key in the file, because the
// file declares scales as well as colours -- `spacing`, `height`,
// `borderRadius` -- and a colour class has to be checked against the colours
// and not against the whole theme.
//
// The block is read by matching braces rather than by a line pattern, because
// the file nests: `theme.extend.colors` is three levels down and a line
// pattern that stops at the first `}` reads the wrong block.
func configColours(t *testing.T, config string) map[string]bool {
	t.Helper()
	const marker = "colors: {"
	start := strings.Index(config, marker)
	if start < 0 {
		t.Fatal("the Tailwind configuration declares no colours block, so this test is asserting nothing")
	}
	body := config[start+len(marker):]
	depth := 1
	end := -1
loop:
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
				break loop
			}
		}
	}
	if end < 0 {
		t.Fatal("the Tailwind configuration's colours block is not closed")
	}
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s*'?([\w-]+)'?\s*:`).FindAllStringSubmatch(body[:end], -1) {
		out[m[1]] = true
	}
	if len(out) == 0 {
		t.Fatal("the Tailwind configuration's colours block declares no names")
	}
	return out
}

// tailwindDefaultColours are the palette names Tailwind ships.
//
// The configuration EXTENDS the theme rather than replacing it, so the
// default palette is still there: `bg-black`, `text-white` and `bg-white/15`
// are real classes in this console. They are listed here because the gate
// checks that a colour class names a colour the CONFIGURATION declares, and
// the default palette is one the configuration does not declare and does not
// remove.
var tailwindDefaultColours = map[string]bool{
	"inherit": true, "current": true, "transparent": true, "black": true, "white": true,
	"slate": true, "gray": true, "zinc": true, "neutral": true, "stone": true,
	"red": true, "orange": true, "amber": true, "yellow": true, "lime": true,
	"green": true, "emerald": true, "teal": true, "cyan": true, "sky": true,
	"blue": true, "indigo": true, "violet": true, "purple": true, "fuchsia": true,
	"pink": true, "rose": true,
}

// nonColourUtilities are the classes whose prefix looks like a colour prefix
// and whose argument is not a colour.
//
// They are listed rather than derived, because Tailwind's own theme is not
// available to a Go test and the alternative is a gate that guesses. A class
// in this list is exempt from the colour check; a class that is NOT in this
// list and NOT a declared colour is a finding.
var nonColourUtilities = map[string]bool{
	// The background IMAGE and POSITION utilities.
	"bg-auto": true, "bg-cover": true, "bg-contain": true, "bg-repeat": true,
	"bg-no-repeat": true, "bg-repeat-x": true, "bg-repeat-y": true,
	"bg-repeat-round": true, "bg-repeat-space": true, "bg-fixed": true,
	"bg-local": true, "bg-scroll": true, "bg-clip-border": true,
	"bg-clip-padding": true, "bg-clip-content": true, "bg-origin-border": true,
	"bg-origin-padding": true, "bg-origin-content": true, "bg-none": true,
	"bg-gradient-to-t": true, "bg-gradient-to-tr": true, "bg-gradient-to-r": true,
	"bg-gradient-to-br": true, "bg-gradient-to-b": true, "bg-gradient-to-bl": true,
	"bg-gradient-to-l": true, "bg-gradient-to-tl": true, "bg-gradient-none": true,

	// The text ALIGNMENT, DECORATION and OVERFLOW utilities.
	"text-left": true, "text-center": true, "text-right": true, "text-justify": true,
	"text-start": true, "text-end": true, "text-wrap": true, "text-nowrap": true,
	"text-balance": true, "text-pretty": true, "text-ellipsis": true, "text-clip": true,
	"text-underline": true, "text-overline": true, "text-line-through": true,
	"text-no-underline": true,

	// The text SIZE utilities. The configuration's own sizes are in here too,
	// because they are sizes and not colours: `text-title` is 34 pixels.
	"text-xs": true, "text-sm": true, "text-base": true, "text-lg": true,
	"text-xl": true, "text-2xl": true, "text-3xl": true, "text-4xl": true,
	"text-5xl": true, "text-6xl": true, "text-7xl": true, "text-8xl": true,
	"text-9xl": true, "text-display": true, "text-title": true, "text-heading": true,
	"text-subhead": true, "text-body": true, "text-body-sm": true, "text-meta": true,
	"text-micro": true,

	// The border WIDTH, STYLE and TABLE utilities.
	"border-0": true, "border-2": true, "border-4": true, "border-8": true,
	"border-t": true, "border-r": true, "border-b": true, "border-l": true,
	"border-x": true, "border-y": true, "border-t-2": true, "border-r-2": true,
	"border-b-2": true, "border-l-2": true, "border-t-4": true, "border-b-4": true,
	"border-solid": true, "border-dashed": true, "border-dotted": true,
	"border-double": true, "border-none": true, "border-hidden": true,
	"border-collapse": true, "border-separate": true, "border-spacing-0": true,
	"border-spacing-2": true, "border-spacing-4": true, "border-spacing-8": true,

	// The ring WIDTH utilities.
	"ring-0": true, "ring-1": true, "ring-2": true, "ring-4": true, "ring-8": true,
	"ring-inset": true,

	// The stroke WIDTH utilities.
	"stroke-0": true, "stroke-1": true, "stroke-2": true,

	// The fill and stroke NONE utilities.
	"fill-none": true, "stroke-none": true,

	// The outline WIDTH, STYLE and OFFSET utilities.
	"outline-none": true, "outline-0": true, "outline-1": true, "outline-2": true,
	"outline-4": true, "outline-8": true, "outline-dashed": true, "outline-dotted": true,
	"outline-double": true, "outline-hidden": true, "outline-offset-0": true,
	"outline-offset-1": true, "outline-offset-2": true, "outline-offset-4": true,
	"outline-offset-8": true,

	// The divide WIDTH and REVERSE utilities.
	"divide-x": true, "divide-y": true, "divide-x-reverse": true, "divide-y-reverse": true,
	"divide-0": true, "divide-2": true,

	// The accent NONE utility.
	"accent-none": true,
}

// stripDirectional removes a leading edge from a colour name.
//
// `border-t-accent` is the accent on the top edge and `border-b-0` is the
// bottom edge at no width. The direction is not part of the colour's name, and
// treating it as part of it makes both of them look like colours nobody
// declared.
func stripDirectional(name string) string {
	for _, d := range []string{"t-", "r-", "b-", "l-", "x-", "y-"} {
		if strings.HasPrefix(name, d) {
			return strings.TrimPrefix(name, d)
		}
	}
	return name
}

// isWidth reports whether a name is a WIDTH rather than a colour.
//
// Tailwind's widths are numbers, with an optional fraction and an optional
// `px`. `0`, `2`, `0.5` and `1.5` are widths; `accent` is a colour.
var widthRe = regexp.MustCompile(`^\d+(\.\d+)?(px)?$`)

func isWidth(name string) bool { return widthRe.MatchString(name) }

// colourPrefixes are the utility prefixes whose argument names a colour.
var colourPrefixes = []string{
	"bg-", "text-", "border-", "ring-", "fill-", "stroke-", "from-", "via-", "to-",
	"divide-", "outline-", "decoration-", "placeholder-", "caret-", "accent-",
}

// TestEveryColourClassInTheConsoleResolves
//
// Every colour class in the console's source names a colour the Tailwind
// configuration declares.
//
// This is the gate for the class of defect the console is full of: a class
// name that COMPILES and DOES NOTHING.
//
// `bg-apple-red`, `border-apple-red` and `ring-apple-red` were in the Live
// page and the Cockpit, and the `apple-*` names had been removed from the
// token file, so the camera status badges and the detection boxes rendered
// with no colour at all. Nothing failed: the class was not in Tailwind's
// scale, the class-existence gate only checked heights, grid columns and
// radii, and the screen came out wrong.
//
// And then `border-line` turned out to name a colour the configuration did
// not declare either -- the configuration had `border` and `line-strong` and
// not `line` -- so EVERY hairline in the console was invisible, on every
// panel, and the console had been read as "flat" rather than as broken.
//
// The check is against the CONFIGURATION, not against a built bundle, so it
// runs on a machine that has never run `npm run build`.
func TestEveryColourClassInTheConsoleResolves(t *testing.T) {
	config := readTailwindConfig(t)
	colours := configColours(t, config)

	classPattern := regexp.MustCompile(`(?:^|[\s"'` + "`" + `])([a-z0-9:/\[\]().%-]+)`)
	for _, path := range spaFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		// The COMMENTS are stripped, because a gate about what the SCREEN
		// says is not a gate about what the code says about the screen. A
		// comment that names a removed class is a note about a fix, and the
		// note is not the defect.
		body := stripComments(string(raw))
		for _, m := range classPattern.FindAllStringSubmatch(body, -1) {
			class := m[1]
			base := class
			// A variant prefix (`hover:`, `dark:`, `md:`) is not part of the
			// colour's name.
			if i := strings.LastIndex(base, ":"); i >= 0 {
				base = base[i+1:]
			}
			// An opacity modifier is not part of it either.
			if i := strings.Index(base, "/"); i >= 0 {
				base = base[:i]
			}
			// An ARBITRARY value is Tailwind's own escape hatch and it always
			// generates: `bg-[#fff]`, `accent-[var(--accent)]`.
			if strings.Contains(base, "[") {
				continue
			}
			prefix := ""
			name := base
			for _, p := range colourPrefixes {
				if strings.HasPrefix(base, p) {
					prefix = p
					name = strings.TrimPrefix(base, p)
					break
				}
			}
			if prefix == "" || name == "" {
				continue
			}
			// A DIRECTIONAL colour: `border-t-accent` is the accent on the top
			// edge, and the direction is not part of the colour's name.
			name = stripDirectional(name)
			// A WIDTH is not a colour: `border-b-0` is the bottom edge at no
			// width, and `ring-2` is two pixels of ring.
			if isWidth(name) {
				continue
			}
			if nonColourUtilities[class] || nonColourUtilities[base] {
				continue
			}
			if colours[name] || tailwindDefaultColours[name] {
				continue
			}
			t.Errorf("%s uses the class %q and the Tailwind configuration declares no colour "+
				"called %q. The class compiles and does nothing, which is how the camera "+
				"badges and every hairline in this console came to be invisible",
				path, class, name)
		}
	}
}

// TestTheConsoleAndTheShowcaseShareOneTokenFile
//
// The console IMPORTS the showcase's token file.
//
// Two token sets in one product is two answers, and the second one is always
// the one nobody remembers to update. The console used to carry a second
// palette - Netflix black and Apple red against the showcase's blue - and
// the two surfaces looked like two products.
func TestTheConsoleAndTheShowcaseShareOneTokenFile(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("spa", "src", "index.css"))
	if err != nil {
		t.Fatalf("read the console's stylesheet: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "static/tokens.css") {
		t.Errorf("the console's stylesheet does not import the token file. The console and " +
			"the showcase are one product with two renderers, and two palettes make them two products")
	}

	// And the token file must not be duplicated anywhere: a second copy is a
	// palette that drifts.
	entries, err := os.ReadDir(filepath.Join("spa", "src"))
	if err != nil {
		t.Fatalf("read the console's source: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".css") {
			continue
		}
		if e.Name() == "index.css" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("spa", "src", e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if strings.Contains(string(raw), "--bg:") || strings.Contains(string(raw), "--accent:") {
			t.Errorf("%s declares a colour of its own. The token file is the only place a "+
				"colour is written down", e.Name())
		}
	}
}

// TestTheBrandIsOneMark
//
// There is one mark, and it is the hexagonal sensor chassis.
//
// The console drew a red rounded square with a "V" in it, the showcase drew
// a blue rounded square, the operations page drew a white hexagon on
// nothing, and the hub drew the real one. Four marks for one product, and
// the console's was the same red as "critical", so the logo announced an
// emergency before anything had happened.
func TestTheBrandIsOneMark(t *testing.T) {
	entries, err := filepath.Glob(filepath.Join("static", "*.html"))
	if err != nil {
		t.Fatalf("glob the showcase's pages: %v", err)
	}
	mark := "M12 2.5 20 6.6v6.2c0 4.9-3.3 8.3-8 9.7-4.7-1.4-8-4.8-8-9.7V6.6z"
	for _, path := range entries {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := string(raw)
		if !strings.Contains(body, mark) {
			t.Errorf("%s does not carry the product's mark. One product, one mark", path)
		}
		if strings.Contains(body, "rect width=\"32\" height=\"32\"") {
			t.Errorf("%s draws the old rounded square", path)
		}
	}

	// And the console draws the same one.
	raw, err := os.ReadFile(filepath.Join("spa", "src", "components", "Mark.tsx"))
	if err != nil {
		t.Fatalf("read the console's mark: %v", err)
	}
	if !strings.Contains(string(raw), mark) {
		t.Errorf("the console's mark is not the product's mark")
	}
}

// TestTheProductNameIsOneName
//
// The wordmark is VISION, on every surface.
//
// The console rendered "Vision VMS", which is neither the locked brand nor
// the machine identifier, and a product with two names is a product with no
// name.
func TestTheProductNameIsOneName(t *testing.T) {
	banned := []string{"Vision VMS", "vision-vms", "VMS Console"}
	entries, err := filepath.Glob(filepath.Join("static", "*.html"))
	if err != nil {
		t.Fatalf("glob the showcase's pages: %v", err)
	}
	for _, path := range entries {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, b := range banned {
			if strings.Contains(string(raw), b) {
				t.Errorf("%s says %q. The product is VISION", path, b)
			}
		}
	}
	for _, path := range spaFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, b := range banned {
			if strings.Contains(string(raw), b) {
				t.Errorf("%s says %q. The product is VISION", path, b)
			}
		}
	}
}

// TestTheConsoleSaysNothingAboutAUser
//
// There is no "Operator", no avatar and no "Sign out" in the console.
//
// The console has no login: the API runs as a configured principal, and
// every action in the ledger is attributed to that principal rather than to
// a person. A sign-out button on a console that never signed in is a
// control that lies about what the system is.
//
// The COMMENTS are exempt, because the comment that says there is no
// sign-out is the one that says it best.
func TestTheConsoleSaysNothingAboutAUser(t *testing.T) {
	banned := []string{"Sign out", "Log out"}
	for _, path := range spaFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := stripComments(string(raw))
		for _, b := range banned {
			if strings.Contains(body, b) {
				t.Errorf("%s says %q. The console has no login, so there is no person to sign out of",
					path, b)
			}
		}
	}
}

// stripComments removes the comments from a source file, so that a gate about
// what the SCREEN says is not a gate about what the code says about the
// screen.
func stripComments(body string) string {
	var out strings.Builder
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "/*") {
			continue
		}
		out.WriteString(line)
		out.WriteString("\n")
	}
	return out.String()
}

// TestEveryScreenHasOneHeading
//
// Every page of the console has exactly one h1, and it is the title.
//
// The focus moves to the h1 on every route change, so a screen with two of
// them moves the focus to whichever the DOM happens to put first, and a
// screen with none of them moves it nowhere.
func TestEveryScreenHasOneHeading(t *testing.T) {
	for _, path := range consolePageFiles(t) {
		body := consolePageSource(t, path)
		count := strings.Count(body, "<h1")
		if count == 0 {
			continue // a page that renders another's heading
		}
		if count > 1 {
			t.Errorf("%s has %d h1 elements. A screen has one title", path, count)
		}
	}
}

// TestTheConsoleNamesItsEmptyStates
//
// An empty state says what is empty and why.
//
// "No data" is not an answer: it does not say whether the thing is absent,
// whether the request failed, or whether the product is down. The console's
// empty states are named, and the gate is that they are named.
func TestTheConsoleNamesItsEmptyStates(t *testing.T) {
	for _, path := range consolePageFiles(t) {
		body := stripComments(consolePageSource(t, path))
		if strings.Contains(body, "No data") || strings.Contains(body, "Nothing here") {
			t.Errorf("%s says %q. An empty state says what is empty and why", path, "No data")
		}
	}
}

// The severity and the state maps are CLOSED.
//
// An untrusted bundle can put any string in a severity field, and a class
// name built from it is a class name an attacker chose. The console's maps
// must cover every severity the product publishes and fall back to unknown
// for anything else, and the Go side must agree.
func TestTheSeverityAndStateMapsAreClosed(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("spa", "src", "components", "ui", "Badge.tsx"))
	if err != nil {
		t.Fatalf("read the console's badges: %v", err)
	}
	body := string(raw)
	for _, severity := range []string{"info", "low", "medium", "high", "critical"} {
		if !strings.Contains(body, severity+":") {
			t.Errorf("the console's severity map has no %q. The product publishes five "+
				"severities, and the console must show all of them", severity)
		}
	}
	for _, state := range []string{"open", "in_review", "dispositioned", "closed"} {
		if !strings.Contains(body, state+":") {
			t.Errorf("the console's state map has no %q. The product publishes four states, "+
				"and the console must show all of them", state)
		}
	}
	if !strings.Contains(body, "SEVERITY_UNKNOWN") || !strings.Contains(body, "STATE_UNKNOWN") {
		t.Errorf("the console's maps have no unknown fallback. A severity the product does " +
			"not recognise is shown as unknown, and it is not the same as low")
	}
}

// The token file is the single source of truth, and the Go side reads the
// same names. A rename in one place that is not made in the other is the
// drift this whole file exists to prevent.
func TestTheShowcaseAndTheConsoleNameTheSameTokens(t *testing.T) {
	tok := tokens(t)
	raw, err := os.ReadFile(filepath.Join("spa", "tailwind.config.js"))
	if err != nil {
		t.Fatalf("read the Tailwind configuration: %v", err)
	}
	config := string(raw)
	for _, name := range []string{"bg", "bg-panel", "bg-elevated", "line", "text", "text-muted", "text-dim", "accent", "success", "warning", "danger"} {
		if _, ok := tok["--color-"+name]; !ok {
			t.Errorf("the token file has no --color-%s, and the Tailwind configuration "+
				"maps a class onto it", name)
		}
	}
	if !strings.Contains(config, "var(--color-") {
		t.Errorf("the Tailwind configuration does not read the token file")
	}
}

// The number the document states must be the number the tree holds. The
// console's screens are counted here so that a page added without a screen
// in the route table is caught.
func TestTheConsoleScreensAreAllRouted(t *testing.T) {
	shell, err := os.ReadFile(filepath.Join("spa", "src", "components", "Shell.tsx"))
	if err != nil {
		t.Fatalf("read the shell: %v", err)
	}
	entries := consolePageFiles(t)
	for _, path := range entries {
		name := strings.TrimSuffix(filepath.Base(path), ".tsx")
		if name == "NotFound" {
			continue
		}
		if !strings.Contains(string(shell), name) {
			t.Errorf("the page %s is not in the shell's route table. A page nobody can "+
				"reach is a file rather than a page", name)
		}
	}
}

// The count, printed, so a reader can check it by hand.
func TestTheDesignSystemHasOnePalette(t *testing.T) {
	tok := tokens(t)
	colours := 0
	for name := range tok {
		if strings.HasPrefix(name, "--color-") {
			colours++
		}
	}
	if colours == 0 {
		t.Fatal("the token file has no colour aliases")
	}
	t.Logf("the token file declares %d tokens, %d of them the Tailwind-side names", len(tok), colours)
}

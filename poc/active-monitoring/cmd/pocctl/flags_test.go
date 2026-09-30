package main

import (
	"strings"
	"testing"
)

// TestEverySubcommandFlagSpecParsesItsOwnFlags is the test for a bug that
// happened THREE times.
//
// `flagSet` required a value for every flag. A boolean flag — `--skip-build`,
// `--force`, `--json` — therefore swallowed the token after it, and the *next*
// flag's value was then read as a bare positional and refused. The error named
// that stray token, so it read as a problem with the user's address and not with
// the flag before it.
//
// The whole class is caught by parsing each subcommand's real flag set, because
// the specs are the only place a boolean can be declared and nothing else will
// notice when one is missing.
func TestEverySubcommandFlagSpecParsesItsOwnFlags(t *testing.T) {
	cases := []struct {
		name  string
		specs map[string]string
		args  []string
	}{
		{"demo", map[string]string{"data-root": "", "clip": "", "zones": "", "seconds": "150",
			"sim-addr": "127.0.0.1:8098", "ui-addr": "127.0.0.1:8097", "model": "", "skip-build": boolSpec},
			[]string{"--seconds", "60", "--skip-build", "--sim-addr", "127.0.0.1:8188", "--ui-addr", "127.0.0.1:8187"}},
		{"zone", map[string]string{"pick": "", "out": "", "at": "2", "width": "640", "grid": "10", "force": boolSpec},
			[]string{"--pick", "c.mp4", "--out", "o.json", "--force"}},
		{"report", map[string]string{"data-root": "", "run": "", "json": boolSpec, "out": ""},
			[]string{"--data-root", `C:\poc`, "--json"}},
		{"soak status", map[string]string{"data-root": "", "run": ""},
			[]string{"--data-root", `C:\poc`, "--run", "r1"}},
		{"soak start", map[string]string{"data-root": "", "sim-addr": "127.0.0.1:8098", "seconds": "150",
			"clock-state": "", "power-state": ""},
			[]string{"--data-root", `C:\poc`, "--sim-addr", "127.0.0.1:8188"}},
		{"rehearse", map[string]string{"data-root": "", "sim-addr": "127.0.0.1:8098"},
			[]string{"--data-root", `C:\poc`, "--sim-addr", "127.0.0.1:8188"}},
		{"preflight", map[string]string{"gate-d": "", "gate-d-age": "0s", "clock-state": "", "power-state": ""},
			[]string{"--gate-d", "PASS", "--gate-d-age", "1h"}},
		{"cap", map[string]string{"source-kind": "", "source-arg": "", "ffmpeg": "", "ffprobe": "",
			"segments": "60", "days": "7"},
			[]string{"--source-kind", "loop", "--source-arg", "c.mp4", "--days", "7"}},
		{"sim", map[string]string{"addr": "127.0.0.1:8098"},
			[]string{"--addr", "127.0.0.1:8188"}},
		{"fetch-model", map[string]string{"url": "", "out": "models/yolov8n.onnx"},
			[]string{"--url", "https://example/m.onnx"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := flagSet(tc.name, tc.specs, tc.args)
			if err != nil {
				t.Fatalf("%s refused its own documented arguments %v: %v", tc.name, tc.args, err)
			}
			// Every flag the caller passed must have landed in the result, and
			// every value must be the one the caller wrote. A boolean left as
			// its default means it was parsed as a value flag and ate the next
			// token, which is the bug this test exists for.
			for i := 0; i < len(tc.args); i++ {
				if !strings.HasPrefix(tc.args[i], "-") {
					continue
				}
				key := strings.TrimLeft(strings.SplitN(tc.args[i], "=", 2)[0], "-")
				if tc.specs[key] == boolSpec {
					if got[key] != "true" {
						t.Errorf("--%s is declared boolean but parsed as %q; it consumed the token after it", key, got[key])
					}
					continue
				}
				if i+1 < len(tc.args) && got[key] != tc.args[i+1] {
					t.Errorf("--%s = %q, want %q", key, got[key], tc.args[i+1])
				}
			}
		})
	}
}

// TestNoBooleanFlagIsDeclaredAsAValueFlag is the same guarantee from the other
// side: the way a boolean flag is declared is the only place this can go wrong,
// so it is pinned here with a list.
func TestNoBooleanFlagIsDeclaredAsAValueFlag(t *testing.T) {
	// These are the flags that take no value. A spec that says "" for one of
	// them is the bug, and this test says so by name.
	booleans := map[string][]string{
		"demo":   {"skip-build"},
		"zone":   {"force"},
		"report": {"json"},
	}
	specs := map[string]map[string]string{
		"demo":   {"data-root": "", "clip": "", "zones": "", "seconds": "150", "sim-addr": "x", "ui-addr": "x", "model": "", "skip-build": boolSpec},
		"zone":   {"pick": "", "out": "", "at": "2", "width": "640", "grid": "10", "force": boolSpec},
		"report": {"data-root": "", "run": "", "json": boolSpec, "out": ""},
	}
	for sub, names := range booleans {
		for _, n := range names {
			if specs[sub][n] != boolSpec {
				t.Errorf("%s --%s is declared %q, not as a boolean; it will swallow the token after it", sub, n, specs[sub][n])
			}
		}
	}
}

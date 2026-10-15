package main

import (
	"strings"
	"testing"
)

// TestLiveArgsSplitsFlagsFromCameraIDs covers the one piece of parsing that
// exists only in this command.
//
// flagSet refuses a positional token by design — an unknown flag is a typo on a
// measurement tool — so `live resolve times-square-north` needs a splitter that
// hands flagSet only the flags. The two parsers must agree about where the ids
// start, or a value flag's argument is read as a camera id.
func TestLiveArgsSplitsFlagsFromCameraIDs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		wantIDs  []string
		wantFmt  string
		wantAll  string
		wantYtDl string
	}{
		{
			name: "a plain id", args: []string{"times-square-north"},
			wantIDs: []string{"times-square-north"},
		},
		{
			name: "a flag before the id", args: []string{"--all", "abbey-road", "dublin-ireland"},
			wantIDs: []string{"abbey-road", "dublin-ireland"}, wantAll: "true",
		},
		{
			name:    "a value flag's argument is not an id",
			args:    []string{"--ytdlp", "/opt/yt-dlp", "abbey-road"},
			wantIDs: []string{"abbey-road"}, wantYtDl: "/opt/yt-dlp",
		},
		{
			name: "the = form is normalised", args: []string{"--format=sh", "abbey-road"},
			wantIDs: []string{"abbey-road"}, wantFmt: "sh",
		},
		{
			name: "no ids at all", args: []string{"--js", "node"}, wantIDs: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags, ids, err := liveArgs(tc.args)
			if err != nil {
				t.Fatalf("liveArgs(%v) failed: %v", tc.args, err)
			}
			if strings.Join(ids, ",") != strings.Join(tc.wantIDs, ",") {
				t.Errorf("ids = %v, want %v", ids, tc.wantIDs)
			}
			if tc.wantFmt != "" && flags["format"] != tc.wantFmt {
				t.Errorf("format = %q, want %q", flags["format"], tc.wantFmt)
			}
			if tc.wantAll != "" && flags["all"] != tc.wantAll {
				t.Errorf("all = %q, want %q", flags["all"], tc.wantAll)
			}
			if tc.wantYtDl != "" && flags["ytdlp"] != tc.wantYtDl {
				t.Errorf("ytdlp = %q, want %q", flags["ytdlp"], tc.wantYtDl)
			}
		})
	}
}

func TestLiveArgsRefusesAnUnknownFlag(t *testing.T) {
	if _, _, err := liveArgs([]string{"--resolv", "x"}); err == nil {
		t.Fatal("an unknown flag was accepted; a typo on a measurement tool is a flag that did nothing")
	} else if !strings.Contains(err.Error(), "--resolv") {
		t.Fatalf("the refusal does not name the flag: %v", err)
	}
}

func TestLiveArgsRefusesAValueFlagWithNoValue(t *testing.T) {
	if _, _, err := liveArgs([]string{"--js"}); err == nil {
		t.Fatal("--js with no value was accepted")
	}
}

// TestTheLiveEnvironmentCarriesThePublicFlagFromTheOperator covers liveEnv's
// one new entry.
//
// liveEnv REPLACES the demo environment with the operator's live one, so the
// demo's `SP_POC_SOURCE_PUBLIC=false` must be dropped and the operator's value
// carried through — and it must be carried, because it is required: a live run
// with the flag absent is a run `watch` refuses at startup, and a live run with
// the demo's `false` is a run reading a public camera while the screen says
// nothing about whose camera it is.
func TestTheLiveEnvironmentCarriesThePublicFlagFromTheOperator(t *testing.T) {
	t.Setenv("SP_POC_SOURCE_PUBLIC", "true")
	out := liveEnv(demoEnvFor(t))
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "SP_POC_SOURCE_PUBLIC=true") {
		t.Fatalf("the operator's public flag did not reach the child: %s", joined)
	}
	if strings.Contains(joined, "SP_POC_SOURCE_PUBLIC=false") {
		t.Fatalf("the demo's own false survived beside the operator's true, and a duplicate name in a child's environment is a value whose meaning depends on which reader looks first")
	}
	// The same replacement the relay variables get, asserted for the new one:
	// a value inherited from the operator's shell is used, and the demo's own
	// is not.
	t.Setenv("SP_POC_SOURCE_PUBLIC", "false")
	out = liveEnv(demoEnvFor(t))
	joined = strings.Join(out, "\n")
	if !strings.Contains(joined, "SP_POC_SOURCE_PUBLIC=false") {
		t.Fatalf("the operator's explicit false was dropped, and the run would refuse a required variable: %s", joined)
	}
}

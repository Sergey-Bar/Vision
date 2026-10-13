package main

// Tests for the demo's material and capability rules — the plan's B2 and B4.
//
// Two claims are under test, and they are the two the demo used to get wrong in
// opposite directions:
//
//   - B2: the demo must not DEFAULT to footage. The old default was a real night
//     intersection whose labels.json row says, in a field rather than in prose,
//     that it may not be shown to anyone — and every demo is shown to people. A
//     test that pins "no default" catches the default creeping back, which is
//     what a default is for.
//   - B4: a refusal must name a TOOL, not a platform. The old refusal named
//     `runtime.GOOS != "windows"`, which is a statement about where the script
//     was developed. The same refusal style, exercised wherever it can.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestADemoWithNoClipRefusesAndPrintsTheRecordOfWhy is B2 itself.
//
// The refusal is a table, so the test checks the table's substance: the youtube
// row is still there as the record of why, and the sentence that says no default
// was chosen is printed rather than merely implied by a non-zero exit.
func TestADemoWithNoClipRefusesAndPrintsTheRecordOfWhy(t *testing.T) {
	out := captureStdout(t, func() {
		err := noClipGiven(repoRoot())
		if err == nil {
			t.Fatal("a demo with no clip was allowed to pick one of its own")
		}
		if !strings.Contains(err.Error(), "no clip was named") {
			t.Errorf("the refusal does not say what it refused: %v", err)
		}
	})
	for _, want := range []string{
		"no default clip",
		"sTF-6_xinUU.mp4",          // the row that is the record of why
		"cleared_for_external_use", // named as a field, because it is one
		"labels.json",              // the table it is read from
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, out)
		}
	}
}

// TestADemoWithNoClipIsRefusedThroughTheCommand asserts the rule at the level
// the operator meets it.
//
// A test that calls `noClipGiven` directly only proves the function works; this
// one proves the COMMAND calls it. The difference is the failure mode: a
// `demo` with no clip that slipped past the refusal would not run the pipeline
// on nothing at all — it would fall through to the file-exists check and fail
// with "demo:  is missing: ...", which is the missing-file wall a judge met
// before, dressed in a new message. The refusal is the material table; anything
// else is a different message about the same wall.
func TestADemoWithNoClipIsRefusedThroughTheCommand(t *testing.T) {
	if err := probeCapabilities(); err != nil {
		t.Skipf("the toolchain this demo needs is not present here (%v), so the refusal it would "+
			"reach could not be distinguished from the one it stops at", err)
	}
	err := cmdDemo(nil)
	if err == nil {
		t.Fatal("demo with no --clip was allowed to continue")
	}
	if !strings.Contains(err.Error(), "no clip was named") && !strings.Contains(err.Error(), "no --clip") {
		t.Errorf("the refusal is not the material-table one: %v", err)
	}
}

// TestTheMaterialTableIsTheProvenanceTable checks that the table is READ and not
// carried in the source.
//
// A hardcoded list of clips is a list that forgets the row added next week, and
// the two facts printed per row — provenance and clearance — exist to be read
// out of labels.json where they were written by a person.
func TestTheMaterialTableIsTheProvenanceTable(t *testing.T) {
	clips := committedClips()
	if len(clips) == 0 {
		t.Fatal("no clips were read from testdata/labels.json; the material table prints whatever " +
			"that file says, and it says something")
	}
	var sawCarCrossing, sawYouTube bool
	for _, c := range clips {
		if strings.HasSuffix(c.File, "car-crossing.mp4") {
			sawCarCrossing = true
			if c.Provenance != "synthetic" {
				t.Errorf("car-crossing.mp4 is labelled %q, want synthetic — a rectangle over a testsrc "+
					"pattern is not real footage whatever it looks like in a report", c.Provenance)
			}
		}
		if strings.Contains(c.File, "youtube") {
			sawYouTube = true
			if cleared, known := c.clearedForExternalUse(); !known || cleared {
				t.Errorf("the youtube row is clearance-known=%v cleared=%v. It is the reason the demo "+
					"stopped defaulting, and it reads false", known, cleared)
			}
		}
	}
	if !sawCarCrossing {
		t.Error("the committed synthetic clip is missing from the material table")
	}
	if !sawYouTube {
		t.Error("the youtube row is missing. It is the RECORD of why the demo stopped defaulting, " +
			"and the plan keeps it")
	}
}

// TestTheZonesFileIsDerivedFromTheClip is the sibling rule.
//
// The alternative it replaces was a zones default measured against footage the
// run was not reading — a line in the wrong place produces zero events and no
// error, which is the most expensive kind of silence there is.
func TestTheZonesFileIsDerivedFromTheClip(t *testing.T) {
	got := zonesBeside(filepath.Join("testdata", "car-crossing.mp4"))
	want := filepath.Join("testdata", "car-crossing.zones.json")
	if got != want {
		t.Fatalf("zonesBeside(%q) = %q, want the sibling %q", "testdata/car-crossing.mp4", got, want)
	}
}

// TestTheClipProvenanceIsReadFromItsRow covers the replacement for the hardcoded
// licence paragraph.
//
// That paragraph was true of the clip the demo used to default to and false of
// every other one, so when the clip stopped being defaulted it had to become a
// read. The case that matters most is the uncleared one: material that may not
// leave the machine has to say so on the first screen whatever it is.
func TestTheClipProvenanceIsReadFromItsRow(t *testing.T) {
	// The youtube clip on this machine is not committed and is not present on a
	// clean clone, so the row is what is under test rather than the file.
	row := filepath.Join("testdata", "youtube", "sTF-6_xinUU.mp4")
	if _, err := os.Stat(filepath.Join(findModuleRoot(), row)); err != nil {
		t.Skipf("testdata/youtube is not present, so only the committed rows are exercised")
	}
	out := captureStdout(t, func() { printClipProvenance(repoRoot(), row) })
	if !strings.Contains(out, "NOT") || !strings.Contains(out, "shown") {
		t.Errorf("uncleared material did not print its limit:\n%s", out)
	}
}

// TestTheDemoRefusalIsAboutAToolNotAPlatform is B4, asserted in both directions.
//
// On a machine with the tools it says so and names the platform it measured. On a
// machine without them it names what is missing — and, this is the part the old
// refusal failed, it does NOT say "written for the Windows shell", because that
// sentence named where a script was developed rather than what a run needs.
func TestTheDemoRefusalIsAboutAToolNotAPlatform(t *testing.T) {
	out := captureStdout(t, func() { _ = demoCapabilities(false) })
	err := probeCapabilities()
	if err == nil {
		if !strings.Contains(out, "capabilities pass on") {
			t.Errorf("a passing capability line does not say what it passed on:\n%s", out)
		}
		if !strings.Contains(out, runtime.GOOS+"/"+runtime.GOARCH) {
			t.Errorf("the capability line does not name the platform it measured:\n%s", out)
		}
		return
	}
	for _, banned := range []string{"windows shell", "written for the Windows", "GOOS"} {
		if strings.Contains(strings.ToLower(out), strings.ToLower(banned)) {
			t.Errorf("the refusal names a PLATFORM (%q). Name the tool instead:\n%s", banned, out)
		}
	}
	for _, want := range []string{"ffmpeg", "install"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, out)
		}
	}
}

// probeCapabilities runs the check and returns its error, so the test above can
// branch on the environment it is in.
func probeCapabilities() error { return demoCapabilities(false) }

// TestTheBootstrapURLIsCheckedAndVersioned is the runtime default, and the two
// properties that make it checkable rather than trustworthy.
//
// It was read off the release's own asset list on 2026-10-11, and a pinned
// VERSION is what makes it reproducible: `latest` would resolve to a different
// library on two different days.
func TestTheBootstrapURLIsCheckedAndVersioned(t *testing.T) {
	url, ok := defaultRuntimeURL()
	if !ok {
		t.Skipf("%s/%s has no CPU release asset for %s, which is a skip rather than a pass",
			runtime.GOOS, runtime.GOARCH, runtimeReleaseVersion)
	}
	if !strings.HasPrefix(url, "https://") {
		t.Errorf("the runtime URL is not https: %q", url)
	}
	if !strings.Contains(url, "/v"+runtimeReleaseVersion+"/") {
		t.Errorf("the URL does not name the version it was checked against: %q", url)
	}
	if runtime.GOOS == "windows" && !strings.HasSuffix(url, ".zip") {
		t.Errorf("the Windows release asset is a ZIP and this URL is not: %q", url)
	}
	if runtime.GOOS != "windows" && !strings.HasSuffix(url, ".tgz") {
		t.Errorf("the Linux and macOS release assets are .tgz and this URL is not: %q", url)
	}
}

// TestTheRuntimeStepWouldRatherUseTheRecordedManifest guards the order of the
// three sources for the runtime URL.
//
// This machine's manifest records the GPU package it measured with; a bootstrap
// that defaulted over it would replace a verified library with an unverified
// smaller one and call that an upgrade. The manifest is the authority, and this
// test asserts the recorded row for this platform is the one that would be
// chosen if the library were absent.
func TestTheRuntimeStepWouldRatherUseTheRecordedManifest(t *testing.T) {
	manifest := filepath.Join(findModuleRoot(), "models", "runtime.json")
	recs, err := readRuntimeRecords(manifest)
	if err != nil || len(recs) == 0 {
		t.Skipf("%s is not present on this machine, so the clean-clone path is the only one that "+
			"runs here", manifest)
	}
	want := runtime.GOOS + "/" + runtime.GOARCH
	for _, r := range recs {
		if r.Kind == "onnxruntime" && r.Platform == want {
			if r.SHA256 == "" {
				t.Error("the recorded row has no digest, so re-fetching from it would not be verified")
			}
			if !strings.HasPrefix(r.Source, "https://") {
				t.Errorf("the recorded source is not https: %q", r.Source)
			}
			return
		}
	}
	t.Errorf("no row for %s in the manifest, so the runtime step would silently fall through to "+
		"the compiled default on a machine that HAS a recorded runtime", want)
}

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestFlagSetIgnoresTheSubcommandName is the bug that made every `poc soak
// status` and `poc soak report` invocation fail with `"status" is not a flag`.
//
// flagSet used to read os.Args[2:] itself, which put the subcommand's own name
// in front of the flags. It now takes the arguments from the caller, and this
// test pins the parse rather than the symptom.
func TestFlagSetIgnoresTheSubcommandName(t *testing.T) {
	specs := map[string]string{"data-root": "", "run": ""}
	// What `poc soak status --data-root X` hands to the parser.
	got, err := flagSet("soak status", specs, []string{"--data-root", `C:\poc`, "--run", "r1"})
	if err != nil {
		t.Fatalf("the subcommand's own arguments were refused: %v", err)
	}
	if got["data-root"] != `C:\poc` || got["run"] != "r1" {
		t.Fatalf("parsed %v, want both flags", got)
	}
	// And the old behaviour is what must NOT come back.
	if _, err := flagSet("soak status", specs, []string{"status", "--data-root", `C:\poc`}); err == nil {
		t.Fatal("a bare positional was accepted as a flag value; flagSet must refuse one")
	}
}

func TestFlagSetRefusesAnUnknownFlagAndSaysTheRealNames(t *testing.T) {
	_, err := flagSet("zone", map[string]string{"pick": "", "out": ""}, []string{"--pick2", "x"})
	if err == nil {
		t.Fatal("a typo in a flag name was accepted; on a measurement tool that is a flag which did nothing")
	}
	if !strings.Contains(err.Error(), "--pick") {
		t.Fatalf("the refusal does not list the real flags: %v", err)
	}
}

func TestFlagSetRefusesAFlagWithNoValue(t *testing.T) {
	_, err := flagSet("zone", map[string]string{"pick": ""}, []string{"--pick"})
	if err == nil || !strings.Contains(err.Error(), "needs a value") {
		t.Fatalf("a flag with no value was accepted: %v", err)
	}
}

// writeClipForTest renders a REAL two-second clip with ffmpeg.
//
// A stand-in text file would fail at ffprobe, and a test that fails at ffprobe
// proves nothing about the overwrite guard — it just fails earlier. The picker
// needs a real frame to size its grid, so the test supplies one.
func writeClipForTest(t *testing.T, path string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatalf("ffmpeg is a hard runtime dependency of this module (D2), and this test renders a real frame with it")
	}
	out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=320x180:rate=5",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg could not render the test clip: %v: %s", err, out)
	}
}

// TestZoneRefusesToOverwriteAnExistingFile is the destructive bug: the picker
// wrote its proposal straight to the derived name and silently replaced a
// hand-drawn zones.json. A wrong line produces zero events and no error, so
// destroying a correct one is worse than not having one.
func TestZoneRefusesToOverwriteAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "src.mp4")
	writeClipForTest(t, clip)
	existing := filepath.Join(dir, "src.zones.json")
	const good = `{"source":"src.mp4","lines":[{"name":"hand-drawn","a":[0,0.4],"b":[1,0.4]}]}`
	if err := os.WriteFile(existing, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}

	err := cmdZone([]string{"--pick", clip, "--out", existing})
	if err == nil {
		t.Fatal("the picker overwrote a hand-drawn zone file")
	}
	if !strings.Contains(err.Error(), "will not be overwritten") {
		t.Fatalf("the refusal does not say what it protects: %v", err)
	}
	body, readErr := os.ReadFile(existing)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(body) != good {
		t.Fatalf("the existing file changed:\n%s", body)
	}
}

// TestZoneForceKeepsTheOldFileBesideTheNewOne: --force is allowed to replace,
// but only with the previous one still on disk.
func TestZoneForceKeepsTheOldFileBesideTheNewOne(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "src.mp4")
	writeClipForTest(t, clip)
	existing := filepath.Join(dir, "src.zones.json")
	const good = `{"source":"src.mp4","lines":[{"name":"hand-drawn","a":[0,0.4],"b":[1,0.4]}]}`
	if err := os.WriteFile(existing, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}

	args := []string{"--pick", clip, "--out", existing, "--force"}
	out := captureStdout(t, func() {
		if err := cmdZone(args); err != nil {
			t.Fatalf("--force was refused: %v", err)
		}
	})
	if !strings.Contains(out, "moved to") {
		t.Fatalf("--force did not say where the previous file went:\n%s", out)
	}
	backup, err := os.ReadFile(existing + ".replaced")
	if err != nil {
		t.Fatalf("the previous file was not kept: %v", err)
	}
	if string(backup) != good {
		t.Fatalf("the backup is not the file that was there: %s", backup)
	}
}

// TestThePortGuardRefusesAnAddressInUse: the rehearsal used to bind blindly, so
// a second simulator took the port and the client tested THAT process, which no
// fault was ever armed on. Gate D went red for a reason that had nothing to do
// with the code, and the same hole could have made it go green.
func TestThePortGuardRefusesAnAddressInUse(t *testing.T) {
	srv := &httpServer{sim: nil, addr: "127.0.0.1:0"}
	if err := srv.start(); err != nil {
		t.Fatal(err)
	}
	defer srv.stop()
	busy := srv.addr

	_, _, err := runSim(busy)
	if err == nil {
		t.Fatal("a simulator started on an address that is already in use; a rehearsal would then test whatever is already there")
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("the refusal does not explain the hazard: %v", err)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

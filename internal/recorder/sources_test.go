package recorder_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/recorder"
)

// The camera list.
//
// The list holds CREDENTIALS, so the tests below are about the two things
// that matter about a file with credentials in it: it is complete, and it is
// not readable by anyone but its owner.

// writeList writes a camera list and returns its path.
func writeList(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cameras.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write the camera list: %v", err)
	}
	return path
}

// The list that a working install has.
const goodList = `[
  {"tenantId":"acme","siteId":"site-1","deviceId":"cam-1","url":"rtsp://192.168.1.10:554/stream","user":"admin","password":"secret"},
  {"tenantId":"acme","siteId":"site-1","deviceId":"cam-2","url":"rtsp://192.168.1.11:554/stream"}
]`

func TestTheCameraListLoads(t *testing.T) {
	sources, err := recorder.LoadSources(writeList(t, goodList))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if sources.Len() != 2 {
		t.Fatalf("the list holds %d camera(s), want 2", sources.Len())
	}
	all := sources.All()
	if all[0].DeviceID != "cam-1" {
		t.Errorf("the first camera is %q, want cam-1", all[0].DeviceID)
	}
	if all[0].User != "admin" || all[0].Password != "secret" {
		t.Error("the camera's credential did not survive the load")
	}
}

func TestAnEmptyCameraListIsRefused(t *testing.T) {
	// A recorder with no camera records nothing, and a recorder that starts
	// happily with no camera reports a site as recording while every camera
	// is dark.
	_, err := recorder.LoadSources(writeList(t, "[]"))
	if err == nil {
		t.Fatal("an empty camera list was accepted")
	}
	if !errors.Is(err, recorder.ErrNoSources) {
		t.Errorf("the refusal is not the empty-list refusal: %v", err)
	}
}

func TestAMalformedCameraListIsRefused(t *testing.T) {
	_, err := recorder.LoadSources(writeList(t, "this is not json"))
	if err == nil {
		t.Fatal("a malformed camera list was accepted")
	}
	if !strings.Contains(err.Error(), "JSON") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
}

func TestAnIncompleteCameraIsRefused(t *testing.T) {
	// Each of the four is a different mistake and each is named, because a
	// camera that is silently skipped is a camera the site believes is
	// recording.
	cases := []struct {
		name string
		body string
		want string
	}{
		{"no tenant", `[{"siteId":"s","deviceId":"d","url":"rtsp://h/x"}]`, "tenantId"},
		{"no site", `[{"tenantId":"t","deviceId":"d","url":"rtsp://h/x"}]`, "siteId"},
		{"no device", `[{"tenantId":"t","siteId":"s","url":"rtsp://h/x"}]`, "deviceId"},
		{"no url", `[{"tenantId":"t","siteId":"s","deviceId":"d"}]`, "url"},
		{"not rtsp", `[{"tenantId":"t","siteId":"s","deviceId":"d","url":"http://h/x"}]`, "RTSP"},
		{"no host", `[{"tenantId":"t","siteId":"s","deviceId":"d","url":"rtsp:///x"}]`, "host"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := recorder.LoadSources(writeList(t, c.body))
			if err == nil {
				t.Fatalf("a camera with %s was accepted", c.name)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the refusal does not name %q: %v", c.want, err)
			}
		})
	}
}

func TestACameraListNobodyElseMayReadIsRefusedWhereTheModeDecides(t *testing.T) {
	// The camera list holds credentials. A file that every local account
	// can read is a credential on every account on the host.
	//
	// On POSIX the mode IS the control, so the file is refused. On Windows
	// the mode is advisory and the ACL is the real control, so the same
	// condition is a warning — refusing an advisory condition would make the
	// product unstartable there, and silently accepting it would make a
	// genuinely exposed file look protected.
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		t.Skip("the mode bits do not decide file access on this platform")
	}
	path := writeList(t, goodList)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	_, err := recorder.LoadSources(path)
	if err == nil {
		t.Fatal("a world-readable camera list was accepted")
	}
	if !strings.Contains(err.Error(), "readable by anyone other than its owner") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

func TestACameraListOnlyItsOwnerMayReadIsAccepted(t *testing.T) {
	// The control has two directions and a test that only proves the refusal
	// proves that the file is rejected, not that the rule is the rule.
	path := writeList(t, goodList)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if _, err := recorder.LoadSources(path); err != nil {
		t.Fatalf("an owner-only camera list was refused: %v", err)
	}
}

func TestAMissingCameraListIsRefused(t *testing.T) {
	_, err := recorder.LoadSources(filepath.Join(t.TempDir(), "nothing.json"))
	if err == nil {
		t.Fatal("a camera list that is not there was accepted")
	}
}

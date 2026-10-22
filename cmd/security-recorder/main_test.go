package main

import (
	"strings"
	"testing"
)

// The recorder binary.
//
// The binary's own job is narrow: read the configuration, open the database,
// load the camera list, and record. Everything it can get wrong at startup
// it can get wrong LOUDLY, because a recorder that starts with no recording
// root and no camera list reports a site as recording while every camera is
// dark.
//
// So the refusals are the tests.

// withoutConfig clears the recorder's configuration for the duration of a
// test.
//
// The variables are read through `os.Getenv`, so they are set in the
// environment rather than through a lookup — the same path the daemon takes.
func withoutConfig(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"SP_RECORDING_ROOT", "SP_RECORDER_SOURCES",
		"SP_DATABASE_URL", "SP_DATABASE_PASSWORD",
	} {
		t.Setenv(name, "")
	}
}

func TestTheRecorderRefusesToStartWithNoRecordingRoot(t *testing.T) {
	withoutConfig(t)
	err := run()
	if err == nil {
		t.Fatal("the recorder started with no recording root")
	}
	if !strings.Contains(err.Error(), "SP_RECORDING_ROOT") {
		t.Errorf("the refusal does not name the variable: %v", err)
	}
}

func TestTheRecorderRefusesToStartWithNoCameraList(t *testing.T) {
	withoutConfig(t)
	t.Setenv("SP_RECORDING_ROOT", "/var/lib/security-platform/recordings")
	err := run()
	if err == nil {
		t.Fatal("the recorder started with no camera list")
	}
	if !strings.Contains(err.Error(), "SP_RECORDER_SOURCES") {
		t.Errorf("the refusal does not name the variable: %v", err)
	}
}

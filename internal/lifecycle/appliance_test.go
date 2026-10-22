package lifecycle_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/lifecycle"
)

// The appliance.
//
// An appliance is ONE BOX: the core, the recorder, one recording root and one
// camera list. The recorder is a separate SERVICE on purpose — it opens
// sockets to cameras and writes to a disk, and a camera that stops answering
// must not take the console down with it.

// applianceOpts builds the options an appliance install uses.
func applianceOpts(t *testing.T) lifecycle.UnitOptions {
	t.Helper()
	o := opts(t)
	o.Appliance = true
	o.RecordingRoot = filepath.Join(filepath.Dir(o.ProgramDir), "recordings")
	o.CameraList = filepath.Join(filepath.Dir(o.EnvFile), "cameras.json")
	return o
}

func TestTheApplianceInstallsTheRecorderAsItsOwnService(t *testing.T) {
	o := applianceOpts(t)
	unit := written(t, o, "security-recorder.service")

	// It points at the recorder binary in the program directory it was
	// given, so an install into a non-default location actually runs.
	exec := filepath.ToSlash(filepath.Join(o.ProgramDir, "security-recorder"))
	if !strings.Contains(unit.Content, "ExecStart="+exec) {
		t.Errorf("the recorder unit does not exec %q:\n%s", exec, unit.Content)
	}

	// And it reads the environment file the core reads, so the tenant and
	// the database are the SAME ones.
	if !strings.Contains(unit.Content, "EnvironmentFile=-"+filepath.ToSlash(o.EnvFile)) {
		t.Errorf("the recorder unit does not read the environment file it was given:\n%s", unit.Content)
	}
}

func TestTheRecorderUnitMayWriteToTheRecordingRootAndNothingElse(t *testing.T) {
	// The recorder WRITES, and it writes to the recording root and to
	// nothing else. A recorder that can write anywhere the service account
	// can write is a recorder that can overwrite the evidence.
	o := applianceOpts(t)
	unit := written(t, o, "security-recorder.service")

	if !strings.Contains(unit.Content, "ReadWritePaths="+filepath.ToSlash(o.RecordingRoot)) {
		t.Errorf("the recorder unit does not confine its writes to the recording root:\n%s", unit.Content)
	}
	// And it keeps the core's hardening.
	for _, directive := range []string{
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
		"ProtectHome=true",
		"CapabilityBoundingSet=",
	} {
		if !strings.Contains(unit.Content, directive) {
			t.Errorf("the recorder unit lost %q:\n%s", directive, unit.Content)
		}
	}
}

func TestTheRecorderUnitIsNotWrittenWithoutTheAppliance(t *testing.T) {
	// A site with a recorder on ANOTHER machine does not want a recorder
	// unit on the core's host, and the choice is the operator's.
	o := opts(t)
	units, err := lifecycle.WriteUnits(o)
	if err != nil {
		t.Fatalf("WriteUnits: %v", err)
	}
	for _, u := range units {
		if u.Name == "security-recorder.service" {
			t.Fatal("a recorder unit was written for a plain install")
		}
	}
}

func TestTheEnvironmentFileNamesTheRecordersVariables(t *testing.T) {
	// The env file is where the operator's configuration lives, and the
	// recorder's variables belong in it: a recorder configured by a
	// variable nobody documented is a recorder nobody can configure.
	o := applianceOpts(t)
	unit := written(t, o, filepath.Base(o.EnvFile))

	for _, want := range []string{
		"SP_RECORDING_ROOT=",
		"SP_RECORDER_SOURCES=",
		"SP_RECORDER_SEGMENT_SECONDS=",
	} {
		if !strings.Contains(unit.Content, want) {
			t.Errorf("the environment file does not name %q:\n%s", want, unit.Content)
		}
	}
}

func TestTheEnvironmentFileIsPreservedOnAReInstall(t *testing.T) {
	// The env file is where the tenant id and the database password live, and
	// an operator who has edited theirs must not have their file replaced by
	// a re-install.
	o := applianceOpts(t)
	if _, err := lifecycle.WriteUnits(o); err != nil {
		t.Fatalf("WriteUnits: %v", err)
	}
	edited := "SP_TENANT_ID=edited-by-hand\n"
	if err := os.WriteFile(o.EnvFile, []byte(edited), 0o640); err != nil {
		t.Fatalf("edit the env file: %v", err)
	}
	if _, err := lifecycle.WriteUnits(o); err != nil {
		t.Fatalf("WriteUnits: %v", err)
	}
	body, err := os.ReadFile(o.EnvFile)
	if err != nil {
		t.Fatalf("read the env file: %v", err)
	}
	if string(body) != edited {
		t.Errorf("the re-install replaced the operator's env file:\n%s", string(body))
	}
}

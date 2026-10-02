package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The service unit is a deployment artefact, and no production code reads it.
//
// Which is why it went wrong without anything noticing: `Type=notify` with no
// `sd_notify` call anywhere in the tree makes systemd wait out its start timeout,
// kill the process, and burn one of `StartLimitBurst`'s five attempts — a service
// that never starts, for a reason that cannot be seen from the unit file itself.
// These assertions live with the installer because the installer is the part of
// the product that ships this file.

// deployRoot is the repository's deploy/ directory, relative to this package.
const deployRoot = "../../deploy"

func readDeployFile(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(deployRoot, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(raw)
}

// TestTheServiceUnitDoesNotWaitForANotificationNobodySends.
func TestTheServiceUnitDoesNotWaitForANotificationNobodySends(t *testing.T) {
	unit := readDeployFile(t, filepath.Join("systemd", "security-core.service"))

	for _, line := range strings.Split(unit, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue // the unit explains why, in comments
		}
		if strings.HasPrefix(trimmed, "Type=") && strings.TrimSpace(trimmed) != "Type=simple" {
			t.Errorf("the unit declares %q. It must be Type=simple: nothing in this tree sends "+
				"sd_notify's READY=1, so a notifying unit waits out systemd's start timeout and is "+
				"killed", trimmed)
		}
		if strings.HasPrefix(trimmed, "NotifyAccess=") {
			t.Errorf("the unit declares %q and nothing notifies it. The line was the other half "+
				"of the same defect and it has to go with Type=", trimmed)
		}
	}

	if !strings.Contains(unit, "\nType=simple\n") {
		t.Error("the unit does not declare Type=simple at all")
	}
	// The reason has to survive the fix, or the next person re-adds it.
	if !strings.Contains(unit, "READY=1") {
		t.Error("the unit's comment no longer explains the notifier it does not have")
	}
}

// The README describes the unit, so a unit that changed and a README that did not
// is the same defect one level up. This asserts the specific claim that was wrong,
// rather than diffing prose.
func TestTheDeployReadmeDoesNotClaimAHandshakeThatDoesNotHappen(t *testing.T) {
	readme := readDeployFile(t, "README.md")

	// The table row and the caveat both named `Type=notify` as a property of the
	// unit. A line may still mention it — the row records that the unit *was*
	// notify — but a line that mentions it must also say what the unit is now,
	// so a reader cannot take the old value as current.
	for _, line := range strings.Split(readme, "\n") {
		if !strings.Contains(line, "Type=notify") {
			continue
		}
		isTableRow := strings.HasPrefix(strings.TrimSpace(line), "|")
		if isTableRow && !strings.Contains(line, "Type=simple") {
			t.Errorf("deploy/README.md has a table row mentioning Type=notify that does not say "+
				"what the unit declares now:\n%s", line)
		}
	}
	if !strings.Contains(readme, "Type=simple") {
		t.Error("deploy/README.md does not mention the unit's actual Type")
	}
}

package identity_test

// This file holds the control for the capability vocabulary: a capability that
// no route and no check reads is a capability that grants nothing, and a role
// table that grants it is lying to the auditor who reads it.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/identity"
)

// capUse matches a reference to a Cap* identifier.
//
// It is deliberately loose. Every match that is not a declared capability is
// ignored — `CapturedAt` is a generated protobuf field — so the noise costs
// nothing. What matters is one direction only: a declared capability has to
// appear here, outside this package. A tighter pattern would be more precise
// and would need to know every shape a reference can take, and the failure mode
// of a pattern that is too tight is a false green, which is the direction that
// does not get caught.
var capUse = regexp.MustCompile(`\bCap[A-Z][A-Za-z0-9_]*`)

// capabilitiesDeclaredButNotYetEnforced is the recorded debt, and the place it
// stays visible.
//
// Each entry is a capability the shipped role table grants and that nothing in
// the tree checks. It is a list rather than a fix because two of them are
// features being built by another participant: deleting CapIncidentEscalate or
// CapSiteConfigure here would remove the name a route under construction is
// going to use, and an agent may not deactivate a capability on the strength of
// a search it ran rather than on a decision somebody signed. The list is a
// record of that, not a permission: adding to it is a human decision, and the
// test below is what makes adding to it visible rather than silent.
//
// CapIdentityManage is NOT on this list any more, and that is a fact rather
// than an opinion: the console's identity surface -- `GET /v1/principal`, which
// names the configured principal the API runs as -- gates on it. A capability
// that is enforced is not debt.
//
// CapSiteConfigure is NOT on this list any more either, and for the same
// reason: the device registry's two write routes -- `POST /v1/devices`, which
// records a device, and `POST /v1/devices/{id}/probe`, which MEASURES one --
// gate on it. Registering a device and measuring one are both acts of site
// configuration, and that is what the capability was declared for.
var capabilitiesDeclaredButNotYetEnforced = map[string]string{
	"CapIncidentEscalate": "no route gates an escalation; it was never wired up",
}

// The control. A capability in this vocabulary is a promise that some route or
// check enforces it, and the only way that promise is kept is if adding a
// constant is paired with adding the code that reads it.
//
// This is the test that would have caught `evidence.export`: it was declared
// here and granted to tenant_admin and security_manager, and a tree-wide search
// for it found no route, no check and no audit action. It was a role table
// entry that answered "who can take this site's evidence out of the building"
// with a capability that could not.
func TestEveryDeclaredCapabilityIsReadBySomething(t *testing.T) {
	enforced := enforcedCapabilities(t)

	for _, c := range identity.AllCapabilities() {
		name := string(c)
		if constantName(t, c) == "" {
			t.Errorf("%q is in AllCapabilities but is not a Cap* constant in this package, so "+
				"nothing can reference it by name and the coverage check cannot see it", name)
			continue
		}
		if enforced[constantName(t, c)] {
			continue
		}
		if reason, carried := capabilitiesDeclaredButNotYetEnforced[constantName(t, c)]; carried {
			t.Logf("%s is declared and granted but enforced by nothing (%s); recorded debt",
				constantName(t, c), reason)
			continue
		}
		t.Errorf("capability %q (constant %s) is declared and granted by a role, but no route "+
			"or check outside internal/identity reads it. It grants nothing, and a role table "+
			"that lists it lies to the auditor who reads it.\n"+
			"Either gate something on it, or remove the constant, the grants and any audit "+
			"action for it — and if the removal cannot ship in this milestone, record it in "+
			"capabilitiesDeclaredButNotYetEnforced with the reason, so the debt is visible "+
			"rather than a second undocumented capability",
			name, constantName(t, c))
	}
}

// The list is a record of history, not a policy knob. This asserts the entries
// are exactly the ones that are still needed, so an entry cannot outlive the
// problem it describes and cannot be added by accident.
func TestTheCarriedCapabilityDebtIsExactlyTheKnownOnes(t *testing.T) {
	enforced := enforcedCapabilities(t)
	for constant, reason := range capabilitiesDeclaredButNotYetEnforced {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("carried capability %s has no reason. A debt with no reason is a "+
				"suppression with a name", constant)
		}
		if enforced[constant] {
			t.Errorf("carried capability %s is enforced by something now (%s), so the entry "+
				"should be removed: carrying a debt that has been paid hides the ones that "+
				"have not", constant, "see the walk for the reference")
		}
	}

	declared := make(map[string]bool, len(identity.AllCapabilities()))
	for _, c := range identity.AllCapabilities() {
		if n := constantName(t, c); n != "" {
			declared[n] = true
		}
	}
	for constant := range capabilitiesDeclaredButNotYetEnforced {
		if !declared[constant] {
			t.Errorf("carried capability %s is no longer declared, so its entry is dead "+
				"weight; remove it", constant)
		}
	}

	if len(capabilitiesDeclaredButNotYetEnforced) == 0 {
		return
	}
	t.Logf("%d declared capabilities are granted by a role and enforced by nothing: %s",
		len(capabilitiesDeclaredButNotYetEnforced),
		strings.Join(sortedKeys(capabilitiesDeclaredButNotYetEnforced), ", "))
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// constantName maps a capability back to the Go constant that declares it.
//
// identity owns both halves of that mapping and does not export it, because
// exporting it would be an API for a test. It is recovered here by matching the
// constant block's own text, which is the same block the coverage check exists
// to police: a constant whose declaration cannot be found is a constant this
// check cannot see, and the caller is told so rather than being handed "".
func constantName(t *testing.T, c identity.Capability) string {
	t.Helper()
	body, err := os.ReadFile("identity.go")
	if err != nil {
		t.Fatalf("read identity.go: %v", err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.Contains(line, string(c)) {
			continue
		}
		name := capUse.FindString(line)
		if name == "" {
			continue
		}
		if strings.Contains(line, `Capability = "`+string(c)+`"`) {
			return name
		}
	}
	return ""
}

// enforcedCapabilities walks the tree outside this package and returns every
// Cap* identifier it finds in non-test Go source.
//
// internal/identity is excluded because the constants are declared there, and a
// declaration is not a use — counting them would make this check pass on the
// file it exists to police. Test files are excluded because a test naming a
// capability is not a gate either: that is the whole defect, an identifier that
// only tests have ever heard of.
func enforcedCapabilities(t *testing.T) map[string]bool {
	t.Helper()
	selfDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolve this package's directory: %v", err)
	}
	found := map[string]bool{}
	for _, root := range []string{"..", filepath.Join("..", "..", "cmd")} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case "testdata", ".kilo":
					return fs.SkipDir
				}
				// Skip this package itself; its constants are declarations.
				abs, absErr := filepath.Abs(path)
				if absErr != nil {
					return absErr
				}
				if abs == selfDir {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for _, m := range capUse.FindAllString(string(raw), -1) {
				found[m] = true
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return found
}

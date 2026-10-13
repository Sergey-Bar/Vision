package lifecycle_test

// This file holds the tests for the signed compatibility fields that were
// verified and never read. `lifecycle_test.go` covers the schema window, which
// was already checked, so these land in a file of their own.

import (
	"crypto/ed25519"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/lifecycle"
	"github.com/Sergey-Bar/vision/internal/release"
)

// Three fields are signed into the manifest, written into the install state, and
// read by nothing. minCoreVersion, maxCoreVersion and contractsVersion were
// declarations: a release declaring maxCoreVersion "0.x" installed onto a 1.x
// core without a word, and because the values are recorded in Installed the
// incompatibility then looked compatible after the fact.
//
// shippedBuild is the window the tree actually ships: migrations/compat.json
// declares 0.1.0 to 0.x over contracts security/v1, so a 1.x release is the
// out-of-window case and a 0.1.0 release is the normal one.
var shippedBuild = release.Compatibility{
	MinSchemaVersion: 1, MaxSchemaVersion: 18,
	MinCoreVersion: "0.1.0", MaxCoreVersion: "0.x", ContractsVersion: "security/v1",
}

// archiveWindow is the window shape buildArchive puts in a manifest: schema
// 1..9, so the schema window is never what refuses these tests, and the contracts
// version every one of them speaks. The upper core bound is "1.x" throughout,
// which is the range whose reading is the point.
func archiveWindow(minCore string) release.Compatibility {
	return release.Compatibility{
		MinSchemaVersion: 1, MaxSchemaVersion: 9,
		MinCoreVersion: minCore, MaxCoreVersion: "1.x", ContractsVersion: "security/v1",
	}
}

// resign re-signs a manifest after a field was changed, which is the only way to
// get a signed manifest declaring something the tests need declared.
func resign(t *testing.T, m release.Manifest, key ed25519.PrivateKey) release.Manifest {
	t.Helper()
	if err := release.Sign(&m, key, "key-1", key.Public().(ed25519.PublicKey)); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return m
}

// A 1.x release on a 0.x core is the case the finding names and the one the
// window exists for: the core cannot read what the release writes, and the
// rollback target after a bad install is that same core.
func TestAnArchiveOutsideTheCoreWindowIsRefused(t *testing.T) {
	key := newKey(t)
	archivePath, manifest, pub := buildArchive(t, map[string]string{"bin/x": "b"}, nil, key)

	// Two builds the shipped 1.0.0-to-1.x release must not be installed onto.
	for name, build := range map[string]release.Compatibility{
		"a 0.x core": shippedBuild,
		"a pre-0.9 core": {MinSchemaVersion: 1, MaxSchemaVersion: 12,
			MinCoreVersion: "0.1.0", MaxCoreVersion: "0.8.9", ContractsVersion: "security/v1"},
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := lifecycle.Verify(archivePath, t.TempDir(), manifest, pub, build)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if plan.CoreWindowOverlaps {
				t.Fatal("a release whose core window is outside this build's was accepted")
			}
			err = lifecycle.CheckRefusals(plan)
			if err == nil {
				t.Fatal("the install proceeded against a core the release does not support")
			}
			if !strings.Contains(err.Error(), "core window") {
				t.Errorf("the refusal should name the core window, got %q", err)
			}
			// It has to name both sides, because the remedy is a different release
			// or a different build and an operator cannot tell which from one
			// number.
			for _, want := range []string{build.MinCoreVersion, build.MaxCoreVersion,
				manifest.Compatibility.MinCoreVersion, manifest.Compatibility.MaxCoreVersion} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal should name %q, got %q", want, err)
				}
			}
			if !strings.Contains(err.Error(), "rollback") {
				t.Errorf("the refusal should say why the state is unrecoverable, got %q", err)
			}
		})
	}
}

// "1.x" is a range, not a string. Compared as text it sorts below "10.0.0", and
// as an upper bound "1.x" means "any 1.y.z" - which is the only reading a
// release author means by it and the reading a string comparison loses.
func TestAVersionRangeIsComparedAsARangeAndNotAsText(t *testing.T) {
	key := newKey(t)
	archivePath, manifest, pub := buildArchive(t, map[string]string{"bin/x": "b"}, nil, key)

	narrow := release.Compatibility{
		MinSchemaVersion: 1, MaxSchemaVersion: 12,
		MinCoreVersion: "1.0.0", MaxCoreVersion: "1.9.9", ContractsVersion: "security/v1",
	}
	plan, err := lifecycle.Verify(archivePath, t.TempDir(), manifest, pub, narrow)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if plan.CoreWindowOverlaps {
		t.Error("a build capped at 1.9.9 accepted a release that runs on all of 1.x")
	}

	// And the normal forward case still passes: a build on 1.x installs a 1.0.0
	// release. A comparison that refused everything would be as wrong as one that
	// refused nothing.
	wide := release.Compatibility{
		MinSchemaVersion: 1, MaxSchemaVersion: 12,
		MinCoreVersion: "1.0.0", MaxCoreVersion: "2.x", ContractsVersion: "security/v1",
	}
	plan, err = lifecycle.Verify(archivePath, t.TempDir(), manifest, pub, wide)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !plan.CoreWindowOverlaps {
		t.Error("a build on 1.x refused a release that runs on 1.0.0")
	}
	if err := lifecycle.CheckRefusals(plan); err != nil {
		t.Errorf("a valid forward install was refused: %v", err)
	}
}

// The window is containment, not overlap, and the difference is load-bearing.
//
// A build pinned to exactly 1.0.0 does run a release that runs on all of 1.x -
// 1.0.0 is inside 1.x - and the install is still refused, for the same reason the
// schema window demands containment: the release may behave differently on
// 1.0.5, and the rollback target after a bad install is this same build, which
// was never tested against that half of the range. "It happens to work today" is
// not a window.
func TestTheCoreWindowIsContainmentAndNotOverlap(t *testing.T) {
	key := newKey(t)
	archivePath, manifest, pub := buildArchive(t, map[string]string{"bin/x": "b"}, nil, key)

	// The release runs on all of 1.x; the build is pinned inside it.
	pinned := release.Compatibility{
		MinSchemaVersion: 1, MaxSchemaVersion: 12,
		MinCoreVersion: "1.0.0", MaxCoreVersion: "1.0.0", ContractsVersion: "security/v1",
	}
	plan, err := lifecycle.Verify(archivePath, t.TempDir(), manifest, pub, pinned)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if plan.CoreWindowOverlaps {
		t.Error("a build on exactly 1.0.0 accepted a release whose window is 1.0.0 to 1.x. " +
			"The rule is containment, the same rule the schema window uses: a release " +
			"that runs on all of 1.x was never tested against the half of 1.x this " +
			"build is, and this build is the rollback target")
	}
	if err := lifecycle.CheckRefusals(plan); err == nil {
		t.Error("the install proceeded on an overlapping but not contained window")
	}

	// Widen the build to the whole range and the same archive installs, which is
	// the point: the refusal is about the window, not about the version strings.
	wide := release.Compatibility{
		MinSchemaVersion: 1, MaxSchemaVersion: 12,
		MinCoreVersion: "1.0.0", MaxCoreVersion: "1.x", ContractsVersion: "security/v1",
	}
	plan, err = lifecycle.Verify(archivePath, t.TempDir(), manifest, pub, wide)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !plan.CoreWindowOverlaps {
		t.Error("a build on 1.x refused a release whose window is 1.0.0 to 1.x")
	}
	if err := lifecycle.CheckRefusals(plan); err != nil {
		t.Errorf("a valid install was refused: %v", err)
	}
}

// A bound nobody can read is a bound nobody is enforcing, so an unparseable one
// is a refusal. Treating it as a pass is how a signed field stays decorative,
// which is the defect being fixed.
func TestACoreBoundThatCannotBeReadIsRefused(t *testing.T) {
	key := newKey(t)
	archivePath, manifest, pub := buildArchive(t, map[string]string{"bin/x": "b"}, nil, key)

	for name, build := range map[string]release.Compatibility{
		"a non-numeric build component": {
			MinSchemaVersion: 1, MaxSchemaVersion: 12,
			MinCoreVersion: "not a version", MaxCoreVersion: "1.x", ContractsVersion: "security/v1",
		},
		"a build with no window at all": {
			MinSchemaVersion: 1, MaxSchemaVersion: 12,
		},
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := lifecycle.Verify(archivePath, t.TempDir(), manifest, pub, build)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if plan.CoreWindowError == "" {
				t.Fatal("a core bound that cannot be parsed was accepted with no complaint")
			}
			err = lifecycle.CheckRefusals(plan)
			if err == nil {
				t.Error("the install proceeded with a bound nobody could read")
			}
		})
	}

	// The same for the archive's own declaration, re-signed so the refusal under
	// test is the installer's and not the verifier's.
	for name, window := range map[string]release.Compatibility{
		"a non-numeric archive component": archiveWindow("one.x"),
		"a negative archive component":    archiveWindow("1.-2.0"),
		"too many components":             archiveWindow("1.0.0.0"),
		"an empty archive bound":          archiveWindow(""),
	} {
		t.Run(name, func(t *testing.T) {
			other := resign(t, func() release.Manifest {
				m := manifest
				m.Compatibility = window
				return m
			}(), key)

			plan, err := lifecycle.Verify(archivePath, t.TempDir(), other, pub, release.Compatibility{
				MinSchemaVersion: 1, MaxSchemaVersion: 12,
				MinCoreVersion: "1.0.0", MaxCoreVersion: "2.x", ContractsVersion: "security/v1",
			})
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if plan.CoreWindowError == "" {
				t.Fatal("a core bound the archive could not be held to was accepted")
			}
			err = lifecycle.CheckRefusals(plan)
			if err == nil {
				t.Fatal("the install proceeded against an unreadable bound")
			}
			if !strings.Contains(err.Error(), "CoreVersion") {
				t.Errorf("the refusal should name the field it could not read, got %q", err)
			}
		})
	}
}

// The third unwatched field. Two binaries that speak different wire formats
// produce incidents one of them cannot read and fields it drops without saying
// so, and a schema window says nothing about any of that.
func TestAnArchiveOnDifferentContractsIsRefused(t *testing.T) {
	key := newKey(t)
	archivePath, manifest, pub := buildArchive(t, map[string]string{"bin/x": "b"}, nil, key)

	for name, contracts := range map[string]string{
		"a different contracts version": "security/v2",
		"no declared contracts version": "",
	} {
		t.Run(name, func(t *testing.T) {
			other := resign(t, func() release.Manifest {
				m := manifest
				m.Compatibility.ContractsVersion = contracts
				return m
			}(), key)

			plan, err := lifecycle.Verify(archivePath, t.TempDir(), other, pub, release.Compatibility{
				MinSchemaVersion: 1, MaxSchemaVersion: 12,
				MinCoreVersion: "1.0.0", MaxCoreVersion: "2.x", ContractsVersion: "security/v1",
			})
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if plan.ContractsVersionMatches {
				t.Fatalf("a release speaking %q was accepted by a build speaking security/v1",
					contracts)
			}
			err = lifecycle.CheckRefusals(plan)
			if err == nil {
				t.Fatal("the install proceeded against a different contracts version")
			}
			if !strings.Contains(err.Error(), "contracts") {
				t.Errorf("the refusal should name the contracts version, got %q", err)
			}
		})
	}
}

// The fields are signed, so an operator is being asked to trust them. A plan
// that does not print them is a plan that verified them without ever reading
// them, which is the defect.
func TestDescribePrintsTheFieldsItNowEnforces(t *testing.T) {
	key := newKey(t)
	archivePath, manifest, pub := buildArchive(t, map[string]string{"bin/x": "b"}, nil, key)
	plan, err := lifecycle.Verify(archivePath, t.TempDir(), manifest, pub, shippedBuild)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	text := lifecycle.Describe(plan)
	for _, want := range []string{
		"core window", "0.1.0", "0.x", "contracts", "security/v1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the plan does not show %q:\n%s", want, text)
		}
	}
}

// The refusal has to reach the operator, not only the exit code. `preflight`
// exists so the reason is readable before anything has been touched, and a plan
// CheckRefusals refuses but does not say so is the failure this package is
// about.
func TestDescribeNamesACoreWindowRefusal(t *testing.T) {
	key := newKey(t)
	archivePath, manifest, pub := buildArchive(t, map[string]string{"bin/x": "b"}, nil, key)
	plan, err := lifecycle.Verify(archivePath, t.TempDir(), manifest, pub,
		release.Compatibility{
			MinSchemaVersion: 1, MaxSchemaVersion: 12,
			MinCoreVersion: "0.1.0", MaxCoreVersion: "0.8.9", ContractsVersion: "security/v1",
		})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := lifecycle.CheckRefusals(plan); err == nil {
		t.Fatal("a plan out of its core window was not refused")
	}
	if !strings.Contains(lifecycle.Describe(plan), "REFUSED") {
		t.Errorf("a plan that CheckRefusals refuses does not say so:\n%s", lifecycle.Describe(plan))
	}
}

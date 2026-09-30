package config

import (
	"sort"
	"strings"
	"testing"
	"time"
)

// TestEveryFlagHasAVariable: a value that exists only as a flag would be a run
// nobody could reproduce from its own recorded configuration.
func TestEveryFlagHasAVariable(t *testing.T) {
	for name, env := range FlagSet {
		if !strings.HasPrefix(env, EnvPrefix) {
			t.Fatalf("flag --%s maps to %q, which is not a %s variable", name, env, EnvPrefix)
		}
		if _, ok := schemaByName[env]; !ok {
			t.Fatalf("flag --%s maps to %q, which is not in the schema: a flag that sets a variable nothing reads is a flag that does nothing", name, env)
		}
	}
}

// TestEveryRequiredVariableHasAFlag: the two audiences are both real. A person
// who has to export forty variables to change one number will not change it.
func TestTheImportantVariablesHaveFlags(t *testing.T) {
	for _, env := range []string{
		"SP_POC_DATA_ROOT", "SP_POC_SOURCE_KIND", "SP_POC_SOURCE_ARG",
		"SP_POC_AI_ADDR", "SP_POC_AI_MODEL", "SP_POC_AI_FPS", "SP_POC_AI_DEADLINE_MS",
		"SP_POC_AI_BUDGET_MS", "SP_POC_SIDECAR_SCRIPT",
		"SP_POC_SCORE_THRESHOLD", "SP_POC_CLASSES", "SP_POC_ZONES",
		"SP_POC_CLIP_BEFORE_MS", "SP_POC_CLIP_AFTER_MS",
		"SP_POC_MAX_DISK_BYTES", "SP_POC_SOAK_TARGET", "SP_POC_HEARTBEAT_INTERVAL",
		"SP_POC_XPROTECT_BASE_URL", "SP_POC_CAMERA_GUID", "SP_POC_DEVICE_PATH",
		"SP_POC_EVENT_TYPE_GUID", "SP_POC_UI_ADDR", "SP_POC_LABEL",
	} {
		found := false
		for _, v := range FlagSet {
			if v == env {
				found = true
			}
		}
		if !found {
			t.Errorf("%s has no flag; a person would have to export it", env)
		}
	}
}

func TestAFlagWinsOverTheEnvironment(t *testing.T) {
	m := complete(t)
	cfg, err := LoadWithFlags([]string{"--label", "from-the-command-line"}, lookupFrom(m))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Label != "from-the-command-line" {
		t.Fatalf("label = %q; the flag did not win over the environment", cfg.Label)
	}
}

func TestTheEnvironmentWinsWhenThereIsNoFlag(t *testing.T) {
	m := complete(t)
	cfg, err := LoadWithFlags(nil, lookupFrom(m))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Label != "baseline" {
		t.Fatalf("label = %q, want the environment's value", cfg.Label)
	}
}

func TestFlagsMayBeWrittenWithEquals(t *testing.T) {
	m := complete(t)
	cfg, err := LoadWithFlags([]string{"--label=equals-form"}, lookupFrom(m))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Label != "equals-form" {
		t.Fatalf("label = %q", cfg.Label)
	}
}

// TestAFlagWithTheWrongTypeStillFailsByVariableName: a flag is not a second,
// laxer path into the configuration. A duration flag carrying "five" must fail
// naming the variable, not naming the flag parser.
func TestAFlagWithTheWrongTypeStillFailsByVariableName(t *testing.T) {
	m := complete(t)
	_, err := LoadWithFlags([]string{"--ai-fps", "five"}, lookupFrom(m))
	if err == nil {
		t.Fatal("a non-numeric --ai-fps was accepted")
	}
	if !strings.Contains(err.Error(), "SP_POC_AI_FPS") {
		t.Fatalf("the refusal does not name the variable: %v", err)
	}
}

func TestAFlagWithNoValueIsRefused(t *testing.T) {
	if _, err := LoadWithFlags([]string{"--label"}, lookupFrom(complete(t))); err == nil {
		t.Fatal("a flag with no value was accepted")
	}
}

// TestAnUnknownFlagIsRefused: a typo in a flag on a measurement tool is a flag
// that did nothing, and a run that did not do what the operator asked is worse
// than a refusal.
func TestAnUnknownFlagIsRefused(t *testing.T) {
	_, err := LoadWithFlags([]string{"--lable", "typo"}, lookupFrom(complete(t)))
	if err == nil {
		t.Fatal("an unknown flag was accepted")
	}
	if !strings.Contains(err.Error(), "--lable") || !strings.Contains(err.Error(), "--label") {
		t.Fatalf("the refusal does not show the operator the real name: %v", err)
	}
}

func TestAFlagCannotSupplyAValueTheSchemaRefuses(t *testing.T) {
	// The flag path is not a way around Validate. A base URL that is plain http
	// to a remote host is refused whether it came from a flag or a variable.
	m := complete(t)
	delete(m, "SP_POC_XPROTECT_BASE_URL")
	_, err := LoadWithFlags([]string{"--xprotect-base-url", "http://vms.example.com"}, lookupFrom(m))
	if err == nil || !strings.Contains(err.Error(), "clear text") {
		t.Fatalf("a flag bypassed the plain-http refusal: %v", err)
	}
}

func TestPositionalArgumentsDoNotBecomeConfiguration(t *testing.T) {
	// This program has no positional arguments. One that appears is either a
	// typo or a subcommand, and silently ignoring it is how a run ends up
	// configured for something nobody asked for.
	m := complete(t)
	cfg, err := LoadWithFlags([]string{"extra-thing"}, lookupFrom(m))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Label != "baseline" {
		t.Fatalf("a positional argument changed the configuration: label = %q", cfg.Label)
	}
}

func TestFlagNamesAreSorted(t *testing.T) {
	names := FlagNames()
	if !sort.StringsAreSorted(names) {
		t.Fatalf("FlagNames() is not sorted: %v", names)
	}
}

func TestDurationFlagSetsAndNamesItsVariable(t *testing.T) {
	var d time.Duration
	dv := newDurationValue(&d)
	if err := dv.Set("250ms"); err != nil {
		t.Fatal(err)
	}
	if d.String() != "250ms" {
		t.Fatalf("d = %s", d)
	}
	if err := dv.Set("not a duration"); err == nil {
		t.Fatal("a non-duration was accepted")
	}
}

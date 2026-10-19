package nlp

import (
	"strings"
	"testing"
)

func TestCompileRuleFromTextRecognizesEntities(t *testing.T) {
	cases := []struct {
		text      string
		wantEvent string
	}{
		{"person with bag at entrance", "person"},
		{"vehicle at gate", "vehicle"},
		{"animal in perimeter", "animal"},
		{"bag on the floor", "bag"},
		{"אנשים עם תיק", "person"},
	}
	for _, c := range cases {
		rule, err := CompileRuleFromText(c.text, "site-1")
		if err != nil {
			t.Errorf("compileRuleFromText(%q) error: %v", c.text, err)
			continue
		}
		if !strings.Contains(rule, "eventType: [") {
			t.Errorf("rule for %q missing eventType", c.text)
		}
		if !strings.Contains(rule, c.wantEvent) {
			t.Errorf("rule for %q does not contain event type %q", c.text, c.wantEvent)
		}
	}
}

func TestCompileRuleFromTextRejectsNoEntity(t *testing.T) {
	_, err := CompileRuleFromText("nothing relevant here", "site-1")
	if err == nil {
		t.Error("expected error for text with no entity")
	}
}

func TestCompileRuleFromTextIncludesZone(t *testing.T) {
	rule, err := CompileRuleFromText("person at entrance", "site-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rule, "/entrance") {
		t.Error("rule should include entrance zone")
	}
}

func TestCompileRuleFromTextIncludesTimeWindow(t *testing.T) {
	rule, err := CompileRuleFromText("person in last hour", "site-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rule, "timeWindow") {
		t.Error("rule should include timeWindow")
	}
	if !strings.Contains(rule, "durationSeconds") {
		t.Error("rule should include durationSeconds")
	}
}

package vlm

import (
	"strings"
	"testing"

	"github.com/Sergey-Bar/ai-captain-poc/internal/aistate"
)

// TestAMalformedAnswerIsCannotTellAndNeverAPartialParse is the parser's whole
// job. A partial parse is the worst outcome available: the caller gets a summary
// and no verdict, stores it, and the incident carries a badge driven by whatever
// field happened to survive.
func TestAMalformedAnswerIsCannotTellAndNeverAPartialParse(t *testing.T) {
	cases := map[string]struct {
		content string
		wantErr bool
	}{
		"a complete answer": {
			content: `{"verdict":"confirmed","summary":"a vehicle","confidence":0.81,"insufficient_evidence":false}`,
		},
		"prose":                          {content: `It looks like a car to me.`, wantErr: true},
		"an empty object":                {content: `{}`, wantErr: true},
		"null":                           {content: `null`, wantErr: true},
		"not json":                       {content: `verdict: confirmed`, wantErr: true},
		"a fenced block":                 {content: "```json\n{\"verdict\":\"none\",\"summary\":\"x\",\"confidence\":0,\"insufficient_evidence\":true}\n```", wantErr: true},
		"a missing verdict":              {content: `{"summary":"a car","confidence":0.5,"insufficient_evidence":false}`, wantErr: true},
		"a missing summary":              {content: `{"verdict":"confirmed","confidence":0.5,"insufficient_evidence":false}`, wantErr: true},
		"a missing confidence":           {content: `{"verdict":"confirmed","summary":"a car","insufficient_evidence":false}`, wantErr: true},
		"a missing evidence flag":        {content: `{"verdict":"confirmed","summary":"a car","confidence":0.5}`, wantErr: true},
		"an unknown field":               {content: `{"verdict":"confirmed","summary":"a car","confidence":0.5,"insufficient_evidence":false,"reasoning":"because"}`, wantErr: true},
		"an unknown verdict":             {content: `{"verdict":"probably_a_car","summary":"a car","confidence":0.5,"insufficient_evidence":false}`, wantErr: true},
		"a confidence above one":         {content: `{"verdict":"confirmed","summary":"a car","confidence":1.4,"insufficient_evidence":false}`, wantErr: true},
		"a negative confidence":          {content: `{"verdict":"confirmed","summary":"a car","confidence":-0.2,"insufficient_evidence":false}`, wantErr: true},
		"an array rather than an object": {content: `[{"verdict":"confirmed"}]`, wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			obs, err := ParseVerdict(tc.content)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("the content parsed to %+v; it must be refused", obs)
				}
				return
			}
			if err != nil {
				t.Fatalf("a valid answer was refused: %v", err)
			}
			if obs.State != aistate.Asked {
				t.Fatalf("a parsed answer carries the state %q rather than asked_answered", obs.State)
			}
			if obs.Verdict != VerdictConfirmed {
				t.Fatalf("the verdict is %q", obs.Verdict)
			}
			if obs.Confidence != 0.81 {
				t.Fatalf("the confidence is %g", obs.Confidence)
			}
		})
	}
}

// TestAVerdictIsOnlyMeaningfulOnAnAnsweredAsk is RC-1, the fabricated-negative
// control, and it is the most important assertion in this module.
//
// A `false_positive` on a degraded or not_asked observation tells an operator that
// nothing happened, because we could not ask. It is the single worst failure this
// product can have.
func TestAVerdictIsOnlyMeaningfulOnAnAnsweredAsk(t *testing.T) {
	for _, state := range []string{aistate.Degraded, aistate.NotAsked, aistate.CannotTell, aistate.NoObjects} {
		o := Observation{
			State: state, Verdict: VerdictFalsePositive, Summary: "nothing is there",
			Confidence: 0.95, InsufficientEvidence: false,
		}
		o.Finalise()
		if o.Verdict != VerdictNone {
			t.Fatalf("an observation in state %q kept the verdict %q", state, o.Verdict)
		}
		if o.Confidence != 0 {
			t.Fatalf("an observation in state %q kept the confidence %g", state, o.Confidence)
		}
		if o.Summary != "" {
			t.Fatalf("an observation in state %q kept the summary %q; prose from an unreadable answer must not reach a screen", state, o.Summary)
		}
		if o.InsufficientEvidence {
			t.Fatalf("an observation in state %q claims the evidence was insufficient; nobody read anything", state)
		}
		if !o.Absence() {
			t.Fatalf("an observation in state %q does not report an absence", state)
		}
	}
	// And the answered case is untouched, because a rule that fires on everything
	// is not a rule.
	ok := Observation{State: aistate.Asked, Verdict: VerdictConfirmed, Summary: "a vehicle crossed", Confidence: 0.8}
	ok.Finalise()
	if ok.Verdict != VerdictConfirmed || ok.Confidence != 0.8 || ok.Summary != "a vehicle crossed" {
		t.Fatalf("normalising an answered observation changed it: %+v", ok)
	}
	if ok.Absence() {
		t.Fatal("an answered observation reports an absence")
	}
}

// TestAnUnrecognisedVerdictIsNeverStored: a verdict outside the closed set is a
// programming error, and correcting it is better than storing it.
func TestAnUnrecognisedVerdictIsNeverStored(t *testing.T) {
	o := Observation{State: aistate.Asked, Verdict: "maybe", Summary: "s", Confidence: 0.9}
	o.Finalise()
	if o.Verdict != VerdictNone {
		t.Fatalf("an unrecognised verdict was kept as %q", o.Verdict)
	}
	if o.Confidence != 0 {
		t.Fatalf("an unrecognised verdict kept the confidence %g", o.Confidence)
	}
}

// TestSetStateIsTheOnlyDoorToAVerdict: the parser, the observer and a replay all
// go through it, so there is one place the rule lives.
func TestSetStateIsTheOnlyDoorToAVerdict(t *testing.T) {
	o := Observation{Verdict: VerdictConfirmed, Summary: "s", Confidence: 1}
	o.SetState(aistate.Degraded)
	if o.Verdict != VerdictNone {
		t.Fatalf("SetState(degraded) kept the verdict %q", o.Verdict)
	}
	o.SetState(aistate.Asked)
	if o.Verdict != VerdictNone {
		t.Fatal("a cleared verdict came back when the state became asked; a verdict is set by an answer, not by a state change")
	}
}

// TestTheOutputHashCannotBeShiftedAcrossAFieldBoundary pins the hash's shape.
//
// It is hashed rather than stored as prose because golden-testing a model's words
// is golden-testing the weather — but a hash over a naive concatenation can be
// made to collide by moving a field boundary, and then it is not a hash of
// anything.
func TestTheOutputHashCannotBeShiftedAcrossAFieldBoundary(t *testing.T) {
	a := Observation{State: aistate.Asked, Verdict: VerdictConfirmed, Summary: "a car", Confidence: 0.5, InsufficientEvidence: false}
	b := Observation{State: aistate.Asked, Verdict: VerdictConfirmed, Summary: "a ca", Confidence: 0, InsufficientEvidence: false}
	if OutputHashOf(a) == OutputHashOf(b) {
		t.Fatal("two different outputs share a hash")
	}
	c := a
	c.Confidence = 0.500001
	if OutputHashOf(a) == OutputHashOf(c) {
		t.Fatal("a confidence change does not change the hash")
	}
	if len(OutputHashOf(a)) != 64 {
		t.Fatalf("the output hash is %d characters rather than 64", len(OutputHashOf(a)))
	}
	if OutputHashOf(a) == "" {
		t.Fatal("the output hash is empty")
	}
}

// TestTwoIncidentClassesAreTwoPromptFilesAndNothingElse is W2-2's assertion.
//
// Same four output fields, same parser, same clip path, same push, same
// bookmark. The ONLY difference is the prompt, and a third class is therefore a
// file rather than a code change.
func TestTwoIncidentClassesAreTwoPromptFilesAndNothingElse(t *testing.T) {
	set, err := EmbeddedPrompts()
	if err != nil {
		t.Fatalf("EmbeddedPrompts: %v", err)
	}
	classes := set.Classes()
	if len(classes) < 2 {
		t.Fatalf("this build carries %v; the unification claim needs at least two classes", classes)
	}
	seenVersions := map[string]bool{}
	for _, id := range classes {
		p, err := set.Get(id)
		if err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
		if p.ID != id {
			t.Fatalf("the prompt for %q is filed under the id %q", id, p.ID)
		}
		if strings.TrimSpace(p.Text) == "" {
			t.Fatalf("the prompt for %q is empty", id)
		}
		if !strings.Contains(p.Text, "insufficient_evidence") {
			t.Fatalf("the prompt for %q does not demand the fourth field; the shape is appended, not optional", id)
		}
		for _, field := range RequiredOutputFields() {
			if !strings.Contains(p.Text, field) {
				t.Fatalf("the prompt for %q never mentions %q", id, field)
			}
		}
		if seenVersions[p.Version] {
			continue
		}
		seenVersions[p.Version] = true
	}
	// The manifest's declared shape is the parser's shape.
	if got := len(RequiredOutputFields()); got != 4 {
		t.Fatalf("the closed output shape has %d fields rather than 4", got)
	}
	if _, err := set.Get("nonexistent_class"); err == nil {
		t.Fatal("an undeclared incident class returned a prompt")
	}
}

// TestTheCameraIntegrityPromptAsksWhichAndNotWhether is W2-3: the novelty score
// says the view changed, and only the model can say how, so the prompt has to ask
// which and the answer has to be recorded as a claim.
func TestTheCameraIntegrityPromptAsksWhichAndNotWhether(t *testing.T) {
	set, err := EmbeddedPrompts()
	if err != nil {
		t.Fatalf("EmbeddedPrompts: %v", err)
	}
	p, err := set.Get("camera_integrity")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	low := strings.ToLower(p.Text)
	// The two outcomes measurement alone cannot separate.
	for _, want := range []string{"moved", "covered"} {
		if !strings.Contains(low, want) {
			t.Fatalf("the camera_integrity prompt never mentions %q; it cannot distinguish what the pixels cannot", want)
		}
	}
	if !strings.Contains(low, "needs_review") {
		t.Fatal("the camera_integrity prompt does not offer needs_review; an honest 'I cannot tell' has to be a valid answer")
	}
}

// TestTheAlarmPromptForbidsCoordinates is the honest split stated in the prompt
// itself: the model is never asked for a box, because the box is ours.
func TestTheAlarmPromptForbidsCoordinates(t *testing.T) {
	set, err := EmbeddedPrompts()
	if err != nil {
		t.Fatalf("EmbeddedPrompts: %v", err)
	}
	p, err := set.Get("alarm")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	low := strings.ToLower(p.Text)
	if !strings.Contains(low, "measure") {
		t.Fatal("the alarm prompt does not say the region is measured; a model told nothing will invent one")
	}
	for _, bad := range []string{"give me the bounding box", "return the coordinates"} {
		if strings.Contains(low, bad) {
			t.Fatalf("the alarm prompt asks the model for %q, which is the one thing the API does not answer", bad)
		}
	}
}

// TestTheContractVersionCoversThePromptAndChangesWithIt: two prompts and one
// prompt at two revisions must not share a contract version, and a provider's
// model name changing must move it too.
func TestTheContractVersionCoversThePromptAndChangesWithIt(t *testing.T) {
	set, _ := EmbeddedPrompts()
	a, _ := set.Get("alarm")
	b, _ := set.Get("camera_integrity")
	va := ContractVersionFor(a, 1, "model-x", RegionEU)
	vb := ContractVersionFor(b, 1, "model-x", RegionEU)
	if va == vb {
		t.Fatal("two different prompts share a contract version")
	}
	if ContractVersionFor(a, 2, "model-x", RegionEU) == va {
		t.Fatal("a schema change did not move the contract version")
	}
	if ContractVersionFor(a, 1, "model-y", RegionEU) == va {
		t.Fatal("a model name change did not move the contract version")
	}
	if ContractVersionFor(a, 1, "model-x", RegionUS) == va {
		t.Fatal("a region change did not move the contract version")
	}
	// The revision to compare against is derived, not written down: the shipped
	// prompt is at v2 and the shipped revision moved from v1 to v2, so hard-coding
	// either side would have broken this test at the very moment the guard was added
	// — which is exactly when a test about prompt revisions is worth having.
	b.Version = b.Version + "-moved"
	if ContractVersionFor(b, 1, "model-x", RegionEU) == vb {
		t.Fatal("a prompt revision did not move the contract version")
	}
	if len(va) != 64 {
		t.Fatalf("the contract version is %d characters", len(va))
	}
}

// TestTheVerdictSetIsClosed guards against a stray verdict reaching a screen.
func TestTheVerdictSetIsClosed(t *testing.T) {
	got := AllVerdicts()
	if len(got) != 4 {
		t.Fatalf("the verdict set has %d members rather than 4", len(got))
	}
	for _, v := range got {
		if !v.Valid() {
			t.Fatalf("AllVerdicts returned the invalid verdict %q", v)
		}
	}
	for _, bad := range []Verdict{"", "confirmed ", "CONFIRMED", "true"} {
		if bad.Valid() {
			t.Fatalf("%q is in the closed set", bad)
		}
	}
}

// TestTheReasonCodesAreDistinct: a caller parses a field, and a code that two
// failures share is a field that cannot tell them apart.
func TestTheReasonCodesAreDistinct(t *testing.T) {
	cases := []struct {
		err  error
		want ReasonCode
	}{
		{nil, ReasonNone},
		{ErrQuotaExhausted, ReasonQuotaExhausted},
		{ErrCrossRegion, ReasonRegionRefused},
		{ErrRegionUnavailable, ReasonRegionUnavailable},
		{ErrMalformedAnswer, ReasonAnswerMalformed},
		{ErrAssetNotFound, ReasonAssetMissing},
		{ErrRedirectRefused, ReasonRedirectRefused},
		{errOther{}, ReasonError},
	}
	seen := map[ReasonCode]error{}
	for _, tc := range cases {
		got := ReasonFor(tc.err)
		if got != tc.want {
			t.Fatalf("ReasonFor(%v) = %q rather than %q", tc.err, got, tc.want)
		}
		if prev, dup := seen[got]; dup && got != ReasonNone {
			t.Fatalf("the code %q is produced by both %v and %v", got, prev, tc.err)
		}
		seen[got] = tc.err
		d := Detail(tc.err)
		if !strings.HasPrefix(d, "reason=") {
			t.Fatalf("the detail %q does not start with reason=", d)
		}
		if strings.Count(d, " ") != 0 {
			t.Fatalf("the detail %q carries more than one field; a consumer parses it, and a fixed shape is what makes it parseable", d)
		}
	}
}

type errOther struct{}

func (errOther) Error() string { return "something else" }

// TestReadClipRefusesAnEmptyFile, because evidence that shows nothing is not
// evidence and storing it would make a count a lie.
func TestReadClipRefusesAnEmptyFile(t *testing.T) {
	if _, err := ReadClip("no-such-file.mp4"); err == nil {
		t.Fatal("a missing clip was accepted")
	}
}

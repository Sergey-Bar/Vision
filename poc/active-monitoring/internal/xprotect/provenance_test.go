package xprotect

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/shared/aistate"
)

// answeredProvenance is the ordinary, fully attributed answer. Every test below
// starts from it and removes exactly one thing, because a test that builds its
// fixture from scratch can accidentally satisfy a refusal for the wrong reason —
// which is how a control ends up unreachable while its test is green.
func answeredProvenance() Provenance {
	return Provenance{
		AIState: aistate.Asked, Verdict: "confirmed",
		Summary:       "a bird crossed the line from left to right",
		ModelIdentity: "nvidia/cosmos-predict2.5", ContractVersion: "contract-1",
		Temperature: 0.0000, OutputHash: strings.Repeat("b", 64),
	}
}

func attributedInput() EventInput {
	return EventInput{
		EventTypeGUID: "type-guid", SourceGUID: "camera-guid",
		At: time.Date(2026, 9, 30, 10, 15, 4, 0, time.UTC), Tag: "aicap-bird-abc",
		Class: "bird", Value: "bird", Confidence: 0.87,
		Box:      Box{Top: 194, Left: 286, Bottom: 429, Right: 408},
		RuleName: "bird-line-cross",
		Evidence: EvidenceRef{StorageRef: "ab/cd/x.mp4", SHA256: strings.Repeat("a", 64)},
		// The note is deliberately WRONG here, and the test below proves BuildEvent
		// overwrites it: a caller that fills in the note and forgets the provenance
		// must not be able to push a sentence with no attribution.
		Provenance: answeredProvenance(),
	}
}

// TestAnEventWithNoAIStateIsRefused is RC-22's subject. "The model was asked and
// said this" and "nobody could reach the model" are different facts about the same
// event, and an event that does not say which one it is cannot be read.
func TestAnEventWithNoAIStateIsRefused(t *testing.T) {
	in := attributedInput()
	in.Provenance.AIState = ""
	_, err := BuildEvent(in)
	if err == nil {
		t.Fatal("an event naming no AI state was pushed")
	}
	for _, want := range []string{"no AI state", "asked_answered", "not_asked", "required on every pushed event"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
}

// TestAnUnknownAIStateIsRefused: an unrecognised state is a protocol error, not a
// sixth answer. Accepting it would put a word in the VMS that nothing in this module
// can count.
func TestAnUnknownAIStateIsRefused(t *testing.T) {
	in := attributedInput()
	in.Provenance.AIState = "probably_fine"
	_, err := BuildEvent(in)
	if err == nil {
		t.Fatal("an unknown AI state was pushed")
	}
	if !strings.Contains(err.Error(), "protocol error") {
		t.Fatalf("the refusal does not say it is a protocol error: %v", err)
	}
}

// TestAnAnsweredEventWithNoAttributionIsRefused is RC-23's subject. R8 requires
// every rendered claim to name the model that produced it, the contract version and
// the hash of the validated output. An event with the sentence and none of those is
// a caption this repository cannot check, and the Smart Client shows it to somebody
// who has no way of knowing that.
func TestAnAnsweredEventWithNoAttributionIsRefused(t *testing.T) {
	for _, drop := range []struct {
		name string
		kill func(*Provenance)
		want string
	}{
		{"no model identity", func(p *Provenance) { p.ModelIdentity = "" }, "model_identity"},
		{"no contract version", func(p *Provenance) { p.ContractVersion = "" }, "contract_version"},
		{"no output hash", func(p *Provenance) { p.OutputHash = "" }, "output_hash"},
	} {
		t.Run(drop.name, func(t *testing.T) {
			in := attributedInput()
			drop.kill(&in.Provenance)
			_, err := BuildEvent(in)
			if err == nil {
				t.Fatalf("an answered event with %s was pushed", drop.name)
			}
			if !strings.Contains(err.Error(), drop.want) {
				t.Fatalf("the refusal does not name the missing field %q: %v", drop.want, err)
			}
			if !strings.Contains(err.Error(), "R8") {
				t.Fatalf("the refusal does not cite the rule it enforces: %v", err)
			}
		})
	}
	// All three at once names all three, so an operator fixing the fixture sees the
	// whole list rather than discovering them one run at a time.
	in := attributedInput()
	in.Provenance.ModelIdentity, in.Provenance.ContractVersion, in.Provenance.OutputHash = "", "", ""
	_, err := BuildEvent(in)
	for _, want := range []string{"model_identity", "contract_version", "output_hash"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestAnUnaskedEventNeedsNoAttribution: nobody was asked, so there is nothing to
// attribute. Requiring a model identity there would force the writer to invent all
// three, which is worse than the empty fields.
func TestAnUnaskedEventNeedsNoAttribution(t *testing.T) {
	for _, state := range aistate.BlindnessStates {
		in := attributedInput()
		in.Provenance = Provenance{AIState: state}
		body, err := BuildEvent(in)
		if err != nil {
			t.Fatalf("(%s) is an unasked state and must be pushable without attribution: %v", state, err)
		}
		if !strings.Contains(body.Data.Evidence.Note, "model=NOT ASKED") {
			t.Fatalf("(%s) does not say WHY there is no model identity: %q", state, body.Data.Evidence.Note)
		}
	}
}

// TestAnUnaskedEventWithAVerdictIsRefused is the fabricated negative, in the place it
// would escape to the VMS. A verdict on a window nobody asked about is precisely the
// lie R4 exists to prevent, and until this refusal existed it was reachable by a
// caller that filled in the field.
func TestAnUnaskedEventWithAVerdictIsRefused(t *testing.T) {
	in := attributedInput()
	in.Provenance = Provenance{AIState: aistate.NotAsked, Verdict: "false_positive",
		Summary: "nothing happened"}
	_, err := BuildEvent(in)
	if err == nil {
		t.Fatal("an unasked window carrying a verdict was pushed")
	}
	for _, want := range []string{"negative answer we did not receive", "not_asked", "false_positive"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
}

// TestNoObjectsIsAnsweredAndNotABlindness: `no_objects` is a real answer, so it needs
// the attribution and it does NOT get its own Analytics Event Type. The two facts
// are the same fact, and a run that treated no_objects as a blindness would refuse
// to push the ordinary verdict its whole design depends on.
func TestNoObjectsIsAnsweredAndNotABlindness(t *testing.T) {
	p := Provenance{AIState: aistate.NoObjects, Verdict: "false_positive", Summary: "the region is empty"}
	if p.Absent() {
		t.Fatal("no_objects was treated as a blindness; it is a negative ANSWER")
	}
	if err := p.Validate(); err == nil {
		t.Fatal("no_objects with no model identity was accepted")
	}
	p.ModelIdentity, p.ContractVersion, p.OutputHash = "m", "c", strings.Repeat("b", 64)
	if err := p.Validate(); err != nil {
		t.Fatalf("a fully attributed no_objects was refused: %v", err)
	}
}

// TestTheNoteCarriesTheModelSentenceVerbatim: R8 forbids a paraphrase, because a
// paraphrase of a model's sentence is a sentence this repository invented.
func TestTheNoteCarriesTheModelSentenceVerbatim(t *testing.T) {
	sentence := "A PERSON walks left to right, then stops and looks at the camera."
	in := attributedInput()
	in.Provenance.Summary = sentence
	body, err := BuildEvent(in)
	if err != nil {
		t.Fatal(err)
	}
	note := body.Data.Evidence.Note
	if !strings.HasPrefix(note, sentence) {
		t.Fatalf("the note does not begin with the model's own sentence:\n%q", note)
	}
	for _, want := range []string{
		"aistate=asked_answered", "verdict=confirmed",
		"model=nvidia/cosmos-predict2.5 (provider claim)", "contract=contract-1",
		"temperature=0.0000", "output=" + strings.Repeat("b", 64),
	} {
		if !strings.Contains(note, want) {
			t.Fatalf("the note does not carry %q:\n%q", want, note)
		}
	}
	// There is no model hash, and there must never be one: a hosted model has no
	// weights to hash, and inventing a field is the failure R8 exists to prevent.
	if strings.Contains(note, "model_hash") {
		t.Fatalf("the note invented a model hash:\n%q", note)
	}
}

// TestTheNoteIsBuiltAndNotSupplied: a caller that fills in the note and forgets the
// provenance must not be able to push an unattributed sentence.
func TestTheNoteIsBuiltAndNotSupplied(t *testing.T) {
	in := attributedInput()
	in.Evidence.Note = "POC evidence ref; whatever the caller felt like writing"
	body, err := BuildEvent(in)
	if err != nil {
		t.Fatal(err)
	}
	if body.Data.Evidence.Note == in.Evidence.Note {
		t.Fatal("BuildEvent passed the caller's note through; the provenance and the note would then be " +
			"two independent things one of which can be filled in alone")
	}
	// And the other four evidence fields ARE the caller's, unchanged: only the note
	// is derived.
	if body.Data.Evidence.StorageRef != in.Evidence.StorageRef || body.Data.Evidence.SHA256 != in.Evidence.SHA256 {
		t.Fatalf("BuildEvent rewrote the evidence pointer: %+v", body.Data.Evidence)
	}
}

// TestTheNoteIsOneLine: a newline inside the note renders in Smart Client as a layout
// this module cannot control, and the line after it reads like a separate field the
// VMS understood. It replaces the control characters rather than refusing, because
// the sentence is the model's and refusing to push it over a byte would turn a
// rendering problem into a lost event.
func TestTheNoteIsOneLine(t *testing.T) {
	in := attributedInput()
	in.Provenance.Summary = "first line\nsecond\tline\rthird"
	body, err := BuildEvent(in)
	if err != nil {
		t.Fatal(err)
	}
	note := body.Data.Evidence.Note
	if strings.ContainsAny(note, "\n\r\t") {
		t.Fatalf("the note kept a control character:\n%q", note)
	}
	if !strings.Contains(note, "first line second line third") {
		t.Fatalf("the sentence did not survive the flattening:\n%q", note)
	}
}

// TestTheTagCarriesTheStateAndTheOutputHash is the fallback carrier. Milestone's
// specification documents `tag` as assignable "for filtering and sorting purposes"
// and never enumerates the inner fields of `data`, so `data` may be dropped by a real
// VMS and the tag is what survives. Both facts therefore go in both places.
func TestTheTagCarriesTheStateAndTheOutputHash(t *testing.T) {
	in := attributedInput()
	body, err := BuildEvent(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"|state=asked_answered", "|v=confirmed", "|out=" + strings.Repeat("b", 16)} {
		if !strings.Contains(body.Tag, want) {
			t.Fatalf("the tag does not carry %q: %q", want, body.Tag)
		}
	}
	if !strings.HasPrefix(body.Tag, in.Tag) {
		t.Fatalf("the tag %q does not begin with the caller's idempotency tag %q", body.Tag, in.Tag)
	}
	// And it is a URL-safe token, because the tag is also the query in the read-back:
	// an unescaped space would make the read-back 400 while the push looked fine.
	for _, r := range body.Tag {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '|' || r == '=' || r == '-' || r == '_' || r == '.'
		if !ok {
			t.Fatalf("the tag carries %q, which has to survive being put in a URL: %q", r, body.Tag)
		}
	}
}

// TestAnAbsenceTagCarriesTheWindowCount: "this camera was not assessed" and "this
// camera was not assessed 500 times in an hour" are different alarms, and the second
// is the one that tells an operator whether to look now or in the morning.
func TestAnAbsenceTagCarriesTheWindowCount(t *testing.T) {
	in := attributedInput()
	in.Provenance = Provenance{AIState: aistate.NotAsked, AbsenceCount: 500}
	body, err := BuildEvent(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.Tag, "|n=500") {
		t.Fatalf("the tag does not carry the count: %q", body.Tag)
	}
	if !strings.Contains(body.Data.Evidence.Note, "window_count=500") {
		t.Fatalf("the note does not carry the count: %q", body.Data.Evidence.Note)
	}
	if strings.Contains(body.Tag, "|v=") {
		t.Fatalf("the tag carries a verdict for an unasked window: %q", body.Tag)
	}
}

// TestTheProvenanceSurvivesTheWireRoundTrip is item three's requirement stated as a
// test: the block has to come back out of a JSON round trip, because that is the
// difference between a claim and a field.
func TestTheProvenanceSurvivesTheWireRoundTrip(t *testing.T) {
	in := attributedInput()
	first, err := BuildEvent(in)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var back EventRequest
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Tag != first.Tag {
		t.Fatalf("the tag changed across the wire: %q -> %q", first.Tag, back.Tag)
	}
	if back.Data.Evidence.Note != first.Data.Evidence.Note {
		t.Fatalf("the note changed across the wire:\n%q\n%q", first.Data.Evidence.Note, back.Data.Evidence.Note)
	}
	// Every fact R8 requires is recoverable from the serialised body alone. If a
	// reader in Smart Client can find all six, the push carried the attribution; if
	// one is missing here it is missing in the VMS too.
	for _, want := range []string{
		first.Data.Evidence.Note, first.Data.Evidence.SHA256, first.Data.Evidence.StorageRef,
		"nvidia/cosmos-predict2.5", "contract-1", strings.Repeat("b", 64),
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("the serialised body does not carry %q: %s", want, raw)
		}
	}
}

// TestTheSameAnswerProducesTheSameTag: idempotency depends on it. A detection whose
// answer has not changed must produce the same tag, so the read-back still collapses
// a retry onto the original event rather than creating a second one.
func TestTheSameAnswerProducesTheSameTag(t *testing.T) {
	a, err := BuildEvent(attributedInput())
	if err != nil {
		t.Fatal(err)
	}
	b := attributedInput()
	b.Provenance.Temperature = 0.5
	second, err := BuildEvent(b)
	if err != nil {
		t.Fatal(err)
	}
	if a.Tag != second.Tag {
		t.Fatalf("the temperature changed the tag (%q -> %q); it is not in the tag and must not be", a.Tag, second.Tag)
	}
	// And a genuinely different answer DOES change it, or the read-back would
	// suppress a corrected event.
	c := attributedInput()
	c.Provenance.Verdict = "false_positive"
	third, err := BuildEvent(c)
	if err != nil {
		t.Fatal(err)
	}
	if third.Tag == a.Tag {
		t.Fatal("a different verdict produced the same tag; a corrected event would be suppressed as a duplicate")
	}
}

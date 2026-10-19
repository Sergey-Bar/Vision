package soak

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/store"
	"github.com/Sergey-Bar/vision-poc/internal/vlm"
	"github.com/Sergey-Bar/vision-poc/internal/xprotect"
	"github.com/Sergey-Bar/vision/shared/aistate"
	"github.com/Sergey-Bar/vision/shared/classify"
)

// Absence aggregation: one pushable alarm per window of blindness, not one per
// proposal.
//
// The case it exists for is concrete. The Hafnia API is unreachable for an hour.
// Five hundred crossings are detected, and each one produces one proposal. Without
// this, "the model could not be reached" reaches the operator five hundred times —
// which is the alarm fatigue this product exists to remove, produced by the absence
// feature itself.
//
// Three design decisions, each with the thing it prevents:
//
//   - ONE row per window per state, refreshed while it is pending. The window closes
//     when the push worker sends the row, not on a timer: a timer would need a new
//     configuration variable to be right, and a window boundary chosen by a knob is a
//     window boundary nobody can reproduce from the stored facts.
//
//   - The count is REFRESHED, not appended. The push worker runs on a ticker, so the
//     row usually goes out after the window has closed, and a count written once — at
//     the first proposal — would read `1` for a window that produced five hundred.
//     That number is what tells an operator whether to look now or in the morning.
//
//   - The detection's own event is NOT withheld. The crossing happened; the VERDICT
//     is what is missing. Withholding the event with the measured bounding box loses
//     the one thing the VMS can draw.

// absencePrefix is the tag prefix the aggregate's idempotency key starts with.
//
// The window's identity is in the tag and nowhere else, which is what lets the push
// worker's read-back collapse N rows onto one remote event without a second index.
// It is `blind` rather than the state name because the STATE is what follows it and
// the state is per-row.
const absencePrefix = "aicap-blind-"

// absenceWindow is the un-sent aggregate for one blindness state.
type absenceWindow struct {
	// id is the outbox row. Zero means no row has been created yet.
	id int64
	// count is how many proposals have joined this window so far.
	count int
	// anchor is the detection the row hangs off, and it is the window's FIRST
	// proposal. The row carries a foreign key to a detection and the count belongs to
	// all of them, so it hangs off one and names the rest in its payload.
	anchor string
	// tag is the window's idempotency key, computed ONCE from the anchor's instant.
	//
	// It is a field and not a per-proposal computation because that is the whole
	// mechanism and getting it wrong is silent. `absenceWindowTag` takes an instant, so
	// calling it with each proposal's own `At` produces a different tag every time:
	// the aggregate still collapses to one row, but the tag the VMS ends up holding is
	// the LAST proposal's, and a retry of the first proposal then computes a tag that
	// matches nothing — so the idempotency the read-back exists to provide is gone,
	// and the retry becomes a second remote event.
	tag string
}

// absenceLedger is the run's open absence windows, one per blindness state.
//
// Guarded by a mutex because the aggregate is written on the frame-loop goroutine
// (`handleIncident`) while the push worker drains the outbox on its own. Two writers
// to one counter produce a count that is wrong in a way nothing can detect.
type absenceLedger struct {
	mu      sync.Mutex
	windows map[string]*absenceWindow
	// refused remembers which states have already produced the "no GUID configured"
	// fault, so an account without admin rights logs one line an hour rather than
	// five hundred identical ones.
	refused map[string]bool
}

func newAbsenceLedger() *absenceLedger {
	return &absenceLedger{windows: map[string]*absenceWindow{}, refused: map[string]bool{}}
}

// Window is the number of open windows, for the report.
//
// It is the number of *pending* aggregates, not the number of windows so far, and the
// report says so. "One pending" and "one per tick" are different facts and only one
// of them is a claim about this instant.
func (a *absenceLedger) Window() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.windows)
}

// enqueueAbsence records one blindness proposal and makes sure the window's single
// pushable alarm exists, with a count.
//
// It NEVER returns an error for an unmapped (class, state) pair. That case is a
// missing configuration, not a failed detection: the detection's own event has
// already been queued and must not be retracted because the operator has not
// registered a blindness alarm type. What it does is record the fault, once per
// state, with the pair named — so the limitation is declared rather than silent.
func (r *Runner) enqueueAbsence(ctx context.Context, ev TriggerEvent, obs vlm.Observation) {
	state := obs.State
	if !aistate.IsBlindness(state) {
		return
	}
	guid, err := r.resolveAbsenceType(ev.DetectionClass, state)
	if err != nil {
		r.noteAbsenceRefusal(ctx, ev, state, err)
		return
	}
	led := r.ledger()
	led.mu.Lock()
	defer led.mu.Unlock()

	// `w` is nil on this state's first proposal. Allocating it here rather than in
	// the map lookup above is what keeps "does this state have a window" and "make
	// one" the same branch — two lookups of the same key, one of which can miss, is
	// how a count gets attributed to a window that does not exist.
	w := led.windows[state]
	if w != nil && w.id != 0 {
		w.count++
		body, berr := r.absenceBody(ev, guid, state, w.tag, w.count)
		if berr != nil {
			r.recordAbsenceFault(ctx, ev, state, berr)
			return
		}
		payload, _ := json.Marshal(body)
		ok, rerr := r.opt.Store.RefreshAbsence(ctx, w.id, string(payload), body.Tag, w.count)
		if rerr != nil {
			r.recordAbsenceFault(ctx, ev, state, rerr)
			return
		}
		if !ok {
			// The push worker spent the row between our read and our write. The count
			// in the VMS would then disagree with the count here with nothing to say
			// which is right, so this proposal starts a NEW window — with a NEW tag,
			// because the old one names an instant that is no longer the window's first.
			r.logf("absence window for %s was already sent; this proposal opens the next one", state)
			w.id, w.count, w.anchor, w.tag = 0, 0, "", ""
		} else {
			return
		}
	}

	if w == nil {
		w = &absenceWindow{}
	}
	w.count = 1
	w.anchor = ev.DetectionIDFor(r.opt.Provenance)
	// The window's idempotency key is minted HERE, once, from the window's first
	// proposal. Every later proposal reuses it — which is what makes the read-back
	// collapse the row onto itself and what makes a retry of the first proposal find
	// the event that is already there.
	w.tag = absenceWindowTag(r.opt.RunID, state, ev.At)
	body, berr := r.absenceBody(ev, guid, state, w.tag, 1)
	if berr != nil {
		r.recordAbsenceFault(ctx, ev, state, berr)
		return
	}
	payload, _ := json.Marshal(body)
	id, eerr := r.opt.Store.Enqueue(ctx, store.OutboxEntry{
		DetectionID: w.anchor, Kind: store.KindAbsenceEvent,
		Payload: string(payload), Tag: body.Tag,
	}, r.opt.PushMax)
	if eerr != nil {
		r.recordAbsenceFault(ctx, ev, state, eerr)
		return
	}
	w.id = id
	led.windows[state] = w
	r.appendLedger(ctx, store.LedgerPushEnqueued, w.anchor,
		"kind="+store.KindAbsenceEvent, "tag="+body.Tag, "outbox="+itoa64(id),
		"aistate="+state, "class="+ev.DetectionClass, "count=1")
	r.logf("queued the aggregated %s alarm for this window: outbox %d, detection %s", state, id, w.anchor)
}

// absenceBody builds the one event a window of blindness pushes.
//
// The box is the WHOLE FRAME, and that is the load-bearing decision. The measured box
// on a detection event is one object's region; this alarm is about the camera, and a
// box drawn around one car would tell the operator that one car was unassessed when
// what happened is that nothing could be assessed at all. `xprotect.Box.NonEmpty`
// accepts a full-frame box and RC-6's zero-box refusal is deliberately unreachable
// for it, which is why this can be built at all.
func (r *Runner) absenceBody(ev TriggerEvent, guid, state, tag string, count int) (xprotect.EventRequest, error) {
	p := xprotect.Provenance{
		AIState: state,
		// The count is what makes one event a summary rather than a sample. Without it
		// "this camera was not assessed" reads as one missed question, which is the
		// reading that produces no urgency at all.
		AbsenceCount: count,
	}
	in := xprotect.EventInput{
		EventTypeGUID: guid, SourceGUID: r.opt.CameraGUID, At: ev.At, Tag: tag,
		// The class is kept so an Alarm Definition can still scope the alarm to one
		// kind of object; the blindness word lives in the tag and the note, both of
		// which the specification declares as carrying arbitrary text.
		Class:      ev.DetectionClass,
		Value:      absenceValue(ev.DetectionClass, state),
		Confidence: 0,
		Box:        fullFrameBox(),
		RuleName:   "camera-not-assessed",
		// No evidence ref, deliberately: this event is about the ABSENCE of an
		// assessment, and pointing at one proposal's clip would attach a picture to a
		// claim about five hundred. The proposals' own clips are on their own events.
		Provenance: p,
	}
	// The camera comes from the runner's configuration rather than from the
	// proposal, so there is one source for it. BuildEvent assembles
	// `cameras/<SourceGUID>` and there is nothing to correct afterwards.
	return xprotect.BuildEvent(in)
}

// fullFrameBox is the whole picture, in the same 0..1000 thousandths every other
// pushed box uses.
//
// It is a function so the constant exists in exactly one place: a full-frame box
// written out three times is three chances to write 1000 as 100 and produce an alarm
// drawn in the top-left corner of the operator's screen.
func fullFrameBox() xprotect.Box {
	return xprotect.Box{Top: 0, Left: 0, Bottom: 1000, Right: 1000}
}

// absenceValue is the pushed event's `value`: the family, then the blindness word.
//
// The word is IN the value and not only in the tag, because `value` is the field the
// Smart Client shows in the event list and the tag is not always displayed. A field a
// human has to go looking for is a field nobody reads.
func absenceValue(class, state string) string {
	fam := string(classify.FamilyOf(class))
	if fam == "" {
		fam = class
	}
	return fam + "/" + state
}

// absenceWindowTag is the idempotency key of a window's single alarm.
//
// It is derived from the run, the state and the window's FIRST proposal's instant —
// the last of which is read off the anchor detection, not from `ev.At`, so that the
// second proposal of a window computes the same tag as the first. That is the whole
// mechanism: same tag, so the second proposal's row refreshes rather than pushes, and
// the push worker's read-back finds one remote event.
func absenceWindowTag(runID, state string, first time.Time) string {
	h := store.HashOf([]byte(strings.Join([]string{runID, state,
		first.UTC().Format(time.RFC3339)}, "\x00")))
	return absencePrefix + aistateTagToken(state) + "-" + h[:16]
}

// aistateTagToken keeps a state usable inside a tag, which is also a URL query.
func aistateTagToken(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "_", "-")
}

// resolveAbsenceType answers "which Analytics Event Type does this blindness carry",
// and a nil resolver is the same answer as an unmapped pair.
//
// The nil check is not defensive noise: a runner assembled in a test — or by any
// future caller that is not `cmd/watch` — can leave the resolver unset, and a nil
// function call here would take down the frame loop in the middle of a run over the
// one condition the whole feature exists to survive. An unresolvable blindness has a
// well-defined answer already: it is recorded, named and not pushed, so that is what
// a missing resolver gets.
func (r *Runner) resolveAbsenceType(class, state string) (string, error) {
	if r.opt.EventTypeGUIDFor == nil {
		return "", fmt.Errorf("no Analytics Event Type map is wired into this runner, so (%s, %s) has no type. "+
			"Set SP_POC_EVENT_TYPE_GUIDS to %s@%s=<guid> once the type is registered on the instance; until then "+
			"the blindness is recorded here and reported by `pocctl brief` as an unsearchable window",
			class, state, "*", state)
	}
	return r.opt.EventTypeGUIDFor(class, state)
}

// ledger returns the run's open windows, creating them if a Runner was built without
// NewRunner.
//
// The lazy create is safe where it looks unsafe: `enqueueAbsence` runs only on the
// frame-loop goroutine, and NewRunner — the only constructor any real binary uses —
// already sets the field. The nil case is a test assembling a Runner literally, and
// a nil map dereferenced there would panic on the exact path this feature exists for.
func (r *Runner) ledger() *absenceLedger {
	if r.absence == nil {
		r.absence = newAbsenceLedger()
	}
	return r.absence
}

// noteAbsenceRefusal records the missing-GUID fault, once per state.
//
// Once, because an account without the rights to register Analytics Event Types
// produces this on every proposal and the log would be five hundred identical lines
// an hour, which is how a real fault next to it goes unread. The count of how many
// proposals have been refused is not lost — it is the count the window would have
// carried, and it is in the observation rows `pocctl brief` reads.
func (r *Runner) noteAbsenceRefusal(ctx context.Context, ev TriggerEvent, state string, cause error) {
	led := r.ledger()
	led.mu.Lock()
	first := !led.refused[state]
	led.refused[state] = true
	led.mu.Unlock()
	if !first {
		return
	}
	_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
		At: ev.At, Reason: store.FaultAbsenceNotTyped,
		Detail: "no Analytics Event Type is configured for (" + ev.DetectionClass + ", " + state + "), so this " +
			"window's blindness is recorded here and reported by `pocctl brief` as an unsearchable window, but " +
			"NOT pushed as an XProtect alarm. Every detection's own event is still queued. " + cause.Error() +
			" This line is printed once per state per run; the proposals behind it are counted in the observations.",
	})
	r.logf("absence alarm for %s is not pushable: %v", state, cause)
}

func (r *Runner) recordAbsenceFault(ctx context.Context, ev TriggerEvent, state string, cause error) {
	_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
		At: ev.At, Reason: store.FaultAbsenceNotTyped,
		Detail: fmt.Sprintf("the aggregated %s alarm for this window could not be queued: %v. The detection's own "+
			"event is unaffected; what is lost is the blindness alarm, and the window stays visible as an "+
			"unsearchable window in `pocctl brief`", state, cause),
	})
}

// obsIsBlindness is the predicate the call site uses, named so the call site reads
// as a sentence about the observation rather than as a lookup in a package.
func obsIsBlindness(obs vlm.Observation) bool { return aistate.IsBlindness(obs.State) }

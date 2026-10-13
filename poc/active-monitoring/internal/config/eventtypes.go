package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Sergey-Bar/vision-poc/internal/aistate"
)

// The Analytics Event Type map.
//
// An XProtect Analytics Event Type is what an Alarm Definition anchors on. One
// type per blindness state is what makes "this camera was not assessed" a
// FILTERABLE alarm an operator can act on, rather than a word buried in a
// description field that nothing in the Smart Client can match.
//
// Three states get their own type and no more:
//
//   - `not_asked`   — the model was never consulted
//   - `degraded`    — it was consulted and the answer did not arrive
//   - `cannot_tell` — it arrived and did not settle the question
//
// All three mean *we could not decide*. `no_objects` does not: it is a negative
// ANSWER, and it already drives the `false_positive` verdict on the detection's own
// event. Giving it its own alarm would fire constantly to say "nothing" — the alarm
// fatigue this product exists to remove. So `no_objects` is refused here BY NAME,
// with that sentence, rather than quietly accepted and turned into an alarm nobody
// wants.

// BlindnessStates are the three absence states that get their own Analytics Event
// Type, in CONTRACT.md §2.2's order.
//
// An ALIAS of [aistate.BlindnessStates] and not a second declaration. Three packages
// ask "is this a blindness?" — this one, the wire client, and the report — and a
// predicate declared twice drifts. A drift here is not a compile error: it is an
// absence arriving at the VMS wearing a detection's event type, which is the single
// most expensive confusion this module has.
var BlindnessStates = aistate.BlindnessStates

// IsBlindnessState reports whether a state is one of the three that justify their
// own event type.
func IsBlindnessState(s string) bool { return aistate.IsBlindness(s) }

// WildcardClass is the class half of a key that applies to every class.
//
// It exists because the three-state case is the ordinary one: an operator creates
// "camera not assessed" once, not once per COCO class. The per-class key is there
// for a site that runs different cameras through different models and wants the
// blindness alarm separated by class — and resolution tries the exact key first, so
// adding one never disturbs the others.
const WildcardClass = "*"

// EventTypeMap is a resolved `(class, absence state) -> Analytics Event Type GUID`
// map.
//
// The zero value is a usable, empty map: it resolves nothing, and every blindness
// pair is refused BY NAME at the point that would push one. That is the declared
// limitation for an account without the rights to register Analytics Event Types,
// and it is loud rather than silent — see Config.EventTypeGUIDFor.
type EventTypeMap struct {
	exact map[string]string // "car@not_asked"
	any   map[string]string // "*@not_asked"
}

// eventTypeKey is the canonical spelling of a key. Both halves are lower-cased and
// trimmed, because `Car@NotAsked` and `car@not_asked` naming the same thing and one
// of them losing to a `switch` is the kind of bug that only shows up on the second
// site.
func eventTypeKey(class, state string) string {
	return strings.ToLower(strings.TrimSpace(class)) + "@" + strings.ToLower(strings.TrimSpace(state))
}

// ParseEventTypeMap reads the `class@state=guid` entries of
// SP_POC_EVENT_TYPE_GUIDS.
//
// Every refusal names the offending ENTRY, not the variable, because the variable
// is usually long and the entry is one token of it. A refusal that says only
// "SP_POC_EVENT_TYPE_GUIDS is malformed" sends an operator to re-read the whole
// line; one that says `car@not_asked=... : 'not_asked' is not a state that gets its
// own event type` is a one-glance fix.
func ParseEventTypeMap(raw string) (EventTypeMap, error) {
	m := EventTypeMap{exact: map[string]string{}, any: map[string]string{}}
	if strings.TrimSpace(raw) == "" {
		return m, nil
	}
	for _, entry := range strings.Split(raw, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		lhs, guid, ok := strings.Cut(entry, "=")
		if !ok {
			return EventTypeMap{}, fmt.Errorf("entry %q is not <class>@<absence-state>=<guid>", entry)
		}
		class, state, ok := strings.Cut(strings.TrimSpace(lhs), "@")
		if !ok {
			return EventTypeMap{}, fmt.Errorf("entry %q has no @: the key is <class>@<absence-state>, and without the "+
				"state half there is nothing that says which kind of blindness it is", entry)
		}
		class, state = strings.TrimSpace(class), strings.ToLower(strings.TrimSpace(state))
		guid = strings.TrimSpace(guid)
		if class == "" {
			return EventTypeMap{}, fmt.Errorf("entry %q names no class; use * for the class that should apply to every one",
				entry)
		}
		if guid == "" {
			return EventTypeMap{}, fmt.Errorf("entry %q has no guid after the =; a blank GUID would be sent as the event's "+
				"type and the VMS would refuse every one of them", entry)
		}
		// The refusal the whole file exists for. `no_objects` is an answer, not a
		// blindness, and an alarm type that fires whenever a camera sees nothing is
		// the fatigue this is supposed to remove.
		if !IsBlindnessState(state) {
			return EventTypeMap{}, fmt.Errorf("entry %q names the absence state %q, which does not get its own Analytics "+
				"Event Type. Only %s do: all three mean we could not decide, and `no_objects` does not — it is a "+
				"negative ANSWER that already drives the false_positive verdict on the detection's own event. An alarm "+
				"that fires to say \"nothing\" is the alarm fatigue this exists to remove",
				entry, state, strings.Join(BlindnessStates, ", "))
		}
		key := eventTypeKey(class, state)
		bucket := m.exact
		if class == WildcardClass {
			bucket = m.any
		}
		if prev, dup := bucket[key]; dup {
			return EventTypeMap{}, fmt.Errorf("entry %q is configured twice (the first was %s). Two GUIDs for one pair means "+
				"the one that gets pushed depends on map iteration order", entry, prev)
		}
		bucket[key] = guid
	}
	return m, nil
}

// Lookup resolves `(class, state)` to a GUID.
//
// Exact first, wildcard second, and the wildcard is the fallback rather than an
// error because the per-class case is the exception. An unmapped pair returns
// `false` and the CALLER turns that into a refusal naming the pair — this function
// returns no error because "not configured" is not an error in the map, it is the
// map being empty, and only the caller knows whether an empty map is acceptable.
func (m EventTypeMap) Lookup(class, state string) (string, bool) {
	if m.exact == nil && m.any == nil {
		return "", false
	}
	if g, ok := m.exact[eventTypeKey(class, state)]; ok {
		return g, true
	}
	g, ok := m.any[eventTypeKey(WildcardClass, state)]
	return g, ok
}

// Pairs returns the configured keys, sorted, for `pocctl` to print.
//
// Sorted because a map's iteration order changes between runs, and a list of
// configured pairs that reorders itself is a list an operator cannot read a diff of.
func (m EventTypeMap) Pairs() []string {
	var out []string
	for k := range m.exact {
		out = append(out, k+"="+m.exact[k])
	}
	for k := range m.any {
		out = append(out, k+"="+m.any[k])
	}
	sort.Strings(out)
	return out
}

// Empty reports whether nothing is mapped.
func (m EventTypeMap) Empty() bool { return len(m.exact) == 0 && len(m.any) == 0 }

// EventTypeGUIDFor is the resolution the runner uses, and it is the only place the
// two GUIDs are chosen between.
//
// A blindness state resolves through the map or is REFUSED, naming the pair. The
// alternative — falling back to the ordinary detection type — is the failure this
// exists to prevent: every absence would then arrive as a line-crossing alert, an
// Alarm Definition on "vehicle crossing the line" would fire on it, and the operator
// would be paged about a car that may not exist because the module could not reach
// its own model. An absence that cannot be typed must not be pushed as something it
// is not.
//
// An ANSWERED state resolves to the ordinary detection type, which is the whole
// existing behaviour. That is why this is one function and not two: a caller with a
// class and a state and two functions to choose between would eventually choose the
// wrong one, and the wrong one is silence.
func (c Config) EventTypeGUIDFor(class, state string) (string, error) {
	if !IsBlindnessState(state) {
		if strings.TrimSpace(c.EventTypeGUID) == "" {
			return "", fmt.Errorf("no Analytics Event Type GUID is configured for the detection class %q and no "+
				"blindness state is involved, so there is nothing to push it as. Set SP_POC_EVENT_TYPE_GUID", class)
		}
		return c.EventTypeGUID, nil
	}
	if guid, ok := c.EventTypeGUIDs.Lookup(class, state); ok {
		return guid, nil
	}
	return c.refuseUnmapped(class, state)
}

// refuseUnmapped is the whole of RC-27, on its own line so that removing it is one
// substitution.
//
// It is a function and not the tail of EventTypeGUIDFor because the alternative makes
// the control un-removable-by-one-line, and a control that cannot be removed by a
// mechanical substitution is a control that will quietly stop existing while its
// description still claims it is there. The fallback it forbids is one line long —
// `return c.EventTypeGUID, nil` — and saying so here is what lets the gate try it.
func (c Config) refuseUnmapped(class, state string) (string, error) {
	known := c.EventTypeGUIDs.Pairs()
	if len(known) == 0 {
		return "", fmt.Errorf("no Analytics Event Type is configured for (%s, %s): SP_POC_EVENT_TYPE_GUIDS is empty. "+
			"Registering one needs admin rights on the XProtect instance; without them the absence stays in this "+
			"module's store, `pocctl brief` reports the window as unsearchable, and NOTHING is pushed as if it were "+
			"a crossing. Set SP_POC_EVENT_TYPE_GUIDS to %s@%s=<guid> once the type exists",
			class, state, WildcardClass, state)
	}
	return "", fmt.Errorf("no Analytics Event Type is configured for (%s, %s). SP_POC_EVENT_TYPE_GUIDS has %d "+
		"pair(s): %s. Add %s@%s=<guid>, or %s@%s=<guid> to cover every class. Falling back to the detection's own "+
		"event type would make an absence arrive as a crossing alarm, which is the exact confusion this map exists "+
		"to prevent", class, state, len(known), strings.Join(known, ", "), class, state, WildcardClass, state)
}

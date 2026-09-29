package rules

import "time"

// Schedules are site data rather than bundle data: an operator edits them in one
// place rather than in every rule that names one. That makes the resolver the
// caller's job, and until now each caller supplied its own — or none.
//
// Three callers read a schedule and they answered three different ways:
//
//   - `securityctl rules simulate` had a resolver (08:00-18:00 UTC).
//   - `internal/incidents` replay answered with the incident's first event's time.
//   - The production pipeline supplied no resolver at all, so `matchSchedule`
//     returned false for every schedule and every schedule rule in the active
//     bundle was dead at runtime.
//
// A digest an operator acknowledged therefore certified a schedule behaviour the
// product did not have, which is the one thing the activation gate exists to
// prevent. The fix is one resolver, here, that all three use.

// DefaultBusinessHours is the schedule the shipped bundle names.
//
// 08:00 to 18:00 UTC is not a claim about anyone's site; it is the documented
// default, and a site with different hours supplies its own resolver. It is
// named rather than inlined so that "what is the default" has one answer.
const DefaultBusinessHours = "business-hours"

// BusinessHoursSchedule is the default schedule resolver: 08:00-18:00 in the
// supplied location.
//
// The location is a parameter rather than hardcoded UTC because AGENTS.md is
// explicit that a site's timezone is the site's timezone and never the host's.
// The simulation and the replay paths both passed UTC unconditionally, so a site
// in UTC+3 had its business hours evaluated three hours early and an after-hours
// rule fired at two in the afternoon.
//
// An unknown schedule id is false, not true. A rule naming a schedule nobody
// defines should not fire; that is a configuration problem the compiler warns
// about, and defaulting to true would make it fire all day instead.
func BusinessHoursSchedule(id string, at time.Time, loc *time.Location) bool {
	if id != DefaultBusinessHours {
		return false
	}
	if loc == nil {
		loc = time.UTC
	}
	h := at.In(loc).Hour()
	return h >= 8 && h < 18
}

// SiteScheduleResolver returns a resolver bound to one site's location, so the
// three callers cannot drift apart by accident: given the same schedule id and
// the same instant, all three now answer the same question.
func SiteScheduleResolver(loc *time.Location) func(string, time.Time) bool {
	return func(id string, at time.Time) bool {
		return BusinessHoursSchedule(id, at, loc)
	}
}

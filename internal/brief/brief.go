// Package brief renders the shift hand-over: one structured summary built
// from the facts the pipeline already holds, byte-stable for a given input.
//
// The July 2026 controls survey's finding is that operators in life-safety
// environments still explain their reasoning to auditors by hand, and the
// July 2026 adoption survey's buyers asked for exactly this: "explain my
// reasoning in audit-ready format". The brief is that artifact, and it has a
// hard rule the whole repo shares with its golden tests — it is rendered from
// stored facts and never from model prose. A VLM sentence in a brief would be
// a sentence that could differ between two runs of the same shift, and a
// hand-over that differs between two runs of the same shift is not a record.
//
// The package is pure: Facts go in, a Brief comes out. Where the facts come
// from is the caller's business.
package brief

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/camerahealth"
)

// Facts is everything a brief is rendered from. The six groups are the six
// things an incoming operator needs, and each is owned by its own package:
// incidents and anomalies by the correlation layer, actions by the playbook
// and the duty log, camera health by the fleet, coverage by the site model
// versus the fleet, patrol notes and open items by the operator. This package
// owns none of them and imports none of them.
type Facts struct {
	Shift    string
	TenantID string
	SiteID   string
	// GeneratedAt is stamped by the caller. It is the one non-deterministic
	// input, which is why the golden tests supply it.
	GeneratedAt time.Time

	// Incidents and their anomalies: what the autonomy layer found.
	Incidents []Incident

	// Actions taken: every decision the playbook made and every override an
	// operator made, newest last.
	Actions []Action

	// Patrol notes: the free-text lines an operator left during the shift.
	Notes []Note

	// Cameras: each camera's named health state.
	Cameras []Camera

	// Coverage: per-zone, whether the cameras declared to cover it are
	// actually watching.
	Coverage []ZoneCoverage

	// OpenItems: what the next shift inherits.
	OpenItems []OpenItem
}

// Incident is one correlated incident in the brief, with the reason the
// autonomy layer gave for its decision.
type Incident struct {
	ID            string
	Title         string
	CameraIDs     []string
	Anomalies     []string
	ThreatScore   int
	Action        string
	AutonomyState string
	Reason        string
	State         string
}

// Action is one decision or override, attributed.
type Action struct {
	TS      time.Time
	Actor   string
	Subject string
	Action  string
	Reason  string
}

// Note is one patrol note, attributed.
type Note struct {
	TS     time.Time
	Actor  string
	Text   string
	Camera string
}

// Camera is one camera's health state, with its named reason when degraded.
type Camera struct {
	ID     string
	State  string
	Reason string
}

// ZoneCoverage is one zone's coverage verdict: which of its declared cameras
// are actually watching.
type ZoneCoverage struct {
	ZoneID          string
	DeclaredCameras []string
	DegradedCameras []string
	UnderObserved   bool
	Unanswerable    bool // the site model cannot tell us (e.g. no declaration)
}

// OpenItem is one thing the next shift inherits.
type OpenItem struct {
	ID      string
	Kind    string // "incident" | "camera" | "coverage" | "note"
	Summary string
}

// Brief is the rendered artifact. Every field is deterministic for a given
// Facts: slices are sorted by stable keys and no map is ever iterated.
type Brief struct {
	Shift    string `json:"shift"`
	TenantID string `json:"tenant_id,omitempty"`
	SiteID   string `json:"site_id,omitempty"`
	// GeneratedAt is echoed, never derived from the clock at render time.
	GeneratedAt time.Time `json:"generated_at"`
	Schema      string    `json:"schema"`

	// The six sections, in the order an incoming operator reads them.
	Headline      string         `json:"headline"`
	Incidents     []Incident     `json:"incidents"`
	Actions       []Action       `json:"actions"`
	Cameras       []Camera       `json:"cameras"`
	Coverage      []ZoneCoverage `json:"coverage"`
	Notes         []Note         `json:"notes"`
	OpenItems     []OpenItem     `json:"open_items"`
	FleetDegraded int            `json:"fleet_degraded"`
}

// Schema identifies the brief's wire format. It is a published contract
// surface: a field is added, never renamed or removed.
const Schema = "brief/v1"

// JSON renders the brief as the wire shape: what `ws brief` carries and what
// GET /api/briefs/latest returns. Encoding is deterministic because every
// slice in the brief is sorted at render time.
func (b Brief) JSON() ([]byte, error) {
	return json.Marshal(b)
}

// Render turns facts into a brief. It sorts every section by a stable key so
// two renders of the same facts are two identical byte strings, and it
// derives the open items it can derive rather than trusting the caller's
// list.
func Render(f Facts) Brief {
	b := Brief{
		Shift:       f.Shift,
		TenantID:    f.TenantID,
		SiteID:      f.SiteID,
		GeneratedAt: f.GeneratedAt.UTC(),
		Schema:      Schema,
	}

	b.Incidents = append(b.Incidents, f.Incidents...)
	sort.Slice(b.Incidents, func(i, j int) bool { return b.Incidents[i].ID < b.Incidents[j].ID })

	b.Actions = append(b.Actions, f.Actions...)
	sort.Slice(b.Actions, func(i, j int) bool {
		if b.Actions[i].TS.Equal(b.Actions[j].TS) {
			return b.Actions[i].Subject < b.Actions[j].Subject
		}
		return b.Actions[i].TS.Before(b.Actions[j].TS)
	})

	b.Cameras = append(b.Cameras, f.Cameras...)
	sort.Slice(b.Cameras, func(i, j int) bool { return b.Cameras[i].ID < b.Cameras[j].ID })

	b.Coverage = append(b.Coverage, f.Coverage...)
	sort.Slice(b.Coverage, func(i, j int) bool { return b.Coverage[i].ZoneID < b.Coverage[j].ZoneID })

	b.Notes = append(b.Notes, f.Notes...)
	sort.Slice(b.Notes, func(i, j int) bool {
		if b.Notes[i].TS.Equal(b.Notes[j].TS) {
			return b.Notes[i].Text < b.Notes[j].Text
		}
		return b.Notes[i].TS.Before(b.Notes[j].TS)
	})

	for _, c := range b.Cameras {
		if camerahealth.Degraded(c.State) {
			b.FleetDegraded++
		}
	}

	// Open items: the incidents still open, the cameras still degraded, the
	// zones still under-observed, and whatever the operator left. Derived
	// here so the list can never disagree with the sections above it.
	b.OpenItems = append(b.OpenItems, f.OpenItems...)
	for _, inc := range b.Incidents {
		if inc.State == "ACTIVE" || inc.State == "VERIFYING" || inc.State == "ESCALATING" {
			b.OpenItems = append(b.OpenItems, OpenItem{
				ID: inc.ID, Kind: "incident",
				Summary: fmt.Sprintf("%s — %s (%s), score %d", inc.ID, inc.Action, inc.AutonomyState, inc.ThreatScore),
			})
		}
	}
	for _, c := range b.Cameras {
		if camerahealth.Degraded(c.State) {
			b.OpenItems = append(b.OpenItems, OpenItem{
				ID: c.ID, Kind: "camera",
				Summary: fmt.Sprintf("%s %s: %s", c.ID, c.State, orDash(c.Reason)),
			})
		}
	}
	for _, z := range b.Coverage {
		if z.UnderObserved || z.Unanswerable {
			b.OpenItems = append(b.OpenItems, OpenItem{
				ID: z.ZoneID, Kind: "coverage",
				Summary: fmt.Sprintf("%s: %d of %d declared cameras degraded", z.ZoneID, len(z.DegradedCameras), len(z.DeclaredCameras)),
			})
		}
	}
	sort.Slice(b.OpenItems, func(i, j int) bool {
		if b.OpenItems[i].Kind != b.OpenItems[j].Kind {
			return b.OpenItems[i].Kind < b.OpenItems[j].Kind
		}
		return b.OpenItems[i].ID < b.OpenItems[j].ID
	})

	b.Headline = headline(b)
	return b
}

func headline(b Brief) string {
	if len(b.Incidents) == 0 && b.FleetDegraded == 0 {
		return "No incidents this shift; every camera reporting."
	}
	parts := make([]string, 0, 3)
	if len(b.Incidents) > 0 {
		parts = append(parts, fmt.Sprintf("%d correlated incidents", len(b.Incidents)))
	}
	if b.FleetDegraded > 0 {
		parts = append(parts, fmt.Sprintf("%d cameras degraded", b.FleetDegraded))
	}
	under := 0
	for _, z := range b.Coverage {
		if z.UnderObserved || z.Unanswerable {
			under++
		}
	}
	if under > 0 {
		parts = append(parts, fmt.Sprintf("%d zones under-observed", under))
	}
	shift := b.Shift
	if shift == "" {
		shift = "this shift"
	}
	return fmt.Sprintf("%s during %s.", strings.Join(parts, ", "), shift)
}

// Text renders the brief as the plain-language hand-over an incoming operator
// reads. It is deterministic: same facts, same bytes, every run. No sentence
// in it is generated — every clause is a field.
func (b Brief) Text() string {
	var out strings.Builder
	fmt.Fprintf(&out, "SHIFT HAND-OVER — %s\n", orDash(b.Shift))
	fmt.Fprintf(&out, "site %s · tenant %s · generated %s\n\n",
		orDash(b.SiteID), orDash(b.TenantID), b.GeneratedAt.UTC().Format(time.RFC3339))
	out.WriteString(b.Headline + "\n\n")

	out.WriteString("CAMERAS\n")
	if len(b.Cameras) == 0 {
		out.WriteString("  no cameras report.\n")
	}
	for _, c := range b.Cameras {
		fmt.Fprintf(&out, "  %-12s %-8s %s\n", c.ID, c.State, orDash(c.Reason))
	}

	out.WriteString("\nINCIDENTS\n")
	if len(b.Incidents) == 0 {
		out.WriteString("  none.\n")
	}
	for _, inc := range b.Incidents {
		fmt.Fprintf(&out, "  %s  score %d  %s / %s\n", inc.ID, inc.ThreatScore, orDash(inc.Action), orDash(inc.AutonomyState))
		fmt.Fprintf(&out, "      cameras: %s\n", join(orEmpty(inc.CameraIDs), "none recorded"))
		if len(inc.Anomalies) > 0 {
			fmt.Fprintf(&out, "      anomalies: %s\n", strings.Join(inc.Anomalies, ", "))
		}
		if inc.Reason != "" {
			fmt.Fprintf(&out, "      reason: %s\n", inc.Reason)
		}
	}

	out.WriteString("\nACTIONS\n")
	if len(b.Actions) == 0 {
		out.WriteString("  none.\n")
	}
	for _, a := range b.Actions {
		fmt.Fprintf(&out, "  %s  %-10s %-14s %s\n", a.TS.UTC().Format(time.RFC3339), a.Actor, orDash(a.Subject), orDash(a.Action))
		if a.Reason != "" {
			fmt.Fprintf(&out, "      why: %s\n", a.Reason)
		}
	}

	out.WriteString("\nCOVERAGE\n")
	if len(b.Coverage) == 0 {
		out.WriteString("  no zones declared.\n")
	}
	for _, z := range b.Coverage {
		verdict := "covered"
		if z.UnderObserved {
			verdict = "UNDER-OBSERVED"
		}
		if z.Unanswerable {
			verdict = "UNANSWERABLE"
		}
		fmt.Fprintf(&out, "  %-14s %-16s declared: %s\n", z.ZoneID, verdict, join(orEmpty(z.DeclaredCameras), "none"))
	}

	out.WriteString("\nNOTES\n")
	if len(b.Notes) == 0 {
		out.WriteString("  none.\n")
	}
	for _, n := range b.Notes {
		fmt.Fprintf(&out, "  %s  %-10s %s\n", n.TS.UTC().Format(time.RFC3339), n.Actor, n.Text)
	}

	out.WriteString("\nOPEN ITEMS FOR THE NEXT SHIFT\n")
	if len(b.OpenItems) == 0 {
		out.WriteString("  none.\n")
	}
	for _, o := range b.OpenItems {
		fmt.Fprintf(&out, "  [%s] %s\n", o.Kind, o.Summary)
	}
	return out.String()
}

func orDash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}

func orEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func join(s []string, empty string) string {
	if len(s) == 0 {
		return empty
	}
	return strings.Join(s, ", ")
}

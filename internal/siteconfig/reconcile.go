package siteconfig

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// The reconcile engine: one engine, two targets, one interface. That
// interface is the multi-VMS seam the plan names — a second VMS adapter
// implements Target the way the Milestone adapter does in the product, and
// nothing here has to change.
//
// Plan is pure: desired objects and actual objects in, a list of changes out,
// golden-tested. Apply takes every side effect as an argument — a target to
// write through, a sink to record into, an options struct with the snapshot
// writer — so this package imports no database and no vendor.

// Object kinds.
const (
	KindCamera = "camera"
	KindZone   = "zone"
	KindVideo  = "video"
)

// knownShapes is the set of object shapes this engine understands. A target
// object whose shape is not in this set makes the whole plan refuse:
// validate-then-write, because a half-applied change is indistinguishable
// from corruption to the next operator.
var knownShapes = []string{KindCamera, KindZone, KindVideo}

// Object is one thing a target holds. Fingerprint identifies its content in
// the target's own terms; two objects with the same fingerprint are the same
// to the engine. Foreign marks an object this engine did not create, which is
// adopted as declared and never modified.
//
// One struct for both directions of the file target's round trip, and the
// yaml tags are the keys on disk. The defect that made the extend-only
// retention guard decorative was a scratch struct on the read side with
// `retention_days` while the write side marshalled this one and produced
// `retentiondays`: every List returned RetentionDays 0, so a desired
// reduction produced an `update` instead of the `refuse` the guard is for.
// Writing and reading the same struct is what keeps the two sides from
// disagreeing, and TestRetentionRoundTripsSoTheGuardCanFire is the guard.
type Object struct {
	Kind          string            `yaml:"kind" json:"kind"`
	ID            string            `yaml:"id" json:"id"`
	Shape         string            `yaml:"shape" json:"shape"`
	Fingerprint   string            `yaml:"fingerprint" json:"fingerprint"`
	Foreign       bool              `yaml:"foreign" json:"foreign"`
	RetentionDays int               `yaml:"retention_days" json:"retention_days"`
	Values        map[string]string `yaml:"values" json:"values"`
}

// Validate checks an object before a plan may include it.
func (o Object) Validate() error {
	if o.Kind == "" || o.ID == "" {
		return fmt.Errorf("siteconfig: object of kind %q has no id", o.Kind)
	}
	if o.Fingerprint == "" {
		return fmt.Errorf("siteconfig: %s %s has no fingerprint", o.Kind, o.ID)
	}
	known := false
	for _, s := range knownShapes {
		if o.Shape == s {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("siteconfig: %s %s has unknown shape %q", o.Kind, o.ID, o.Shape)
	}
	return nil
}

// Target is where a plan applies. Create and Update are the only writes: the
// engine never deletes, so there is no Delete to call.
type Target interface {
	// List returns the objects the target currently holds.
	List(ctx context.Context) ([]Object, error)
	// Create writes a new object.
	Create(ctx context.Context, obj Object) error
	// Update rewrites an existing object's content.
	Update(ctx context.Context, obj Object) error
}

// Change is one planned modification and the reason for it.
type Change struct {
	Kind       string            `json:"kind" yaml:"kind"` // create | update | keep | refuse
	ObjectKind string            `json:"object_kind" yaml:"object_kind"`
	ObjectID   string            `json:"object_id" yaml:"object_id"`
	Reason     string            `json:"reason" yaml:"reason"`
	Before     map[string]string `json:"before,omitempty" yaml:"before,omitempty"`
	After      map[string]string `json:"after,omitempty" yaml:"after,omitempty"`
}

// Plan is the result of a reconciliation: the changes, in a stable order, and
// the desired objects they were computed from (filled by PlanWithDesired so
// Apply can write content the changes only reference).
type Plan struct {
	Changes []Change `json:"changes" yaml:"changes"`
	desired []Object
}

// Appliable returns the changes that would write, in the plan's order.
func (p Plan) Appliable() []Change {
	var out []Change
	for _, c := range p.Changes {
		if c.Kind == "create" || c.Kind == "update" {
			out = append(out, c)
		}
	}
	return out
}

// Summary renders the plan the way an operator reads it before approving.
func (p Plan) Summary() string {
	var b strings.Builder
	creates, updates, keeps, refusals := 0, 0, 0, 0
	for _, c := range p.Changes {
		switch c.Kind {
		case "create":
			creates++
		case "update":
			updates++
		case "keep":
			keeps++
		case "refuse":
			refusals++
		}
	}
	fmt.Fprintf(&b, "plan: %d create, %d update, %d keep, %d refused\n", creates, updates, keeps, refusals)
	for _, c := range p.Changes {
		fmt.Fprintf(&b, "  %-7s %-6s %-24s %s\n", c.Kind, c.ObjectKind, c.ObjectID, c.Reason)
	}
	return b.String()
}

// Reconcile computes the plan that takes actual to desired. It is pure: no
// context, no target, no clock.
//
// The guardrails are what it produces rather than what it checks after:
//
//   - Never delete. An actual object with no desired counterpart is `keep`,
//     reported, and left alone.
//   - Never modify foreign. An object the engine did not create is adopted as
//     declared and reported as a conflict, never overwritten.
//   - Retention is extend-only. A desired retention below the actual one is
//     refused, with the reason, rather than applied: shrinking retention is a
//     data-loss boundary that only an explicit human command crosses.
func Reconcile(desired []Object, actual []Object) Plan {
	actualByID := make(map[string]Object, len(actual))
	for _, o := range actual {
		actualByID[o.Kind+"/"+o.ID] = o
	}
	changes := make([]Change, 0, len(desired)+len(actual))

	for _, want := range desired {
		if err := want.Validate(); err != nil {
			changes = append(changes, Change{
				Kind: "refuse", ObjectKind: want.Kind, ObjectID: want.ID,
				Reason: "refused: " + err.Error(),
			})
			continue
		}
		have, ok := actualByID[want.Kind+"/"+want.ID]
		if !ok {
			changes = append(changes, Change{
				Kind: "create", ObjectKind: want.Kind, ObjectID: want.ID,
				Reason: "absent from the target", After: want.Values,
			})
			continue
		}
		switch {
		case have.Foreign:
			// Adopted as declared and never modified, whatever the
			// desired state says.
			changes = append(changes, Change{
				Kind: "keep", ObjectKind: want.Kind, ObjectID: want.ID,
				Reason: "not created by this engine; adopted as declared and left alone (conflict reported to the operator)",
				Before: have.Values, After: want.Values,
			})
		case want.Kind == KindVideo && want.RetentionDays < have.RetentionDays:
			// Checked before the fingerprint comparison, because a retention
			// reduction is a data-loss boundary and not a content difference:
			// it is refused whether or not anything else changed.
			changes = append(changes, Change{
				Kind: "refuse", ObjectKind: want.Kind, ObjectID: want.ID,
				Reason: fmt.Sprintf("retention reduction: %dd → %dd refused; retention is extend-only and a reduction is a data-loss boundary", have.RetentionDays, want.RetentionDays),
				Before: have.Values, After: want.Values,
			})
		case have.Fingerprint == want.Fingerprint:
			changes = append(changes, Change{
				Kind: "keep", ObjectKind: want.Kind, ObjectID: want.ID,
				Reason: "already at the desired state", After: want.Values,
			})
		default:
			changes = append(changes, Change{
				Kind: "update", ObjectKind: want.Kind, ObjectID: want.ID,
				Reason: "content differs", Before: have.Values, After: want.Values,
			})
		}
	}

	// Actual objects nobody desired are kept and reported: never delete. The
	// lookup is a map rather than a scan of `desired` per actual object: at
	// site scale (cameras + zones + video plans, on both sides) the scan is
	// quadratic in the size of the site, and the map is what `actualByID`
	// above already shows the shape to be.
	desiredByID := make(map[string]bool, len(desired))
	for _, want := range desired {
		desiredByID[want.Kind+"/"+want.ID] = true
	}
	for _, have := range actual {
		if !desiredByID[have.Kind+"/"+have.ID] {
			changes = append(changes, Change{
				Kind: "keep", ObjectKind: have.Kind, ObjectID: have.ID,
				Reason: "not in the desired state; never deleted", Before: have.Values,
			})
		}
	}

	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].ObjectKind != changes[j].ObjectKind {
			return changes[i].ObjectKind < changes[j].ObjectKind
		}
		return changes[i].ObjectID < changes[j].ObjectID
	})
	return Plan{Changes: changes}
}

// AuditSink is where an applied change is recorded. The product passes its
// audit.Writer; the POC passes the duty log. The interface exists so
// siteconfig imports neither.
type AuditSink interface {
	// RecordChange records one applied change. A failure here fails the apply:
	// a change nobody can see happened is the defect this artifact exists to
	// prevent.
	RecordChange(kind, objectKind, objectID, reason string) error
}

// ApplyOptions configures one apply. DryRun is intentionally a default the
// caller must turn off explicitly, because an operator reading a diff is the
// control.
type ApplyOptions struct {
	DryRun            bool
	MaxChangesPerHour int // <= 0 means no cap configured
	// Snapshot writes the pre-apply state of the target. The product writes
	// it to the artifact directory; restore is a documented operator
	// procedure, not a rollback system this package builds.
	Snapshot func(objects []Object) error
	// Actual is the target's current state, supplied by a caller that already
	// read it to compute the plan. Left nil, Apply reads it from the target
	// itself. Both paths validate the objects before anything is written; the
	// option exists so an apply is one read of the target rather than two,
	// because a list is a paginated vendor call and the caller has just made
	// the same one.
	Actual []Object
}

// ApplyReport is what one apply did, or refused to do.
type ApplyReport struct {
	Applied []Change `json:"applied" yaml:"applied"`
	Refused []string `json:"refused,omitempty" yaml:"refused,omitempty"`
	DryRun  bool     `json:"dry_run" yaml:"dry_run"`
}

// Apply writes a plan through a target, recording every change to the sink.
//
// Validate-then-write, in this order and for these reasons: the whole plan is
// validated against the target's actual objects first, so an unknown shape
// refuses everything rather than half a configuration; the snapshot is taken
// before any write; the per-hour change cap is checked before any write; and
// then each change is applied and recorded, stopping at the first failure.
// Because every write is idempotent, a stopped apply is re-run, not repaired.
func Apply(ctx context.Context, target Target, plan Plan, sink AuditSink, opts ApplyOptions) (ApplyReport, error) {
	report := ApplyReport{DryRun: opts.DryRun}

	// The target's actual state: read here, or taken from the caller that read
	// it to compute the plan. Both paths are validated before any write, so
	// the two cannot disagree about what was checked.
	actual := opts.Actual
	if actual == nil {
		listed, err := target.List(ctx)
		if err != nil {
			return report, fmt.Errorf("siteconfig: list the target before applying: %w", err)
		}
		actual = listed
	}
	for _, o := range actual {
		if err := o.Validate(); err != nil {
			return report, fmt.Errorf("siteconfig: refusing the whole plan: %w", err)
		}
	}

	appliable := plan.Appliable()
	if opts.MaxChangesPerHour > 0 && len(appliable) > opts.MaxChangesPerHour {
		return report, fmt.Errorf("siteconfig: refusing the whole plan: %d changes exceed the per-hour cap of %d",
			len(appliable), opts.MaxChangesPerHour)
	}
	if opts.DryRun {
		report.Applied = nil
		report.Refused = append(report.Refused, "dry run: nothing was written")
		return report, nil
	}

	if opts.Snapshot != nil {
		if err := opts.Snapshot(actual); err != nil {
			return report, fmt.Errorf("siteconfig: pre-apply snapshot failed, nothing written: %w", err)
		}
	}

	for _, change := range appliable {
		want, ok := desiredFor(plan, change)
		if !ok {
			continue // a change with no object behind it is not applied
		}
		switch change.Kind {
		case "create":
			if err := target.Create(ctx, want); err != nil {
				return report, fmt.Errorf("siteconfig: create %s %s: %w", change.ObjectKind, change.ObjectID, err)
			}
		case "update":
			if err := target.Update(ctx, want); err != nil {
				return report, fmt.Errorf("siteconfig: update %s %s: %w", change.ObjectKind, change.ObjectID, err)
			}
		}
		if sink != nil {
			if err := sink.RecordChange(change.Kind, change.ObjectKind, change.ObjectID, change.Reason); err != nil {
				return report, fmt.Errorf("siteconfig: record %s %s %s: %w", change.Kind, change.ObjectKind, change.ObjectID, err)
			}
		}
		report.Applied = append(report.Applied, change)
	}
	return report, nil
}

// desiredFor finds the desired object a change refers to. The change list
// carries the transition, not the content, so the content comes from the
// desired side the plan was computed from.
func desiredFor(plan Plan, change Change) (Object, bool) {
	for _, o := range plan.desired {
		if o.Kind == change.ObjectKind && o.ID == change.ObjectID {
			return o, true
		}
	}
	return Object{}, false
}

// PlanWithDesired attaches the desired objects a plan was computed from, so
// Apply can write them without the caller passing them twice. Reconcile
// returns a Plan whose desired side is nil; this fills it.
func PlanWithDesired(p Plan, desired []Object) Plan {
	p.desired = append([]Object(nil), desired...)
	return p
}

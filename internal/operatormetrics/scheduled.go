package operatormetrics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/audit"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/evidence"
)

// The scheduled reports.
//
// # Why a scheduled report and not a dashboard
//
// A dashboard is a screen somebody looks at. A report is an ARTEFACT: a file,
// with a digest, that somebody can be handed six months later and read. The
// difference is that the artefact is a GATE ARTEFACT -- a Postgres computation,
// never a counter reading -- so it survives a restart, it is the same every time
// it is produced for the same window, and it can be checked.
//
// # The cadences
//
// Three reports, three readers:
//
//   - the EXECUTIVE report is DAILY, and carries a weekly trend, because the
//     question it answers is "is this getting better" and that question is
//     weekly whether anybody looks daily;
//   - the SITE HEALTH report is WEEKLY, because a site's health is a shape and
//     a shape is read over a week rather than a day;
//   - the OPERATOR EFFICIENCY report is MONTHLY, because the number it carries
//     is attention minutes, and attention minutes measured over a day is a
//     number about one shift rather than about the operation.
//
// # The artefact is CONTENT-ADDRESSED
//
// Its name carries its digest, so two artefacts with the same name have the
// same content and an artefact nobody can re-derive is visible as a file whose
// name does not match its bytes.
//
// # The digest is RECORDED
//
// In the audit ledger, with the window and the path. The ledger is the
// product's authoritative record, and a report that exists on a disk with
// nothing recording that it was published is a file somebody found.

const (
	// ActionReportPublished is the audit action for a published report.
	//
	// The action vocabulary is open, and this is in it because a report is a
	// thing the product did that somebody may need to account for.
	ActionReportPublished = "report.published"
)

// reportPolicyVersion is the policy version the publication carries.
const reportPolicyVersion = "analytics/v1"

// ScheduledKind is one scheduled report.
type ScheduledKind string

// The scheduled reports.
const (
	// ReportExec is the daily executive report, with a weekly trend.
	ReportExec ScheduledKind = "exec"
	// ReportSiteHealth is the weekly site health report.
	ReportSiteHealth ScheduledKind = "site-health"
	// ReportOperatorEfficiency is the monthly operator efficiency report.
	ReportOperatorEfficiency ScheduledKind = "operator-efficiency"
)

// Cadence is how often a report is produced.
func (k ScheduledKind) Cadence() time.Duration {
	switch k {
	case ReportSiteHealth:
		return 7 * 24 * time.Hour
	case ReportOperatorEfficiency:
		return 30 * 24 * time.Hour
	}
	return 24 * time.Hour
}

// AllScheduledKinds returns every scheduled report, in the order they are
// published.
func AllScheduledKinds() []ScheduledKind {
	return []ScheduledKind{ReportExec, ReportSiteHealth, ReportOperatorEfficiency}
}

// Published is one published report.
type Published struct {
	// Kind is the report's kind.
	Kind ScheduledKind
	// Window is the period the report covers.
	Window Window
	// Digest is the SHA-256 of the artefact's bytes.
	Digest string
	// Path is where the artefact was written.
	Path string
	// Written says whether this call produced the artefact or found it already
	// published.
	//
	// The two are different answers and a caller that cannot tell them apart
	// would report the same publication every hour.
	Written bool
	// Trend is the executive report's trend, and nil for the others.
	Trend *Trend
}

// Scheduled writes the scheduled reports.
type Scheduled struct {
	// Pool is the database handle. Required.
	Pool *dbx.Pool
	// Root is where the artefacts are written. Required.
	Root string
	// Audit records the publication. Required for the digest to be recorded.
	Audit *audit.Writer
	// Sites is the scope the reports read.
	Sites []string
	// Now is the clock. It is a field so that a test can pin it.
	Now func() time.Time
}

// now is the writer's clock, defaulted.
func (s *Scheduled) now() time.Time {
	if s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now().UTC()
}

// Publish writes the report for one window, if it has not been published.
//
// It is IDEMPOTENT: a window that has already been published is found in the
// ledger and reported as found, not written again. That matters because the
// loop runs on an interval and the window it derives is the same window until
// the period ends, so without the check the product would publish the same
// report every hour.
func (s *Scheduled) Publish(ctx context.Context, tenant string, kind ScheduledKind, w Window) (Published, error) {
	out := Published{Kind: kind, Window: w}
	if s.Pool == nil {
		return out, ErrNoPool
	}
	if s.Root == "" {
		return out, fmt.Errorf("operatormetrics: no report root is configured, so there is " +
			"nowhere to publish a report to")
	}
	if tenant == "" {
		return out, fmt.Errorf("operatormetrics: a report with no tenant is a report about everybody")
	}
	if s.Audit == nil {
		// Refused rather than published without a record.
		//
		// A report whose digest is not in the ledger is a file on a disk with
		// nothing saying the product published it, and the whole point of the
		// ledger record is that somebody can find out. Publishing it anyway
		// would make the artefact and the record disagree, and the record is
		// the one that is authoritative.
		return out, fmt.Errorf("operatormetrics: no audit writer is configured, so the " +
			"publication could not be recorded, and a report nobody can find out about is not " +
			"a report")
	}

	// Already published? The ledger says.
	prior, err := s.published(ctx, tenant, kind, w)
	if err != nil {
		return out, err
	}
	if prior != nil {
		prior.Written = false
		return *prior, nil
	}

	reader := &Reader{Pool: s.Pool, Sites: s.Sites}
	report, err := reader.Read(ctx, tenant, "", "", w)
	if err != nil {
		return out, err
	}
	if err := report.Validate(); err != nil {
		return out, fmt.Errorf("operatormetrics: the %s report does not validate against its own "+
			"rules, so it is not published: %w", kind, err)
	}

	// The executive report carries a TREND, because the question it answers is
	// "is this getting better" and a number without a direction does not answer
	// it.
	if kind == ReportExec {
		trend, err := reader.Trend(ctx, tenant, "", "", metricNorthStar, w)
		if err != nil {
			return out, err
		}
		out.Trend = &trend
	}

	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return out, fmt.Errorf("operatormetrics: render the %s report: %w", kind, err)
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])

	path, err := s.writeArtefact(fmt.Sprintf("%s-%s.json", kind, digest), body)
	if err != nil {
		return out, err
	}
	if err := s.record(ctx, tenant, kind, w, digest, path); err != nil {
		return out, err
	}
	out.Digest = digest
	out.Path = path
	out.Written = true
	return out, nil
}

// published finds a report that has already been published for the window.
func (s *Scheduled) published(ctx context.Context, tenant string, kind ScheduledKind, w Window) (*Published, error) {
	target := reportTarget(kind, w)
	var digest, path string
	err := s.Pool.For(tenant, "report").QueryRow(ctx,
		`SELECT reason, evidence_refs FROM audit_ledger
		  WHERE tenant_id = $1 AND action = $2 AND target_id = $3
		  ORDER BY sequence DESC LIMIT 1`,
		tenant, ActionReportPublished, target).Scan(&digest, &path)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("operatormetrics: read the published reports: %w", err)
	}
	return &Published{Kind: kind, Window: w, Digest: digest, Path: path}, nil
}

// reportTarget is the ledger's name for one published report.
//
// The kind and the window, so a reader of the ledger can tell WHICH report
// this was without opening the artefact.
func reportTarget(kind ScheduledKind, w Window) string {
	return fmt.Sprintf("%s:%s:%s", kind,
		w.From.UTC().Format(time.RFC3339), w.To.UTC().Format(time.RFC3339))
}

// record writes the publication into the ledger.
//
// It is a ledger entry rather than a row in a table of its own, because the
// ledger is append-only, hash-chained and already the product's authoritative
// record: a report that exists with nothing recording that it was published is
// a file somebody found.
func (s *Scheduled) record(ctx context.Context, tenant string, kind ScheduledKind, w Window, digest, path string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("operatormetrics: begin the publication record: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = s.Audit.Append(ctx, tx, audit.Entry{
		TenantID:      tenant,
		Actor:         "report/" + string(kind),
		ActorType:     audit.ActorSystem,
		Action:        ActionReportPublished,
		TargetType:    "analytics_report",
		TargetID:      reportTarget(kind, w),
		PolicyVersion: reportPolicyVersion,
		Reason:        digest,
		EvidenceRefs:  []string{path},
		SiteScope:     s.Sites,
	})
	if err != nil {
		return fmt.Errorf("operatormetrics: record the publication: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("operatormetrics: commit the publication record: %w", err)
	}
	return nil
}

// writeArtefact writes the report under the root.
//
// The NAME is validated by `internal/evidence`'s path guard before anything is
// written, because a name that crossed out of the root would reach wherever
// the process can write, and the guard that already refuses those is the one
// that refuses them here.
func (s *Scheduled) writeArtefact(name string, body []byte) (string, error) {
	if err := evidence.ValidateRef(name); err != nil {
		return "", fmt.Errorf("operatormetrics: the report name %q was refused: %w", name, err)
	}
	full := filepath.Join(s.Root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return "", fmt.Errorf("operatormetrics: create the report directory: %w", err)
	}
	if err := os.WriteFile(full, body, 0o640); err != nil {
		return "", fmt.Errorf("operatormetrics: write the report: %w", err)
	}
	return full, nil
}

// Due returns the kinds that are DUE and the windows they cover.
//
// A report is due when its period has ended and it has not been published. The
// period that just closed is the one whose end is in the past, so the daily
// report is due every day after midnight and the monthly one is due on the
// first of the month.
func (s *Scheduled) Due(ctx context.Context, tenant string) ([]ScheduledKind, []Window, error) {
	now := s.now()
	var kinds []ScheduledKind
	var windows []Window
	for _, kind := range AllScheduledKinds() {
		w := periodWindow(kind, now)
		published, err := s.published(ctx, tenant, kind, w)
		if err != nil {
			return nil, nil, err
		}
		if published != nil {
			continue
		}
		kinds = append(kinds, kind)
		windows = append(windows, w)
	}
	return kinds, windows, nil
}

// periodWindow is the window a kind's LAST COMPLETE period covered.
//
// The end is truncated to the DAY, because a daily report is a day and a
// weekly one is seven of them, and a window that started at 09:17 would be a
// period nobody recognises.
//
// The length is the kind's own CADENCE, so the two cannot drift: a report whose
// cadence changed would otherwise keep writing the old period's window.
func periodWindow(kind ScheduledKind, now time.Time) Window {
	day := now.UTC().Truncate(24 * time.Hour)
	return Window{From: day.Add(-kind.Cadence()), To: day}
}

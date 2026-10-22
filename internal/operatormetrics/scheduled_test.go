package operatormetrics_test

// The scheduled report, and the two properties it has to have.
//
// # It is IDEMPOTENT
//
// A window that has already been published is FOUND, not written again. The
// loop checks every hour and the window it derives is the same window until
// the period ends, so a writer that did not check would publish the same daily
// report twenty-four times a day.
//
// # It is CONTENT-ADDRESSED and RECORDED
//
// The artefact's name carries its digest, and the digest is written to the
// audit ledger with the window and the path. A report that exists on a disk
// with nothing recording that it was published is a file somebody found.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/audit"
	"github.com/Sergey-Bar/vision/internal/operatormetrics"
)

// TestTheScheduledReportIsPublishedOnceAndRecorded is the whole thing.
//
// The report is published, the artefact is on disk under a content-addressed
// name, the ledger records the digest, and a second publication of the same
// window finds the first one rather than writing a second file.
func TestTheScheduledReportIsPublishedOnceAndRecorded(t *testing.T) {
	ctx := context.Background()
	h := setup(t)
	root := t.TempDir()

	// The rollup the report reads.
	win := operatormetrics.Window{
		From: time.Now().UTC().Truncate(time.Hour).Add(-24 * time.Hour),
		To:   time.Now().UTC().Truncate(time.Hour),
	}
	rollup := &operatormetrics.Rollup{Pool: h.pool, SiteID: h.site}
	if _, err := rollup.Write(ctx, h.tenant,
		operatormetrics.Bucket{Start: win.From, End: win.To}); err != nil {
		t.Fatalf("derive the rollup: %v", err)
	}

	scheduled := &operatormetrics.Scheduled{
		Pool:  h.pool,
		Root:  root,
		Audit: &audit.Writer{Pool: h.pool},
		Sites: []string{h.site},
	}

	first, err := scheduled.Publish(ctx, h.tenant, operatormetrics.ReportSiteHealth, win)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !first.Written {
		t.Fatal("the first publication says it wrote nothing")
	}
	if first.Digest == "" {
		t.Error("the publication carries no digest")
	}

	// The artefact is on disk, under a name that carries the digest.
	body, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatalf("read the artefact: %v", err)
	}
	if filepath.Base(first.Path) != string(operatormetrics.ReportSiteHealth)+"-"+first.Digest+".json" {
		t.Errorf("the artefact is named %q, and the name does not carry the digest %q",
			filepath.Base(first.Path), first.Digest)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("the artefact is not JSON: %v", err)
	}
	if doc["report_version"] != operatormetrics.ReportVersion {
		t.Errorf("the artefact's report version is %v, want %q",
			doc["report_version"], operatormetrics.ReportVersion)
	}

	// The ledger records the publication.
	var digest, path string
	if err := h.pool.For(h.tenant, "test").QueryRow(ctx,
		`SELECT reason, evidence_refs FROM audit_ledger
		  WHERE tenant_id = $1 AND action = $2
		  ORDER BY sequence DESC LIMIT 1`,
		h.tenant, operatormetrics.ActionReportPublished).Scan(&digest, &path); err != nil {
		t.Fatalf("read the publication record: %v", err)
	}
	if digest != first.Digest {
		t.Errorf("the ledger records the digest %q, want %q", digest, first.Digest)
	}

	// And a second publication of the SAME window finds the first.
	second, err := scheduled.Publish(ctx, h.tenant, operatormetrics.ReportSiteHealth, win)
	if err != nil {
		t.Fatalf("publish again: %v", err)
	}
	if second.Written {
		t.Error("the second publication of the same window wrote a second artefact. The loop " +
			"checks every hour and the window does not change until the period ends")
	}
	if second.Digest != first.Digest {
		t.Errorf("the second publication found the digest %q, want the first one's %q",
			second.Digest, first.Digest)
	}

	// And there is exactly ONE artefact.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read the report root: %v", err)
	}
	if len(entries) != 1 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the report root holds %d artefacts, want 1: %v", len(entries), names)
	}
}

// TestTheScheduledReportKindsHaveTheirCadences is the cadence table.
//
// The three reports answer three questions, and a report that answers the
// wrong question at the wrong frequency is a report nobody reads.
func TestTheScheduledReportKindsHaveTheirCadences(t *testing.T) {
	want := map[operatormetrics.ScheduledKind]time.Duration{
		operatormetrics.ReportExec:               24 * time.Hour,
		operatormetrics.ReportSiteHealth:         7 * 24 * time.Hour,
		operatormetrics.ReportOperatorEfficiency: 30 * 24 * time.Hour,
	}
	kinds := operatormetrics.AllScheduledKinds()
	if len(kinds) != len(want) {
		t.Fatalf("there are %d scheduled reports, want %d", len(kinds), len(want))
	}
	for _, k := range kinds {
		if got := k.Cadence(); got != want[k] {
			t.Errorf("%s has the cadence %s, want %s", k, got, want[k])
		}
	}
}

// Command probe_vlm stores a structured fact-object as evidence and enqueues one
// VLM job referencing it, so a running security-core (with a bound AI engine) can
// investigate it and record a real, non-degraded observation.
//
// It is the end-to-end proof the whole AI-plane work has been building toward: the
// worker fetches the bytes, sends them to the plane with the investigate schema, and
// the engine answers a cited answer. This command drives only the ENQUEUE half; the
// running worker and plane do the rest, so the proof runs against exactly the code an
// operator's site runs, not a stand-in.
//
// It exists as a standalone main rather than a test because it needs a live plane and
// a running worker, and a Go test that boots those (and skips without Python) cannot
// be honestly declared in the skip-audit ledger. The boot gate drives it and asserts
// the observation lands. The fact-object it stores is evidence bytes the three rules
// never emit today -- structured facts -- which is the honest v1 shape of the
// investigate role: it consumes facts, and a VLM for raw bytes is a later backend.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/evidence"
	"github.com/Sergey-Bar/vision/internal/jobs"
	"github.com/jackc/pgx/v5"
)

func main() {
	tenant := os.Getenv("PROBE_TENANT")
	site := os.Getenv("PROBE_SITE")
	root := os.Getenv("SP_EVIDENCE_ROOT")
	if tenant == "" || site == "" || root == "" {
		fmt.Fprintln(os.Stderr, "probe_vlm: PROBE_TENANT, PROBE_SITE and SP_EVIDENCE_ROOT are required")
		os.Exit(2)
	}

	ctx := context.Background()
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv("SP_DATABASE_URL")); err != nil {
		fmt.Fprintf(os.Stderr, "probe_vlm: SP_DATABASE_URL: %v\n", err)
		os.Exit(2)
	}
	pool, err := dbx.Open(ctx, cfg, os.Getenv("SP_DATABASE_PASSWORD"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "probe_vlm: open: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	// One incident to attach the observation to (the row's NOT NULL reference). The
	// seed created incidents for the tenant; use the newest.
	var incident string
	if err := pool.QueryRow(ctx,
		`SELECT incident_id FROM incidents WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT 1`,
		tenant).Scan(&incident); err != nil {
		fmt.Fprintf(os.Stderr, "probe_vlm: no incident for tenant %q: %v (seed the tenant first)\n", tenant, err)
		os.Exit(1)
	}

	// A structured fact, the shape the investigate engine reads. Determining bytes:
	// the engine investigates over facts, so we store facts, not a clip.
	facts, _ := json.Marshal(map[string]any{
		"kind":                 "person",
		"count":                2,
		"zone":                 "restricted",
		"restrictedZoneBreach": true,
		"afterHours":           false,
		"dwellSeconds":         42,
	})
	store, err := evidence.New(evidence.Options{Root: root})
	if err != nil {
		fmt.Fprintf(os.Stderr, "probe_vlm: evidence store: %v\n", err)
		os.Exit(1)
	}
	obj, err := store.Put(tenant, bytes.NewReader(facts))
	if err != nil {
		fmt.Fprintf(os.Stderr, "probe_vlm: put evidence: %v\n", err)
		os.Exit(1)
	}

	payload, _ := json.Marshal(map[string]any{
		"incidentId":     incident,
		"eventId":        incident, // event_id carries no FK; binding it to the incident is honest
		"evidenceId":     obj.Ref,
		"evidenceSha256": obj.SHA256,
		"profileId":      "investigate/v1",
		"contentType":    "application/json",
	})
	// Enqueue inside the tenant context with the site id set. The worker records the
	// observation under the job's site, and site id is mandatory attribution on every
	// observation (an unattributable answer is one an operator cannot act on), so a
	// job with no site would produce a job the worker validly refuses to record.
	if err := pool.InTenant(ctx, dbx.TenantContext{TenantID: tenant, ActorID: "e2e"}, func(tx pgx.Tx) error {
		_, _, err := jobs.New(pool).Enqueue(ctx, tx, jobs.Enqueue{
			TenantID:       tenant,
			SiteID:         site,
			Queue:          jobs.QueueVLM,
			IdempotencyKey: "e2e-" + obj.SHA256,
			Payload:        payload,
			MaxAttempts:    5,
		})
		return err
	}); err != nil {
		fmt.Fprintf(os.Stderr, "probe_vlm: enqueue: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("incident=%s\tevidenceId=%s\tevidenceSHA256=%s\n",
		incident, obj.Ref, obj.SHA256)

	// Wait for the running worker to investigate it against the real plane and
	// record the observation, then report it. This is the end the whole AI plane was
	// built for, so the probe proves it end to end: non-degraded, cited, attributed
	// to the investigate engine. It reports rather than asserts so the boot gate can
	// quote the row; a non-degraded cited observation is exit 0, anything else is
	// exit 1 with what it saw.
	const modelID = "vision-investigate-facts/1"
	q := `SELECT COALESCE(degraded_reason,'') AS dr, model,
	             structured_output->>'citations' AS citations,
	             structured_output->>'confidence' AS confidence,
	             insufficient_evidence
	        FROM ai_observations
	       WHERE tenant_id = $1 AND incident_id = $2
	       ORDER BY created_at DESC LIMIT 1`
	deadline := time.Now().Add(45 * time.Second)
	for {
		var dr, model, citations, confidence string
		var insufficient bool
		err := pool.QueryRow(ctx, q, tenant, incident).Scan(&dr, &model, &citations, &confidence, &insufficient)
		if err == nil && model == modelID {
			fmt.Printf("observation: degraded=%q model=%s citations=%s confidence=%s insufficient=%v\n",
				dr, model, citations, confidence, insufficient)
			// The real answer. The stored degraded_reason is the enum "none" (or
			// empty) for a non-degraded observation and a named reason otherwise;
			// "none" is the non-degraded case, not a degraded one.
			nonDegraded := dr == "" || dr == "none"
			if nonDegraded && citations != "" && citations != "[]" && !insufficient {
				os.Exit(0)
			}
			os.Exit(1)
		}
		if time.Now().After(deadline) {
			if err != nil {
				fmt.Fprintf(os.Stderr, "probe_vlm: no observation appeared within 45s: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "probe_vlm: no investigate observation within 45s (last model=%q)\n", model)
			}
			os.Exit(1)
		}
		time.Sleep(1 * time.Second)
	}
}

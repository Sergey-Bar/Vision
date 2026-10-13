package main

// Evidence, the AI plane, and a reviewer's disposition.
//
// # Evidence
//
// The three committed clips go through `evidence.Store.Put`, which generates the
// storage reference from the content hash — so the reference, the sha256 in the
// `evidence` row and the bytes on disk are the same fact stated three times, and
// `VerifyHashOnRead` re-derives it on the way out. Each clip is attached to the
// oldest incident raised during the ingest, because an evidence row nobody can
// reach from an incident is an evidence row the console will not show.
//
// # The AI plane, degraded on purpose
//
// Every incident gets an observation, and which kind it gets is the point:
//
//   - The FIRST incident gets a healthy, attributed observation. This is what
//     makes the rest of the demo possible: an all-degraded site has no
//     corroboration to show and no reviewer note to disagree with.
//   - The rest get EXPLICITLY DEGRADED observations, each naming the failure it
//     stands in for — `no_runtime` for the ones that would have needed a model
//     that is not there, `timeout` for one, `model_unavailable` for another.
//
// The degradation is not decoration and it is not a failure of the seed. A
// missing AI observation is NEVER a negative observation: `verification_results`
// carries an `ai_degradation` reason beside every degraded decision, the incident
// carries `degraded = true`, and the console's absence panel has something true to
// say. A site where every observation is healthy proves nothing about that, and a
// seed that made them all healthy would be hiding the product's central invariant
// behind a nicer-looking demo.
//
// The degraded rows go through `observations.Writer.RecordDegraded`, which is the
// same call the VLM worker makes when a model is unreachable, and which REFUSES a
// degradation with no reason — the first control in the shipped red-check table.
// A degraded observation with no reason is a row that reads as a negative answer,
// so this seed could not write one even if it wanted to.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/incidents"
	"github.com/Sergey-Bar/vision/internal/observations"
)

// storeEvidence puts the committed clips into the evidence store and links them to
// the oldest incident.
func (s *seeder) storeEvidence(ctx context.Context) error {
	clips, err := committedClips(s.opts.clipsDir)
	if err != nil {
		return err
	}
	if len(clips) == 0 {
		return errf("security-seed: no clips in %s. The evidence store would be empty and "+
			"every incident would render as \"no clip retained\", which is a real state but "+
			"a poor thing to demonstrate", s.opts.clipsDir)
	}
	incidentID, err := s.oldestIncident(ctx)
	if err != nil {
		return err
	}
	if incidentID == "" {
		return errf("security-seed: the fixtures produced no incident, so there is nothing " +
			"to attach evidence to. An evidence store with no incident pointing at it is " +
			"not a demo; check that the bundle's production rule is reachable")
	}
	deviceID := "dev-entrance-01"

	var stored int
	for _, clip := range clips {
		f, err := os.Open(clip.path)
		if err != nil {
			return fmt.Errorf("open the clip %s: %w", clip.path, err)
		}
		obj, putErr := s.evidence.Put(s.opts.tenant, f)
		closeErr := f.Close()
		if putErr != nil {
			return fmt.Errorf("store the clip %s: %w", clip.name, putErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close the clip %s: %w", clip.name, closeErr)
		}
		observed := s.epoch
		if _, err := s.scoped(ctx).Exec(ctx, `
			INSERT INTO evidence (evidence_id, tenant_id, site_id, incident_id, camera_id,
			                      device_id, kind, tier, observed_at, captured_at, storage_ref,
			                      media_type, size_bytes, sha256, retention_policy, retain_until)
			VALUES ($1,$2,$3,$4,$5,$6,'clip','A',$7,$7,$8,$9,$10,$11,'default',$12)`,
			"evd-"+clip.name, s.opts.tenant, s.opts.site, incidentID, "cam-CAM-001", deviceID,
			observed, obj.Ref, clip.mediaType, obj.SizeBytes, obj.SHA256,
			observed.Add(90*24*time.Hour)); err != nil {
			return fmt.Errorf("record the evidence row for %s: %w", clip.name, err)
		}
		if _, err := s.scoped(ctx).Exec(ctx, `
			INSERT INTO incident_evidence (tenant_id, incident_id, evidence_id)
			VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
			s.opts.tenant, incidentID, "evd-"+clip.name); err != nil {
			return fmt.Errorf("link the evidence to %s: %w", incidentID, err)
		}
		stored++
		s.say("%s -> %s  sha256 %s  %d bytes", clip.name, obj.Ref, shortHash(obj.SHA256), obj.SizeBytes)
		row := EvidenceRow{
			Name: clip.name, EvidenceID: "evd-" + clip.name, Ref: obj.Ref,
			SHA256: obj.SHA256, SizeBytes: obj.SizeBytes, IncidentID: incidentID,
			DetectorZones: clip.DetectorZones,
		}
		s.report.Evidence = append(s.report.Evidence, row)
	}
	s.say("%d clip(s) stored by content hash and linked to %s", stored, incidentID)
	return nil
}

// clip is one committed media file.
type clip struct {
	name      string
	path      string
	mediaType string
	// DetectorZones names the LINES and POLYGONS the POC drew for this clip, read
	// from the sibling `<name>.zones.json`.
	//
	// These are NOT the product's `zones` table. A `.zones.json` says where in a
	// FRAME to look — a kerb line at y=0.5, a roadway polygon — and the product's
	// zone is a named place in a SITE's tree with a path a rule filters on. They
	// are different concepts and mapping one onto the other would put a frame
	// coordinate into a column that means "the loading bay".
	//
	// The seed therefore reads the file for its PROVENANCE — which line this clip's
	// ground truth was drawn against — and seeds the product's zone tree from the
	// shipped rule bundle's own requirement instead. Both facts are in the report.
	DetectorZones []string
}

// committedClips finds the clips to store.
//
// Only what the tree actually commits, and only `*.mp4`. The POC's testdata is
// where the three committed clips live, so that is the default directory even
// though it belongs to another Go module: the media is a committed fixture of
// THIS repository and the plan names those three files specifically.
func committedClips(dir string) ([]clip, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.mp4"))
	if err != nil {
		return nil, fmt.Errorf("list the clips in %s: %w", dir, err)
	}
	sort.Strings(matches)
	out := make([]clip, 0, len(matches))
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			return nil, fmt.Errorf("stat the clip %s: %w", m, err)
		}
		if info.IsDir() {
			continue
		}
		base := filepath.Base(m)
		c := clip{
			name:      base,
			path:      m,
			mediaType: "video/mp4",
		}
		if names, err := readDetectorZones(filepath.Join(dir, strings.TrimSuffix(base, ".mp4")+".zones.json")); err == nil {
			c.DetectorZones = names
		}
		out = append(out, c)
	}
	return out, nil
}

// readDetectorZones names the lines a `.zones.json` declares, without importing
// the POC's types.
//
// The POC is a separate Go module with its own `go.mod`, so importing its zone
// type would make the product module depend on the proof of concept. Reading three
// field names out of the JSON is the honest amount of coupling: enough for the
// report to say which line a clip was drawn against, and no more.
func readDetectorZones(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Lines []struct {
			Name string `json:"name"`
		} `json:"lines"`
		Polygons []struct {
			Name string `json:"name"`
		} `json:"polygons"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	var out []string
	for _, l := range doc.Lines {
		out = append(out, "line:"+l.Name)
	}
	for _, p := range doc.Polygons {
		out = append(out, "polygon:"+p.Name)
	}
	return out, nil
}

// writeObservations writes one healthy observation and one degraded observation
// per remaining incident.
func (s *seeder) writeObservations(ctx context.Context) error {
	incidents, err := s.incidentList(ctx)
	if err != nil {
		return err
	}
	if len(incidents) == 0 {
		return errf("security-seed: there are no incidents, so the AI plane has nothing to " +
			"have an opinion about. The console's degradation notice is the demo's best " +
			"moment and it needs an incident to hang on")
	}
	// The observation writer with BOTH of its golden-test seams injected.
	//
	// `observations.Writer` has an `IDGenerator` and a `Now`, and the default id
	// is a random hex string with `time.Now()` beside it. Using the defaults here
	// would put two wall-clock-dependent values in the middle of a seed whose whole
	// claim is that it repeats, so both are injected — the observation ids become a
	// function of the incident and the clock becomes the replay epoch.
	//
	// These are the same seams the golden tests use, which is the point: a seam
	// that only a test can reach is a seam the product cannot rely on.
	n := 0
	writer := &observations.Writer{
		Pool: s.pool,
		IDGenerator: func(_, profile, _ string) string {
			n++
			return fmt.Sprintf("obs_%s_seed_%03d", profile, n)
		},
		Now: func() time.Time { return s.epoch },
	}

	for i, inc := range incidents {
		eventID := inc.triggerEventID
		if eventID == "" {
			// Never observed, and never invented: an observation's `event_id` is
			// NOT NULL and names what the model was asked about. An incident with
			// no supporting event is a product defect the seed reports rather than
			// papers over.
			return errf("security-seed: incident %s cites no supporting event, so no "+
				"observation can be recorded against it", inc.incidentID)
		}
		// The attribution every seeded observation carries, healthy or degraded.
		//
		// `ai_observations.model` and `runtime_version` are NOT NULL and
		// `observation.Validate` refuses a row without them, so even a DEGRADED
		// observation has to say what would have answered — which is exactly the
		// right shape: "the profile that did not answer" is more useful than a
		// blank row, and it is what an operator debugging a degradation needs.
		//
		// `model_hash` is EMPTY and that is the honest value, not an oversight.
		// `Observation.ModelHash`'s own comment says the field may be empty exactly
		// when the provider gave us no weights, and this product did not ship a
		// bundle here — so the row takes the hosted-provider branch and is
		// attributed by `model_identity` + `model_contract_version` instead. A
		// synthetic 64-character string would satisfy a column check while
		// defeating the only thing the column is for, which is the sentence
		// `internal/identity/keystore.go` was caught by gosec for.
		base := observations.Observation{
			TenantID: s.opts.tenant, SiteID: s.opts.site,
			IncidentID: inc.incidentID, EventID: eventID,
			ProfileID:            productionProfile,
			CreatedAt:            inc.firstEventAt,
			Model:                seededModelIdentity,
			ModelIdentity:        seededModelIdentity,
			ModelContractVersion: seededContractVersion(),
			RuntimeVersion:       seededRuntimeVersion,
			PromptTemplateID:     productionProfile,
			PromptVersion:        "1",
			OutputSchemaVersion:  1,
			InputEvidenceIDs:     []string{seededEvidenceID},
		}

		if i == 0 {
			o := base
			o.Temperature = 0
			o.LatencyMS = 812
			o.Hardware = "seeded (no model ran)"
			// The field names are the profile's own: `restricted-person` declares
			// exactly these seven, `validateAgainstProfile` refuses anything else,
			// and a bundle may only name a field the profile emits. So this is the
			// shape the shipped rule reads, not a shape invented for the demo.
			o.StructuredOutput = map[string]any{
				"insufficientEvidence": false,
				"persons":              1,
				"accessState":          "no_access_recorded",
				"restrictedZoneBreach": true,
				"afterHours":           true,
				"summary": "One person inside the loading bay after hours. No badge event " +
					"was seen in the window, so the absence of one is reported as an " +
					"absence and not as a clearance.",
				"confidence": 0.91,
			}
			recorded, err := writer.Record(ctx, o)
			if err != nil {
				return fmt.Errorf("record the healthy observation for %s: %w", inc.incidentID, err)
			}
			s.say("%s: observation %s recorded, model %s, confidence 0.91, NOT degraded",
				inc.incidentID, recorded.ObservationID, seededModelIdentity)
			s.report.Observations = append(s.report.Observations, ObservationRow{
				IncidentID: inc.incidentID, ObservationID: recorded.ObservationID,
				Degraded: false, Reason: "none",
			})
			continue
		}

		// The degraded ones, rotating through the reasons the schema allows so the
		// console's degradation notice has more than one cause to name.
		reason, detail := degradedReasonFor(i)
		o := base
		o.StructuredOutput = map[string]any{}
		recorded, err := writer.RecordDegraded(ctx, o, reason, detail)
		if err != nil {
			return fmt.Errorf("record the degraded observation for %s: %w", inc.incidentID, err)
		}
		s.say("%s: observation %s DEGRADED with reason %s - %s. This is NOT a negative "+
			"answer; the decision stands on the deterministic half", inc.incidentID,
			recorded.ObservationID, reason, detail)
		s.report.Observations = append(s.report.Observations, ObservationRow{
			IncidentID: inc.incidentID, ObservationID: recorded.ObservationID,
			Degraded: true, Reason: string(reason),
		})
	}
	return nil
}

// The observation facts the seeded site records.
//
// A fixed model hash, not a computed one. `ai_observations.model_hash` is NOT NULL
// and `observation.Validate` refuses a row with neither it nor the hosted
// model identity and contract version. This value is a LABEL for a model this
// repository did not run, and saying so in the name is the honest form:
//
// the observation is attributed to "seeded/absent", not to a real provider, and
// `model_hash` records that nothing was hashed because nothing ran.
const (
	productionProfile    = "restricted-person"
	seededModelIdentity  = "seeded/absent"
	seededRuntimeVersion = "seeded/1.0.0"
	// seededDispositionActor is the site operator, by user id. A disposition
	// without a named actor is an opinion rather than a record.
	seededDispositionActor = "usr-operator"
	// seededEvidenceID is the evidence row every observation names as its input.
	// It is the first stored clip's row, and naming it is what makes the
	// observation traceable back to bytes rather than to a sentence.
	seededEvidenceID = "evd-car-crossing.mp4"
)

// seededContractVersion is a content hash over the four things
// `Observation.ModelContractVersion` says it is a hash OF: the prompt template,
// the prompt version, the output schema version and the API model name.
//
// Computed rather than typed, because the field's own comment says it is
// "reproducible from the repository" and a hand-written string in a seed is not
// reproducible from anything — it is a claim that happens to look like a hash.
func seededContractVersion() string {
	sum := sha256.Sum256([]byte(productionProfile + "\x00" + "1\x00" + "1\x00" + seededModelIdentity))
	return hex.EncodeToString(sum[:])
}

// degradedReasonFor rotates the three reasons a site with no model plane really
// sees, so the console's degradation notice has more than one cause to name.
//
// `no_runtime` first because that is what `config` leaves an install with when
// `SP_AI_ENDPOINT` is unset, and it is the honest state for this container: there
// is no AI runtime here and every observation is standing in for one.
func degradedReasonFor(index int) (observations.Degradation, string) {
	switch index % 3 {
	case 1:
		return observations.DegradationNoRuntime,
			"no AI runtime is configured for this install; every observation is recorded " +
				"as degraded with this reason rather than as a negative answer"
	case 2:
		return observations.DegradationTimeout,
			"the runtime did not answer inside the client deadline; the question reached " +
				"the model and did not come back as an answer"
	default:
		return observations.DegradationModelUnavailable,
			"the configured provider refused the request; the credential or the quota is " +
				"an operator problem and this row is where it is visible"
	}
}

// writeDisposition records the reviewer's answer on the first incident.
//
// It is what makes `search_documents.unindexed_vector` have something in it: the
// view unions the deterministic explanation with `dispositions.notes` and with the
// model's `structured_output`, and a seeded site with no disposition has an
// unindexed column that is empty for every row.
//
// Two choices here are deliberate and both are visible in the console:
//
//   - `agreement: disagreed`. The demo's strongest sentence is "the model said a
//     person was present at 0.91 and a named reviewer looked at the clip and said
//     why", and a reviewer who always agrees demonstrates nothing.
//   - `outcome: benign`, NOT `dismissed`. `dismissed` and `escalated` require a
//     step-up MFA verification (`incidents.stepUpRequired`), and the seed has no
//     session to present one from. Choosing an outcome the service will accept
//     without a step-up is the honest move: the demo of step-up belongs to the
//     judge, who has a live session, and a seed that forged a `StepUpVerified`
//     would be asserting a second factor that was never presented.
//
// The disposition's own id is `disp_<time.Now().UnixNano()>` — `Record` has no id
// seam — so it is the one value in a seeded site that differs between two runs.
// That is a real observation about the product and is recorded as one rather than
// worked around by writing the row directly behind the service's back.
func (s *seeder) writeDisposition(ctx context.Context) error {
	list, err := s.incidentList(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return errf("security-seed: there is no incident to disposition")
	}
	first := list[0]
	note := "Reviewed the clip: a delivery rider with a hand truck, cleared by the guard. " +
		"The model was right that a person was present and wrong about why."

	id, err := s.incidentService().Record(ctx, incidents.Disposition{
		TenantID:   s.opts.tenant,
		IncidentID: first.incidentID,
		Outcome:    "benign",
		ReasonCode: "authorised_delivery",
		Notes:      note,
		Agreement:  "disagreed",
		ActorID:    seededDispositionActor,
		ActorRoles: []string{"operator"},
		SiteScope:  []string{s.opts.site},
	})
	if err != nil {
		return fmt.Errorf("record the disposition on %s: %w", first.incidentID, err)
	}
	s.say("%s dispositioned by %s: benign, agreement DISAGREED with the observation. "+
		"The reviewer note is what a text search over `dispositions.notes` will find, "+
		"and that column is matched but not indexed - see migration 0024's header",
		first.incidentID, seededDispositionActor)
	s.report.Dispositions = append(s.report.Dispositions, DispositionRow{
		IncidentID: first.incidentID, DispositionID: id,
		Actor: seededDispositionActor, Outcome: "benign", Agreement: "disagreed",
	})
	return nil
}

// oldestIncident is the incident the evidence is attached to.
func (s *seeder) oldestIncident(ctx context.Context) (string, error) {
	incidents, err := s.incidentList(ctx)
	if err != nil {
		return "", err
	}
	if len(incidents) == 0 {
		return "", nil
	}
	return incidents[0].incidentID, nil
}

// incidentSummary is one incident the seed needs to act on.
type incidentSummary struct {
	incidentID     string
	triggerEventID string
	firstEventAt   time.Time
}

// incidentList reads the seeded incidents in a stable order.
func (s *seeder) incidentList(ctx context.Context) ([]incidentSummary, error) {
	rows, err := s.scoped(ctx).Query(ctx, `
		SELECT i.incident_id,
		       COALESCE((SELECT e.event_id FROM incident_events e
		                  WHERE e.tenant_id = i.tenant_id AND e.incident_id = i.incident_id
		                  ORDER BY (e.role = 'trigger') DESC, e.event_id
		                  LIMIT 1), ''),
		       i.first_event_at
		  FROM incidents i
		 WHERE i.tenant_id = $1 AND i.primary_site_id = $2
		 ORDER BY i.first_event_at, i.incident_id`,
		s.opts.tenant, s.opts.site)
	if err != nil {
		return nil, fmt.Errorf("read the seeded incidents: %w", err)
	}
	defer rows.Close()
	var out []incidentSummary
	for rows.Next() {
		var it incidentSummary
		if err := rows.Scan(&it.incidentID, &it.triggerEventID, &it.firstEventAt); err != nil {
			return nil, fmt.Errorf("read a seeded incident: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

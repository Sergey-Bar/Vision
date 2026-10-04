package main

// The seed's report: what it built, and what a demo or a golden test reads.
//
// # Why a report and not just a database
//
// The claim this repository makes is that every number has a transcript. A
// database is not a transcript — it is a state, and a state cannot be quoted in a
// commit message or compared between two runs. So the seed writes down the ids it
// produced, the hashes it stored, the reasons it degraded, and the instant it
// replayed onto, and `dist/demo/transcript.md` is built from this rather than
// scraped off a console.
//
// # What is deliberately NOT in it
//
// No pass/fail verdict. The seed is not the acceptance report and does not
// pretend to be; `securityctl acceptance` owns the thirteen criteria and reports
// `pass`, `fail` and `not-run` separately. A seed that printed "OK" next to its
// output would be a second, weaker claim about the same product.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// Report is the machine-readable seed artefact.
type Report struct {
	// Epoch is the instant every fixture was rebased onto. It is the first field
	// because a report without it cannot be compared with another report: the same
	// fixtures at a different instant are a different run.
	Epoch string `json:"epoch"`
	// SiteTimezone is the zone schedules were evaluated in. It is here because
	// "after hours" is the difference between an incident and nothing, and it is a
	// function of this value and not of the host.
	SiteTimezone string `json:"siteTimezone"`

	Tenant string `json:"tenant"`
	Site   string `json:"site"`
	Zones  int    `json:"zones"`
	// Devices is how many devices the fixtures' own `Source` fields named and the
	// seed registered. An event naming a device that is not registered arrives
	// unattributed, which is correct behaviour and which would make the seeded
	// site assert nothing.
	Devices int `json:"devices"`

	BundleVersion string `json:"ruleBundleVersion"`
	BundleHash    string `json:"ruleBundleContentHash"`

	SecretKeyPath string     `json:"secretKeyPath"`
	TLS           TLSSummary `json:"consoleTLS"`

	Principals []Principal `json:"principals"`
	// Passphrase and TOTPSecret are printed because they are demo credentials with
	// fixed values. They are in the report rather than on stderr so that a
	// transcript which quotes the report quotes them too — a demo that hides its
	// own credential is a demo nobody can reproduce.
	Passphrase     string `json:"demoPassphrase"`
	TOTPSecret     string `json:"demoTOTPSecret,omitempty"`
	TOTPSecretPath string `json:"demoTOTPSecretNote,omitempty"`

	Inbox          InboxCounts      `json:"inbox"`
	Incidents      []IncidentRow    `json:"incidents"`
	Evidence       []EvidenceRow    `json:"evidence"`
	Observations   []ObservationRow `json:"observations"`
	Dispositions   []DispositionRow `json:"dispositions"`
	BundleProblems []string         `json:"bundleProblems,omitempty"`

	// AlreadySeeded says this report describes a site the seed FOUND rather than
	// one it built. A reader comparing two reports needs to know which of the two
	// shapes they are looking at, and a non-empty incident list does not tell them:
	// a seed that rebuilt the site and a seed that declined to touch it both end up
	// with rows.
	AlreadySeeded bool `json:"alreadySeeded"`

	ConsoleURL string `json:"consoleUrl"`
}

// TLSSummary is the certificate the seed minted.
type TLSSummary struct {
	Cert              string `json:"cert"`
	Key               string `json:"key"`
	FingerprintSHA256 string `json:"fingerprintSha256"`
	// SelfSigned is stated in the artefact rather than left for a reader to
	// discover from a browser warning. It is TRUE, and a report that omitted it
	// would be the one place in this repository that let a reader assume
	// something better than the truth.
	SelfSigned bool `json:"selfSigned"`
}

// Principal is one seeded user, at the scope it was given.
type Principal struct {
	Username string   `json:"username"`
	UserID   string   `json:"userId"`
	Roles    []string `json:"roles"`
	// Sites is nil for the system worker, and that is the point: a principal with
	// no `user_site_assignments` row has no site access at all, which is the safe
	// default rather than a gap in the seed.
	Sites []string `json:"sites,omitempty"`
}

// InboxCounts is invariant 1, measured rather than asserted.
type InboxCounts struct {
	Processed    int `json:"processed"`
	DeadLettered int `json:"deadLettered"`
}

// IncidentRow is one incident the pipeline raised.
type IncidentRow struct {
	IncidentID   string `json:"incidentId"`
	RuleID       string `json:"ruleId"`
	State        string `json:"state"`
	Severity     string `json:"severity"`
	Title        string `json:"title"`
	EventID      string `json:"eventId"`
	FirstEventAt string `json:"firstEventAt"`
	// Verification and Reason come from `verification_results`, which is the
	// deterministic explanation. A demo that shows only the incident row is
	// showing the conclusion without the argument.
	Verification  string `json:"verificationState"`
	Reason        string `json:"reasonCode"`
	AIDegradation string `json:"aiDegradation"`
}

// EvidenceRow is one stored clip and the hash that identifies it.
type EvidenceRow struct {
	Name       string `json:"name"`
	EvidenceID string `json:"evidenceId"`
	Ref        string `json:"storageRef"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"sizeBytes"`
	IncidentID string `json:"incidentId"`
	// DetectorZones is the POC's frame geometry for this clip, from the sibling
	// `.zones.json`. It is NOT the product's `zones` tree and is named as what it
	// is; see the note on `clip.DetectorZones`.
	DetectorZones []string `json:"detectorZones,omitempty"`
}

// ObservationRow is one AI observation and whether it degraded.
type ObservationRow struct {
	IncidentID    string `json:"incidentId"`
	ObservationID string `json:"observationId"`
	Degraded      bool   `json:"degraded"`
	Reason        string `json:"degradedReason"`
}

// DispositionRow is a reviewer's answer.
type DispositionRow struct {
	IncidentID    string `json:"incidentId"`
	DispositionID string `json:"dispositionId"`
	Actor         string `json:"actor"`
	Outcome       string `json:"outcome"`
	Agreement     string `json:"agreement"`
}

// print writes the human-readable form, or the JSON.
//
// The two are exclusive. `--json` exists so a demo script can read the ids, and a
// report that printed both would be two reports of which one is the record.
func (s *seeder) print() {
	if s.opts.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(s.report); err != nil {
			fmt.Fprintf(os.Stderr, "security-seed: encode the report: %v\n", err)
		}
		return
	}

	w := s.out
	heading := "SEED COMPLETE"
	if s.report.AlreadySeeded {
		heading = "ALREADY SEEDED — this run wrote nothing"
	}
	fmt.Fprintf(w, "\n%s. tenant %s, site %s (%s)\n", heading,
		s.report.Tenant, s.report.Site, s.report.SiteTimezone)
	fmt.Fprintf(w, "replayed onto %s; rule bundle %s (%s)\n",
		s.report.Epoch, s.report.BundleVersion, shortHash(s.report.BundleHash))
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "INCIDENTS (%d) -- raised by the product's own pipeline, not inserted\n",
		len(s.report.Incidents))
	if len(s.report.Incidents) == 0 {
		fmt.Fprintf(w, "  none. The console will be empty, which is a fact about the bundle\n")
		fmt.Fprintf(w, "  and the fixtures rather than about the core.\n")
	}
	for _, i := range s.report.Incidents {
		fmt.Fprintf(w, "  %s  %-11s %-8s %s\n", i.IncidentID, i.Verification, i.Severity, i.Title)
		fmt.Fprintf(w, "      rule %s, event %s at %s\n", i.RuleID, i.EventID, i.FirstEventAt)
		fmt.Fprintf(w, "      ai_degradation %s, reason %s\n", i.AIDegradation, i.Reason)
	}
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "AI OBSERVATIONS (%d)\n", len(s.report.Observations))
	healthy, degraded := 0, 0
	for _, o := range s.report.Observations {
		if o.Degraded {
			degraded++
		} else {
			healthy++
		}
	}
	fmt.Fprintf(w, "  %d healthy, %d degraded. A degraded observation is NOT a negative one:\n", healthy, degraded)
	fmt.Fprintf(w, "  the deterministic decision stands and the console says why the model was not there.\n")
	for _, o := range s.report.Observations {
		if o.Degraded {
			fmt.Fprintf(w, "  %s  %s  DEGRADED %s\n", o.IncidentID, o.ObservationID, o.Reason)
		}
	}
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "EVIDENCE (%d) -- stored by content hash, verified on read\n", len(s.report.Evidence))
	for _, e := range s.report.Evidence {
		fmt.Fprintf(w, "  %-22s %s  sha256 %s  %d bytes  on %s\n",
			e.Name, e.Ref, e.SHA256, e.SizeBytes, e.IncidentID)
	}
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "REVIEW\n")
	for _, d := range s.report.Dispositions {
		fmt.Fprintf(w, "  %s  outcome %s by %s, agreement %s with the observation\n",
			d.IncidentID, d.Outcome, d.Actor, d.Agreement)
	}
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "SIGN IN AT %s\n", s.report.ConsoleURL)
	fmt.Fprintf(w, "  the certificate is SELF-SIGNED and was minted by this run. SHA-256:\n")
	fmt.Fprintf(w, "  %s\n", s.report.TLS.FingerprintSHA256)
	for _, p := range s.report.Principals {
		sites := "no site assignment"
		if len(p.Sites) > 0 {
			sites = strings.Join(p.Sites, ", ")
		}
		fmt.Fprintf(w, "  %-9s roles %-14s %s\n", p.Username, strings.Join(p.Roles, ","), sites)
	}
	if s.report.TOTPSecret != "" {
		fmt.Fprintf(w, "\n  second factor, required by the console:\n")
		fmt.Fprintf(w, "    secret  %s\n", s.report.TOTPSecret)
		fmt.Fprintf(w, "    code    securityctl auth code --secret %s\n", s.report.TOTPSecret)
		fmt.Fprintf(w, "    (a six-digit code, RFC 6238 SHA-1, valid for 30 seconds. Any\n")
		fmt.Fprintf(w, "     authenticator app computes the same code from the same secret.)\n")
	} else {
		fmt.Fprintf(w, "\n  second factor: already enrolled on a previous run. Its secret is not\n")
		fmt.Fprintf(w, "  recoverable and is not printed again — a wrapped factor that could be\n")
		fmt.Fprintf(w, "  read back would not be wrapped.\n")
	}
	fmt.Fprintf(w, "  passphrase: %s (a demo credential with a fixed value, printed on purpose)\n",
		s.report.Passphrase)
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "What this did NOT do: there is no camera, no VMS and no model behind these rows.\n")
	fmt.Fprintf(w, "Every AI observation above is either explicitly degraded or labelled `seeded/absent`,\n")
	fmt.Fprintf(w, "and `securityctl acceptance` reports which criteria that leaves unevidenced.\n")
}

// collect reads the seeded rows back and fills the report's incident list.
//
// Read back rather than accumulated in memory, because the ids were chosen by the
// product: the seed injects an id GENERATOR and the incident rows are whatever the
// pipeline actually raised. A report built from what the seed intended to create
// would be a report about the seed.
func (s *seeder) collect(ctx context.Context) error {
	s.report.SiteTimezone = s.opts.timezone
	s.report.Passphrase = demoPassphrase
	if s.opts.passphrase != "" {
		s.report.Passphrase = s.opts.passphrase
	}
	s.report.ConsoleURL = consoleURL

	rows, err := s.scoped(ctx).Query(ctx, `
		SELECT i.incident_id, i.rule_id, i.state, i.severity, i.title, i.degraded,
		       i.first_event_at,
		       COALESCE((SELECT e.event_id FROM incident_events e
		                  WHERE e.tenant_id = i.tenant_id AND e.incident_id = i.incident_id
		                  ORDER BY (e.role = 'trigger') DESC, e.event_id LIMIT 1), ''),
		       COALESCE((SELECT v.state FROM verification_results v
		                  WHERE v.tenant_id = i.tenant_id AND v.incident_id = i.incident_id
		                  ORDER BY v.evaluated_at DESC LIMIT 1), ''),
		       COALESCE((SELECT v.reason_code FROM verification_results v
		                  WHERE v.tenant_id = i.tenant_id AND v.incident_id = i.incident_id
		                  ORDER BY v.evaluated_at DESC LIMIT 1), ''),
		       COALESCE((SELECT v.ai_degradation FROM verification_results v
		                  WHERE v.tenant_id = i.tenant_id AND v.incident_id = i.incident_id
		                  ORDER BY v.evaluated_at DESC LIMIT 1), '')
		  FROM incidents i
		 WHERE i.tenant_id = $1 AND i.primary_site_id = $2
		 ORDER BY i.first_event_at, i.incident_id`, s.opts.tenant, s.opts.site)
	if err != nil {
		return fmt.Errorf("read the seeded incidents back: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			r        IncidentRow
			degraded bool
			firstAt  time.Time
		)
		if err := rows.Scan(&r.IncidentID, &r.RuleID, &r.State, &r.Severity, &r.Title,
			&degraded, &firstAt, &r.EventID, &r.Verification, &r.Reason, &r.AIDegradation); err != nil {
			return fmt.Errorf("read a seeded incident back: %w", err)
		}
		// Rendered in the site's own offset, never the host's, and with the offset
		// spelled out. `AGENTS.md` is explicit about this and dropping the zone
		// would make every time in the report ambiguous.
		r.FirstEventAt = firstAt.In(s.loc).Format(time.RFC3339)
		if degraded {
			r.AIDegradation = firstNonEmpty(r.AIDegradation, "degraded")
		}
		s.report.Incidents = append(s.report.Incidents, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	sort.SliceStable(s.report.Incidents, func(i, j int) bool {
		return s.report.Incidents[i].IncidentID < s.report.Incidents[j].IncidentID
	})
	return nil
}

// firstNonEmpty keeps a real value in preference to a placeholder, and never
// replaces one with the other silently.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" && v != "none" {
			return v
		}
	}
	return "none"
}

// consoleURL is what the compose file publishes.
//
// Spelled here rather than composed from a flag so the report and the compose
// service cannot drift into two different answers about where the console is.
const consoleURL = "https://localhost:8443/"

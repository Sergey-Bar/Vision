package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/Ai-Captain/internal/audit"
	"github.com/Sergey-Bar/Ai-Captain/internal/bundles"
	"github.com/Sergey-Bar/Ai-Captain/internal/checkpoint"
	"github.com/Sergey-Bar/Ai-Captain/internal/command"
	"github.com/Sergey-Bar/Ai-Captain/internal/incidents"
	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
	"github.com/Sergey-Bar/Ai-Captain/internal/simulate"
)

// The operator surface. Every capability below already existed as a tested
// service; what was missing was the command that reaches it, which meant the
// plan's items 23 and 24 were described as done when only the library half was.

func incidentCommand() *command.Command {
	inc := &command.Command{
		Name:    "incident",
		Summary: "Inspect incidents and why they exist",
		Usage: `incident — the operator's view of a decision.

Every answer comes from stored facts and the stored AIObservation. No command in
this tree re-runs a model to explain a past decision, and one that tried to would
be a bug rather than a feature.`,
	}
	inc.Register(
		simpleFlag("list", "List incidents visible to the session", runIncidentList),
		simpleFlag("show", "Show an incident with its full provenance", runIncidentShow),
		simpleFlag("explain", "Answer why this incident existed", runIncidentExplain),
		simpleFlag("replay", "Reproduce a past decision from stored facts", runIncidentReplay),
		simpleFlag("disposition", "Record an operator disposition", runIncidentDisposition),
	)
	return inc
}

func simpleFlag(name, summary string, run func(context.Context, *command.Env, []string) error) *command.Command {
	return &command.Command{Name: name, Summary: summary, Run: run}
}

func newIncidentService() (*incidents.Service, error) {
	pool, err := openPool(context.Background())
	if err != nil {
		return nil, err
	}
	return &incidents.Service{Pool: pool, Audit: &audit.Writer{Pool: pool}}, nil
}

func jsonFlag(fs *flag.FlagSet, target *bool) {
	fs.BoolVar(target, "json", false, "Emit machine-readable output.")
}

func runIncidentList(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("incident list", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	site := fs.String("site", "", "Restrict to one site. Must be one the session is assigned to.")
	state := fs.String("state", "", "open, in_review, dispositioned or closed.")
	degraded := fs.String("degraded", "", "true lists only incidents raised without a live AI observation.")
	limit := fs.Int("limit", 50, "Maximum rows.")
	var asJSON bool
	jsonFlag(fs, &asJSON)
	if err := fs.Parse(args); err != nil {
		return err
	}

	svc, err := newIncidentService()
	if err != nil {
		return err
	}
	defer svc.Pool.Close()

	tenant := configuredTenant()
	items, err := svc.List(ctx, tenant, *site, *limit)
	if err != nil {
		return err
	}
	filtered := make([]incidents.Summary, 0, len(items))
	for _, item := range items {
		if *state != "" && item.State != *state {
			continue
		}
		if *degraded == "true" && !item.Degraded {
			continue
		}
		filtered = append(filtered, item)
	}

	if asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"items": filtered})
	}
	if len(filtered) == 0 {
		fmt.Fprintln(env.Stdout, "no incidents match")
		return nil
	}
	fmt.Fprintf(env.Stdout, "%-34s %-10s %-9s %-8s %-18s %s\n",
		"INCIDENT", "STATE", "SEVERITY", "RULE", "FIRST EVENT", "AI")
	for _, item := range filtered {
		ai := "-"
		if item.Degraded {
			ai = "degraded:" + strings.Join(item.DegradedReasons, ",")
		}
		fmt.Fprintf(env.Stdout, "%-34s %-10s %-9s %-8s %-18s %s\n",
			item.IncidentID, item.State, item.Severity, shorten(item.RuleID, 8),
			item.FirstEvent.UTC().Format("2006-01-02T15:04:05Z"), ai)
	}
	return nil
}

func runIncidentShow(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("incident show", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	var asJSON bool
	jsonFlag(fs, &asJSON)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &command.UsageError{Err: errors.New("usage: incident show <incident-id>")}
	}

	svc, err := newIncidentService()
	if err != nil {
		return err
	}
	defer svc.Pool.Close()

	d, err := svc.Get(ctx, configuredTenant(), fs.Arg(0))
	if err != nil {
		if errors.Is(err, incidents.ErrNotFound) {
			return command.Refuse("no such incident, or you are not assigned to its site")
		}
		return err
	}
	if asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(d)
	}

	fmt.Fprintf(env.Stdout, "%s  [%s / %s]\n", d.Title, d.State, d.Severity)
	fmt.Fprintf(env.Stdout, "  incident   : %s\n", d.IncidentID)
	fmt.Fprintf(env.Stdout, "  site       : %s\n", d.SiteID)
	fmt.Fprintf(env.Stdout, "  rule       : %s   bundle %s (%s)   policy %s\n",
		d.RuleID, d.BundleVersion, d.BundleVersion[:min(12, len(d.BundleVersion))], d.PolicyVersion)
	fmt.Fprintf(env.Stdout, "  created    : %s   first event %s\n",
		d.CreatedAt.UTC().Format(time.RFC3339), d.FirstEvent.UTC().Format(time.RFC3339))
	if d.Degraded {
		fmt.Fprintf(env.Stdout, "  AI         : DEGRADED (%s) — this incident is not fully corroborated\n",
			strings.Join(d.DegradedReasons, ", "))
	}
	fmt.Fprintf(env.Stdout, "  verified   : %s (%s)\n", d.Verification.State, d.Verification.ReasonCode)
	fmt.Fprintf(env.Stdout, "  why        : %s\n", d.Verification.Explanation)

	if len(d.Observations) > 0 {
		fmt.Fprintln(env.Stdout, "\n  AI observations")
		for _, o := range d.Observations {
			kind := "shadow"
			if o.ProductionProfile {
				kind = "production"
			}
			fmt.Fprintf(env.Stdout, "    %s  %s [%s]  model %s (%s)  prompt %s v%s  %dms  out %s\n",
				o.ObservationID, o.ProfileID, kind, o.Model, shortHash(o.ModelHash),
				o.PromptTemplateID, o.PromptVersion, o.LatencyMS, shortHash(o.OutputHash))
		}
	} else {
		fmt.Fprintln(env.Stdout, "\n  no AI observation participated in this decision")
	}

	if len(d.Events) > 0 {
		fmt.Fprintln(env.Stdout, "\n  events")
		for _, e := range d.Events {
			fmt.Fprintf(env.Stdout, "    %-26s %-22s obs %s  rcv %s  norm %s  clock %s(%dms)\n",
				e.EventID, e.EventType,
				e.ObservedAt.UTC().Format("15:04:05.000"),
				e.ReceivedAt.UTC().Format("15:04:05.000"),
				e.NormalizedAt.UTC().Format("15:04:05.000"),
				e.ClockSource, e.ClockDriftMS)
		}
	}

	if len(d.Verification.DriftExclusions) > 0 {
		fmt.Fprintln(env.Stdout, "\n  excluded from corroboration")
		for _, x := range d.Verification.DriftExclusions {
			fmt.Fprintf(env.Stdout, "    %s — %s\n", x.DeviceID, x.Reason)
		}
	}
	if len(d.Dispositions) > 0 {
		fmt.Fprintln(env.Stdout, "\n  operator dispositions")
		for _, x := range d.Dispositions {
			fmt.Fprintf(env.Stdout, "    %s  %s  agreement=%s  %s  %s\n",
				x.CreatedAt.UTC().Format(time.RFC3339), x.Outcome, x.Agreement, x.ReasonCode, x.Notes)
		}
	}
	return nil
}

func runIncidentExplain(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("incident explain", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	var asJSON bool
	jsonFlag(fs, &asJSON)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &command.UsageError{Err: errors.New("usage: incident explain <incident-id>")}
	}

	svc, err := newIncidentService()
	if err != nil {
		return err
	}
	defer svc.Pool.Close()

	tenant := configuredTenant()
	bundle, err := activeBundleFor(ctx, tenant)
	if err != nil {
		return command.Refuse("cannot replay without the active bundle: %v", err)
	}
	ex, err := svc.Replay(ctx, incidents.ReplayInputs{
		TenantID: tenant, IncidentID: fs.Arg(0), Bundle: bundle,
	})
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(ex)
	}

	fmt.Fprintf(env.Stdout, "%s — %s under rule %s (bundle %s, policy %s)\n",
		ex.IncidentID, ex.State, ex.RuleID, ex.BundleVersion, ex.PolicyVersion)
	if ex.ObservationID != "" {
		fmt.Fprintf(env.Stdout, "stored observation %s: model %s (%s), prompt %s v%s, output %s\n",
			ex.ObservationID, ex.Model, shortHash(ex.ModelHash),
			ex.PromptTemplateID, ex.PromptVersion, shortHash(ex.BundleContentHash))
	} else {
		fmt.Fprintln(env.Stdout, "no AI observation participated")
	}
	fmt.Fprintln(env.Stdout)
	for _, step := range ex.Steps {
		fmt.Fprintf(env.Stdout, "%d. [%s] %s\n", step.Order, step.Kind, step.Description)
		for _, e := range step.DriftExclusions {
			fmt.Fprintf(env.Stdout, "     excluded %s — %s\n", e.DeviceID, e.Reason)
		}
	}
	fmt.Fprintf(env.Stdout, "\nreplayed from stored facts: %v\n", ex.ReplayedFromStoredFacts)
	if ex.Matches {
		fmt.Fprintln(env.Stdout, "re-evaluating the stored facts reproduces the recorded outcome")
	} else {
		fmt.Fprintf(env.Stdout, "MISMATCH: %s\n", ex.Mismatch)
	}
	return nil
}

// replay exists as a distinct command from explain because the difference is
// what the operator is asking. explain shows the reasoning; replay additionally
// re-evaluates the stored facts and reports whether they still produce the
// recorded outcome, which is how a bundle change or a corrupted row is found.
func runIncidentReplay(ctx context.Context, env *command.Env, args []string) error {
	if err := runIncidentExplain(ctx, env, args); err != nil {
		return err
	}
	fmt.Fprintln(env.Stdout,
		"\nNothing above re-ran the model. The observation shown is the one stored when "+
			"the decision was made.")
	return nil
}

func runIncidentDisposition(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("incident disposition", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	outcome := fs.String("outcome", "", "true_positive, false_positive, benign, escalated, acknowledged or dismissed.")
	reason := fs.String("reason", "", "Short reason code.")
	notes := fs.String("notes", "", "Free-text notes.")
	agreement := fs.String("agreement", "unsure",
		"no_ai_observation, agreed, disagreed or unsure. Feeds the evaluation harness.")
	actor := fs.String("actor", "", "Operator recording this. Defaults to SP_OPERATOR.")
	stepUp := fs.String("step-up", "", "Step-up proof, required for escalated and dismissed.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &command.UsageError{Err: errors.New("usage: incident disposition <incident-id> --outcome <o>")}
	}
	if *outcome == "" {
		return &command.UsageError{Err: errors.New("--outcome is required")}
	}

	svc, err := newIncidentService()
	if err != nil {
		return err
	}
	defer svc.Pool.Close()

	tenant := configuredTenant()
	operator := *actor
	if operator == "" {
		operator = os.Getenv("SP_OPERATOR")
	}
	if operator == "" {
		return command.Refuse("no operator is identified. Pass --actor or set SP_OPERATOR: a " +
			"disposition with no actor is a disposition nobody can be shown to have made.")
	}

	// The step-up proof belongs to the identity service and is spent there, so a
	// replayed proof cannot escalate a second incident.
	if *stepUp != "" {
		ident, err := newIdentityService()
		if err != nil {
			return err
		}
		defer ident.Pool.Close()
		if err := ident.ConsumeStepUp(ctx, tenant, operator, *stepUp); err != nil {
			return command.Refuse("%v", err)
		}
	}

	if _, err := svc.Record(ctx, incidents.Disposition{
		TenantID: tenant, IncidentID: fs.Arg(0), Outcome: *outcome,
		ReasonCode: *reason, Notes: *notes, Agreement: *agreement,
		ActorID: operator, SiteScope: []string{configuredSite()},
		StepUpVerified: *stepUp != "",
	}); err != nil {
		if strings.Contains(err.Error(), "step-up") {
			return command.Refuse("%v", err)
		}
		return err
	}
	fmt.Fprintf(env.Stdout, "recorded %s on %s as %s\n", *outcome, fs.Arg(0), operator)
	fmt.Fprintln(env.Stdout,
		"The AI observation is untouched. \"The model said X\" and \"the operator said Y\" "+
			"are both stored and both replay.")
	return nil
}

// ---------------------------------------------------------------------------
// audit
// ---------------------------------------------------------------------------

func auditCommand() *command.Command {
	aud := &command.Command{
		Name:    "audit",
		Summary: "Verify and seal the audit ledger",
		Usage: `audit — the ledger is separate from the operational logs.

Logs are diagnostic and can be lost. The ledger is authoritative, hash-chained,
and sealed to storage the database owner cannot reach.`,
	}
	aud.Register(
		simpleFlag("verify", "Recompute the hash chain and report the first break", runAuditVerify),
		simpleFlag("seal", "Seal the verified range through the checkpoint sink", runAuditSeal),
		simpleFlag("checkpoints", "List sealed checkpoints", runAuditCheckpoints),
		simpleFlag("verify-seals", "Verify the offline seal set with no database present", runAuditVerifySeals),
	)
	return aud
}

func runAuditVerify(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("audit verify", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	from := fs.Int64("from", 1, "First sequence to verify.")
	var asJSON bool
	jsonFlag(fs, &asJSON)
	if err := fs.Parse(args); err != nil {
		return err
	}

	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	writer := &audit.Writer{Pool: pool}
	var v audit.Verification
	err = pool.InSystemTenant(ctx, func(tx pgx.Tx) error {
		var e error
		v, e = writer.Verify(ctx, tx, configuredTenant(), *from)
		return e
	})
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	if v.Valid {
		fmt.Fprintf(env.Stdout, "chain valid: %d row(s) from sequence %d through %d\n",
			v.Checked, v.FromSequence, v.ToSequence)
		return nil
	}
	fmt.Fprintf(env.Stdout, "CHAIN BROKEN at sequence %d, after %d verified row(s)\n",
		v.BrokenAt, v.Checked)
	fmt.Fprintln(env.Stdout,
		"Do not repair the rows. Recompute from the last good seal and compare; a repair "+
			"destroys the only evidence that something was wrong.")
	return command.Refuse("the audit chain does not verify")
}

func runAuditSeal(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("audit seal", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	from := fs.Int64("from", 1, "First sequence to seal.")
	keyFile := fs.String("signing-key", os.Getenv("SP_CHECKPOINT_KEY"), "Ed25519 signing key file.")
	keyID := fs.String("key-id", "checkpoint-1", "Signing key identifier.")
	sinkDir := fs.String("sink", os.Getenv("SP_CHECKPOINT_DIR"), "Checkpoint sink directory.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyFile == "" || *sinkDir == "" {
		return command.Refuse(
			"a seal needs SP_CHECKPOINT_KEY and SP_CHECKPOINT_DIR. The sink must not be " +
				"writable by anyone who can reach the database, or a seal proves nothing.")
	}

	signer, trusted, err := loadOrCreateKey(*keyFile)
	if err != nil {
		return err
	}
	defer zeroKey(signer)

	sink, err := checkpoint.NewFileSink(*sinkDir)
	if err != nil {
		return err
	}
	checkpointSigner, err := checkpoint.NewSignerFromKey(*keyID, signer)
	if err != nil {
		return err
	}
	sealer := &checkpoint.Sealer{
		Sink: sink, Signer: checkpointSigner, Trusted: trusted,
	}

	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	seal, err := sealer.SealRange(ctx, tx, configuredTenant(), *from, 0, time.Now().UTC())
	if err != nil {
		return command.Refuse("%v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_checkpoints (checkpoint_id, tenant_id, sealed_at, from_sequence,
		                               to_sequence, entry_count, chain_digest, algorithm,
		                               signature, public_key, key_id, sink_ref)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (tenant_id, checkpoint_id) DO NOTHING`,
		seal.CheckpointID, seal.TenantID, seal.SealedAt, seal.FromSequence, seal.ToSequence,
		seal.EntryCount, seal.ChainDigest, seal.Algorithm, seal.Signature, seal.PublicKey,
		seal.KeyID, "file:"+sink.Root); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "sealed sequences %d..%d (%d entries)\n",
		seal.FromSequence, seal.ToSequence, seal.EntryCount)
	fmt.Fprintf(env.Stdout, "  digest   : %s\n", seal.ChainDigest)
	fmt.Fprintf(env.Stdout, "  key      : %s\n", seal.KeyID)
	fmt.Fprintf(env.Stdout, "  sink     : %s\n", sink.Root)
	fmt.Fprintf(env.Stdout, "  verify   : securityctl audit verify-seals --sink %s --trusted-key %s\n",
		*sinkDir, shortHash(seal.PublicKey))
	return nil
}

func runAuditCheckpoints(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("audit checkpoints", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	var asJSON bool
	jsonFlag(fs, &asJSON)
	if err := fs.Parse(args); err != nil {
		return err
	}

	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `
		SELECT checkpoint_id, sealed_at, from_sequence, to_sequence, entry_count,
		       COALESCE(chain_digest, ''), key_id, COALESCE(sink_ref, '')
		  FROM audit_checkpoints WHERE tenant_id = $1 ORDER BY sealed_at DESC`,
		configuredTenant())
	if err != nil {
		return err
	}
	defer rows.Close()

	type entry struct {
		CheckpointID string    `json:"checkpointId"`
		SealedAt     time.Time `json:"sealedAt"`
		From         int64     `json:"fromSequence"`
		To           int64     `json:"toSequence"`
		Entries      int64     `json:"entryCount"`
		Digest       string    `json:"chainDigest"`
		KeyID        string    `json:"keyId"`
		SinkRef      string    `json:"sinkRef"`
	}
	var items []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.CheckpointID, &e.SealedAt, &e.From, &e.To, &e.Entries,
			&e.Digest, &e.KeyID, &e.SinkRef); err != nil {
			return err
		}
		items = append(items, e)
	}
	if asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"items": items})
	}
	if len(items) == 0 {
		fmt.Fprintln(env.Stdout, "no checkpoints sealed for this tenant")
		return nil
	}
	fmt.Fprintf(env.Stdout, "%-28s %-8s %-8s %-7s %-16s %s\n",
		"CHECKPOINT", "FROM", "TO", "ENTRIES", "SEALED", "KEY")
	for _, e := range items {
		fmt.Fprintf(env.Stdout, "%-28s %-8d %-8d %-7d %-16s %s\n",
			shorten(e.CheckpointID, 28), e.From, e.To, e.Entries,
			e.SealedAt.UTC().Format("2006-01-02T15:04"), e.KeyID)
	}
	return nil
}

// verify-seals is the air-gapped audit path: no database, just the seal
// directory and a trusted key.
func runAuditVerifySeals(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("audit verify-seals", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	sinkDir := fs.String("sink", os.Getenv("SP_CHECKPOINT_DIR"), "Checkpoint sink directory.")
	trustedHex := fs.String("trusted-key", "", "Trusted Ed25519 public key, hex.")
	latest := fs.Int64("latest", 0, "Highest sequence the database holds, if known.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sinkDir == "" {
		return command.Refuse("--sink is required")
	}

	sink, err := checkpoint.NewFileSink(*sinkDir)
	if err != nil {
		return err
	}
	seals, err := sink.List()
	if err != nil {
		return err
	}
	if len(seals) == 0 {
		return command.Refuse("no seals found in %s", *sinkDir)
	}

	var trusted []byte
	if *trustedHex != "" {
		trusted, err = decodePublicKey(*trustedHex)
		if err != nil {
			return err
		}
	} else {
		fmt.Fprintln(env.Stderr,
			"warning: no --trusted-key. Each seal is checked against the key it carries, "+
				"which proves only that whoever wrote the seal signed it.")
	}

	var pub ed25519PublicKey
	if trusted != nil {
		pub = trusted
	}
	next, err := checkpoint.VerifyAgainstSeals(seals, *latest, pub)
	if err != nil {
		return command.Refuse("%v", err)
	}
	for _, s := range seals {
		fmt.Fprintf(env.Stdout, "verified %s  sequences %d..%d  %d entries  key %s\n",
			shorten(s.CheckpointID, 28), s.FromSequence, s.ToSequence, s.EntryCount, s.KeyID)
	}
	fmt.Fprintf(env.Stdout, "\ncontiguous through sequence %d; the next unsealed sequence is %d\n",
		next-1, next)
	if *latest > 0 && next > *latest {
		fmt.Fprintln(env.Stderr,
			"warning: the seals reach further than the database you pointed this at. Either "+
				"the database is behind, or these seals are from a different site.")
	}
	return nil
}

// ---------------------------------------------------------------------------
// identity
// ---------------------------------------------------------------------------

func identityCommand() *command.Command {
	id := &command.Command{
		Name:    "identity",
		Summary: "Administrator identity and recovery",
		Usage: `identity — MFA is mandatory and there is no default password.

bootstrap-admin takes a passphrase from SP_PASSPHRASE or stdin, never as an
argument: a passphrase on a command line is in the shell history.`,
	}
	id.Register(
		simpleFlag("bootstrap-admin", "Create the first administrator", runBootstrapAdmin),
		simpleFlag("enroll-mfa", "Enrol a WebAuthn or TOTP factor for a user", runEnrollMFA),
		simpleFlag("list", "List users, roles and site assignments", runIdentityList),
		simpleFlag("revoke-sessions", "Revoke every session for a user", runRevokeSessions),
	)
	return id
}

func runBootstrapAdmin(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("identity bootstrap-admin", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	username := fs.String("username", "", "Username.")
	site := fs.String("site", configuredSite(), "Site to assign the administrator to.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *username == "" {
		return &command.UsageError{Err: errors.New("--username is required")}
	}

	passphrase := os.Getenv("SP_PASSPHRASE")
	source := "SP_PASSPHRASE"
	if passphrase == "" {
		fmt.Fprint(env.Stderr, "Passphrase (not echoed, at least 16 characters): ")
		line, err := readLine(env)
		if err != nil || strings.TrimSpace(line) == "" {
			return command.Refuse("no passphrase was supplied")
		}
		passphrase = strings.TrimSpace(line)
		source = "stdin"
	}
	if len([]rune(passphrase)) < 16 {
		return command.Refuse("the passphrase must be at least 16 characters")
	}

	ident, err := newIdentityService()
	if err != nil {
		return err
	}
	defer ident.Pool.Close()

	userID, err := ident.BootstrapAdmin(ctx, configuredTenant(), *username, passphrase, *site)
	if err != nil {
		return command.Refuse("%v", err)
	}
	fmt.Fprintf(env.Stdout, "created administrator %s (%s), assigned to %s\n",
		*username, userID, *site)
	fmt.Fprintf(env.Stdout, "passphrase read from %s and is not recoverable afterwards.\n", source)
	fmt.Fprintln(env.Stderr,
		"\nThis account has no enrolled second factor, which is the one state that can "+
			"sign in on a password alone. Enrol one now, before anything else: the login "+
			"that used it is audited at the highest volume precisely so a site that "+
			"leaves it open is visible.")
	fmt.Fprintf(env.Stderr, "  securityctl identity enroll-mfa --user %s --method totp\n", userID)
	return nil
}

func runEnrollMFA(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("identity enroll-mfa", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	user := fs.String("user", "", "User id.")
	method := fs.String("method", "totp", "totp or webauthn.")
	label := fs.String("label", "", "Credential label.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *user == "" {
		return &command.UsageError{Err: errors.New("--user is required")}
	}
	if *method != "totp" {
		return command.Refuse(
			"%q enrolment is not implemented in this build. The schema carries WebAuthn and "+
				"the service verifies an assertion's counter, but there is no registration "+
				"ceremony: enrolment that cannot be completed is not an enrolment.", *method)
	}
	if *label == "" {
		*label = *user
	}

	ident, err := newIdentityService()
	if err != nil {
		return err
	}
	defer ident.Pool.Close()

	secret, uri, err := ident.EnrollTOTP(ctx, configuredTenant(), *user, *label)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "secret (shown once): %s\n", secret)
	fmt.Fprintf(env.Stdout, "add to an authenticator: %s\n", uri)
	fmt.Fprintln(env.Stdout,
		"\nNow confirm the enrolment, or the credential is stored but unusable:\n"+
			"  securityctl identity enroll-mfa --user "+*user+" --method totp --confirm <code>")
	return nil
}

func runIdentityList(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("identity list", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	var asJSON bool
	jsonFlag(fs, &asJSON)
	if err := fs.Parse(args); err != nil {
		return err
	}

	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Roles and sites are aggregated in correlated subqueries rather than with a
	// GROUP BY. Grouping means either listing every selected user column in the
	// clause — which breaks the moment somebody adds a field to the output — or
	// quietly folding columns Postgres decides are functionally dependent.
	rows, err := pool.Query(ctx, `
		SELECT u.user_id, u.username, u.display_name, u.enabled, u.break_glass,
		       u.recovery_required, u.failed_attempts,
		       COALESCE((SELECT array_agg(ur.role_id ORDER BY ur.role_id)
		                   FROM user_roles ur
		                  WHERE ur.tenant_id = u.tenant_id AND ur.user_id = u.user_id), '{}'),
		       COALESCE((SELECT array_agg(a.site_id ORDER BY a.site_id)
		                   FROM user_site_assignments a
		                  WHERE a.tenant_id = u.tenant_id AND a.user_id = u.user_id), '{}'),
		       (SELECT count(*) FROM mfa_credentials m
		         WHERE m.tenant_id = u.tenant_id AND m.user_id = u.user_id AND m.revoked_at IS NULL)
		  FROM users u
		 WHERE u.tenant_id = $1
		 ORDER BY u.username`, configuredTenant())
	if err != nil {
		return err
	}
	defer rows.Close()

	type entry struct {
		UserID           string   `json:"userId"`
		Username         string   `json:"username"`
		DisplayName      string   `json:"displayName"`
		Enabled          bool     `json:"enabled"`
		BreakGlass       bool     `json:"breakGlass"`
		RecoveryRequired bool     `json:"recoveryRequired"`
		FailedAttempts   int      `json:"failedAttempts"`
		Roles            []string `json:"roles"`
		Sites            []string `json:"sites"`
		Factors          int64    `json:"enrolledFactors"`
	}
	var items []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.UserID, &e.Username, &e.DisplayName, &e.Enabled, &e.BreakGlass,
			&e.RecoveryRequired, &e.FailedAttempts, &e.Roles, &e.Sites, &e.Factors); err != nil {
			return err
		}
		items = append(items, e)
	}
	if asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"users": items})
	}
	if len(items) == 0 {
		fmt.Fprintf(env.Stdout, "no users for tenant %s\n", configuredTenant())
		return nil
	}
	fmt.Fprintf(env.Stdout, "%-20s %-22s %-9s %-6s %-19s %-22s %s\n",
		"USERNAME", "ROLES", "ENABLED", "MFA", "STATE", "SITES", "FAILED")
	for _, e := range items {
		mfa := "none"
		if e.Factors > 0 {
			mfa = fmt.Sprintf("%d", e.Factors)
		}
		// Two flags that look alarming and mean different things. Recovery-required
		// is the state after a bootstrap or a password reset: the placeholder has
		// not been replaced yet. Break-glass is an account that should not exist
		// on most sites and whose every use is audited.
		state := ""
		if e.BreakGlass {
			state = "BREAK-GLASS"
		} else if e.RecoveryRequired {
			state = "password-chosen"
		}
		fmt.Fprintf(env.Stdout, "%-20s %-22s %-9v %-6s %-19s %-22s %d\n",
			shorten(e.Username, 20), shorten(strings.Join(e.Roles, ","), 22), e.Enabled,
			mfa, state, shorten(strings.Join(e.Sites, ","), 22), e.FailedAttempts)
	}
	return nil
}

func runRevokeSessions(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("identity revoke-sessions", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	user := fs.String("user", "", "User id.")
	reason := fs.String("reason", "revoked by operator", "Recorded on the revocation.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *user == "" {
		return &command.UsageError{Err: errors.New("--user is required")}
	}

	ident, err := newIdentityService()
	if err != nil {
		return err
	}
	defer ident.Pool.Close()

	n, err := ident.RevokeUserSessions(ctx, configuredTenant(), *user, *reason)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "revoked %d session(s) for %s\n", n, *user)
	return nil
}

// ---------------------------------------------------------------------------
// rules explain
// ---------------------------------------------------------------------------

func runRulesExplain(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("rules explain", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	ruleID := fs.String("rule", "", "Rule id to explain.")
	bundlePath := fs.String("bundle", "", "Bundle file. Defaults to the stored active bundle.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var content []byte
	var version string
	if *bundlePath != "" {
		raw, err := os.ReadFile(*bundlePath)
		if err != nil {
			return err
		}
		content = raw
	} else {
		var err error
		version, content, err = activeBundleBytes(ctx, configuredTenant())
		if err != nil {
			return command.Refuse("no active bundle: %v", err)
		}
	}

	compiled, problems := rules.NewCompiler().Compile(content)
	if compiled == nil {
		for _, pr := range problems {
			fmt.Fprintf(env.Stderr, "%s\n", pr)
		}
		return command.Refuse("the bundle does not compile")
	}
	if version == "" {
		version = compiled.Version
	}

	if *ruleID == "" {
		fmt.Fprintf(env.Stdout, "bundle %s, %d rule(s)\n\n", version, len(compiled.Compiled))
		for _, r := range compiled.Compiled {
			state := "enabled"
			if !r.Enabled {
				state = "DISABLED"
			}
			fmt.Fprintf(env.Stdout, "  %-5d %-9s %-46s %s\n",
				r.Priority, state, r.ID, describeAction(r.Rule, r.Severity))
		}
		if len(compiled.Conflicts) > 0 {
			fmt.Fprintln(env.Stdout, "\n  conflicts")
			for _, c := range compiled.Conflicts {
				fmt.Fprintf(env.Stdout, "    %s at priority %d: %s\n",
					strings.Join(c.Rules, " + "), c.Priority, c.Reason)
			}
		}
		return nil
	}

	r, ok := compiled.Lookup(*ruleID)
	if !ok {
		return command.Refuse("no rule %q in bundle %s", *ruleID, version)
	}
	fmt.Fprintf(env.Stdout, "rule %s  (priority %d, %s)\n", r.ID, r.Priority,
		enabledWord(r.Enabled))
	if r.Description != "" {
		fmt.Fprintf(env.Stdout, "  %s\n", strings.TrimSpace(r.Description))
	}
	var cond strings.Builder
	printCondition(&cond, r.When, "  ")
	fmt.Fprint(env.Stdout, "  when:\n")
	fmt.Fprint(env.Stdout, cond.String())
	fmt.Fprintf(env.Stdout, "  then: %s\n", describeAction(r, orNone(r.Then.Severity)))

	if problems := explainRequirements(r); len(problems) > 0 {
		fmt.Fprintf(env.Stdout, "  requires:\n")
		for _, p := range problems {
			fmt.Fprintf(env.Stdout, "    - %s\n", p)
		}
	}
	if problems := explainDegradation(r); len(problems) > 0 {
		fmt.Fprintf(env.Stdout, "  when the AI plane is unavailable:\n")
		for _, p := range problems {
			fmt.Fprintf(env.Stdout, "    - %s\n", p)
		}
	}
	return nil
}

func printCondition(b *strings.Builder, c rules.Condition, indent string) {
	switch {
	case len(c.All) > 0:
		fmt.Fprint(b, indent, "all of:\n")
		for _, sub := range c.All {
			printCondition(b, sub, indent+"  ")
		}
	case len(c.Any) > 0:
		fmt.Fprint(b, indent, "any of:\n")
		for _, sub := range c.Any {
			printCondition(b, sub, indent+"  ")
		}
	case c.Not != nil:
		fmt.Fprint(b, indent, "not:\n")
		printCondition(b, *c.Not, indent+"  ")
	case len(c.EventType) > 0:
		fmt.Fprintf(b, "%seventType is one of: %s\n", indent, strings.Join(c.EventType, ", "))
	case c.Device != nil:
		parts := []string{}
		if c.Device.ID != "" {
			parts = append(parts, "id="+c.Device.ID)
		}
		if c.Device.Kind != "" {
			parts = append(parts, "kind="+c.Device.Kind)
		}
		if len(c.Device.Tags) > 0 {
			parts = append(parts, "tags="+strings.Join(c.Device.Tags, ","))
		}
		fmt.Fprintf(b, "%sdevice: %s\n", indent, strings.Join(parts, " "))
	case c.Zone != nil:
		parts := []string{}
		if c.Zone.ID != "" {
			parts = append(parts, "id="+c.Zone.ID)
		}
		if c.Zone.PathPrefix != "" {
			parts = append(parts, "under="+c.Zone.PathPrefix)
		}
		if c.Zone.AncestorOf != "" {
			parts = append(parts, "ancestorOf="+c.Zone.AncestorOf)
		}
		fmt.Fprintf(b, "%szone: %s\n", indent, strings.Join(parts, " "))
	case c.Schedule != nil:
		in := "inside"
		if c.Schedule.Invert {
			in = "outside"
		}
		fmt.Fprintf(b, "%sschedule %q, %s it\n", indent, c.Schedule.ID, in)
	case c.Field != nil:
		fmt.Fprintf(b, "%sfield %s %s %v\n", indent, c.Field.Path, c.Field.Op, c.Field.Value)
	case c.Corroborate != nil:
		fmt.Fprintf(b, "%srequires %d further signal(s) from: %s, at least tier %s\n",
			indent, c.Corroborate.MinSignals, strings.Join(c.Corroborate.EventTypes, ", "),
			orNone(string(c.Corroborate.MinEvidenceTier)))
	case c.ClockDrift != nil:
		fmt.Fprintf(b, "%sthe device's clock drift is at most %dms\n", indent, c.ClockDrift.MaxMS)
	case c.AIObservation != nil:
		fmt.Fprintf(b, "%sthe AI observation %s has %s == %v\n", indent,
			c.AIObservation.ProfileID, c.AIObservation.Field, c.AIObservation.Equals)
	case c.TimeWindow != nil:
		fmt.Fprintf(b, "%swithin %ds of normalized time\n", indent, c.TimeWindow.DurationSeconds)
	}
}

func explainRequirements(r rules.Rule) []string {
	var out []string
	for _, d := range r.Then.Dispatch {
		out = append(out, fmt.Sprintf("dispatches %s to %s", d.Type, d.Target))
	}
	if r.Then.RequireAIObservation {
		out = append(out, "an AI observation must have been recorded before the incident is raised")
	}
	if r.Then.Escalate {
		out = append(out, "escalates, which needs a step-up MFA verification from the operator")
	}
	return out
}

func explainDegradation(r rules.Rule) []string {
	var out []string
	walkForDegradation(r.When, &out)
	if len(out) == 0 {
		out = append(out, "nothing; this rule does not consume a Tier C signal, so an AI "+
			"outage does not change its outcome")
	}
	return out
}

func walkForDegradation(c rules.Condition, out *[]string) {
	if c.Corroborate != nil && c.Corroborate.IfAIAvailable != nil {
		switch c.Corroborate.IfAIAvailable.DegradeTo {
		case rules.DegradeToUnverified:
			*out = append(*out, "the rule still fires and the incident is marked UNVERIFIED with "+
				"the reason recorded, so an operator can tell it apart from a fully "+
				"corroborated one")
		case rules.DegradeToProceedWithout:
			*out = append(*out, "the rule fires as if the AI signal were absent; incidents "+
				"raised this way carry less evidence and the console flags it")
		}
	}
	if c.Corroborate != nil && c.Corroborate.RequiresAIObservation && c.Corroborate.IfAIAvailable == nil {
		*out = append(*out, "an AI observation is required but no behaviour is declared for "+
			"its absence; the bundle should not compile, and does not")
	}
	for _, sub := range c.All {
		walkForDegradation(sub, out)
	}
	for _, sub := range c.Any {
		walkForDegradation(sub, out)
	}
	if c.Not != nil {
		walkForDegradation(*c.Not, out)
	}
}

func describeAction(r rules.Rule, resolvedSeverity string) string {
	parts := []string{}
	if r.Then.Suppress {
		parts = append(parts, "suppress")
	} else if r.Then.MarkDuplicate {
		parts = append(parts, "mark duplicate")
	} else if r.Then.CreateIncident {
		parts = append(parts, "incident at "+orNone(resolvedSeverity))
	}
	if r.Then.Escalate {
		parts = append(parts, "escalate")
	}
	for _, d := range r.Then.Dispatch {
		parts = append(parts, d.Type+"→"+d.Target)
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, ", ")
}

func enabledWord(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "DISABLED"
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func shortHash(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}

func configuredSite() string { return os.Getenv("SP_SITE_ID") }

func activeBundleBytes(ctx context.Context, tenant string) (string, []byte, error) {
	pool, err := openPool(ctx)
	if err != nil {
		return "", nil, err
	}
	defer pool.Close()
	svc := &bundles.Service{Pool: pool}
	return svc.Active(ctx, tenant)
}

func activeBundleFor(ctx context.Context, tenant string) (*rules.CompiledBundle, error) {
	_, content, err := activeBundleBytes(ctx, tenant)
	if err != nil {
		return nil, err
	}
	compiled, problems := rules.NewCompiler().Compile(content)
	if compiled == nil {
		for _, pr := range problems {
			if pr.Severity == rules.SeverityError {
				return nil, fmt.Errorf("the active bundle does not compile: %s", pr)
			}
		}
		return nil, fmt.Errorf("the active bundle did not compile")
	}
	return compiled, nil
}

func readLine(env *command.Env) (string, error) {
	reader := bufioReader(env)
	return reader.ReadString('\n')
}

var _ = simulate.Diff
var _ = checkpoint.VerifySeal
var _ = errors.New

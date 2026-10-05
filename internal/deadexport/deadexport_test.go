package deadexport_test

// The gate, run over this repository.
//
// The allow-list below is the whole mechanism by which this gate can be made to
// pass without a reader existing, so it is the thing under test twice over: every
// entry names a symbol and a reason, and `TestTheAllowListIsAccurate` proves each
// entry is still a real, still-unread symbol — because a stale entry is an
// exemption for a defect that has since been fixed or renamed, and nobody
// notices, because the gate is green either way.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/deadexport"
)

const repoRoot = "../.."

// allowList is every exported symbol in the tree that nothing outside its own
// package reads, and why that is not dead code.
//
// Written from a run of the gate, not from memory. A reason written from memory
// is a guess about why a symbol has no reader, and a guess in an allow-list is
// indistinguishable from a real reason to anyone reading it.
// allowList is every exported symbol in the tree that nothing outside its own
// package reads, and why that is not dead code.
//
// Written from a run of the gate, not from memory. A reason written from memory
// is a guess about why a symbol has no reader, and a guess in an allow-list is
// indistinguishable from a real reason to anyone reading it.
//
// It has two sections, and the split is the point.
//
// **Published** — a closed vocabulary (the audit ledger's action names, the rule
// kind set, the H.264 NAL types), a sentinel error the package publishes as its
// error identity, or a documented default or bound. For all three, "no caller
// outside the package" is the normal state, because the consumer is a stored
// string in a ledger row, a comparison in a future failure path, or an operator
// reading a configuration value.
//
// **Unwired** — genuinely dead. The reason says so, says what would close it, and
// names the phase. This is not an exemption that claims the code works; it is a
// backlog item made visible, and the gate's stale-entry check turns it into a
// failure the day somebody wires or deletes the symbol.
var (
	allowList = map[string]deadexport.Exemption{
		// The audit ledger's action and actor vocabularies. These names are
		// written into hash-chained rows and read back by an auditor and by
		// `securityctl audit`, not by a Go caller; the vocabulary has to be
		// complete before the rows exist, so a member having no writer today is
		// not a defect.
		"internal/audit.Action":             {Prefix: "internal/audit.Action", Why: "the audit ledger's action vocabulary, published into hash-chained rows and read by an auditor rather than by a Go caller. A member with no writer today is a vocabulary that is not yet used, not dead code"},
		"internal/audit.Actor":              {Prefix: "internal/audit.Actor", Why: "the audit ledger's actor vocabulary. Same reasoning as the actions: the values are stored and read back as data"},
		"internal/audit.ErrIncompleteEntry": {Symbol: "internal/audit.ErrIncompleteEntry", Why: "sentinel: the hash-chain verifier returns it and the reason reaches the operator in the checkpoint failure row, so the identity is published even though nothing calls errors.Is on it"},

		// Clock-gap vocabulary. `clock.Gap*` is a closed set the corroboration
		// path switches over, and a gap kind is written into the exclusion reason
		// an operator reads when a window is not counted.
		"internal/clock.Gap":                    {Prefix: "internal/clock.Gap", Why: "the clock-gap kind vocabulary, written into the exclusion reason on a verification row and read by the operator and by docs, not by a Go caller"},
		"internal/clock.DriftMeasurementMaxAge": {Symbol: "internal/clock.DriftMeasurementMaxAge", Why: "published bound: how old a drift measurement may be before the gap assessor refuses to trust it. The value is a decision recorded in docs/ai-and-data.md and the in-package assessor is its only current reader because the override arrives from configuration rather than from code"},

		"internal/checkpoint.DefaultInterval": {Symbol: "internal/checkpoint.DefaultInterval", Why: "published default: the seal interval a site gets when its configuration omits one. The value is named in the site configuration documentation, so it is part of the configuration contract rather than an internal detail"},
		"internal/checkpoint.DecodeSeed":      {Symbol: "internal/checkpoint.DecodeSeed", Why: "published decoder for the checkpoint seed encoding, which is a wire format: a seed written by one build has to be readable by another, and the decoder is named in the format's own contract"},
		"internal/checkpoint.ErrVerify":       {Symbol: "internal/checkpoint.ErrVerify", Why: "sentinel: the checkpoint verifier returns it and the reason reaches the operator through `securityctl doctor` and the audit ledger"},

		"internal/clocksub.ErrUnknownDevice": {Symbol: "internal/clocksub.ErrUnknownDevice", Why: "sentinel: the clock subscriber returns it and the drift row records the reason in prose, so the identity is published without an errors.Is caller"},

		"internal/config.DefaultLookup":                 {Symbol: "internal/config.DefaultLookup", Why: "published default: how often the DSN lookup cache re-reads. The value is a decision in the site configuration contract and a tuning knob an operator is told about by name"},
		"internal/config.DefaultShutdownGrace":          {Symbol: "internal/config.DefaultShutdownGrace", Why: "published default: the drain window on shutdown, stated in the operations runbook by its Go name so an operator can find it"},
		"internal/config.MaxClaimBatch":                 {Symbol: "internal/config.MaxClaimBatch", Why: "published bound: the ceiling on a lease claim batch. It is a safety limit whose value belongs to the configuration contract, and raising it is a product decision rather than a refactor"},
		"internal/config.MaxLeaseDuration":              {Symbol: "internal/config.MaxLeaseDuration", Why: "published bound: the ceiling on a job lease. Same reasoning as MaxClaimBatch — a limit that exists to be lowered by a deployment, not to be referenced by a caller"},
		"internal/config.MaxPollInterval":               {Symbol: "internal/config.MaxPollInterval", Why: "published bound: the ceiling on the poll backoff. Same reasoning as MaxClaimBatch"},
		"internal/config.DefaultEvidenceFetchPreRoll":   {Symbol: "internal/config.DefaultEvidenceFetchPreRoll", Why: "published default: the default pre-roll for evidence fetch bookmarks, stated in the site configuration documentation"},
		"internal/config.DefaultEvidenceFetchPostRoll":  {Symbol: "internal/config.DefaultEvidenceFetchPostRoll", Why: "published default: the default post-roll for evidence fetch bookmarks, stated in the site configuration documentation"},
		"internal/config.DefaultEvidenceFetchRetainFor": {Symbol: "internal/config.DefaultEvidenceFetchRetainFor", Why: "published default: the default retention period for evidence fetch receipts, stated in the site configuration documentation"},
		"internal/config.DefaultRetentionSweepInterval": {Symbol: "internal/config.DefaultRetentionSweepInterval", Why: "published default: the default interval for the retention sweep, stated in the site configuration documentation"},

		"internal/correlator.DefaultTiers":  {Symbol: "internal/correlator.DefaultTiers", Why: "published default tier policy. The tiers are a product decision recorded in docs/ai-and-data.md, and the function is the seam a site configuration overrides"},
		"internal/correlator.MaxCandidates": {Symbol: "internal/correlator.MaxCandidates", Why: "published bound: the ceiling on correlation candidates per trigger. It is a cost limit an operator can lower, so the number belongs to the configuration contract"},
		"internal/correlator.ErrNoTrigger":  {Symbol: "internal/correlator.ErrNoTrigger", Why: "sentinel: the correlator returns it when a window held no trigger, and the reason appears in the window report an operator reads"},

		"internal/dbx.ErrNoTenant":  {Symbol: "internal/dbx.ErrNoTenant", Why: "sentinel: the connection layer refuses without a tenant context, which is invariant 5's first half made mechanical, and the refusal is reported to the operator in prose rather than by comparison"},
		"internal/dbx.ParseDSN":     {Symbol: "internal/dbx.ParseDSN", Why: "published parser for the site's DSN, which is a configuration format an operator writes by hand. It is named in the deployment documentation and is the parser the migrator and the install path both document"},
		"internal/dbx.SystemTenant": {Symbol: "internal/dbx.SystemTenant", Why: "published constant: the tenant id the system-level operations run under. It is a value in a configuration file and in a migration, not an internal detail"},

		"internal/evidence.DefaultFsync":            {Symbol: "internal/evidence.DefaultFsync", Why: "published default: the value New applies when Options.Fsync is nil, and the value the site configuration documents"},
		"internal/evidence.DefaultVerifyHashOnRead": {Symbol: "internal/evidence.DefaultVerifyHashOnRead", Why: "published default: the value New applies when Options.VerifyHashOnRead is nil. Hash verification on read is ON by default and applied at store.go:150; the option exists so a developer can turn it off while debugging, and no production caller turns it off"},

		"internal/graph.ErrNoProvenance":  {Symbol: "internal/graph.ErrNoProvenance", Why: "sentinel: the graph refuses an edge with no provenance, and the refusal is recorded on the rebuild report an operator reads"},
		"internal/graph.MaxSnapshotEdges": {Symbol: "internal/graph.MaxSnapshotEdges", Why: "published bound: the ceiling on edges in one topology snapshot. It is a memory limit a site can lower, so the number belongs to the configuration contract"},
		"internal/graph.LoadTopology":     {Symbol: "internal/graph.LoadTopology", Why: "UNWIRED. The snapshot loader nothing calls. Close it by having the console's topology view and the copilot's context builder call it, or delete it — this entry is not a claim that it works"},

		// The hosted-model client's vocabulary. Reason* is the degradation-reason
		// set that lands in `degraded_detail`, so its members are read by operators
		// and by the evaluation report rather than by a Go caller.
		"internal/hafnia.Reason":           {Prefix: "internal/hafnia.Reason", Why: "the hosted client's degradation-reason vocabulary, plus the ReasonFor table that maps a sentinel error onto it. A reason is written into an observation's degraded_detail and read by the operator and by the evaluation report, so a member with no in-tree comparator is a reason not yet produced rather than dead code"},
		"internal/hafnia.Err":              {Prefix: "internal/hafnia.Err", Why: "sentinel: the hosted client returns it, and the failure path converts it into a Reason* and a degraded row rather than comparing the error value"},
		"internal/hafnia.MaxResponseBytes": {Symbol: "internal/hafnia.MaxResponseBytes", Why: "published bound: the ceiling on a hosted response body. It is a resource limit an air-gapped deployment lowers, so the number is part of the configuration contract"},
		"internal/hafnia.NewObservationID": {Symbol: "internal/hafnia.NewObservationID", Why: "published identifier constructor: the observation id scheme is part of the stored-fact contract a replay depends on, so the constructor is named by the contract rather than called from one place"},
		"internal/hafnia.QuotaExhausted":   {Symbol: "internal/hafnia.QuotaExhausted", Why: "published quota query: the hosted budget is an operator-facing number surfaced by `securityctl doctor` and by the acceptance report, so the reader is a person rather than a package"},
		"internal/hafnia.RegionUS":         {Symbol: "internal/hafnia.RegionUS", Why: "published constant: the default hosted region, named in the site configuration file. Invariant: a region is a deployment decision, not an internal detail"},
		"internal/hafnia.Ask":              {Symbol: "internal/hafnia.Ask", Why: "UNWIRED. The one-shot ask, reserved-after-upload. Client.Do is the path in use because it must reserve before shipping footage to a third party, and that is the correct ordering — so Ask is the form a caller takes when the asset is already uploaded. Close it by having the evidence path call it, or delete it"},

		// Identity. The capability constants are the authorisation vocabulary, and
		// a capability with no route behind it today is a capability the operator
		// surface has not grown yet.
		"internal/identity.Cap":         {Prefix: "internal/identity.Cap", Why: "the authorisation capability vocabulary. Capabilities are compared in the policy tables and granted by role, so a capability with no in-tree grantor is one no role holds yet rather than dead code"},
		"internal/identity.Err":         {Prefix: "internal/identity.Err", Why: "sentinel: the identity service returns it and the handler maps it to an HTTP status by comparing the error value, in a path a future MFA flow will take"},
		"internal/identity.KeySize":     {Symbol: "internal/identity.KeySize", Why: "published constant: the generated key length, stated in the key-rotation runbook and checked by an operator's own tooling"},
		"internal/identity.TokenLength": {Symbol: "internal/identity.TokenLength", Why: "published constant: the session token length, which the login handler's own documentation and the incident runbook state by name"},

		"internal/inbox.StatusReceived": {Symbol: "internal/inbox.StatusReceived", Why: "the inbox lifecycle vocabulary. Statuses are stored as text and read by the recovery drill, by `securityctl doctor` and by the ops screen rather than by a Go caller"},
		"internal/jobs.Status":          {Prefix: "internal/jobs.Status", Why: "the job lifecycle vocabulary. Statuses are stored as text and read by the recovery drill, by the soak criteria and by the ops screen rather than by a Go caller"},

		"internal/jobs.ErrNotClaimed":    {Symbol: "internal/jobs.ErrNotClaimed", Why: "sentinel: the claim returns it when a lease lapsed, which is the zero-accepted-event-loss path, and the reason is recorded on the job row"},
		"internal/jobs.VLMLeaseDuration": {Symbol: "internal/jobs.VLMLeaseDuration", Why: "published bound: how long a VLM job may hold its lease before another worker may take it. It is a recovery-timeout an operator tunes by name"},

		"internal/incidents.StateDispositioned": {Symbol: "internal/incidents.StateDispositioned", Why: "the incident lifecycle vocabulary. States are stored as text and read by the console, by the replay and by the North Star denominator"},

		"internal/lifecycle.ErrUnsafePath": {Symbol: "internal/lifecycle.ErrUnsafePath", Why: "sentinel: the installer refuses an install path that is not safe, and the refusal text is the operator-facing message, so the error value is published as prose"},

		// Milestone. The capability, refusal and drift-band sets are vendor
		// vocabulary recorded in the route audit, and the route table is a list of
		// assumptions about an API nobody has reached.
		"internal/milestone.Capability":           {Prefix: "internal/milestone.Capability", Why: "the vendor capability vocabulary, carried on the route table and printed by `adapter-probe`. A capability with no adapter behind it is a route marked Unverified, which is the honest state of every one of them"},
		"internal/milestone.ClockDrift":           {Prefix: "internal/milestone.ClockDrift", Why: "the clock-drift band vocabulary, compared against the measured drift on every bookmark write-back and printed by the adapter probe"},
		"internal/milestone.Err":                  {Prefix: "internal/milestone.Err", Why: "sentinel: the adapter returns it and the transport converts it to an HTTP status through AsStatus, in a path a future route will take"},
		"internal/milestone.DefaultPageSize":      {Symbol: "internal/milestone.DefaultPageSize", Why: "published default: the page size the adapter requests when configuration omits one"},
		"internal/milestone.MaxPageSize":          {Symbol: "internal/milestone.MaxPageSize", Why: "published bound: the largest page the adapter will request from a vendor API that nobody has reached, so the value is a written-down assumption rather than a tuned limit"},
		"internal/milestone.MaxBodyBytes":         {Symbol: "internal/milestone.MaxBodyBytes", Why: "published bound: the largest response body the adapter will read. It is a resource limit an air-gapped deployment lowers"},
		"internal/milestone.VerdictUnreachable":   {Symbol: "internal/milestone.VerdictUnreachable", Why: "part of the probe's three-verdict vocabulary (confirmed, contradicted, unreachable). It is a value in the probe report a human adjudicates, and a probe that could not reach the API has not cleared anything"},
		"internal/milestone.AsStatus":             {Symbol: "internal/milestone.AsStatus", Why: "published mapping from a sentinel error to an HTTP status, used by the transport and named in the route audit's §5 sequence"},
		"internal/milestone.IsNotFound":           {Symbol: "internal/milestone.IsNotFound", Why: "published predicate over the not-found sentinels, the documented way for a caller to tell 'the vendor has no such object' from 'the vendor call failed'"},
		"internal/milestone.RouteByKind":          {Symbol: "internal/milestone.RouteByKind", Why: "published lookup into the route table. The table is a list of assumptions about an API nobody has reached, and this is its published index"},
		"internal/milestone.NormalizedEventTypes": {Symbol: "internal/milestone.NormalizedEventTypes", Why: "UNWIRED. The vendor-name to canonical-type mapping nothing reads. It is blocked on evidence no test can produce — the names must be a real site's — and the sequence is in the route audit §5. Ratification item R9"},
		"internal/milestone.BookmarkKey":          {Symbol: "internal/milestone.BookmarkKey", Why: "published key derivation for a write-back bookmark, part of the replayable write-back record format an operator's tooling reads"},
		"internal/milestone.ExposedScenarioNames": {Symbol: "internal/milestone.ExposedScenarioNames", Why: "UNWIRED. The scenario-name accessor nothing calls. Close it by having the console or the demo print the scenario names a replay transport exposes, or delete it"},

		// MP4. The NAL constants are the H.264 unit-type codes from the standard,
		// and a set transcribed from a specification is complete or it is wrong.
		"internal/mp4.NAL":                 {Prefix: "internal/mp4.NAL", Why: "the H.264 NAL unit-type codes, transcribed from the standard. A set read off a specification is complete or it is wrong, and a caller switches on it rather than naming one member"},
		"internal/mp4.Err":                 {Prefix: "internal/mp4.Err", Why: "sentinel: the Annex-B reader returns it and the recorder propagates the reason into the clip's own record, so the identity is published without an errors.Is caller"},
		"internal/mp4.Default":             {Prefix: "internal/mp4.Default", Why: "published default: the pre-roll, post-roll and clip-length ceilings a site gets when configuration omits them. The values are the site's evidence budget, stated in the operations runbook by name"},
		"internal/mp4.Max":                 {Prefix: "internal/mp4.Max", Why: "published bound: the ceiling on a box or a parameter set, which is what stops a malformed file from making the reader allocate without limit. A bound nobody names is still the bound that is enforced"},
		"internal/mp4.AVCC":                {Symbol: "internal/mp4.AVCC", Why: "UNWIRED. The length-prefix reader nothing calls. Close it by having the recorder read AVCC when a camera emits length-prefixed NALs, or delete it"},
		"internal/mp4.AVCCLen":             {Symbol: "internal/mp4.AVCCLen", Why: "UNWIRED. The companion of AVCC and the same decision: wire both in the same commit or delete both"},
		"internal/mp4.NewPreRoll":          {Symbol: "internal/mp4.NewPreRoll", Why: "UNWIRED. The pre-roll ring constructor nothing calls. Close it by having the RTSP recorder build its pre-roll through it, or delete it"},
		"internal/mp4.Closed":              {Symbol: "internal/mp4.Closed", Why: "UNWIRED. The recorder's closed-state predicate nothing calls. Close it by having the health surface report it, or delete it"},
		"internal/mp4.DecoderConfigRecord": {Symbol: "internal/mp4.DecoderConfigRecord", Why: "UNWIRED. The decoder-configuration record extractor nothing calls. Close it by having the evidence viewer render the codec it extracted, or delete it"},
		"internal/mp4.PreRollDepth":        {Symbol: "internal/mp4.PreRollDepth", Why: "UNWIRED. The pre-roll depth accessor nothing calls. Close it by having the recorder's health line report it, or delete it"},
		"internal/mp4.PreRollGroups":       {Symbol: "internal/mp4.PreRollGroups", Why: "UNWIRED. The pre-roll group accessor nothing calls. Close it with PreRollDepth, in the same commit"},

		"internal/normalize.ToProto":              {Symbol: "internal/normalize.ToProto", Why: "UNWIRED. The canonical-event to protobuf projection nothing calls. Close it by having the release exporter emit through it, or delete it"},
		"internal/observations.ErrInvalid":        {Symbol: "internal/observations.ErrInvalid", Why: "sentinel: the observation store returns it and the rule engine records the reason on the degraded row, so the identity is published without an errors.Is caller"},
		"internal/observations.InputHash":         {Symbol: "internal/observations.InputHash", Why: "UNWIRED. The input-hash accessor nothing calls in product code. It is the same value the evidence-completeness companion re-verifies against, so P4's fourth North Star companion is where it is wired"},
		"internal/outbox.ErrLeaseLost":            {Symbol: "internal/outbox.ErrLeaseLost", Why: "sentinel: the relay returns it when a lease lapsed, which is the zero-duplicate-side-effect path, and the reason is recorded on the message row"},
		"internal/release.ErrVerification":        {Symbol: "internal/release.ErrVerification", Why: "sentinel: release verification returns it and `securityctl verify-release` prints the reason to the operator, so the identity is published as prose"},
		"internal/pipeline.InvalidateBundleCache": {Symbol: "internal/pipeline.InvalidateBundleCache", Why: "UNWIRED. The bundle cache invalidation hook nothing calls. Close it by having bundles.Activate call it, so an activated bundle takes effect without a restart, or delete it"},

		"internal/rtsp.DefaultPort": {Symbol: "internal/rtsp.DefaultPort", Why: "published constant: the RTSP port a site binds, stated in the network requirements the deployment document lists"},
		"internal/rtsp.UserAgent":   {Symbol: "internal/rtsp.UserAgent", Why: "published constant: the User-Agent this product presents to a camera. It is part of the product's external identity and is asserted by the camera's own logs"},
		"internal/rtsp.Err":         {Prefix: "internal/rtsp.Err", Why: "sentinel: the RTSP client returns it and the adapter records the reason on the device's integration row, so the identity is published without an errors.Is caller"},
		"internal/rtsp.State":       {Prefix: "internal/rtsp.State", Why: "the RTSP connection-state vocabulary, written to the device's integration row and read by the console's device page and by `securityctl doctor`"},

		// The rule language. These are the published vocabularies of an untrusted
		// input format: the JSON Schema in contracts/json-schema names them, the
		// compiler switches over them, and a member with no in-tree producer is a
		// rule construct the language offers and the built-in bundle does not use.
		"internal/rules.Kind":            {Prefix: "internal/rules.Kind", Why: "the compiled rule-kind vocabulary. It is the enum of an untrusted input format — rule-bundle.schema.json names every member — and the compiler switches over the set, so a member no built-in rule uses is a construct the language offers, not dead code"},
		"internal/rules.Degrade":         {Prefix: "internal/rules.Degrade", Why: "the rule engine's degradation-reason vocabulary. The reasons are written onto the incident's verification row and read by the operator and by the evaluation harness"},
		"internal/rules.KnownEventTypes": {Symbol: "internal/rules.KnownEventTypes", Why: "published vocabulary: the canonical event types a rule may match, shared by the bundle schema, the compiler's validation and the adapter's normaliser. It is read by a bundle that is not in this tree (a site's own bundle) and by the rule-lint gate's vocabulary check"},
		"internal/rules.DeviceKinds":     {Symbol: "internal/rules.DeviceKinds", Why: "published vocabulary: the device kinds a rule may match, shared by the bundle schema and the compiler. A site's own bundle names them"},
		// `internal/rules.DispatchTypes` was HERE, excused as "published vocabulary ... and P8's
		// policy engine is the consumer that will read them". It is deleted because
		// `internal/capability` now reads it - which is the whole point: the closed set was
		// declared for a consumer that did not exist, and the vocabulary was dead in exactly
		// the way a vocabulary is dead when nothing dispatches anything. Its reader is the
		// gate that measures whether every member has an executor.
		"internal/rules.Severities":           {Symbol: "internal/rules.Severities", Why: "published vocabulary: the incident severities a rule may declare, shared by the bundle schema and the compiler"},
		"internal/rules.DefaultBusinessHours": {Symbol: "internal/rules.DefaultBusinessHours", Why: "published default: the business-hours window a schedule condition gets when the bundle omits one. It is a product decision recorded in docs/ai-and-data.md"},
		"internal/rules.TierC":                {Symbol: "internal/rules.TierC", Why: "published constant: the tier a Tier C signal carries, which is the vocabulary the schema's tier field is expressed in"},
		"internal/rules.StateDuplicate":       {Symbol: "internal/rules.StateDuplicate", Why: "part of the verification-state vocabulary the evaluation harness and the incident row share, stored as text and read by the harness rather than by a Go caller"},
		"internal/rules.MarshalCanonical":     {Symbol: "internal/rules.MarshalCanonical", Why: "UNWIRED. The canonical bundle marshaller nothing calls. Close it by having the bundle digest and the console's rule screen use it, so the digest an operator reads is the bytes the compiler saw, or delete it"},
		"internal/rules.RuleIDs":              {Symbol: "internal/rules.RuleIDs", Why: "UNWIRED. The rule-id accessor nothing calls. Close it by having the console's rule screen and the activation digest diff list ids through it, or delete it"},

		"internal/simulate.Change": {Prefix: "internal/simulate.Change", Why: "the what-if simulation's change vocabulary, written into the simulation report a judge reads"},

		"internal/soak.Answer":      {Prefix: "internal/soak.Answer", Why: "the soak harness's stub-answer vocabulary. It is the set of failure shapes a soak run rehearses — degraded, garbage, malformed, refused, timeout, unavailable — and a rehearsal set that dropped a member would no longer cover it"},
		"internal/soak.Event":       {Prefix: "internal/soak.Event", Why: "the soak harness's event-state vocabulary, compared against the events a run observed to decide whether the run passed. Every member is a pass/fail criterion, so the set is the specification of the criterion"},
		"internal/soak.CostPerCall": {Symbol: "internal/soak.CostPerCall", Why: "published cost constant: the per-call cost the soak report multiplies by a call count, so an operator can read a budget rather than a number"},
		"internal/soak.Rehearsals":  {Symbol: "internal/soak.Rehearsals", Why: "UNWIRED. The failure-rehearsal runner nothing calls. Close it by having `securityctl loadrun` and `securityctl demo` run the rehearsals before the load, so the degradation path is exercised before it is measured, or delete it"},

		"internal/airuntime.MaxResponseBytes": {Symbol: "internal/airuntime.MaxResponseBytes", Why: "published bound: the largest body the AI runtime will read. The AI runtime is untrusted input, so the bound is part of its contract and is asserted by the Python test that drives it"},

		"internal/api.DecodeJSON":           {Symbol: "internal/api.DecodeJSON", Why: "UNWIRED. The request decoder nothing calls. Close it by having the disposition form and the new write routes decode through it, so every body is bounded the same way, or delete it"},
		"internal/api.WriteJSON":            {Symbol: "internal/api.WriteJSON", Why: "UNWIRED. The response writer nothing calls. Close it with DecodeJSON, in the same commit, so the pair is one decision"},
		"internal/api.WriteError":           {Symbol: "internal/api.WriteError", Why: "UNWIRED. The error-response writer nothing calls. Close it with the two above: every handler's refusal should render through one writer, which is also what makes the console's error pages consistent"},
		"internal/api.RequestIDFromContext": {Symbol: "internal/api.RequestIDFromContext", Why: "UNWIRED. The request-id accessor nothing calls. Close it by having the handlers stamp the request id on every log line, which is the convention AGENTS.md already states and nothing currently enforces"},
		"internal/api.ErrNotWired":          {Symbol: "internal/api.ErrNotWired", Why: "UNWIRED. The not-wired sentinel nothing returns. Close it by having each route that has a nil dependency return it rather than rendering an empty page, which is the distinction the console's own comments call out"},

		"internal/checkpoint.Gap":      {Symbol: "internal/checkpoint.Gap", Why: "UNWIRED. The checkpoint gap predicate nothing calls. Close it by having the recovery drill assert on it, or delete it"},
		"internal/checkpoint.SealOnce": {Symbol: "internal/checkpoint.SealOnce", Why: "UNWIRED. The single-interval seal entry point nothing calls. Its range sibling SealRange was one of the nine dead exports that started this gate; this is the same cluster. Close it by having the scheduler call it, or delete it"},
		"internal/clock.AssessGap":     {Symbol: "internal/clock.AssessGap", Why: "UNWIRED. The gap assessor nothing calls from product code. Close it by having the corroboration path call it before excluding a window, which is the invariant-3 requirement that an exclusion carry a reason, or delete it"},
		"internal/clock.Partial":       {Symbol: "internal/clock.Partial", Why: "UNWIRED. The partial-window predicate nothing calls. Close it with AssessGap, in the same commit"},
		"internal/clock.Present":       {Symbol: "internal/clock.Present", Why: "UNWIRED. The clock-presence predicate nothing calls. Close it with AssessGap, in the same commit"},

		"internal/bundles.ErrNotStored": {Symbol: "internal/bundles.ErrNotStored", Why: "sentinel: the bundle store returns it for a version it does not hold, and the console's rules screen renders the reason so an operator at a site with no network can tell a missing version from a refused activation"},

		"internal/detector.LastRatio":  {Symbol: "internal/detector.LastRatio", Why: "UNWIRED. The changed-pixel fraction accessor nothing calls. Close it by having the incident's evidence line report the ratio beside the threshold, which is the number an operator needs to tell a real movement from a lighting change, or delete it"},
		"internal/fixture.RedactURL":   {Symbol: "internal/fixture.RedactURL", Why: "UNWIRED. The URL redactor nothing outside the package calls. Close it by having the sanitiser's entry point route through it, so there is one redactor rather than two shapes, or delete it"},
		"internal/fixture.SortedNames": {Symbol: "internal/fixture.SortedNames", Why: "UNWIRED. The manifest ordering helper nothing calls. Close it by having the manifest writer order through it, which is what the comment says it is for — a regenerated manifest has to produce a reviewable diff rather than a reshuffle — or delete it"},

		"internal/graph.Relations": {Symbol: "internal/graph.Relations", Why: "the graph's closed relation vocabulary, enforced in-package by the edge validator and exported so anything that offers an edge can check membership first. No adapter offers an edge by hand today, so the exported set has no external reader; the enumeration is what makes an unrecognised relation a deployment fault rather than a row nobody reads"},
		"internal/graph.Methods":   {Symbol: "internal/graph.Methods", Why: "the graph's closed provenance-method vocabulary, enforced in-package by the edge validator. Same reasoning as Relations: exported so an offerer can check membership, and the enumeration is the control"},

		"internal/identity.ConstantTimeEqual": {Symbol: "internal/identity.ConstantTimeEqual", Why: "published comparator for secrets, used in-package by every secret comparison and exported so a caller outside the identity package compares a secret the same way. Its absence as an external reader is why this entry exists: the function is correct and the discipline is not yet applied outside the package that owns the secrets"},
		"internal/identity.NewToken":          {Symbol: "internal/identity.NewToken", Why: "published token constructor. The session layer uses it in-package for sessions and CSRF tokens; it is exported because the raw token never reaches storage, so every producer of one needs the same raw-plus-hash shape rather than a hash of something it chose itself"},
		"internal/identity.PurgeExpired":      {Symbol: "internal/identity.PurgeExpired", Why: "UNWIRED. The collection of dead sessions, challenges and proofs nothing runs. Close it by scheduling it from the janitor job the migrations already have a queue for, or delete it"},
		"internal/identity.InSiteScope":       {Symbol: "internal/identity.InSiteScope", Why: "UNWIRED. The site-scope predicate nothing calls. Close it by having the console's per-site routes check it, which is migration 21's RESTRICTIVE site_isolation made mechanical at the application edge, or delete it"},

		"internal/inbox.ClaimAll": {Symbol: "internal/inbox.ClaimAll", Why: "UNWIRED. The cross-tenant claim nothing calls. Its own comment says a caller reaching for it is making a deliberate choice to drain across tenants and is visible at the call site — which is the argument for leaving it unwired until a system worker genuinely needs it, and the argument for deleting it if none ever does"},

		// The methods below are one class, and it is worth naming: each is called
		// from a test and from nothing else. A test proves a symbol behaves as
		// documented; it does not prove anything calls it, which is the whole
		// reason this gate exists and the reason `slo.Breaches` was on the list.
		// Each entry says what the method is for and which surface should be
		// calling it, so the list is a backlog somebody can work rather than
		// thirty-seven lines of "unused".

		"internal/api.SetClock": {Symbol: "internal/api.SetClock", Why: "TEST-ONLY. The server's clock override. Nothing sets it, so a console request is judged against the host clock rather than a pinned one; P5's disposition flow should set it, because a step-up proof that expires is a proof judged against the wrong clock"},

		"internal/clocksub.Measure":                {Symbol: "internal/clocksub.Measure", Why: "TEST-ONLY. The device-clock measurement the subscriber records. Nothing in the ingest path calls it, so drift is measured for the operator's benefit only and never for the normalization that depends on it"},
		"internal/clocksub.MeasureFromSourceTimes": {Symbol: "internal/clocksub.MeasureFromSourceTimes", Why: "TEST-ONLY. The measurement for a source system rather than a device, for a site whose events come from a VMS rather than from a camera. Nothing calls it, so a VMS-sourced site gets no drift correction and the correlator's window is a window on the VMS's belief about itself"},
		"internal/clocksub.SevereCount":            {Symbol: "internal/clocksub.SevereCount", Why: "TEST-ONLY. How many devices are past the trust bound. It should be what `securityctl doctor` reports and what the console's device page shows; neither reads it, so an operator cannot tell which devices are untrustworthy without querying by hand"},

		"internal/clock.SkewExceeds": {Symbol: "internal/clock.SkewExceeds", Why: "TEST-ONLY. The predicate deciding whether a measurement is past the trust bound. The gap assessor is where it belongs and the assessor itself is unwired, so the two are one backlog item"},

		"internal/deadexport.FilesParsed":     {Symbol: "internal/deadexport.FilesParsed", Why: "TEST-ONLY. This gate's own accessor, read by its own test. The gate reported itself before it reported anything else, which is the only honest way to introduce one. It stays because the test's floor on the walk is the control that catches a walk which stopped working"},
		"internal/deadexport.SymbolsDeclared": {Symbol: "internal/deadexport.SymbolsDeclared", Why: "TEST-ONLY. The other half of that floor: a walk that reaches nothing reports no symbols, and both numbers are asserted together so neither can pass alone"},

		"internal/detector.Baseline": {Symbol: "internal/detector.Baseline", Why: "TEST-ONLY. The coded-size baseline the framerate detector measures against. Nothing outside the detector reads it, so a camera's baseline is set once by a test and never re-measured by the product"},

		"internal/evidence.Exists":         {Symbol: "internal/evidence.Exists", Why: "TEST-ONLY. Whether a stored reference is present. The console's evidence list and the retention sweep should both ask it; neither does, so a fetch can be reported for a reference the store was never told about"},
		"internal/evidence.StagingResidue": {Symbol: "internal/evidence.StagingResidue", Why: "TEST-ONLY. What the staging directory still holds after a crash. It should be what `securityctl doctor` reports: a crash mid-fetch leaves footage staged for ever and nothing surfaces it"},

		"internal/graph.AddEdge":      {Symbol: "internal/graph.AddEdge", Why: "TEST-ONLY. The single-edge adder behind the bulk one. Either the bulk form should be built on it or the single form should go; a pair where only one has a caller is a pair whose shape is undecided"},
		"internal/graph.CheckRebuild": {Symbol: "internal/graph.CheckRebuild", Why: "TEST-ONLY. The rebuild check that reports a graph problem rather than storing it. The console's topology view should render its findings; until then a broken edge set is found by a test and by nobody else"},
		"internal/graph.MarshalJSON":  {Symbol: "internal/graph.MarshalJSON", Why: "TEST-ONLY. The graph's own JSON form. The console renders a snapshot through this, so a graph shown in the console is a re-serialised copy rather than the stored shape"},
		"internal/graph.Revoke":       {Symbol: "internal/graph.Revoke", Why: "TEST-ONLY. Revoking an edge. Nothing revokes one in the product, so the revoked-edges view is empty by construction rather than by finding"},
		"internal/graph.ZonePaths":    {Symbol: "internal/graph.ZonePaths", Why: "TEST-ONLY. The zone-path index the rule compiler consumes. The compiler reads it through the snapshot, so this is a second path to the same answer with no caller of its own"},

		"internal/hafnia.Quota":             {Symbol: "internal/hafnia.Quota", Why: "TEST-ONLY. The hosted budget. `securityctl doctor` should report it: an operator who cannot see the budget learns about it by being cut off mid-incident"},
		"internal/hafnia.OutstandingAssets": {Symbol: "internal/hafnia.OutstandingAssets", Why: "TEST-ONLY. What is still uploaded at the hosted provider. This is a data-residency question as much as a cost one -- footage in flight is footage outside the site -- and nothing reports it"},
		"internal/hafnia.SweepAssets":       {Symbol: "internal/hafnia.SweepAssets", Why: "TEST-ONLY. Deleting the hosted copy of an asset whose retention has passed. Nothing calls it, so a deletion meant to happen never does, and the honest statement about residency is the one that cannot be made"},

		"internal/identity.CheckCSRF":   {Symbol: "internal/identity.CheckCSRF", Why: "TEST-ONLY. The CSRF check. The handler calls it and the service method does not -- two entry points to one control, and the one a future handler reaches for is the one with no caller"},
		"internal/identity.SetPassword": {Symbol: "internal/identity.SetPassword", Why: "TEST-ONLY. Setting a password. The login flow sets one by a different path, so this is a second entry point to the password path with no caller"},

		"internal/inbox.Renew":        {Symbol: "internal/inbox.Renew", Why: "TEST-ONLY. Extending an event lease. A normalizer that finishes fast never renews and one that needs more must re-claim; that is the right design, so this is the wrong use of it"},
		"internal/jobs.Renew":         {Symbol: "internal/jobs.Renew", Why: "TEST-ONLY. Extending a job lease, the same reasoning as the inbox's. A long VLM call runs on a fixed lease and is recovered as dead rather than completed"},
		"internal/incidents.SetState": {Symbol: "internal/incidents.SetState", Why: "TEST-ONLY. The lifecycle transition itself. Every transition the product performs goes through a higher-level method, so this is the primitive under one call site and a test"},

		"internal/milestone.AllCapabilities": {Symbol: "internal/milestone.AllCapabilities", Why: "TEST-ONLY. Every capability the vendor API is assumed to have. The route audit is checked against it, and a route marked Unverified is honest only while something can say which capabilities would have cleared it"},
		"internal/milestone.AllConfirmed":    {Symbol: "internal/milestone.AllConfirmed", Why: "TEST-ONLY. The capabilities a probe has actually confirmed. The report prints three verdicts and none of the confirmed set, so a reader cannot tell an empty result from a result never gathered"},

		"internal/observability.IncLabeled": {Symbol: "internal/observability.IncLabeled", Why: "TEST-ONLY. The labelled counter increment. Ten of twenty-seven metrics had no producer and this is the producer they would have used; a metric whose only caller is a test is a metric nobody scraped"},
		"internal/observability.Stale":      {Symbol: "internal/observability.Stale", Why: "TEST-ONLY. Whether a gauge has been updated since it was last expected. This is the metric that turns a silently dead pipeline into a red one, and nothing reads it"},

		"internal/observations.Availability":      {Symbol: "internal/observations.Availability", Why: "TEST-ONLY. The AI availability figure. The console dashboard should show it and the SLO should be evaluated against it; neither does, so 'the AI plane was up' is a belief rather than a measurement"},
		"internal/observations.LatestForIncident": {Symbol: "internal/observations.LatestForIncident", Why: "TEST-ONLY. The current observation for an incident. The console's incident page renders one, so it is reaching past this to a raw read and the page and the invariant can disagree about which observation is current"},
		"internal/observations.Stats":             {Symbol: "internal/observations.Stats", Why: "TEST-ONLY. The observation statistics. P3's evaluation harness scores a report against them and the copilot quotes them; neither reads them, so the numbers exist and nothing consumes them"},

		"internal/rtsp.KeepAlive": {Symbol: "internal/rtsp.KeepAlive", Why: "TEST-ONLY. The RTSP keep-alive. The recorder should send one; nothing does, so a camera that times out a stream stops and the gap looks like a quiet scene rather than a lost camera"},

		// New packages from the stabilization branch (Tier 1.2-1.3). These are UNWIRED
		// because the packages are newly created and their consumers (pipeline, relay,
		// acceptance) are wired in subsequent commits. Each entry is a commitment to
		// wire it, not a permanent exemption.
		"internal/dispatch.DecodeNotification": {Symbol: "internal/dispatch.DecodeNotification", Why: "UNWIRED. The notification decoder. The relay will call it when handling notify_* outbox messages; currently only the internal test reads it"},
		"internal/dispatch.DecodeJob":          {Symbol: "internal/dispatch.DecodeJob", Why: "UNWIRED. The job decoder. The relay will call it when handling fetch_evidence/request_vlm outbox messages; currently only the internal test reads it"},
		// DecodeJobPayload and EncodeJobPayload are used by internal/evidencefetch, so they
		// have external readers and are not dead. The allow-list entries were stale.
		"internal/evidencefetch.ActionPlaced":   {Symbol: "internal/evidencefetch.ActionPlaced", Why: "UNWIRED. The audit action for a placed bookmark. The audit ledger will record it when the worker runs; currently only the worker writes it"},
		"internal/evidencefetch.ActionRefused":  {Symbol: "internal/evidencefetch.ActionRefused", Why: "UNWIRED. The audit action for a refused bookmark. The audit ledger will record it when the worker refuses; currently only the worker writes it"},
		"internal/evidencefetch.ReceiptVersion": {Symbol: "internal/evidencefetch.ReceiptVersion", Why: "UNWIRED. The receipt document's contract version. A future reader will check it; currently only the worker writes it"},
		"internal/evidencefetch.VendorName":     {Symbol: "internal/evidencefetch.VendorName", Why: "UNWIRED. The vendor name constant. The receipt carries it; currently only the worker writes it"},

		// Three that the gate was NOT reporting, and the reason is in the gate's own
		// code. `addSelectors` records every selector name in the tree UNQUALIFIED,
		// because a method is only ever reached through a receiver and a per-package
		// rule would report every interface method as dead. The qualification it did
		// apply was "the qualifier names one of THIS repository's packages" -- so a
		// qualifier that names an IMPORTED package still counted, and:
		//
		//	filepath.Walk          in internal/deadexport, internal/predicatecov and
		//	                       cmd/securityctl/pack.go  gave internal/graph.(*Service).Walk
		//	                       a reader it has never had
		//	ed25519.PrivateKey    in three cmd files is a TYPE CONVERSION, and its selector
		//	                       read gave internal/checkpoint.(*Signer).PrivateKey one too
		//	fs.Sub/embed.FS        in the schemasync package itself did the same to
		//	                       internal/contracts/schemasync.FS
		//
		// So the gate was reporting dead exports as LIVE, which the package comment
		// names as the direction that gets a gate switched off rather than the
		// direction that makes it cry wolf. It was found by adding `runtime.KeepAlive`
		// to the tree, which did the same thing to `internal/rtsp.KeepAlive` and made
		// that ledger entry look stale.
		"internal/graph.Walk":              {Symbol: "internal/graph.Walk", Why: "DEAD. The graph traversal entry point. Nothing in the product calls it -- `filepath.Walk` was the reader the gate saw, three files over. The pipeline's graph service is written and tested and unwired, so the `edges` the decision cites are built by a correlator rather than traversed; the close is either a traversal step in internal/correlator or deleting this, and which of those is a design decision rather than a cleanup"},
		"internal/checkpoint.PrivateKey":   {Symbol: "internal/checkpoint.PrivateKey", Why: "DEAD. Key material out of a signer. `ed25519.PrivateKey(key)` in three cmd files is a TYPE CONVERSION and its selector read satisfied this gate. There is deliberately no reason for anything in the product to read a signing key back -- key material should flow one way into a Signer -- so the close is most likely DELETING the accessor rather than wiring it"},
		"internal/contracts/schemasync.FS": {Symbol: "internal/contracts/schemasync.FS", Why: "DEAD. Exposes the embedded schema copies as an fs.FS 'for a caller that wants to compile a validator from them'. No such caller exists yet: schemasync.Bytes and schemasync.Published are what the contract gate and the bundle loader read. The close is a validator that takes the whole directory rather than a single file, which is a real requirement for rule-bundle schemas that $ref each other -- not a one-line wire-up"},
		// The three gate entry points below are the same shape as one another and
		// they are this gate's own first finding of itself: correct code, a test
		// that proves it works, and no caller outside its own test. They are run by
		// `scripts/verify.ps1` through `go test ./internal/<gate>/...`, which reaches
		// the package rather than the symbol. Closing them properly means a
		// `tools/` runner per gate so the script can invoke the package directly,
		// and that is a real change to the gate script rather than to these packages.
		"internal/statuscheck.Derive": {Symbol: "internal/statuscheck.Derive", Why: "UNWIRED (test-only caller). The countable-claims derivation behind internal/statuscheck. It has an external test package reader today -- statuscheck_test.go -- which is a real consumer, and this entry is here because the derivation is also what `securityctl status` should print, so a caller in the command would close it properly"},

		// Two lease defaults, one per job family. The inbox worker and the outbox
		// relay each state how long a claim is held; both are also deployment
		// decisions an operator is told about by name in the operations runbook.
		"internal/jobs.DefaultLeaseDuration":   {Symbol: "internal/jobs.DefaultLeaseDuration", Why: "published default: how long a claimed job is held before another worker may take it. The companion `jobs.VLMLeaseDuration` is the AI-plane figure and is recorded beside it; this is the general one, and both are deployment decisions an operator tunes by name"},
		"internal/outbox.DefaultLeaseDuration": {Symbol: "internal/outbox.DefaultLeaseDuration", Why: "published default: how long a claimed outbox message is held before the relay may take it again. It is the lease whose loss would cause a duplicate dispatch, which is invariant 1's second half, so the number belongs to the operations contract rather than to a caller"},
	}
)

func loadRepo(t *testing.T) *deadexport.Repo {
	t.Helper()
	if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err != nil {
		t.Fatalf("%s is not the module root: %v", repoRoot, err)
	}
	return deadexport.Load(repoRoot)
}

func TestNoExportedSymbolIsDead(t *testing.T) {
	repo := loadRepo(t)
	t.Log(repo.Summary())
	t.Log(deadexport.PrintedAllowList(allowList))

	findings, errs := repo.Check(allowList)
	for _, e := range errs {
		t.Errorf("%v", e)
	}
	for _, f := range findings {
		t.Errorf("%s", f)
	}
}

// TestTheGateCoversTheTree is the floor on the gate's own honesty. A gate that
// parses nothing reports nothing and looks like a pass, so the count is asserted
// rather than trusted.
func TestTheGateCoversTheTree(t *testing.T) {
	repo := loadRepo(t)
	// A deliberately low floor, and the check is on the number of Go files rather
	// than on the number of symbols, because the file count is what proves the
	// walk reached the tree. A wrong walk usually still finds some symbols.
	const minFiles = 200
	if n := repo.FilesParsed(); n < minFiles {
		t.Fatalf("only %d Go files were parsed (floor %d); the walk is not reaching the tree, so "+
			"a green result would mean nothing", n, minFiles)
	}
	if n := repo.SymbolsDeclared(); n < 400 {
		t.Fatalf("only %d exported symbols were read (floor 400); the walk is not reaching the tree", n)
	}
}

// TestTheGateFindsADeadExport proves the gate is a gate.
//
// It runs the whole pipeline over a fixture written to a temp directory: a
// package with an exported symbol nothing reads, a package with one that is read,
// an in-package test that reads the dead symbol, and an external test that reads
// it. The first must be reported and the last three must not. This is the
// red-check for the gate itself — the control is the check, and a check nobody
// has watched work is a comment.
func TestTheGateFindsADeadExport(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "deadpkg/thing.go", `package deadpkg

// Unused is exported and nothing outside this package names it.
func Unused() {}

// AlsoUnusedConst is a published constant nobody outside reads.
const AlsoUnusedConst = 3

// Counter has a method nothing in the tree calls, which is the shape
// observability.Breaches had: a unit test proved the evaluator worked and no
// route or loop ever invoked it.
type Counter struct{ n int }

func (c *Counter) Add(delta int) { c.n += delta }

// Live has a method that is called, and the gate must not confuse the two.
type Live struct{ n int }

func (l *Live) Bump() { l.n++ }

// Uncalled is the method with no selector anywhere in the tree.
func (l *Live) Uncalled() {}
`)
	write(t, dir, "deadpkg/thing_test.go", `package deadpkg

import "testing"

func TestInPackageTestReadsIt(t *testing.T) {
	// A test in the declaring package is a test, not a caller. This is the shape
	// slo.Breaches had: a test that proved the evaluator worked, and no evaluator
	// ever called.
	Unused()
	_ = AlsoUnusedConst
	c := &Counter{}
	c.Add(1)
	(&Live{}).Uncalled()
}
`)
	write(t, dir, "deadpkg/external_test.go", `package deadpkg_test

import (
	"testing"

	"deadpkg"
)

func TestExternalTestReadsIt(t *testing.T) {
	// An external test package is a real consumer of the top-level symbols, so
	// these two are alive. It calls no method, deliberately: a test calling a
	// method is the slo.Breaches case, not a reader.
	deadpkg.Unused()
	_ = deadpkg.AlsoUnusedConst
}
`)

	// A second package that calls one method of the two, so the method rule has a
	// positive case in the same fixture as its negative one.
	write(t, dir, "livepkg/live.go", `package livepkg

import "deadpkg"

// call is unexported on purpose: the fixture is about deadpkg, and an exported
// Call with no reader would be a second finding.
func call() int {
	(&deadpkg.Live{}).Bump()
	_ = deadpkg.AlsoUnusedConst
	return 0
}
`)

	repo := deadexport.LoadDir(dir, []string{"deadpkg", "livepkg"})
	findings, errs := repo.Check(nil)
	for _, e := range errs {
		t.Fatalf("unexpected error: %v", e)
	}

	got := map[string]bool{}
	for _, f := range findings {
		got["deadpkg."+f.Symbol] = true
	}

	if !got["deadpkg.Uncalled"] {
		t.Error("deadpkg.Uncalled is a method no non-test selector in the tree names, and was not reported")
	}
	if !got["deadpkg.Add"] {
		t.Error("deadpkg.Add is called only from a test, so it is dead product code, and was not reported")
	}
	if got["deadpkg.Bump"] {
		t.Error("deadpkg.Bump is called from livepkg and was reported as uncalled")
	}
	if got["deadpkg.Unused"] {
		t.Error("deadpkg.Unused is read by an external test package and was reported")
	}
	if got["deadpkg.AlsoUnusedConst"] {
		t.Error("deadpkg.AlsoUnusedConst is read by livepkg and was reported as dead")
	}

	// Exactly the two methods nothing in product code calls. The const is read,
	// the func is read, and the two test-only methods are the findings.
	want := map[string]bool{"deadpkg.Uncalled": true, "deadpkg.Add": true}
	if len(findings) != len(want) {
		names := make([]string, 0, len(findings))
		for _, f := range findings {
			names = append(names, f.Package+"."+f.Symbol)
		}
		t.Errorf("expected exactly %v, got %d: %v", want, len(findings), names)
	}
}

// TestTheGateIsNotFooledByItsOwnFile is the other half of the self-check: a symbol
// mentioned in its own declaration, and in its own body, is not a reader of
// itself. Without the object subtraction the fixture above would pass for the
// wrong reason.
func TestTheGateIsNotFooledByItsOwnFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "selfpkg/self.go", `package selfpkg

// Recursive names itself twice, in its signature and in its body. Neither is a
// reader: the file is the declaring file.
func Recursive(n int) int {
	if n <= 0 {
		return 0
	}
	return Recursive(n - 1)
}
`)
	repo := deadexport.LoadDir(dir, []string{"selfpkg"})
	findings, errs := repo.Check(nil)
	for _, e := range errs {
		t.Fatalf("unexpected error: %v", e)
	}
	if len(findings) != 1 || findings[0].Symbol != "Recursive" {
		names := make([]string, 0, len(findings))
		for _, f := range findings {
			names = append(names, f.Package+"."+f.Symbol)
		}
		t.Errorf("a self-recursive function is not a reader of itself; expected exactly Recursive, got %v", names)
	}
}

// TestAPackageQualifiedSelectorIsNotAMethodCall is the red-check for a false
// negative the gate had in production use.
//
// `covercheck.Baseline` is a type referenced across a package boundary, and
// `detector.(*FrameDiff).Baseline` is a method whose only callers are tests. The
// method rule matched on the bare spelling, so the type reference excused the
// uncalled method and an allow-list entry became stale — which the gate then
// reported as "you excused a symbol nothing breaks the rule about any more",
// pointing at a package nobody had touched.
//
// A package-qualified selector is never a method call: a method is reached through
// its receiver, and the receiver is a value.
func TestAPackageQualifiedSelectorIsNotAMethodCall(t *testing.T) {
	dir := fixture(t, map[string]string{
		"shared/thing.go": `package shared

// Baseline is a type, referenced across the package boundary by uses.go.
type Baseline struct{ N int }
`,
		"uses/uses.go": `package uses

import "shared"

func Make() shared.Baseline { return shared.Baseline{N: 1} }
`,
		"deadpkg/dead.go": `package deadpkg

type Detector struct{ n int }

// Baseline shares a spelling with shared.Baseline and is called by nobody in
// product code.
func (d *Detector) Baseline() int { return d.n }
`,
	})
	repo := deadexport.LoadDir(dir, []string{"shared", "uses", "deadpkg"})
	findings, errs := repo.Check(nil)
	for _, e := range errs {
		t.Fatalf("unexpected error: %v", e)
	}

	for _, f := range findings {
		if f.Package == "deadpkg" && f.Symbol == "Baseline" {
			return
		}
	}
	var names []string
	for _, f := range findings {
		names = append(names, f.Package+"."+f.Symbol)
	}
	t.Errorf("a type named Baseline in another package excused an uncalled method of the same name; "+
		"findings were %v", names)
}

// TestAnImportedPackageQualifiedSelectorIsNotAMethodCall is the same test for the
// qualifier that is NOT one of this repository's packages, and it is the one that
// found three live defects being reported as dead.
//
// `TestAPackageQualifiedSelectorIsNotAMethodCall` above covers `shared.Baseline`,
// where `shared` is a package in the tree, so `r.packageNames` recognises the
// qualifier. An IMPORTED package is the same syntactic shape and was not recognised:
// the qualifier was not a local package name, the selector was recorded as a bare
// `Walk`, and every `.Walk` in the tree became a reader of any method called `Walk`.
//
// In this repository that had been hiding three dead exports, which the previous test
// could not have found for all the world:
//
//	filepath.Walk         in internal/deadexport, internal/predicatecov and
//	                      cmd/securityctl/pack.go masked internal/graph.(*Service).Walk
//	ed25519.PrivateKey   in three cmd files is a TYPE CONVERSION, and its selector
//	                      read masked internal/checkpoint.(*Signer).PrivateKey
//	embed.FS / fs.Sub     in internal/contracts/schemasync masked
//	                      internal/contracts/schemasync.FS
//
// The direction is the one the gate's own package comment calls out as the bad one:
// reporting a dead export as LIVE. A gate that under-reports is a gate that gets
// switched off, and this one was switched off in three places without anybody
// noticing.
//
// The fixture is `filepath.Walk` specifically rather than a fictional import, because
// the thing being asserted is that a selector whose qualifier the file IMPORTED is
// not a receiver, and `filepath` is the import that caused it.
func TestAnImportedPackageQualifiedSelectorIsNotAMethodCall(t *testing.T) {
	dir := fixture(t, map[string]string{
		"walkpkg/walk.go": `package walkpkg

type Detector struct{ n int }

// Walk shares a spelling with filepath.Walk, which the repository calls in three
// places, and is called by nobody in product code.
func (d *Detector) Walk() int { return d.n }
`,
		"walker/walker.go": `package walker

import "path/filepath"

// Visiting calls filepath.Walk, whose selector shares the spelling of
// walkpkg.(*Detector).Walk.
func Visiting(root string) error {
	return filepath.Walk(root, func(string, any, error) error { return nil })
}
`,
	})
	repo := deadexport.LoadDir(dir, []string{"walkpkg", "walker"})
	findings, errs := repo.Check(nil)
	for _, e := range errs {
		t.Fatalf("unexpected error: %v", e)
	}

	for _, f := range findings {
		if f.Package == "walkpkg" && f.Symbol == "Walk" {
			return
		}
	}
	var names []string
	for _, f := range findings {
		names = append(names, f.Package+"."+f.Symbol)
	}
	t.Errorf("filepath.Walk excused an uncalled method of the same name; findings were %v", names)
}

// TestAnExemptionWithoutAReasonIsRefused keeps the allow-list from growing blanks.
func TestAnExemptionWithoutAReasonIsRefused(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "p/p.go", "package p\n\nfunc Dead() {}\n")
	repo := deadexport.LoadDir(dir, []string{"p"})

	findings, errs := repo.Check(map[string]deadexport.Exemption{"p.Dead": {Symbol: "p.Dead"}})
	if len(findings) != 0 {
		t.Errorf("a blank exemption should still hide the finding, but the finding survived: %v", findings)
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "without saying why") {
			found = true
		}
	}
	if !found {
		t.Errorf("a blank reason was not reported: %v", errs)
	}
}

// fixture writes a throwaway module tree from a map of `package/file.go` keys.
//
// The three-argument `write` above is used by the earlier tests, which lay their
// files out themselves; this is for the ones that read better as a whole package
// list, and it returns the root so the caller can point `LoadDir` at it.
func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	names := make([]string, 0, len(files))
	for k := range files {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, rel := range names {
		write(t, dir, rel, files[rel])
	}
	return dir
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", full, err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

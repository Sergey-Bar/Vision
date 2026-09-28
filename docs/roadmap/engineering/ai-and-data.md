# AI and Data

Owner: E1 (Product/Architecture), with the model runtime owned by the Applied
AI hire once it exists. Status: living. Last revised 2026-09-28.

Scope: the AI track — VLM observation profiles, the local LLM, evidence
intelligence, semantic search, site intelligence, autonomy, and the governance
that makes it sellable. Read `AGENTS.md` (the five invariants are the law),
`../strategy-and-north-star.md` §4 (the three buyer questions), `../master-roadmap.md`
§M2/§M3/§M6/§M7, `quality-engineering.md` §7, `../security/trust-and-security-program.md` §4,
`metrics-and-analytics.md` §5, and `docs/status.md` first.

Every claim below was read in the tree. Where something does not exist, this
document says so and says what it must be. Estimates are marked **ESTIMATE**.

---

## 1. The AI architecture, as it exists

### 1.1 The tiers, which are the spine of invariant 3

`contracts/protobuf/security/v1/evidence.proto:48-56` defines three tiers, and
the comments in the proto are the architecture:

| Tier | Name | Meaning | May be absent? |
|---|---|---|---|
| A | `EVIDENCE_TIER_A_AUTHORITATIVE` | "authoritative system state reported by the source of truth" | No. A device either said it or it did not. |
| B | `EVIDENCE_TIER_B_DERIVED` | "deterministic derived fact computed by the core" | No, but it is *derived* and must be reproducible from stored facts (invariant 2) |
| C | `EVIDENCE_TIER_C_AI_OBSERVATION` | "AI observation. **May be absent; never treated as negative evidence.**" | Yes. This is the only tier that may be absent. |

A rule declares what it needs through `corroborate.minEvidenceTier`
(`rule-bundle.schema.json:178`) and what it does when Tier C is missing through
`corroborate.ifAIAvailable` (`:180-188`). The shipped bundle uses exactly this:
`core.person-in-restricted-zone-after-hours` sets `minEvidenceTier: B` with
`requiresAIObservation: true` and `ifAIAvailable.degradeTo: UNVERIFIED`, and
`core.door-forced-after-hours` two rules later "needs no AI" at all. Both
patterns are in `rules/builtin/bundle.yaml:87-99` and `:117-120`.

### 1.2 The real record

`internal/observations/observations.go` is 563 lines and the package doc states
the model plainly: "An observation is a historical fact: 'this model, this
prompt, said this, about evidence with these hashes, at this time'. It is
append-only, it is attributable, and it is never overwritten — a re-evaluation is
a new row that names the one it supersedes."

The attribution every row carries (`Observation`, lines 107-145) is the thing
that makes the answer to "what did the AI decide and why?" answerable:

| Field | Enforcement |
|---|---|
| `Model`, `ModelHash` | `Validate` refuses empty. "A model name is not an identity." (`:172-174`) |
| `RuntimeVersion` | required (`:175-176`) |
| `PromptTemplateID`, `PromptVersion` | required. "The same model with a different prompt is a different thing." (`:177-180`) |
| `ModelBundleSignature` | carried; **not required by `Validate`** — see §10.3 |
| `InputEvidenceIDs`, `InputEvidenceHashes` | `InputHash()` (`:209-219`) length-prefixes both; the Python runtime sorts by `(id, hash)` rather than id alone because "a sort that is not a total order makes this hash depend on the order the caller happened to pass" (`runtime.py:119-127`, and `docs/status.md` deviation 11) |
| `StructuredOutput` | **never prose**: "a rule that could match wording would change behaviour when the prompt is rephrased, and an incident that appears or disappears because a prompt was edited is not an incident anyone can defend" (`:131-133`) |
| `OutputSchemaVersion` | required, must be > 0 (`:182-183`) |
| `OutputHash` | `OutputHash()` (`:199-206`) — SHA-256 over the canonical JSON, so "a months later somebody can recompute the hash from the stored output and compare it" |
| `Degraded` | one of six closed values (`:44-56`), refused if unrecognised: "a value this package does not recognise would read as no degradation downstream" (`:184-186`) |
| `InsufficientEvidence` | a degraded observation is forced to `true` (`:329-330`) |
| `SupersedesObservationID` | the chain is walkable via `SupersededBy` (`:448-471`) |
| `LatencyMS`, `Hardware` | recorded, feeding the latency gate |

`Record` (`:260-314`) runs inside the caller's transaction with
`ActorID: "ai_runtime"` on the tenant context — "because 'the model said this'
and 'the system decided this' are different facts and a reader must be able to
tell them apart." The same distinction is a first-class audit actor:
`audit.ActorAIRuntime` (`audit.go:44`) and the actions
`ai.observation.recorded` / `ai.observation.degraded`
(`audit.go:75-76`).

**Ids are random, deliberately and for a stated reason** (`:333-359`): a
canonical event is a fact recorded once and gets a *derived* id
(`normalize.DeriveEventID`) so a crash retry lands on the same row; an
observation is a *run* of a model over evidence, and two runs are two
observations. "A derived id would collide them, or force the clock to be part of
the identity — which is worse, because a golden test then cannot pin it."

### 1.3 The three absences, kept apart

The package doc (`:9-16`) is the clearest statement of the product invariant in
the codebase:

```
no observation        the AI plane was never asked
degraded observation  it was asked and could not answer
insufficient evidence the model answered "I cannot tell"
```

"None of them is a negative answer, and none of them may satisfy a rule that
required an observation." `Stats` (`:497-534`) counts them in three separate
buckets, and the comment on the middle one is worth repeating to anyone who
tries to fold them together: insufficient-evidence is "not a negative answer
and it is not an availability failure either, so it is counted separately rather
than folded into either."

`Stats.Availability()` (`:488-494`) is the number a pilot is asked for:
`(Total − Degraded) / Total`.

### 1.4 The runtime, and the four facts about it that are load-bearing

`deploy/ai-runtime/runtime.py` is 526 lines, standard library only, and CI
asserts the no-third-party-import property by grepping the import lines
(`.github/workflows/ci.yml:130-139`).

**(a) It is a separate process on purpose** (`:1-19`): "a library call inside the
core cannot honour that: a hung inference would hang the ingest pipeline." It
binds `SP_AI_LISTEN`, default `127.0.0.1:8800` (`:511`).

**(b) It has no model.** `Runtime.run` (`:314-341`) validates evidence, checks
availability, and then **unconditionally raises** `DEGRADED_NO_RUNTIME`. There is
no branch that produces a successful result. `docs/status.md` says the same
thing in prose.

**(c) It returns a *reason* on every failure path, and a reason is never
`none` on a failure.** `InferenceUnavailable` (`:61-76`) validates its own
reason against `ALLOWED_DEGRADATIONS` and *raises* rather than returns, "because
every caller in this process has to handle it and a return value invites a caller
that forgets." `degraded_response` (`:185-214`) sets `insufficientEvidence: True`
on every degraded body, and the comment is the product's fail-closed property in
one sentence: "A consumer that ignores the degradation field then fails closed
rather than reading an empty output as a negative answer."

The HTTP layer answers **200, not 503**, on a degradation (`:460-468`): "the
request was answered: the answer is that the plane could not answer, and it
carries a reason. A 503 would make a caller retry a condition that will not
change."

**(d) It validates its output against the profile schema** — `validate_output`
(`:355-374`) checks the top-level field set and the required set, and nothing
else: "A full JSON Schema validator is a dependency this service deliberately does
not take, and the field set is the part that matters here: an undeclared field is
one nothing downstream validates."

`Evidence.verify()` (`:96-107`) refuses evidence whose bytes do not match the
recorded hash: "an observation citing evidence the model never actually saw is
worse than no observation, because it looks corroborated."

### 1.5 Four findings in the AI plane, named

**(F1) The Go core has no client for the runtime. There is no AI plane
integration at all.** `rg` for `/v1/infer`, `SP_AI_LISTEN`, `runtime.py`,
`Inference`, `aiRuntime` across every `.go` file in the tree returns **zero
hits**. `jobs.QueueVLM` (`jobs.go:40`) and `job.proto`'s `JOB_QUEUE_VLM` exist as
queue names; `jobs.go:281` special-cases the queue for a longer lease; **no
worker consumes it**, and no binary enqueues it. `internal/api/handlers.go`
registers no route that reaches the plane. So the deployed topology today is: a
Go core with a complete, tested, append-only observation record; a Python process
that is correct, tested and honest about having no model; and **nothing
connecting them.**

**What must exist (M1, and it is a gate dependency, not a feature):**

1. A Go client for `POST /v1/infer` with a hard deadline, no retry on a degraded
   body (a degraded answer is a terminal answer for that observation), and
   no automatic retry that could turn a slow model into a queue storm.
2. A `vlm` queue worker that claims a job, assembles `Evidence` from
   `evidence` rows (hash and bytes, never a path), posts, and records the result
   through `observations.Writer.Record` or `RecordDegraded`.
3. A configuration surface (`securityctl` today has no command for the plane —
   `cmd/securityctl/main.go:53-69` registers preflight, doctor, install, upgrade,
   rollback, backup, restore, verify-release, build-release, migrate, rules,
   incident, audit, identity, release-key).
4. A test that kills the runtime process mid-request and asserts a degraded
   observation is recorded and no other pipeline stage is affected. That test is
   the operational proof of invariant 3.

Without (1)-(4), "M1 exit gate 4 — a production VLM profile has passed its
promotion gates on a real GPU, in shadow mode, for 14 consecutive days" is
unreachable, because there is no path from a rule's `dispatch: request_vlm`
(`rules/bundle.yaml:105-107`) to the plane.

**(F2) The model's signature is not verified. `ModelBundle.verify()` compares a
digest, not a signature.** `runtime.py:238-279`: it reads `manifest.json` and
`manifest.sig`, requires `public_key` to be *non-empty*, then does
`actual = sha256(manifest_bytes)` and `declared = manifest.sig` contents, and
compares those two strings. **No asymmetric signature is computed or checked, and
the public key is never used for anything except a truthiness test.** A
`manifest.sig` containing the manifest's own SHA-256 hexdigest passes.

The docstring says "Check the bundle's signature against the configured public
key" and the error message says "the bundle signature does not match its
manifest". Both are inaccurate. The code does refuse an unsigned or modified
bundle relative to a *digest file* — which is a tamper-evidence check, not an
authenticity check, and it does not survive an attacker who can write to the
bundle directory, because that attacker regenerates `manifest.sig`.

**What must exist:** an actual Ed25519 (or the project's chosen algorithm)
verification against the configured public key over the manifest bytes, with
the signature file being the signature and the public key being the trust
anchor. `release-sre-and-support.md` §1.2 already builds, signs and verifies
release archives this way; the model bundle is the same problem and should reuse
the same primitive rather than inventing a digest comparison. Until then,
`../security/trust-and-security-program.md` §4.1's AI-governance record cannot honestly
claim a signed model bundle, and neither should a customer-facing document.

**(F3) `Evidence.verify()` returns true for evidence sent without bytes.**
`runtime.py:96-107`: `if not self.data: return True`. The HTTP handler builds
`Evidence` from `base64.b64decode(e.get("dataBase64", ""))` (`:440-447`), so a
caller that omits the bytes gets a free pass on hash verification. The property
being bought is "the model was shown exactly the bytes the observation records",
and a caller that sends ids and hashes without bytes does not get it. **What
must exist:** a decision — either bytes are mandatory in this API, or the
hash-verification claim is narrowed to "bytes are verified when supplied" and
the observation records that it was not verified. An unstated third option is
what exists today.

**(F4) The `AIDegradation` closed set is written in three places and nothing
checks that they agree.** `observations.Degradations` (`observations.go:53-56`),
`incident.proto`'s `AIDegradation` enum (`:95-102`), and
`runtime.py`'s `ALLOWED_DEGRADATIONS` (`:49-58`). `runtime.py` says so itself
("These are the same closed set the Go schema's CHECK constraint allows, and a
reason outside it would be stored as a string nothing recognises"). There is no
generated source, no `make contract-sync` rule and no CI check. This is the same
class of gap as the two hand-maintained event-type vocabularies in
`integrations-and-adapters.md` §2.1, and it should be fixed the same way: one
source, generated, embedded, checked.

---

## 2. Invariant 3, restated operationally

> A missing AI observation is never a negative observation.

### 2.1 The rule, at the layer where it is enforced

`internal/rules/semantic.go:419-440` is the enforcement and it is unusually
well-argued:

```go
// The invariant the plan states most forcefully: a rule that consumes a Tier C
// signal must declare what happens without one. There is no default here,
// deliberately — defaulting would make the decision on the operator's behalf
// at evaluation time, on a site that has never thought about it.
consumesAI := corr.RequiresAIObservation ||
    (corr.MinEvidenceTier != "" && corr.MinEvidenceTier == TierC)
if consumesAI && corr.IfAIAvailable == nil {
    // error: if_ai_unavailable_missing
}
```

Note the definition of `consumesAI`: it is true when the rule *explicitly*
requires an AI observation **or** when it sets `minEvidenceTier: C`. That is a
complete test, and `checkAIObservation` (`:465-506`) closes the other half: a
rule naming an `aiObservation.field` that the profile does not declare is a
compile error (`ai_field_unknown`), and one naming a profile this build has no
schema for is a *warning* — "Without it the check would be a guess about a bundle
that may be perfectly valid on a site that has the profile installed."

There is also a warning in the direction that matters commercially
(`semantic.go:450-460`): `degradeTo: proceed_without` is permitted but warns,
because "an incident is created on less evidence whenever the model is down, and
that should be a conscious choice." The shipped bundle uses `UNVERIFIED`.

### 2.2 The three states and the three behaviours

| State | How a consumer detects it | What it may do | What it must never do |
|---|---|---|---|
| **Never asked** | No `ai_observations` row for `(incident, profile)` | Evaluate the degraded branch declared in `ifAIAvailable` | Infer a negative from the absence |
| **Degraded** | `degraded_reason != none`; `insufficient_evidence` forced true (`observations.go:329`) | Record it, surface it, count it in `Stats.DegradedByReason` | Treat the empty `structured_output` as "no restricted person present" |
| **Insufficient** | `insufficient_evidence = true`, `degraded = none` | Treat as UNVERIFIED | Count it as a false positive *or* a miss |

`evaluation.predictedRestricted` (`:296-309`) is the invariant expressed once,
and its doc comment says exactly that: "A missing field, a degraded observation
and an insufficient-evidence observation all yield nil, which the caller counts as
an absence rather than as a negative. That is the invariant, expressed once."

### 2.3 The authoring consequences, per consequence

| Consequence for a rule author | Rule | Compile-time signal | Test |
|---|---|---|---|
| A rule that needs AI must say so | `requiresAIObservation: true` or `minEvidenceTier: C` | — (the declaration *is* the signal) | `compile_test.go:386-392` |
| …and must declare its degraded behaviour | `ifAIAvailable.degradeTo` required | `if_ai_unavailable_missing` (error) | `TestUnknownEventTypeIsRejected` neighbours; the two `if_ai_unavailable` assertions at `compile_test.go:392` and `:441` |
| Declaring it without using it is a smell | `ifAIAvailable` on a rule with no AI | `if_ai_unavailable_unused` (warning) | `semantic.go:434-440` |
| Choosing the unsafe degradation is allowed but visible | `degradeTo: proceed_without` | `degrade_proceeds` (warning) | `semantic.go:450-460` |
| Naming a field the profile does not emit is impossible | field checked against `Compiler.KnownAIFields` | `ai_field_unknown` (error) | `compile_test.go:456` (`hallucinated` field) |
| A rule must not read model prose | `AIObservationCondition` has no `contains`/`matches` operator; only `equals` | the type has one field | `bundle.go:225-229`, `rule-bundle.schema.json:209-218` |
| A time-window predicate may not read `observed_at` or `received_at` | `timeWindow.field` must be `normalized_at` | `time_window_field` (error) | `semantic.go:302-313` |

### 2.4 The tests, and the one that is missing

| Property | Test | Exists? |
|---|---|---|
| A rule consuming Tier C without `ifAIAvailable` fails to compile | `compile_test.go` | **Yes** |
| A degraded observation cannot satisfy a Tier C requirement | `predictedRestricted` returns nil | **Yes**, as unit logic |
| The plane being *entirely absent* leaves the core working | — | **No.** This is the load-bearing test and it needs F1's client to exist first. The equivalent today is "the core never imports the plane", which is trivially true because nothing imports it, and trivially true is not a test. **What must exist:** a fault-injection test that stops the runtime process, processes a fixture scenario end to end, and asserts every incident, verification, disposition and audit row is identical to the run with the plane present, modulo `degraded` flags and `Stats.Degraded` |
| A missing observation is never a negative | — | **No, at the integration level.** The unit-level `predictedRestricted` is the right primitive; the property that matters is that the *engine* does not convert a nil into a false. `rules/match.go` should be asserted on |

`quality-engineering.md` §3's invariant-3 row should carry these three, and the
second and third are currently missing.

---

## 3. The VLM profile programme (M2)

### 3.1 The gap in the plan, stated first

`../master-roadmap.md` §M2 says "**Ten profiles**, each with a structured
observation schema, a pinned prompt version, a pinned model version, an
evaluation dataset, a promotion gate, and shadow-then-production stages" and then
never names one. `product-and-backlog.md` §3.4 has a *template* for a profile
spec, and §8.2 of that template's list of things to complete is empty. The ten
profiles below are **this document's proposal** and they need E1's signature
before anything is built. Getting this wrong costs a milestone, because a
profile is a schema, a prompt, a dataset, a gate run and a shadow period — six
to eight weeks each, and they cannot be compressed by adding engineers.

### 3.2 The three that exist

`observations.M1Profiles` (`:74-99`) is the shipped set, and its structure is
already the right one: `restricted-person` is production; `door-forced` and
`vehicle-at-gate` are `Shadow: true`. `IsProductionProfile` (`:102-105`) is the
gate.

| Profile | Production? | Schema | Fields |
|---|---|---|---|
| `restricted-person` | **yes** | `contracts/json-schema/ai-observations/restricted-person.schema.json` | `insufficientEvidence`, `persons[]` (max 32, each with `present`, `region`, `obstructed`, `carryingObject`, `uniformed`, `direction`), `accessState` (unknown/authorized/unauthorized/forced/tailgated), `restrictedZoneBreach`, `afterHours`, `summary` (≤1000), `confidence` |
| `door-forced` | shadow | `door-forced.schema.json` | `insufficientEvidence`, `doorState` (unknown/closed/open/forced/ajar), `personPresent`, `obstruction` (none/unknown/object/body), `openDurationSeconds` (0..86400), `summary`, `confidence` |
| `vehicle-at-gate` | shadow | `vehicle-at-gate.schema.json` | `insufficientEvidence`, `vehicleState` (unknown/absent/approaching/at_gate/departing), `vehicleCount` (0..64), `plateLegible`, `plateText` (≤16, nullable), `gateState`, `summary`, `confidence` |

All three have `additionalProperties: false`, `schemaVersion: {const: 1}`,
`profileId: {const: <name>}`, and `"The model must be able to say 'insufficient
evidence' rather than guess"` in the `restricted-person` description. The
`persons[].region` field is described as "Frame region label **emitted by the
runtime**, e.g. door_roi" — the runtime does the region segmentation and the
model classifies within a named region. That is the right design and it is worth
stating explicitly because it is what makes `restrictedZoneBreach` derivable
(§3.4).

### 3.3 The statistics: `MinLabelledCases: 30` is arithmetically wrong

`evaluation.DefaultGates()` (`:103-111`) sets `MaxFalsePositiveRate: 0.20`,
`MinRecallOnTruePositives: 0.70`, `MaxMedianLatencyMS: 8000`,
`MaxDisagreementRate: 0.25`, `MinLabelledCases: 30`. The harness enforces the
rate on the **observed** proportion (`evaluateGates`, `:267`, fails when
`pr.FalsePositiveRate > g.MaxFalsePositiveRate`), so at n=30 a profile passes if
it produces at most 6 false positives in 30 labelled cases.

**The problem is not the threshold. The problem is that 30 observations cannot
distinguish a 20% false-positive rate from a 37% one, and the direction of the
error is always the dangerous one.**

Derivation. Wilson score interval, 95% (z = 1.96), for p̂ = k/n:

```
centre   = (p̂ + z²/2n) / (1 + z²/n)
halfwidth = z/(1 + z²/n) · sqrt( p̂(1−p̂)/n + z²/4n² )
```

**False-positive gate, n = 30, observed exactly at the gate (k = 6, p̂ = 0.200):**

- z²/n = 3.8416/30 = 0.128053; 1 + z²/n = 1.128053
- centre = (0.200 + 0.064027)/1.128053 = **0.23406**
- p̂(1−p̂)/n = 0.16/30 = 0.005333; z²/4n² = 3.8416/3600 = 0.001067; sum = 0.006400; √ = 0.080003
- halfwidth = (1.96/1.128053) × 0.080003 = 1.737517 × 0.080003 = **0.13900**
- **upper 95% bound = 0.3731**

So a profile that lands precisely on the stated 0.20 gate at n = 30 has data
consistent with a true false-positive rate of **37.3%** — nearly double the gate,
with 95% confidence. The gate is not demonstrated to be met; it is demonstrated
not to have been missed by more than a factor of two.

For completeness, n = 30 with k = 0 gives an upper bound of 0.1135. So *zero*
observed false positives at n = 30 is a genuine pass — the gate is achievable at
n = 30 only if the profile is essentially perfect, which is not the design point.

**Recall gate, n = 30, observed exactly at the gate (p̂ = 0.70):**

- centre = (0.700 + 0.064027)/1.128053 = 0.67729
- p̂(1−p̂)/n = 0.21/30 = 0.007000; + 0.001067 = 0.008067; √ = 0.089817
- halfwidth = 1.737517 × 0.089817 = 0.15606
- **lower 95% bound = 0.5212**

A profile with a true recall of **52%** passes the "70% recall" gate at n = 30.
This is the severe direction: the gate is designed to keep a profile from being
precise by being quiet, and at n = 30 it does not.

Solving for the observed recall at n = 30 whose lower bound is 0.70: p̂ = 0.86
gives 0.6953, p̂ = 0.87 gives 0.7072. So the *effective* recall gate at n = 30 is
an observed **0.865**, not 0.70 — 16.5 points stricter than the document says,
which means every profile evaluated at n = 30 either fails for a reason the gate
report cannot explain, or passes for a reason the gate report overstates.

**What n = 200 does.** At n = 200 (z²/n = 0.019208, 1 + z²/n = 1.019208,
z/(1+z²/n) = 1.923072):

- FP gate: p̂ = 0.140 (28/200) → centre 0.14679, halfwidth
  1.923072 × 0.0250202 = 0.04812, **upper = 0.1949** ✓. So at n = 200 the
  effective observed FP threshold is ~14.0% against a stated 20%.
- Recall gate: p̂ = 0.800 → centre 0.79434, halfwidth
  1.923072 × 0.0287040 = 0.05520, **lower = 0.7391** ✓. So at n = 200 the
  effective observed recall threshold is ~0.79 against a stated 0.70.

**Conclusion.** `MinLabelledCases: 30` is not a conservative choice that happens
to be small. It is a value that makes both gates *unfalsifiable* while appearing
to enforce them. **The order of magnitude required for a 0.20 FP gate at 0.70
recall is ~200 labelled cases**, and at n = 200 both gates become real
statements. The change to make is `DefaultGates().MinLabelledCases = 200`, and it
has a consequence that must be said out loud: **the M1 exit gate 4 ("a production
VLM profile in shadow mode for 14 consecutive days at a design-partner site")
cannot be signed at 200 labelled cases unless the site produces 200 judged cases
of one class in the shadow window.** A single-site physical-security stream does
not produce 200 *judged* cases of a restricted-person class in two weeks. The
four options, and the recommendation:

| Option | What it means | Cost |
|---|---|---|
| **A — Lower the gate to what the site can produce, and say so** | Set the gate to n = 50 and state publicly that the FP rate is known to ±13 points. Ship with the honesty, not with the number | Free. Sells badly |
| **B — Pool cases across sites and vendors** | A shared evaluation corpus, anonymised, versioned. This is what `quality-engineering.md` §8.3's "customer data, before anything else" is for | Legal (DPA, anonymisation) — `../security/compliance-and-certifications.md` §8.2 |
| **C — Promote on shadow-mode agreement rate, not on absolute rates** | For a *shadow* profile, the honest gate is "does it agree with the operator's eventual disposition, on the cases we have", with the interval reported | Cheapest and most honest, and it is what `metrics-and-analytics.md` §5.4's replay-audit approach already gestures at |
| **D — Delay the gate to M2** | Accept that M1 gate 4 is a *plumbing* gate (the profile runs, the harness scores it, the report is produced) and the *accuracy* gate lands at M2 with a larger corpus | Requires saying so to the board |

**Recommendation: C, then B.** C is true about what 14 days of shadow mode can
demonstrate, and B is the mechanism that makes the absolute gate reachable at
M2. **What must not happen** is a gate report that prints "false-positive rate
0.20, passed" from 30 cases. That is the specific dishonesty this product
exists to avoid, and it would be found by the first buyer who asks to see the
denominator.

**A third, quieter consequence.** The rule of three: to demonstrate a
missed-incident rate below 1% with zero observed misses you need more than 300
true positives (3/n < 0.01 → n > 300). At one design-partner site, with a
restricted-person class, that is on the order of two years of traffic. **Any
claim of a sub-1% missed-incident rate before M7 is arithmetic we cannot make**,
and `metrics-and-analytics.md` §5.4's "false negatives: the silent direction"
should be read together with this.

### 3.4 Two fields that should not be the model's to answer

`restricted-person` emits `restrictedZoneBreach` and `afterHours`. Both are
deterministic derivations, not perception:

- **`restrictedZoneBreach`**: the runtime emits `persons[].region` (a named
  frame region), and the graph knows which regions are restricted
  (`zones`, `zones.path`, and the rule DSL's `zone.pathPrefix`). The breach is
  `any(person.present && zone(region).restricted)`. Asking a model to make it
  means asking it to do a join we already do exactly.
- **`afterHours`**: the site's schedules are site data, resolved against the
  site's timezone. `ReplayInputs.InSchedule` (`incidents/replay.go:69`) exists
  precisely so the decision is made on stored facts. A model's opinion of "after
  hours" is not reproducible and is not the site's definition.

**What must exist:** the runtime (or the platform, deterministically, between
the runtime's response and the observation record) computes both and the profile
schema declares them as `readOnly`/derived rather than model-emitted. The shipped
rule already computes the schedule condition deterministically
(`rules/builtin/bundle.yaml:84-86`, `schedule: {id: business-hours, invert:
true}`), so the model-emitted `afterHours` is currently redundant *and*
authoritative-looking. Tier C questions with wrong inputs is a category of bug
this product will otherwise ship.

### 3.5 A finding about `summary`

`summary` is a field in all three profiles' `Fields` maps
(`observations.go:79, 87, 95`) and therefore passes
`validateAgainstProfile` (`:366-384`). `rule-bundle.schema.json`'s
`aiObservation` condition has no operator restriction — only `equals`. **A rule
can therefore write `aiObservation: {profileId: restricted-person, field: summary,
equals: "..."}`.** The `restricted-person` schema's own description says summary
is "Human-facing only. **Never evaluated by a rule**", and
`product-and-backlog.md` §3.4 says the same. **Nothing enforces it.**

This is an invariant-2 hole with a specific failure shape: an incident appears or
disappears because a prompt was rephrased, and the replay from stored facts
reproduces the *stored* answer, so the mismatch is invisible to replay and shows
up only as an unexplainable change in incident volume. **What must exist:** the
semantic validator refuses `field: summary` (and any field whose schema
description marks it non-evaluable) with a specific error code. A better design
is a per-field `ruleEvaluable: true|false` in the profile schema and a validator
that reads it, so the property is data rather than a hard-coded name.

### 3.6 The ten profiles

Proposed, ordered by the value they add against the cost of collecting their
labels. "Gate n" is the minimum labelled cases for that profile's gate
(§3.7's derivation applies to all of them).

| # | Profile | Question it answers | Schema sketch | Hard cases | Known failure modes | Gate n |
|---|---|---|---|---|---|---|
| 1 | `restricted-person` **(exists, production)** | Is there a person in a restricted zone, and what are they doing? | see §3.2 | Partial occlusion; a person behind glass; a mannequin, a poster, a shadow; a person legitimately present; IR footage at night | Hallucinating a person from a shadow; reporting `restrictedZoneBreach` from a region the runtime mis-segmented; confidence inflation on ambiguous frames | 200 |
| 2 | `door-forced` **(exists, shadow)** | Is this door forced, propped, or merely open, and is anyone there? | `doorState`, `personPresent`, `obstruction`, `openDurationSeconds` | A door held by a box; wind; a door whose contact chatters; a camera that cannot see the latch | **A `door-forced` model is dangerous**: a REX contact is Tier A and a model guessing at it is a fabricated Tier A. The model should be scoped to *corroborate* a contact, never to assert it | 200 |
| 3 | `vehicle-at-gate` **(exists, shadow)** | Is a vehicle at the gate, is the plate legible, and what is the gate doing? | `vehicleState`, `vehicleCount`, `plateLegible`, `plateText`, `gateState` | Night, rain, mud, a partially occluded plate, a trailer, a bus reading as several vehicles | Plate hallucination is the single worst failure in physical security — a wrong plate sends an armed response to the wrong car. **`plateText` must never be authoritative and never a basis for a gate action** | 200 |
| 4 | `abandoned-object` | Is there an unattended object, and for how long? | `objectPresent`, `objectClass`, `confidence`, `presentSinceSeconds` | A trolley that lives there; a bench; a bag against a wall with a person nearby | Timeout "confidence" that is really a person leaving frame. The false-positive rate on this class is the highest of the ten | 300 |
| 5 | `loitering` | Is a person remaining in a zone longer than the site permits? | `personPresent`, `dwellSeconds`, `moving` | A guard on a fixed post; a smoker; someone sheltering from weather | A dwell threshold is a *site policy* input; a model cannot know it. The model measures, the rule decides | 300 |
| 6 | `crowd-density` | How many people, and is it unusual for this zone at this hour? | `personCount`, `zoneRegions[]` | Partial counting at the frame edge; double-counting a reflection; a mirrored wall | A count that is wrong by 20% is operationally useless. The output is a *band* (`low/normal/high` relative to the site's own history), not a number — and that needs M6 baselines, so this profile is **M6-gated, not M2** | 400 |
| 7 | `intrusion-confirmed` | Does the imagery corroborate an intrusion sensor that fired? | `personPresent`, `confirmed`, `region` | A sensor triggered by an animal, a tree, a vehicle | This profile's job is *corroboration of a Tier A signal*, which makes it the second-best use of the plane after #1: when the sensor is right, the model confirms; when the model says "no person", the incident degrades to UNVERIFIED and never suppresses | 200 |
| 8 | `perimeter-breach` | Is there a person at the fence line, and are they on the site side? | `personPresent`, `side` (`inside`/`outside`/`unknown`), `climbing` | A person legitimately on a walkway outside the fence; foliage movement; a shadow that crosses at exactly the right height | `side: unknown` must be a real, frequent outcome. A model that always picks a side is worse than one that admits it cannot | 300 |
| 9 | `line-crossing-direction` | Which way did the object cross the line, and what was it? | `objectClass`, `direction`, `crossed` | A person who crosses back and forth; two people crossing together | Moot if Profile M metadata is available — this profile exists for cameras that publish no analytics | 300 |
| 10 | `evidence-quality` | **Is this frame usable at all?** Blur, obstruction, darkness, frozen image, lens fog | `usable`, `defects[]` | A legitimately dark scene; a privacy mask; a scene change | This is the profile that makes the other nine honest. A model that cannot see anything should say so, and every other profile's `insufficientEvidence` rate is only interpretable next to this one | 150 |

**Recommendation on ordering:** build #1, #7, #4 and #10 first. #1 is already
half-built. #7 is the highest Tier A corroboration value per label collected
(the sensor produces the label for free — the operator's disposition of a
sensor event is the ground truth). #4 is the most common real-world incident
class after restricted-zone. #10 is cheap, and it is what lets every other
profile's abstention be trusted. #3 stays in shadow longest, because plate
hallucination is the one failure in this list that could get someone hurt.

**The gap to record:** `../master-roadmap.md` §M2's "ten profiles" is not a
specification, and §3.4 above is a proposal that needs E1's signature. It should
be reconciled into `product-and-backlog.md` §2.2 as ten named rows before M2
work starts, or "ten profiles" will be read as "whatever we get to."

### 3.7 Promotion sequence and the version-change rule

**The sequence, which is `../master-roadmap.md`'s and `../security/trust-and-security-program.md`
§4.2's and is not negotiable:**

```
dataset  →  offline eval  →  shadow  →  canary  →  production
```

| Stage | What runs | What may affect state | Exit condition |
|---|---|---|---|
| **dataset** | The profile is specified; a labelled corpus exists, versioned and anonymised | nothing | The corpus is committed with a content hash and a `Covers` list |
| **offline eval** | `evaluation.Evaluate(cases, observations, gates)` over the corpus | nothing | `report.Passed` with **every** gate named, and the denominators printed |
| **shadow** | The profile runs on live traffic; the row is written with `production_profile = false` | nothing — `IsProductionProfile` (`:102-105`) is what guarantees this, and `Writer.Record` writes `!profile.Shadow` into the column (`:301`) | The shadow-period report is attached. `ShadowGates()` (`:116-120`) only lowers `MinLabelledCases`, deliberately: "the point of shadow evaluation is to collect cases, not to promote" |
| **canary** | One site, one zone, a percentage of events | only in the canary scope | No regression against the four companion metrics (`metrics-and-analytics.md` §6) |
| **production** | Full scope | yes | The M1 gate-4 evidence: 14 consecutive days in shadow with the evaluation report attached |

**On a model or prompt version change — the rule and the mechanism.** A change to
the model, the weights, the prompt template or the prompt version produces a
**new observation row, never an edit of an old one**
(`observations.go:142-143`, `SupersededBy` at `:448-471`). Therefore:

1. The old profile version keeps running, or is explicitly retired, and
   **decisions that cited it still replay** — because `incidents.Replay` reads
   the stored `AIObservation`, not the current profile.
2. The change is a **new profile version** (the `Profile.Version` field exists at
   `:64`) and a **new dataset run**, not an increment. The old dataset does not
   validate the new prompt.
3. **Both versions are evaluated on the same corpus** and both reports are kept.
   A version change that improves the new version's numbers and is not compared
   against the old on identical cases has proved nothing.
4. The delta is queryable: `SupersededBy` gives the chain, `OutputHash` on both
   rows gives the content difference, and `DiffDigest` in `simulate.Report`
   (`:75`) is the same idea for rules.
5. A model change is an ADR, not a config push: `decisions-and-process.md` §2.4,
   `../security/trust-and-security-program.md` §4.3, and the hardware/cost implications in §10
   below.

### 3.8 The lifecycle, and the rule about demos

States: `proposed` → `specified` (schema published under `contracts/`) →
`implemented` (runtime produces it) → `shadow` → `canary` → `production` →
`deprecated` → `retired`. A profile may skip *implemented* only by not existing.
`Shadow: true` in `M1Profiles` is the `shadow` state expressed in code, and it is
the only state that is safe to run against live traffic without a gate.

**The rule: a profile is never promoted because a demo looked good.** The
enforcement already exists and is worth defending in code review —
`evaluation.Report.Promotable` (`:157`) says "Production promotion is a separate,
**human decision**; this says which ones are eligible." `Passed` is computed per
gate, never averaged (`:142-148`): "a profile that is precise and slow is not
half-passing." `evaluateGates` returns a list of named failures so "a promotion
decision reads as a list rather than as a boolean."

**Two harness defects that would let a bad profile through, and both are in the
evaluation programme's scope:**

- **The harness only scores `restricted-person`.** `predictedRestricted`
  (`:296-309`) hard-codes the field `restrictedZoneBreach`. For any other
  profile, `DetectedRestricted` and `MissedRestricted` stay 0, so
  `pr.Recall = rate(0, TruePositives) = 0`, and `evaluateGates` fails on
  `pr.TruePositives > 0 && pr.Recall < 0.70`. **No profile other than
  `restricted-person` can ever pass `Evaluate`.** With ten profiles planned and
  the M2 gate requiring "the M1 incident class plus two more", this is a
  blocker. **What must exist:** the scored field is part of `Case` (a
  `ScoredField` or a per-profile predicate), not a constant. This is the
  single highest-value change to the harness.
- **`DisagreementRate` leaks a count.** `pr.DisagreementRate++` at `:232` and the
  division at `:240-242` is guarded by `if labeledNonNil > 0`. When
  `labelpedNonNil <= 0` the field is never divided and is compared against the
  0.25 gate as a raw count. A single disagreement on a profile with no
  non-nil predictions fails the gate for the wrong reason.

---

## 4. The local LLM

### 4.1 Why it orchestrates and never asserts

The plan's phrase is exact: "The LLM orchestrates; it never asserts"
(`../master-roadmap.md` §M2 scope 3). The three roles:

| Milestone | Role | What it may produce | What it may never produce |
|---|---|---|---|
| **M2** | Investigation orchestrator | A plan over cameras, alarms, time windows, access events, observations and history; a question set; an evidence-bound narrative | A verdict, a severity, a suppression, a state change |
| **M3** | Playbook runner | A proposal from the enumerated set: dismiss, monitor, escalate, request evidence, notify supervisor, move PTZ to an approved preset, extend recording, open an incident | An action that was not authorised by policy. `action.proto`'s `ActionStatus` starts at `REQUESTED` and the policy engine decides before `DISPATCHED` |
| **M7** | Incident agent | A continuously updated timeline and evidence set, checked against new events | A closing decision outside policy, and never a physical action (§9.4) |

**The structural reason, which is the same reason the AI plane is a process and
not a library:** a verdict is a stored fact that replay must reproduce. A plan is
a proposal that replay does not reproduce, because the plan was never the
decision. If the model produced the verdict, invariant 2 would be a claim about
the model rather than a property of the system.

### 4.2 The plan/act/tool interface

The interface must be **structured tools, not free text**, and the reason is
`AGENTS.md`: rule bundles, release archives and model bundles are untrusted input.

```
Tool: plan_evidence(incidentId, hints[], budget)  → EvidencePlan{ windows[], cameras[], accessRefs[] }
Tool: fetch_evidence(evidenceId)                 → { sha256, capturedAt, cameraId }
Tool: read_observation(observationId)             → { profileId, modelHash, promptVersion, structuredOutput }
Tool: read_graph(nodeType, nodeId, depth)         → { nodes[], edgeIds[] }   // graph.Walk, already bounded
Tool: read_history(incidentId|entityId, window)   → { events[], dispositions[], observations[] }
Tool: propose_action(actionType, target, rationale) → { requestId }           // enters the policy engine
```

Four properties, each from something real in the tree:

1. **Every tool returns stored facts with their references.** `graph.Walk` already
   returns `Node{Type, ID, EdgeIDs}` with `Truncated` and `VisitedAtDepth`
   (`graph.go:212-217`, `233-239`); `observations.Get` returns the full
   attribution; `evidence` rows carry `sha256`. A tool that returned prose
   instead would make "which evidence supports this" unanswerable.
2. **Bounded, and the bounds are already written.** `Traversal.MaxDepth` 6,
   `MaxNodes` 2,000 (`graph.go:259-266`); `evidence.DefaultMaxBytes` 512 MiB
   (`store.go:74`); the runtime's request cap 32 MiB (`runtime.py:420`). An
   unbounded tool is a way for the model to consume the database.
3. **`propose_action` is the only write path, and it is a request.**
   `action.proto` has `requested_by_actor`, `idempotency_key` and
   `ActionStatus_REQUESTED`. A model with a write path is not an orchestrator.
4. **No unconstrained free text reaches an operator.** Every operator-facing
   string the model contributes is either a `summary` field (≤1000 chars, in the
   profile schema, marked "never evaluated by a rule" — and §3.5 says that
   marking must be enforced) or nothing. `ux-and-design-system.md` §6.1's
   "attribution present, ranked, never buried" is the UX expression of the same
   rule.

### 4.3 Failure modes and mitigations, per mode

| Failure mode | What it looks like | Mitigation | Where the mitigation lives |
|---|---|---|---|
| **Hallucinated correlation** | "These two events are the same incident" with no shared fact | Every claim binds to an evidence reference; a claim with no reference is not rendered. Reuse `EvidenceEdge`-style binding (`edge.proto:46-51`): the claim carries `evidence_id` and `observation_id` | M2, and `metrics-and-analytics.md` §6's guardrails |
| **Confabulated timeline** | Events ordered by the model's belief about sequence rather than by `normalized_at` | The timeline is rendered from stored events, sorted by `normalized_at`; the model may *narrate* the timeline and may not *order* it. `normalized_at` is the only field a window may read (`semantic.go:302-313`) — the same rule | Already enforced in the rule engine; the LLM must not be a way around it |
| **Over-broad plan** | "Check all cameras on site 3 hours either side" | Hard budget on tools per invocation; `graph.Walk` bounds; evidence-fetch budget in the plan schema; every fetch audited as `audit.ActionEvidenceFetched` | M2 |
| **Prompt injection from a rule bundle** | A bundle's `description`, `title` or `dispatch.target` contains text addressed to the model | **Bundles are untrusted input** (`AGENTS.md`). The rule DSL has no free-text field that reaches a prompt; a bundle's strings are operator-authored metadata, not model input. Any M3 change that puts bundle text into a prompt is an ADR with a threat-model review | `../security/trust-and-security-program.md` §3.7 |
| **Prompt injection from a model bundle** | The signed bundle's manifest or config carries instructions | The bundle is verified before load (`runtime.py:238-280`) and its contents are weights plus a declared prompt id/version — never free text consumed as instructions. **Fix F2 first**: until the signature is real, a bundle that passes verification is a bundle an attacker with write access to that directory authored | §1.5 (F2) |
| **Prompt injection from an operator note** | An operator types "ignore your instructions" in a disposition note | `disposition.proto`'s `notes` is a string with no privilege. The rule is: **no field of any record is model input except through a tool that returns stored facts with references**, and notes are not among them. Dispositions feed the LLM as *data* (an outcome enum) and never as text | M2/M3; `product-and-backlog.md` §3.7's customer-commitment template is the wrong place, this is an ADR |
| **Model asserts what a Tier A fact already says** | "I think the door was open" when the contact says it was | The model sees Tier A facts as facts. Any model statement contradicting a Tier A fact is dropped and the contradiction is recorded | M2 |
| **Model answer becomes authoritative by accident** | A rule is added matching on the model's narrative | `aiObservation.field` must be a schema-declared structured field; `summary` is not one — and §3.5 says nothing enforces that yet | §3.5 |

---

## 5. Evidence intelligence (M2)

`../master-roadmap.md` §M2 scope 1: "the platform identifies relevant cameras,
retrieves the right time windows across them, pulls snapshots and clips, gathers
metadata, and correlates neighbouring cameras, access events and sensor events.
The operator stops searching cameras by hand."

### 5.1 The blocker, stated plainly

**No evidence retrieval exists.** The adapter has no method that returns bytes;
`routes.go:67-69` names `ImagePath` and `VideoRecordingStartTime` as *assumed*
fields and nothing reads them; `evidence.Store` exists and is carefully built but
nothing in the tree calls it from a source. `integrations-and-adapters.md` §4.3
treats this from the integration side. From the AI side it is worse, because
M2's first deliverable depends on it and it is not in the M2 scope list as a
build item — it is in the adapter's scope list as an assumption.

### 5.2 Retrieval design

| Source of relevance | Mechanism today | What must exist |
|---|---|---|
| **Graph topology** | `graph.Walk` from an INCIDENT node over `has_evidence` → `supported_by` → EVENT → `observed_by` → DEVICE, bounded to 6 hops and 2,000 nodes | A query that walks *backwards* (DEVICE → events) — `walkSQL` (`graph.go:241-256`) is a forward recursive CTE from `(from_type, from_id)`, so device→events is a second walk with the relations inverted, or a materialised adjacency |
| **Time windows** | `normalized_at` is the only field a window may read (`semantic.go:302-313`); the index exists: `canonical_events_site_time_idx (tenant_id, site_id, normalized_at)` and `canonical_events_device_time_idx (tenant_id, site_id, device_id, normalized_at)` | A retrieval window expressed in the same terms: `[normalized_at − pre, normalized_at + post]` with the site's `skewTolerance` from the bundle's `settings.clock` |
| **Neighbouring cameras** | **Does not exist.** The M1 graph has `ZONE → contains → DEVICE` and `DEVICE → kind → camera`, and camera *adjacency* and field-of-view relationships are an **M6** item (`../master-roadmap.md` §M6 item 1) | Either pull adjacency forward to M2 — it is the single highest-value retrieval input and M6's "site model" should not be a prerequisite for it — or derive a first approximation from shared zone + `site-config`'s free-form placement. **Recommendation: pull adjacency forward.** M2 cannot deliver its exit gate without it, and M6 will then extend rather than create it |
| **Access and sensor correlation** | `access.granted` / `access.denied` are in the canonical vocabulary; `Entity` with `ENTITY_KIND_BADGE` exists; there is no `access.forced` | Event retrieval by `entity` value inside the window. The `entities` table's `entities_time_idx (tenant_id, site_id, last_observed_at)` and `entities_device_idx` exist |
| **Evidence already attached** | `incident_evidence` and `evidence` tables; `Evidence.tier`, `sha256`, `retain_until`, `legal_hold` | Completeness computation (§5.5) |

### 5.3 Latency and cost budget (ESTIMATE)

| Item | Budget | Basis |
|---|---|---|
| Evidence *discovery* (graph + query planning) | ≤ 50 ms p95 | Two indexed queries and one bounded CTE; `Walk` is already a recursive CTE with a `LIMIT` |
| Snapshot fetch (one camera, one frame) | ≤ 2 s p95 | One vendor round trip on the site LAN |
| Clip fetch (one camera, 30 s window) | ≤ 8 s p95 | Streaming or a server-side clip; the cost is the VMS, not us |
| VLM inference on ≤ 8 frames | ≤ 8 s median | **This is the product's own gate**: `MaxMedianLatencyMS: 8000` (`evaluation.go:107`), with the stated reason "it is on the p95 incident-visibility path, and a profile that adds thirty seconds to every incident is a profile operators learn to work around" (`:85-87`) |
| Total time from event to a viewable incident | ≤ 30 s p95 | The operator-experience number. **ESTIMATE — must be measured**, and `release-sre-and-support.md` §5's instrumentation gap means nothing is emitted today |
| Evidence bytes per incident | ≤ 50 MB ESTIMATE | Snapshots dominate; clips are bounded by a site retention policy (`../security/compliance-and-certifications.md` §7.3) |
| Cost | **$0 to us per site** | Inference and storage are the customer's hardware (`../company/financial-plan.md` §6.1). Our cost is the shared evaluation footprint |

### 5.4 Caching

There is **no cache in the evidence path today** and that is correct. The
`evidence` table's `sha256` is content addressing, so re-fetching the same frame
returns the same row; a cache would be a second source of truth for bytes that
must be provable. The one legitimate cache is a *per-incident* short-lived
materialisation of what has already been shown to the operator, which is a
read-model concern (`internal/api/readmodels.go` is the pattern) and not an
evidence concern.

The one thing to cache: **retrieval plans**, not bytes. A plan is a pure function
of (incident, graph, window) and a stale plan is re-derivable.

### 5.5 Evidence completeness, and the rule about visibility

**Completeness** is the property M7's North Star metric carries as one of its
four companions (`../master-roadmap.md` §M7: "human attention minutes per 1,000
security events ... never reported without its four companions: missed-incident
rate, operator override rate, false escalation rate, **evidence completeness**").
It is defined as: of the evidence an incident's verification *referenced*, how
much is present, verified by hash, and still inside its retention window.

**The rule: a retrieval failure is visible and never silently empty.** This is
the same rule as `malformed` events being carried rather than dropped, the same
rule as `MalformedError` "never swallowed: a feed that silently parses to zero
events looks exactly like a site with no activity" (`routes.go:184-186`), and the
same rule as `readmodels.ErrNotWired` — "an empty list and a broken list look
identical to an operator, and only one of them is a lie" (`readmodels.go:28-30`).

Therefore, concretely:

- A retrieval attempt that fails produces a row recording the attempt, the
  target, the reason and the time. It is not a log line.
- An incident whose retrieval partially failed carries a visible completeness
  marker, and the operator surface renders it — `ux-and-design-system.md` §5's
  "AI unavailable" state is the design precedent and the copy pattern already
  exists.
- A retrieval that returns nothing because nothing matched is **not** a failure
  and must not be rendered as one. This is the same three-way distinction as
  no-observation / degraded / insufficient.
- Every retrieval is audited: `audit.ActionEvidenceFetched` and
  `ActionEvidenceExported` (`audit.go:66-67`) with `fetched_by_actor` on the
  `Evidence` row (`evidence.proto:20`).

---

## 6. Semantic search (M2)

### 6.1 The rule, from the roadmap

`../master-roadmap.md` §M2 scope 4: "`pgvector` for derived data only. PostgreSQL
stays authoritative." `../strategy-and-north-star.md` §6's "no new runtime, no new
datastore, no new protocol family without an ADR" makes pgvector itself an ADR
candidate — it is a Postgres extension, so it does not violate the letter, but it
is a new dependency on a customer's database and `release-sre-and-support.md`
§3 (offline and air-gapped) makes every extension a deployment problem. **That
ADR is not written and should be, before the extension is added to a customer
site.**

### 6.2 What gets embedded, and what must never

| Data | Embedded? | Reasoning |
|---|---|---|
| Incident title, description, the operator's disposition `notes` | **Yes** | Derived text, tenant-scoped, and the operator wrote it about the incident. This is the corpus that makes "find me the incidents where a delivery driver was let in" work |
| Structured facts rendered to text: device display names, zone paths, event-type sequences | **Yes** | Deterministic, no raw media |
| Evidence **metadata** (kind, tier, sha256, camera, window) | **Yes** | Addressing, not content |
| `ai_observations.structured_output` | **Yes, structured fields only** | Never `summary` prose (AGENTS.md: "Golden tests are deterministic-only... Never golden-test VLM prose"; the same logic applies to embeddings) |
| Audit and disposition **reason codes** | **Yes** | Enums, cheap, high-signal |
| **Raw footage, snapshots, clips** | **Never** | Not a cost decision. This is a tenant's camera footage and a plate is personal data under several regimes (`../security/compliance-and-certifications.md` §3.1, §3.2) |
| **Licence plates** (`vehicle-at-gate.plateText`) | **Never as a searchable vector** | Stored as an `Entity` observation with an exact-match index if at all. Embedding a plate creates a derived biometric-adjacent representation that no retention policy currently covers |
| **Credentials, badge numbers, tokens, secrets** | **Never** | The fixture sanitizer already strips them from committed data (`fixture/sanitize.go`); the same allow-list discipline applies here |
| **Person imagery, face crops, re-identification artefacts** | **Never** | `graph.go:16-22`'s no-person-model decision extends to derived data. An embedding of a person is a person model |

### 6.3 The re-embedding problem

An embedding model change invalidates every stored vector. The strategy, and it
is the standard one for a local-first product with an offline install:

1. **Two generations, side by side.** `embed_model_id` on every embedding row; a
   new model writes generation *N+1* while *N* continues to serve. There is no
   window where search is broken, because there is no window where a single
   column was rewritten in place.
2. **Backfill is a job, and it is bounded.** `jobs.QueueRuleBulk` is the wrong
   queue; a new `embed_backfill` queue with its own lease duration and retry
   bound is right, and `job.proto` supports it (a new `JobQueue` enum value is a
   contract change and goes through `contractcheck`).
3. **Nothing is deleted until the new generation has served and the old
   generation's rows are outside every retention window.** Deleting a vector
   early is a records-retention event, not a maintenance task.
4. **A search that cannot serve generation N must say so**, never quietly return
   generation N+1 results labelled as N. Same visibility rule as §5.5.
5. **The model is a signed bundle**, verified by the same mechanism as the VLM
   model bundle — which means F2 must be fixed first, or the embedding model's
   provenance claim is as weak as the VLM's.

### 6.4 Every result carries an evidence reference

Non-negotiable, and it is the same rule as M2's exit gate line "Every search
result and every summary carries resolvable evidence references"
(`../master-roadmap.md` §M2). A vector-similarity hit on an embedding is not a
result; it is a pointer. A result is a pointer plus a reference that resolves to
a row an operator can open, which for a search hit means at minimum the incident
id and the evidence ids that justify the embedding's presence. A result whose
references do not resolve is a defect, and the test is: for every row the search
returns, the reference resolves.

---

## 7. Incidents, summaries, and the triple (M2 / M6)

### 7.1 What is stored, derived, and never stored

| | Stored | Derived | Never stored |
|---|---|---|---|
| **Incident** | `incident.proto`: ids, rule, bundle version, content hash, policy version, state, severity, title, first/last event times, `degraded`, `degraded_reasons`, `evidence[]` | — | The model's prose as the incident's meaning |
| **AI observation** | `observation.proto`: the full attribution and the validated structured output | — | Any edit to an existing row; a re-evaluation is a new row |
| **Disposition** | `disposition.proto`: actor, roles, site scope, outcome, reason code, notes, `agreement`, `related_observation_id`, session | — | Anything that overwrites the observation. The proto's own comment: "It never overwrites the AI observation: 'AI said X' and 'operator said Y' are both stored and both replay" |
| **Summary** | — | A rendered view over incident + events + observations + dispositions. Marked derived; recomputable; carries `replayedFromStoredFacts` semantics if it ever appears in an explanation | As an authoritative field, and **never** as a golden-test fixture |

`evaluation.Case` already reads the operator's disposition as the label
(`evaluation.go:21-35`), and its `OutcomeUndispositioned` case is handled
carefully: "A case nobody judged is not evidence, and counting it as anything is
how a harness makes a profile look better than it is" (`:32-35`).

### 7.2 The triple

`(AI said, operator said, final outcome)` per class and per site, which
`../master-roadmap.md` §M6 item 4 makes the feedback loop:

| Element | Where it lives | Notes |
|---|---|---|
| **AI said** | `ai_observations.structured_output` + attribution | Immutable, per run, supersession-linked |
| **Operator said** | `dispositions.outcome` + `disposition.agreement` (`AGREED`/`DISAGREED`/`UNSURE`/`NO_AI_OBSERVATION`) + `notes` | `NO_AI_OBSERVATION` is a first-class answer, not a missing one |
| **Final outcome** | The incident's terminal state and, where it exists, the customer's own record | The third element is the one we do not own, and `../security/compliance-and-certifications.md` §7.4 ("the test of whether it is really the customer's record") applies |

### 7.3 How feedback becomes evaluation data without becoming a training pipeline

**The rule: feedback becomes labelled cases, and labelled cases are the only
thing a promotion decision reads.** It never becomes gradient signal, and we do
not train foundation models (`../strategy-and-north-star.md` §7).

The mechanism, which is already half-built:

1. `dispositions.outcome` is the label. `evaluation.Outcome` already encodes the
   four cases: `true_positive`, `false_positive`, `benign`, `undispositioned`.
2. A case is a `(event, profile, observation, label, hoursToDisposition)` tuple —
   which is `evaluation.Case` (`:38-57`) exactly. `HoursToDisposition` is in the
   struct on purpose: "a profile that is accurate and costs twenty minutes of
   operator attention per incident has not earned its place" (`:53-56`).
3. Cases are **versioned and anonymised** — the same discipline as fixtures
   (`fixture.Scenario.ContentHash`, `FormatVersion`, `ManifestEntry.Covers`) and
   the same customer-data rules (`quality-engineering.md` §8.3).
4. `metrics-and-analytics.md` §5.2's 2×2 (agreement vs operator outcome) is the
   reporting form.

**What the triple must never become:**

- **A feature store.** A triple table that the runtime reads at inference time
  is a feedback loop where the model's past answers change its future answers
  without a gate. Inference must be a pure function of (evidence, prompt, model).
- **A ranking signal.** "The operator agreed last time" is a *measurement*, not
  a prior. Sorting a queue by it is a product decision with a fairness
  consequence (early-dispositioned incidents are the easy ones).
- **Training data.** The M1 decision not to train is a company-level decision
  (`../strategy-and-north-star.md` §7) and it is also the *cheapest* one: a training
  pipeline at two people is a second product. A per-site fine-tune is explicitly
  out of scope and is also where the privacy regimes bite hardest.

---

## 8. Site intelligence (M6)

### 8.1 The product requirement, and it is not a nuance

`../master-roadmap.md` §M6 item 2: the output is *"differs significantly from this
site's baseline"*, **never** *"abnormal = dangerous"*. "The distinction is a
product requirement, not a nuance."

That means the type system must make the illegal answer unconstructible. Proposed
shape:

```
Baseline        : per (tenant, site, zone, metric, stratum) → distribution over a window
Deviation       : observed vs baseline → a *deviation score* and the stratum it was computed against
Anomaly         : deviation score over threshold → "unusual for this zone, at this hour, on this weekday"
Consequence     : NEVER derived from Anomaly. Only from a rule.
```

There is no type that means "dangerous", no proto field that carries it, and no
API response that returns it. `incidents.proto` has `Severity`, and severity is
set by `then.severity` in an activated rule bundle — a fact already stated in
`integrations-and-adapters.md` §3.4 and re-stated here because it is the M6
version of the same rule. **What must exist:** a review rule that no code path
computes a severity from a baseline, a drift score or an anomaly.

### 8.2 The flow, and it is fixed

`../master-roadmap.md` §M6 item 6: "the flow is always: proposal → simulation →
historical replay → impact diff → human approval → activation. **The AI never
activates its own rule.**"

Every stage already has a mechanism:

| Stage | Mechanism that exists | Mechanism that must exist |
|---|---|---|
| proposal | An `IncidentClass` with counts and a suggested predicate | The proposal format. It is **not** a bundle — it is a structured description that compiles to a bundle, so the proposal itself is not code |
| simulation | `simulate.Runner.Run` + `simulate.Diff` + `DiffDigest` over the change set (`simulate.go:159`, `279-311`) | Running it over the site's full history rather than the fixture set |
| historical replay | `incidents.Replay` and `ReplayInputs` (`incidents/replay.go:55-70`) | The same, at site scale, and the `Matches`/`Mismatch` fields must be surfaced rather than logged |
| impact diff | `simulate.Change` with `ChangeKind` ranked "created, closed, severity_changed, degraded_flag_changed, verification_state_changed, no_change" (`:31-38`) | The exact presentation an operator approves against. `ux-and-design-system.md` owns it |
| human approval | `bundles.Activate(ctx, tenant, version, diffDigest, actor)` (`bundles.go:225`) — activation **requires the digest of the diff the operator reviewed** | — |
| activation | `bundles.Rollback` (`:293`) and the immutable `RuleBundle` proto | — |

`bundles.Activate` taking a `diffDigest` is the mechanism that makes "the AI
never activates its own rule" structural rather than aspirational: **the operator
must hold the digest.** And because the digest is computed over the *diff*
(`simulate.go:75-76`), a fixture set or an active bundle that moves after the
review invalidates the acknowledgement. This is one of the best pieces of design
in the tree and M6 should not weaken it.

### 8.3 Statistical honesty — the part that is genuinely hard

| Problem | Why it bites | What must exist |
|---|---|---|
| **Minimum data volume before a baseline is trusted** | A baseline built on 3 days of data is not a baseline. It is a copy of last week | A minimum-observation gate per (zone, metric, stratum) with the count **stated in the report**. Below it: "insufficient history", not "normal". Reuse the three-absence shape |
| **Seasonality** | A loading bay at 06:00 on a Monday in December and in July are different distributions | Strata of (hour-of-day, day-of-week, month-band) with a minimum per stratum. A metric with no populated stratum is unavailable, not zero |
| **Concept drift** | The site's activity changed because the site changed — a new tenant, a new shift, a moved camera | `../master-roadmap.md` §M6 item 5 already lists the drift signals. The detection needs a change-point test on the baseline itself, and the response is a *re-baseline proposal*, which goes through the same approval flow as a rule |
| **The false-anomaly problem** | With enough dimensions, something is always unusual. A per-(zone, hour, weekday) test on 500 zones at a 1% threshold produces ~5 flagged zone-hours a day, all noise | Multiple-testing discipline: a stated false-discovery budget, and — the cheap, honest answer — **an anomaly that nothing acts on is not an alert, it is a dashboard**. The exception queue is the only surface anomalies reach, and only via a rule |
| **Feedback loops** | Operators dismiss anomalies; dismissals train nothing (no training) but they do change what gets *shown* | The triple (§7) makes dismissal countable. `metrics-and-analytics.md` §5.4's replay-audit approach is the check on whether a site is being shown anomalies that are always wrong |
| **Baseline poisoning** | A model's early mistakes get recorded as normal | Baselines are computed from **Tier A and Tier B facts only** — events, clock states, access grants, sensor transitions. Never from AI observations. This is the one hard architectural rule in M6 |

**Camera health** (`../master-roadmap.md` §M6 item 7) belongs here and is the
cheapest item in the milestone: "the target state is not 'camera online' but
'camera operationally useful'". `camera.proto` already has the vocabulary
(`CameraState`, `width`, `height`, `low_light_capable`) and profile #10
(`evidence-quality`) in §3.6 is the AI component. **What must exist:** the
health signal for blur, frozen image, low FPS and obstruction, computed
deterministically first (a frame-identical check is free and catches most frozen
cameras) with the model only for what is left.

---

## 9. Autonomy (M7)

### 9.1 The levels, and what is permitted at each

`../master-roadmap.md` §M7 item 1 names A0–A4 and the act of raising a level:
"explicit, audited, reversible act with a blast-radius preview."

| Level | Name | May do | May not do |
|---|---|---|---|
| **A0** | Observe | Record, correlate, surface, explain. Nothing else | Change any state |
| **A1** | Assist | A0, plus: propose, recommend, pre-fill a disposition for one-click acceptance | Accept its own proposal |
| **A2** | Recommend | A1, plus: rank, batch, deduplicate the queue, suppress *only* with an operator's standing rule that was activated by a human | Suppress without a human-activated rule |
| **A3** | Execute with approval | A2, plus: dispatch actions — but every one lands in an approval queue and nothing executes without a human click | Dispatch an action whose physical effect is in a customer's building without approval (see §9.4) |
| **A4** | Policy-controlled autonomy | Execute within a policy the customer authored, within the blast radius the customer previewed and approved | Exceed the policy; act on a class the policy does not name; act when a companion metric has regressed |

**The escalation is not a permission escalation. It is a removal of a human from
a loop that the *policy* has replaced.** A0–A2 are about what the system
proposes; A3 is about a human in the dispatch path; A4 is a policy in its place.
Only A3→A4 is a genuine autonomy change, and it is the one that needs the
blast-radius preview, the audit and the rollback.

### 9.2 `internal/policy` DOES NOT EXIST — and it is a first-class M3 deliverable

`policy.proto` exists (`Policy`, `RuleBundle`, `RuleEvaluationMode`) and
`migrations/0005_ai_and_policy.expand.sql` creates a `policies` table. **There is
no `internal/policy` package.** `rg 'internal/policy'` returns nothing; the
`internal/` tree has 30 leaf packages and none of them is `policy`.

This is the most consequential structural gap in the plan after F1, because:

- M3's recommendation engine depends on it: "The policy engine decides what is
  permitted, and a proposal the policy rejects never reaches an operator"
  (`../master-roadmap.md` §M3 item 2).
- M3's safe actions depend on it: "Everything with physical impact on the world
  stays approval-gated."
- M6's AI-proposed rules depend on it.
- **All of M7 depends on it.** A4 is "policy-controlled autonomy". There is no
  policy engine, so there is no A4, so there is no M7.

`product-and-backlog.md` §2.3 calls the policy work "partial (policy table
exists)", which understates it by an order of magnitude: what exists is a table
and three proto messages. **The policy engine is a build item and it is currently
in nobody's backlog.** It should be an M3 backlog row with an owner and a date,
before M3 starts, not discovered in M3.

**What the policy engine must be** (a shape, not a code proposal):

```
Decide(subject, action, target, context) → { allowed | requires_approval | denied, reason, policyVersion }
```

- Inputs: the principal or agent, the action type, the target, the site's
  autonomy level, the incident's class and severity, the blast radius.
- Output: a decision carrying a **reason** and the **policy version** that
  produced it, because the reason is what `incidents.Replay` must reproduce
  (invariant 2) and the version is what `incident.proto`'s `policy_version`
  already stores.
- It must be **deterministic and replayable**, and the same discipline as the
  rule engine applies: no shell, no SQL, no network, no dynamic code, no
  unbounded regex. A policy is untrusted input in exactly the sense
  `AGENTS.md` gives bundles.
- It must be **the only writer of `ActionStatus.DISPATCHED`**. A component that
  can dispatch without consulting it is a back door, and the review rule is a
  `go/ast` test: nothing outside `internal/policy` constructs
  `ACTION_STATUS_DISPATCHED`.

### 9.3 The closed loop, and action-outcome verification

`../master-roadmap.md` §M7 item 3: "detect → verify → act → observe outcome → verify
the action succeeded → audit. **An action that is not verified as having
succeeded is a failed action, not a completed one.**"

`action.proto` already has the vocabulary: `requested`, `dispatched`,
`completed`, `failed`, `deduped`, with `requested_at`, `completed_at`,
`idempotency_key` and `error_detail`. The missing piece is `failed` being
distinguishable from "unverified", and the requirement that an unverified action
**reverts to a retry or escalates** rather than sitting in `dispatched`.

**The M3 gate line is the test**: "Every automated action is idempotent under
replay, proven by a fault-injection test at the action stage"
(`../master-roadmap.md` §M3). That is a `kill -9` test at the dispatch boundary
(`quality-engineering.md` §5), and the outbox already gives the primitive:
`outbox.Message.IdempotencyKey` is "required, not optional: a consumer cannot
dedupe what it cannot identify" and is tenant-scoped
(`outbox.go:36-44`; `docs/status.md` deviation 2). The relay gives at-least-once, so
the consumer must be idempotent — that is the design, and the fault-injection
test is what proves it.

### 9.4 The physical-action boundary

This is the most important paragraph in the AI track, and the rule is narrow.

**An action inside our system** — creating an incident, bookmarking, attaching
evidence, recording a disposition, posting a notification, changing a rule
bundle under an approved flow — is a state change in software we own. We can
authorize it, audit it, replay it and reverse it.

**An action in the customer's building** — releasing a door, arming or
disarming a zone, opening a barrier, dropping a lock, moving a camera to a preset
that a guard is watching, anything with a physical consequence — is not ours. It
is:

- **approval-gated, permanently.** Not "at lower autonomy levels". The M3 line is
  categorical: "Anything with physical impact on the world stays approval-gated."
- **mediated by the customer's own systems.** Our `Action` row records that we
  requested it, who authorised it, and that the customer's access-control system
  executed it. We do not hold the credential that opens the door; the customer's
  system does, on the customer's policy, and the customer's audit trail is the
  authoritative one. Where a VMS can be commanded directly (PTZ, bookmark), the
  action is a *request* to a device, and the device is on the customer's network
  under the customer's control.
- **reversible by the customer without us.** A customer must be able to revoke
  our ability to request a physical action, and the product must work — degraded
  and visibly so — when they do.

**Concretely forbidden, and these are review rules:**

1. A rule bundle may not name a physical action type. `rule-bundle.schema.json`'s
   `dispatch.type` enum is `notify_operator | notify_supervisor | request_vlm |
   fetch_evidence` (`:248`) and is closed. **Adding a physical action to that enum
   is an ADR with a threat-model review, and the default answer is no.**
2. No LLM tool may construct an `Action` with a physical target. `propose_action`
   returns a request; the policy engine and the approval queue decide.
3. No credential we hold may open anything. If we can unlock a door, we are the
   attack target and the customer's audit trail has a hole in it.
4. Every physical action request is audited with `requested_by_actor` and
   `idempotency_key`, and its outcome is verified the same way a software action
   is — which means a request that was refused by the customer's system is
   `failed`, not `completed`.

### 9.5 Failure taxonomy

| Failure | What it looks like | Why it is the severe one | Guardrail | Metric that catches it |
|---|---|---|---|---|
| **False suppression** | A real incident suppressed by the AI or by an autonomous action | The worst outcome. The event existed, the system decided it did not matter, and no human saw it. Directly the failure `docs/status.md` says the product must never have: "a stub returning `{\"restrictedZoneBreach\": false}` would produce a corroborated incident on a fabricated negative" | Suppression is a **rule action with a rule id**, auditable and replayable — never a model decision. A suppression rule must declare what it can suppress and be activated by a human. Tier C signals may *raise* an incident; they may not lower one. Reuse `VERIFY_STATE_SUPPRESSED` and make its production rate a monitored, alerted number, not a dashboard line | Override rate and missed-incident rate (`../master-roadmap.md` §M7's companions) |
| **Missed escalation** | A real incident that should have reached a human and did not | Second worst, and it is the *silent* one — nothing in the system reports it | Every incident class has a deterministic floor: the incident is created on Tier A/B facts alone, and AI *enhances* it. The shipped bundle already has this shape (`core.door-forced-after-hours`, "Needs no AI") | Missed-incident rate, measured by **replay audit** (`metrics-and-analytics.md` §5.4) rather than by the system itself |
| **Wrong action** | A permitted action that should not have been taken — PTZ to the wrong preset, notify the wrong person, extend recording on the wrong camera | Damage is bounded but real; blast radius is what makes it survivable | Policy engine, blast-radius preview before activation, `A3` approval gate, and `target_id` on every `Action` with a graph-resolved target rather than a string | False escalation rate |
| **Action not verified** | The system believes it did something it did not do — a notification that never sent, a bookmark that was never written, a PTZ move the camera rejected | The failure that makes the other three unrecoverable, because the system stops asking. It is also the most common | `ActionStatus` with an explicit unverified state; unverified reverts to retry or escalates; every action is verified by a **source-side** confirmation, not by our own dispatch succeeding | Evidence completeness and the action-completion rate |
| **(A governance failure) Autonomy raised without a measured baseline** | A4 enabled on a site whose companion metrics were never measured | The customer accepted an autonomy level they could not evaluate, and the product's own North Star claim becomes unfalsifiable | Blast-radius preview; autonomy change is audited, reversible, and requires the four companion metrics to exist *before* the change, not after | All four, plus an audit row |

---

## 10. The model abstraction layer

### 10.1 The rule

`../strategy-and-north-star.md` §7: "We do not train foundation models; we use the
best available models behind our own abstraction layer, and **the abstraction is
the asset**." The layer must be **model-agnostic and provider-neutral**, and the
operational test is: swapping a model changes a bundle's `prompt_version` and a
profile's `Version`, nothing else. No model name in a rule, no model name in a
policy, no model name in the API.

The evidence that the current design already satisfies this: `Observation`
carries `Model` and `ModelHash` as *data* (`observations.go:119-120`), the
runtime's `InferenceResult.to_response` is model-shaped but profile-shaped too,
and `incident.proto` stores no model reference at all — the model is reachable
only through the observation id.

**What must exist:** a `ModelProvider` interface the SDK exposes, with
`load(bundle)`, `infer(request, deadline)`, `capabilities()`. Today the runtime
*is* the abstraction (it is a separate process with an HTTP contract), which is
structurally right and should be kept — but the provider boundary needs to be
explicit so a second runtime (a different vendor's inference stack, or a CPU-only
path) is a new provider, not a fork of `runtime.py`.

### 10.2 Hardware profile (ESTIMATE)

| Profile class | Workload | GPU (ESTIMATE) | Latency (ESTIMATE) | Notes |
|---|---|---|---|---|
| **Frame classification** (`restricted-person`, `door-forced`, `evidence-quality`) | 1–8 frames, single forward pass, ~1–3 B parameters quantised | 8–12 GB VRAM (e.g. a workstation-class card) | 0.5–2 s per invocation | The M1/M2 class. A customer's back-room workstation qualifies |
| **Multi-object / analytics** (`vehicle-at-gate`, `crowd-density`, `loitering`) | 8–32 frames, more regions, higher resolution | 16–24 GB VRAM | 2–8 s | The M2 gate budget is 8 s median (`evaluation.go:107`) and this class is what consumes it |
| **Video-window reasoning** (M3–M5 orchestration over clips) | Short clip, temporal reasoning | 24 GB VRAM minimum; 40 GB+ for comfort | 10–60 s | Deferred. ESTIMATE |
| **Local LLM** (orchestrator, M2) | 7–14 B quantised, 8–32k context, tool calling | 16 GB VRAM shared, or CPU with unacceptable latency | 2–15 s per call | `release-sre-and-support.md` §9.2 sizes the site compute; this class shares the card with the VLM and the two contend. **The contention is the design problem, not the VRAM** |
| **No GPU** | — | — | — | The product must work. `../company/financial-plan.md` §1.3 says "the GPU is optional and its absence degrades verification quality rather than disabling the product" — which is invariant 3 restated, and it is correct |

**All figures are ESTIMATE and none has been measured on real hardware in this
tree.** They must be replaced by measurements from a real GPU before any of them
appears in a customer-facing document, per `../strategy-and-north-star.md` §8.

### 10.3 Latency and cost

- **Latency budget**: the product's own gate is `MaxMedianLatencyMS: 8000`
  (`evaluation.go:107`). Design to it, measure the median *and* the tail, and
  note that `ProfileReport` stores only the median (`median()` at `:319-330`) —
  **there is no p95 or p99 in the harness**, and for a GPU serving a 24/7 site
  the tail is what determines whether an operator experiences it as instant.
  **What must exist:** p95 latency in `ProfileReport` and in the metric, from M2.
- **Cost per 1,000 events (ESTIMATE)**: at the customer's hardware, **$0**. Our
  cost is the shared evaluation footprint — `../company/financial-plan.md` §6.1 puts it at
  ~$25/site/month, ESTIMATE. The honest framing for a customer is therefore *not*
  a per-event inference cost but the **GPU capital cost amortised against events
  per month**, which rises as a site's event volume falls. A small site with a
  quiet night is the worst case and it is a pricing question, not an engineering
  one (`../company/pricing-and-packaging.md` owns it).
- **A second cost that is not in any model**: *idle GPU power*. A 200 W card
  running 24/7 is a real line on a customer's bill and it is the objection that
  arrives in procurement. **What must exist before M2 pilots:** a duty-cycle
  policy (the plane sleeps when no incident is open) and a stated idle power
  figure.

### 10.4 Offline bundle install and signing

- A model bundle is a directory with `manifest.json` and `manifest.sig`
  (`runtime.py:220-284`), located by `SP_MODEL_BUNDLE` and verified at startup
  with `SP_MODEL_BUNDLE_SIGNATURE` / `SP_MODEL_BUNDLE_KEY`.
- A missing bundle is **not a startup error** (`build_runtime`, `:484-496`): "The
  plane starts, reports itself unavailable, and the core records a degraded
  observation — which is exactly what a site running without a model should see."
- **The signature is not actually verified. F2, §1.5.** Until that is fixed, a
  model bundle is tamper-*evidenced* against a digest, not authenticated, and no
  customer-facing document may claim otherwise. `../security/trust-and-security-program.md`
  §3.6's offline-verification property is the one to reuse.
- Offline distribution is `release-sre-and-support.md` §3.4's air-gapped
  problem, and it is a *release-pipeline* problem for model bundles specifically:
  they are large, they are vendor-specific, and they are not built by us. The
  ADR should decide whether a model bundle ships inside the release archive or
  beside it. **Recommendation: beside it**, because the release archive is
  signed once per build and model bundles change far more often.

---

## 11. The evaluation programme

### 11.1 Datasets

| Property | Requirement | Mechanism that exists |
|---|---|---|
| **Built from operator dispositions** | A label is an operator's judgement, never the model's or ours | `evaluation.Case.Outcome` (`:22-36`); `dispositions.outcome`; `OperatorAgreement` |
| **Undispositioned cases counted separately** | Never folded into a rate | `OutcomeUndispositioned`; `ProfileReport.Undispositioned` |
| **Anonymised** | No person images, no plates as images, no credentials | `fixture/sanitize.go`; `quality-engineering.md` §8.3 |
| **Versioned** | A dataset is a content hash, a version and a `Covers` list | `fixture.FormatVersion`, `Scenario.ContentHash()`, `ManifestEntry.Covers` — the same shape, deliberately |
| **Covered behaviours declared** | "A scenario whose purpose is unstated is a scenario nobody will notice has stopped testing anything" (`fixture.go:183-186`) | `Covers` |

### 11.2 The golden rule, restated

`AGENTS.md`: "Golden tests are deterministic-only: fixture to canonical to
verification to incident must be byte-stable. **Never golden-test VLM prose.**"
`quality-engineering.md` §4.2 has the reasoning and §7.3 has the boundary.

The operational statement, for this track: **a golden test may assert that a
profile was *invoked*, that its output validated against its schema, that the
observation row carries the right attribution, and that the rule's degraded
branch fired. It may not assert what the model said.** The `structured_output`
of a real model is not byte-stable and a golden test that pins it is a test that
fails on a prompt edit and teaches the team to re-baseline rather than to
investigate.

### 11.3 Offline vs shadow vs canary

| Stage | Traffic | Gate evaluated | State affected | Report attached to |
|---|---|---|---|---|
| **Offline** | none | `evaluation.Evaluate` over the corpus | none | The PR |
| **Shadow** | 100% of the production site's eligible events, `production_profile = false` | `ShadowGates()` — same rates, `MinLabelledCases` lowered to 10 because the point is collection | none | The promotion decision |
| **Canary** | one site, one zone, a stated percentage | The full `DefaultGates()` plus the four companion metrics | only the canary scope | The activation |
| **Production** | full | Continuous companion metrics | yes | Ongoing |

**Shadow mode is a *storage* fact, not a flag.** `observations.Record` writes
`production_profile = !profile.Shadow` into the column (`:301`) from the profile
set, not from the caller. An adapter or a rule cannot request a shadow
observation and get a production one. That is the right place for the property
and it should not move.

### 11.4 Per-gate reporting

`ProfileReport.Failures []string` (`:146-148`) is the reporting form and it is
right: "a promotion decision reads as a list rather than as a boolean." Five
things a gate report must print that `Report` does **not** currently produce,
and which §3.3 shows are the difference between a real gate and a decorative
one:

1. **The denominator for every rate**, and the confidence interval. `Passed`
   today is a boolean over point estimates.
2. **The Wilson interval for FP rate and recall** (or an equivalent), so a reader
   can see the width.
3. **The count of degraded and insufficient-evidence cases**, separately. An
   FP rate computed only over the cases the model answered is the most flattering
   and least honest number available.
4. **The time window** the cases came from, and the number of *unjudged* cases in
   it. A gate run over 30 judged cases out of 4,000 unjudged ones is a gate over
   a selected sample.
5. **The model, prompt version and dataset hash** the report was produced with,
   so a report cannot be re-read against a different one.

### 11.5 The regression suite for a model or prompt change

| Layer | What it catches | Cadence |
|---|---|---|
| **Contract tests** (the existing 21) | The request/response shape, the degradation vocabulary, the reason strings | Every commit. Already in CI (`.github/workflows/ci.yml:119-139`) |
| **Schema validation** | An output that no longer matches its profile schema | Every commit |
| **Offline eval on the frozen corpus** | Accuracy regression | Every prompt or model change, with both versions scored on identical cases |
| **Determinism of the harness** | A changed corpus | Every corpus change |
| **A/B on the corpus** | Whether the new version is *better*, not just different | Every promotion |
| **Golden tests** | The deterministic pipeline only | Every commit. Never prose |
| **Shadow** | Real traffic, real distribution shift, zero state effect | 14 days minimum for M1 gate 4 |

**The rule for a version change:** the new version is scored on the *same*
corpus as the old, both reports are kept, and the delta is reported in absolute
points with intervals. A change that improves the new version against a
*re-labelled* corpus has proved nothing, because the label moved.

### 11.6 The promotion checklist, as a template

```
Profile:            <profile id, version>
Model:              <name>   hash: <sha256>   bundle signature: <hex>
Prompt:             template <id>  version <semver>
Dataset:            <name>   hash: <sha256>   cases: <n>   strata: <n>
Runtime:            <version>   hardware: <GPU model, VRAM>

Gate results (each named, none averaged):
  false-positive rate        observed <x>  n <k/N>  95% CI [<lo>, <hi>]   limit <= 0.20   PASS/FAIL
  recall on true positives   observed <x>  n <k/N>  95% CI [<lo>, <hi>]   limit >= 0.70   PASS/FAIL
  median latency             <ms>   limit <= 8000                             PASS/FAIL
  disagreement rate          observed <x>  n <k/N>                            limit <= 0.25   PASS/FAIL
  labelled cases             <n>        minimum 200                          PASS/FAIL
  degraded observations      <n>  by reason <map>
  insufficient evidence      <n>
  unjudged cases in window   <n>

Companion metrics (no regression vs the pre-promotion window):
  missed-incident rate       <x> (delta <+/-)
  operator override rate     <x> (delta <+/-)
  false escalation rate      <x> (delta <+/-)
  evidence completeness      <x> (delta <+/-)

Shadow period:       <start> → <end>  (<n> days, 14 required)
Pre-promotion comparison run: <report id>
Known limitations:  <from ../security/trust-and-security-program.md §4.1>
Approved by:         <human, not the harness>
```

The line that matters is the last one, and it is the same one
`evaluation.Report.Promotable` already encodes: "Production promotion is a
separate, human decision; this says which ones are eligible."

---

## 12. An AI risk register for this domain

`../company/risk-register.md` §2.9 holds the company's AI-specific risks. This section adds
the ones specific to physical security that the generic register cannot state,
because the asymmetry is the whole argument.

### 12.1 The asymmetry

**A false negative and a false positive are not the same kind of error, and
they are not the same size.** A false positive costs an operator thirty seconds
and a little scepticism. A false negative, at a customer's site, with a response
protocol behind it, can cost a person their life or a company a lawsuit.
`evaluation.go:27-30` names the mild one precisely: `OutcomeFalsePositive` is "the
observation being right that something happened and wrong that it mattered. The
rule's noise floor." There is no corresponding mild description of a false
negative, and that absence is the finding.

**The asymmetry is not "orders of magnitude" in a colloquial sense. It is
structural**, and the structure is measurable:

| | False positive | False negative |
|---|---|---|
| Detected by | The operator, immediately, in the queue | **Nobody.** This is `metrics-and-analytics.md` §5.4's "the silent direction" |
| Measured by | The triple's `AI said / operator said` disagreement | **Only by replay audit** — re-running stored facts and asking what the system would have produced |
| Cost | Operator attention | Unbounded, externalised to the customer |
| Sample size to demonstrate < 1% | ~300 negatives is easy | **> 300 true positives (rule of three)**, ~2 years at one design-partner site (§3.3) |
| Effect on the trust claim | Slightly lower trust | Destroys the trust claim permanently and publicly |

**Therefore the product's answer is never "the model decides alone", and it is
never going to be, at any milestone up to and including M7.** The answer is
M7's policy-bounded autonomy with the four companion metrics, and the four are
not a reporting nicety — they are the mechanism by which "trusted automation"
is distinguished from "automation":

- **Human Attention Minutes per 1,000 security events** — the North Star, live
  from M7. It is meaningless alone; a system that suppresses everything scores
  beautifully.
- **Missed-incident rate** — the false-negative measure. Measured by replay audit,
  never by the system.
- **Operator override rate** — the signal that the automation is wrong *and*
  humans noticed. A rising override rate is the earliest warning in the whole
  system.
- **False escalation rate** — the cost of the missed ones. Every missed escalation
  that is caught late arrives as a false escalation somewhere.
- **Evidence completeness** — whether the automations were acting on evidence
  that exists. A high automation rate with low completeness means the system is
  confidently acting on nothing.

`../master-roadmap.md` §M7 states the rule: "Automation alone is not success. Trusted
automation is success, and the four companions are how we prove the difference
rather than assert it."

### 12.2 The register

| # | Risk | Severity | Likelihood | The specific shape in this domain | Mitigation that exists | What is missing |
|---|---|---|---|---|---|---|
| **AI-1** | Model asserts a negative that corroborates an incident | Critical | Low | A stub returning `{"restrictedZoneBreach": false}` produces a *corroborated* incident on a fabricated negative — "the single worst failure this product can have" (`docs/status.md`) | The runtime has no such code path: no model, and `degraded_response` sets `insufficientEvidence: true` (`runtime.py:200`) | The F1 client must preserve this: never record a negative as a non-degraded observation |
| **AI-2** | Fabricated licence plate sends a response to the wrong vehicle | Critical | Medium at M2, Low if `plateText` stays non-authoritative | `vehicle-at-gate.plateText`; the false confidence of a legible-plate model at night in rain | Schema: `plateLegible` is separate from `plateText`; the schema's own description says "Not normalized, not resolved to a vehicle identity in M1" | The rule that `plateText` may never gate a physical action (§9.4). It should be a semantic-validator check like §3.5's |
| **AI-3** | Anomaly ≠ danger (§8.1) | Critical | High — it is the default failure of every anomaly product | M6 baselines producing "abnormal" that escalates | The roadmap states the rule twice | The type-level enforcement: no `dangerous` type, and a review rule that no code computes severity from a baseline |
| **AI-4** | The gate passes on 30 cases and is believed | High | **Certain**, given `MinLabelledCases: 30` | §3.3's arithmetic | `MinLabelledCases` exists as a concept | The value. Change to ~200, or change the shadow gate to agreement rate (§3.3 C) |
| **AI-5** | Autonomy raised before the metrics exist | Critical | Medium | M7 A4 enabled on a site with no baseline | Blast-radius preview is specified | A pre-condition: the four companion metrics must be computable before an autonomy change, enforced by the policy engine (§9.2) |
| **AI-6** | Suppression by model rather than by rule | Critical | Low today | Any path from Tier C to `VerificationState.SUPPRESSED` | Suppression is a rule action with a rule id | The review rule: Tier C may raise, never lower |
| **AI-7** | Prompt injection via a bundle, a model bundle, or an operator note | High | Medium | §4.3 | Bundles are untrusted input (`AGENTS.md`); the runtime verifies before load | F2 (the "verification" is a digest), and a written rule that no record text is model input except through a reference-returning tool |
| **AI-8** | Model bundle tampered on the customer's disk | Critical | Low | F2 | The runtime refuses an unsigned or hash-mismatched bundle | A real signature check. Until then, no claim |
| **AI-9** | Provenance lost in the vector store | Medium | Medium | §6.3 re-embedding; an embedding from an unverified bundle | Two generations side by side | A rule that an embedding's `model_id` is itself an attributed, bundle-verified artefact |
| **AI-10** | An evaluation dataset that is not representative of the site | High | Medium | Labels come from the incidents the operator already saw, which are the ones the system produced | `Covers` per scenario | A periodic sample of **unjudged** incidents for labelling, so the corpus is not only the system's own output. Without it the harness measures self-consistency |
| **AI-11** | The product's own reporting becomes the marketing claim | High | Medium | A customer reads a gate report as a product benchmark | `../strategy-and-north-star.md` §8's evidence discipline | A published methodology — the checklist in §11.6 *is* the methodology, and it should be published with the numbers or not at all |
| **AI-12** | The AI plane's absence is invisible until it is measured | High | Low | `Stats.Availability()` exists | It is computed (`observations.Stats`, `:497`) and audited (`ai.observation.degraded`) | The metric is not emitted: `internal/observability` has no production call site. `release-sre-and-support.md` §5.2 names this gap. **AI availability must be a dashboard line before the first pilot**, or a pilot will claim a reduction that was really a degradation |

---

## 13. What this document does not claim

1. **No number in §10.2 or §10.3 has been measured on real hardware.** All are
   ESTIMATE. `../strategy-and-north-star.md` §8 forbids asserting unverified
   figures, and this document marks every one of them rather than choosing the
   most flattering.
2. **The ten profiles in §3.6 are a proposal, not a decision.** `../master-roadmap.md`
   says "ten profiles" and does not name them.
3. **Nothing here claims the AI plane works end to end.** It does not: F1 says
   there is no client. This is stated rather than hidden because M1's third
   DoD item — "a production VLM profile passing its gates" — is stated in
   `docs/status.md` as one of three things that "cannot be signed here", and the
   fourth thing it does not name is the missing connection.
4. **The claim that the harness is a gate, not a suggestion**, is true for the
   gate *arithmetic* and false for two of its *inputs* today: `MinLabelledCases`
   is set too low (§3.3) and the harness can only score one profile (§3.8).
5. **The evidence-engineering designs in §5, §6 and §8 are designs.** The
   evidence engine, semantic search and site intelligence do not exist; the
   mechanisms they must be built on (`evidence.Store`, `graph.Walk`,
   `incidents.Replay`, `simulate.Diff`, `bundles.Activate` with a diff digest)
   do.

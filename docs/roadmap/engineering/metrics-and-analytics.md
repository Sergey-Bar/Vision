# Metrics and Analytics

Owner: E1 (definition and reporting), E2 (instrumentation). Status: living.
Last revised 2026-09-28. Live from M1.

`` §"Product analytics" names this file. It is the definitions
and sources behind every number in the roadmap. Read ``
§4 (the three buyer answers) and `01` §M7 (the North Star) first.

The rule this document is built on, from `01`:

> A metric without a definition, an owner and a report is not a metric, and adding
> one is a change with a cost.

So this document has a **Definitions** section, a **Cadence** section, and a
**Not tracked** section, and the last one is the most load-bearing of the three.

**Evidence discipline, applied to this document.** Every number below is one of
three kinds, and it says which:

| Marker | Meaning |
|---|---|
| *(real)* | Read directly from code or a migration in this repository. The file is named. |
| *(PROPOSED)* | A threshold or definition this document proposes. It has never been measured. It is not a commitment and it does not appear in a customer conversation until it has been measured |
| *(NOT YET KNOWN)* | A number that does not exist yet. M1 measures it. Inventing it here would make every later document that cites this one wrong in a way nobody would notice |

No number in this document was estimated, extrapolated, or carried over from the
source material the README §"Evidence rule" calls unverified.

---

## 1. The metric tree

```
                        NORTH STAR
        Human Attention Minutes per 1,000 security events
        (headline metric from M7; 01 §M7; never reported alone)
                              │
        ┌─────────────────────┴──────────────────────┐
        │                                            │
   OPERATOR                                   AI & AUTOMATION
   EFFICIENCY                                  EFFECTIVENESS
        │                                            │
        ├─ mt_ti_seconds           MTTI             ├─ operator_intervention_rate   (M3 gate)
        ├─ mt_tv_seconds           MTTV             ├─ ai_agreement_rate
        ├─ attention_minutes        ─────────┐       ├─ ai_false_positive_rate
        │   (the numerator)         ════════╪═══════╪═▶ ai_false_negative_rate
        └─ operator_actions_          North  │       ├─ ai_insufficient_evidence_rate
            per_incident           Star    │       ├─ operator_override_rate
        ▲                             ratio │       └─ ai_hours_to_disposition
        │                                   │
        └───── 1,000 security events ───────┘
              (canonical_events in the window)

                              │
        ┌─────────────────────┴──────────────────────┐
        │                                            │
   PLATFORM HEALTH                            GUARDRAILS
   (is it working)                              (must not regress — §6)
        │                                            │
        ├─ inbox_accepted_total        *(real)*      ├─ accepted_event_loss        MUST BE 0
        ├─ inbox_dead_lettered_total   *(real)*      │   (invariant 1)
        ├─ canonical_events_total      *(real)*      ├─ missed_incident_rate
        ├─ corroborated_total          *(real)*      │   (PROPOSED threshold)
        ├─ contradicted_total          *(real)*      ├─ audit_chain_valid          MUST BE true
        ├─ unverified_total            *(real)*      ├─ checkpoint_gap_seconds     MUST BE 0
        ├─ backlog_depth               *(real)*      ├─ evidence_completeness_high MUST BE 1.0
        ├─ outbox_pending              *(real)*      └─ operator_override_rate
        ├─ job_queue_depth             *(real)*         (no regression until a
        ├─ ingest_lag_seconds          *(real)*          absolute is measured)
        ├─ clock_drift_ms              *(real)*
        ├─ clock_severe_device_count   *(real)*      A guardrail breach HALTS a
        ├─ evidence_disk_free_percent  *(real)*      release regardless of how
        ├─ evidence_fetched_total      *(real)*      good any feature metric
        ├─ evidence_fetch_failed_total *(real)*      looks. §6.
        ├─ vlm_invocations_total       *(real)*
        ├─ vlm_errors_total            *(real)*
        ├─ incident_open_count         *(real)*
        ├─ incident_degraded_count     *(real)*
        ├─ ai_unavailable              *(real)*
        ├─ postgres_conns_in_use       *(real)*
        ├─ audit_appends_total         *(real)*
        ├─ checkpoints_sealed_total    *(real)*
        ├─ rule_bundle_activated_total *(real)*
        └─ actions_dispatched_total    *(real)*
```

### 1.1 When each layer becomes real

| Layer | M1 | M2 | M3 | M4–M6 | M7 |
|---|---|---|---|---|---|
| **Platform health** | **Instrumented and demonstrated** — required by M1 exit gate 3 | Rolling | Rolling | Rolling | Rolling |
| **Operator efficiency** | **Baseline only.** MTTI, MTTV and attention minutes are *measured for the first time*; no target is set | **Target set and met** — this is the M2 exit gate | Rolling | Rolling | Reported per site |
| **AI effectiveness** | **Baseline in shadow.** FPR, FN, insufficient-evidence, disagreement on a profile that affects nothing | Rolling, per class | **Target on OIR**, with FN and missed rate not regressing | Per profile and per site | Reported with the North Star |
| **Guardrails** | **Live from day one** | Rolling | Rolling | Rolling | Reported as the North Star's four companions |
| **North Star** | Inputs only — the denominator and numerator are both recorded | Inputs only | Inputs only | Inputs only | **Becomes the headline** |

### 1.2 The four statements about this tree that are load-bearing

**1. From M1 the inputs are already instrumented.** Not because the North Star is
being chased, but because `01` §M1 gate 3 requires every SLO in
`internal/observability/slo.go` to be demonstrated under a recorded load run, and
because M2's exit gate is a measurement and you cannot measure a before you
measured a baseline. The North Star is M7's headline. Its two components are M1's
deliverable.

**2. The North Star is never reported alone.** `01` §M7 is explicit: it "is never
reported without its four companions: missed-incident rate, operator override
rate, false escalation rate, evidence completeness. Automation alone is not
success. Trusted automation is success." Any exec report showing the North Star
without all four is a defective report, and that is a reporting rule, not a
sensitivity.

**3. The denominator is `canonical_events`, not alarms and not incidents.** A
security event is one row in `canonical_events` *(real — `migrations/0003`)*. Using
incidents as the denominator would make the metric improve automatically as the
rules get more conservative, which is the wrong direction and a very easy mistake.

**4. There is a fabrication trap in this tree and it is named here so nobody
walks into it in M5.** `Human Attention Minutes per 1,000 events` can be reduced
by making events *less* eventful — by deduplicating, by filtering, by
suppressing. All three are things the product will legitimately do from M3. So the
metric is meaningless without its companions, and the companions are not a
formality. `01` §M7 already says this; it is repeated here because the failure
mode is invisible in the number itself.

---

## 2. Metric definitions

Every name in the first table is a real constant from `internal/observability/metrics.go`
or a real SLO name from `internal/observability/slo.go` *(both marked *(real)*)*.
Every source is a real table from `migrations/0001`–`0012` *(real)*.

### 2.0 One architectural rule that applies to every row

> **Postgres is the source of truth. The counter is for live SLO evaluation only.**

`internal/observability/metrics.go:5-13` states that counters are monotonic and
that the package keeps the metric names "spelled the same way here as in docs,
because a dashboard somebody hand-writes against a name this package invents is a
dashboard that quietly stops updating." The consequence, which is not stated there
and which this document makes explicit:

| Property | Counter / gauge in-process | Postgres query |
|---|---|---|
| Survives a restart | **No** — the value resets to zero | Yes |
| Historical | **No** — only since process start | Yes, forever, subject to retention |
| Comparable across sites | **No** | Yes |
| Good for | Live SLO evaluation, alerting within a process lifetime | Every reported number, every trend, every gate artifact |

So: **a number that appears in a gate artifact, a release note, a customer report
or a board document is always the Postgres computation.** A counter reading is only
ever a live health signal. This is not a preference; the M1 gate 3 evidence has to
be reproducible by the other engineer a month later, and a counter reading is not.

### 2.1 Platform health

| Metric *(real name)* | Definition in plain English | Exact computation | Source table · columns | Why it matters | Who reads it |
|---|---|---|---|---|---|
| `inbox_accepted_total` | Events we took responsibility for | Count of inbox rows in a terminal state in the window. Durable form: `count(*) FROM inbox_events WHERE received_at >= $1 AND received_at < $2 AND status IN ('processed','dead_letter')` | `inbox_events.received_at`, `.status` | It is the numerator of the loss guardrail. If a row is not in a terminal state, the platform owes somebody a result | E2 daily, E1 on the exec dashboard |
| `inbox_dead_lettered_total` | Events we could not process, and why | `count(*) FROM inbox_events WHERE status = 'dead_letter'`, grouped by `dead_letter_reason` | `inbox_events.status`, `.dead_letter_reason` | A dead letter is an accepted event the platform could not handle. It is surfaced, never dropped, and replay is an operator action (SLO 7) | E2 within 60s (SLO), E1 weekly |
| *(loss, durable)* | **Accepted events not in a terminal state — the loss guardrail** | `count(*) FROM inbox_events WHERE status NOT IN ('processed','dead_letter')`. **Must be 0.** This is the SLO 1 `accepted` input, computed as a live count rather than a running total | `inbox_events.status` | Invariant 1. This is the single most important number in the product and it is a count, not a percentage — "0.0000% loss" should make an operator suspicious of the measurement, per `slo.go:44-46` | Everyone, always. Breach halts a release (§6) |
| `canonical_events_total` | Facts the platform decided are real | `count(*) FROM canonical_events WHERE normalized_at >= $1` | `canonical_events.normalized_at` | The denominator of the North Star. Also the count of things a decision can replay from | E2 |
| `corroborated_total` | Incidents backed by the required independent signals | `count(*) FROM verification_results WHERE evaluated_at >= $1 AND state = 'corroborated'` | `verification_results.state`, `.evaluated_at` | Corroboration is what separates a signal from an incident. Accepted risk R2: a site with one camera type cannot corroborate itself, and that is correct | E2 daily, E1 monthly |
| `contradicted_total` | Verification found the evidence pulling the other way | `… AND state = 'contradicted'` | `verification_results.state` | Contradiction is information, not failure. Suppressing it is how a rule becomes theatre | E2 |
| `unverified_total` | Verification could not decide | `… AND state = 'unverified'` | `verification_results.state` | **This is where degraded AI decisions land.** A high rate here is a Tier C signal problem, not a rule problem, and the two are not the same fix | E2 daily |
| *(coverage gap — **no metric exists**)* | — | `verification_results` has **five** states: `corroborated`, `unverified`, `contradicted`, `duplicate`, `suppressed` *(real — `migrations/0004`)*. `metrics.go` declares counters for **three**. `duplicate` and `suppressed` have no counter | — | **Finding.** You cannot compute "share of all verdicts" or a false-negative denominator from these three counters, because two verdicts are unaccounted for. §7.2 proposes `duplicate_total` and `suppressed_total` | — |
| `backlog_depth` | Work accepted and not finished | `count(*) FROM inbox_events WHERE status IN ('received','processing','failed')` | `inbox_events.status` | SLO 5 soft limit 5,000 / hard 50,000 *(real — `slo.go`)*. Above the hard limit the platform **refuses** rather than dropping. That is the design; a drop would be invariant 1 failing | E2 live |
| `outbox_pending` | Side effects committed but not delivered | `count(*) FROM outbox_events WHERE status IN ('pending','delivering','failed')` | `outbox_events.status` | Same soft/hard limits. An outbox that grows is a relay that stopped | E2 live |
| `job_queue_depth` | Queued work by kind | `count(*) FROM jobs WHERE status IN ('pending','leased','failed')`, reported by `queue` | `jobs.status`, `.queue` | Queues are `vlm`, `evidence_fetch`, `rule_bulk`, `checkpoint_seal` *(real — `migrations/0002`)*. A growing `vlm` queue is an AI problem; a growing `evidence_fetch` queue is a `W1` evidence problem. They need different remedies | E2 live |
| `ingest_lag_seconds` | How long after we accepted an event the canonical fact exists | p95 over `extract(epoch FROM canonical_events.normalized_at - inbox_events.received_at)`. **PROPOSED feed definition** — the code has no caller, so this is the definition the M1 wiring must implement | `canonical_events.normalized_at`, `inbox_events.received_at` (join on `canonical_events.canonical_event_id → inbox_events` via `inbox_event_keys` / `canonical_event_keys`) | SLO 2, p95 under 5s *(real)*. The remedy names both causes — "a source is falling behind or the normalizer is saturated" — so the measurement must cover both, which the definition above does | E2 live; SLO in every health check |
| *(source lag — **new, PROPOSED**)* | How far behind the source's own clock we are | `extract(epoch FROM inbox_events.received_at - canonical_events.observed_at)` | `inbox_events.received_at`, `canonical_events.observed_at` | The SLO 2 remedy tells an operator to look at the source feed. This is the number that tells them *the source is the problem*. Conflating it with pipeline latency makes the remedy wrong | E2 when SLO 2 breaches |
| `clock_drift_ms` | How far a device's (or a source system's) clock is from reference | `device_clocks.drift_ms`, by `device_id` and by `clock_source` | `device_clocks.drift_ms`, `.band`, `.clock_source` | A VMS 40 seconds fast is not one bad camera, it is every event that system produced *(real — `migrations/0011`)*. Past the severe band a source is excluded from time-windowed corroboration, and the exclusion is recorded on `verification_results.drift_exclusions` rather than applied silently | E2; the operator sees it in `GET /v1/ops/clock-drift` |
| `clock_severe_device_count` | Devices excluded from corroboration | `count(*) FROM device_clocks WHERE band = 'severe'` | `device_clocks.band` | SLO 6, "fewer than 1% of devices in the severe band" *(real — `slo.go`)*. **Finding:** the implementation is `Breached: … > 0` — it breaches on **any** severe device, not at 1%. Either the target or the code is wrong; §7.2 lists the resolution | E2 live |
| `evidence_disk_free_percent` | Free space on the evidence volume | Filesystem `statfs` on the evidence root. **No table** — the bytes are on disk, not in Postgres | — | SLO 8, 10% free, warning at 25% *(real)*. `slo.go` says "this is the one that ends an incident badly": a full disk stops evidence fetches, and an incident without its evidence is not defensible | E2 live, page at 25% |
| `evidence_fetched_total` | Evidence items stored | `count(*) FROM evidence WHERE stored_at >= $1`, by `kind` (`snapshot`, `clip`, `bookmark`, `export`) | `evidence.stored_at`, `.kind` | Evidence completeness is a guardrail (§6), and it is computed from this | E2 |
| `evidence_fetch_failed_total` | Evidence we could not fetch | Durable proxy: `count(*) FROM jobs WHERE queue = 'evidence_fetch' AND status = 'dead_letter'`, plus `count(*) FROM evidence WHERE kind = ...` with no corresponding row. **There is no per-attempt failure table** | `jobs.queue`, `.status`, `.dead_letter_reason` | Same reason as above. **Finding:** a counter named `evidence_fetch_failed_total` has no durable source in the schema; §7.2 proposes `evidence_fetch_failures` as a `jobs`-derived query and says so | E2 |
| `vlm_invocations_total` | Model runs | `count(*) FROM ai_observations WHERE created_at >= $1` | `ai_observations.created_at` | The denominator of every AI quality rate | E2 |
| `vlm_errors_total` | Model runs that produced a degraded row | `count(*) FROM ai_observations WHERE created_at >= $1 AND degraded_reason <> 'none'`, by `degraded_reason` | `ai_observations.degraded_reason` | The five degradation reasons are `none`, `model_unavailable`, `no_runtime`, `timeout`, `error`, `absent` *(real — `migrations/0005`, `internal/observations/observations.go:40-45`)*. They need different remedies and lumping them destroys the signal | E2 daily |
| `ai_unavailable` | Fraction of observations that were real inferences | `1 − (degraded / total)` — implemented already as `observations.Stats.Availability()` at `internal/observations/observations.go:485-491` | `ai_observations.degraded_reason` | SLO 4 is "reported, never required" *(real)*. A run with a non-zero degraded count is a **pass**: the requirement is that the pilot can say how often the AI plane was there, not that it was always there. A12 in the threat model calls this an asset, not a metric | E1 monthly, E2 live |
| `incident_open_count` | Incidents needing somebody | `count(*) FROM incidents WHERE state IN ('open','in_review')` | `incidents.state` | The queue size. States are `open`, `in_review`, `dispositioned`, `closed` *(real)* | E1 weekly |
| `incident_degraded_count` | Open incidents where some signal was degraded | `count(*) FROM incidents WHERE degraded`, with `degraded_reasons` | `incidents.degraded`, `.degraded_reasons` | Invariant 3 made visible. An incident that degraded must never read as "nothing found" (`02` M1-T49) | E2 daily |
| `postgres_conns_in_use` | Connections in use | `count(*) FROM pg_stat_activity WHERE state <> 'idle'` | `pg_stat_activity` | The most common cause of a stalled pipeline is connection exhaustion, and it is invisible from inside the application | E2 live |
| `audit_appends_total` | Rows in the ledger | `count(*) FROM audit_ledger WHERE ts >= $1`, by `actor_type` | `audit_ledger.ts`, `.actor_type` | The ledger is the authoritative record *(real — `AGENTS.md`)*. A flatline here means something is not being audited, which is worse than an outage | E2 daily |
| `checkpoints_sealed_total` | Signed seals written | `count(*) FROM audit_checkpoints WHERE created_at >= $1` | `audit_checkpoints.created_at` | A seal is what makes the ledger falsifiable. **New metric `checkpoint_age_seconds` (§7.2) is the more useful one** and does not exist | E2 daily |
| `rule_bundle_activated_total` | Bundles activated | `count(*) FROM rule_bundles WHERE active`, plus the ledger `action` for the activation | `rule_bundles.active`, `audit_ledger.action` | Activation requires a reviewed simulate diff digest *(real — `migrations/0005` `.activate_diff_digest`)*. The count is a change-control signal, not a performance metric | E1 on change |
| `actions_dispatched_total` | Actions sent to the world | `count(*) FROM outbox_events WHERE topic = … AND created_at >= $1` | `outbox_events.topic`, `.created_at` | **M1: expected to be 0.** No action type in M1 has physical impact, and the M1 exit gate 2 chain ends at a disposition and a sealed checkpoint. This counter is a forward declaration | E2 from M3 |
| `dead_letter_visible_total` | Times a dead letter reached an operator | Ledger `action` naming dead-letter visibility | `audit_ledger.action`, `.ts` | SLO 7, "visible within 60s" *(real)*. **Finding:** the code breaches on `dead_letter_count > 0`, i.e. on the mere *existence* of a dead letter, not on visibility time; §7.2 | E2 |
| *(checkpoint gap — **new, PROPOSED**)* | Sequences sealed but absent from the database | `audit_checkpoints.to_sequence − max(audit_ledger.sequence)`, per tenant, and the count of removed seals detected by the gap detector | `audit_checkpoints.to_sequence`, `audit_ledger.sequence` | Threat model A7.2: deleting seals to hide a gap is detectable *by anyone holding the remaining seals*, which is why they are kept off the database host. That detection is a metric | E2 daily, E1 on any non-zero |
| `window` (the ingest-lag reservoir) | — | **Finding:** `NewReservoir(1024)` *(real — `metrics.go:252`)* is a fixed 1024-sample ring, so the p95 covers the last 1024 samples. At 5 events/second that is 3.4 minutes; at 1 event/minute it is 17 hours. **The "recent window" of SLO 2 and the reservoir's actual time span are not the same thing**, and a load run at a different rate measures a different window. §7.2 proposes recording the reservoir's wall-clock span alongside every p95 | — | — |

### 2.2 Operator efficiency

None of these has a metric name in `metrics.go` yet. All sources are real.

| Metric | Definition in plain English | Exact computation | Source table · columns | Why it matters | Who reads it |
|---|---|---|---|---|---|
| **`mt_ti_seconds`** (MTTI, "time to investigate") | From the moment the thing happened to the moment an operator recorded a verdict | Reported as **three numbers, never one**: **(a) detection latency** = `incidents.created_at − incidents.first_event_at`; **(b) investigation latency** = `min(dispositions.created_at) − incidents.created_at`; **(c) MTTI = (a) + (b)**. Aggregated per `rule_id` class, as mean and **median**, over incidents dispositioned in the window. Report the **median as the headline and the mean beside it** | `incidents.created_at`, `.first_event_at`, `.rule_id`; `dispositions.created_at` | The M2 exit gate is a reduction in this, measured against a VMS-only baseline (`01` §M2) | E1 monthly, E2 per site |
| **`mt_tv_seconds`** (MTTV) | From the fact existing to the verdict on it | `extract(epoch FROM verification_results.evaluated_at − canonical_events.normalized_at)` for the trigger event, per class, median and p95 | `verification_results.evaluated_at`, `.state`; `canonical_events.normalized_at` | **PROPOSED definition, and a name the roadmap never expands.** `01` uses "MTTV" without defining it. This document proposes **Mean Time To Verify**, because the pipeline's second stage is verification and SLO 3 (`incident_visibility`) is the same quantity measured end to end. **E1 must confirm or correct this before M2 opens.** If it means Mean Time To *Respond*, only the source changes (`dispositions.created_at`); every other definition in this document is unaffected | E1 monthly |
| **`attention_minutes`** | The numerator of the North Star: human minutes actually spent on our incidents | See §2.2.1 — **this is not measurable today** and the instrumentation is a named M1 deliverable | `audit_ledger` (new action), `dispositions.created_at`, `incidents.created_at` | The North Star. Also the only measure that goes *down* when the product works, which is why it is easy to fake and why §8 exists | E1 monthly, board |
| **`operator_actions_per_incident`** | How many distinct operator actions a disposition took | `count(DISTINCT action)` from `audit_ledger` where `actor_type = 'operator'` and `target_id = <incident>` — a **distinct-action** count, not a row count, so a page refresh is not an action | `audit_ledger.actor_type`, `.action`, `.target_id` | A disposition that takes four actions is not the same product as one that takes one, even at identical MTTI | E1 monthly |
| **`operator_intervention_rate`** | Share of incidents that needed a human at all | `count(DISTINCT incident_id) FROM incidents WHERE <a human acted> ÷ count(*) FROM incidents`, in the window | `incidents.state`, `dispositions` existence | The M3 exit gate *(real — `01` §M3)*. **In M1 and M2 this is 1.0 by construction**: every incident in M1 requires a human, because bounded auto-resolution does not exist until M3. Reporting it before M3 measures the milestone, not the product | E1 from M3 |
| **`evidence_completeness`** | Share of closed incidents that have retrievable evidence | `count(*) FROM incident_evidence` joined to `incidents` where `state IN ('dispositioned','closed')`, divided by the count of those incidents. Also report the retrieval status of each item, not just its existence | `incident_evidence`, `incidents.state`, `evidence.sha256` | A guardrail (§6) and a North Star companion. Existence is not completeness: a row pointing at bytes that fail their hash is incomplete | E1 monthly, E2 live |
| **`hours_to_disposition`** | Wall time from an observation to an operator judging it | Already implemented as `ProfileReport.HoursToDisposition` — the **mean** of `Case.HoursToDisposition` *(real — `internal/evaluation/evaluation.go:237`)* | `ai_observations.created_at` → `dispositions.created_at` | In the harness's own words: "a profile that is accurate and costs twenty minutes of operator attention per incident has not earned its place" *(real — `evaluation.go:54-56`)* | E2 per profile |

#### 2.2.1 `attention_minutes` — the honest problem, and the M1 fix

There is no column, no table, and no ledger action that records when an operator
**started** paying attention. `dispositions.created_at` records when they finished.
`incidents.created_at` records when the incident appeared. Nothing records the
opening of an incident, and the console's read endpoints do not audit: `GET
/v1/incidents/{id}` is a `requireCapability(identity.CapIncidentRead, …)` route
*(real — `internal/api/handlers.go:99-102`)* that writes no ledger row.

**What is proposed for M1** (`08` §7.1, task I-4):

1. The console writes one audited ledger row, `action = 'incident.opened'`,
   `target_type = 'incident'`, `target_id = <incident_id>`, on the first
   authenticated read of an incident detail page by a given session.
2. `attention_minutes(incident) = dispositions.created_at − min(audit_ledger.ts WHERE action='incident.opened' AND target_id=incident_id)`, capped at a declared per-class maximum **(PROPOSED: 4 hours; to be set from the M1 distribution, not before)**.
3. The **capped** and **uncapped** values are both reported. The uncapped number is
   an over-count — it includes an operator who opened an incident before going to
   lunch — and saying so is the point.

**What this is not.** It is elapsed time, not active time. It measures the
interval a human was *responsible for*, which is the quantity the customer
experiences, and it is the right first approximation. It is not keystrokes, and it
is not a claim about productivity. M2 adds a session heartbeat so active time can
be separated from queue time; until then, both the capped and the uncapped number
ship with that caveat in the footnote.

### 2.3 AI effectiveness

The AI / operator / final-outcome triple is recorded today, from three real
columns. The construction, the disqualifications, and the minimum-N problem are in
§5.

| Metric | Definition in plain English | Exact computation | Source table · columns | Why it matters | Who reads it |
|---|---|---|---|---|---|
| **`ai_agreement_rate`** | How often the operator said the model's read matched theirs | `count(*) WHERE agreement = 'agreed' ÷ count(*) WHERE agreement IN ('agreed','disagreed')`. **`unsure` and `no_ai_observation` are excluded from the denominator and reported separately** — an unsure is not a disagreement | `dispositions.agreement` (`no_ai_observation`/`agreed`/`disagreed`/`unsure` *(real — `migrations/0005`)*) | The harness's own gate is `MaxDisagreementRate: 0.25` *(real)*, i.e. a ceiling on 1 − agreement of 0.75 | E2 per profile |
| **`ai_false_positive_rate`** | How often the model said something was happening that was not worth an incident | See §5.2. Durable form: the 2×2 in §5.2, FP cell ÷ labelled denominator | `ai_observations.structured_output->>'restrictedZoneBreach'`, `.insufficient_evidence`, `.degraded_reason`; `dispositions.outcome` | Gate: `MaxFalsePositiveRate: 0.20` *(real)*. In the harness's words: "an operator shown noise twice a week learns to dismiss the feed, and everything the product does afterwards depends on them not doing that" | E2 per profile and class |
| **`ai_false_negative_rate`** | How often the model said nothing was happening and an operator said something was | 2×2 FN cell ÷ labelled denominator. Requires the operator to disposition something that produced **no** incident, which today can only be done by creating one | same as above | The most dangerous direction of error and the one with the least data. In M1 it is measured on a **replay audit** (§5.4), not on live dispositions, because live FNs are silent | E2 per class |
| **`ai_insufficient_evidence_rate`** | How often the model answered "I cannot tell" | `count(*) WHERE insufficient_evidence ÷ count(*)` from `ai_observations`; already implemented as `Stats.Insufficient` and deliberately **excluded from both the degraded count and the availability figure** *(real — `observations.go:521-528`)* | `ai_observations.insufficient_evidence` | A high rate is not a failure. It is the model correctly refusing, and the platform must be able to show the operator the difference between "no" and "I cannot tell" | E2 per profile |
| **`operator_override_rate`** | How often the operator overruled the observation that drove the verdict | `count(*) WHERE agreement = 'disagreed' ÷ count(*) WHERE related_observation_id IS NOT NULL`. **Rows with `agreement = 'no_ai_observation'` are excluded — there was no AI output, so there is nothing to override** | `dispositions.agreement`, `.related_observation_id` | A guardrail (§6) and a North Star companion. `related_observation_id` **is populated today** — `incidents.Record` looks up the latest observation for the incident and writes it *(real — `internal/incidents/incidents.go:683-690`)*, so this join is durable and needs no new schema | E1 monthly |
| **`ai_degraded_decision_rate`** | Verification decisions that ran on an absent AI signal | `count(*) FROM verification_results WHERE ai_degradation <> 'none' ÷ count(*)`; breakdown over `none`/`model_unavailable`/`no_runtime`/`timeout`/`error`/`absent` | `verification_results.ai_degradation` | Invariant 3, measured. A missing AI observation is never a negative observation, and this is the number that proves the difference was honoured rather than asserted | E2 daily, E1 monthly |
| `vlm_median_latency_ms` | The cost the pipeline pays | Already implemented as `ProfileReport.MedianLatencyMS` *(real — `evaluation.go:236`)*; durable source `ai_observations.latency_ms` | `ai_observations.latency_ms`, `.hardware` | Gate: `MaxMedianLatencyMS: 8000`. The harness's comment is the reason: "the AI plane is not on the availability path, but it is on the p95 incident-visibility path" | E2 per profile and hardware |
| `labelled_cases` / `undispositioned_cases` | How much evidence the rates are computed from | Already implemented as `ProfileReport.Labeled` and `.Undispositioned` *(real)* | `evaluation` harness output | **Reported before every rate, always.** `undispositioned` is counted separately and *never* folded into a denominator — "a case nobody judged is not evidence, and counting it as anything is how a harness makes a profile look better than it is" *(real — `evaluation.go:29-35`)* | E2, always |

### 2.4 Automation

Structural from M1. See §2.2 for the important note that Operator Intervention
Rate is 1.0 until M3.

| Metric | Definition | Computation | Source | State |
|---|---|---|---|---|
| `auto_triaged_rate` | Incidents reaching a terminal state with no human action | Zero in M1 and M2 by construction | `incidents.state` plus absence of `dispositions` | M3 |
| `auto_resolved_rate` | Incidents closed by a playbook with no operator | Zero in M1 and M2 by construction | `incidents.state = 'closed'`, `audit_ledger.actor_type` | M3 |
| `operator_intervention_rate` | See §2.2 | See §2.2 | — | M3 gate |
| `failed_action_rate` | Actions dispatched that were not verified as having succeeded | Zero in M1; no action type exists | M3 stores it. **An action not verified as succeeded is a failed action, not a completed one** *(real — `01` §M7 §3)* | M3 |
| `playbook_awaiting_operator_rate` | Proposals policy refused, so the operator never saw them | M3. A proposal the policy rejects never reaches an operator, and the *rejection* count is a policy-health metric | M3 | M3 |

---

## 3. Baselines and targets

**Nothing in this table is estimated. Every cell is either a number that exists in
the repository, or `(NOT YET KNOWN)`.**

### 3.1 Per-milestone: what each milestone measures, sets, and leaves alone

| Milestone | Metric | Role | Value | Source of the number |
|---|---|---|---|---|
| **M1** | `ingest_availability` | **Demonstrate** | zero accepted events lost | SLO 1 *(real — `slo.go:47-54`)* |
| M1 | `ingest_latency` p95 | **Demonstrate** | < 5s | SLO 2 *(real — `slo.go:56`)* |
| M1 | `incident_visibility` p95 | **Demonstrate** | < 15s | SLO 3 *(real — `slo.go:72`)* |
| M1 | `ai_plane_availability` | **Demonstrate** | reported, never required; a non-zero degraded count **passes** | SLO 4 *(real — `slo.go:88-97`)* |
| M1 | `ingest_backlog` | **Demonstrate** | soft 5,000 / hard 50,000 | SLO 5 *(real — `slo.go:103`)* |
| M1 | `correlation_under_drift` | **Demonstrate** | fewer than 1% of devices in the severe band | SLO 6 *(real — `slo.go:117`)* — **but the code breaches at `> 0`; see §7.2** |
| M1 | `dead_letter_visibility` | **Demonstrate** | visible within 60s | SLO 7 *(real — `slo.go:129`)* — **but the code breaches on existence; see §7.2** |
| M1 | `evidence_disk` | **Demonstrate** | 10% free, warn at 25% | SLO 8 *(real — `slo.go:142`)* |
| **M1** | `mt_ti_seconds` | **BASELINE ONLY — no target** | **(NOT YET KNOWN)**. M1 measures the distribution; M2 sets the target | — |
| M1 | `attention_minutes` | **BASELINE ONLY** | **(NOT YET KNOWN)**. First measurement, with the caveat in §2.2.1 | — |
| M1 | `operator_actions_per_incident` | **BASELINE ONLY** | **(NOT YET KNOWN)** | — |
| M1 | `ai_plane_availability` share | **BASELINE** | **(NOT YET KNOWN)**. How often the AI plane was actually live at the design-partner site | — |
| M1 | `ai_false_positive_rate` (production profile) | **BASELINE, against an existing gate** | gate ≤ 0.20, on ≥ 30 labelled cases | `evaluation.DefaultGates()` *(real — `evaluation.go:103-111`)* |
| M1 | `ai_recall` (production profile) | **BASELINE, against an existing gate** | gate ≥ 0.70 on true positives, on ≥ 30 labelled cases | `evaluation.DefaultGates()` *(real)* |
| M1 | `ai_disagreement_rate` | **BASELINE, against an existing gate** | gate ≤ 0.25 | `evaluation.DefaultGates()` *(real)* |
| M1 | `vlm_median_latency_ms` | **BASELINE, against an existing gate** | gate ≤ 8000ms | `evaluation.DefaultGates()` *(real)* |
| M1 | Shadow-window case count | **Demonstrate** | ≥ 10 labelled cases during shadow | `evaluation.ShadowGates()` *(real — `evaluation.go:116-120`)* |
| M1 | Consecutive shadow days | **Demonstrate** | ≥ 14 | M1 exit gate 4 *(real — `01` §M1)* |
| **M2** | `mt_ti_seconds` | **SET AND MEET** | **(PROPOSED, must be set before the trial opens and per class)** — `01` §M2: "reduced by a statistically meaningful margin on supported classes, measured per operator against their own VMS-only baseline. 'Meaningful' is set before the trial, per class, and recorded." §4 is the design | `01` §M2 *(real)* — the number itself is NOT YET KNOWN |
| M2 | `evidence_completeness` | **SET** | **(PROPOSED: 1.0 for `high` and `critical`)**. The basis is `slo.go`'s own statement that an incident without evidence is not defensible, and asset A4 in the threat model | — |
| M2 | All AI rates | **Carry forward, no regression** | No regression versus the M1 baseline | — |
| **M3** | `operator_intervention_rate` | **SET AND MEET** | **(PROPOSED)** below an agreed threshold on the bounded class set, **while** `ai_false_negative_rate` and `missed_incident_rate` do not regress versus M2 | `01` §M3 names the metric and the non-regression condition *(real)*; the threshold is NOT YET KNOWN and is set in M2 against the M1/M2 baseline |
| M3 | `failed_action_rate` | **SET** | **(PROPOSED: zero unrecovered failed actions)**. `01` §M7 §3 — an action not verified as succeeded is a failed action | — |
| M3 | `mt_ti_seconds` | **Carry forward** | No regression; the win is OIR, not speed | — |
| **M4** | Non-adapter diff | **Demonstrate** | zero behavioural changes outside the adapter | `01` §M4 exit gate *(real)* |
| M4 | `mt_ti_seconds` on a second VMS | **SET** | **(NOT YET KNOWN)** — a second VMS's classes have their own baseline and their own target | — |
| **M5** | RTO / RPO | **Demonstrate against published numbers** | **(NOT YET KNOWN)** — but the DR drill already surfaces the real finding: after a database loss the restored ledger is provably behind the seal, because a backup is a snapshot of some earlier moment *(real — `internal/dr/drill_test.go:196-220`)*. That gap gets designed around, not explained away | `01` §M5 §5 *(real)* |
| M5 | `attention_minutes` | **CARRY FORWARD** | The M1 measurement design carries into the control-plane work unchanged, and must survive a disconnected control plane | — |
| **M6** | The compounding claim | **Test, do not assume** | by M6, a new customer costs more to onboard than an existing customer costs to keep | `00` §5 and `01` §M6 exit gate *(real)*. This is a ratio of onboarding cost to marginal site cost and has **no metric name yet**; §7.2 proposes one |
| **M7** | **`attention_minutes` per 1,000 `canonical_events`** | **BECOMES THE HEADLINE** | must fall continuously; **never reported without** `missed_incident_rate`, `operator_override_rate`, `ai_false_escalation_rate`, `evidence_completeness` | `01` §M7 *(real)* — **the value is NOT YET KNOWN and no target is set, by design. `01` §M7 says "must fall continuously", not "must reach X"** |
| M7 | The four companions | **Must not regress** | Continuous | `01` §M7 *(real)* |

### 3.2 The numbers this document refuses to state

Written out, because the pressure to fill these in arrives in M2 and it is easier
to resist with a list than with a principle.

| Number | Why it is absent |
|---|---|
| A target MTTI | Requires a VMS-only baseline from a real operator, which does not exist until `02` M1-T38 runs. A target set before the baseline is a guess with a deadline attached |
| A target attention-minutes-per-1,000-events | The numerator needs `attention_minutes`, which needs the `incident.opened` instrumentation (§2.2.1) and a distribution to set a cap from |
| A missed-incident rate | Requires the labelled replay audit (§5.4) to have been run at a known sample size. The resolution of the estimate is bounded by N — see §5.5 — and quoting a rate without its N is the exact error §5.5 is about |
| A `human_attention_minutes_per_1000_events` value | It is a ratio of two (NOT YET KNOWN) numbers. Dividing one unknown by another and printing the result is how a roadmap acquires a number nobody can source |
| A "we prevent X% of incidents" figure | Not defined, not measurable, and the README §"Evidence rule" already marks unverified market claims. If it is ever computed it is a per-site claim with a denominator, never a product claim |
| Anything from the original source material | README §"Evidence rule": "Market claims, benchmark numbers and vendor comparisons in the source material were never verified" |

---

## 4. Measurement design for the M2 exit gate

The M2 exit gate, in the roadmap's words *(real — `01` §M2)*:

> Mean Time To Investigate reduced by a **statistically meaningful margin** on
> supported classes, measured per operator against their own VMS-only baseline.
> "Meaningful" is set before the trial, per class, and recorded.

This section is how that is actually conducted. It is written in M1 and signed
before M2 opens, because a design written after the data exists is a rationalisation.

### 4.1 Design: within-subject, alternating, class by class

| Element | Choice | Why this and not the alternative |
|---|---|---|
| **Within-subject** | Every operator is their own control. There is no between-operator group | Investigation time varies far more between operators than between tools. If E2 (fast) and E1 (careful) are on different arms, the arm difference is the person. The roadmap already requires this: "per operator against their own VMS-only baseline" |
| **Alternating blocks (ABAB), not parallel groups** | One block of VMS-only, then one block with Captain, then VMS-only, then Captain, and so on. **Block length = one shift** | Parallel groups drift with the event mix. Alternating also controls for the site getting busier or quieter over the trial, which over a quarter it will |
| **Counterbalanced order** | Half the operators start with VMS-only, half start with Captain. Order is randomised per operator and recorded | Order effects (learning, fatigue) are real and asymmetric: the second block is faster for reasons that have nothing to do with the tool. Counterbalancing distributes the effect instead of ignoring it |
| **Class by class** | The analysis is per `rule_id` class. A pooled result across classes with different intrinsic difficulty is meaningless | `01` §M2 says "on supported classes". A pool is only reportable as a secondary, and only with the class mix reported beside it |
| **Minimum N per class per operator** | See §4.4. **N is not a number chosen in advance of the data; it is computed from the M1 baseline variance** | A fixed N chosen because it is convenient is the most common way a pilot produces a result nobody believes |
| **Blinding** | **Impossible**, and stated rather than fudged. The operator knows which workflow they are in | Claiming a blinded trial with two visibly different UIs would be the first false statement in this document |
| **Washout** | One shift at each transition, excluded from analysis | Prevents a half-and-half shift contaminating a block |

### 4.2 The Hawthorne problem, and the five countermeasures

The operator will work harder during a measured trial than on a normal shift. That
is not a flaw in the operator; it is what happens when work is being watched, and
a study that ignores it produces a number that inflates both arms — and inflates
the Captain arm more, because the Captain arm is the one the operator is being
asked to try.

| # | Countermeasure | Detail |
|---|---|---|
| 1 | **Measure from telemetry, not from self-report** | MTTI is computed from `incidents.created_at` / `first_event_at` and `dispositions.created_at`. The operator is never asked how long it took, and never asked to time anything. A stopwatch is a second job and a second thing to remember |
| 2 | **Do not announce the direction** | The operator is told two workflows are being compared and that we want their honest view. They are **not** told that the hypothesis is that one is faster, and not told the target. A stated hypothesis gets a compliant operator |
| 3 | **Counterbalance and alternate** | See §4.1. The Hawthorne lift applies to both arms, and alternation spreads it rather than concentrating it in one |
| 4 | **Interleaved practice** | Both workflows are in use from block 2, so neither is "the experiment" for the whole trial. This is also what `01` §M2's "sequential, not parallel" discipline implies for a 2-person company |
| 5 | **No aggregate feedback until the study closes** | The operator does not see running per-class results. Seeing a favourable number mid-trial is the strongest possible incentive to make the next shift look like the last one |

Two further controls, for the confounders that actually bite at a security site:

| Confounder | How it is handled |
|---|---|
| **Event-mix shift** over the trial (more nuisance alarms, a delivery week, a staffing change) | Every report states the per-class distribution for both arms. If the mix differs by more than a pre-declared amount, the affected class is **excluded and the exclusion is recorded**, not silently pooled |
| **Batching** — an operator returning to a queue of 20 incidents does not investigate each in 90 seconds | The unit of analysis is the **incident**, and the primary statistic is the **per-operator median**, not the pooled mean. A backlog is a real cost, but it is not investigation time, and pretending otherwise produces a number that swings with the queue depth |
| **The operator learns the site's cameras** during the study | Recorded as an operator-experience covariate in the report. Two blocks on day 1 and two on day 60 are not the same operator |

### 4.3 What is recorded

| Recorded | Source | Why |
|---|---|---|
| Incident id, class (`rule_id`), severity, first event time, incident creation time, first disposition time, the actor | `incidents`, `dispositions` | The three MTTI components in §2.2, per incident |
| Operator id and role | `dispositions.actor_id`, `.actor_roles` | Within-subject pairing, and the analysis is per operator |
| Block id, arm, block order, date | Trial log — a **new** table, not in the current schema | Assignment is data. If it lives in someone's notebook the analysis is not reproducible |
| Session start/end around the block | `sessions.created_at`, `sessions.last_seen_at` — these are **already** recorded *(real — `migrations/0008`)* and can bound active time without new instrumentation | Partial answer to the "elapsed vs active" problem in §2.2.1, available now |
| Degraded decisions in the window | `incidents.degraded`, `verification_results.ai_degradation` | A degraded incident is a different investigation. Mixing degraded and non-degraded is a confound with a name |
| AI plane availability in the window | `observations.Stats.Availability()` | Same reason, and it is what the customer will ask about first |
| The operator's own written assessment, at the end | Free text, per operator, per class | Qualitative. Reported **beside** the number, never merged into it |
| Anything the operator says was harder | Free text | The failure mode of every MTTI study is optimising the metric and producing a product operators route around. This is how that is caught |

### 4.4 When the target is set, and by whom

| Step | Who | When | Output |
|---|---|---|---|
| 1. Measure the M1 baseline | E2 | During M1, before M2 opens | The per-class MTTI distribution: median, p25, p75, n, and the **pooled within-operator standard deviation** |
| 2. Declare the minimum meaningful effect | **E1**, with E2's feasibility input, **agreed by both** | **Before the trial opens. Recorded in `01` §M2's scope.** Per class | A number per class, in seconds or as a percentage, in writing |
| 3. Compute N | E2 | Immediately after step 2, before the trial opens | The per-operator, per-class sample needed to detect the declared effect at α = 0.05 two-sided with 80% power, from the step-1 variance. **If the M1 baseline variance is not known, N cannot be computed, and that is an M1 deliverable, not an M2 surprise** |
| 4. Fix the trial length from N and the event rate | E2 | Before the trial opens | If the site's event rate for a class cannot supply N inside the M2 window, **the trial extends**. The effect threshold does not move. A trial that ends because a date arrived produces a result that is a function of the date |
| 5. Run | Both | M2 | The trial log |
| 6. Analyse against the pre-declared effect | E2 | M2 close | The report |
| 7. Sign or not | **E1 and E2, jointly** | M2 exit gate | The signed result |

**The two ways this gets faked, named so they can be recognised:**

1. **Moving the threshold after seeing the data.** Step 2 is written into
   `01` §M2's scope before the trial opens. A threshold changed afterwards is not
   a threshold; it is a description of the result.
2. **Pooling classes to reach significance.** Step 4 fixes the classes and the
   event-mix exclusion rule in advance. A class excluded for insufficient events
   is reported as insufficient, not quietly dropped from the denominator.

### 4.5 The M2 result has a fourth companion too

MTTI is the headline of the M2 gate. The report carries, unprompted:

- `missed_incident_rate` on the same classes, over the same window (a faster
  investigation that misses more is not an improvement)
- `evidence_completeness` on the same incidents
- the **degraded-decision share**, so nobody reads an MTTI improvement as an AI
  availability claim
- the operator's own assessment, in their words

---

## 5. AI quality measurement

### 5.1 What is recorded today, from real columns

The AI / operator / final-outcome triple `01` §M6 §4 describes ("AI said vs
operator said vs final outcome, tracked per class and per site") is **two-thirds
real today**:

| Element | Where it lives | Real? |
|---|---|---|
| **AI said** | `ai_observations.structured_output->>'restrictedZoneBreach'`, plus `.insufficient_evidence`, `.degraded_reason`, `.model`, `.model_hash`, `.prompt_version`, `.input_evidence_hashes`, `.output_hash` | **Yes** — `migrations/0005`, and `observations.go` validates every attribution field is present and refuses the row otherwise *(real — `observations.go:157-189`)* |
| **AI said, for *this* disposition** | `dispositions.related_observation_id` | **Yes** — populated by `incidents.Record` from the latest observation for the incident *(real — `internal/incidents/incidents.go:676-690`)*. No schema work needed |
| **Operator said** | `dispositions.outcome` (`true_positive`/`false_positive`/`benign`/`escalated`/`acknowledged`/`dismissed`) and `dispositions.agreement` (`no_ai_observation`/`agreed`/`disagreed`/`unsure`) | **Yes** — `migrations/0005` |
| **Final outcome** | — | **No. In M1 the operator's disposition *is* the outcome, and there is nothing after it.** The third element arrives when the customer's own ground truth exists — a claim, a site incident, a report. That is an M2+ data item and it is **not** something to synthesise. |

Two recording properties that make this work, and that are worth stating because
they are easy to break:

1. **A disposition never overwrites an observation.** Both are stored, both
   replay, and `dispositions` is append-only by trigger
   *(real — `migrations/0005` `dispositions_append_only`)*. "AI said X and the
   operator said Y" is a fact rather than whichever was written last.
2. **A superseding observation is a new row, never an edit**, and the chain is
   walkable via `supersedes_observation_id` *(real — `observations.SupersededBy`)*.
   A re-evaluation is therefore measurable as a delta, and does not corrupt the
   historical record the gate is judged on.

### 5.2 The 2×2, and who is disqualified

Build one row per disposition that has a `related_observation_id`, read the
model's answer from `structured_output`, and read the operator's from `outcome`.

| | Operator: `true_positive` | Operator: `false_positive` or `benign` |
|---|---|---|
| **Model: `restrictedZoneBreach = true`** | **TP** | **FP** — the noise floor |
| **Model: `restrictedZoneBreach = false`** | **FN** — the dangerous direction | **TN** |

```
ai_false_positive_rate = FP / (TP + FP + FN + TN)
ai_false_negative_rate = FN / (TP + FP + FN + TN)
ai_recall             = TP / (TP + FN)
```

**The disqualified rows, and why each is excluded rather than folded in:**

| Disqualified | `ai_observations` predicate | Why it is not a data point |
|---|---|---|
| **Degraded** | `degraded_reason <> 'none'` | The model did not answer. Counting a timeout as a negative is precisely the bug invariant 3 exists to prevent — and it would show up here as a fake TN, improving the numbers while the product degraded |
| **Insufficient evidence** | `insufficient_evidence = TRUE` | The model answered "I cannot tell". The harness says this in code: "a missing field, a degraded observation and an insufficient-evidence observation all yield nil, which the caller counts as an absence rather than as a negative" *(real — `evaluation.go:296-297`)* |
| **Undispositioned** | no `dispositions` row | "A case nobody judged is not evidence, and counting it as anything is how a harness makes a profile look better than it is" *(real — `evaluation.go:29-35`)* |
| **`agreement = 'no_ai_observation'`** | `dispositions.agreement` | There was no AI output. Disqualified from the agreement and override rates, and it is a **platform-health** signal instead: it is the count of decisions the AI plane did not reach |

**Each disqualified bucket is reported with its own count, every time, beside the
rate.** A rate with a large adjacent "excluded: N" is a rate computed on a
survivorship-selected subset, and the reader cannot see that unless the number is
printed.

### 5.3 The rule-vs-model confound, named

The harness's own comment on `OutcomeFalsePositive` is precise: "the observation
was right that something happened and wrong that it mattered. **The rule's noise
floor**" *(real — `evaluation.go:28-30`)*.

At M1 there is **one production rule and one production profile** on a
single-site deployment. A false positive can therefore be:

- the model asserting a breach that was not one (model error), **or**
- the rule raising an incident on a real but uninteresting event (rule error), **or**
- the customer's site being genuinely noisy at that hour of day (site).

**These are three different fixes and the pooled rate cannot distinguish them.** So
every M1 FP/FN report is cross-tabbed by:

| Cross-tab dimension | Source | What it separates |
|---|---|---|
| `verification_results.state` | `migrations/0004` | Whether the incident was corroborated, unverified, contradicted, duplicate or suppressed. A high FP rate concentrated in `unverified` is a corroboration problem, not a model problem |
| `incidents.severity` | `migrations/0004` | Whether the noise is in the low-severity tail. The `evaluation.go` comment is about noise "twice a week", which is a severity-and-frequency question |
| `device_clocks.band` on the triggering device | `migrations/0003` | Whether clock drift is manufacturing the event. Accepted risk R2: a source in the severe band cannot corroborate, so its incidents read `UNVERIFIED` by design |
| Hour of day in the **site's** timezone | `incidents.first_event_at` + `sites.timezone` | Whether the site is noisy at that hour. `00` §M6's "normal occupancy" baseline is the eventual answer; in M1 it is a cross-tab |
| `ai_observations.hardware` and `.model_hash` | `migrations/0005` | Whether the answer is model-specific. A different `model_hash` is a different subject and never pooled with another |

### 5.4 False negatives: the silent direction, measured by replay audit

A false negative **produces no incident**, so no operator ever sees it and no
disposition is ever written. Live dispositions cannot measure it. This is not a
small problem: `01` §M3's exit gate requires false-negative rate not to regress,
and there is no live signal that could detect a regression.

**The M1 method: a labelled replay audit of non-incident events.**

1. Draw a sample of `canonical_events` in the window that produced **no** incident,
   stratified by `event_type` and by hour so it is not dominated by one boring
   camera. **Stratified, and the stratification is recorded.**
2. An operator labels each: *should this have raised an incident?*
3. Every `true` is a **missed incident**, with the event id, the time, and the
   operator's reasoning.
4. The rate is `misses / labelled`, **reported with N**, and the labels are stored
   so the sample can be extended rather than re-drawn.

Cost control, because this is a 2-person company: the operator labels in batches
inside the bi-weekly review session already in the operating model's cadence
(`` §6.3), and the batch is capped at
whatever fits in the session. A partial sample is a partial sample and is reported
as one.

**What the sample size can and cannot resolve** is §5.5, and it is the reason
`missed_incident_rate` is a guardrail with a stated resolution rather than a target
with a decimal point.

### 5.5 Why these rates are meaningless below a minimum case count

This is not a general statistical caution. It is a specific, computed finding
about the gates that are already in the code.

`evaluation.DefaultGates()` sets `MinLabelledCases: 30` *(real — `evaluation.go:109`)*.
The harness's comment is: "A rate computed from four cases is a number, not an
estimate" *(real — `evaluation.go:93-95`)*. **Thirty is better than four, and it is
still not an estimate.** The table below is a Wilson score interval at 95%, two-
sided, computed for this document. It assumes i.i.d. Bernoulli cases, which real
cases are not — they cluster by incident, by camera, by hour and by site — so the
effective sample size is smaller than the nominal one and these intervals are
**optimistic**.

**The false-positive gate, `MaxFalsePositiveRate = 0.20` (an upper bound):**

| N | Observed rate | 95% Wilson interval | Upper bound ≤ 0.20? |
|---|---|---|---|
| 30 | 0.20 (6 of 30) | 0.095 – **0.373** | **No.** The gate is met by the point estimate and the interval says the true rate could be 3.7× the gate |
| 30 | 0.10 (3 of 30) | 0.035 – **0.256** | No |
| 50 | 0.16 (8 of 50) | 0.083 – **0.285** | No |
| 100 | 0.15 (15 of 100) | 0.093 – **0.233** | No |
| 100 | 0.10 (10 of 100) | 0.055 – **0.174** | Yes |
| 200 | 0.15 (30 of 200) | 0.107 – **0.206** | Marginal |
| 200 | 0.14 (28 of 200) | 0.099 – **0.195** | Yes |
| 300 | 0.13 (39 of 300) | 0.097 – **0.173** | Yes |

**The recall gate, `MinRecallOnTruePositives = 0.70` (a lower bound):**

| N | Observed recall | 95% Wilson interval | Lower bound ≥ 0.70? |
|---|---|---|---|
| 30 | 0.70 (21 of 30) | **0.521** – 0.833 | **No.** A profile at exactly the gate, on exactly the minimum case count, has a lower bound three-quarters the gate |
| 50 | 0.80 (40 of 50) | **0.670** – 0.888 | No |
| 100 | 0.80 (80 of 100) | **0.711** – 0.867 | Yes, narrowly |
| 200 | 0.80 (160 of 200) | **0.739** – 0.850 | Yes |

**What follows, and it is a change to how the gate is reported, not to what it is:**

1. **A gate pass at N = 30 is a pass, and it must be reported as a pass with its
   interval attached.** It is not evidence that the rate is at or under the
   threshold. Saying "FPR 0.20, gate 0.20, PASS" without the interval is the
   single most likely false claim this metrics program could produce, and it would
   be made in good faith by a harness that is behaving correctly.
2. **The intervals are computed, not asserted.** Every profile report carries a
   Wilson interval and the N beside every rate, alongside the existing gate
   evaluation. The existing `ProfileReport` already carries `Labeled` and
   `Undispositioned`; the interval is the missing half.
3. **Clustering correction is required before any of these are quoted to a
   customer.** Real cases are not independent. The honest correction is a
   cluster-robust interval over the incident, or a per-site split. Until that
   exists, the report says "unadjusted for clustering" in the footnote, and a
   customer report says the same thing.
4. **`MinLabelledCases: 30` stays as it is for the M1 shadow gate.** It is a
   floor that rejects a profile judged on a handful of cases, and it does that
   job. What changes is the **claim**: at 30 cases we can say the profile is not
   obviously bad, and we cannot say it is at or under the gate. Roughly 200
   labelled cases at an observed rate of ~0.14 is what it takes to support that
   claim, and whether M1 or M2 can supply 200 per class is **(NOT YET KNOWN)** and
   depends entirely on the design partner's event rate for that class.

### 5.6 What the harness already does, and this document does not redo

`internal/evaluation` is written and correct on the points in this section, and
this document does not propose replacing it. It proposes **sourcing the same
numbers from Postgres** (§2.0) and **adding the interval and the N to the
report**. From the code:

| Already implemented *(real)* | Where |
|---|---|
| Cases grouped per profile; missing cases counted as absences, not as misses | `evaluation.go:199-204` |
| Degraded and insufficient-evidence rows counted separately and excluded from the prediction | `evaluation.go:191-197`, `296-309` |
| `Undispositioned` counted separately and never folded into a rate | `evaluation.go:208-211` |
| Every gate evaluated **independently**; a profile with three failures reports three, never a score | `evaluation.go:143-148`, `259-289` |
| `Failures` is a named list, so a promotion decision reads as a list rather than a boolean | `evaluation.go:146-148` |
| Promotion eligibility is separate from promotion; "Production promotion is a separate, human decision" | `evaluation.go:139-141` |
| `HoursToDisposition` is part of the profile score, with the reason stated in code | `evaluation.go:54-56` |
| Median, not mean, for latency | `evaluation.go:319-331` |

---

## 6. Guardrails

**The rule:**

> A guardrail breach **halts the release**, no matter how good the feature metric
> looks.

This is not a guideline. The M1 release ships with gates 1–4 unsigned if a guardrail
is breached, and the reduced-scope cut in `01` §M1 does not permit a guardrail
exception.

**Who may stop a release:**

| Actor | Authority |
|---|---|
| **E1, alone** | Yes. No reason required at the time; the reason is written afterwards |
| **E2, alone** | Yes. Same |
| **Any AI agent** | May **flag** a guardrail concern and may **refuse to help ship** something it believes breaches one. It may not resolve the breach, waive it, or set the threshold. A guardrail is not something an agent can talk a human out of |
| **A design partner** | May stop a release **at their own site** for any of these. A site refusing our build is the guardrail working |

### 6.1 The guardrail table

| Guardrail | Hard threshold | Computed from | Breached when | Source of the number | May stop a release |
|---|---|---|---|---|---|
| **Accepted event loss** | **Exactly 0.** Not a percentage, not a rate | `count(*) FROM inbox_events WHERE status NOT IN ('processed','dead_letter')` | Any non-terminal row older than the normalizer's lease window | **Invariant 1** *(real — `AGENTS.md`)*. SLO 1's own comment: a percentage of zero is still a non-zero number of lost events, and an operator reading "0.0000% loss" should be suspicious of the measurement | **E1 or E2, alone, immediately** |
| **Audit chain integrity** | `audit_verify_chain` returns `valid = true` over the full sealed range, and every seal verifies against a trusted key | `audit_verify_chain(tenant, from_seal.from_sequence)`; `securityctl audit verify-seals` | Any row whose stored `hash` or `prev_hash` does not recompute; any missing `prev_hash` predecessor; a seal over a chain that does not verify; a foreign public key | **Invariant 4** *(real)*. The chain is computed by a database trigger, not by the caller, so this is a property of the table *(real — `migrations/0007`)* | **E1 or E2, alone, immediately.** This is the asset the whole M1 moat is made of |
| **Checkpoint continuity** | **0 sequences** sealed-but-absent; checkpoint age below a declared maximum | `audit_checkpoints.to_sequence − max(audit_ledger.sequence)`; `now() − max(audit_checkpoints.sealed_at)` | A non-zero gap (a seal was removed — A7.2), or sealing has stopped | Threat model A7.2/A7.3. The maximum age is **(PROPOSED, NOT YET KNOWN)** and is set from the M1 sealing interval | **E1 or E2** |
| **Missed-incident rate** | **(PROPOSED) Zero known misses above the labelling resolution of the audit** | The labelled replay audit, §5.4 | Any operator-labelled miss on an event the rules should have caught | **(NOT YET KNOWN)**. Stated as a resolution rather than a percentage on purpose: with N labelled non-incident events, the finest rate distinguishable from zero is 1/N. At N = 200 that is 0.5%; a "rate below 0.5%" claim from 200 labels is 0-or-1-miss, and that is the claim's real meaning | **E1 or E2.** This is the guardrail most likely to be argued with, and the argument is answered by N, not by opinion |
| **False-negative rate** | **No regression** versus the previous recorded window | 2×2 FN cell ÷ labelled, §5.2 | The rate rises, or the labelled case count falls below `MinLabelledCases` | No absolute threshold exists and none is invented. A regression rule is used until an absolute is measured from real data | **E1 or E2** |
| **Evidence completeness** | **(PROPOSED) 1.0 for `incidents.severity IN ('high','critical')`** | `incident_evidence` joined to `incidents` where `state IN ('dispositioned','closed')`; each item's `sha256` re-verified against the bytes on disk | Any high or critical incident closed with no evidence, or with evidence whose hash does not match | **(PROPOSED)**. Grounded in `slo.go`'s own statement — "a full disk stops evidence fetches, and an incident without its evidence is not defensible" — and in asset A4. The hash re-check is included because a row pointing at altered bytes is not evidence | **E1 or E2** |
| **Operator override rate** | **No regression** versus the previous window | `dispositions.agreement = 'disagreed'` ÷ rows with a `related_observation_id` | The rate rises | `01` §M3 requires false-negative and missed-incident rates not to regress; override is the third North Star companion *(real — `01` §M7)*. No absolute is set until one is measured | **E1 or E2** |
| **AI degradation rate** | **No bound.** Reported, never required | `count(*) FROM ai_observations WHERE degraded_reason <> 'none' ÷ count(*)` | **Never breaches.** | SLO 4 *(real — `slo.go:88-97`)*: "The product keeps working, but a pilot claiming an AI benefit has to be able to say how often it was there." A degraded run is a **pass**; an **unreported** one is a breach, because the claim was made without the number attached | Nobody — and that is deliberate. Making the AI an SLO would put it on the availability critical path, which invariant 3 forbids |
| **Rule bundle lint and simulate** | `rules lint` clean; `activation_diff_digest` present and matching | `rule_bundles.lint_status`, `.activate_diff_digest` | A bundle activated without a digest, or with a digest that does not match the review | Activation **refuses** a bundle whose simulate diff was not acknowledged by that exact digest *(real — `migrations/0005`)*. This is enforced in the database, not in a review checklist | The code, automatically |
| **Contract compatibility** | `make contract-check` green | `tools/contractcheck` against `contracts/compat/baseline.json` | Any removed field, reused tag, changed type or cardinality, removed or renamed enum value, or field moved out of a `oneof` | C29 *(real — `docs/threat-model.md` §5)*. A breaking contract change **is a build failure** | The build, automatically |

### 6.2 The two guardrails that are not numbers

| Guardrail | What it is | Why it is here |
|---|---|---|
| **A weakened invariant** | Any change that weakens one of the five, without an ADR **and** a test, in that order | `AGENTS.md`: "A change that weakens one needs an ADR and a test, in that order." The exit-gate checklist in `02` §6 has this as precondition P6. It is a guardrail with no threshold because it has no acceptable value |
| **A missing invariant test** | Any durability change without a `kill -9` fault-injection test at the stage it touches; any evidence path change without its traversal negative test | `AGENTS.md` §Testing: "It is a DoD item, not a nice-to-have." This is the guardrail most likely to be waived "just this release" and the one with the worst long-run price |

### 6.3 The interaction between guardrails and the feature metric

Three cases where the guardrail wins, stated in advance so nobody has to make the
argument under time pressure:

| Situation | What the feature metric says | What happens |
|---|---|---|
| M2's MTTI target is met | MTTI down 40% on a class | **Check `missed_incident_rate` on the same classes in the same window first.** A faster investigation that misses more is not an improvement, and `01` §M3 requires non-regression. The M2 gate line is not signed without the companion |
| M3's Operator Intervention Rate target is met | OIR down to 0.3 | **Check FN and missed rate against M2.** `01` §M3 states this condition inside the gate itself: "below an agreed threshold on the bounded class set **while** false-negative rate and missed-incident rate do not regress versus M2" |
| M7's North Star is falling | Attention minutes per 1,000 events down sharply | **Check all four companions.** `01` §M7 is explicit that it is "never reported without its four companions: missed-incident rate, operator override rate, false escalation rate, evidence completeness. Automation alone is not success. Trusted automation is success." A North Star report missing a companion is a defective report and is not issued |

---

## 7. Instrumentation plan

### 7.1 What must be added in M1

**The finding that governs this whole section:** `internal/observability` is
imported by exactly **one** file in the repository — its own test. There is no
production call site and there is no scrape endpoint. Every name in `metrics.go` is
correctly spelled and correctly evaluated by `Breaches()`, and **nothing emits
any of them.** M1 exit gate 3 — "every SLO demonstrably met under a recorded load
run" — is not reachable from the current code.

| # | Task | What | Depends on | Why it blocks |
|---|---|---|---|---|
| **I-1** | Construct the `Registry` in the core and emit at every named call site | Every constant in `metrics.go`, incremented at the point in the pipeline it describes. A counter that is incremented in a wrapper rather than at the event is a counter that measures the wrapper | — | Without it, no SLO is demonstrable and **no number in this document is measured** |
| **I-2** | Expose a scrape endpoint | `Registry.Render()` already produces Prometheus text exposition *(real — `metrics.go:257-274`)*. Serve it behind a **new capability** `metrics.read` added to the capability list — capabilities are a list, not a code path, and adding one is a schema change which is the point *(real — `migrations/0008`, `identity.go:197-210`)*. **Do not** put it behind `incident.read`: an operator who may read incidents should not automatically be able to read site-wide operational telemetry | `01` §4 (contract change) for the capability | SLO surface for the operator |
| **I-3** | Wire `Breaches()` to something an operator reads | Every SLO carries a `Remedy` in code *(real — `slo.go`)*. Render the breach list with the remedy, on `/v1/ops/*` and in `securityctl doctor`. `Breached()` already returns only the failing ones *(real — `slo.go:156-163`)* | I-1 | "An alert with no remedy is an alert everybody learns to ignore" |
| **I-4** | **Audit `incident.opened`** | The console writes one ledger row on the first authenticated incident-detail read. Without it, `attention_minutes` — the North Star's numerator — is not measurable (§2.2.1) | — | The M7 headline metric's input, from M1 |
| **I-5** | **Bound the incident-visibility window** | `Breaches()` takes `incidentVisibilities []time.Duration` as a slice *(real — `slo.go:32`)*. An unbounded slice is a memory leak in a process that never restarts, and there is no reservoir for it the way there is for ingest lag. Add a `Reservoir`, same pattern, and record its wall-clock span | I-1 | SLO 3 evaluation on a site that runs for months |
| **I-6** | **Record the reservoir's span with every p95** | `NewReservoir(1024)` is a fixed *sample* count, not a time window *(real — `metrics.go:252`)*. A p95 over the last 1024 samples is 3.4 minutes at 5 events/second and 17 hours at 1 event/minute. Emit the span alongside the quantile so a p95 is never quoted without the window it covers | I-1 | Any p95 in a gate artifact |
| **I-7** | **Reconcile the two SLO implementations to their stated targets** | See §7.2. Two targets and two implementations disagree | I-1 | Gate 3 is signed line by line, and a line whose code contradicts its stated target cannot be honestly signed |
| **I-8** | Daily durable report | A `securityctl metrics report --from --to` that computes the §2 Postgres definitions, per tenant and site, and emits the numbers. It runs against the database with no process running, which is what makes it reproducible in a gate artifact | I-1 | Every number in §2 |
| **I-9** | Wire `vlm_invocations_total`, `vlm_errors_total`, `evidence_fetched_total`, `evidence_fetch_failed_total` | These have durable sources in `ai_observations`, `evidence` and `jobs` (§2.1). The counters are for live health; the report is for evidence | I-8, `02` M1-T33 | The AI baseline in §3.1 |

### 7.2 Naming, cardinality, and the findings that need a decision

**Naming convention for new metrics.** The existing names are the contract and are
**not renamed** — `metrics.go:4-6` is explicit that a dashboard written against a
name the package invents is a dashboard that quietly stops updating, and the same
logic says renaming breaks the ones that already exist. For anything new:

| Kind | Pattern | Example |
|---|---|---|
| Counter | `<domain>_<noun>_total`, monotonically increasing, never decreases, never a rate | `missed_incident_total` |
| Gauge | `<domain>_<noun>_<unit>` where the unit is in the name when the unit is not seconds | `checkpoint_age_seconds`, `evidence_disk_free_percent` |
| Rate | `_<rate>_rate`, and **never stored as a gauge** — computed at report time from the durable source | `ai_false_positive_rate` |
| Duration | `_<domain>_seconds` | `mt_ti_seconds` |
| Disqualification buckets | always reported beside the rate they were excluded from | `undispositioned_cases` |
| Domain prefixes, closed set | `inbox_`, `canonical_`, `verification_`, `incident_`, `ai_`, `evidence_`, `vlm_`, `audit_`, `checkpoint_`, `operator_`, `adapter_`, `rule_`, `job_`, `outbox_`, `clock_`, `postgres_` | — |

**A rate is never a gauge.** A rate in a gauge is a rate that stays true after the
denominator changes, which is the definition of a stale number. Rates are computed
in the report from the durable source.

**Cardinality rules.**

| Allowed as a label | Forbidden as a label | Why |
|---|---|---|
| `tenant_id` | `request_id` | Unbounded. One per request, and the value is a secret-adjacent correlation handle — `AGENTS.md` allows it in log lines and `audit_ledger.request_id`, never as a metric dimension |
| `site_id` | `device_id`, `camera_id` | Unbounded on a 2,000-camera site |
| `rule_id`, `profile_id`, `event_type`, `queue`, `degraded_reason`, `state`, `severity`, `band` | **`actor_id`, `session_id`, `ip`, `user_agent`** | **Operator-level and user-level metrics are a surveillance product wearing a metrics product's clothes.** An operator's individual disposition rate is HR data. The aggregate is the product; the individual is not, and the schema must not make the individual cheap to compute by accident |
| A bounded enum from a `CHECK` constraint | Any value from a source system, a note, a path, a URL, a filename | Untrusted input as a label is an allocation primitive. The existing `IncLabeled` already folds past 64 distinct values into `"other"` *(real — `metrics.go:118-136`)*, and `maxLabelValues = 64` is the hard cap to design against |
| `hardware` *(bounded by the model bundle's declared hardware profile)* | Anything from `ai_observations.structured_output` | Model output is untrusted input. B4: "A compromised model runtime therefore cannot alter authoritative state, only lie about evidence it was shown" — a label set is an allocation target |
| — | Free-text `notes`, `reason`, `last_error` | Unbounded by construction |

**Note on the existing single-label shape.** `IncLabeled(name, label)` supports
**one** label key, folded to `other` past 64 values *(real — `metrics.go:118-136`)*.
It is not a general label set. A new metric needing two dimensions needs a new
mechanism with a **product-level** cap, not a per-key cap — a per-key cap is
multiplicative. **This is a design decision E2 makes and records before the first
two-dimensional metric is added**, not a default that should be inherited.

**Findings requiring a decision, with the decision stated rather than deferred:**

| # | Finding | Evidence | Decision needed | Owner | By when |
|---|---|---|---|---|---|
| F-1 | `internal/observability` has no production caller | Only importer is its own test | Wire it (I-1). `02` M1-T33, zero float, 2026-W49 | E2 | M1, 2026-W49 |
| F-2 | **RESOLVED 2026-10-02.** `securityctl adapter-probe` was documented as the first thing to run on a real install and did not exist. It does now, reports the response body per route, and carries `--param` so a documented filter can be asked of a server rather than believed from a page | `internal/milestone/routes.go:16`, `:121`; `cmd/securityctl/adapter_probe.go` | Built, and the runbook for a real site is `docs/vendor/milestone/design-partner-runbook.md` | E2 | M1, 2026-W40 |
| F-3 | `correlation_under_drift` states "fewer than 1% of devices in the severe band" and implements `> 0` | `slo.go:117-119` | **Fix the code to match the stated target.** The target is the deliberate one: one excluded device in a 2,000-camera site is 0.05% and is not a site that cannot corroborate. A gate that fires on a single bad camera trains the operator to ignore it | E2 | With I-7, before gate 3 |
| F-4 | `dead_letter_visibility` states "visible within 60s" and implements `> 0` on the count | `slo.go:129-132` | **Split the two.** Count > 0 is not a breach — a dead letter is a legitimate terminal state. Visibility time is the SLO and needs the `incident.opened`-style audit (I-4) or an operator-side timestamp to measure | E2 | With I-7 |
| F-5 | `verification_results` has five states; `metrics.go` has three counters | `migrations/0004` vs `metrics.go:28-30` | Add `duplicate_total` and `suppressed_total`, or document that they are deliberately unexported. Right now a reader summing the three counters gets a number that is not the total | E2 | M1 |
| F-6 | `Breaches()` accepts `driftExcluded`, `correlations` and `contradictions` and discards all three with `_ =` | `slo.go:148-150` | Either use them or remove them. As written, the SLO evaluator **cannot** compute drift-exclusion rate, corroboration rate or contradiction rate — the three product-analytics numbers `01` names | E2 | M1 |
| F-7 | The ingest-lag reservoir is a fixed 1024 samples, not a time window | `metrics.go:252` | Emit the span with every p95 (I-6). Do not silently change the size — the size is what makes a load run at one rate comparable to a load run at another, and that comparability is a gate-3 requirement | E2 | M1 |
| F-8 | `Render()` converts `map[string]int64` counters to `map[string]float64` | `metrics.go:262` | At ~9.0e15 (`2^53`) a counter starts losing precision. Practically unreachable — it is 9 quadrillion events — and it is noted for completeness, not as a defect | — | No action |
| F-9 | `disposition_id` is `disp_<UnixNano>` | `internal/incidents/incidents.go:662` | Time-derived ids collide under a clock step and are not stable keys. `observations.go:330-356` does this deliberately and with a written rationale; this one has neither. **Low severity, and not a blocker** — but any metric grouped by `disposition_id` inherits the weakness, and a golden test that writes dispositions is not clock-stable | E2 | M2 |
| F-10 | The M6 compounding claim has no metric | `00` §5, `01` §M6 | Propose `onboarding_cost_minutes` and `marginal_site_minutes`, both measured from the installation record once the installer produces one. **Both (NOT YET KNOWN)** | E1 | M5, so M6 can test it rather than assert it |

### 7.3 The three dashboards

Three, not one. They have different audiences, different refresh rates, and
different failure modes, and merging them produces a dashboard nobody reads and
everybody screenshots.

#### 1. Operator dashboard

| | |
|---|---|
| **Audience** | The security operator, on a site, possibly at 3am |
| **Refresh** | 30s |
| **Question it answers** | *Is my site healthy, and what needs me?* |
| **Contents** | `incident_open_count`; `backlog_depth` / `outbox_pending` / `job_queue_depth` with the soft (5,000) and hard (50,000) limits marked; `dead_letter_count` with the oldest age; `evidence_disk_free_percent` with the 10% / 25% lines; `clock_severe_device_count`; `incident_degraded_count`; the SLO breach list **with each remedy**; the last checkpoint's age |
| **Source** | `GET /v1/ops/dead-letters` and `GET /v1/ops/clock-drift` *(both real)*, plus the scrape |
| **Must not contain** | Anything about a tenant the operator is not assigned to. RLS makes that a schema property, not a dashboard setting — the dashboard is secondary and reads through the same session |
| **Failure mode to design against** | It becomes a wall of gauges and the operator stops looking. **Every gauge on it must be one the operator can act on**, and the SLO remedies are the action list |

#### 2. Executive dashboard

| | |
|---|---|
| **Audience** | E1, the board, and eventually a customer's CISO |
| **Refresh** | Daily, with a weekly trend |
| **Question it answers** | *Is the product doing what it claims, and is the claim safe to make?* |
| **Contents** | The five guardrails, first and largest: accepted event loss, audit chain valid, checkpoint continuity, evidence completeness, override rate. Then the operator numbers: `mt_ti_seconds` per class with N, `attention_minutes`, `operator_actions_per_incident`. Then the AI numbers: agreement, FPR/FN with N and interval, insufficient-evidence, degraded share, hours-to-disposition. Then platform: ingest latency p95 with its window, incident visibility p95, AI availability, adapter availability. Then the M1 demonstration set: the SLO table with each reading and each remedy |
| **Must not contain** | Per-operator breakdowns (§7.2 forbids the underlying label). The **North Star appears here only from M7**, and only with all four companions present or the panel renders `INCOMPLETE` |
| **Failure mode** | It becomes the deck the roadmap is read from, and then every number on it is a commitment. Every figure on it carries its source table, its N, and its window in the tooltip. **A number on this dashboard with no source is a bug** |

#### 3. Incident review dashboard

| | |
|---|---|
| **Audience** | E2, and any engineer reading a disputed incident six months later |
| **Refresh** | On demand — one incident at a time, not a time series |
| **Question it answers** | *For this incident: what did the system see, what did it decide, why, and can I prove it?* |
| **Contents** | For one `incident_id`: the timeline across `canonical_events` (`observed_at` / `received_at` / `normalized_at`, all three, because the difference between them is the drift story); `incident_events` by role (`trigger` / `supporting` / `contradicting`); the `edges_active` provenance with `asserted_by`; `verification_results` including `drift_exclusions` and `ai_degradation`; `ai_observations` with `model_hash`, `prompt_version`, `input_evidence_hashes`, `output_hash`, `insufficient_evidence`, `degraded_reason`, and the supersession chain; `incident_evidence` with a hash re-verification against the bytes; the `dispositions`; the `audit_ledger` rows for this incident; and `GET /v1/incidents/{id}/explanation` with `replayedFromStoredFacts: true` |
| **Not on it** | A rate, a trend, or a comparison. This dashboard is a single incident and the discipline of it is that nothing is aggregated — aggregation is where the interpretation starts, and interpretation is a human job with a name on it |
| **Failure mode** | It becomes a screen for one incident that the customer reads while we are demonstrating. Which is the point: `01` §M3's gate is that "an operator can explain, from the product alone, why the system did what it did", and this is the surface that has to carry it |

### 7.4 Reporting cadence

| Report | Frequency | Who reads it | Contents |
|---|---|---|---|
| Site health note to the design partner | Weekly | The customer, from E1 | Backlog, dead letters, drift, evidence disk, degraded decisions — automated, and not explained unless something is wrong (`03` §6.3) |
| Guardrail check | **Per release, and before any exit-gate signature** | E1 and E2 | All of §6.1, with the evidence. Not optional and not a summary |
| Profile report | Per promotion decision | E2, then E1 | All §5, per profile, per class, with N and the Wilson interval |
| MTTI baseline | Once, in M1 | E1 | The §4.4 step-1 distribution, and the within-operator variance that N depends on |
| Operator efficiency report | Monthly | E1 | MTTI components, attention minutes (capped and uncapped, §2.2.1), actions per incident, evidence completeness |
| Replay audit | Per site, quarterly, and at each exit gate | E1, E2 | The §5.4 labels and the miss count with N |
| Exec dashboard | Daily, reviewed weekly | E1 | §7.3 dashboard 2 |

---

## 8. Metrics we deliberately do not track

Written down for the same reason `01` §M7 writes down the North Star's companions:
because a number that is not forbidden gets optimised, and optimising a number that
is not the operator's outcome is how a good product becomes a bad one.

### 8.1 Vanity metrics

| Not tracked | Why |
|---|---|
| Logins, DAU, MAU, sessions per operator | Measures the UI, not the outcome. Optimising it produces a console that is pleasant to open and a security desk that is not improved |
| Number of incidents **prevented** | Not defined, not measurable, and there is no counterfactual. If it is ever computed it is a **per-site** claim with a stated denominator and a stated method, never a product claim. The README §"Evidence rule" already marks unverified claims and this is the most tempting one to launder |
| Alerts reduced, alarms suppressed | **Suppression is how you improve this number by hiding things.** It is only reportable beside `missed_incident_rate` and `evidence_completeness`, and only as a per-class number for a class the operator has dispositioned |
| MRR, ARR, CAC, LTV, pipeline | Do not exist before M2 (`01` §M1: "no pricing"). When they do, they are a finance report, not a product metric, and they do not belong on a dashboard an engineer reads |
| NPS, CSAT, satisfaction surveys | Self-report with a known response bias and a survivorship problem, on a base of three design partners. Three data points and a survey is a number that means nothing |
| GitHub stars, contributors, community size | Not a metric for a 2-person product with a private repo until M8 |
| Number of integrations, adapters or rule packs shipped | **A trap worth naming.** M4 is explicitly about one second VMS adapter that works with zero changes outside the adapter, and `01` §M4's reduced scope says "A second VMS adapter is not cuttable — without it, M4 is a claim, not a layer." A count of integrations is optimised by shipping nine shallow ones. The metric that matters is the **non-adapter diff**, which should be zero |
| Model parameter count, benchmark scores, MMLU/VisionScore/anything public | §8.2 |
| Lines of code, test count, coverage percentage | Optimises the code, not the customer. Coverage is a way of finding untested code, not a target; a coverage percentage as a gate is a way of writing tests that assert nothing |
| Velocity, story points, closed tickets | Two people do not need a throughput measure, and a number that measures throughput rewards splitting work into smaller pieces |
| Uptime as a headline | Uptime is a health input, not a product outcome. The product's SLOs are stated as counts and thresholds precisely because "ingest availability 99.5%" is not actionable at 3am and "more than 5 accepted events lost in a day" is *(real — `slo.go:12-13`)* |

### 8.2 AI benchmark scores without a product gate

| Not tracked | Why |
|---|---|
| Public benchmark scores (MMLU, VisionScore, LLaVA-style, any leaderboard) | They do not measure **this profile on this site at this camera angle at 3am**, which is the only thing that matters. A profile that scores well on a public benchmark and fails on a customer's night-time footage is exactly the "shipped a model because it felt right" failure the evaluation harness exists to prevent *(real — `evaluation.go:7-11`)* |
| Any model score without: an evaluation dataset, a promotion gate, a shadow run, and a per-class case count | A score with no gate is a number in a slide. A score with a gate is a promotion decision |
| "AI accuracy" as a single number | It is at minimum four numbers (§5.2), over a denominator that excludes three buckets (§5.2), on a profile grouped by profile rather than by class, with a minimum case count that a Wilson interval shows is not sufficient for the claim (§5.5). One number called "accuracy" is how all four of those facts get lost |
| Model output confidence scores, as a reported metric | The model's confidence is not calibrated against anything we have measured, and reporting it invites using it as a threshold. A rule consuming a confidence value is consuming an unvalidated Tier C signal; if the product wants that, it declares `if_ai_unavailable` and the degradation is tested like any other |
| Per-model cost per inference, before M2 | There is no pricing and no unit economics. Once there is, cost per **resolved** incident is the number, and cost per inference alone is optimised by never inferring |

### 8.3 Anything that would optimise a number instead of the operator's outcome

The three that are most likely to appear, and the reason each is refused:

| Tempting metric | What it would actually optimise | What we track instead |
|---|---|---|
| **Automation rate** (share of incidents handled with no human) | Closing incidents without a human, including the ones that should not have been closed | `operator_intervention_rate` **with** `missed_incident_rate`, `ai_false_negative_rate` and `evidence_completeness` beside it. `01` §M7: "Automation alone is not success. Trusted automation is success" |
| **Human attention minutes per 1,000 events, alone** | Making events less eventful — by deduplicating, filtering, and suppressing, all of which are legitimate things the product will do from M3 | The North Star **with all four companions**, or the report is not issued (§1.2) |
| **MTTI, alone** | Investigating only the easy incidents, and closing the hard ones later or not at all | MTTI **as three components** (detection, investigation, total) **per class with N**, beside `missed_incident_rate` on the same classes in the same window (§4.5) |

### 8.4 The general rule

> A metric is added only if it is read by a named person, at a stated cadence, and
> the decision it informs is written down. Adding one is a change with a cost
> *(real — `01` §"Product analytics")*.

The test before adding anything, and it is the same test the intake filter uses
(`` §5):

1. **Who reads it, and what decision does it change?** "The team" is not an answer.
2. **What would we do differently at a different value?** A metric where no value
   changes a decision is a number in a chart.
3. **What is the failure mode of gaming it?** If the answer is not "it would be
   hard", the metric is either unusable or actively harmful, and it belongs in §8
   with a note explaining why.

And the last one, because it is the one that actually keeps this honest:

4. **Does it survive the operator being right and us being wrong?** The operator's
   outcome is the ground truth this whole program is a proxy for. A metric that
   improves while an operator says the product is worse has measured the wrong
   thing, and the fastest way to find out is to ask the operator — which is why the
   operator's own unprompted assessment is recorded in every MTTI study and in
   every site health note, and why it is reported beside the number and never
   merged into it.

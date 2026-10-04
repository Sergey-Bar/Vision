# Open decisions

Every item here is a decision an agent is not allowed to make, with a recommendation
attached so that signing it costs a reading rather than a research project.

`AGENTS.md` is explicit: *"An AI agent may not change an invariant, change a published
contract, activate a capability, contact anyone, or commit to a date. It may write
code, tests and documentation, and it may propose. A human signs every exit gate."*

So each entry is written as: the question, what the tree currently does, the
recommendation, and what signing or declining it costs. **Nothing here is a decision
already made.** Where the work has already forced a shape — because a control could
not be wired any other way — that is stated plainly as a fact about the tree and left
for you to ratify or reverse.

---

## D-01 — Where does an AI request actually go?

**The tree does not know.** `internal/nlsearch` does not exist, so there is no code
that makes a model request, no host it names, and no record of what was sent.

**What the plan wants:** a natural-language search that compiles a question into a
structured query, and a *pinned* host for the request — one named in a committed
manifest, with the prompt and its hash beside it, rather than a URL from
configuration.

**Recommendation: pin the host, keep the prompt in the repository, and treat a host
change as a contract change.** A model request leaves the customer's site. The
defensible position is that the destination is a build-time decision a human makes, not
a deployment-time one an operator can change without anyone noticing where their
evidence went. That is a different posture from every other setting in this product,
and it should be a decision rather than an accident of how the first client was
written.

**Cost of declining:** the first NL search client decides this by accident, and
retrofitting a pinned host means re-reviewing every deployment that has already
pointed elsewhere.

---

## D-02 — Is the model allowed to see the video?

**The tree does not know**, for the same reason: there is no request yet.

**Recommendation: no, and the refusal should be structural.** The evidence store holds
raw frames. A request that can reach it can exfiltrate it. The plan's own rule is that
an AI observation is *not* stored raw and is not replayable from stored facts, and a
model that sees the pixels contradicts that.

**Cost of declining:** the first implementation sends frames, and nothing in the
architecture stops the second one sending more.

---

## D-03 — What happens when the model is unreachable?

**The tree already answers this, and it is the right answer.** A missing AI
observation is never a negative one: `internal/rules` refuses to treat degradation as
corroboration, `usableAIObservation` gates on it, and the console renders a notice
naming the reason. The degradation reasons are an enum with five values and a
`NOT NULL` reason beside every degraded decision.

**Recommendation: ratify the existing behaviour and add the refusal reason to the
audit ledger.** What is *not* yet recorded is *why* the model was unreachable on a
given decision, on the row itself. The reason is on `degraded_reasons`; it is not on
the audit ledger entry.

**Cost of declining:** nothing today, and a growing answer to "why did this incident
have no AI observation?" that has to be reconstructed from a log.

---

## D-04 — How long is a step-up proof good for?

**The tree answers this, and the answer is deliberate.** `ConsumeStepUp` is
single-use: a proof is spent by the operation it was presented for and cannot be
replayed. The console disposition form spends it *before* recording the disposition,
so a failure to record costs an operator a re-authentication rather than leaving a
usable escalation token behind.

**Recommendation: ratify single-use, and add an explicit expiry as the next change.**
There is currently no time bound — a proof minted at 03:00 and presented at 09:00 is
accepted if it was never spent. Single-use bounds the blast radius; a time bound
would bound the window.

**Cost of declining:** a proof obtained by shoulder-surf survives indefinitely on a
machine that never presents it.

---

## D-05 — Does the audit ledger need a write-only database role?

**The tree says no.** `audit_ledger` is append-only by design and the application role
can read it; `internal/audit` refuses an edit at the Go level and the ledger's
hash-chain would detect one at the verification level. A write-only role would make the
second check unreachable by construction, which sounds good until you need to explain
what happened.

**Recommendation: keep the application role, and add the database-level guarantee as
a separate migration with its own review.** The Go check is a guard against a bug; the
database check is a guard against a bug in the guard.

**Cost of declining:** the hash-chain is the only thing standing between a mistake and
a silent edit, and it is checked after the fact rather than at write time.

---

## D-06 — Is a dead letter visible to the operator?

**The tree has now answered this by deletion.** `CounterDeadLettersVisible` was in the
unproduced-metric ledger as *"sits beside `CounterInboxDeadLettered` with no stated
difference"*, and it is **deleted** rather than wired.

**Recommendation: ratify the deletion.** A "seen" counter has to be incremented by
whoever renders the dead-letter list, which means it counts operator *attention* rather
than a system state — and every value such a series can take is a function of how often
somebody looked. The operator-attention number this product cares about is
`GaugeIncidentOpen`, and it is defined as states an operator watches rather than events
they accumulate.

**Cost of declining:** the ledger has to be reopened, or the decision has to be
re-derived from the deletion's reasoning.

---

## D-07 — What is the supported scale?

**The tree does not state one.** There are floor numbers — eight packages each with a
pool, `max_connections` raised to 300 on the test instance because the default 100 is
exceeded — and no claim about tenants, sites, cameras or events per day.

**Recommendation: state the number, and state it as what was measured rather than what
was designed for.** Every number in this repository that has been checked has turned
out different from the one in the document.

**Cost of declining:** every capacity conversation starts from an assumption.

---

## D-08 — Do we ship Windows?

**The tree is Windows-first for development and Linux-first for deployment.** The
database-backed tests run on Windows here; the product installs on Linux. The new
`diskFreePercent` needed both halves for the first time — `syscall.Statfs` does not
exist on Windows, and Windows' `syscall` exports no `GetDiskFreeSpaceEx` wrapper, so
the Windows half goes through `syscall.NewLazyDLL` with four `runtime.KeepAlive`
calls and a `gosec` suppression written at the call site.

**Recommendation: Linux is the supported platform; Windows is a development platform
and the code that runs on it is best-effort.** The unsafe block is the argument: it is
written down, it is suppressed rather than excluded, and it would be the first thing
to review if Windows support ever became real.

**Cost of declining:** the suppression becomes a permanent exemption rather than a
documented trade.

---

## D-09 — What is the migration rollback path?

**The tree has expand/contract and a compatibility window, and that is the answer.**
`migrations/compat.json` is a real window with a lower and an upper bound, a binary
refuses to run outside it, and every migration notes why widening is safe. Migration
0023 widened it to 23 precisely because it adds no column and no data.

**Recommendation: ratify, and add a documented rollback for the first CONTRACT step.**
Expands roll back by rolling the binary back, which the window makes safe. The first
contract step has no automatic path and should have one written before it ships.

**Cost of declining:** the first destructive migration is also the first one where
"roll back" has to mean something.

---

## D-10 — Who owns the threshold for a false-positive rate?

**The tree has a floor, not a threshold.** `covers` is a floor that fails when a
package's committed coverage moves — upwards too, because a rising floor is a request
to raise it. There is no false-positive *rate* and no cost model.

**Recommendation: the product does not own one, and that is the decision.** A threshold
implies a number somebody agreed to, and this product's whole argument is that a
detection which cannot be explained is not a detection. A rate would be a commitment
to a number nobody has measured.

**Cost of declining:** the roadmap's gate 3 stays open, and stays open honestly.

---

## D-11 — What does a site-less row mean?

**This one is already decided by the code and needs your name on it.** Migration 0023
gives `jobs` and `outbox_events` a `site_isolation` policy. `app_site_visible(x)`
returns **true** when `x IS NULL`, and `site_id` is nullable on both, so:

> **a site-less row on these tables is TENANT-scoped, not site-scoped.**

That is the existing helper's semantics and the migration did not change it, because a
check that refused NULL would break the worker that enqueues a site-less job — and
that failure appears under load, not at migration time.

The question is what a site-less job *means*. Three readings, and they have different
consequences:

| Reading | Consequence |
|---|---|
| a system-level job for the whole tenant | current behaviour; every principal in the tenant can see and enqueue it |
| a job that belongs to no site yet and will get one | correct today, wrong the moment a site-less job is read as tenant-wide |
| a bug that should be refused at insert | the strongest isolation; requires every enqueue path to carry a site |

**Recommendation: the second reading, enforced.** A site-less row should be permitted
only for the system worker, which means the check becomes
`app_is_system_worker() OR app_site_visible(site_id)` with NULL **not** passing
through. That is a contract step, not an expand, and it needs its own migration and
its own review.

**Until this is signed, the behaviour is the first row of that table** and the audit
ledger records a NULL site for any job enqueued without one.

---

## What is NOT in this document

Nothing here is an invariant, and nothing here has been decided by an agent. The five
invariants in `AGENTS.md` are unchanged by any of it, and the two items below are the
ones where a decision has already been forced by a control and could not have gone the
other way:

- **`user_site_assignments` is not site-scoped.** Scoping the table that *defines* the
  site scope by the site scope is circular: reading a principal's assignments needs
  `app.site_ids`, and `app.site_ids` comes from reading the assignments. Migration 0023
  excludes it for that reason and the exclusion is deliberate.
- **`dbx.TenantContext.System` exists.** A transaction that needs a named tenant *and*
  the site-scope exemption has no other way to express itself, so `identity.BootstrapAdmin`
  uses it to read the site it is about to assign an administrator to. It is an
  escalation and it is on the type so that it is visible.

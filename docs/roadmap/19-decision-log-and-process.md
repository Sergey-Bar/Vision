# Decision Log and Decision Process

Owner: E1 (accountable), both are subjects. Status: living. Last revised
2026-09-28.

**What this document is for.** In a two-person company the risk is not
bureaucracy. It is **amnesia and re-arguing**. A decision is made on a Tuesday
in March for a reason that is obvious to both of us in the room and invisible to
both of us in August. Then in August somebody — or something — re-opens it, the
re-opening is framed as an improvement, and the better answer replaces a
settled one that was correct.

This is not a theoretical risk in this specific repository. This codebase is
built by two people *with heavy AI assistance*, which means:

- **A future engineer — including an AI agent with no memory of this
  conversation — needs to know what was decided, why, on what evidence, and
  what would reverse it.** All four. The fourth is the one that is usually
  missing and it is the most valuable: a decision without a stated reversal
  trigger is a decision that will be re-litigated the first time somebody
  disagrees with it.
- **The AI makes amnesia structural rather than incidental.** An agent asked
  "should we add regex to the rule language?" has no way to know that this was
  considered and refused with a stated reason. Without §5's settled index it
  will happily re-open a question that was closed on principle. The index is
  what makes the repository's decisions *legible to a machine*, and that is a
  security property as much as an engineering one.
- **`AGENTS.md` already encodes part of this**, and this document **reconciles
  with it rather than contradicting it**. Where `AGENTS.md` says something,
  `AGENTS.md` wins. Where `AGENTS.md` is silent, this document fills the gap.
  The differences are listed explicitly in §4.4.

---

## 1. What counts as a decision

A decision is anything where, had we chosen differently, the code, the
contracts, the schema, the roadmap, or a customer's expectations would be
different. That definition is broad on purpose, and it is broad in the
direction of "write it down", because a two-person company's entire memory is
two people.

**What is not a decision** and does not get a record: a bug fix, a test, a
refactor with no behaviour change, a wording fix, a dependency bump inside a
declared policy, a formatting change. These are *work*, and the memory that
matters for them is `git log` with the message discipline in `12` §1.3 (one
line per change, naming the invariant or gate it touches).

**The asymmetry to notice.** It costs almost nothing to record a decision that
turns out to be obvious. It costs a quarter to reconstruct one that was not
recorded. Every threshold in §2 is set low on purpose for that reason.

---

## 2. The decision taxonomy

Eight categories. Each has a **threshold** — the thing that forces a written
ADR rather than a conversation — an **approver**, and a **record** of where the
result lives.

### 2.1 Architecture

**Threshold for a written ADR.** Any change that alters a component's
responsibility, any new process, goroutine, datastore, or protocol family, any
change to how data crosses a trust boundary, and any change that would require
editing more than one `internal/` package to implement.

**Approver.** E1 (Product/Architecture is E1's role per `03`).

**Record.** `docs/adr/NNNN-*.md`, indexed in `docs/adr/README.md` and mirrored
into §5's settled index.

**Standing constraints that are already decisions, not preferences.** `00` §6:
no new runtime, no new datastore, no new protocol family without an ADR. The
rule language has no shell, SQL, network, dynamic code or regex. The
deterministic core stays functional with the AI plane entirely absent. These
are §5 rows, and an ADR that weakens one is an ADR that needs the other person
explicitly.

### 2.2 Contract (the wire)

**Threshold.** Any change to `contracts/` at all — a field, an enum value, a
type, cardinality, a `oneof` membership, a JSON Schema property — **and any
change to `contracts/compat/baseline.json`**.

**Approver.** E2 builds, E1 reviews; a baseline change is **both**, because
`07` §2.2 makes the point that nothing in CI stops someone running
`-mode write` and committing the result, and the control that protects the wire
is a review rule.

**Record.** The contract diff, the ADR, and the release-gate line (E2 signs
`contract-check`, E1 signs the API/rule-language/migration diffs).

**The existing bug in this category**, because it is exactly the case the
process exists for: `07` §2.2 names it. "`make contract-check` compares against
`contracts/compat/baseline.json` … **nothing in CI prevents someone running
`-mode write` and committing the result.** The gate only detects an
*unintentional* break. The control that actually protects the wire is a review
rule: a diff to `contracts/compat/baseline.json` is an ADR, every time, with the
removed fields listed. Add a check that the baseline's git history contains
exactly one commit per intentional version bump, keyed to an ADR filename."
**Still not done. It is a §6 open decision with a date.**

### 2.3 Schema and migration

**Threshold.** Any migration. Any change to a `NOT NULL`, a `CHECK`, a foreign
key, an RLS policy, a grant, or a role. Any change to how the tenant context is
established.

**Approver.** E2, with E1's agreement for anything touching invariant 5.

**Record.** The migration file (forward-only, phase in the filename), the
`migrations/` test results, and an ADR if it changes a *law* rather than
applying one.

**The standing laws** (`AGENTS.md`, §5): expand and contract ship separately; a
contract step may never be bundled with the expand step that makes it safe;
migrations are forward-only; an applied migration is never edited; SQL lives in
`migrations/`, never inline in Go.

### 2.4 Dependency

**Threshold.** Every new dependency, and every *major* version bump of an
existing one. No exceptions for "it's a patch" on a `golang.org/x/*` module,
because those move together and `x/crypto` is the Argon2id and TLS path.

**Approver.** E2 proposes, **E1 approves** — because the decision is about the
product's six-month-old isolated install, not about the code.

**Record.** The ADR, per `13` §3.4: context, why the standard library or an
existing dependency will not do, the transitive set, the licence, whether it is
vendored, and what breaks if it is removed.

**The test, quoted, because it is the whole policy:** *does a six-month-old
release still install on a customer's isolated network?* A dependency that
needs a registry, a proxy, a runtime download, or a licence check is declined
on that basis alone.

### 2.5 Security posture

**Threshold.** Any change to the threat model, to an accepted risk, to a
control in `docs/threat-model.md` §5, to a trust boundary, to the release
verification path, to the key management story, or to the disclosure policy.

**Approver.** **Both, always.** Security posture is not E1's call alone even
though `03` makes E1 accountable for the security role, because the second
signature is the control: a two-person company that lets one person change
security posture unilaterally has one person in the security model.

**Record.** The threat-model diff, the ADR if a control changed, the §5
invariant row, and the test that fails when the control is removed.

**The ordering law**, from `AGENTS.md`, is not negotiable and is the reason the
ADR threshold here is low: *"A change that weakens one needs an ADR and a
test, **in that order**."* Writing the test first is a test of the new
behaviour, which is not the same thing.

### 2.6 Product scope

**Threshold.** Anything added to a milestone; anything removed from one;
anything that changes an exit gate, a reduced-scope cut, or a milestone's
mission. **And any feature that cannot answer the three questions in `13` §1.2
— the security decision, the UX surface, the audit surface.**

**Approver.** E1, with both for anything that touches an exit gate (because an
exit gate is signed by two people and one cannot sign alone).

**Record.** The milestone plan, the ticket's three-question block, and an ADR
for anything that changes what the product *is* rather than what it contains.

**The standing law.** `00` §6: "Scope reduction is the only allowed response to
a slip. Not more hours, not a third stream of work." And `00` §6's test for a
new feature: "does it deepen the intelligence layer above the installed security
stack, or does it grow us into a different company? A feature that fails that
test is declined even if a customer asks for it in writing."

### 2.7 Pricing, packaging and commercial

**Threshold.** Any price, any discount, any licence term, any free-versus-paid
boundary, any contract term, and anything a customer may rely on.

**Approver.** E1, with both for anything that becomes a contractual commitment
(`14` §8's clause list).

**Record.** The commercial document, the contract, and the design-partner
agreement text — which is a record because `12` §3.3 and §7.3 both put specific
*sentences* in agreements and those sentences are commitments.

### 2.8 "Stop doing X"

The category people skip, and the one that most needs a record.

**Threshold.** Any decision to stop, pause, defer, or permanently decline
something: a capability, a platform, a support commitment, a customer segment,
a feature line, a dependency, a process.

**Why it needs its own category.** "Stop doing X" decisions are invisible in the
code and get re-litigated hardest, because the next person (or the next
instance of the same agent) reads the roadmap, sees the absence, and reads
absence as an oversight rather than a decision. **An explicit decision to stop
is a fact; an unexplained absence is not.** And in a two-person company the
person who remembers deciding to stop is sometimes the person who is on holiday.

**Approver.** E1, with both for anything that was ever promised externally.

**Record.** The ADR, with a **stated reason**, and — for the ones that are hard
to reverse — a **stated trigger that would make us restart it.** Hebrew/RTL is
the model example (§5.10) and the trigger is already written: it would be
revisited only if a customer segment required it, not if a founder found it
elegant.

---

## 3. The ADR process

### 3.1 The template

Every ADR is this, in this order. Eleven sections, and the last two are the ones
people skip and the ones that make the document worth reading in two years.

```markdown
# ADR-NNNN: <the decision, in one imperative sentence>

Status:   Proposed | Accepted | Superseded by ADR-NNNN | Withdrawn
Date:     YYYY-MM-DD
Deciders: E1, E2                     (both for §2.5; one for most others)
Affects:  <packages, contracts, migrations, milestones, documents>
Tracked:  <issue or milestone item, if any>

## Context
What forced the question. The state of the world *before* the decision, in
enough detail that somebody who was not in the room understands why the
obvious answer was not taken. Two to five paragraphs. State the constraint
that made this hard — constraints are the part that is forgotten, and
forgetting them is how a settled decision gets re-opened.

## Decision drivers
The three to five things that actually mattered, in order. If one of them is
"a customer asked", say so and say whether it was a named customer.

## Options considered
Three or more, each with: what it is, what it would have cost, and **why it
was not chosen**. The option that was nearly chosen gets the fullest
treatment, because that is the one a future reader will propose again.

## Decision
The decision, in the active voice, in one or two sentences. "We will X."
Then: what specifically will not happen, because an ADR that says only what
will happen gets read as a licence for everything it did not mention.

## Consequences
Positive, negative, and — separately — **what this makes harder later**. The
third list is the one that changes behaviour, because every real decision has
one and a document that omits it is a document that will be regretted at the
exact moment the cost arrives, with no warning.

## What would reverse this
The specific evidence or event that would make us change our mind, stated so
that somebody else can recognise it. "If performance becomes a problem" is not
a reversal trigger. "If a profile's p95 median latency exceeds 8s on a design
partner's hardware for a full shadow period" is.

## Follow-ups
What has to change as a result, with owners and dates. An ADR with no
follow-ups is usually a decision that was not actually made.
```

### 3.2 Numbering, location, lifecycle

| | |
|---|---|
| **Directory** | `docs/adr/` — **it does not exist yet and creating it is the first follow-up of this document** |
| **Numbering** | `ADR-0001`, `ADR-0002`, … **sequential, never reused, never renumbered.** Sequential because at two people a gap-free run is easier to scan than an RFC-style number that implies a process we do not have |
| **Filename** | `docs/adr/NNNN-kebab-case-title.md` |
| **Index** | `docs/adr/README.md`, a table of number, title, status, date, and one-line summary. Plus a mirror of the *settled* entries in §5 of this document, which is the copy an agent will actually read |
| **Lifecycle** | Proposed → Accepted → Superseded by ADR-NNNN, or Withdrawn |
| **Exception, existing** | `docs/threat-model.md` is a *living* document and is the record for security posture; it is not an ADR and is not numbered. Where a decision changes the threat model, **both** are written: the threat model diff for the current state, the ADR for the decision and its reason. The same applies to `docs/status.md` (build state) and to the roadmap documents (plans) |
| **Review cadence** | Every ADR is re-read at the milestone review. An ADR with no trigger met stays Accepted. An ADR whose trigger *is* met is **superseded, not edited** |

### 3.3 An ADR is never edited after acceptance

> **Once Accepted, an ADR is immutable. A change of mind is a new ADR that
> supersedes the old one, and the old one stays readable with its original date
> and its original reasoning.**

This is the rule the whole document exists to protect, and the reasoning is
worth stating because it will look like bureaucracy until the first time it
saves us:

- **The value is in the *reasons as they were known then*, not in the decision
  as it is now.** An ADR rewritten to reflect today's reasoning has destroyed
  the only record of what we believed and why, which is the record that would
  have told us we were wrong. The retrospective half of `07` §10.3's T+5d
  ("What we believed, why it was wrong") is only possible if the "we believed"
  was written down and left alone.
- **A rewritten ADR cannot be reviewed.** A diff on an ADR should be the whole
  decision. A diff that changes the context is a diff nobody reads carefully,
  and a review that does not happen is not a review.
- **It makes the settled index trustworthy.** §5 claims these decisions are
  settled. That claim is only checkable if the documents behind it are
  immutable.

**Withdrawn** is the exception, and it exists for one case: a decision made and
discovered to be unimplementable before it was built. It is marked Withdrawn
with the date and the reason, and it stays in the file. "Withdrawn" is not
"reversed" — a reversal is a new ADR.

### 3.4 The first five ADRs, which are overdue

Named here so the directory is created with content rather than structure.

| # | Title | Why it is overdue | Approver |
|---|---|---|---|
| **0001** | The fifth invariant: `tenant_id` scoping, or bundles-are-untrusted | `AGENTS.md` and `README.md` state two different fifth invariants (`13` §9.3's R11). Both are in force and both are law, but the repository does not say so anywhere a reader would look | both |
| **0002** | `sites.default_locale` stays in the schema | `AGENTS.md` says it stays; `docs/status.md` deviation #9 says migration 0010 removes it. One of them is wrong and the newer intent is `AGENTS.md`'s (`13` §9.3's R10) | both |
| **0003** | The contract baseline is a review control, and CI will assert it | `07` §2.2 identifies the gap; nothing stops `-mode write` from being committed (§2.2 above) | both |
| **0004** | Release reproducibility: content-identical, not byte-identical | `13` §3.2's SC-6. Timestamps are inside the signed manifest, so two builds of one commit produce different signatures. We need a published content digest excluding timestamps, and a definition of "reproducible" in `12` §1.2 that we can keep | both |
| **0005** | Telemetry: never, or customer-initiated and scrubbed | `14` §4.2's DR-1, target 2027-01-15. A security product that phones home is a product an air-gapped site cannot run (`12` §3.1), and "we will never" is a promise that is much cheaper to keep than "we will not, unless…" | both |

---

## 4. Rules of engagement for AI agents

### 4.1 What an agent may decide alone

Low blast radius, easily reversed, and a human reviewing the diff would spend
more time deciding than doing:

- **A test**, including a generated test table, a negative case, or a fault-
  injection harness's boilerplate (`07` §11.2).
- **An internal refactor with no behaviour, contract, schema or interface
  change**, including extracting a helper, renaming a local, or restructuring a
  package.
- **A doc fix** — a broken link, a command that no longer exists, a stale line
  number, a `docs/status.md` correction **provided it does not move a `green`
  marker**.
- **Generated scaffolding** whose design is already settled: fixtures, test
  helpers, a migration template, a matrix assembly script.
- **A negative test for a function that already exists**, including a path
  validator, a JSON schema, a command dispatcher or an HTTP handler.
- **Mutants**, for the purpose of finding vacuous tests.

**The standing condition on all of it:** every generated artefact is read by a
human before it lands, and **the human's job is to be able to answer "what does
this fail on" for every assertion.** `07` §11.2 states it as a hard line and it
is the whole defence against a suite that is large, green, and worthless.

### 4.2 What needs a human diff review before it lands

The list is not "risky changes". It is **specific named surfaces**, because a
principle is not enforceable and a list of paths is.

| Surface | Why | The review that is required |
|---|---|---|
| **Anything in `contracts/`** | the wire; a break is a break for every client | `make contract-check` green **and** a named review of the diff. A diff to `compat/baseline.json` is an ADR every time |
| **Anything in `internal/contracts/`** | generated code nobody may hand-edit (`AGENTS.md`) | **May not be edited at all.** Only `make generate` writes it, and CI's "generated code is up to date" step is the check |
| **Any migration** (`migrations/*.sql`) | forward-only; a bad one runs on a live site | Phase in the filename; `make migration-phases`; an ADR if it changes a law; **and the "is this step one transaction" question answered in the file's header comment** (`07` §2.3's named quality incident) |
| **Any change to an RLS policy, a grant, a role, or the tenant context** | invariant 5 | A named test that fails when the control is removed. `schemalaws` is the file and the 24 tests in it are the standard |
| **Anything in `internal/identity/`** | authentication, MFA, sessions, capabilities | A named test from the 21 in `identity_test.go` that covers the new path. **And the WebAuthn and TOTP defects in `13` §6.2 are on this surface, so this is currently the highest-risk area in the repository** |
| **Anything in `internal/rules/`** | the untrusted-input boundary | A test about the **absence** of a capability (`TestNoUnboundedRegexOperatorExists` is the model), not a test that the new operator works |
| **Anything in `internal/evidence/`, `internal/release/`, `internal/fixture/`** that touches a path, a ref, a root, a join, or a scenario name | path traversal | `07` §6.4's review rule, in the same diff: the four negatives re-read and passing, a line naming which function is the evidence path, and a note if a new filesystem call was added |
| **Anything in `.github/workflows/`** | the build and the trust path | E2, because a compromised or edited workflow is a supply-chain attack (profile P4) |
| **Anything in `.golangci.yml`** | a disabled linter is a removed control — `gosec` is currently disabled and that is a finding | Both. Turning a linter **on** is uncontroversial; turning one **off** is a security-posture decision (§2.5) |
| **Anything in `internal/audit/`** | invariant 4 | A named test, and the question "does the hash chain still compute in the database rather than in the application?" |
| **Anything in `internal/checkpoint/`** | the seal and the trusted key | A named test, plus: does this change what verifies **offline with no database**? |
| **The five invariants themselves** | they are the law | **An agent may not do this at all.** See §4.3 |

### 4.3 What an agent may not do at all

Not "may not do without asking". **May not do.**

- **Change an invariant.** Not weaken it, not reinterpret it, not "temporarily"
  for one release. An invariant change is an ADR *and a test, in that order*
  (`AGENTS.md`), which means a human wrote the ADR before the test existed. An
  agent cannot be the author of that.
- **Change a public contract**, in either direction. Additive fields go through
  the ADR route; removals, type changes, enum removals, cardinality changes and
  `oneof` moves are major-only and are a human decision. **Writing code that
  would *require* a contract change is also in scope** — if the only way to
  implement a feature is to change the wire, the agent stops and reports that,
  rather than implementing it and leaving the contract to be discovered.
- **Activate a capability.** This means, concretely: enable a feature flag, turn
  on an API route, promote a rule bundle, activate a rule set, promote an AI
  profile out of shadow, raise an autonomy level, or ship a contract step. `01`'s
  sequence dependencies are the rule: "AI profiles gate on evaluation … shadow
  first, gates named individually, never a single boolean"; "The AI never
  activates its own rule"; "the AI never activates its own rule" at M6.
- **Contact anyone.** No customer, no vendor, no auditor, no lawyer, no
  researcher, no user of the product. A message an agent sends is a commitment
  from a company with two people and no review step. **This includes the
  seemingly harmless ones**: a support reply, a disclosure acknowledgement, an
  email to a design partner.
- **Commit to a date.** No milestone date, no gate date, no release date, no
  "we will have this by". Dates in this plan are commitments subject to a named
  gate (`00` §8) and a date an agent invents becomes a commitment nobody
  reviewed.
- **Touch a `green` marker in `docs/status.md`.** Green means "covered by a test
  that would fail if it broke" and that is a claim about *adequacy*, not
  coverage. `07` §11.2: "Only a human promotes a capability to green."
- **Sign an exit gate.** `00` §6: "AI multiplies implementation, not judgement …
  it does not decide what to build, decide whether a decision is defensible,
  talk to a customer, or sign an exit gate." Every gate is signed by a human,
  and `07` §2.8 is blunter: "A gate signed by one person is not a gate."
- **Write the test for a durability change on its own.** `07` §5.3: the
  fault-injection harness's *assertions* are where the knowledge of what the
  invariants mean at that stage lives, and that knowledge is E2's. The agent may
  write the child-process boilerplate; it may not write what the stage must
  prove.
- **Decide that a control is adequate.** An agent cannot know which control a
  rule is *supposed* to enforce, and `13` §2.3's S-02 is a register row precisely
  because three documented controls had no code behind them. **Auditing
  control-to-test completeness is the highest-value agent task in this
  repository and it is not a judgement task — it is "find the row whose named
  test does not exist".**
- **Accept a risk.** `16` §1.3: an agent may add a row and may not set a state
  other than Open.
- **Change a legal or contractual artefact.** `14` §9: a product artefact may be
  generated from the registry because a generated document generated from the
  code cannot drift from the code; a *legal* artefact is not a product artefact.
- **Decide, rather than recommend, on the eight open decisions in §6.** An agent
  may prepare the analysis, the options table, and a recommendation. The decision
  is human, and for OD-1 (offline licensing) and OD-5 (telemetry) it is
  contractual.

### 4.4 Where this reconciles with `AGENTS.md`, stated explicitly

`AGENTS.md` is the working agreement and it wins. This document does not
contradict it; it fills the gaps and adds the process around it. The mapping:

| `AGENTS.md` says | This document | Relationship |
|---|---|---|
| "A change that weakens one needs an ADR and a test, **in that order**" | §2.5's ordering law, §4.3's "may not change an invariant" | **`AGENTS.md` is the rule; §4.3 is the enforcement for agents.** Same rule, one is addressed to a human reader of a file and one to a process |
| "Contracts are the source of truth … Never hand-edit `internal/contracts/`" | §2.2's threshold, §4.2's "may not be edited at all" | Identical. `AGENTS.md` says it to everyone; §4.2 says it in the agent's terms |
| "Rule bundles, release archives and model bundles are untrusted input … the threat model owns the answer, not the convenience of the call site" | §2.5, `13` §3.7 | Identical in substance. `13` adds the per-input table |
| "No new runtime, no new datastore, no new protocol family without an ADR" (`00` §6) | §2.1's standing constraints | `00` states the rule; §2.1 states the threshold and the approver |
| "Migrations are forward-only. Never edit an applied migration" | §2.3 | Identical |
| "Path traversal … needs an explicit negative test on every evidence path, on every change" | §4.2's `07` §6.4 review rule | Identical, and `07` §6.4 makes it mechanical |
| "Golden tests are deterministic-only … Never golden-test VLM prose" | §5.11 | `AGENTS.md` is the law; `07` §4 is the reasoning |
| — | §4.1's "an agent may decide a test alone" | **A gap `AGENTS.md` does not cover.** Filling it, not contradicting it |
| — | §4.3's "may not contact anyone", "may not commit to a date" | **A gap.** `00` §6 says AI does not "talk to a customer" or "sign an exit gate"; this makes the prohibition operational and names the cases that look harmless |
| — | §3.3's immutability rule | **A gap.** `AGENTS.md` has an ordering rule for invariant ADRs, not a lifecycle rule for ADRs generally |
| — | §2.8's "stop doing X" category | **A gap.** The most re-litigated category, and the only one `AGENTS.md` does not mention |

**One thing this document does *not* do:** it does not restate or paraphrase the
five invariants. `AGENTS.md` holds them, they are the law, and a second copy in
a second document is a second thing to keep in sync. §5 references them by
number and links.

---

## 5. Settled decisions index

**The decisions this codebase and this plan have already made.** Seeded from a
read of the code and of `docs/status.md`'s deviations list, and from `00`, `01`
and `AGENTS.md`.

**How to use this section.** If a proposal touches one of these rows, the
answer is *here, with the reason, and with the trigger that would reverse it* —
not "let me think about it". **Re-opening a row requires new evidence.**
"I have thought about it more" is not evidence. "The situation has changed
because X" is.

| # | Decision | Why (one line) | Where it is written down | What would reverse it |
|---|---|---|---|---|
| **5.1** | **Five invariants: zero accepted event loss; replay from stored facts; a missing AI observation is never a negative observation; the audit ledger is append-only and hash-chained; `tenant_id` is `NOT NULL` everywhere and never read from a request body** | They are the product's claim. Everything else exists to protect one of them | `AGENTS.md` §"Invariants that tests must protect" (the law); `README.md` §"The law"; per-invariant control→test tables in `07` §3; `13` §9.1's matrix | Nothing in this plan. A change needs an ADR **and** a test, in that order |
| **5.1b** | **Bundles are untrusted input** — `AGENTS.md` states this as a working agreement; `README.md` states it as invariant 5 | The rule language, the archive verifier and the model-bundle installer all depend on it, and `07` §3 has to carry it as a separate "Invariant 5b" row because the two documents disagree about slot 5 | `AGENTS.md`; `README.md`; `07` §3's 5b table; `13` §9.1's last row | ADR-0001 will reconcile the two lists. Until then **both are in force** |
| **5.2** | **Expand and contract migrations ship separately, and a contract step may never be bundled with the expand step that makes it safe** | It is the mechanism that makes a rolling upgrade and a rollback possible. Bundling them makes a half-upgrade reachable and a rollback require a restore | `AGENTS.md`; `internal/release/release.go:244-252` refuses an archive containing both, with the reason in the code; `TestContractStepAlongsideExpandIsRefused`; `07` §2.6; `12` §1.1 | Nothing practical. A single-transaction migration is not a small transaction |
| **5.3** | **Contracts first; `contractcheck` decides whether the wire is broken; nothing hand-edits `internal/contracts/`** | It is the only way a schema is owned by nobody and therefore correct for everybody. CI's "generated code is up to date" step is what makes the rule real | `AGENTS.md`; `Makefile`'s `generate` / `contract-check` / `contract-sync`; `tools/contractcheck`; `07` §2.2; the `contract` job in `.github/workflows/ci.yml` | Nothing. The gap is not the rule, it is the *baseline* (ADR-0003) |
| **5.4** | **The rule language has no shell, SQL, network, dynamic code or regex** | A customer-supplied bundle is untrusted input, and an unbounded pattern is a denial-of-service primitive in a language an operator can be persuaded to upload. It is also, structurally, why a rule cannot match model prose | `AGENTS.md`; `internal/rules`; `rules.TestNoUnboundedRegexOperatorExists`, `TestFieldValueListIsBounded`; `07` §2.6, §7.3; `13` §3.7 | A capability that genuinely cannot be expressed without one of these. It would need an ADR and a proof that the safe form is expressible |
| **5.5** | **The audit hash chain is computed by a database trigger over a length-prefixed encoding, and the application never computes a chain hash itself** | If the application supplied `prev_hash` and `hash`, the chain would mean "whatever the last writer believed", and one buggy writer would break every row it touched. Length prefixing removes field-boundary ambiguity, so actor `"a|b"` cannot collide with actor `"a"` and action `"b"` | `internal/audit/audit.go`'s package comment, stated as "the whole design"; `docs/status.md` deviation 6; `audit_verify_chain($1,$2)`; `schemalaws.TestTamperedRowBreaksChainVerification` | Nothing. Moving the computation into the application would be a downgrade and would need an ADR explaining why |
| **5.6** | **Evidence is content-addressed: the reference is derived from the hash, never supplied by a caller** | It removes a whole class of question — "there is no path for a caller to supply" — and it deduplicates identical bytes across a tenant without letting deduplication become a disclosure channel | `internal/evidence/store.go`'s package comment and `Put`; `<root>/<tenant>/<xx>/<sha256>`; `TestIdenticalBytesDeduplicateAndCannotCollide` | Nothing in this product. `12` §6.3 notes evidence deliberately lives on a filesystem and not in the database, and that is the same decision taken once more |
| **5.7** | **`security_core` has no `BYPASSRLS`; only the migrator does** | The core role is the one that serves requests, so it is the one that must be subject to the row-level policies. A role exemption is invisible in a query and permanent in a grant | `docs/status.md` deviation 4; `db/seed.sql`; `schemalaws.TestMigratorIsTheOnlyRoleThatBypassesRLS`; `12` §2.3.1 | Nothing. If a legitimate need appears, the answer is a scoped policy or the `app.system_worker` setting, not an exemption |
| **5.8** | **Background workers reach other tenants through an explicit `app.system_worker` setting, bounded by grants, rather than a role exemption** | It is the same reasoning as 5.7 with a narrower blast radius: an exemption would be unconditional and invisible; a setting is set per transaction, is visible in a transaction log, and is still bounded by what the role's grants allow | `docs/status.md` deviation 3; `internal/dbx/dbx.go`'s `InSystemTenant`, which does `SELECT set_config('app.system_worker', 'true', true)` | Nothing. A new system-worker path would use the same mechanism, not a new one |
| **5.9** | **Hebrew and RTL were dropped by product decision. The operator surface is English and LTR, `dir="ltr"`, `lang="en"`, and no template may carry a per-element direction override** | There is no second direction to override to, so a per-element override is an untestable abstraction over nothing. Time rendering is unaffected: a site's timezone is still the site's, and every rendered time carries an explicit offset | `AGENTS.md` §"Hebrew and RTL", including the two consequences it calls out; `docs/status.md` deviation 10; `api.TestConsoleIsLeftToRight` asserts `dir="ltr"`, `lang="en"`, **no `<script` element anywhere and no `style=` attribute anywhere** | A customer segment that requires Hebrew. Not a founder's aesthetic preference |
| **5.10** | **`sites.default_locale` stays in the schema — a rendering input, not a security control — and a site that later needs another locale should not require a migration to express it** | It is a rendering input, and encoding a rendering input in a migration is coupling the schema to today's languages | `AGENTS.md` §"Hebrew and RTL", second bullet | ADR-0002. **Note the conflict: `docs/status.md` deviation 9 says migration 0010 removes it.** Until ADR-0002 lands, the two documents disagree and the newer intent is `AGENTS.md`'s |
| **5.11** | **The console is server-rendered Go templates with no client JavaScript, which is what makes a strict CSP achievable at all** | An inline `<script>` anywhere would mean a CSP with `unsafe-inline` and no benefit, and the XSS path that matters here is an operator's browser running a script an event payload injected | `AGENTS.md`; `internal/api/server.go:5-7` and `contentSecurityPolicy` — `default-src 'none'; script-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'`; `api.TestContentSecurityPolicyForbidsInlineScript` (which also asserts neither `unsafe-inline` nor `unsafe-eval` appears anywhere); `07` §2.7, §11.3; `06` §4.2 | OD-4 in §6, and it is the decision most likely to be re-opened by a well-meant request for a better operator experience. The deciding factors are recorded there |
| **5.12** | **SQL lives in `migrations/`, never inline in Go** | A schema change that is not a migration is not applied, not checksummed, not rehearsed, and not part of the release artefact. And an applied migration is never edited | `AGENTS.md`; `internal/migrate`; `migrations/*.sql` with the phase in the filename; `migrate.TestVerifyChecksumsCatchesDrift`; `07` §2.3 | Nothing. It is a two-line rule and the cost of breaking it is unbounded |
| **5.13** | **Migrations are forward-only, and an applied migration is never edited** | A site cannot un-apply a migration. Editing one silently changes what every existing site would have received, and the checksum catches it — which is the point | `AGENTS.md`; `migrate.TestVerifyChecksumsCatchesDrift`; `07` §2.3; `12` §1.1's train rules | Nothing. A migration that is wrong is corrected by a new one |
| **5.14** | **Errors wrap with context; log lines carry `tenant_id`, `site_id` and `request_id`; no secrets, tokens, credentials or raw biometrics in logs; the audit ledger is separate from operational logs and is the authoritative one** | A bare error at the top of a twelve-frame stack is a wasted twenty minutes at 03:00, and a log line nobody can attribute to a site is a line nobody can act on | `AGENTS.md` §"Conventions"; `12` §4.3's log field table and §5.3; `Config.DSN()` omitting the password and documented safe to log; `TestSecretMaterialIsRefused`; `TestSiteConfigSchemaCannotCarryASecret` | Nothing in this product. **Note two open defects: Q-9 (the HTTP access log omits all three fields) and `identity/session.go`'s `recordAuth` writing the source IP into `audit.Entry.RequestID`** |
| **5.15** | **Time is UTC in storage; `observed_at` is what the source claimed, `received_at` is our ingest, `normalized_at` is drift-corrected, and `normalized_at` is the only field a time-window predicate may read** | It is what makes clock drift a *measurement* rather than a silent corruption of corroboration. A device 40 seconds fast must not be able to join two unrelated events into a false corroboration | `AGENTS.md`; `rules.TestTimeWindowMayOnlyReadNormalizedAt`; `internal/clock`; the severe drift band and its exclusion from corroboration; `12` §8.4; `13` §7.1 | Nothing. It is the reason the drift subsystem exists |
| **5.16** | **Storing a rule bundle is not activating it; activation requires a reviewed simulate diff digest, computed over the diff** | A bundle that could be activated in one step would be activated without anybody reading what it changes. A digest *over the diff* rather than over the bundle is what stops a review being silently invalidated by the fixtures or the active bundle moving afterwards | `internal/bundles`; `bundles.TestStoreDoesNotActivate`, `TestActivationRequiresAReviewedDiffDigest`, `TestActivationIsAudited`, `TestRollbackReturnsToThePreviousVersion`, `TestStoredContentIsImmutable`; `docs/threat-model.md` §5 C34 area | Nothing. And the same shape applies to model bundles: install is not activate (`12` §3.4) |
| **5.17** | **A release is a signed, offline-installable archive, and the verifier takes a trusted public key rather than the key the archive carries** | Without the trusted-key check the signature proves only that whoever wrote the archive signed their own forgery. And a site that can verify with no network, no database and no server of ours is the whole product's claim in one command | `internal/release`; `cmd/securityctl/release.go`; `release.TestForeignSigningKeyIsRefused`, `TestUnsignedManifestIsRefused`, `TestABuiltArchiveVerifies`, `TestUnlistedFileIsRefused`, `TestSecretMaterialIsRefused`; `12` §1.2, §3.2, §3.6 | Nothing. **Open: the trusted key is optional today** (`cmd/securityctl/release.go:158-163` warns rather than refusing) — see `13` §3.2's SC-4 |
| **5.18** | **The product makes no network request at run time — no CDN, no licence check, no telemetry, no crash reporter, no update check, no online revocation check** | A product that phones home is a product an air-gapped site cannot run, and an air-gapped site is a large part of the market. It is also a residency and a compliance answer (`14` §4.1) | `12` §3.1; `12` §3.5: "the site still verifies the signature locally before installing. The fleet is a convenience, not a trust path"; `12` §5.6: the site owner can answer 90% of "it is broken" with the product and the runbooks on their machine | **OD-5 in §6 is this decision, and it is the one place where re-opening is legitimate**, because it is a business constraint with no technical basis and a customer may force it. An ADR must state the business case, because the technical case is this row |
| **5.19** | **Local authentication remains available for isolated sites, always and without exception** | A site that has lost its IdP connection and whose accounts are federated must still have operators who can sign in. A non-requirement is a requirement | `01` M5; `12` §2.2; `13` §10.3's B11; `05`'s row #87 and its note | Nothing. A customer may federate *in addition*; never instead |
| **5.20** | **A break-glass account is a person, not a backdoor: it needs a real password, every use raises a high-priority audit event, and it cannot be created without an audit row** | Its existence is then a fact somebody knows rather than a possibility somebody has, and there is no way to use one that leaves no trace — and the trace is the feature | `internal/identity/identity.go`'s package comment; `schemalaws.TestBreakGlassRequiresAPassword`; `TestEveryAuthEventIsAudited`; `12` §8.7's "there is no way to use one that leaves no trace" | Nothing. A break-glass account with no audit trail is a backdoor, whatever it is called |
| **5.21** | **Deterministic golden tests only: fixture → canonical → verification → incident must be byte-stable, and VLM prose is never golden-tested** | A regenerated golden becomes a change-detector that always passes when regenerated, which teaches the team the suite is green — the exact opposite of what a golden test is for. And prose is not stable across a prompt rephrase, so it cannot be a contract | `AGENTS.md`; `07` §4 in full; `rules.AIObservation` has **no free-text field at all**, which is the structural version of the rule | Nothing. The structural form (no free-text field in the type) is stronger than the review rule and should not be traded away for a convenience |
| **5.22** | **The replay path has no model client, no bundle path and no clock** | It is what makes invariant 2 a structural property rather than a convention, and it is the property the entire trust story rests on | `internal/incidents`; `TestReplayReproducesTheDecisionFromStoredFacts`; `07` §3's invariant-2 table, which carries the most ADRs in the product | Nothing. `07` §3: "a capability that cannot answer [what is stored, and does this still reproduce with the model switched off?] is declined" |
| **5.23** | **`internal/fixture.SanitizeResponse` is the single choke point through which live data becomes a committed file, and the order is capture → sanitise → human review → record → commit** | A second path into the repository is a second place to forget, and "we were careful" is not a control. And capture → commit → sanitise is a customer-data disclosure that is in the git history for ever | `internal/fixture/sanitize.go`; `docs/status.md` §B; `07` §8.1, §8.3, §8.4; the five sanitiser tests including `TestRedactsOpaqueLongStrings`, which stops the sanitiser being a key-name list | Nothing. And the open extension is that the sanitiser protects the fixture path only — the operating path has no automated check, and `13` §5.2 names a quarterly manual audit instead |
| **5.24** | **The incident feed is eventually consistent and the console states the incident's state rather than implying completeness** | An operator acting on the last 15 seconds is acting on an incomplete picture, and a product that hides that is the product this company is not | `docs/threat-model.md` §6 R3; the SLO p95 15 s; `12` §5.4 | Nothing. It is a stated limit, not a defect |
| **5.25** | **The build-internality of the evidence store: evidence lives on a filesystem, deliberately not in the database** | A database loss is recoverable from a backup; a filesystem loss of a few TB of video is not, and a 2,000-camera site's evidence does not fit a backup window at all | `internal/dr`'s `TestRecoveryDrill` ("Evidence lives on a filesystem, not in the database. That is the point of the split."); `12` §2.1's three splits; `12` §2.3.4 step 2: the backup copies the *references*, not the bytes | Nothing in this plan. Copying TB of video into a backup is how a backup stops being taken |
| **5.26** | **The checkpoint sink must be outside the database's reach** | A seal stored where the database owner can rewrite it is a claim rather than evidence, which is the entire reason the sink exists | `docs/threat-model.md` B7 and R1; `12` §2.1's second split; `12` §6.4's seal-loss drill: the system must still be operable and say so | The residual is accepted: a host that can reach both can lose both. `12` §6.4 proves the *site keeps running*, which is the property we can defend |
| **5.27** | **We buy an external penetration test before M3 GA and the threat model is the scope document** | The first outside pair of eyes on the identity, the release and the evidence path is worth more than a year of our own tests, and we already wrote the scope | `07` §11.1; `13` §6.1's H1..H8; `14` §2 items 3 and 7 (a cheaper scoped review first, in M1, precisely so a structural finding arrives while M2's design is still movable) | Nothing. The scope is the threat model, so a threat-model delta re-opens the scope |
| **5.28** | **No telemetry, in any form, that identifies a site** | `12` §3.1's constraint, and the strongest data-residency answer we have | `12` §3.1; `14` §4.2's DR-1 | **OD-5.** This is the one row in the index most likely to be re-opened, and it should be re-opened by a business constraint with a written business case, not by a preference |
| **5.29** | **Corrections are superseding rows, never edits. We do not rewrite a customer's history** | A wrong incident, a wrong disposition and a wrong action are all rows in a hash-chained ledger, and they are not editable. A migration that *edits* history would break invariant 4 to fix an invariant-2 problem, and it is never the answer. A system that can be edited after the fact is a system nobody has to trust, only believe | `12` §10.3 steps 1–4 and its closing paragraph; `TestReEvaluationSupersedesRatherThanOverwrites`; the UI's undo: "both replay, the earlier one does not disappear" | Nothing. It is the same rule as an append-only ledger applied to everything else |
| **5.30** | **A sweep that cannot determine legal hold deletes nothing; the default for a destructive action whose precondition cannot be evaluated is to do nothing** | It is the one irreversible operation in the product, and a sweep that runs a check it could not complete has destroyed something on the strength of an assumption | `internal/evidence.Store.Sweep`; `TestSweepFailsClosedWhenLegalHoldCannotBeChecked`; `07` §6.3; `12` §8.3 step 1 | Nothing. This is the rule to copy for every new destructive operation in the codebase |

---

## 6. Open decisions

**Decisions that must be made, in order of consequence.** Not a wish list: each
one has a date, and the date is a commitment subject to a gate. If a date passes
and no decision is recorded, the decision is made by default, which is the
worst possible way to make one — so the default is stated.

| # | Decision | Why it is open | Options | **By** | If it passes undetermined |
|---|---|---|---|---|---|
| **OD-1** | **Offline licensing** | `12` §3.3 lays out five options and recommends E (perpetual, contractual renewal, enforcement in the fleet view, never a site-side gate on operation) and says the decision is an ADR. It is not made. It is a pre-agreed recommendation that has not been accepted | A perpetual / B site-clock expiry / C signed grant / D online (**rejected** — contradicts 5.18) / **E recommended** | **ADR, before M3 GA; interim answer in writing to every design partner by 2026-11-30** | The interim answer stands and is written into every design-partner agreement: the licence is perpetual and the product will not be disabled for non-payment. That sentence in a document a customer holds is worth more than whatever enforcement we might have shipped |
| **OD-2** | **The AI-unavailable UX copy** | The *behaviour* is decided — invariant 3, tested, the strongest thing in the product. The **words on the screen** are not, and the console's degraded banner is the first thing a customer reads when they ask "what does this do when the AI is down?" | (a) The measured, unflattering one: "AI UNAVAILABLE since 02:14. Incidents are UNVERIFIED, not dismissed. Treat UNVERIFIED as look at it, not as nothing happened." (b) A warmer, shorter one. (c) Per-site configurable | **2026-12-31**, with the console content assertions in `07` §2.7's gap | It gets written by whoever writes the template last, which means it is written by an agent from a prompt, in a product whose central claim is honesty about degradation. **The default must be (a)**, because it is the only version that cannot be mistaken for "nothing happened" |
| **OD-3** | **The supported OS and hardware matrix** | `12` §3.6 states a decision "taken now" for Windows Server 2019/2022, Ubuntu 22.04/24.04 LTS, and gates ARM64 on a named condition. It is a plan, not a decision we have signed, and the ARM gate has no date | The stated matrix, with the ARM64 gate ("if a design partner or a reseller pipeline names an ARM site by the end of M2, it is an ADR; if not, it stays unsupported and we say so in the support matrix rather than leaving it ambiguous") | **ARM64 gate: 2027-09-30** (end of M2). The rest: **ADR by 2026-12-31** | ARM drifts into "probably works", and a platform we call supported and do not test on is a platform where the evidence store's escape protection is unverified — which is `TestSymlinkEscapeIsRefused` skipping on Windows, the concrete example already |
| **OD-4** | **The GUI strategy: stay server-rendered, or introduce a frontend** | The most re-litigated decision in the plan and the one most likely to be re-opened by a well-meant request for a better operator experience | (a) Stay server-rendered, no client JS. (b) A progressive-enhancement layer: still no client JS, but more server-rendered interaction and better keyboard paths. (c) A real frontend (React/Vue/SPA) | **ADR by 2027-03-31**, at the M1 gate, with a prototype of (b) as evidence | **(a) holds by default**, because `5.11` is settled and a frontend would weaken it. The deciding factors, stated so the ADR does not have to re-derive them: **the CSP is the moat.** `default-src 'none'; script-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'` is only achievable because there is no inline script, and an SPA means bundling, serving assets, a build toolchain, a client-side trust boundary, and a class of XSS that a server-rendered console does not have. **Against that: two people, a fixed budget, no browser-automation budget (`07` §11.1), and an operator at 03:00 on a workstation in a warehouse.** **Recommendation: (b) — stay server-rendered, and spend the difference on keyboard reachability, empty states, and the content assertions that `07` §2.7 says are missing. Re-open only if a real customer workflow is measurably impossible without client-side interaction, and the evidence is a named workflow, not a preference** |
| **OD-5** | **Is telemetry ever allowed, and in what form** | `5.18` and `5.28` are settled in the negative, and `14` §4.2's DR-1 records four options. It is open because it is a business constraint with no technical basis, and a customer may force it | (a) Never. (b) Customer-initiated, one-way, explicitly toggled, sanitiser-scrubbed, never automatic, disabled by default, visible in the ledger. (c) A customer-run collector the customer owns | **2027-01-15** — before M2's paid pilots convert and before the first enterprise conversation | **(a) holds.** Retrofitting a phone-home is far worse than never having one, and every month of delay makes (b) harder to argue for |
| **OD-6** | **Second VMS vendor selection criteria** | `01` M4 says "Chosen by customer pipeline, not by preference", and names Genetec, FLIR, Avigilon and the Axis ecosystem. It is the real architecture test — "**nothing outside the adapter may require rewriting**" — and the criteria are not written down, so the choice will be made by whoever is in the room that week | (a) Write the criteria *before* looking at the candidates: customer pipeline first, then ONVIF coverage, then the non-adapter diff, then documentation quality, then cost. (b) Choose the candidate with the best pipeline and discover the criteria | **2027-12-31**, so M4's work is not started on a hunch | The second vendor is chosen on enthusiasm, M4 misses its window, and the "universal layer" claim becomes "a layer that works with two VMS in theory" |
| **OD-7** | **The M7 autonomy commercial model** | `01` M7 says it is "sold per site per month, priced on the exception queue's value" and that every level increase is a customer decision with a documented rollback. It is not a pricing model, and the commercial consequence — that a customer at A4 is relying on us for their alarm triage — is not a contract question we have answered | (a) Per-site-per-month, priced on the exception queue's value, with the level as a customer option. (b) Per-outcome or per-incident-handled. (c) Included and monetised through the enterprise tier | **2029-06-30**, before M7's build; the *contract* clauses much earlier, with M5 Enterprise GA | We build the most dangerous capability in the plan and price it as a feature toggle. The contract clauses (`14` §8.4's AI-2, AI-3) need to exist before the capability, not after |
| **OD-8** | **Multi-tenancy topology for MSSP customers** | `00` §3 names MSSP/SOC operators as segment 4 (M5–M6) and says "Multi-tenant monitoring, exception queue" is a channel. The **topology** is undecided and it is a topology decision, which is the most expensive kind to change | (a) One database per customer, with a control plane that aggregates read-only. (b) One database, many tenants, strictly RLS-isolated — which invariant 5 makes strong but which one noisy query in a shared pool could weaken. (c) A shared services layer with a per-customer plane | **ADR by 2028-12-31** (M4's exit gate), because it constrains M5's build | The MSSP conversation starts without an answer, and an MSSP is the customer most likely to ask the question that has no written position. Note that (a) is the default posture if we do nothing, because it is the one that matches a single-site product and an on-prem deployment |

**The rule about this table:** every entry is a decision two people can make in
an hour, and every entry has been open longer than an hour. The reason is not
indecision — it is that each one needs a piece of information that arrives
later. The table exists so that "we will decide it when we have to" has a date
attached to it, and so that the "when we have to" is a calendar entry rather
than an incident.

---

## 7. The weekly decision digest

**Fifteen minutes, Friday, both people, written.** Written rather than spoken
because the artefact is the point: a decision made in a conversation on a
Wednesday is a decision that is re-litigated in a conversation on a Wednesday
three months later unless somebody wrote down what it was and why.

```
DECISION DIGEST — week of YYYY-MM-DD

DECIDED  (ADR number, or "no ADR needed", or "deferred to <date>")
  · <decision in one line> — why — ADR-00NN or none
  · <decision> — why — ADR-00NN
  ...
  Every entry that is not an ADR gets a one-line reason anyway. "Decided not to
  write an ADR" is itself a decision somebody will question later.

PENDING  (waiting on a person or a date, with the date it becomes a problem)
  · OD-# <what> — owner E1/E2 — needed by YYYY-MM-DD — blocked on <what>
  · RISK-## <what> — owner — review due YYYY-MM-DD
  · <an answer from a customer, a vendor, counsel, an auditor> — chased YYYY-MM-DD

QUESTIONS  (a question with an owner and a date, which is the only kind that
            gets an answer)
  · <question> — owner — answer by YYYY-MM-DD
  Never "we should think about X". A question with no owner and no date is a
  wish.

SETTLED THIS WEEK  (new rows for §5 of 19-decision-log-and-process.md)
  · <what is now settled> → the ADR that settled it, or the code and the
    reason. **A row added here is a row that will not be re-opened in six
    months**, and it takes one sentence.

ESCALATED  (anything Tier 0 or Tier 1 from 16 §6, and the state of it)
  · <what> — raised YYYY-MM-DD — owner — current state

RE-READ  (things that changed and that we decided from memory rather than from
         a document — the thing this digest exists to make rare)
  · <the question> — the settled answer, and the ADR that says so
```

**Three rules for the digest, and they are why it works at two people:**

1. **A decision with no ADR and no reason is not entered as decided.** It is
   entered as a question with an owner and a date. This is the mechanism that
   stops a decision being made by whoever spoke last.
2. **"Settled this week" is the most valuable section and the one most likely to
   be skipped.** It is what makes §5 grow, and §5 is what makes the repository
   legible to a future agent. A week with three rows added is a good week.
3. **The digest never contains a decision about a date** (§4.3), and it never
   contains a customer communication. It is an internal instrument, and its
   only output is the next week's starting position.

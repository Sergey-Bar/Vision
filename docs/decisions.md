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

# PROPOSED — D-01 and D-11, written to be signed

These two are now written as **signable proposals**: the decision, the alternatives
that were rejected and why, the blast radius, and the signature block. Everything
else in this file is still a recommendation attached to a question.

Nothing here is in force. `AGENTS.md` reserves an invariant change, a published
contract, an activated capability, an outbound contact and a date to a human, and
D-11 is a change to a row-level security policy — which is the first thing on that
list. An agent can write the proposal and cannot sign it.

## PROPOSAL D-01 — a model request goes to one host, named in a committed manifest

**Decision.** The natural-language search gateway sends its model request to exactly
one host. That host, the prompt, and the prompt's SHA-256 are committed in
`contracts/nlsearch/host.json`, and the prompt's hash is checked at startup against
the prompt in the binary. A host that is not in the manifest is refused before a
request is built.

**Rejected, and why:**

| Alternative | Why not |
|---|---|
| the host is configuration (`SP_NLSEARCH_HOST`) | the first deployment decides where evidence went, by editing an env file nobody reviews. This product's whole posture is that a destination should not be a deployment-time choice. |
| the host is derived from the incident's site | the destination would vary per tenant, which means an operator's credential leaves toward a different host per customer and the audit ledger records a request with no fixed destination. |
| the host is pinned but the prompt is not | a pinned endpoint with an unpinned prompt is a pinned room with an open window. The prompt decides what the model is asked, so it is the same decision. |

**Blast radius.** `internal/nlsearch` does not exist yet, so this is greenfield and
costs nothing to get right now. It becomes expensive the moment the first client
lands, which is why it is written down before the code rather than after.

**What signing it commits the build to.** A change to `contracts/nlsearch/host.json`
becomes a contract change under `contractcheck`, with the same review that a schema
change gets. That is the cost, and it is the point.

```
PROPOSED D-01 — pinned host, prompt in the repository, host change is a contract change

  decision      ACCEPT / DECLINE / AMEND
  signed by     ______________________________
  date          ______________________________
  amendments    ________________________________
                ________________________________
```

## PROPOSAL D-11 — a site-less row is the system worker's, and nothing else's

**The fact the code already establishes.** Migration `0023` gives `jobs` and
`outbox_events` a `site_isolation` policy. `app_site_visible(x)` returns **true**
when `x IS NULL`, `site_id` is nullable on both, so a site-less row on those two
tables is **TENANT-scoped today**. That is the existing helper's semantics and the
migration did not change it, because a check refusing NULL would break the worker
that enqueues a site-less job — and that failure appears under load, not at
migration time.

**Decision.** A site-less row is permitted **only for the system worker**. The policy
becomes `app_is_system_worker() OR app_site_visible(site_id)`, with `NULL` not
passing through. Every enqueue path carries a site, or is a worker.

**Rejected, and why:**

| Alternative | Why not |
|---|---|
| leave it as tenant-scoped (current behaviour) | every principal in the tenant can see and enqueue a row that belongs to no site. That is defensible until the day someone writes a query that treats "visible" as "yours". |
| refuse any NULL `site_id` | breaks the worker that enqueues before the site is known. It would fail under load rather than at migration time, which is the worst possible time to find out. |
| add a `system_owned` boolean | a second source of truth for "who does this belong to", beside the one already in `user_roles`. Two sources of truth for tenancy is how invariant 5 gets broken. |

**Blast radius.** This is a **contract step, not an expand**, and it ships in its own
migration after the expand that makes it safe. It cannot be bundled with the expand,
because until the expand lands, the stricter check would refuse rows the running
system legitimately enqueues.

**What it costs if declined.** The behaviour stays as the first row of the table above,
and the audit ledger records a NULL site for any job enqueued without one. That is a
defensible place to be. It is a place to be **by accident**, which is the part worth
naming.

```
PROPOSAL D-11 — a site-less row is the system worker's, enforced by policy

  decision      ACCEPT / DECLINE / AMEND
  signed by     ______________________________
  date          ______________________________
  amendments    ________________________________
                ________________________________
```

---

## PROPOSAL D-12 — one path, one owner: the demo owns the root, the console owns /console

**Decision.** The console server serves two surfaces and they are separated by a
prefix rather than by registration order.

- The **showcase** owns `/` and the acts of the judge track: `/live`, `/pipeline`,
  `/trust`, `/census`, `/operations`, `/lab`, `/ask`, `/chief`.
- The **console** (the React SPA) owns `/console` and every path under it.
- The route table lives in `web/routes.go` and in ONE place only. The console's own
  client routes live in the SPA's `components/Shell.tsx` and nowhere else; the Go
  server serves the shell for the whole prefix and keeps no copy of them.
- `web/routes_test.go` fails when a path has two owners, when a page in
  `web/static` has no owner, or when a route the SPA declares is not under the
  prefix.

**Rejected, and why:**

| Alternative | Why not |
|---|---|
| the SPA owns `/` and the demo lives under `/demo` | the judge track is five minutes long and its URLs are printed in `docs/showcase/narration-judges.md` and in the narration itself. A judge who mistypes `/pipelne` should be told what exists, not be moved to a second site. |
| the console server keeps serving the SPA from `/`, and the demo moves to a second process | two servers, two origins, and the demo's own data endpoints (`/api/showcase/*`) would have to be proxied across them. The single process is what lets the demo read the same files the pages quote. |
| registration order decides, as it did | `http.ServeMux` takes the LAST registration of a pattern, and the SPA's catch-all was registered last. That is how every page of the demo returned 404 with an empty body while the pages themselves sat complete in the tree. An ordering that decides ownership is an ordering that will decide it differently after the next three files are added. |
| the showcase's pages keep their old paths and the demo moves to `/demo` | the same objection as the first row, with an extra step: the pages' own links, the census's `see it` column and the narration's timings are all written against the paths the demo already had. |

**What the tree already does, and this ratifies rather than changes.** The code is
written; the question is whether the shape is the one you want to keep. The
consequences are:

- The URL a judge is given for the console is now `/console`, not `/`.
- `web/static/index.html` — the capture-path runner — became `web/static/dev.html`
  and is served at `/dev`, behind the new `-dev` flag, together with the four
  endpoints that run the CLI (`/api/poc`, `/api/acceptance`, `/api/rules/lint`,
  `/api/rules/simulate`). A default server is no longer a remote shell with a JSON
  body.
- `web/static/fleet.html` was **deleted**: it called four endpoint families
  (`/api/fleet/*`, `/api/operator/*`, `/api/search/chat`) that exist nowhere in
  this repository. A page whose data layer was never written renders four empty
  tables and four console errors, and it was in the tree because nobody could
  reach it to find out.
- The showcase's assets are compiled into the binary (`go:embed`), so the demo
  renders the same bytes from a release archive as from a source tree.

**Cost of declining.** The route table goes back to being implicit, and the next
surface added to the server can shadow the demo again. The gate that catches it
(`web/routes_test.go`) goes with it.

```
PROPOSAL D-12 — the demo owns the root, the console owns /console, one table, one owner

  decision      ACCEPT / DECLINE / AMEND
  signed by     ______________________________
  date          ______________________________
  amendments    ________________________________
                ________________________________
```

---

## PROPOSAL D-13 — one token file, one mark, one name, one set of weights

**Decision.** The showcase and the console are ONE product with two renderers, and
they share everything that decides what the product looks like.

- **One token file.** `web/static/tokens.css` holds every colour, size, radius and
  duration. The showcase links it; the console's `index.css` imports it. The
  Tailwind configuration is the console's *translation* of that file — every value
  in it is a `var(--color-…)` reference — and the Tailwind-side names are
  aliases, not a second palette.
- **One accent, and it is not a semantic colour.** The accent is blue. The
  console's accent was the same red as "critical", so every button, focus ring
  and active nav item announced an emergency before anything had happened.
- **Three weights: 400, 500, 600.** `font-black` appeared 128 times across sixteen
  files. At 900 every element is shouting and the eye has nowhere to start.
- **No glows and no drop shadows.** On a dark ground a shadow is a smudge. Depth
  is a tint step plus a hairline.
- **Sharp radii: 4, 6, 10, 14, full.** The console's 24/32/40-pixel radii were a
  consumer app's.
- **One mark.** A hexagonal sensor chassis with a visor and a sensor node, on a
  24×24 grid, drawn in the accent. The showcase carried three and the console a
  fourth.
- **One name: VISION.** The console rendered "Vision VMS", which is neither the
  locked brand nor the machine identifier.
- **The de-emphasised ink is `#7A8494`,** not the `#4A5568` the design document
  proposed: `#4A5568` is 2.6:1 on this ground, and the token file uses it for
  text. The contrast requirement won over the palette proposal.
- **The console's own route table lives in the console's own source.** The server
  serves the shell for the whole `/console` prefix and keeps no copy of it.

**Rejected, and why:**

| Alternative | Why not |
|---|---|
| two token files, one per surface | two answers to every question about the product's appearance, and the second one is the one nobody updates. The console's colours were being dropped by the browser for a whole release because a helper in its config built an invalid declaration; a second file is how that class of bug survives. |
| the showcase adopts the console's palette | the console's accent was the same red as "critical", and its radii were a consumer app's. Adopting it would have made the demo a consumer app. |
| the console adopts the showcase's file as-is | the showcase's file carried a layer of VMS furniture that the console does not have. The token file is the part that is shared; the furniture is not. |
| keep `font-black` and fix it page by page | 128 occurrences over sixteen files is a scale problem, not a page problem. |

**What the gates are.** `web/design_test.go` holds the design system's own
contracts: the palette clears WCAG AA for every pair that carries text and 3:1 for
every pair that carries a non-text signal; the token file is complete; there is
no pure black and no pure white; the console uses no weight above 600, no glow
and no drop shadow, and no raw hex; every class the console's source uses is a
class Tailwind GENERATES; the console and the showcase share one token file; the
mark is one; the name is one; and every screen has exactly one `h1`.

```
PROPOSAL D-13 — one token file, one mark, one name, one set of weights

  decision      ACCEPT / DECLINE / AMEND
  signed by     ______________________________
  date          ______________________________
  amendments    ________________________________
                ________________________________
```

---

## PROPOSAL D-14 — one screen, one search, one rail

**Decision.** The console is ONE screen, and the operator's first three
questions are answered on it in the order they are asked.

- **The screen.** The strip (is anything broken), the wall (what is the site
  doing) and the column (what needs me, and where). The wall is on the left
  because the picture is the thing an operator looks at while they work, and
  the column is on the right because it is the thing they read. The wall has
  its own scroll and stays put; the column scrolls with the page.
- **The search is one search.** A command palette on `⌘K` (or `/`) over the
  same index the record's own search uses, with the record's moments and the
  places the record can be read in one list. The top bar and the column's
  always-visible input open the same palette.
- **The rail has one entry for the page.** `/console`, `/console/live` and
  `/console/events` all render the operations page, so the rail lights ONE
  entry for all three. Three entries pointing at one page is a navigation
  that cannot say where you are.
- **The old routes answer.** `/console/live` scrolls to the wall and
  `/console/events` to the record, because a bookmark and the rehearsal's URL
  list both name them.
- **The keyboard is an instrument.** `⌘K` the palette, `/` the search, `j` and
  `k` the queue, `Enter` opens, `Esc` closes, `g` then `o`/`i`/`c` a section.
  A key pressed while the reader is typing belongs to the field.
- **The density is a URL parameter,** `?density=compact`, because the console
  holds no state of its own: a density kept in the browser is a density that
  differs per browser, and the density of a screen is a property of the screen
  rather than of the machine somebody is reading it on.

**Rejected, and why:**

| Alternative | Why not |
|---|---|
| keep the three pages and make the nav better | the three pages had three sets of empty states, and the operator had to choose between "the picture" and "the queue" rather than having both. Better navigation over three answers is still three answers. |
| one page, one long scroll | the picture would scroll away from the operator while they read a queue, and the picture is the thing they are looking at. |
| the palette replaces the record's own search field | the record's field is how an operator who does not know the shortcuts works, and a search that is only reachable by keyboard is a search that only the people who read the manual can use. |
| the density in `localStorage` | RC-35 bans it, and the repository's rule is the same: the console holds no state of its own. |

**What the gates are.** `web/routes_test.go` parses the route table out of
`Shell.tsx`, so the routes and the pages cannot drift; `web/design_test.go`
checks that every class the console's source uses is a class the configuration
generates, and now that every colour class names a colour the configuration
declares; `internal/treestate` checks that every red-check row has a
transcript in `docs/status.md`.

```
PROPOSAL D-14 — one screen, one search, one rail

  decision      ACCEPT / DECLINE / AMEND
  signed by     ______________________________
  date          ______________________________
  amendments    ________________________________
                ________________________________
```

---

## PROPOSAL D-15 — the calibrated engine, behind a flag

**Decision.** The threat engine scores the CLAIM and the EVIDENCE separately,
decides on two axes rather than on one number, and calibrates itself against
the site's own labelled history. **It is behind a flag that defaults to the
behaviour it replaces.**

- **`SP_THREAT_ENGINE`** is `legacy` or `calibrated`, and it is `legacy` by
  default. The default is the old behaviour because a change to what an
  operator sees is a decision a human signs, and a flag that defaulted to the
  new behaviour would make the decision by default, which is the same thing as
  making it without one.
- **`SP_THREAT_HALF_LIFE`** (two hours) is the queue's recency decay, and zero
  disables it. The decay is a property of the QUEUE: the incident's stored
  score does not change, because an incident's score is a fact about the
  incident and a queue's order is a judgement.
- **The severity axis is the claim** — the rule's own severity times the
  zone's multiplier: how bad it would be if the incident were real.
- **The likelihood axis is the evidence** — the corroborating device count,
  the duration excess, and the local base rate. The base rate CAPS the
  likelihood: a rule that is noise at this site stays at the bottom of the
  scale however much evidence there is, because the evidence is about the rule
  and not about this incident.
- **`ActionHold` is the corner where a serious claim meets thin evidence.**
  Before this it was in no cell at all.
- **The score is capped on a curve,** `100 · (1 − e^(−Σ/100))`, so a raw sum
  of 120 and a raw sum of 300 are different numbers, and the cap is bounded
  by 100 without ever reaching it.
- **The prior is Laplace-smoothed,** `(tp+1)/(tp+fp+2)`, and **a site with no
  labelled history has no prior at all.** An absence of history is not a
  negative observation about a rule, and a fresh install behaves exactly as
  the engine it replaced.

**Rejected, and why:**

| Alternative | Why not |
|---|---|
| replace the engine | the new engine's numbers are measured against a fixture of twenty-seven cases, and no installation has measured it against its own history yet. The flag is what makes the measurement possible before the replacement is. |
| default the flag to `calibrated` | a change to what an operator sees, made by default, is a change nobody decided. |
| the base rate as a global constant | a rule that is right nine times in ten in a lobby and two times in ten in a loading bay is the same rule, and a global constant tells the engine to treat both the same. |
| the prior without smoothing | one true positive would give a rule a prior of 1 and the next incident a prior of 0. |
| a prior of zero for a site with no history | a score of zero says "nothing anomalous", which is a different claim from "this rule is noise here". |

**What the gates are.** `internal/calibration` holds the labelled corpus, the
golden decisions and the measurement; `go test ./internal/calibration -update`
regenerates the golden decisions, and a change to the engine is therefore a
diff in a file a human reads. `TestTheCalibratedEngineBeatsTheLegacyOneOnTheCorpus`
fails when the new engine is no longer better on the corpus, and the red-check
RC-44 removes the base rate's cap and watches it fail. `internal/threat`'s own
gates hold the soft cap monotone and bounded, the decay half-life correct, the
sixteen cells reachable and `HOLD` reachable.

```
PROPOSAL D-15 — the calibrated engine, behind a flag

  decision      ACCEPT / DECLINE / AMEND
  signed by     ______________________________
  date          ______________________________
  amendments    ________________________________
                ________________________________
```

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

---

## D-16 — Where does the operator's identity come from?

**This one is a decision and not a fact, and it needs your name on it.** The console has
no login, no session, no cookie and no token. Every request runs as a CONFIGURED
principal: a role and a site scope, both from configuration
(`internal/api/server.go`, `openPrincipal`). The capability checks are real — a role
without `evidence.fetch` does not fetch evidence — but every operator who reaches the
console's port is the same principal.

That is not a bug. It is the shape of an appliance that is deployed on a network its
operator controls, and it is why the product can be a single binary with no login form.
It is also a statement about where the trust boundary is, and the boundary is currently
**the network**.

| Reading | Consequence |
|---|---|
| the console is on a trusted network, and the operator's identity is the network's | the current behaviour; every operator who reaches the port is the configured principal, and the audit ledger records that principal rather than a person |
| the console is behind a trusted reverse proxy that authenticates the operator | the operator's identity is the proxy's assertion; the product trusts it, and the audit ledger records the asserted identity rather than a configured role |
| the console authenticates in-app | `internal/identity` is wired to a login route, and the M5 scope starts here rather than in 2029 |

**Recommendation: the second reading, for M1 to M4.** A trusted reverse proxy in front
of the console authenticates the operator; the product trusts the proxy's assertion
about who they are and about which sites they may see; the product's own capability
model still governs what an asserted identity may DO, so a proxy that asserts a
supervisor's identity cannot grant that identity more than the product's capability
set allows.

**Why not the third reading now.** `internal/identity` implements a per-source rate
limit, exponential lockout, MFA, step-up and a break-glass path, and every one of them
is tested. None of them is reachable, because there is no login for them to guard.
Wiring them is an auth change, and `AGENTS.md` reserves auth for a human who reads the
diff. It is also the M5 scope: the local password stack is the fallback for isolated
sites, and enterprise identity (OIDC, SCIM) is M5.

**Cost of the second reading:** the proxy becomes part of the security boundary, and the
product's audit ledger records an ASSERTED identity rather than a resolved one. The
audit ledger's hash chain still proves the sequence; it does not prove who the operator
was, and the difference has to be stated in any document that claims otherwise.

**Until this is signed, the behaviour is the first row of that table**, and
`docs/threat-model.md` A3.1, A3.3 and A3.5 say what that costs.

---

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

## D-17 — What may read the analytics?

**This one is a decision and not a fact, and it needs your name on it.** The
durable analytics are readable through a new capability, `analytics.read`, and
not through the one the metrics scrape uses.

| Reading | Who may read the attention minutes |
|---|---|
| `audit.read` (the scrape's capability) | anybody who may read every row of the audit ledger: every operator's disposition, every incident's state, every rule activation |
| `analytics.read` (the new one) | somebody whose role says "you may read how long a shift took to clear this site", and who may not read the individual dispositions behind it |

**Recommendation: the second.** The aggregate and the records behind it are
different permissions, and the reason is about the READER rather than the
writer. Attention minutes are a number about people: how long a shift took to
clear a site, how many escalations were false, how much of an operator's
attention went to things the rules later proved were not incidents. Somebody who
may read the ledger may read every operator's disposition, which is a different
question from "how did this site perform", and the second is a question a
security director asks and an operator is not always entitled to have answered
about their colleagues.

The capability is granted to `tenant_admin` and `security_manager`: the same
two roles that hold `audit.read` today, so the change is not a change in who
can see what on a single-site install. It is a change in what a ROLE MEANS, and
a role table that means one thing today and another tomorrow is a role table
that cannot be read.

**What it does NOT change:** the site scope is still the principal's, bound onto
the connection, and the `site_isolation` policy is still RESTRICTIVE. A
principal assigned to one site still cannot read another site's aggregate, and
a site filter in a query is still a filter and not a grant.

**The refusal that is not:** the metrics scrape at `/v1/metrics` keeps
`audit.read`. It is a Prometheus exposition, its shape is the ledger reader's
shape, and changing it would break a collector that exists.

---

# PROPOSED — D-18 to D-22, the AI VMS decisions

Written on 2026-10-21, when the strategy change in
[`roadmap/strategy-and-north-star.md`](roadmap/strategy-and-north-star.md) §9
amended the product from a layer above the installed stack to a VMS that
proves. The full plan is
[`roadmap/milestones/m2-ai-vms-execution-plan.md`](roadmap/milestones/m2-ai-vms-execution-plan.md).

Each one below is written as a signable proposal: the decision, the
alternatives that were rejected and why, the blast radius, and the signature
block. **Nothing here is in force.** An agent can write a proposal and cannot
sign it, and none of these has been.

## PROPOSAL D-18 — we own the camera path, and we keep reading other VMSes

**Decision.** The product records the cameras it is given — RTSP ingest, our
remux, our pre-roll, our per-device-day hash chain — and it keeps reading other
vendors' VMSes through the existing adapter seam as a first-class secondary
source. **Both are the product. Neither is a fallback for the other.**

**The two halves are not alternatives.** This is the part worth being precise
about, because a reader who hears "we now record" reasonably assumes the
adapter is being retired. It is not. The adapter is the cheapest way for a
customer to buy the product without migrating their recording: they keep their
VMS, we read it, they pay the site fee and no device tier. The camera path is
what we offer when a customer wants the recording to be *provable*, because a
recording we did not make is a recording we cannot chain.

**Rejected, and why:**

| Alternative | Why not |
|---|---|
| stay a layer, and never record | the customer buys a VMS to record video, and a layer cannot make the product's central promise — *prove what you recorded* — because it did not record it |
| become a VMS, and drop the adapter | it retires the installed base and the OEM channel, and it makes a customer who is happy with their XProtect install a prospect rather than a customer. The adapter also proves the vendor-neutral seam the whole "losing the vendor does not lose the evidence" claim rests on |
| record and read, but treat the recording as an optional extra | a VMS that records some sites and not others is two products with one console, and the pricing model cannot describe it. The device tier counts devices **we record**, and the site fee is the same either way |
| rip and replace a customer's working VMS | the plan's own scope boundary, and the cheapest competitor to beat is a working install |

**The blast radius.** `internal/milestone` is frozen, not deleted. The media
plane is a new binary, `cmd/security-recorder`, which is a **second process**
and not a second runtime: same language, same Postgres, same contracts, same
release archive, and it cannot write the schema (`recorder-schema-isolated`).
`internal/mp4`, `internal/rtsp` and `internal/detector` stop being a "demo
capture path" and become product code, or they are deleted.

```
PROPOSAL D-18 — we own the camera path, and we keep reading other VMSes

  decision      ACCEPT / DECLINE / AMEND
  signed by     ______________________________
  date          ______________________________
  amendments    ________________________________
                ________________________________
```

---

## PROPOSAL D-19 — the auth posture reverses for the appliance era

**Decision.** The console gains a real login and OIDC. D-16's second reading —
a trusted reverse proxy asserting the operator's identity — remains valid for
the sites that already run it, and the in-app login becomes the path for
everybody else.

**Why the current reading cannot survive.** D-16's first reading is that the
console is on a trusted network and the operator's identity *is* the network's,
and every request runs as one configured principal. That is defensible for a
layer bolted onto somebody else's install, and it is not defensible for a VMS
sold to an enterprise: the tenant's own security team will not accept a product
whose audit ledger records "the configured principal" for every disposition
their CISO asks about, and a VMS that cannot tell two operators apart cannot
support per-operator escalation, per-operator rates, or per-operator withdrawal
of a consent. **An enterprise buyer asks who did this, and the current answer
is "the server".**

**Rejected, and why:**

| Alternative | Why not |
|---|---|
| keep the configured principal and let the proxy do it all | it is what D-16 recommends, and it is right for M1–M4. It is wrong for a product that now holds a customer's entire video record under one identity |
| in-app login only, with no OIDC | enterprise identity is OIDC and SCIM, and building a local password stack without OIDC means building it twice |
| OIDC only | an air-gapped site has no identity provider. The local stack is not a fallback for a legacy product; it is the only thing a regulated, disconnected site can use |

**What this does NOT change.** The capability model still governs what an
asserted identity may do: a proxy that asserts a supervisor's identity cannot
grant that identity more than the product's capability set allows. The step-up
second factor is unchanged. The rate limit is unchanged. The audit ledger stays
append-only and hash-chained, and a login is an audited act like any other.

**The blast radius.** `internal/identity` is wired to a login route, and the
M5 scope starts now rather than in 2029. **This is an auth change, and
`AGENTS.md` reserves auth for a human who reads the diff.** It is the single
largest piece of work in this list and it is not the one to do alone.

```
PROPOSAL D-19 — the auth posture reverses for the appliance era

  decision      ACCEPT / DECLINE / AMEND
  signed by     ______________________________
  date          ______________________________
  amendments    ________________________________
                ________________________________
```

---

## PROPOSAL D-20 — the price is a site, a device tier and an outcome

**Decision.** The pricing model changes from "never per camera, ever" to **per
site base + per device tier + the retained outcome component + a separately
priced 24/7 support line**, with the device tier counting **devices we record**.

**Why.** The old rule was correct for a layer and is wrong for a VMS. It
existed because a per-camera price punishes a buyer for closing a coverage
gap, and because a layer's cost driver is incidents rather than cameras. A
VMS's cost driver *is* the camera count: thirty-two cameras are thirty-two
streams to ingest, thirty-two pre-roll rings, thirty-two chains to seal at day
close and thirty-two camera credentials in a vault. The old rule charged for
the wrong variable and charged nothing for the variable that costs money.

**Rejected, and why:**

| Alternative | Why not |
|---|---|
| keep "never per camera" | the cost driver is the device count, and a price that does not follow the cost driver is a price that goes wrong quietly and is discovered at renewal |
| price per camera, literally | the old objection still holds where it should: a customer who adds four cameras crosses a tier, and is not billed for four cameras. A tier keeps the objection and loses the fiction |
| price per segment or per gigabyte | it prices the customer's own usage back at them, and it makes an outage — the thing we promise to prevent — the thing that costs them most |
| bundle everything into one site fee | a 300-device site and a 4-device site pay the same, and the 300-device site is the one whose storage and ingest cost money |

**The blast radius.** `pricing-and-packaging.md` §1.1 and §4.4 are amended and
unsigned. The OEM royalty's base becomes three named lines rather than "15% of
list", because a royalty on an ambiguous base is the first thing a partner's
finance function disputes. **A customer who keeps their incumbent VMS pays the
site fee and no tier**, and a customer who migrates to our camera path does not
pay twice.

```
PROPOSAL D-20 — the price is a site, a device tier and an outcome

  decision      ACCEPT / DECLINE / AMEND
  signed by     ______________________________
  date          ______________________________
  amendments    ________________________________
                ________________________________
```

---

## PROPOSAL D-21 — ONVIF is a new protocol family, and the ADR is the gate

**Decision.** ONVIF is adopted as the device protocol family. The capacity law
in `strategy-and-north-star.md` §6 requires an ADR for a new protocol family,
and this is that ADR.

**Why it needs an ADR rather than a note.** ONVIF is the first protocol family
added since the stack was fixed, and it is the one that decides whether the
device registry is measured or assumed. It is also a family of *optional*
profiles, and the plan's own warning is that ONVIF compatibility is a swamp:
a camera that claims a profile it does not implement is the normal case, and
the whole product's credibility rests on not believing a device's own claims.

**The rule the ADR carries, and it is the same rule as `internal/milestone`'s:**
**a capability without a probe result is not a capability.** The manifest is
measured per device. A device that has not been probed has an empty capability
set and the console draws no live tile for it.

**Rejected, and why:**

| Alternative | Why not |
|---|---|
| vendor-specific APIs per manufacturer | one adapter per vendor, forever, and the "compatible" claim becomes per-vendor and unverifiable |
| ONVIF only, with no fallback | some cameras do not speak it, and a product that refuses a camera is a product with a smaller market and the same engineering |
| assume the profiles from the model name | a guess that reads as a measurement, and the first guess that is wrong is the first customer who finds out |

**The blast radius.** `internal/onvif` is a new package, `internal/device` a new
one, and the capability manifest a new published JSON Schema. ONVIF membership
and the camera lab are **procurement**, they have the longest lead time in the
plan, and they start in P0.

```
PROPOSAL D-21 — ONVIF is a new protocol family, and the ADR is the gate

  decision      ACCEPT / DECLINE / AMEND
  signed by     ______________________________
  date          ______________________________
  amendments    ________________________________
                ________________________________
```

---

## PROPOSAL D-23 — the auth posture is decided by the MODE, not by the product

**Decision.** The console gains a real login and OIDC **in `control` mode
only**. In `site` mode — one site, one appliance, one operator — the console
keeps running as the **configured principal** and there is **no sign-in**.

This **amends D-19**, which proposed the reversal unconditionally. D-19's own
reasoning is right; its conclusion is too broad.

**Why D-19's conclusion is too broad.** D-19 says the current posture "is
not defensible for a VMS sold to an enterprise", and that is true. But D-19
also proposes a **login screen at first boot for every install**, and that is
the opposite of the product's other promise.

The product's promise is that it is **plug and play**. A login screen is the
first thing an operator meets, and for the segment the strategy names first —
a single-site commercial operator, "buys with a credit card and a phone
call" — a login screen at first boot is a support call, a password policy, a
"where do I put this" and a "who is the admin". It is friction standing
exactly where the product promised none.

D4 already drew the seam: **one binary, two modes.** The modes differ in
what they are responsible for, and the identity posture follows the
responsibility:

| | `site` mode | `control` mode |
|---|---|---|
| responsible for | one site | a fleet of sites |
| identity | the **configured principal** | **OIDC**, with a real person per action |
| per-operator attribution | no — and that is honest, because the install has one operator | **yes** — the ledger names the person |
| the CSRF surface | **none**: no cookie, no session, no token | present, and it is enforced |
| a login screen at first boot | **no** | yes |

**The CSRF point is the one that settles it.** The repository records a
known GAP: the console's browser-facing form routes carry no CSRF token,
because the API is a machine API whose authority comes from its own
configuration rather than from anything the caller carries. That reasoning is
**sound for a machine API and unsound for a browser-facing page** — and it is
unsound precisely because the console IS a browser-facing page.

The mode split removes the gap rather than papering over it. `site` mode has
no cookie, no session and no token, so there is nothing for a cross-site
request to ride on. `control` mode has a session, so it has a CSRF surface, so
it must have a CSRF control. **One posture per mode, each one correct for
what that mode is.**

**Rejected, and why:**

| Alternative | Why not |
|---|---|
| D-19 as written — a login everywhere | a login screen at first boot is the opposite of plug and play, and the strategy names a single-site segment first |
| the configured principal everywhere, and D-19 deferred | it leaves the enterprise objection unanswered: the ledger would say "the configured principal" for every disposition a CISO asks about, and a VMS that cannot tell two operators apart cannot support per-operator escalation or per-operator withdrawal of a consent |
| OIDC only, with no local stack | an air-gapped site has no identity provider, and the local stack is not a fallback for a legacy product — it is the only thing a regulated, disconnected site can use |
| a local stack only, with no OIDC | enterprise identity IS OIDC and SCIM, and building a local password stack without OIDC means building it twice |

**What this does NOT change.** The capability model still governs what an
asserted identity may do: a proxy that asserts a supervisor's identity cannot
grant that identity more than the product's capability set allows. The
step-up second factor is unchanged. The rate limit is unchanged. The audit
ledger stays append-only and hash-chained, and a login is an audited act like
any other.

**What it costs.** Two postures to build and two to test, and a mode switch
that decides which one an install runs. The switch is the `site`/`control`
split D4 already named, so the cost is a configuration value rather than a
second product.

**The blast radius.** `internal/identity` grows a real sign-in path for
`control` mode, and `internal/api` grows the session and CSRF surfaces that
`site` mode does not have. **This is an auth change, and `AGENTS.md` reserves
auth for a human who reads the diff.** It is the single largest piece of work
in this list, and it is not the one to do alone.

```
PROPOSAL D-23 — the auth posture is decided by the MODE, not by the product

  decision      ACCEPT / DECLINE / AMEND
  signed by     ______________________________
  date          ______________________________
  amendments    ________________________________
                ________________________________
```

---

## PROPOSAL D-22 — the recorder cannot write the schema, and cannot write the ledger

**Decision.** The media plane is a **separate binary** (`cmd/security-recorder`)
and a **separate process**. It owns no schema change and it cannot write the
audit ledger. It writes segments and index rows; the core seals and audits
them.

**Why.** Two reasons and they are different. The first is the failure domain:
**a recorder crash must not stop event ingest, and an ingest crash must not
stop recording.** A single process that does both cannot promise that, and the
promise is the product's. The second is the schema: the recorder runs for
weeks unattended on a customer's disk, and a long-running writer that can
reshape the schema is a long-running writer that can eventually reshape it by
accident. `security-migrate` already exists for exactly this reason and the
reasoning transfers.

**Rejected, and why:**

| Alternative | Why not |
|---|---|
| the recorder inside `security-core` | one process, one crash domain, and a schema writer on the hot path of a 24-hour recording |
| the recorder inside the core process but with a separate package | the package boundary is a compile-time rule; the *failure* boundary is a process boundary. A goroutine that blocks still blocks the recorder's own backpressure |
| the recorder may write the audit ledger | it is a different process with a different failure profile, and a ledger written by a process that can be killed mid-write is a ledger with a gap in it. The core seals the chain head at day close |

**What it costs.** A second binary, a second service unit, a second health
signal, and an inter-process handoff between "the segment is on disk" and "the
index row is written". The handoff is the `outbox` discipline the repository
already uses, and it is why a partially written segment is recorded as
`state='partial'` with its true boundary rather than discarded: **a partial
segment is never a silent hole.**

**The blast radius.** `internal/recorder` and `cmd/security-recorder` are new.
`recorder-schema-isolated` is a gate, and so is `disk-full`.

```
PROPOSAL D-22 — the recorder cannot write the schema, and cannot write the ledger

  decision      ACCEPT / DECLINE / AMEND
  signed by     ______________________________
  date          ______________________________
  amendments    ________________________________
                ________________________________
```

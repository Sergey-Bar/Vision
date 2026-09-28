# UX and Design System

Owner: E1 (Product/Architecture). Status: living. Last revised 2026-09-28.

For the operator who uses this at 02:00 to decide whether something is worth
waking somebody for, and who has to be able to justify the click afterwards.

This document is written against the console that exists
(`internal/api/console.go`, `internal/api/templates/*`,
`internal/api/templates/static/console.css`) and the routes that exist
(`internal/api/handlers.go`, `internal/api/server.go`). Where a screen, a route
or a state is described that does not exist yet, it says so and names the
milestone that adds it.

---

## 1. The operator's actual context

Everything below follows from this section. Read it before disagreeing with a
layout decision.

### 1.1 What the context is

**Night shift.** Most of the product's screen time is 22:00–06:00 local site
time, in a room that is dark, often with one operator covering several sites.
Design consequence: dark-first, not dark-themed. The default surface is the one
the operator's own eyes are adapted to. Light mode exists for daytime, for
print-to-PDF of an evidence page, and for the rare bright office — it is a
second theme, not the first.

**Multiple monitors.** The operator usually has the VMS in one window, email or
a radio in another, and us in a third. Design consequence: no layout that
requires horizontal scrolling of the primary column, no modals that trap focus
away from a window they need to check, and every screen is usable at 1280×800
because that is the resolution of the monitors actually in the buildings.

**Interruption-driven.** The shift is not a queue, it is a series of
interruptions. Design consequence: state is durable. An operator who is pulled
off an incident mid-read and comes back four hours later must find the incident
exactly as it was, with the same scroll position if we can manage it, with the
same evidence expanded, and with no partially-completed action. No client-side
session state of any kind exists to lose, which §4.2 explains is not a choice.

**Consequence of a click.** A disposition is an audit row. An escalated incident
is a phone call. A PTZ move is a camera pointed at a place. Design consequence:
every state-changing control is one deliberate act with a named consequence, and
the two outcomes that can cost a person their job — `escalated` and `dismissed`
— already require a step-up MFA in `internal/incidents` (`stepUpRequired`).
The UI's job is to make that second factor feel like a feature, not a tax.

**Fatigue.** Decision quality degrades measurably after hour three of a night
shift, and the incident volume is highest in the small hours. Design consequence:
the interface reduces cognitive load rather than adding information. The
anti-pattern this rules out is a dense grid that rewards scanning; the
post-1925-finding is that a design which is faster to *read* beats one that is
faster to *scan* for exactly this population. Large type, one decision per row,
no colour-only signals.

**The incumbent is a VMS they already know.** They have muscle memory for
Genetec or Milestone, a muscle memory for what a "live view" is, and a muscle
memory for how alarms arrive. Design consequence: do not invent navigation
metaphors. `Live view`, `alarm`, `bookmark`, `PTZ preset` and `workstation` are
their words, not ours. Do not hide the VMS — our value is partly that we sit
above something they can still open. And do not make them learn a new job; make
their existing job measurable and then faster.

### 1.2 Design constraints that follow, stated as refusals

| Constraint | Because | What it refuses |
|---|---|---|
| Dark-first, no pure black, no pure white | dark room, long dwell | A `--bg: #000` and `#fff` pair; a light mode as the default |
| Body ≥ 15px, tables ≥ 14px, never below 12px for metadata | night, fatigue, age of the operator population | "Dense enterprise grid" as a flex |
| Every action keyboard-reachable; no pointer-only affordance | an operator with one hand, a phone, or a keyboard they can feel at 3am | Drag-and-drop as the only way to do anything |
| No client-side JavaScript (§4.2) | the CSP is a security control we do not trade | Any interaction that cannot survive a full page reload |
| The site clock, never the host's, always with an offset | the operator compares against a guard's account of the same minute | `time.Now()` in a template; a timestamp without `Z` or `±hh:mm` |
| English, LTR, `lang="en"` and `dir="ltr"`, no per-element direction | `AGENTS.md` — product decision, no second direction exists | A `dir` attribute on any element; a translated console |
| Absence is labelled, never blank | invariant 3 | A `—` that means "we did not look" the same as it means "there was nothing" |
| No colour-only signal, ever | ~8% of men have a red-green deficiency and this population skews to it | A severity encoded only in hue |
| Every screen has five states | §3 | A component that only knows "loaded" |

---

## 2. Information architecture

### 2.1 The shape of the surface across M3–M7

The product's surface changes character four times. This is not a redesign
cycle; it is the same navigation gaining one more primary destination as each
milestone's work becomes available. The order is deliberate: **the exception
queue ships before the autonomy that fills it** (`01` M7 reduced-scope clause —
if we ship autonomy with a worse surface than the alert list we replaced,
operators route around us).

```
M1  incident list ──────────────────────────────▶ one incident, four outcomes
      the whole product is one screen and a form

M2  incident list ──▶ investigation workspace
      the operator stops searching cameras by hand;
      a question in, an evidence-backed answer out

M3  incident list ──▶ investigation workspace ──▶ playbook / recommendation review
      the system starts proposing; the policy engine decides what is
      permitted and a rejected proposal never reaches the operator

M4  + the estate
      devices, access control, sensors — the graph behind the incidents
      becomes something an operator can navigate directly

M5  + the fleet
      sites, health, licences, versions, identity. The operator is no longer
      looking at one site; the nav is site-scoped at the top.

M6  + the site's own behaviour
      baselines, drift, site memory, proposed rules pending approval

M7  the exception queue becomes the home page
      not 813 alerts — 12 incidents, of which 9 were handled automatically,
      2 need review and 1 is urgent
```

### 2.2 Site map

```
/                                     redirect: session? → /incidents : /login
│
├── /login                            [M1]  MFA step
│
├── /incidents                        [M1]  PRIMARY. the feed
│   ├── /incidents/{id}               [M1]  the incident: evidence, verification,
│   │                                 attribution, events, dispositions
│   ├── /incidents/{id}/evidence      [M2]  the evidence bundle for an incident
│   ├── /incidents/{id}/explain       [M2]  the why, in prose + citations
│   ├── /incidents/{id}/timeline      [M2]  one timeline, all sources
│   └── /incidents/{id}/package      [M3]  the exportable incident package
│
├── /investigate                      [M2]  PRIMARY. ask a question
│   ├── /investigate/{qid}            [M2]  the plan, the evidence, the answer
│   └── /investigate/{qid}/evidence   [M2]  citations, resolved
│
├── /exceptions                       [M3]  queue of things the system did
│   ├── /exceptions/{id}              [M3]  the proposal or the auto-handled
│   └── /exceptions?queue=review|urgent|auto  the tabs
│
├── /playbooks                        [M3]  versioned SOPs: draft, simulated,
│   └── /playbooks/{id}               active, superseded. never edited in place
│
├── /estate                           [M4]  the graph, navigated
│   ├── /estate/sites/{siteId}
│   │   ├── /estate/sites/{siteId}/devices
│   │   ├── /estate/sites/{siteId}/doors
│   │   ├── /estate/sites/{siteId}/zones
│   │   └── /estate/sites/{siteId}/cameras
│
├── /fleet                            [M5]  the organisation above the site
│   ├── /fleet/health                 versions, health, licences, drift
│   ├── /fleet/sites
│   ├── /fleet/identity               OIDC/SCIM, roles, ABAC policy
│   └── /fleet/autonomy               [M7]  the A0–A4 dial, per site
│
├── /intelligence                     [M6]  this site's own behaviour
│   ├── /intelligence/baselines
│   ├── /intelligence/drift
│   ├── /intelligence/memory
│   └── /intelligence/proposals       [M6]  pending AI-proposed rules
│
├── /rules                            [M1]  store, simulate, activate, rollback
├── /audit                            [M1]  ledger, checkpoints, seals
├── /ops                              [M1]  dead letters, clock drift, health
└── /evidence/{evidenceId}            [M1]  one artefact, hash and all
```

### 2.3 Route table — the real surface

`internal/api/handlers.go` `RegisterRoutes`, plus `internal/api/server.go`.
Everything marked ✅ exists and is wired to a real service; the console tests in
`internal/api/api_test.go` wire real `incidents.Service`, `bundles.Service` and
`identity.Service` behind the routes rather than stubs, deliberately.

| Route | Method | Capability | Screen | State |
|---|---|---|---|---|
| `/v1/health` | GET | none | — | ✅ `server.go:71` |
| `/v1/health/ready` | GET | none | — | ✅ `server.go:72` |
| `/v1/auth/login` | POST | none | `/login` step 1 | ✅ |
| `/v1/auth/mfa/verify` | POST | none | `/login` step 2 | ✅ |
| `/v1/auth/session` | GET | any session | — | ✅ |
| `/v1/auth/session` | DELETE | any session | sign out | ✅ |
| `/v1/incidents` | GET | `incident.read` | `/incidents` | ✅ |
| `/v1/incidents/{id}` | GET | `incident.read` | `/incidents/{id}` | ✅ |
| `/v1/incidents/{id}/explanation` | GET | `incident.read` | the "why" block | ✅ |
| `/v1/incidents/{id}/dispositions` | POST | `incident.disposition` | the four-outcome form | ✅ API · **M1 console form pending** |
| `/v1/incidents/{id}/evidence` | GET | `evidence.read` | evidence list | ✅ |
| `/v1/evidence/{evidenceId}/content` | GET | `evidence.read` | the artefact | ✅ |
| `/v1/audit/ledger` | GET | `audit.read` | `/audit` | ✅ |
| `/v1/rules/bundles` | GET | `rules.read` | `/rules` | ✅ |
| `/v1/rules/bundles` | POST | `rules.write` | store, **never activates** | ✅ |
| `/v1/rules/bundles/{version}/activate` | POST | `rules.activate` | activate with reviewed diff digest | ✅ |
| `/v1/rules/bundles/active/rollback` | POST | `rules.activate` | rollback | ✅ |
| `/v1/ops/dead-letters` | GET | `dead_letter.read` | `/ops` dead-letter table | ✅ |
| `/v1/ops/clock-drift` | GET | `incident.read` | `/ops` drift table | ✅ |
| `/` | GET | none | redirect | ✅ |
| `/incidents` | GET | any session | `/incidents` | ✅ `incidents.html` |
| `/incidents/{id}` | GET | any session | `/incidents/{id}` | ✅ `incident.html` |
| `/login` | GET | none | `login.html` | ✅ |
| `/static/{file}` | GET | none | `console.css`, embedded | ✅ |

**M1 console work that adds no new route** — the disposition form posts to
`/v1/incidents/{id}/dispositions` from a server-rendered form with the session's
CSRF token, and every other screen is a template plus a GET. The reason §4.2
gives for why that is a constraint rather than a limitation.

### 2.4 Where M2–M7 add routes

| Milestone | New API routes | New console routes | Nav change |
|---|---|---|---|
| M2 | `GET /v1/evidence` (search), `POST /v1/investigations`, `GET /v1/investigations/{qid}`, `GET /v1/incidents/{id}/timeline`, `GET /v1/incidents/{id}/summary` | `/investigate`, `/investigate/{qid}`, `/incidents/{id}/explain`, `/incidents/{id}/timeline` | second primary item: Investigate |
| M3 | `GET /v1/playbooks`, `POST /v1/playbooks/{id}/simulate`, `POST /v1/playbooks/{id}/activate`, `GET /v1/exceptions`, `POST /v1/exceptions/{id}/{approve,reject,undo}`, `POST /v1/incidents/{id}/package` | `/exceptions`, `/exceptions/{id}`, `/playbooks`, `/playbooks/{id}` | third primary item: Exceptions |
| M4 | `GET /v1/estate/...` for devices, doors, zones; `GET /v1/graph/{relation}` | `/estate/...` | fourth item: Estate (3) |
| M5 | `GET /v1/fleet/...`, `POST /v1/fleet/sites/{id}/commands`, IdP config | `/fleet/...` | site scope moves to a top-level selector; nav gains Fleet (2) |
| M6 | `GET /v1/baselines`, `GET /v1/drift`, `GET /v1/site-memory`, `GET /v1/rule-proposals`, `POST /v1/rule-proposals/{id}/{approve,reject}` | `/intelligence/...` | fifth item: Intelligence |
| M7 | `GET /v1/autonomy`, `POST /v1/autonomy/{siteId}` (level change, with blast-radius preview), `GET /v1/agents/{incidentId}` | `/fleet/autonomy` | **`/exceptions` becomes the home page** |

**Route naming rules, stated because they will be violated.** Plural nouns for
collections, singular ids otherwise, hyphenated multi-word, never camelCase,
never a verb in a GET path. `POST /v1/exceptions/{id}/approve` uses a verb
because it is a command, not a sub-resource — a command's endpoint is
`POST /v1/incidents/{id}/dispositions` because the disposition is a thing that
was created, and `POST /v1/autonomy/{siteId}` is a command because the
autonomy level is a state, not a thing. The test for which is which: if the
request creates a row with an identity of its own, it is a noun; if it changes
a state that has no history, it is a verb and it gets an audit row either way.

---

## 3. The three critical flows

### 3.1 Flow A — M1 disposition

The whole M1 product in one flow. Steps, states, keyboard, undo.

**Step 0 — arrive.** The operator is on `/incidents` and presses `Enter` on a
row, or clicks it, or presses `j` then `Enter` in list navigation. The row
expands in place (§3.1.1) rather than navigating, because a disposition is a
decision made while comparing, and navigating destroys the comparison.

**Step 1 — read the verdict.** The verification state, its reason code, and what
participated, above the fold, before any evidence.

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ CAPTAIN  /incidents            Site: WH-DEPOT-1     s.okafor · operator · NO
│                                                   SECOND FACTOR        Sign out│
├──────────────────────────────────────────────────────────────────────────────┤
│ Restricted zone breach — Loading Bay 2                     [CRITICAL] ● SEALED│
│ inc_01J8XQ4M2R · SITE-B1 · opened 2026-03-01 09:00:12Z (09:00:12+02:00)      │
├──────────────────────────────────────────────────────────────────────────────┤
│  VERDICT                                                                  1/4 │
│  ┌───────────────────────────────────────────────────────────────────────┐   │
│  │ ● CORROBORATED      reason: zone_breach.confirmed_by_second_camera   │   │
│  │ Camera CAM-3 corroborated the restricted-zone trigger from CAM-7      │   │
│  │ within the site's 2s skew tolerance.                                │   │
│  │                                                                       │   │
│  │  supporting events      evt_9a41c2  evt_7be003                       │   │
│  │  AI observations        obs_51d8e0  (restricted-zone, production)    │   │
│  │  contradicting events   —  none                                    │   │
│  │  excluded from corroboration  —  none                               │   │
│  └───────────────────────────────────────────────────────────────────────┘   │
│                                                                               │
│  [ 1 EVIDENCE 4 ]  [ 2 ATTRIBUTION 3 ]  [ 3 EVENTS 9 ]  [ 4 DISPOSITIONS 0 ] │
├──────────────────────────────────────────────────────────────────────────────┤
│  EVIDENCE CITED                                                       [1/4]   │
│  ┌────┬──────────────────────────┬────────┬─────┬──────────┬────────────────┐ │
│  │ #  │ WHAT IT SHOWS            │ TIER   │SIZE │ CAPTURED │ SHA-256        │ │
│  ├────┼──────────────────────────┼────────┼─────┼──────────┼────────────────┤ │
│  │ 1  │ [snapshot] CAM-7 09:00:07 │ A     │184K │09:00:07Z │ a3f9c2e1b884…  │ │
│  │    │ Loading Bay 2, one person │        │     │(+02:00) │ open ↗         │ │
│  ├────┼──────────────────────────┼────────┼─────┼──────────┼────────────────┤ │
│  │ 2  │ [clip 8s]  CAM-3 09:00:09  │ A     │1.2M │09:00:09Z │ 7c04be91a2f3…  │ │
│  │    │ adjacent angle, same frame │        │     │(+02:00) │ open ↗         │ │
│  ├────┼──────────────────────────┼────────┼─────┼──────────┼────────────────┤ │
│  │ 3  │ [access]  BADGE-4417      │ A     │  2K │09:00:06Z │ e11c04aa7719…  │ │
│  │    │ DENIED at Loading Bay 2   │        │     │(+02:00) │ open ↗         │ │
│  ├────┼──────────────────────────┼────────┼─────┼──────────┼────────────────┤ │
│  │ 4  │ [observation] CAM-7       │ C     │  1K │09:00:11Z │ (see 2)         │ │
│  │    │ what the model saw        │        │     │(+02:00) │ open ↗         │ │
│  └────┴──────────────────────────┴────────┴─────┴──────────┴────────────────┘ │
│  ▸ 3 more of 7 evidence items                                            >   │
│  ⓘ  2 of these 4 items independently show the same person. Corroboration    │
│     count is why this incident is CORROBORATED and not UNVERIFIED.  [why?]  │
├──────────────────────────────────────────────────────────────────────────────┤
│  DISPOSITION                                                            [4/4]  │
│  What did the system get right?                                            │
│  ( ) A real incident      ( ) Not a real incident      ( ) Nothing to report│
│                                                                               │
│  What are you doing about it?                                               │
│  ( ) Escalate to the duty manager      → requires a second factor            │
│  ( ) Acknowledge and watch                                                    │
│  ( ) Dismiss                             → requires a second factor            │
│                                                                               │
│  Reason code:  [ not_a_restricted_person        ▾ ]                           │
│  Notes:        [                                                    ]        │
│                                                                               │
│              [ Confirm — records your name, the time, and this reason ]      │
├──────────────────────────────────────────────────────────────────────────────┤
│ request 7fq2m1c4            rendered 2026-03-01 09:03:41Z      tenant tn_9c  │
└──────────────────────────────────────────────────────────────────────────────┘
```

**States on this screen, all five, and what each looks like:**

| State | Rendering |
|---|---|
| **Loading** | The shell (topbar, breadcrumb, id, timestamp, disposition form) renders immediately; the verdict and evidence blocks are `aria-busy="true"` placeholders that keep their height. Layout does not shift when they fill. Because there is no client JavaScript, "loading" is *the previous render*: the page is a plain GET and the data is present on arrival or the request errored. There is no spinner, and this is stated rather than hidden — a spinner on a server-rendered page is a lie about a request that either completed or failed before a line was painted. |
| **Empty** | `No evidence was captured for this incident.` plus the reason it is empty — `the evidence fetch failed at 09:00:14Z` or `no rule cited evidence` — never a bare `—`. The disposition form stays fully available: an operator with no evidence can still escalate. |
| **Partial** | Evidence arrived for 4 of 7 items, or 2 of 3 cameras responded. Each missing item is a row with its own reason, not a gap. A camera that timed out is shown as `not retrieved — camera did not respond within 5s`, because an operator who cannot tell "no evidence exists" from "we did not get it" will dismiss the incident and be wrong. |
| **Error** | `The incident could not be read: <reason>. request 7fq2m1c4`. The `request_id` is on the page because it is the footer line and because it is the only thing support can use. The disposition form is disabled with a stated reason, not silently inert. |
| **AI unavailable** | §5. The verdict block shows `UNVERIFIED` with the degradation reason, and the attribution tab says which observations did not arrive. The form is **unmodified** — this is the state that matters most and it must not feel like an error. |

**Keyboard.** `1`–`4` jump to a tab. `j` / `k` next / previous row. `o` open the
selected evidence item in a new tab. `e` expand all evidence. `c` focus the
first radio in the current group. `Enter` submits — but only from a field, and
only for the non-step-up outcomes; `escalated` and `dismissed` submit into the
step-up interstitial (§3.1.2) rather than straight to the server, because a
mis-keyed `Enter` must never be the thing that wakes a duty manager at 02:00.
`Esc` leaves the form. `?` opens the shortcut list.

**Undo.** A disposition is a new row, never an edit
(`TestDispositionDoesNotOverwriteTheObservation` is the invariant; the UI
mirrors it). The undo is a *superseding* disposition, POSTed to the same
endpoint with a fresh step-up, and it renders on the page as:

```
DISPOSITION HISTORY                                                    [4/4]
  2026-03-01 09:04:11Z  s.okafor    dismissed        "contractor, checked in"
  2026-03-01 09:03:58Z  s.okafor    superseded by the above
  ▲ 2 rows. Both replay. The ledger has both.
```

There is no delete, there is no edit-in-place, and the copy says so. An operator
who corrects a decision in the morning can see exactly what they said at 02:00,
which is the property that makes the whole product defensible.

#### 3.1.1 Why the row expands rather than navigates

The incumbent is a VMS the operator has used for years, and in every one of them
a row navigates. We are breaking that pattern deliberately, once, for the single
most frequent action in the product. The justification is narrow: a disposition
requires comparing the verdict against the evidence against the recent
dispositions in the feed, and a navigation makes the first two impossible. Every
other row in the product navigates. If expansion turns out to be wrong in
research (§8), it is one template and one handler.

#### 3.1.2 The step-up interstitial

```
┌──────────────────────────────────────────────────────────────────────────────┐
│  This will escalate an incident.                                            │
│                                                                              │
│  inc_01J8XQ4M2R · Restricted zone breach — Loading Bay 2                     │
│  a real incident · reason: not_a_restricted_person                          │
│                                                                              │
│  Recorded as:  s.okafor, escalated, 2026-03-01 09:04:11Z                     │
│  The duty manager on shift will be notified.                                 │
│                                                                              │
│  Your second factor is required again.                                       │
│  [ 6-digit code                    ] [ Confirm  ]      [ Cancel  ]           │
│                                                                              │
│  This can be undone, but the notification cannot.                            │
└──────────────────────────────────────────────────────────────────────────────┘
```

It states what will happen, to whom, and what cannot be taken back. The
notification is the irreversible part, so the copy names it. `Cancel` returns
to the form with everything still filled in.

### 3.2 Flow B — M2 investigation

**Step 0 — the operator asks.** The question goes in the operator's own words,
and the examples come from shift shadowing (§8), not from a marketing page.
Default suggestions are the last three questions this operator asked, because the
second question in a night is usually a variant of the first.

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ CAPTAIN  /investigate                                    s.okafor · operator  │
├──────────────────────────────────────────────────────────────────────────────┤
│  Ask about this site. Every answer links to the evidence behind it.           │
│                                                                              │
│  [ what happened at the loading bay last night?                  ]  [ Ask ]   │
│                                                                              │
│  Recent:  ·who used BADGE-4417 last week  ·was there anyone in bay 2 on Fri  │
│           ·show me the door 18 alarms this week                             │
├──────────────────────────────────────────────────────────────────────────────┤
│  PLAN                                                                   4/7  │
│  Built by the local model. It plans; it does not assert. Every step is        │
│  checked against the policy engine before it runs.                           │
│                                                                              │
│   1 ✓ alarms on Loading Bay 2 between 22:00 and 06:00          7 found     │
│   2 ✓ every camera covering bay 2 and bay 3, ±5 min              4 cameras  │
│   3 ✓ access events for those doors in the same window          31 events  │
│   4 ✓ observations already stored for matching windows          2 found     │
│   5 ✓ prior incidents in bay 2, last 30 days                    2 found     │
│   6 · a clip from CAM-7 covering the busiest moment             fetching…   │
│   7 · a clip from CAM-3 for corroboration                       queued       │
│                                                                              │
│   ⓘ  1–5 are stored facts. Step 6 needs a new retrieval, so it can fail.     │
├──────────────────────────────────────────────────────────────────────────────┤
│  ANSWER                                                                    │
│                                                                              │
│  Between 22:00 on Friday and 06:00 on Saturday there were 7 alarms at        │
│  Loading Bay 2. One (23:41) was a confirmed restricted-zone breach by a      │
│  person carrying BADGE-4417 — [evidence 1], with a second camera [evidence   │
│  2] and a matching access denial [evidence 3].                              │
│                                                                              │
│  The other 6 were all the same nuisance: the roller door was open and the    │
│  camera view into bay 2 was blocked by a parked trailer. [evidence 4]       │
│  Operators dismissed 5 of the 6 as `obstruction` in the last 30 days.        │
│  [evidence 5]                                                                 │
│                                                                              │
│  UNRESOLVED                                                                 │
│  · The 23:41 clip is 4 seconds short at the end; the person's exit is not    │
│    visible in any camera. [evidence 1]                                       │
│  · CAM-11 covering the bay 2 exit did not respond to the 23:41 retrieval.    │
│    This is a gap in the evidence, not a finding.                              │
│                                                                              │
│  ─────────────────────────────────────────────────────────────────────────── │
│  ⚠ This answer is derived. It is not a record and it is not authoritative.   │
│    The incidents it cites, and the dispositions on them, are.  [replay ↗]    │
└──────────────────────────────────────────────────────────────────────────────┘
```

**The design decisions this wireframe makes, each of which is a test of a
principle:**

- **The plan is shown, not hidden.** An operator who cannot see why a system
  looked at 4 cameras and not 40 has no way to calibrate trust, and no way to
  tell a bug from a design. The plan is also the "why am I seeing this" affordance
  (§6).
- **Every claim carries its citation inline.** Not a footer, not a reference
  list at the end: `[evidence 3]` in the sentence, so the operator never has to
  hold a claim in their head while hunting for its source.
- **Unresolved is a first-class section with the same visual weight as the
  answer.** A system that reports only what it found is a system that is confident
  more often than it should be.
- **The footer is a demotion, not a caveat.** "Derived, not authoritative, here
  is the replay" is a product statement, and it is the same statement the
  evaluation harness makes about the model.
- **Retrieval can fail and says which step failed.** Step 6's failure is named,
  and its consequence ("a gap in the evidence, not a finding") is stated
  explicitly, because the failure mode of an investigation system is confidently
  answering with less than it appears to have.

**States.** Loading: the question, the plan skeleton and the answer skeleton
render, with the plan steps marked `queued`. Because this flow genuinely is
multi-step and server-driven, each step's state is server-rendered; a step that
has not run says `queued`, a step that failed says why, and the answer never
appears before its citations resolve. Empty: no plan matched, and the answer says
so. Partial: steps 1–5 returned, 6–7 failed. Error: the plan is refused by
policy, and the refusal is the message. AI-unavailable: the local model is down,
steps 1–5 (stored facts) still run, steps 4 and 6–7 are marked unavailable, and
the answer is the deterministic one — a timeline with no prose. **The
investigation is still useful without the model**; that is invariant 3 applied to
a whole surface, and it is the first thing to test in M2.

**Keyboard.** `Enter` asks. `↑`/`↓` move through plan steps. `1`–`7` expand a
step's result. `o` opens the selected citation. `r` re-runs the answer (a new
plan, not a re-render). `Esc` returns to the question box with the text intact.

**Undo.** There is nothing to undo in asking a question: no state changes, no
side effects, no audit row beyond the read. A `r` re-run supersedes the previous
answer in the view, and both plans are replayable for 30 days.

### 3.3 Flow C — M7 exception queue triage

The destination surface. Nine handled automatically, two need review, one
urgent, in one screen, in one pass.

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ CAPTAIN  /exceptions                    Site: WH-DEPOT-1 (3 assigned)        │
│                 [ NEEDS REVIEW 2 ]  [ URGENT 1 ]  [ AUTO-HANDLED 9 ]        │
│                 s.okafor · operator · MFA ✓                    Sign out     │
├──────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  URGENT  (1)                                                                 │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │ ●● Loading Bay 2 — restricted zone breach          inc_01J8X  09:00:12 │  │
│  │    AUTO-HANDLED: escalated at 09:00:14 (policy P-14 permits)           │  │
│  │    │                                                                     │  │
│  │    ├─ 09:00:14  action: escalate → duty-manager                       │  │
│  │    │             outcome: DELIVERY NOT CONFIRMED (webhook timeout 5s)   │  │
│  │    ├─ 09:00:14  playbook: PB-BAY2-REVIEW v3 · step 2 of 4              │  │
│  │    └─ 09:00:13  why: BADGE-4417 denied at bay 2 + person in zone       │  │
│  │                 [ plan ]  [ evidence 4 ]  [ replay ]                    │  │
│  │    ⚠ This action was not verified as delivered. Treat as NOT escalated. │  │
│  │                                                                                          │  │
│  │    YOU MUST: ① confirm who received it, or ② re-escalate.              │  │
│  │    [ Confirmed delivered — enter who ]  [ Re-escalate (step-up) ]      │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
│  NEEDS REVIEW  (2)                                                           │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │ ●  Perimeter east — fence contact                    inc_01J8W  06:12:44 │  │
│  │    AUTO-HELD: notified, not closed. Confidence 0.61 (min 0.55)         │  │
│  │    Reason held: no second camera covers this fence span.               │  │
│  │    Recommended: request evidence from CAM-22 (63 m away, 41° arc)      │  │
│  │    [ Approve recommendation ]  [ Dismiss (step-up) ]  [ Plan ]         │  │
│  ├───────────────────────────────────────────────────────────────────────┤  │
│  │ ●  Loading Bay 3 — door forced open                    inc_01J8V  03:41:02 │  │
│  │    AUTO-HELD: bookmark created, incident open. Door has been open 6h.   │  │
│  │    ⚠ Site memory: this door has been forced 14× in 90 days. CAM-12 has  │  │
│  │       not been online for 11 days — this is likely a coverage fault,   │  │
│  │       not a break-in.  [ why? ]  [ open CAM-12 health ]                 │  │
│  │    [ Accept — keep incident open ]  [ Classify as equipment fault ]     │  │
│  │                                    [ Dismiss (step-up) ]                │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
│  AUTO-HANDLED  (9)  ─ collapsed by default, one line each ──────────────────  │
│  ✓ Bay 2 alarm 08:14 · nuisance (obstructed) · closed, no action      [undo] │
│  ✓ Bay 2 alarm 07:52 · nuisance (obstructed) · closed, no action      [undo] │
│  ✓ Corridor motion 05:31 · within baseline · closed, no action        [undo] │
│  ✓ Perimeter south 04:02 · within baseline · closed, no action        [undo] │
│  … 5 more, 8h 41m operator time avoided                                     │
│      [ why are these 9 closed? ]  [ see all 9 ]                              │
│                                                                              │
├──────────────────────────────────────────────────────────────────────────────┤
│ Autonomy: A3 (execute with policy) · [ change ]   •  human attention: 2m 41s │
│ request 3kd8xq01        rendered 2026-03-01 09:07:22Z        tenant tn_9c   │
└──────────────────────────────────────────────────────────────────────────────┘
```

**The design decisions, and each is load-bearing:**

- **The urgent item is not an alert, it is a statement of what the system already
  did.** "AUTO-HANDLED: escalated at 09:00:14" and then an unconfirmed delivery.
  The operator's job is not to handle the alarm; it is to close a loop the
  machine could not close. This is `01` M7's closed loop surfacing in the UI.
- **The unconfirmed action is the loudest thing on the screen**, because an
  action that is not verified as having succeeded is a failed action, not a
  completed one. It is the only state on this screen rendered at full contrast
  with a fill, and it is a fill rather than a border for the same reason the
  `.breakglass` badge in `console.css` is a fill.
- **Auto-handled rows are collapsed and last.** They are proof, not work. Nine
  lines of "closed, no action" is the receipt the operator is owed — it is how
  they learn the system is working and how they audit it — and it is below the
  two things they must actually decide.
- **Confidence is shown with its threshold.** `0.61 (min 0.55)` is the whole
  number and the number it cleared. A confidence score alone is a number an
  operator cannot check; a threshold turns it into a policy question they can
  have an opinion about.
- **Site memory is injected as a hypothesis, never a conclusion.** "Likely a
  coverage fault, not a break-in" with a `[why?]` link and a link to CAM-12's
  health. The system that says "this is probably fine" without showing the
  camera that has been down for 11 days is the system that gets an operator
  killed.
- **Every automated row has `[undo]`.** Undo is a *new* action with a new
  justification, audited identically, and the row moves to NEEDS REVIEW. It is
  not a deletion and the confirmation says so.
- **The autonomy level is visible on every screen that autonomy touches**, with
  a `[change]` link, because the operator must never have to guess what the
  machine is permitted to do on their site.

**States.** Empty (all three tabs empty): `Nothing needs you. The last 12
incidents were handled automatically.` with the last auto-handled time and the
attention minutes avoided — the empty state is the product's best news and it
says so. Urgent empty: `No urgent exceptions. 2 need review.` Partial: some plan
steps ran, some failed, each named. Error: a queue read failure renders the last
known queue with a staleness timestamp, never a blank screen that looks like an
empty queue. AI-unavailable: the nine auto-handled rows change provenance. They
are still closed — they were closed by the deterministic core — and the reason
line reads `closed without an AI observation (model_unavailable)` rather than
`auto-handled`. **They do not move to NEEDS REVIEW.** An AI outage must not
manufacture work, and if it does, we have turned a degradation into a burden and
also lied about why the incident closed.

**Keyboard.** `Tab`/`Shift-Tab` between exceptions. `1` urgent, `2` needs review,
`3` auto-handled. `j`/`k` next/previous. `Enter` expands the selected exception
and focuses its primary action. `a` approve the recommendation. `u` undo the
selected auto-handled row. `r` re-run the plan. `Esc` collapses. `?` shortcuts.
Every step-up action requires its second factor and nothing else does, so the
common case stays fast and the consequential case is deliberate.

**Undo.** The row returns, already triaged, with the previous auto-action
recorded and struck through — `auto-dismissed 09:00:14, undone by s.okafor
09:07:40`. Not a deletion, not a re-open of a closed row, and the operator can
see exactly what the machine did while they were not looking.

---

## 4. Design system

### 4.1 Tokens

The console is one stylesheet, served from the embedded FS, with no framework
(`internal/api/console.go` `Static()`), and the tokens are the ones already in
`console.css` `:root`. Values below are the current ones; the additions are
marked.

**Colour — surface and text**

| Token | Value | Use |
|---|---|---|
| `--bg` | `#101418` | page. Not `#000` — a pure-black field next to a monitor's black bezel loses the window edge |
| `--panel` | `#171d23` | topbar, cards, table headers |
| `--line` | `#2a333d` | borders, table rules |
| `--text` | `#dfe6ed` | body. `#e8eef4` on `--panel` for headings (ADD) |
| `--muted` | `#8b98a5` | metadata, hints, units. Used at ≥14px only |
| `--accent` | `#4c9be8` | links, focus ring, primary button |
| `--ok` `--warn` `--err` `--info` | `#3fb950` `#d29922` `#f85149` `#58a6ff` | semantic, see §4.3 |

**Colour — semantic.** `--ok`, `--warn`, `--err`, `--info` map to the four
states an operator must distinguish under fatigue: *settled*, *look at this*,
*act now*, *being worked*. They are not five severities (§4.3).

**Type.** System stack, as today: `-apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif`, 15px/1.5 body. Monospace `ui-monospace, "Cascadia Mono", Consolas, monospace` at 0.9em for hashes, ids and code — **not** for content, because a page of monospace is a page nobody scans. Scale: 12 / 13 / 14 / 15 / 17 / 20 / 26px. **13px is the floor for metadata; 12px is the floor for a hash and nothing else.** Line height 1.5 body, 1.35 headings, 1.4 in tables. A dense table row is 44px, a comfortable one is 52px (§4.4).

**Spacing.** 4px base. Scale 4 / 8 / 12 / 16 / 20 / 28 / 40. Section gaps use 28, matching the current `h2` margin in `console.css`. Padding 20px page (as today), 10/14 inside alerts.

**Elevation.** No shadows on a dark surface — they read as smudges. Depth is
expressed by three levels: `--bg` (page) → `--panel` (a raised region) → a
1px `--line` border, plus one fill state for the single most urgent thing on a
screen (`.breakglass` today, the unconfirmed-action row in §3.3). **At most one
filled element per screen.** A screen with three filled elements has no
hierarchy.

**Density.** Two: `comfortable` (52px rows) is the default and is never
switched; `compact` (44px rows, 14px type) is a per-site setting, not a
per-user one, because two operators at one desk must see the same rows. No third
density, ever. A density system with three levels is a density system nobody
enables.

### 4.2 The constraint that decides what components are possible

`Content-Security-Policy` is `default-src 'none'; script-src 'self'; style-src
'self'; frame-ancestors 'none'; object-src 'none'; base-uri 'none'`
(`TestContentSecurityPolicyForbidsInlineScript` asserts each of these, and
asserts that neither `unsafe-inline` nor `unsafe-eval` appears anywhere in the
header). `TestConsoleIsLeftToRight` additionally asserts that a rendered console
page contains no `<script` element and no `style=` attribute.

**Therefore: the console has no client-side JavaScript at all.** This is not a
limitation to be engineered around reluctantly; it is a product property, and it
is worth stating what it buys:

- A stored-XSS in a template is not reachable. `severityClass` and `stateClass`
  are closed maps precisely because a severity from an untrusted rule bundle
  reaches a class attribute, never a style one.
- No client session state to lose, so interruption is cheap: everything durable
  is in the URL, in the database, or in the form the operator has not submitted.
  If they walk away mid-form, the form is still there.
- Nothing to break at 3am that a site operator cannot diagnose from a log.

**And it costs, concretely.** Every interaction is a form POST and a full page
render. That means:

| Normally a component | Here it is | Consequence for the design |
|---|---|---|
| modal dialog | a dedicated route, with the form as the whole page | A step-up interstitial is a page, so it needs its own back path (§3.1.2) |
| toast | a `role="status"` element in the shared `messages` block, from the redirect's query string | A success acknowledgement survives a refresh; a toast does not |
| client-side validation | server-side validation with per-field errors on redisplay | The redisplayed form must preserve every field the operator typed |
| live search | a `GET` form with a submit key | Results change on submit, not on keystroke — the UI must say so, and must not look like it is searching |
| infinite scroll | `limit` + a "next page" link, as `parseLimit(…, 50, 200)` already does | A cap is a real number the operator is told about, not an illusion |
| inline expand/collapse | `<details>`/`<summary>`, which are native and need no script | Used for the evidence rows in §3.1; the open state is in the DOM and survives a reload |
| keyboard shortcuts | `<accesskey>` and native focus order | `accesskey` is the only script-free way to get §3's shortcuts, and it is genuinely limited by which keys browsers reserve. **Decision: the shortcut list in the UI is authored against the browsers we support and the real key set is a per-milestone verification item, not an assumption.** Where `accesskey` cannot carry it, the affordance is a visible key hint next to the control |
| optimistic update | none | Every action shows the server's result, so a slow site feels slow rather than lying. Acceptable: the alternative is an interface that looks fast and is wrong |

**No bundler, no framework, no component library, no CSS preprocessor.** The
stylesheet is hand-written CSS with custom properties, served from the embedded
FS with `Cache-Control: public, max-age=300`. A dependency that ships a JS
runtime is not a dependency we can take under the air-gap constraint in `00` §6.

### 4.3 Semantic colour, and the rule that colour is never the only channel

Severity, today: `sev-info`, `sev-low`, `sev-medium`, `sev-high`,
`sev-critical`, `sev-unknown` (`console.css`). Verification state: `CORROBORATED`,
`UNVERIFIED`, `CONTRADICTED`, `DUPLICATE`, `SUPPRESSED`
(`internal/rules/engine.go`). Drift band: `band-ok`, `band-warn`, `band-severe`.

**The encoding is a glyph plus a word plus a colour, in that order of weight.**
Never colour alone, and never a colour-only difference between two rows.

| Severity | Glyph | Word | Fill | Text on `--bg` | Contrast |
|---|---|---|---|---|---|
| info | `·` | info | none | `--info` `#58a6ff` | 7.0:1 |
| low | `✓` | low | none | `--ok` `#3fb950` | 8.2:1 |
| medium | `▲` | medium | none | `--warn` `#d29922` | 7.5:1 |
| high | `▲` | high | none | `--err` `#f85149` | 5.6:1 |
| critical | `●` | critical | `--err` | `#fff` on `--err` | 4.5:1 fill |
| unknown | `?` | unknown | none | `--muted` `#8b98a5` | 5.4:1 |

**Contrast targets, stated as targets and verified by a test, not asserted in
prose.** Body text and all severity/verification text on `--bg` and on `--panel`:
**≥ 4.5:1 (WCAG 2.2 AA for normal text)**. Large text (≥ 18.66px bold or 24px)
and non-text indicators — badge borders, focus ring, glyphs carrying meaning:
**≥ 3:1 (AA non-text)**. A filled badge's text against its own fill: **≥ 4.5:1**,
which is why `.sev-critical` is white-on-`--err` (4.5:1) rather than
`--warn`-on-`--err` (1.6:1). A new token or a changed value is not merged until a
contrast check runs; the check is a test, because the values will change again.

**Verification state, encoded separately from severity.** A `CORROBORATED`
incident can be `low` and a `SUPPRESSED` one can be `critical`; conflating them
means an operator cannot tell how much to believe it. Verification is a
*shape*: `CORROBORATED` a filled `●`, `UNVERIFIED` a half-filled `◐` with the
degradation reason in text, `CONTRADICTED` `✕`, `DUPLICATE` `⧉`, `SUPPRESSED`
`⊘` with the suppressing rule named. Severity is a *colour band*; verification
is a *glyph and a word*. Two channels, two meanings, no overlap.

**Colour-blind safety.** The severity set is `--info` / `--ok` / `--warn` /
`--err` / filled `--err`, chosen so that the three that matter most
(`medium`, `high`, `critical`) are distinguished by glyph (`▲▲●`), lightness
and fill, not only by hue — deuteranopes read `medium` and `high` as similar
hues, and the glyph carries the difference. Deuteranopia simulation is a
per-component check in the component test, not a one-time audit. The one
forbidden pattern is a red/green *pair* carrying opposite meanings with no
other channel, which is why `low` (`ok`, green) and `high` (`err`, red) both
carry glyphs and words.

**Light theme (ADD, M2).** Same tokens, different values, `--ok` and `--err`
chosen at their darkened equivalents for a white ground (a `#3fb950` on white is
2.2:1 and would fail). Light is a second theme validated by the same contrast
test; it is never generated by inverting the dark values.

### 4.4 Component inventory

What exists (`console.css` and the four templates) and what each milestone adds.
`●` exists, `○` planned.

**Layout and chrome**
`●` topbar (`head`) · `●` page shell + `main` + `main.narrow` · `●` footer with
rendered time, request id, tenant · `●` `messages` block (`role="alert"` /
`role="status"`) · `○` `breadcrumb` (M2) · `○` `site-scope` selector (M5) ·
`○` `nav.rail` (M3)

**Data display**
`●` `table` with the standard header treatment · `●` `dl.facts` (the 190px
definition grid) · `●` `badge` (severity) · `●` `badge` (state) · `●` `hint` ·
`●` `empty` · `●` `muted` · `●` `reason` · `●` `alert.{error,ok,degraded}` ·
`○` `tabs` (M1) · `○` `rows` (the incident feed as keyboard-navigable rows, M1)
· `○` `data-table--feed` (M7 exception queue) · `○` `plan` (M2) ·
`○` `citation` (M2) · `○` `timeline` (M2) · `○` `diff` (M6 rule proposals) ·
`○` `sparkline` for drift and attention minutes (M5) — drawn as inline SVG in
the template, which `style-src 'self'` permits, not as a script

**Forms and controls**
`●` `form.stack` with `<label for>` · `●` `form.inline` (sign out) ·
`●` `input[type=text|password]` · `●` `button` (primary) · `●` `button.link` ·
`○` `radio-group` for the disposition (M1) · `○` `step-up-form` (M1) ·
`○` `filter-bar` (M1, a GET form — not a live search, §4.2) ·
`○` `approvals` with per-item reason codes (M3) · `○` `blast-radius-preview`
(M7) · `○` `dial` for A0–A4 (M7) — a radio group with a preview panel, not a
slider, because a slider cannot have a step-up confirmation attached to a
specific value

**Status**
`●` `badge.band-{ok,warn,severe,unknown}` (drift) · `●` `badge.degraded` ·
`●` `strong.breakglass` · `●` `strong.warn` (NO SECOND FACTOR) ·
`○` `state-ai-unavailable` (M1, §5) · `○` `state-partial` · `○` `action-`
`unconfirmed` (M7) · `○` `countdown` (never a spinner, §3)

**Never built, and why.** A client-side grid with virtual scrolling (needs
script; the feed is capped at 200 and said so). A rich text editor (needs
script; notes are plain text, which is also what an audit row should hold). A
toast library. A modal library. A charting library — two inline SVGs and a table
with numbers, because a chart an operator cannot read in the dark is a picture,
and a picture is not a fact a shift can be accountable for.

---

## 5. AI unavailable: a first-class design state

### 5.1 Why it gets its own section

Invariant 3: *a missing AI observation is never a negative observation*. Every
rule that consumes a Tier C signal declares `if_ai_unavailable`. The engine
already expresses this — `rules.DegradedReason` is one of `model_unavailable`,
`no_runtime`, `timeout`, `error`, `absent`; a degraded observation is not
evidence; insufficient evidence is not a negative answer
(`TestMissingAIObservationDegradesToUnverified`,
`TestDegradedObservationIsNotEvidence`,
`TestInsufficientEvidenceIsNotANegativeAnswer`).

**A design that renders an absence as a blank, a dash, a grey row, or an error
has told the operator something false.** It has said either "there was nothing
there" or "the product is broken", and both are wrong. Absence gets its own
state, its own glyph, its own words, and it is rendered *in the place where the
observation would have been*.

### 5.2 The copy pattern

Five fields, always all five, never abbreviated, never dropped when the screen
is narrow:

```
1. WHAT HAPPENED      "The AI plane was unavailable."        — the fact, in the
                                                                past tense, no
                                                                hedging verb
2. WHEN              "Unavailable 09:00:11Z – 09:07:44Z"    — the window, in UTC
                                                             with the offset, so
                                                             a shift is verifiable
3. WHY               "reason: model_unavailable"           — the machine reason
                                                             from DegradedReason,
                                                             not a paraphrase
4. WHAT IT MEANS     "This incident was decided without it." — the consequence for
                                                             THIS decision, not a
                                                             general statement
5. WHAT IS STILL TRUE "What it saw: person present, 2 objects, no restricted-zone
                        claim. What it did not decide: whether the person was
                        authorised."                          — the boundary, stated
                                                             as two lists
```

Field 5 is the one that gets cut first by anyone optimising for space, and it is
the one that carries the product's claim. It is mandatory.

### 5.3 Literal microcopy

**The badge, wherever a degraded observation would appear:**

```
◐ AI UNAVAILABLE
```

Screen-reader text: `AI plane unavailable, this decision was made without it.`
Never `AI error`. Never `No data`. Never a dash.

**The inline block, on the incident, above the verdict:**

```html
<p class="alert degraded" role="note" data-state="ai-unavailable">
  <strong>AI plane unavailable 09:00:11Z – 09:07:44Z (reason: model_unavailable).</strong>
  This incident was decided without it. The verdict below is the deterministic
  one: two cameras, 4 seconds apart, inside this site's 2-second skew tolerance.
  <details>
    <summary>What it saw, and what it did not decide</summary>
    <dl class="facts">
      <dt>What it saw</dt>
      <dd>person present; 2 objects; vehicle in adjacent zone</dd>
      <dt>What it did not decide</dt>
      <dd>whether the person was authorised; whether the zone was restricted
          at the time (schedule read from site data, not the model)</dd>
      <dt>Rule behaviour</dt>
      <dd>PB-BAY2-REVIEW v3, step 2 · if_ai_unavailable: <code>unverified</code></dd>
      <dt>Observations stored</dt>
      <dd>0 of 1 expected · <a href="/v1/incidents/…/evidence">evidence still available
          for manual review</a></dd>
    </dl>
  </details>
</p>
```

**The empty attribution table, replacing `No AI observation participated in this
decision.`:**

```html
<p class="empty" data-state="ai-unavailable">
  No AI observation participated. The AI plane was unavailable for this decision —
  see the notice above. This is <strong>not</strong> a finding that nothing
  happened; it is a record that we did not have a second opinion.
</p>
```

That sentence exists because the honest version and the misleading version differ
by one clause, and the misleading one is what a table with a dash says.

**The incident feed row**, replacing the current `degraded:` prefix:

```html
<td>
  <span class="badge ai-unavailable" title="Raised without a live AI observation">
    <span aria-hidden="true">◐</span> AI UNAVAILABLE
    <span class="muted">model_unavailable</span>
  </span>
</td>
```

**The exception queue, M7.** An auto-handled row closed without an observation
reads `closed without an AI observation (model_unavailable)` and **stays
auto-handled**. It does not move to NEEDS REVIEW. An AI outage that manufactures
operator work is both a burden invented from nothing and a lie about why the
incident closed.

**The investigation workspace, M2.** Steps 4, 6 and 7 (observation retrieval and
the two clip fetches driven by the model) are marked unavailable; steps 1, 2, 3
and 5 — stored facts — still run. The answer is the deterministic timeline with no
prose, headed `Answered without the AI plane. This is a timeline of stored
events, not a conclusion.` It is useful anyway. That is the invariant working at
the scale of a whole surface, and it is the first thing tested in M2.

**The list of things this state may never say.** `AI error`. `Analysis failed`.
`No data`. `Confidence 0%`. A dash. A greyed-out row with no text. `Insufficient
data` as a *conclusion* — insufficient evidence is a model's statement about
itself, and when the whole plane is gone there is no model to have made one. A
percentage, because a percentage invites a comparison to the other rows and the
other rows are not percentages.

---

## 6. Trust, explainability and evidence UX

The operator's question is not "how does the AI work". It is: *can I defend this
click, and can I check it in 90 seconds at 04:00?* Everything here serves that.

### 6.1 Attribution: present, ranked, never buried

An `AIObservation` already carries `ProfileID`, `ProductionProfile`, `Model`,
`ModelHash`, `PromptTemplateID`, `PromptVersion`, `OutputHash`, `LatencyMS`,
`DegradedReason` — and `internal/observations` makes attribution mandatory
(`TestAttributionIsMandatory`), refuses unknown profiles and fields
(`TestUnknownProfileAndFieldAreRefused`), marks shadow profiles and stops them
affecting authoritative state (`TestShadowProfilesCannotAffectAuthoritativeState`),
and supersedes rather than overwrites on re-evaluation
(`TestReEvaluationSupersedesRatherThanOverwrites`).

**The surfacing rule: the id and the profile go in the main flow; the hashes go in
a `<details>` that is closed by default and openable in one keypress.**

```
TIER C · what the model contributed                                     obs_51d8e0
  Profile     restricted-zone-breach  v3                        [production]
  Answer      restrictedZoneBreach: true    personCount: 1
  Model       qwen2-vl-7b-instruct                                    2.4s
  Prompt      pz-restricted-v3                                     v3
  <details><summary>Full attribution</summary>
    model hash     sha256:4f8c1e9a2b77d0…      (full value, copyable)
    prompt hash    sha256:9a03bd77c1e5f42…
    input hash     sha256:c71fe0a44982b13…   ← exactly the bytes that were sent
    output hash    sha256:2e9b0ca4d17f83…
    observed at    2026-03-01 09:00:11Z (+02:00)
    recorded by    vlm-runtime-1  worker lease 4f2a…
    superseded     obs_9c1d0e2 at 09:04:11Z by re-evaluation (superseded, not
                   overwritten — both replay)
  </details>
```

**Why input hash is in the list at all.** It is the only field that ties an
observation to a specific bundle of bytes rather than to a version string. It is
also the field that makes "the model changed" a *provable* claim rather than a
hypothesis, and it is what an operator's lawyer will ask for. It is inside the
`<details>` for one reason: it is a 64-character string and an operator does not
need it to decide what to do at 04:00.

**Shadow profiles are visually distinct and never ambiguous.** A shadow
observation is rendered with `(shadow)` exactly as the current template does, it
is never counted in the corroboration total, and hovering states `measured
against live traffic; cannot affect any decision`. If an operator can mistake a
shadow measurement for something that changed their site's behaviour, the whole
shadow programme is unsafe.

### 6.2 Corroboration count

The count is the most load-bearing number in the product and it is currently
absent from the incident page: the evidence table lists items, and nothing says
"these three are independent of each other".

```
CORROBORATION · 2 independent sources
  1  CAM-7  Loading Bay 2            camera        tier A
  2  CAM-3  Loading Bay 3 (adjacent) camera        tier B
  3  BADGE-4417 access denied        access control tier A
  ⚠ 3 of these 7 evidence items are not independent of CAM-7 (same camera,
     adjacent window, or derived from it). They corroborate nothing.
```

The warning is the point. Evidence that is not independent is the most common
way a corroborated incident is weaker than it looks, and an operator who is told
"7 pieces of evidence" and not "2 independent sources" is being misled by an
accurate number.

### 6.3 The audit link

Every decision, disposition, action, activation and dead letter has one row in
the hash-chained ledger. Every screen that shows a state has a link to it.

```html
<a href="/audit?tenant=tn_9c&since=2026-03-01T09:00:00Z&subject=inc_01J8XQ4M2R">
  audit entries for this incident: 7
</a>
```

The link is scoped to the subject, not to a page number, and it opens at the
timestamp the operator is looking at. The audit page's header states the last
verified checkpoint sequence and whether it is current, because an operator
reading a ledger needs to know whether that ledger has been verified since
something they care about happened.

### 6.4 "Why am I seeing this"

One affordance, present on every screen, opening a `<details>` (no script, §4.2).
It answers four questions in this order, and nothing else:

```
WHY AM I SEEING THIS?
  1  What put this on my screen
     inc_01J8XQ4M2R · raised by rule zone_breach.confirmed v3 · priority 40
     triggered by evt_9a41c2 (CAM-7, motion) · 09:00:12Z
     6 other events matched the same rule and did not raise an incident —
     listed below, with the reason each was suppressed

  2  What the AI plane contributed
     1 observation, production profile restricted-zone-breach v3
     (or: AI plane unavailable — §5)

  3  What policy allowed
     policy P-14 · no suppression matched · escalation permitted ·
     not in an exclusion window · 4 rules evaluated, 1 fired

  4  What I can check
     [ the 6 suppressed events ] [ replay this decision ]
     [ the bundle that was active ] [ the policy version ] [ the audit entries ]
```

Question 1's second sentence is the one a VMS never offers and the one an
operator values most after their first week: *why did these six not fire*. The
suppression list is what turns a feed from a mystery into a system — and it is
the surface that makes M6's "propose a rule, see the historical impact diff"
credible, because the same list is what the diff is computed over.

---

## 7. Accessibility and the 24/7 operator

Target: **WCAG 2.2 AA**. A product sold on auditability cannot ship a console
that excludes the operator who cannot use a mouse at 3am.

**Conformance is a release gate, not a claim.** Per-milestone: an automated
axe/WCAG pass over every template, plus the manual checks below, plus a
keyboard-only pass of all three critical flows by a person who has not built
them. An automated pass catches roughly a third of real issues and every issue
it catches is free.

| Requirement | Target | Why it is not negotiable |
|---|---|---|
| Text contrast | ≥ 4.5:1 (AA); ≥ 3:1 for large text and non-text | §4.3 |
| Focus visible | 2px `--accent` ring, 2px offset, on **every** focusable element, never `outline: none` | keyboard-only operation |
| Contrast, non-text | ≥ 3:1 for badge borders, glyphs, focus ring, the autonomy dial | a border that vanishes in a dark room is not a border |
| Target size | **≥ 24×24 CSS px** (WCAG 2.2 AA 2.5.8), **≥ 44×44 preferred** for anything above `compact` density | night, tremor, a trackpad in the dark |
| Font size | body 15px floor; table 14px; metadata 13px; **nothing below 12px**, and 12px only for hashes | §1.2 |
| Reflow | usable at 320px width with no two-dimensional scrolling | a monitor in a locked cabinet |
| Keyboard | every function reachable and operable by keyboard alone; no keyboard trap; a visible skip link to `<main>` | a NOC operator's hands are on one input device |
| Focus order | matches visual order; no positive `tabindex`; a re-rendered page returns focus to the flow root | `<details>` expansion must not lose the place |
| Screen reader | every table has a `<caption>` and `<th scope>`; every form control has a real `<label for>`; every state change is `role="status"` or `role="alert"`; `aria-busy` on loading regions; the verdict and the AI-unavailable notice are the first things read | `layout.html`'s `messages` block already does `role="alert"`/`role="status"` correctly |
| Live regions | the disposition result is a `role="status"`; the step-up failure is `role="alert"`; neither is a focus move | a screen-reader user must not lose their place to learn something succeeded |
| Severity and verification | word + glyph + colour, always (§4.3) | ~8% of men, and this population skews to it |
| Motion | `prefers-reduced-motion` honoured; there is no motion in the console to honour — this is a consequence of §4.2, not an achievement | a page that reflows while an operator is reading it is a page that causes errors |
| Zoom | 200% zoom without loss of function or content | 40-year-old monitors and cheap panels |
| Timeout | no session timeout that can interrupt a form mid-entry; sessions expire on 8 hours of inactivity, and the page warns at 7 with a way to extend | a timeout that eats a disposition is a data-loss bug |
| Language | `lang="en"` and `dir="ltr"` on `<html>`, asserted by `TestConsoleIsLeftToRight` | `AGENTS.md` |
| No per-element direction | no `dir` attribute on any element, ever | `AGENTS.md`; no second direction exists to override to |
| Time | every rendered time carries an explicit offset (§8) | see the finding in the summary |

**Keyboard-only, the three flows, as a hard requirement.** Every disposition,
every approval, every step-up, every undo and every activation is completable
with no pointing device, in the order the HTML says, with a visible focus at all
times. The three auto-handled dispositions in §3.3 that are closable must be
closable; a keyboard operator who cannot dismiss a nuisance alarm learns to leave
the console.

**Operator-exception surface, accessibility-specific.** The screen in §3.3 has
twelve items. A keyboard user triaging it uses `j`/`k` and `Tab` and must be able
to reach the urgent item's primary action within three presses from page load,
because the whole point of the surface is that the one thing that matters is the
one thing you can get to.

---

## 8. UX research at two-person scale

The budget is real: §6 of `00` gives 40% of two engineers to everything that is
not new build, and research, support, demos and the AI review pass all draw on
it. The plan below costs roughly 3 engineer-days per month.

### 8.1 Unmoderated tests, 5–8 operators per round, four rounds a year

A remote, unmoderated task session, 25 minutes, screen-recorded with written
consent, run in the customer's own environment on their own console where
possible. Two tasks only, because a third task measures endurance rather than
comprehension:

1. *"This incident says CORROBORATED. It is 09:00 and you are deciding. Tell
   us, out loud, what you need to look at and in what order."*
   Measures: whether the verdict-then-evidence order is the order they want, and
   how long the attribution block takes to dismiss.
2. *"You have just used the product for two weeks. The escalation you sent did
   not arrive. What now?"*
   Measures: whether the unconfirmed-action state (§3.3) is comprehensible, and
   whether the copy's insistence on "treat as NOT escalated" survives contact.

Analysis is by us, on the recordings, in `docs/research/YYYY-MM/`, sanitised. The
output is a change or a decision not to change, written down. A round that
produces a list of "observations" and no decision has been wasted and does not
run again in that form.

### 8.2 Shift shadowing

Twice a quarter per segment, as `` §5.2 specifies. The
UX yield is a timing and a vocabulary; the research yield is the answer to
question 7 in that guide, which is consistently "what had it shown me".

### 8.3 The diary study

Six operators, 14 days, one prompt per day, ninety seconds, in a chat channel
they already use:

> What did the product get wrong today? Not what went missing — what did it
> *say* that was wrong, or show you in a way that misled you?

The yield is small and specific: sentences containing the word "but". Every
instance is a place where the product stated something true and the operator drew
a false conclusion, and that is precisely the class of defect an accuracy metric
cannot see. 14 days is enough for each operator to hit a night shift and a
degraded AI plane; 21 is not worth the extra week.

### 8.4 What we will not do

- **No 200-person surveys.** The population we care about is measured in the
  thousands worldwide; a sample we cannot recruit from the population is a
  sample of the people who answer surveys. A 5–8 operator round from the
  actual population beats a 200-person panel every time, and it takes a week.
- **No A/B tests below detectable effect.** At 3 design-partner sites with 20–60
  incidents a week, an A/B test on disposition phrasing has no detectable effect
  in any horizon we can afford. We use shadowing and observation, and we accept
  the n. Where we *do* have volume — M3 onward, across sites — a real experiment
  on the exception queue's default sort is a legitimate use of the instrument and
  a quarter of the design effort.
- **No design system for its own sake.** There is no component library, no
  Storybook, no design-token pipeline, no visual regression baseline image
  suite. The stylesheet is one hand-written file, and a token change is a
  `git diff`. A design system built at two people is a second product with a
  second roadmap, and the console needs one designed surface, not a platform.
- **No usability test of a screen that has not been built.** A clickable
  prototype in a tool we do not otherwise run, for a screen nobody has written
  HTML for, tests the tool. Every M1 screen is testable because the server
  renders it.
- **No localisation research.** `AGENTS.md` dropped RTL by product decision.
  `sites.default_locale` — or its successor, since migration 0010 removes it —
  is a rendering input, not a design constraint, and re-opening this costs a
  quarter of console work for a segment we have not reached.

---

## 9. Visual identity

### 9.1 Naming

**The product is Captain.** Named in `00` §1. What the name has to do, and the
decisions that followed:

| Decision | Rationale |
|---|---|
| One word, no "AI", no "Security" in the name | `AI` in a product name dates it and, from 2026, reads as a feature rather than the product. The name is the thing, not its mechanism |
| Not a person's surname, not a naval metaphor | A surname is unfindable and a naval metaphor is a costume. This audience is extremely literal about names on a screen |
| "Captain" is a rank, not a person | The operator is the captain. The product advises. A product that calls itself Captain is making a claim about the human's agency, and that claim is the whole of `00` §1 and principle 1.5. It also happens to be the Captain obvious pun, and at 04:00 that is one fewer thing to explain |
| The internal repo keeps `security-platform` as `release.productName` | `internal/release/release.go` sets `productName = "security-platform"`. That string is in signed manifests and in the OpenAPI title. It is **not** changed: renaming it is a contract change requiring `contractcheck` and a baseline write, for a cosmetic gain, and the manifest string is a machine identifier, not a brand. The name in the UI is `Captain`; the name on the wire is `security-platform`; both are correct |
| The console topbar says "Physical Security Intelligence Layer" today | That is the category, not the brand. **M1 change: the topbar brand becomes `Captain` with the category as a subtitle**, and the `<title>` in `layout.html` becomes `Captain — <page>`. Tracked as backlog row #25-adjacent in `05` §2, effort S |

Domain: `captain.security`. Checked for conflicts at the time of writing; a
domain is not registered from this repository and the check is a manual step.

### 9.2 Logo and mark

A single mark: **a shield outline containing a bracketed timestamp** — the
`[` `]` are the same brackets the evidence hashes and event ids are rendered in,
and the shield is the shape security buyers already read without being told. No
second symbol, no gradient, no shield-plus-waveform, no lock.

Construction rules, because a mark has to survive being 16px on a dark table
header and being etched on a rack:

- Single-weight stroke, 1 unit, no fills except the timestamp bracket.
- Grid 24×24 units, 1 unit of clear space on all sides.
- Solid `--text` on dark, solid `#08121c` on `--accent` fill. Two-colour
  maximum, so it works in one ink on a printed incident package.
- Never recoloured by severity. A red logo is a logo for a different product.

### 9.3 One-line statement

> **Captain turns your security stack's noise into a short, evidenced list of things that actually happened — and it can prove every one of them.**

The long form for the site and for a deck:

> Captain is the local-first intelligence and autonomy layer that sits above the
> physical-security stack you already own. It turns raw signals into verified,
> evidenced incidents, then handles the routine work under policy — so your
> people get exceptions, not alerts. Every decision is reproducible from stored
> facts, months later, with the model switched off.

Both are restatements of `00` §1 and `00` §4. Neither says "AI". Neither says
"real-time". Neither says a number.

### 9.4 Colour and mark rationale

The product palette is the console palette, unchanged from `console.css`: a
desaturated blue-grey dark surface (`--bg #101418`) with a single blue accent
(`--accent #4c9be8`) and four semantic colours carrying the four operator states
(settled / look / act / working). Rationale, stated so a future request to "make
it pop" can be refused against a reason:

1. **The surface is not black and the accent is not saturated.** A pure-black
   page in a dark room is a hole; a `#ff0000`-family accent in a 24/7 surface is
   an alarm that has already been spent by 04:00. Desaturation is a fatigue
   decision before it is an aesthetic one.
2. **Four semantic colours, not five severities in five colours.** Severity is
   encoded by glyph and word; colour carries the operator state. A five-colour
   severity scale looks like a traffic light and is unreadable for a
   deuteranope at 04:00 (§4.3).
3. **No brand colour in the semantic set.** If `--accent` were also "medium
   severity", every link in an alert would look like a warning. The accent is
   only ever "you can act on this".
4. **The red is `--err #f85149`, not `#ff0000`.** It reaches 5.6:1 on `--bg` and
   survives a projector and a print. Pure red does neither.

### 9.5 Licensing and provenance — the rule is no scraped imagery

**Every asset in this repository carries a recorded licence and a source. There
are no third-party photographs, icon sets, fonts, illustrations or AI-generated
images in the product.** Not because of taste: because the buyer is a physical
security operator whose entire profession is detecting claims that turn out to be
unsupported, and because the product sells provability. A logo we cannot name the
licence of is the first thing a procurement questionnaire finds.

`docs/assets.toml` — one record per asset, checked by a test:

```toml
[[asset]]
path        = "internal/api/templates/static/console.css"
kind        = "stylesheet"
author      = "in-house"
licence     = "Proprietary — product source"
source      = "in-repo"
attribution = "n/a"
added       = "2026-09-28"
sha256      = "<pinned; the test fails if the file changes without a record>"

[[asset]]
path        = "brand/mark.svg"
kind        = "logo"
author      = "in-house"
licence     = "Proprietary — product source"
source      = "drawn in-repo, by hand, in this repository's history"
attribution = "n/a"
added       = "2026-09-28"
```

`TestEveryAssetHasARecordedLicence` walks the asset directories, and fails if a
file exists that `docs/assets.toml` does not name, if a named file is missing, or
if a `sha256` no longer matches. The rule is symmetric, so an asset cannot be
added quietly and a licence cannot drift from the file it covers.

**Consequently, and specifically:**

- **Icons are hand-drawn inline SVG** in the templates, on the 24-unit grid, in
  `currentColor`. No icon font, no sprite sheet from a CDN, no third-party set.
  A dozen glyphs is an afternoon; a licence file is a legal review.
- **No web fonts.** The stack is the system stack, as `console.css` already is.
  A self-hosted font is a licence, a file, a licence file, and 400KB on a site
  that may be on a metered connection.
- **No photography anywhere, including the marketing site.** A camera image on a
  security product's homepage is either ours (and the subjects are our staff or
  our customers' premises) or scraped (and unlicensed). Ours means a model
  release; we do not have one, so there is no photography. The site uses the
  mark, the palette, the statement, and a screenshot of the real console, which
  is the only image a provable product should lead with.
- **Generated images are prohibited, permanently.** Not "reviewed" — prohibited.
  A synthetic photograph in a product whose moat is that its records are real is
  a contradiction in the artefact itself, and it cannot be defended if asked.
- **Customer marks, trademarks and screenshots** in docs, decks or the site
  require the written permission named in that customer's §3.7 commitment. A
  design-partner logo without one is a breach, not a marketing decision.
- **Third-party code** keeps its own licence and its own notice file, and its
  presence is recorded in the release manifest's SBOM (`internal/release` carries
  an `SBOM` field and `TestSecretMaterialIsRefused` already scans the archive).

### 9.6 Naming pitfalls for an AI product, from the ones we nearly hit

Specific, and each one a real argument we had:

1. **Don't put "AI" in the product name.** It dates the product to a technology
   cycle and, worse, invites the comparison "what does the AI do that rules
   don't" — a frame where our evidence and audit properties look like
   alternatives to a claim rather than as the product.
2. **Don't put the outcome in the name.** "Zero false alarms" in a product name
   is a number we cannot yet defend, and `00` §8 forbids asserting it. A name is
   not a place for a claim you have not measured.
3. **Don't name the product after the mechanism that will change.** If the name
   contains "vision", "VLM" or "model", the first model change is a rename. The
   name must survive `01` M8's model marketplace, where the model is the
   customer's choice.
4. **Don't use a trust word.** "Sentinel", "Guardian", "Aegis", "SentinelOne"
   (already taken, and for exactly this reason) imply a guarantee. We provide
   evidence of what happened; we do not guarantee that it happened. A name that
   implies a guarantee is a name a customer's legal team reads differently than
   our engineers do.
5. **Avoid "copilot".** Copilot implies a suggestion in an editor and a human
   with the keyboard. `01` M3 is recommendations and M7 is an exception queue;
   the metaphor changes the product's apparent ceiling and undersells M7 by two
   years. It is also a term of art in a different market, which is where the SEO
   problem starts.
6. **Avoid the singular-agent voice.** "It thinks", "it decides", "it knows". Our
   central claim is that the deterministic spine decided and the model
   contributed an attributable observation. A name or copy that says "the system
   decided" is fine; a name that says "it decided" is a claim about which
   component acted, and we do not want that ambiguity in the product's own voice.
7. **One word, one spelling, no clever pluralisation.** "Captain", "captain",
   `CAPTAIN`, "Captain AI" and "captain.security" all appearing in the same
   codebase is how a brand becomes a search problem. The string is in
   `layout.html`, the docs and the `<title>` and it is spelled the same way in
   all of them; `TestProductNameIsConsistent` checks the templates.
8. **Check the trademark before the domain, not after.** A single-class
   application in class 9/42, in the two jurisdictions we would sell in first, is
   a few hundred euros and takes weeks. It is a budget line in the M1 commercial
   plan, and it is cheaper than a rename at M7.

---

## 10. Known findings in the current console

Recorded here because a design document that does not list what is wrong with
the implementation is a wish.

| # | Finding | Where | Disposition |
|---|---|---|---|
| 1 | `consoleData.Time` renders `t.UTC().Format("2006-01-02 15:04:05Z")`, so every console time is UTC. `AGENTS.md` requires that a site's timezone is the site's timezone and that every rendered time carries an explicit offset. The function is named `SiteTimezone` and its comment says "SiteTimezone renders a site's time, never the host's" while the body hard-codes UTC. A UTC `Z` *is* an explicit offset, so it is not ambiguous — but it is not the site's clock either, and the incident timeline is the one place an operator compares against a human account | `internal/api/console.go:89-94` | Fix in M1: the site timezone belongs in `consoleData` and the format carries a numeric offset, e.g. `2026-03-01 09:00:12+02:00`, with UTC alongside on hover. Design commitment, not a preference — see §7 |
| 2 | No disposition form in the console. The API endpoint exists and is tested; the operator's only path today is `securityctl incident disposition` | `internal/api/templates/`, backlog #25 | M1, §3.1 |
| 3 | No `/rules` or `/audit` console routes. The rule store, simulate, activate, rollback and the ledger are CLI-only | `internal/api/handlers.go` | M1; they are operator surfaces, not admin surfaces — a site owner must be able to see their own active bundle and their own seals |
| 4 | The incident page shows an evidence list and never a corroboration count, so 4 items from 2 cameras reads as 4 sources | `incident.html` | M1, §6.2 |
| 5 | The dead-letter and clock-drift tables are on the incident feed page, below the incidents. Both are `data.Incidents`-length-20 sections rendered regardless of whether the operator has `dead_letter.read` — the console uses `requirePrincipal`, not `requireCapability` (`handlers.go:158-163`), so a site-scoped operator with no `dead_letter.read` capability is shown the dead-letter list | `incidents.html`, `console.go:186-195` | M1, and it is a **capability leak**, not a layout preference. The console routes must use `requireCapability` like the API routes, or the data must be omitted by capability. A test to match |
| 6 | The degraded badge title is `Raised without a live AI observation` and the row says `degraded: <reasons>`. Correct as far as it goes, but it does not say what the decision was made *without*, and it collides visually with the drift `band-warn` badge which means a different thing | `incidents.html:49` | M1, §5 |
| 7 | `Cache-Control: no-store` on every console page, which is right (a cached page is a stale authorization decision) and means every navigation is a full round trip with no client cache. This is §4.2's cost, accepted | `console.go:109` | accepted |
| 8 | `parseLimit(…, 50, 200)` caps the feed, and the page never says so — an operator with 300 open incidents sees 200 and no indication that 100 exist | `console.go:159` | M1: a "showing 200 of 312" line with a link to the rest |

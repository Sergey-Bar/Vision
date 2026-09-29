# ROADMAP

The plan from today to product, on one page. The full set of documents lives in
[`docs/roadmap/`](docs/roadmap/README.md); this file is the entry point and the
only one you need before you know which of those to open.

Last revised 2026-09-28.

## What we are building

The intelligence and autonomy layer **above** the physical-security stack a
customer already owns. It does not replace the VMS, the panel or the camera. It
replaces the human shift that triages alarms, and it does that while keeping
every decision attributable, replayable and auditable.

```
Signals → Understanding → Verification → Decision → Action → Verification → Audit
```

The product's claim is not that it is clever. It is that it can **prove what it
did** — and that is the thing no incumbent can answer when a security director
asks.

## Where we are

**Milestone 1 of 8. The deterministic spine is built and green.** 30 Go
packages, twelve forward-only migrations, thirteen contracts, a hash-chained
audit ledger, a fixture-driven replay path, a content-addressed evidence store, a
rule engine with no shell, SQL, network, dynamic code or regex, a working
install/upgrade/rollback/backup lifecycle, and a dependency-free Python AI
runtime that holds no model.

[`docs/status.md`](docs/status.md) is the only source of truth for build state.

**Four things stand between us and signing M1**, and all four are external to
the code:

| # | Blocker | Needs |
|---|---|---|
| 1 | All 8 Milestone adapter routes are `Unverified`; the fixtures are synthetic | A real XProtect installation |
| 2 | No model, no GPU, no signed model bundle — and no Go client for the runtime | A model choice and a GPU |
| 3 | No SLO demonstrated under load | A dev install and a load generator |
| 4 | No design-partner run | 14 consecutive days at one site |

## The calendar

| | Milestone | Window | Commercial state |
|---|---|---|---|
| **M1** | Trust Spine — prove one real AI-assisted workflow | → **2027-03-31** | Design partner, technical pilot |
| **M2** | AI Investigator — the platform investigates incidents | 2027-04-01 → 2027-09-30 | Paid pilot |
| **M3** | AI Operator Copilot — it performs the routine operator workflow | 2027-10-01 → **2028-03-31** | **First production GA** |
| **M4** | Universal Security Layer — ONVIF, a second VMS, the Adapter SDK | 2028-04-01 → 2028-12-22 | Platform expansion; OEM talks |
| **M5** | Enterprise Command Platform — multi-site, HA, OIDC/SCIM | 2029-01-04 → 2029-09-28 | **Enterprise GA** |
| **M6** | Site Intelligence — it learns the site | 2029-10-01 → 2030-06-28 | Intelligence premium |
| **M7** | Autonomous Security Operations — it runs the routine | 2030-07-01 → 2031-06-30 | Autonomous operations |
| **M8** | Physical Security OS — others build on us | 2031-07-01 → 2032-12-31 | Platform / marketplace |

Each milestone has a signed exit gate, a commercial state, and a
**reduced-scope cut decided in advance**, because the only legal response to a
slip is less product. Gates and arithmetic:
[`docs/roadmap/master-roadmap.md`](docs/roadmap/master-roadmap.md).

## The North Star

**Human Attention Minutes per 1,000 security events.** It must fall
continuously, and it is never reported without four companions: missed-incident
rate, operator override rate, false escalation rate, and evidence completeness.

Automation alone is not success. **Trusted automation is success**, and those
four numbers are how we prove the difference rather than assert it.

## Who we sell to, in order

Single-site commercial operators → in-house teams at 5–50 sites → VMS OEMs and
integrators → MSSP/SOC operators → regulated verticals. Each rung becomes
reachable only when the previous one has produced a reference the next can
quote. We are not a VMS, not a camera maker, not a generic SIEM, and never
cloud-only. See
[`docs/roadmap/strategy-and-north-star.md`](docs/roadmap/strategy-and-north-star.md).

## How we work

Two engineers and heavy AI assistance. A quarter of new build is **15.6
engineer-weeks**; every plan is sized against that number. AI writes the code
and the first-draft tests. **It does not decide scope, sign an exit gate, talk
to a customer, or commit a date** — that is written into
[`AGENTS.md`](AGENTS.md) and the operating model.

## If you are starting here

| You want to | Read |
|---|---|
| Work on the code | [`internal/README.md`](internal/README.md) — the package map, and where to add what |
| Understand the contracts | [`contracts/README.md`](contracts/README.md) |
| Add a migration | [`migrations/README.md`](migrations/README.md) |
| Deploy or operate it | [`deploy/README.md`](deploy/README.md) |
| Know what is built | [`docs/status.md`](docs/status.md) |
| Plan the work | [`docs/roadmap/`](docs/roadmap/README.md) |
| Know the rules | [`AGENTS.md`](AGENTS.md) |

## Ten code defects the plan review found

Recorded rather than fixed, because several are design decisions and not typos.
Full detail and evidence in
[`master-roadmap.md`](docs/roadmap/master-roadmap.md) §"What this review got
wrong". The two worth fixing first:

- **The AI runtime's bundle check verified a digest, not a signature.** It
  compared `sha256(manifest)` against the *contents* of `manifest.sig` and never
  used the public key, while the docstring said "signature". A bundle "signed"
  with a hash was accepted as if it were signed. **Fixed**: `runtime.py` now
  verifies a real Ed25519 signature over the manifest, in pure-Python RFC 8032
  code with no dependency, because the air-gap supply-chain decision forbids one.
- **The adapter's known-event-type table was keyed on spellings normalisation
  never emits.** Twelve keys were unreachable and five produced types were
  missing, so real motion, analytics and access events were marked malformed.
  **Fixed**: the table is now derived from the normalizer's own output and
  `TestEveryTypeTheAdapterProducesIsKnown` asserts both directions.

Also open, and unchanged by the runnable core work: `slo.go`'s `Breaches` still
has no caller. The metrics are emitted and the SLO thresholds are not yet
evaluated in-process, so gate 3 is closer but not closed.

The other two items this section used to list are now done. The known-event-type
table is derived from the normalizer's own output and both directions are
asserted by a test. `internal/observability` has a production caller — the
pipeline's gauge loop and the `/v1/metrics` route — and `cmd/security-core`
exists and runs the pipeline.

The 2026-09-29 sweep closed the M1-code items this file had no entry for. The
full list, with the red-check evidence for each control, is in
[`docs/status.md`](docs/status.md); the two with a date attached are the
2027-01-01 partition boundary, which migration 0015 removes permanently, and the
verifiers that accepted self-signed forgeries, which now refuse.

# Proposal 09 — Evaluated and deliberately not adopted

**Status:** record of a decision not to act. No code, no dependency.

`AGENTS.md`: "A capability has to be justified as a platform primitive, not
added ad hoc." This file is the other half of that: a list of plausible,
well-regarded, genuinely free projects that were examined and rejected, with the
reason each one would have made the product **worse**.

The value of this file is that it stops the same evaluation being repeated, and
it names the traps, because every entry below is a case where the obvious
comparison is the wrong one.

## 1. `riverqueue/river` — job queue for Go and Postgres

**Apache/MPL-2.0, 5,576 stars.** The strongest library in its category. Its
documentation is emphatic that transactional enqueueing eliminates whole
classes of distributed-systems problems, and it is right.

`internal/jobs` and `internal/outbox` already implement this, and
`internal/README.md` records the reasoning in the dependency rules: store
packages do not import each other, and acceptance is a database commit. `outbox`
takes the caller's `pgx.Tx` and never opens its own.

**Rejected:** replacing a working, invariant-protected implementation with a
library, on the strength of a README, would put a durability property that is
currently *tested against `kill -9`* into the hands of a dependency. The
existing code is the evidence. This is the single most tempting entry in the
list, which is why it is first.

## 2. `pgmq/pgmq` — SQS on Postgres

**Apache-2.0.** Exactly-once delivery within a visibility timeout, FIFO with
message groups, archive instead of delete.

**Rejected:** installing it as a Postgres **extension** collides directly with
`AGENTS.md` — applied migrations are immutable and forward-only, and this
product's whole deployment story is an offline archive the operator can reason
about. A managed Postgres extension is outside the release archive, outside the
manifest, and outside `securityctl install`'s hash verification. It would make
an install that currently proves itself byte-for-byte depend on a component the
release cannot account for.

It also offers SQL-only installation, so this rejection is about the *operational
boundary*, not only the extension. It is a reasonable choice for a product
without this deployment model, and it is wrong for this one.

## 3. `theupdateframework/go-tuf` and `sigstore/cosign` — signed update frameworks

**Apache-2.0 both.** Mature, correct, and designed for exactly the problem
"make an untrusted update verifiable".

**Rejected, and the cosign finding is the interesting part.** Per
`sigstore/cosign` issue #4454, the `--offline` flag is **deprecated and
non-functional** in Cosign v3, and still reaches for the CDN to fetch
`trusted_root.json`. A maintainer states plainly that the flag "is not well
documented and doesn't do airgapped verification". Genuine airgapped
verification requires supplying a trust-root **file** and having some way to
keep it fresh.

This product installs at sites with no network. Trusting a tool whose offline
mode reaches for a CDN is worse than the existing `release.Verify`, which is
implemented, tested, exercised through a full build-sign-install-upgrade-
rollback cycle, and refuses a wrong key at every step. TUF would additionally
add a trust root with an expiry that must be maintained out of band — precisely
the recurring-maintenance cost `status.md` describes the product as avoiding.

**Kept:** the *idea*. If the release ever needs multiple signing authorities or
quarantine windows, that is a design decision for a human, not an import.

## 4. `xraph/chronicle`, `opskernel-io/go-merkle-audit-chain`, `GoLogX` — audit chains

**MIT / various.** All three implement hash-chained, optionally signed,
append-only logs. All three are competent, and one of them (`go-merkle-audit-
chain`) is explicitly framed against EU AI Act Article 12.

**Rejected — and this is the subtlest entry in the list.** Each of these states
the same limitation in its own documentation: a hash chain proves *internal
consistency*, and a party with full write access to the storage **can rewrite
the entire chain** and `Verify` will return clean. Their answer is Merkle
anchoring to an external timestamp authority.

This product's audit property is different and stronger in the way that
matters: `internal/audit` is trigger-computed in the database, and
`internal/checkpoint` seals the chain to a **sink outside the database**. The
threat being defended is a privileged insider with DB access, and the external
sink is what defeats a wholesale rewrite. Swapping that for a hash chain without
an external anchor would be a **downgrade wearing an upgrade's clothes** — the
kind of change that looks like hardening on a diff and is not.

The counter-measure those libraries are missing is the one this repository
already has. Read them before redesigning the ledger, not instead of it.

## 5. `google/cel-go` — safe expression evaluation

**Apache-2.0.** Non-Turing-complete, deterministic, side-effect free,
protobuf-aware, with static type checking. It is an excellent language and it
matches the constraints `internal/rules` has already adopted.

**Rejected at M1.** `internal/rules` has **zero external dependencies**, by
design, and `internal/README.md` gives the reason: an engine that can query the
database produces decisions that depend on data the explanation does not carry,
and replay stops being reproducible. A CEL `Program` would be compiled against
its own environment; `rules.Decision` and the `explanation` structure behind
`incidents.Explanation` are tied to the current internal model. Integrating it
changes the explanation format — which is operator-facing, audited output — and
the replay inputs, for a gain in ergonomics that this product does not need.

**Keep:** the vocabulary. If the rule language ever outgrows hand-written
compilation, CEL is the right successor and this file says so.

## 6. `go2rtc`, `mediamtx`, `scrypted` — RTSP/ONVIF streaming

**MIT / Apache-2.0.** Excellent, widely deployed, Go-native.

**Rejected as a VMS source.** All three assume a continuous media stream —
RTSP, ONVIF, or a URL. XProtect does not expose that; it exposes the ImageServer
protocol with a separate token, and a two-token authentication model. Every
architecture in those projects assumes a source this product does not have.

**Kept:** two real use cases. Console playback, where WebRTC is a better
operator experience than the Smart Client plugin. And a local test rig — a
known-good stream to replay against — which is exactly the "Milestone server
reachable" problem that `AGENTS.md` requires every test to avoid depending on.

## 7. Agent runtimes (`NVIDIA/OpenShell`, `cloudflare/security-audit-skill`, `alibaba/open-code-review`)

Surfaced through TrendShift's weekly and monthly windows, and worth recording
because the reasoning is not obvious.

**`NVIDIA/OpenShell`** is architecturally very close to this repository's threat
model: declarative YAML policy, Landlock, seccomp, network namespace, fail-closed
by default, OCSF events, credentials injected at runtime rather than written to
disk. Every one of those is a control this repository states in prose.

**Rejected** because it sandboxes *coding agents* — Claude Code, Codex,
Copilot. This product does not run coding agents. Its AI plane is a
dependency-free inference runtime reached over HTTP, and invariant 3 keeps it
off the critical path precisely so a hung inference cannot hang ingest. A
sandbox for a process that is not running is not a control.

**The transferable idea:** the policy-as-version-controlled-YAML pattern, with
every control's default documented alongside the risk of changing it. That is
already `docs/threat-model.md`'s shape, which is a point in the repository's
favour.

**`cloudflare/security-audit-skill`** (machine-readable, independently verified
findings) and **`alibaba/open-code-review`** (deterministic pipelines plus rules
for injection classes) are developer tooling. Not product dependencies.

## 8. `pgguru/pg_kaboom` — do not install

Included as a warning, not a proposal. It is a SQL extension exposing callable
functions including `rm -Rf $PGDATA`, `segfault`, and "kill the postmaster",
guarded only by a session GUC that a user can set. It is a legitimate teaching
tool in a disposable cluster and a catastrophe everywhere else. **Recorded so
that a future search does not rediscover it and mistake it for a chaos-testing
option.**

## What is not on this list

Projects that were not evaluated for quality and found wanting — `DeepCamera`,
`O-VAD`, several small NVR forks. They are not rejected on merit. They were
excluded by the licence and dependency constraint in proposal 08: a research
repository with `thirdparty/`, checkpoints and a torch pin cannot be imported
into a process that is standard-library-only by design and by test.

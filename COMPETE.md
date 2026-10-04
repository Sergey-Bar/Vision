# COMPETE.md

**A physical-security intelligence layer.** One vendor-agnostic pipeline that turns
a camera's bytes into a durable, hash-chained, replayable finding — and refuses to
guess when the evidence is not there.

> **Status, stated first because everything below depends on it.** Nine of the
> thirteen acceptance criteria pass in this build. Four are reported `NOT RUN` and
> `NOT RUN` is a distinct verdict from `pass`: criteria 06, 07, 08 and 10 need a
> live camera, a second site, or a model key, and this build has none of those.
> Nothing in this document claims them. Run `make demo` and read
> `dist/demo/transcript.md` to see the same nine criteria printed by the product's
> own binary.

---

## Run it

```sh
docker compose up
```

Console at **<https://localhost:8443/>**. The certificate is self-signed and the
seed prints its SHA-256 fingerprint, so you can check what your browser warned you
about. Sign in as `admin` with the passphrase and the six-digit code the seed
printed; `securityctl auth code --secret <the one it printed>` computes the current
code if you have no authenticator app.

```sh
make demo          # the whole thing end to end, and a transcript of it
make verify-container   # the gate, in a container, on Linux
```

---

## What it does

A camera produces H.264. This product turns that into **an incident an operator can
act on and an auditor can re-derive six months later**, and it is equally careful
about what it cannot say.

Four claims, and what backs each one.

### 1. The decision is derived, not stored.

An incident's explanation is **recomputed** from the stored facts and the stored AI
observation. Nothing in the record asserts an outcome; everything in it is
something the pipeline can re-read. A replayed fixture produces the same incident
and asks nothing of anyone — criterion 04, measured, in the transcript.

This is the property the whole storage design exists for. It means a bug in the
rule engine is recoverable, because there is no stored answer to be wrong; it means
an auditor does not have to trust a verdict, because they can ask what it was
derived from.

### 2. A missing AI observation is never a negative one.

Three absences stay distinguishable, and the product refuses to collapse them:

| The system says | What it means | What it does **not** mean |
|---|---|---|
| `degraded` | the model was asked and did not answer | that the answer would have been no |
| `absent` | there is no model configured | that nothing was there |
| `insufficient_evidence` | the model answered "I cannot tell" | that it answered "no" |

Every rule that consumes a Tier C signal declares `if_ai_unavailable`, and every
degraded observation carries a **reason**, because a degradation with no reason is a
row that reads as a negative answer. That control is in the shipped red-check table
and has been watched go red.

The seeded demo site makes this visible rather than hiding it: **one** observation is
healthy, seven are explicitly degraded with named reasons, and one reviewer
disagreed with the model in writing. A site where everything is healthy looks
better and proves nothing about the invariant that matters.

### 3. Losing the vendor does not lose the evidence.

Nothing outside `internal/milestone` names a vendor — enforced by three tests that
fail on a direct import, on a vendor name in the code, and on a vendor in the
module graph. The milestone routes are in a **route table**, and every one of them
carries an `Unverified` flag that no code path can clear except a probe against a
real device. Criterion 11, measured.

The scripted vendor endpoint in the demo serves that table and **says on startup
that it cannot promote a route**, because a server on a socket is exactly the kind
of thing a reader might assume did its own homework.

### 4. The gate is the product.

25 checks. Not a subset, and not a different list for a different platform: the
container runs *the same* `scripts/verify.ps1`, under PowerShell on Debian, against
your working tree.

Two things that set it apart:

- **A control with no such run is not a control.** Every claim in this repository
  has a transcript of the control being *removed* and the suite being watched to go
  red. `scripts/force-red.ps1` applies one mutation per gate, requires it to fail,
  restores the file, and requires it to pass again — and a non-zero exit is not
  enough, it has to produce a record naming that gate. Nine of the twenty-five gates
  are now recorded red by hand; the other sixteen carry a sentence in
  `docs/status.md` saying what was tried and what the failure was.
- **The gate found five defects nobody had seen**, the first time it ran on Linux.
  Including a Linux-only test that had been failing since it was written, because
  it was skipped on the only platform the product installs on.

---

## The invariant that shapes everything

**Zero accepted event loss.** Anything reaching `inbox_events` reaches a terminal
state unless explicitly dead-lettered. `kill -9` at any stage: no loss, no duplicate
side effect.

This is why the inbox is a table rather than a queue, why leases carry a recovery
window, and why the dead-letter reason lives in its own column rather than in the
last transient failure. It is the one property that has to hold when everything else
is broken.

---

## What this build cannot do, and says so

| Criterion | Why `NOT RUN` |
|---|---|
| 06 — a real event produces a clip containing the cause | needs a live camera with real motion in it |
| 07 — false-positive rate over a soak | needs a camera, a site, and an operator to adjudicate |
| 08 — a second site needs no new code | needs a second VMS with a different API |
| 10 — a vision model costs $0.15/event | needs a key and a real event rate |

Four `NOT RUN` rows reported honestly are a stronger artefact than thirteen rows
reported as passes, because a pass is a claim somebody will check and a `NOT RUN` is
a claim nobody can mistake for anything else.

The other limits, stated the same way:

- **No camera, no VMS, no model behind any row.** Every AI observation is labelled
  or degraded. The RTSP server is a **camera substitute** and says so on startup.
- **The console certificate is self-signed**, minted by the seed. A real install
  presents a real certificate; nothing else about the product changes.
- **Model-dependent criteria are checked against a stub provider.** A pass there is a
  pass for the plumbing and says nothing about a model's accuracy.
- **The POC's soak harness is Windows-shaped.** It builds and runs on both platforms
  now, but its resource sampling was Windows-first.

---

## Where to look

| If you want to check | Read |
|---|---|
| what is actually built | [`docs/status.md`](docs/status.md) — the only source of truth, and it is derived from the tree |
| what the gate found, and what it could not | [`docs/status.md`](docs/status.md), "The 2026-10-04 pass…" |
| the threats, and what is not designed | [`docs/threat-model.md`](docs/threat-model.md) §4 |
| the five invariants and why | [`AGENTS.md`](AGENTS.md) |
| one screen | [`docs/one-pager.md`](docs/one-pager.md) |

---

## The short version

A security product that will be judged on a claim it cannot support is already in
trouble. This one reports four criteria as `NOT RUN`, prints the fingerprint of its
own certificate, labels every AI observation it did not really get, and ships a
script that forces its own gates red. **The evidence is the product.**
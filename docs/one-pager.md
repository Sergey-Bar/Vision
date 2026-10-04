# Physical Security Intelligence Layer — one page

**Turns a camera's bytes into a durable, hash-chained, replayable finding, and
refuses to guess when the evidence is not there.**

```sh
docker compose up        # console on https://localhost:8443
make demo                # end to end, and dist/demo/transcript.md
make verify-container    # 25 checks, in a container, on Linux
```

**Status: 9 of 13 acceptance criteria pass. 4 are `NOT RUN`** and need a live
camera, a second site, or a model key. `NOT RUN` is a distinct verdict from `pass`
on purpose.

---

## The problem

Every vendor has its own API, its own event shape, its own idea of what a
"detection" is. Every customer's compliance evidence is a pile of screenshots and
exports that cannot be re-derived. And the interesting failure — *the model was not
there* — is routinely recorded as *the model said no*.

---

## The product

One pipeline. Canonical events → graph → correlate → deterministic rules → incident,
with a vendor adapter at the boundary and a human reviewer at the end.

Three things it does that most do not:

**1. The explanation is recomputed, never stored.**
An incident's reasoning is derived from the stored facts and the stored AI
observation. Nothing in the record asserts an outcome, so a bug in the rule engine
is recoverable and an auditor does not have to trust a verdict.

**2. A missing AI observation is never a negative one.**
`degraded`, `absent` and `insufficient_evidence` are three different facts and the
product refuses to collapse them. Every rule consuming a model signal declares
`if_ai_unavailable`; every degradation carries a **reason**. The demo site makes it
visible: 1 healthy observation, 7 explicitly degraded, 1 reviewer who disagreed with
the model in writing.

**3. Losing the vendor does not lose the evidence.**
No package outside `internal/milestone` may name a vendor — three tests enforce it.
Every vendor route carries an `Unverified` flag that **no code path can clear except
a probe against a real device**, so the demo's scripted endpoint cannot promote its
own homework.

---

## What backs the claims

- **25 gates**, one script, the same one on Windows and in a Linux container.
- **Every control has been watched failing.** `scripts/force-red.ps1` removes each
  one, requires the suite to go red, restores it, and requires it to go green again.
  A non-zero exit is not accepted as proof — it has to produce a record naming that
  gate.
- **Running the gate on Linux for the first time found five defects** nobody had
  seen, including a Linux-only test that had been failing since it was written
  because it was skipped on the only platform the product installs on.
- **The demo writes its own transcript** at `dist/demo/transcript.md`. Every number
  in it was printed by a command shown above it; nothing was written by hand.

---

## The invariant everything else is shaped by

**Zero accepted event loss.** Anything reaching `inbox_events` reaches a terminal
state unless explicitly dead-lettered. `kill -9` at any stage: no loss, no duplicate
side effect. The inbox is a table, leases carry a recovery window, and the
dead-letter reason lives in its own column rather than in the last transient
failure.

---

## Honest limits

No camera, no VMS and no model behind any row. The RTSP server is a camera
**substitute** and prints that it is one. The console certificate is self-signed and
the seed prints its fingerprint. Model-dependent criteria are checked against a stub
provider, so a pass there is a pass for the plumbing and nothing else. Criteria 06,
07, 08 and 10 are `NOT RUN`.

Full detail: [`COMPETE.md`](../COMPETE.md) · [`docs/status.md`](status.md) ·
[`docs/threat-model.md`](threat-model.md) §4 · [`AGENTS.md`](../../AGENTS.md)
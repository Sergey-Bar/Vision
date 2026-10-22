# VISION — a VMS that proves — one page

**Records the cameras it is given, reads the VMSes a customer already runs as a
first-class source, turns raw signals into verified, evidenced incidents, and
then handles the routine work under policy so that humans receive exceptions
instead of alerts.**

**Every frame, every decision and every exported evidence item is verifiable
offline. Recording continues when the cloud, the network and the model are
gone. The system states what it does *not* know.**

```sh
docker compose up        # console on https://localhost:8443
make demo                # end to end, and dist/demo/transcript.md
make verify-container    # the gate, in a container, on Linux
```

**Status: 10 of 13 acceptance criteria pass. 3 are `NOT RUN`** and need a live
camera, a soak, or a second site. `NOT RUN` is a distinct verdict from `pass`
on purpose.

---

## The problem

Every vendor has its own API, its own event shape, its own idea of what a
"detection" is. Every customer's compliance evidence is a pile of screenshots and
exports that cannot be re-derived. And the interesting failure — *the model was not
there* — is routinely recorded as *the model said no*.

---

## The product

**The Chief of Security.** One screen, one calm canvas. The Chief tells you
what changed while you were away, what needs you now, and what the system does
not know — and it is the same answer for every operator, because it is the
product's answer and not the console's.

Behind it, one pipeline. Canonical events → graph → correlate → deterministic
rules → incident, with a device adapter at the boundary and a human reviewer at
the end.

Four things it does that most do not:

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

**3. A capability that was not measured is not a capability.**
A camera names the profiles it supports and is routinely wrong about it. The
registry records what a *probe* established, and only the `verified` entries
count — a `contradicted` one is a finding about the camera, and a camera nobody
probed says so by name rather than rendering an empty capability list.

**4. Losing the vendor does not lose the evidence.**
No package outside `internal/milestone` may name a vendor — three tests enforce it.
Every vendor route carries an `Unverified` flag that **no code path can clear except
a probe against a real device**, so the demo's scripted endpoint cannot promote its
own homework.

---

## What backs the claims

- **43 gates**, one script, the same one on Windows and in a Linux container.
- **57 red-check controls**, each of which has been **watched failing**. A
  non-zero exit is not accepted as proof — it has to produce a record naming
  that control.
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
07 and 08 are `NOT RUN`.

Full detail: [`COMPETE.md`](../COMPETE.md) · [`docs/status.md`](status.md) ·
[`docs/threat-model.md`](threat-model.md) §4 · [`AGENTS.md`](../../AGENTS.md)

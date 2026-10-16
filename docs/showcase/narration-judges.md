# Track J — five minutes, for a judge

This is the five-minute track from `poc/active-monitoring/docs/JUDGE.md`, run on the
showcase hub instead of a terminal. Same claims, same honesty labels; the difference is
that every number on a screen is read from a file the moment the page loads, so nothing
here is a slide.

Run `scripts/showcase.ps1` first. It prints what this machine has actually produced
before the browser opens — gate record, red-check transcript, demo incidents, live-demo
report — and each line is either a dated file or `NOT RUN`.

Total: **5:00**. Times are budgets, not promises. If a command is slow, say what it is
doing and keep going; the point is the record, not the speed.

---

## 0:00 — The hub, and the one sentence (30 s)

Open `http://localhost:8080`. That is the hub. Do not narrate the whole page; read the
one line and point at the badges under it.

> "A security camera asks a vision model whether an alert is real, refuses to push the
> model's answer without the model naming itself, keeps a signed record of what it
> pushed, and proves that record catches an edit."

Then the three chips at the top — gate, red-check, incidents. Each was read from this
machine when the page loaded. If one says `NOT RUN`, say so and why; that is the product
working, not the product failing.

---

## 0:30 — The live surface: `/live` (60 s)

Click **Live**. Two things on one page, and it labels which is which: the POC's own
live gate — a terminal run whose **report** and **transcript** open from this page —
and the lab-grade analytics surface below it, which is not on the gate path.

Read the run's report out loud: a real public camera resolved to a live stream, and
**11 preflight checks** passed against it (1920x1080, 30.000 fps, decode 6.42x real
time). The watched window produced **no detections**, so the checks that need one read
**NOT RUN** — and `prove` **refused**: "a proof of nothing is not a proof."

Then the arithmetic: the CPU sidecar's measured loopback p99 is **471.5 ms recorded,
809.3 ms re-measured on this box** — at `ai_fps=5` a frame arrives every 200 ms, so it
does not fit the declared rate, and the gate refuses rather than pretend. The
in-process path measures p99 **16.4 ms** and fits the rate — and still refuses the
pinned export by name: that export is a multi-scale anchored head, and the in-process
decoder reads only fused outputs.

**Do not claim:** that accuracy has been measured on this footage. It has not. The
committed material is synthetic and the census records the real-footage rate as
`NOT RUN`.

---

## 1:30 — How a decision is made: `/pipeline` (90 s)

Click **Pipeline**. This page replays **one incident from this machine's own files** —
`dist/demo/…incident.json` and its observation — not a mock-up.

Walk the journey once, in these words:

> "A frame arrives. The detector proposes a box. The decision is taken from stored
> facts. The verdict is stored with the model's own name. The event is chained into the
> ledger, and the ledger is what you will attack in ninety seconds."

Then open the honesty fields on the incident, and read them out:

- `reasonCode: ai_plane_absent` — the AI plane was absent on the run that wrote this
  file;
- `decidedWithoutAiObservation: true`, `observationDegraded: true`,
  `insufficientEvidence: true`, model `"none"`.

> "This incident is here *because* the run could not reach the model. A missing answer
> is never a negative one: it is recorded as degraded, with the reason, and the census
> keeps the row."

**Do not claim:** that the demo events went to a customer's Milestone. They went to
`xpsim`, the local simulator; the page says so.

---

## 3:00 — Proof you can poke: `/trust` (90 s)

Click **Trust**. Two things are on this page before you touch anything:

- the **verification record** — the newest `dist/verification/verify-*.json`, with its
  commit, gate count and the note if the tree was dirty when it ran;
- the **red-check** — a control is a control only if it was watched failing. The page
  counts the controls from `scripts/verify.ps1` and reads the POC transcript.

Then press the buttons — they run the POC's own commands, on this machine, no arguments
from the browser:

1. **Recompute the ledger chain** — `pocctl audit ledger`. It re-walks every row and
   prints `PASS — n row(s) … recomputed and chained`.
2. **Tamper one row, then restore it** — `pocctl audit ledger --tamper`. One byte of
   one row moves (`0.700000` → `0.700001`), the chain fails **at the row number**, the
   byte and the append-only triggers are restored, and the third pass verifies again.
   It refuses to run against a chain that is already broken: breaking a second one
   hides the first.
3. **Replay the newest decision — zero provider calls** — `pocctl prove`. The decision
   is recomputed from the stored row: same box, same score, and the model is **not**
   called again.

Watch the output panel, not the presenter: the command line is printed above its own
output, so what you read is what ran.

**If a button says `NOT RUN`:** the data root on this machine has no run to act on. The
page lists every data root it found and the state of each ledger, and says which one it
chose and why. An empty chain is the absence of evidence, and it is displayed as that.

---

## 4:30 — The census, and the exit (30 s)

Click **Census**. One row per capability, each carrying the command that proves it or
the file it was measured into: `Live`, `Recorded`, `NOT RUN`, `Lab`. Press `NOT RUN`
first — that filter is the fastest way to see where this repository knows more than it
has measured.

Closing line, and then stop talking:

> "Nothing on these pages is a claim without a file behind it, including the rows it
> grades `NOT RUN`. That is the whole product: the system that says what it does not
> know."

---

## What NOT to claim, in any sentence

| do not say | because |
|---|---|
| "it detects at X% accuracy" | accuracy on real footage is `NOT RUN`; committed material is synthetic |
| "CI is green" | there is no CI in this repository; the gate is a local run, named by its record |
| "it works with XProtect" | the schema and read-back are exercised against a simulator; nothing was pushed to a real server |
| "the ledger cannot be edited" | it can; the claim is that an edit is detected, located by row, and survives a restart |
| "we solve false positives" | the false-positive rate is `NOT RUN` until a person marks detections |

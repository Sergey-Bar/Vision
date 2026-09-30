# `docs/hackathon/demo-script.md` — the three minutes, and what people will ask

**For the person running the demo.** Everything below is a line you can read out
loud. The "expected questions" section is the part that matters: those are the
questions a non-technical person actually asks, with the answers that do not
overstate.

---

## Before you start

Three things, and you say all three out loud at the beginning:

1. "This is a proof of concept. It proves one thing: that a detection becomes a
   Milestone API call with a verified clip and no lost events."
2. "The push target is a **simulator on this laptop**. Nothing reached Milestone."
3. "The video is a **synthetic clip** — a rectangle moving. The system is not
   being asked to recognise anything; it is being asked to be reliable."

If you only say one of them, say the second. It is the one that stops a
misunderstanding that is expensive to undo later.

---

## Minute 0–1 — the gate

```powershell
.\scripts\verify.ps1
```

**Say:** "This is the gate. It builds, vets, formats, checks the module boundary,
runs every test with the race detector, runs the sidecar's Python tests, and
then — this is the part most projects do not have — it removes three checks from
the code and proves the test suite goes red each time."

**Say, if asked what that is for:** "A green test suite means nothing unless
some test can fail. We take the control away and watch it break. A suite that
stays green when its subject is gone is not a test."

**If it prints SKIPPED:** say so. "Two steps were skipped, and here is which and
why. A skip is not a pass."

---

## Minute 1–2 — the fault rehearsal

```powershell
.\bin\pocctl.exe rehearse --data-root C:\poc-demo
```

**Say:** "Before we show a number, we break the thing on purpose. A 401, a 403, a
400, a 500, a connection reset, a timeout, and a response that arrives in half.
Each one has an expected outcome, and the run is told which outcome it got."

**Say, if asked "why does this matter?":** "Because a week-long run that has
never seen a failure is a week of evidence about nothing. This table is the
evidence that the failure paths do what the report says they do."

---

## Minute 2–4 — the run

```powershell
.\bin\pocctl.exe demo
```

The screen comes up at `http://127.0.0.1:8097`. The run is `mode=demo-replay`,
and that is stamped in the header, in the log, and in the report.

**Say, pointing at the header:** "Read that line. It says the push target is a
simulator and that no XProtect server was reachable. That sentence is on every
page, every run. It is not a disclaimer we remember to add; it is generated from
the run's own configuration, so it cannot be forgotten."

**When a detection appears:** "That is a line crossing. Three things happened in
this order, and the order is the design: the detection was written to the
database and committed **first**; then the clip was cut and read back and its
bytes hashed; then the event was pushed, and a second call confirmed it is
really there."

**If asked "what if the machine crashed right then?":** "There is a test for
exactly that. A child process commits a detection, is killed with no chance to
clean up, and the parent reopens the database and finds the detection in
`accepted` with its push still pending. Nothing lost, nothing already-sent."

---

## Minute 4–5 — the report

```powershell
.\bin\pocctl.exe report --data-root C:\poc-demo
```

**Say:** "Every number in this report was read from a row. There is no second
source of numbers anywhere in the system. And here — at the bottom — is the list
of what it does *not* prove. It is generated too, not written by hand, so it
cannot drift away from what the run actually did."

---

## The expected questions, with answers that do not overstate

### "Does it detect birds?"

No. The clip is a rectangle. It is labelled `synthetic` in the file's metadata,
on the screen, in the log and next to every number in the report. What the
system is being tested on is **reliability**: does a detection become a stored,
verified clip and a delivered API call without being lost along the way.

### "How accurate is it?"

We don't know, and the report says we don't know. There is no ground truth for
this material, so there is a detection rate and no accuracy claim. The
false-positive row says `NOT RUN`, because no person has looked at the events
and marked which were false. When someone does, the number appears with their
name and the date on it.

### "Did the event reach Milestone?"

No. The push target was a simulator on this laptop. What *is* proven is that the
`POST` is shaped the way Milestone's own published specification says, checked
by a test that reads the specification we downloaded from their documentation,
and that a read-back call found the event on the server.

**If asked "so when would we know it works with Milestone?":** "When someone
points it at a real VMS. That needs a licensed XProtect server, an Analytics
Event Type registered on it, and a user with permission to push. None of that has
been reachable from this machine. The report prints a warning when a run is
configured for a live VMS precisely because that has never happened."

### "Seven days without stopping — how do we know it will hold?"

We don't, yet. That is what the seven-day run is for. The acceptance criteria
are written down with their exact arithmetic, so the answer will be a number and
not an impression. One of them — "the modelled faults behaved" — is `NOT RUN`
until a fault is actually injected on that run, and it is never automatically
green.

### "What happens when the disk fills?"

The run **stops** and says why. It deletes the oldest rolling recording to stay
under the ceiling, and it never deletes a stored clip to make room. If the
stored clips are what filled the disk, the run ends with
`disk_budget_exceeded` and a line that says the space went to evidence.

**Say, if asked "wouldn't it be better to just delete the clips?":** "It would
be better right up until the one clip you need is the one that got deleted. A
reliability measurement that throws away its own evidence to keep a disk happy is
not a measurement."

### "Is this a plug-in? Does it replace Milestone?"

No. The existing platform code has no "trigger event" path at all — it reads
events and places bookmarks. What a MIP Analytics plug-in does is push analytics
events, and that is what this proves can be built. This is the add-on, not a
replacement. Its own `internal/milestone` package has three specific
disagreements with Milestone's published API, recorded in
`contracts/xprotect/README.md`.

### "How is this different from the platform you already have?"

It is not a subset of it. The POC shares **no code** with the platform — the
module boundary is enforced by a test that fails if the import appears. It
borrows the platform's *ideas* (record before effect, a missing observation is
not a negative one) and none of its implementation.

---

## The three sentences to end on

1. "It detects a line crossing, stores a clip whose bytes it has verified, and
   puts an event on the Milestone API that a second call confirms is there."
2. "Every number came from a row, and every screen says what it does not prove."
3. "A week of uptime is the next measurement, and it has not been taken yet."

Do not add a fourth sentence. There is no result yet that would make one true.

---

## Commands that were broken until a QA pass found them

These are worth knowing about because a person following this script would have
hit them, and the failure would have looked like something else entirely.

| Command | What it did | Now |
|---|---|---|
| `pocctl zone --pick <clip>` | rendered the grid still as MJPEG, which ffmpeg 8 refuses to encode at all. The tool never worked. | writes a PNG; reads a lossless grid, which is what you want anyway |
| `pocctl zone --pick <clip>` (again) | **overwrote** the hand-drawn `zones.json` with its proposal | refuses; `--force` moves the old one to `.replaced` first |
| `pocctl soak status` / `soak report` | every invocation failed with `"status" is not a flag` | parses its own arguments |
| `pocctl soak start` | forwarded `--data-root` to a subcommand that does not accept it, so it could never run | each step gets only the flags it accepts |
| `pocctl soak start` | could never go green: preflight demanded a green rehearsal and the rehearsal ran after it | the rehearsal runs first and preflight reads its verdict |
| `watch --config` | always failed; the secret check that should have caught a leak passed **vacuously** | works, and redacts both secrets with a visible marker |
| `pocctl rehearse` | if something else held the simulator's port, it silently tested *that* process and gate D went red for no reason | refuses to start on an address in use |

If you are asked "how do you know these work", the answer is that each of them
has a test in `cmd/pocctl/commands_test.go` or `internal/soak/preflight_test.go`
that fails when the bug comes back, and `pocctl soak start` runs the whole gate
in one command: rehearsal, then preflight, then it tells you what to run.
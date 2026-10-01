# The five-minute verification script

A judge does not read this repository. They watch a screen for five minutes and then
ask whether any of it is real.

This document is the five minutes, in order, with the answer to each question that
follows it.

**Every minute has a `live-vms` variant**, printed as a second block under the first.
The simulator is the default because it is reproducible and needs no credentials; the
live variant is what you show when the judge has asked whether any of it is real
against a real system. Read both. The differences are the point: they are the only
places where a claim gets stronger, and each one names what became weaker in
exchange.

---

## 0:00 — "Show me something working."

```powershell
pocctl demo
```

A real pipeline against a labelled recording, against a local simulator, for three
minutes. Then it reads back what landed, over HTTP, through the same read-back the
client uses.

The banner on every screen and every page says `demo-replay` and
`no Milestone server was reached`. That is not an apology. It is the first claim.

> **Q: did the events actually go anywhere?**
> A: No. `xpsim` is a program in this repository that answers HTTP. Nothing
> reached Milestone. The screen and the report say so on every page.

<details>
<summary><b>live-vms variant</b> — the same minute against a real instance</summary>

```powershell
# docs/demo-live.md is the prerequisite. Read it before this, not after.
pocctl demo --live --rtsp          # a real camera on rtsp
pocctl demo --live                 # a recording, but a REAL push
```

What changes, and it is one line: the banner stops saying `no Milestone server was
reached`. The read-back at the end of the run is then a real HTTP query against a
real instance, and it is the same code that just answered `xpsim`.

What does **not** change, and is worth saying out loud:

- `demo --live` against a recording proves the **push** is real and says nothing about
  the **source**. Use `--rtsp` for the source.
- AC7 is `NOT RUN` on every live run. `soak.Rehearser.IsSimulator()`
  (`internal/soak/rehearse.go:58`) returns `nil, nil` against a real VMS, so the
  fault rehearsal has nothing to inject into. Run `pocctl rehearse` against `xpsim`
  separately and **print both verdicts** — the rehearsed one and the live one. A
  single green rehearsal against a simulator does not cover a live push.
- `store.StartRun` refuses a `live-vms` run whose URL contains `127.0.0.1`
  (`internal/store/runs.go:37`). You cannot accidentally label a simulator run as a
  live one.

> **Q: did the event reach an operator?**
> A: The push and the read-back prove it reached the **VMS**. It does not prove
> anything reached a Smart Client — that is the one claim that needs the screen
> capture from 1.1, and it is the artefact the submission turns on.
</details>

---

## 0:30 — "Show me the region is measured, not guessed."

The picture has a box on it. So does every pushed event. The box is arithmetic on
pixels the process already had, computed by ~900 lines of pure Go with no model in
it.

```powershell
pocctl audit decisions
```

```
det-3f9a2c1b8e7d4056
  mode=temporal class=motion score=0.4127 threshold=0.3500 sustain=1/1 region=measured 426,150,546,350
  frame     1841
```

The score, the threshold it was compared against, and the region in the same
0..1000 thousandths the event carries and the database stores. One expression
produces all three, because three expressions is three chances for one to drift —
and a drifted box on a pushed event is a number on a screen that disagrees with the
row beside it.

> **Q: where did the coordinates come from?**
> A: From the pixels. The model is asked what is happening *in* the region and is
> never asked for coordinates — the Hafnia VLM API documents no region output, and
> a prompt that asked for one would be asking for something the API does not
> answer. `internal/novelty` is pure Go, licence-free, 900 lines, and owns the box.

<details>
<summary><b>live-vms variant</b> — the same box, on a pushed event</summary>

Open the event in Smart Client. The `highlight` object is the same four numbers,
scaled to whatever the Smart Client's own coordinate space is. What you are showing
is that the numbers on the VMS screen, the numbers in the database, and the numbers
`pocctl audit decisions` prints are one set of numbers in three places.

```powershell
pocctl audit decisions --run <the live run id>
```

> **Q: a real camera moves and the box jitters. Is that the measurement or the codec?**
> A: Both, and the module says which. A jittering box is the novelty engine measuring
> a compressed frame that moved; the score beside it is the same number the threshold
> was compared against. What the module will not do is quietly widen a box to make it
> look stable — the region on the row is the region that was measured, and that is
> one of the red checks.
</details>

---

## 1:00 — "Show me the verdict is the model's and not the detector's."

The bottom bar of the picture is burned into the pixels. It cannot be hidden by
editing HTML, because there is no HTML involved in that bar and no JavaScript on the
screen at all.

```
VERDICT confirmed  ·  push_sent  ·  det 4  ·  pushed 4  ·  vlm hafnia
```

> **Q: what if the model was not asked?**
> A: It says `VERDICT not asked`, and every bookmark says the model was not asked
> and why. A missing answer is never a negative answer — a `false_positive` on a
> degraded call tells an operator "nothing happened" because *we* could not ask, and
> that is the worst failure this product can have. There are two red checks on this
> rule: one where the verdict is cleared, and one where the store refuses to write
> it.

<details>
<summary><b>live-vms variant</b> — the same bar, and the failure it is built for</summary>

On a live run this is where the four absence states stop being a table in a document
and become a thing you can cause. Take the API key away for ten seconds — or set the
daily cap to zero — and run again.

```
VERDICT not asked  ·  push_sent  ·  det 6  ·  pushed 6  ·  vlm hafnia (cap_reached)
```

The event is **still pushed**, with the model marked as not asked and the reason
beside it. Nothing was lost: the detection, the clip and the hash are all on the
record, and the operator sees a bookmark that says the classification is missing
rather than one that says the camera was fine.

> **Q: why is the event pushed at all if the model could not classify it?**
> A: Because the event is a measurement and the verdict is an opinion about it.
> Dropping the event because the opinion is missing would lose the measurement; the
> five minutes you need for the operator to look is real time.
</details>

---

## 2:00 — "Prove it. Right now, on this machine."

```powershell
pocctl prove --run <the run id from step 0>
```

```
proof for det-3f9a2c1b8e7d4056
  at              2026-09-30T19:41:07Z
  provider calls  0 (measured, not asserted)
  ledger          seq 12..31  20 row(s) from sequence 12 to 31 recomputed and chained

  [PASS] trigger recomputed from the stored measurement
         observed: score=0.412700 threshold=0.350000 region_source=measured streak=1/1 reason=changed
         expected: score >= threshold, which is true because this detection exists
  [PASS] the region on the row is the region that was measured
  [PASS] a verdict exists only on an answered ask
  [PASS] the stored output hashes to the stored hash
  [PASS] ledger chain recomputed from genesis
  [PASS] seal verified against the stored chain
  [PASS] zero provider calls during the replay

  PASS
  7 beats recomputed from stored facts, zero provider calls
```

> **Q: how do I know it did not just re-run the model?**
> A: It cannot. The replay function is **handed no observer and no client**, so there
> is no handle by which it could call one. `provider calls 0` is then measured
> anyway, and the whole report is void if it is not zero. The printed report says
> `THIS IS NOT A REPLAY` in that case.

> **Q: what does it actually recompute?**
> A: The measurement against the threshold that was stored beside it, the reported
> region against the row, the stored structured output against its stored hash, and
> the ledger from the genesis hash. It does **not** recompute the verdict — there is
> nothing to recompute it from. It checks that the verdict is *consistent with the
> ask*, which is the thing that can be wrong.

<details>
<summary><b>live-vms variant</b> — the same proof, over a run that really pushed</summary>

```powershell
pocctl prove <a detection from the live run> --data-root <the live root>
```

The beats look different and the two numbers that matter do not:

- `provider calls 0` is measured the same way, by the same counter, over a run where
  the model **was** called once per event. That is the point: on a live run the model
  genuinely was called, and the proof still reports zero, which is only possible
  because the replay path holds no client.
- `ledger seq …` now includes `LedgerPushResult` rows for real pushes. Recomputing
  them proves the recorded push outcomes were not edited to say "sent".

> **Q: is the live run's proof as good as the simulator's?**
> A: It is the same proof over more rows. Nothing about the proof is weaker and one
> thing is stronger: the `push_sent` rows it chains are rows about a real server's
> real answer.
</details>

---

## 3:00 — "Fine. Show me you did not just write the answers down."

```powershell
pocctl audit ledger          # recompute the chain
pocctl audit seal --key k    # sign the head
pocctl audit seals --trusted-key k.pub
```

Each row's hash is computed over a **length-prefixed** encoding of that row and the
previous row's hash, so moving a character across a field boundary changes the
bytes it commits to. `UPDATE` and `DELETE` on the ledger are refused by **SQLite
triggers**, not by a Go function — because the person most likely to edit a row is
not running this program.

The seal is ed25519 over the head hash and the row count. A chain that verifies is
not the same claim as a chain somebody signed, and the output says which one you are
looking at: `SEALED`, or `NOT SEALED`, never a blank.

> **Q: what if you just recomputed the hashes after editing the rows?**
> A: Then the old signature stops matching the new head, and `pocctl audit seals`
> reports `STALE`. That is the specific attack the seal closes and it is a test.

<details>
<summary><b>live-vms variant</b> — the same chain, over a longer run</summary>

Nothing changes and that is the finding. On a 24-hour soak the chain is a few
hundred thousand rows and the same three commands are the whole verification. The
seal is worth signing at the **end** of the soak and again at each handover: a seal
whose timestamp is inside the run window is evidence about that window, and a seal
signed after somebody edited the database is evidence about nothing.

> **Q: what if the database is deleted?**
> A: Then the seal is the only thing left, and `pocctl audit seals --trusted-key`
> verifies it against the public key without the database. That is why the seal goes
> to a sink outside the database at all.
</details>

---

## 4:00 — "Show me the thing you have not done."

```powershell
pocctl brief --run <the run id from step 0>
pocctl search "…" --run <the run id from step 0>
```

The brief is one row per hour, built from the run's own heartbeats. It exists
because "nothing happened in that hour" has four different causes and three of them
are fabrications if you print them the same way:

```
hour    frames  hits  what happened
10:00      900     0  stream up, detector found nothing (never asked: nothing to ask)
11:00        0     0  stream was DOWN: heartbeats with zero frames
11:00      1800     2  2 detection(s), model NEVER ASKED: not asked
```

The second line is a camera that was not there. The third is footage nobody looked
at. Both would print as "no events" in every incident tool ever built, and both are
claims about a camera's safety that nobody measured.

The search is the other half. It returns what the model *said*, with the clip to open
and the model's own claim beside it, and it prints the four absence states of the
**window**:

```
in this window: 3 proposal(s), asked and answered 1 (0 with no objects, 1 degraded, 1 not asked, 0 cannot tell)
THIS WINDOW IS NOT FULLY SEARCHABLE.
```

> **Q: you searched for "person" and found nothing — does that mean there was no person?**
> A: No, and the tool refuses to say so. A window with an unasked proposal cannot
> support the sentence "the model found nothing", so `pocctl search` prints that
> line instead. That refusal is a red check: remove it and the suite goes red.

> **Q: where did the false-positive rate go?**
> A: `NOT RUN`, because nobody has looked at footage yet. It is one command now:
>
> ```powershell
> pocctl mark aicap-… --verdict false_positive --by <your name>
> ```
>
> The store refuses a tag the run never pushed, so a rate cannot be inflated by
> marking events that were never shown to anybody. Until somebody makes it, the
> report prints `NOT RUN`, which is not a zero.

| what this build has **not** proven | why it is printed rather than guessed |
|---|---|
| a false-positive rate | no live camera and no live XProtect instance were reachable, so there were no pushed events for a person to judge |
| that an alarm reached a Smart Client | what is proven is that the POST is contract-shaped and that a read-back found it |
| that a *reference-mode* detector fires on a real camera | the pinned-reference path is tested against synthetic fixtures; a real moved camera has not been in front of it. It reports `unpinned`, never `unchanged`, until a reference exists |
| that the EU region host is the right one | the provider's own documentation carries a contradiction; `pocctl vlm-probe` makes the one call that settles it, and it has not been run against a key |

> **Q: so is it finished?**
> A: No, and the module says so on every page. `pocctl demo --live` is the run that
> closes the first three rows and `docs/demo-live.md` is what you read before it.

<details>
<summary><b>live-vms variant</b> — the same minute, and the list gets shorter</summary>

On a live run with a marking window behind it, the first row of that table is the
only one left:

```powershell
pocctl mark aicap-… --verdict false_positive --by <name>
```

```
marked aicap-car-9f2c1b7e --verdict false_positive by A. Reviewer, run 2026-10-14-live
precision: 3/11 (precision 0.27), marked by A. Reviewer on 2026-10-14
```

Print **the reviewer's name and the date** with the number. A false-positive rate
with neither is not a measurement; it is a number that will be asked about and cannot
be defended.

Three things about that number, and they are the whole of 1.2:

- The store refuses a tag the run never pushed, so a rate cannot be inflated by
  marking events nobody was shown.
- Until somebody marks something, the report prints `NOT RUN`. Not `0`, not a
  percentage, not a blank. `NOT RUN` is a correct answer; `0` is a lie.
- Marking is a **person with a named end date**. It does not parallelise with
  anything, and it is the only item on the critical path that no amount of code
  compresses.

| still not proven, after a live run | why it is printed rather than guessed |
|---|---|
| that an alarm reached a **Smart Client** | the push and the read-back prove it reached the VMS. The screen capture is the artefact, and it is a person with a mouse |
| that a *reference-mode* detector fires on a real camera | it reports `unpinned`, never `unchanged`, until a reference exists. A real moved camera needs a ladder |
| that the EU region host is the right one | one call settles it. If `vlm-probe` has not been run, the contract row is still `asserted_not_verified` and says so |
</details>

---

## 5:00 — The last question

> **Q: how do I know any of this is enforced and not just written down?**

```powershell
.\scripts\verify.ps1
```

Every control in the table is removed from the **production** code, one at a time,
and the suite must go red every time. The step prints its own count, so this table
and the gate cannot drift apart:

| | control removed | the lie that lets in |
|---|---|---|
| 1 | the evidence path must derive from a content hash | two events point at one stored object |
| 2 | the detection terminal-state guard | one crossing ends up with two stories |
| 3 | the line-crossing tolerance band | no crossing is ever decided, and it reports zero events and no error |
| 4 | **a verdict is cleared on an unasked ask** | an operator is told nothing happened because we could not ask |
| 5 | the store refuses a verdict on an unasked ask | a second writer puts the same lie into the proof table |
| 6 | the chain compares hashes | "the ledger is intact" becomes a comment |
| 7 | the ledger is append-only | a person with a sqlite3 prompt edits a recorded decision |
| 8 | a replay makes zero provider calls | "replayable from stored facts" stops being a claim about the code |
| 9 | a snapshot is the frame the decision was made on | the picture on file is a real frame of the same camera and a different instant |
| 10 | an all-zero bounding box is refused | Milestone draws nothing while the database says otherwise |
| 11 | a cross-region call is refused | a site in the EU ships footage to the US host |
| 12 | an unasked window is not searchable | `pocctl search` says "no matches" for footage the model never saw |

Removing a **test** would prove nothing. A suite with one fewer test still passes and
the thing that test was watching is still unwatched. These remove the checks
themselves.

There is one more refusal that is deliberately **not** in that table: a
reference-mode run with no pinned view can never report `unchanged`
(`novelty.IntegrityUnchanged`). It has two tests and no control row, because the
table is a list of properties of code and "this is not a control" belongs next to the
ones that are.

And the gate prints `SKIPPED`, visibly, for anything it could not run. It did not
run the race detector on the run you are looking at unless `PASS go test -race` says
so. A gate that describes its own coverage as green when it is grey is a gate nobody
should trust, and there is one on this machine right now: **`mediamtx` is not
installed**, so the rtsp relay path was not exercised.

---

## Before the five minutes: the two probes

Each minute above has a `live-vms` variant, and those variants assume you have run
these two first. They are the shortest possible answer to "have you ever spoken to
the thing?" and they take a minute between them.

```powershell
# 1. does the pinned host, path and auth scheme answer?
#    ONE call. No clip. The raw body lands in contracts/hafnia/raw/ whatever happens.
pocctl vlm-probe --print-body

# 2. do these credentials work, and is there an Analytics event type to push to?
#    READ-ONLY. It never POSTs an event and never promotes a row to verified.
pocctl probe --live
```

What each one settles, and what it does not:

| | settles | does not settle |
|---|---|---|
| `vlm-probe` | the EU host spelling (assumption 6), the completions path, the `ApiKey` auth scheme, whether the API reports a model name | whether a clip upload or a full-length answer works — that is the demo's job |
| `probe --live` | the credentials, the event-type list, the tag filter syntax, the bookmark read-back | **live stream availability**, which no REST route answers. It prints `NOT RUN` for that and says a person has to open the camera with `ffplay` |

`docs/probes.md` is where their answers go, question by question. It is currently a
page of `NOT RUN` with a mechanism beside each one, because nothing has been run.

> **Q: you ran it against Milestone. Was it verified?**
> A: The route table says `reachable`, and that is the strongest word any tool in
> this repository is allowed to say. `verified` is a row in
> `contracts/xprotect/contract.json` that a **human** flips, with a date on it, after
> reading the evidence. A probe that could promote its own homework would be worth
> nothing.

## One thing to hold onto

The default is not "everything works". The default is "nothing was measured, and this
is exactly what was measured".

Every number on the screen came out of SQLite. Every `NOT RUN` means a person did
not look. Every `false_positive: NOT RUN` sentence is longer than a number would
have been, and that length is the feature.
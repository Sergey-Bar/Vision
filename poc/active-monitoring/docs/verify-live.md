# The five-minute verification script

A judge does not read this repository. They watch a screen for five minutes and then
ask whether any of it is real.

This document is the five minutes, in order, with the answer to each question that
follows it.

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

---

## 4:00 — "Show me the thing you have not done."

```powershell
pocctl audit observations
```

```
false positives: NOT RUN: no human marked any event in testdata/groundtruth.json
```

| what this build has **not** proven | why it is printed rather than guessed |
|---|---|
| a false-positive rate | no live camera and no live XProtect instance were reachable, so there were no pushed events for a person to judge |
| that an alarm reached a Smart Client | what is proven is that the POST is contract-shaped and that a read-back found it |
| that a *reference-mode* detector fires on a real camera | the pinned-reference path is tested against synthetic fixtures; a real moved camera has not been in front of it |
| that the EU region host is the right one | the provider's own documentation carries a contradiction; `contracts/hafnia/README.md` names both spellings and one live call settles it |

> **Q: so is it finished?**
> A: No, and the module says so on every page. `pocctl demo --live` is the run that
> closes the first three rows and `docs/demo-live.md` is what you read before it.

---

## 5:00 — The last question

> **Q: how do I know any of this is enforced and not just written down?**

```powershell
.\scripts\verify.ps1
```

Eleven controls are removed from the **production** code, one at a time, and the
suite must go red every time. They are:

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

Removing a **test** would prove nothing. A suite with one fewer test still passes and
the thing that test was watching is still unwatched. These remove the checks
themselves.

And the gate prints `SKIPPED`, visibly, for anything it could not run. It did not
run the race detector on the run you are looking at unless `PASS go test -race` says
so. A gate that describes its own coverage as green when it is grey is a gate nobody
should trust, and there is one on this machine right now: **`mediamtx` is not
installed**, so the rtsp relay path was not exercised.

---

## One thing to hold onto

The default is not "everything works". The default is "nothing was measured, and this
is exactly what was measured".

Every number on the screen came out of SQLite. Every `NOT RUN` means a person did
not look. Every `false_positive: NOT RUN` sentence is longer than a number would
have been, and that length is the feature.
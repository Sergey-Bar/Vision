---
name: red-check
description: Prove that a control, invariant, or test in this repository actually fails when the control is removed. Use when adding or changing an invariant test, a schema CHECK, a validation guard, a RLS policy, a red-checked assertion, or any claim in docs/status.md or docs/threat-model.md that says a control "was run once with the control removed and watched go red". Also use when asked to prove a test is meaningful, to verify a guard is not decorative, or to check whether a claim of protection is backed by evidence. This is not a general test-coverage audit; it is the falsification step for a specific claim.
---

# Red-check

A control with no such run is not a control; it is a comment.

`docs/status.md` says it, `AGENTS.md` requires it, and every gate comment in
`scripts/verify.ps1` assumes the reader knows what it means. This skill is the
procedure for producing that evidence.

The output is not a test you write. It is a **record that the test you already
wrote can fail**, with both transcripts kept.

## Why this exists

A test that has only ever passed has never been shown to be capable of failing.
Three of the four real defects listed in `docs/status.md` would have been caught
by a reader — but the control that would have caught them was a comment, and a
comment does not fail.

This is also the one thing the imported skills cannot do for you. `tdd` produces
a failing test before the code. That is a different claim: it shows the test
tracks a *new* behaviour, not that it guards an *existing* invariant.

## Procedure

### 1. State the claim as a falsifiable sentence

Before touching anything, write down:

```
CLAIM:     <what the control is>
GUARDED:   <file:line of the test that guards it>
HOLDS WHEN: <what is true in the tree today>
FAILS IF:  <the specific thing being removed>
```

"FAILS IF" must be one concrete edit. "if the invariant were violated" is not
an edit; it is a wish.

### 2. Locate the guard

Read the invariant in `AGENTS.md`, then find the test that names it. If you
cannot find a test that fails when the invariant is violated, **stop and report
that** — that absence is the finding, and it is a DoD item rather than something
to paper over.

Useful places to look, in the order the repository puts them in:

| Control | Where the proof lives |
|---|---|
| A Go invariant | the `_test.go` next to the package, or `internal/schemalaws/` |
| A schema rule | `internal/schemalaws/schemalaws_test.go` (RLS, CHECK, append-only) |
| A durability rule | `internal/durability/durability_test.go` (kill -9, lease races) |
| A wire change | `internal/contracts/contractcompat_test.go` |
| A vendor assumption | `internal/milestone/route_audit_test.go` |
| An encoding rule | `internal/testutil/encoding_test.go` |
| A decision replay | `internal/dr/` (`TestRecoveryDrill`) |

### 3. Remove the control, and only the control

Make the smallest edit that removes the guard and nothing else. Then run the one
test:

```powershell
& "C:\Program Files\Go\bin\go.exe" test -p 1 -count=1 -run '^TestTheExactName$' ./internal/thepackage/
```

Record the exit code and the failing line.

**The test must FAIL.** If it passes with the control gone, the control was
decorative and you have found a real defect. That is a better outcome than a
pass; report it as one.

### 4. Restore, and prove the restore

```powershell
git diff --stat   # must show the control back
```

Re-run the test. It must pass. A red-check that was never restored is a broken
tree, and the evidence for it is worthless.

### 5. Write the record

The record belongs in `docs/status.md`, in the section for the work, not in a
separate file nobody reads. The shape already used there is a table:

| Found | Cost had it shipped |
|---|---|
| `<the control that was removed>` | `<what would have shipped>` |

Include both transcripts. A claim without the failing output is exactly the
kind of claim this repository treats as a defect.

## Rules

- **One control per run.** Removing two controls at once produces a failure you
  cannot attribute, which is the same as having no evidence.
- **Never remove a control from a migration, a contract, or `AGENTS.md`.**
  Those are reserved for a human, and a red-check that edits one to prove a point
  has changed the thing it was measuring. Prove the equivalent guard in a
  scratch copy instead, and say that is what you did.
- **Red-check the test, not the invariant.** The invariant is the claim; the test
  is the guard. Removing the invariant from `AGENTS.md` proves nothing about
  whether the code enforces it.
- **A skipped test is not a pass.** `internal/testutil.Pool` skips when
  `SP_TEST_DATABASE_URL` is absent, and `TestSymlinkEscapeIsRefused` skips on
  Windows. If the guard you are red-checking is one of those, say the evidence
  is missing rather than reporting a result.
- **Stay inside the repository.** Red-checking means editing tracked files and
  restoring them. If the tree is dirty when you start, say so and stop — you will
  not be able to tell your restore from someone else's work.

## Recording a negative result

"Not red-checked" is a legitimate, valuable answer. Write it as:

```
Not red-checked: <control>. Reason: <what blocked it>. Cost if it is
decorative: <what would ship>.
```

`docs/status.md` already uses this shape for missing evidence ("that is missing
evidence, not a pass"), and it is the honest form. Do not leave a control
unlabelled, because an unlabelled control reads as a checked one.
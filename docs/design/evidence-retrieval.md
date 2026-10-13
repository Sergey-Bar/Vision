# Evidence retrieval — the design, written before the code

P0.4 of the autonomous plan, owner `internal/evidence` and `internal/xprotect`.
Contract: **`evidence.proto` unchanged.** This document is the design
sign-off; the code is the second step and a human is still required.

Status: **design written, implementation blocked on M2** (the XProtect
bookmark-and-clip phase, which P2.5 requires before a real clip ID is issued).

---

## 1. Why this document exists before the code

The platform today can retrieve *evidence it already holds*. `GET
/v1/evidence/{evidenceId}/content` finds the row, opens the object from local
storage, hashes it and streams it. That is the whole retrieval path, and it is
correct: the product copies bytes once, at ingest, and a fetch is a local read.

What it cannot do is retrieve a **bookmark or clip in the vendor's own
storage** — the thing M2's XProtect phase *places* and receives a read-back
receipt for. `internal/evidencefetch` writes that receipt, and its own test
`TestTheWorkerIsNotAFetchWorker` is the guard: this product deliberately does
not copy vendor footage into its own store, so a receipt is a pointer plus a
read-back, and a pointer is a different fact from a file.

So P0.4 is one design with two retrieval kinds and one failure law that applies
to both:

> **A retrieval failure is always distinguishable from an empty result.** A 200
> with no bytes means "there were no bytes"; every other outcome carries a
> reason an operator can act on. An empty body and a failed retrieval are
> different facts, and no code path may render them the same.

The second sentence is the red-check's claim and the reason this document is a
gate rather than a ticket.

---

## 2. The shape of a retrieval request

A retrieval request is addressed by **an evidence identity, not by a location**.
There is no path in the request, and `storage_ref` is never taken from one: it is
resolved from the row and then re-validated on the way out (the traversal guard
in `evidence.Ref` runs on every fetch, not only on write).

```proto
// NOT A CHANGE to evidence.proto. This is the shape of the REQUEST, which the
// existing search/searchbookmark/readback endpoints already assemble from
// Evidence. Nothing here adds a field to the wire.
message evidence_retrieval_request {   // conceptual, request-shaped
  string evidence_id = 1;     // required; the row's identity
  bytes  expected_sha256 = 2; // optional; when present the returned bytes MUST
                              // match it, and a mismatch is a REFUSAL
                              // (409), not a warning
  RetrievalMode mode = 3;     // STORED is the default: refuse-or-open local

  enum RetrievalMode {
    STORED = 0;               // the object is ours; open it, verify, stream
    VENDOR_POINTER = 1;       // resolve the bookmark/clip reference and
                              // describe it (a receipt), never a body
  }
}
```

Three facts follow from the shape, and each one is deliberate:

- **`evidence_id` and never a path.** The row is the authority on what this
  evidence is, where it lives, and which tenant and site it belongs to. A
  caller-supplied path makes the caller the authority on location, which is the
  traversal class this repository tests on every evidence path.
- **`expected_sha256` is checked on the way OUT, not only at write.** A fetch
  that reads a stored object and forgets to verify it is a fetch that will
  happily serve a corrupted store. Today's `handleEvidenceContent` passes the
  expected hash into `Open`, which verifies it (line 663-668 in
  `internal/api/handlers.go`); the design keeps that and extends it to the
  read-back path.
- **`VENDOR_POINTER` returns a description, never bytes.** This is the
  `TestTheWorkerIsNotAFetchWorker` invariant restated as a mode. A retrieval
  request for a vendor bookmark returns the receipt: the reference, the
  read-back outcome, the idempotence key, and the time the read-back was
  performed. It does not return the clip, because the product does not copy
  clips.

---

## 3. The stages, and what a failure looks like at each one

Retrieval is a pipeline, and every stage must fail **legibly**. The current
implementation has the first three stages; the last three are M2's.

| # | Stage | What it does | What failure looks like | Where |
|---|-------|--------------|-------------------------|-------|
| 1 | **resolve** | find the row by `evidence_id` | `404` "no such evidence" — with NO statement about whether it exists for another tenant | `handleEvidenceContent:715-722` |
| 2 | **authorise** | verify `tenant_id` and `site_id` against the caller's claims | `404` "not my evidence" — deliberately the same 404 as stage 1, so an enumeration probe learns nothing | `handlers.go:727-738` |
| 3 | **locate** | turn `storage_ref` into a path and namespace it to the object's `(tenant, evidence_id)` | `404` "storage ref does not resolve" | `evidence.Open` |
| 4 | **read** | open the bytes | `500` "evidence store error" — the only stage where a 500 is honest | `handlers.go:763-770` |
| 5 | **verify** | hash the bytes and compare with `expected_sha256` | `409` "hash mismatch" — **a corrupted store, never an empty result** | `handlers.go:666-676` |
| 6 | **audit and stream** | write the `ActionEvidenceFetched` row, then the bytes | `500` "audit append failed" — the audit row is written BEFORE any byte is streamed, so a failure to record the read leaves no un-audited bytes | `handlers.go:680-698` |

Stages 7–9 are M2's vendor path, and the design says now what their failures
look like, so the M2 phase cannot invent them:

| # | Stage | Failure |
|---|-------|---------|
| 7 | **resolve pointer** — find the bookmark/clip receipt for the evidence id | `404` "no bookmark receipt for this evidence" — an incident that cites evidence nobody ever bookmarked |
| 8 | **read back** — confirm the bookmark still exists in the vendor | `502` "the vendor no longer has this bookmark", with the receipt's idempotence key so a retry cannot double-place |
| 9 | **describe** — return the receipt | no body. An empty receipt list and a failed read-back are different answers, and only one of them is a 200 |

### The law these stages encode

**Stage 6's ordering is the law applied to the code.** The audit row is written
*before* the first byte is streamed, so there is no window in which an operator
has read an incident's evidence and no trail records it. That is not a
convention: it is the attention-producer control (RC-17) on the evidence path,
and the design fixes it here so the M2 phase cannot reorder it for convenience.

**Stage 2's 404 is a security decision, not an implementation shortcut.** A
distinct 403 for "this evidence exists but is not yours" turns
`GET /v1/evidence/{id}/content` into an enumeration oracle. The cost is that an
operator who mis-copies an id cannot tell "wrong id" from "wrong site"; the
benefit is that a probe cannot either. The audit ledger records the denial, so
the operator's confusion is visible to support even though it is not visible to
the operator.

---

## 4. The fault-injection test that proves the failure is visible

This is the red-check the gate runs, and the plan names it: **"retrieval
failure is visible, never empty."** The test has to break the property the same
way the code could, so it injects the fault at each stage rather than asserting
on a suite of unit errors.

Four injections, one per failure that matters:

1. **The store is empty where the row says there is a file.**
   A row exists, `storage_ref` resolves to a path, and `Open` returns
   `ErrEvidenceNotStored`. The claim: the response is a `404` with a body naming
   the stage, and **not** a `200` with an empty body. A 200 here is the defect:
   a client that got an empty stream cannot tell "no clip" from "the store is
   gone", and the incident's evidence silently stops existing.

2. **The bytes changed under us.** `expected_sha256` is supplied and does not
   match. The claim: `409`, and the audit row for the *read* is still written —
   a failed verification is still a read that happened, and hiding it means the
   corruption is invisible except to the one operator who tried.

3. **The audit append fails.** The ledger is ahead, or the append is refused.
   The claim: no bytes are streamed AT ALL. This is the control that stage 6 is
   ordered for, and the injection that proves it is a read that must produce no
   body rather than a body without a trail.

4. **The object is a vendor pointer and there is no receipt.** The claim: `404`
   from stage 7, not a `200` with an empty receipt list — the same statelessness
   as (1), one abstraction up.

Each injection is a row in the red-check table, and the mutation it guards is
removing the stage's legibility, not the stage itself: the control's claim is
that the *failure stays visible*. A mutation that makes the failure silent is
the one that goes red.

### Why these four and not the whole table

A test for every error return is a test of an error enum. The four above are the
ones where a silent failure is *indistinguishable from success to a client*,
which is the property the whole design exists to protect and the property that
distinguishes "retrieval failed" from "there was nothing to retrieve".

---

## 5. What this design does not do

- **It does not copy vendor footage.** `VENDOR_POINTER` returns a receipt. The
  product's position on copying is decided (`TestTheWorkerIsNotAFetchWorker`)
  and this design does not reopen it.
- **It does not change `evidence.proto`.** No new message, no new field, no new
  endpoint. `contractcheck` is a gate and a contract change needs a human.
- **It does not add a retention path.** Retention law is in
  `migrations/` and is the append-only ledger's, not this document's.
- **It does not implement M2.** Stages 7–9 are described so that M2 cannot invent
  them; they are built in P2.5, by a human, against a real XProtect
  installation.

---

## 6. What a reviewer is signing

1. That a retrieval request is addressed by evidence identity, optionally with a
   hash to verify.
2. That the six existing stages' failure codes are the ones in §3, and that the
   404/403 collapse is deliberate.
3. That the audit row precedes the bytes.
4. That the four injections in §4 are the fault-injection set, and that each is
   added to the red-check table with a hand-run transcript in
   `docs/status.md` — an entry nobody watched go red is a comment, and this
   repository's rule is that it is not added until it has been watched.

Items 1–3 are in the tree today. Items 4 and the M2 stages are the remaining
work, and they are blocked: item 4 waits on the M2 phase that can inject at all,
and items 7–9 wait on a CLI I do not have.

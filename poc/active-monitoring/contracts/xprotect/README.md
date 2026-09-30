# `contracts/xprotect/` — what these files are, and where they came from

Every file in this directory is **downloaded from Milestone's public developer
documentation**, unmodified except for the line-ending normalisation that the
`Invoke-WebRequest` fetch performed. Nothing here was written from memory. The
whole point of the directory is that a field the POC sends can be checked against
Milestone's own words, and a test can fail when the two disagree.

## Provenance

| File | Source | Retrieved | Licence |
|---|---|---|---|
| `bookmarks-rest-v1.openapi.yaml` | `https://doc.developer.milestonesys.com/mipvmsapi/api/bookmarks-rest/v1/openapi.yaml` | 2026-09-30 | MIT (`info.license.name: MIT License`, `url: https://opensource.org/licenses/MIT`) — declared inside the file itself |
| `events-rest-v1.reference.html` | `https://doc.developer.milestonesys.com/mipvmsapi/api/events-rest/v1` | 2026-09-30 | Milestone public documentation page |
| `events-rest-v1.reference.md` | the HTML above, tag-stripped so it is reviewable in a diff | 2026-09-30 | same as the page above |

## Why the events specification is HTML and not YAML

`https://doc.developer.milestonesys.com/mipvmsapi/api/events-rest/v1/openapi.yaml`
returns **404**, and so does `openapi.json`. The download links on that page are
rendered by JavaScript, so the specification is not reachable as a file. The
fetched page is kept in both its original form (`…reference.html`) and as
stripped text (`…reference.md`) because the original is the evidence and the
stripped form is what a reviewer can read.

This is recorded here rather than quietly worked around, because "we wrote the
event body from the prose" is a weaker claim than "we checked the event body
against the page Milestone serves".

## What was checked against what

The contract test in `internal/xprotect/contract_test.go` reads these files and
asserts that:

* every HTTP path the client calls appears in the downloaded specification, and
* every JSON field the client marshals appears in it, and
* the status codes the client treats as success appear in it, and
* the `servers` block points at `/API/rest/v1` and not at the `/api/v1` prefix
  the platform's own `internal/milestone` guessed at.

A test that cannot find a path or a field fails. That is the whole mechanism: no
call goes out without a line of Milestone's own documentation behind it.

## The three places the platform's existing Milestone code is wrong

Recorded because the temptation to reuse it is strong and the reuse would be
silently wrong:

1. **Path prefix.** `internal/milestone/routes.go` uses `/api/v1/...` and marks
   every route `Unverified: true`. The real API is under `/API/rest/v1/`.
2. **Bookmark body.** `internal/milestone/writeback.go` posts
   `{"bookmarks":[{idempotencyKey, deviceRef, time, label, description}]}`. The
   published body is `newBookmarkBody{header, devicePath{type,id},
   timeTriggered, timeBegin, timeEnd, description, reference}`. `idempotencyKey`
   and `deviceRef` do not exist.
3. **No event push at all.** The platform can place a bookmark and read events;
   it has no "trigger event" path. The POC adds one, and that is the function a
   MIP analytics plug-in exists to provide.

## What these files do not license anyone to claim

Having checked a `POST` against a published specification proves the request is
contract-shaped. It does not prove an event reached an operator's Smart Client,
because no XProtect server has ever been reachable from this repository
(`docs/status.md` §535 in the parent repository says the same thing). The report
prints that line every time, whatever the contract tests say.

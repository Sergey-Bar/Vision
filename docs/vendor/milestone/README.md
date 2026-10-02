# `docs/vendor/milestone/` — what these files are, and where they came from

Last revised: 2026-10-02.

Every vendor document in this directory was **downloaded from Milestone's public
developer documentation** and committed unmodified except for the line-ending
normalisation the fetch performed. Nothing here was written from memory.

## Provenance

| File | Source | Retrieved | Licence |
|---|---|---|---|
| `bookmarks-rest-v1.openapi.yaml` | `https://doc.developer.milestonesys.com/mipvmsapi/api/bookmarks-rest/v1/openapi.yaml` | 2026-09-30 | MIT — declared inside the file itself (`info.license.name`, `info.license.url`) |
| `events-rest-v1.reference.md` | `https://doc.developer.milestonesys.com/mipvmsapi/api/events-rest/v1`, HTML tag-stripped so it is reviewable in a diff | 2026-09-30 | Milestone public documentation page |

The events API has **no reachable OpenAPI file**: `…/events-rest/v1/openapi.yaml`
and `openapi.json` both return 404, and the download links on the page are
rendered by JavaScript. That is recorded rather than worked around, because
"we wrote the event body from the prose" is a weaker claim than "we checked it
against the page Milestone serves".

## The audit

[`route-audit.md`](route-audit.md) is what this directory is *for*: every route in
`internal/milestone` with a verdict against these two documents and a line
citation. It is the evidence behind the transport corrections in
`internal/milestone`, and `internal/milestone/route_audit_test.go` is what stops
the code and the audit drifting apart.

## Why these files are duplicated in `poc/active-monitoring/`

The same two documents are committed under
`poc/active-monitoring/contracts/xprotect/`, because
`poc/active-monitoring/internal/xprotect/contract_test.go` reads them from a
relative path and is part of that module's own gate. Moving them would have broken
a separately gated module over a copy of two files, and deleting the originals
would have taken that module's evidence with them.

They are byte-identical. **If one is updated, both are**, and the two contract
tests are what will notice if they are not — one per module, each asserting
against its own copy.

The duplication is a cost with a name rather than a surprise later: two copies of
the same vendor evidence, in two modules, each with its own assertion that they
have not diverged. It is smaller than the alternative, which was either a broken
gate or a shared path across a module boundary the repository does not otherwise
cross.

## What these files do not license anyone to claim

Checking a path or a field against a published document proves the request is
contract-shaped. It does not prove anything reached an operator's client, because
**no Milestone server has ever been reachable from this repository**
(`docs/status.md` says so, and `internal/milestone/routes.go` marks every route
`Unverified` for the same reason). `securityctl adapter probe` against a real
installation is what changes that, and a human edits `routes.go` afterwards.

The three defects these documents exposed in the platform's own adapter are
recorded in [`route-audit.md`](route-audit.md) §1, and in
`poc/active-monitoring/contracts/xprotect/README.md` §"The three places the
platform's existing Milestone code is wrong", which reached the same three
conclusions independently before this audit existed.
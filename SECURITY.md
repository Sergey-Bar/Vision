# Security Policy

This product runs on a customer's isolated network, holds camera footage, and
makes decisions about physical security. A vulnerability report is the most
valuable thing anyone can send us, and the handling of one is a promise.

## Reporting a vulnerability

**Do not open a public issue for a security problem.**

> **TODO(owner): this repository is private and on GitHub Free, which means
> neither the Security Advisory flow nor private vulnerability reporting is
> available — both need a paid plan. Until that is sorted, report by email to
> `SECURITY-CONTACT@EXAMPLE` — replace this with a real monitored address before
> this product ships to anyone.** An address nobody reads is worse than no
> policy, because it looks like a promise.

If you would rather not wait for that, open a normal issue and mark it
`security-sensitive`; the maintainers will convert it to a private channel and
delete the public copy. Say that in the issue body so it is obvious.

## What to include

The more of these you can give us, the faster we can act:

- What you tested and how, including the version or commit.
- The impact you believe it has, in the terms below.
- A minimal reproduction, ideally against the release archive rather than a
  developer's tree.
- Whether it is already known to you, and whether anyone else has been told.

You do not need to propose a fix. A clear reproduction is worth more than a
patch we cannot verify.

## What we consider in scope

| In scope | Out of scope |
|---|---|
| Tenant isolation, or any path where one tenant sees another's data | Findings that require physical access you could not have had |
| The audit ledger, checkpoints, or evidence integrity — anything that lets a record be altered, removed or forged | Denial of service against a site you do not operate |
| A rule, bundle, or model bundle executing something it should not | A model's output being wrong, where the design explicitly makes it advisory |
| Authentication, authorisation, or session handling | Missing hardening headers on an operator console with no sensitive content |
| The path from an accepted event to a terminal state, if an event can be lost or duplicated | Performance findings without a demonstrated correctness or safety consequence |
| Any path where AI output is treated as authoritative | The absence of features listed in the roadmap |

The rule of thumb: **if it would let someone conclude something about the
physical world that is not true, it is in scope.** If it is inconvenient but
does not mislead an operator about what happened at a site, it is probably a bug
report rather than a vulnerability.

## Our commitments

These are the response times we intend to hold, and we would rather publish them
than be vague:

| Severity | Meaning | Acknowledged | Fixed or mitigated |
|---|---|---|---|
| Critical | Tenant escape, forged audit record, arbitrary code execution from untrusted input, loss of accepted events | 1 business day | 7 days |
| High | Privilege escalation, auth bypass, evidence tampering | 1 business day | 14 days |
| Medium | Information disclosure, DoS with a safety consequence | 3 business days | Next release |
| Low | Hardening with no demonstrated consequence | 5 business days | Scheduled |

We will tell you when we believe a report is out of scope, and why, rather than
closing it silently. We credit reporters in the fix unless you ask us not to.

## What we already do

Not a promise, a description of the current state, verifiable in the code:

- Five invariants, listed in [`AGENTS.md`](AGENTS.md), protected by tests. A
  change that weakens one requires an ADR and a test that fails without the
  change.
- The audit ledger is append-only and hash-chained by a database trigger, sealed
  to signed offline checkpoints. Editing a row fails verification.
- Rule bundles, release archives and model bundles are verified before
  execution. The rule language has no shell, SQL, network, dynamic code or
  unbounded regex.
- The deterministic core runs with the AI plane entirely absent. A missing AI
  observation is never treated as a negative observation.
- CI runs on every pull request, with the token scoped to read-only, and every
  third-party action pinned to a commit SHA.

**Known gaps are listed rather than hidden.** See
[`docs/status.md`](docs/status.md) §"What is not done, and why" and
[`docs/roadmap/master-roadmap.md`](docs/roadmap/master-roadmap.md) §"What this
review got wrong". Two of those findings are security-relevant and open: the AI
runtime's bundle check verifies a digest rather than a signature, and the
adapter's known-event-type table is keyed on spellings normalisation never
emits. We would rather you found them from this page than from a customer site.

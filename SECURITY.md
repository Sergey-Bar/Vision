# Security Policy

This product runs on a customer's isolated network, holds camera footage, and
makes decisions about physical security. A vulnerability report is the most
valuable thing anyone can send us, and the handling of one is a promise.

## Reporting a vulnerability

**Do not open a public issue for a security problem.**

This repository is public, so GitHub's **Security Advisory flow and private vulnerability reporting are available**. Use them: go to the repository's **Security → Advisories** tab and draft a report; GitHub will handle disclosure privately and give you a CVE when one is warranted. You do not need to wait for a monitored email address.

If you would rather not use the Advisory flow, open a normal issue and mark it `security-sensitive`; the maintainers will convert it to a private channel and delete the public copy. Say that in the issue body so it is obvious.

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
- **`scripts/verify.ps1` is the gate and it runs locally**, before a commit is
  made. GitHub Actions is not yet wired (no workflow file is committed), so
  nothing automated blocks a merge; the gate is run by a person before committing.

### Outstanding: third-party apps still have read access

**This is the one place where this repository does not currently do what it
says.** Six GitHub Apps are installed and receive the full repository on every
pull request: `claude`, `coderabbitai`, `kilo-code-bot`, `sonarqubecloud`,
`codecov` and `sentry`. They should be removed.

They cannot be removed through the API with the token this project uses, which
returns 403 for the installations endpoints, so it has to be done in the browser:
**repository Settings → Installed GitHub Apps → Configure → Remove** for each.
This is tracked as an open item rather than described as done, because a security
policy that describes an intention as a fact is worse than one that admits a
gap.

The reason to remove them is not that they are malicious. It is that this
codebase is the thing that makes the supply-chain claim, and sending it to be
indexed by a service is the same mistake the product tells its customers not to
make. The cost is real — an independent set of eyes is genuinely useful — and it
is paid knowingly. `golangci-lint`, `go vet`, `gofmt` and the project's own
tests continue to run locally and lose nothing.

**Known gaps are listed rather than hidden.** See
[`docs/status.md`](docs/status.md) §"What is not done, and why" and
[`docs/roadmap/master-roadmap.md`](docs/roadmap/master-roadmap.md) §"What this
review got wrong". Two of those findings are security-relevant and open: the AI
runtime's bundle check verifies a digest rather than a signature, and the
adapter's known-event-type table is keyed on spellings normalisation never
emits. We would rather you found them from this page than from a customer site.

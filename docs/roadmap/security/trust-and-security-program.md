# Trust and Security Program

Owner: E1 (accountable), E2 (implementation). Status: living. Last revised 2026-10-12.

This is the "Trust and Security" parallel track named in
[`master-roadmap.md`](../master-roadmap.md). It is a **continuous staffed obligation, not a project**:
it has no end date and no exit gate, and it is budgeted out of the 40% tax in
[`strategy-and-north-star.md`](../strategy-and-north-star.md) §6 rather than out of milestone new-build.

Read `00`, `01`, [`quality-engineering.md`](../engineering/quality-engineering.md) and
[`release-sre-and-support.md`](../engineering/release-sre-and-support.md)
first. This document does not restate the release gate (`07` §9), the release
train (`12` §1), the disclosure policy (`12` §10.4) or the control-to-test map
(`docs/threat-model.md` §5). It says what the *program* is, who owns each area,
on what cadence, and what we are not doing yet.

**A rule about language in this document.** Every claim about a control is
traceable to a file and, where one exists, a named test. Where I inferred
something, the text says **Inference**. Where something is declared in a schema
or a doc but has no code behind it, the text says **declared, not implemented**,
and that is treated as a finding, not as a control.

---

## 1. Why security is the product

### 1.1 The three buyer questions

`00` §4 states the three questions every buyer asks and our standing answer.
They are security questions, not feature questions, and every one of them is
answered by a *control* rather than by a capability:

| Buyer question | What actually answers it | Where it is real |
|---|---|---|
| "What did the AI decide, and why?" | Every decision is a pure function of stored facts and the stored `AIObservation`; attribution is mandatory | `internal/observations` requires `Model`, `ModelHash`, `RuntimeVersion`, `PromptTemplateID`, `PromptVersion`, `ModelBundleSignature`, and refuses a row missing any of them; `TestAttributionIsMandatory`; `internal/incidents` replay path with no model client |
| "What happens when the AI is down?" | The deterministic core is complete without the AI plane; a missing observation is never a negative | `TestBuiltInBundleRunsWithNoAIObservationAtAll`; `TestHealthExcludesTheAIPlane`; the closed `Degradations` set in `internal/observations`; `TestMissingAIObservationDegradesToUnverified` |
| "Can I prove the system did what it says it did?" | Append-only hash-chained ledger, sealed to signed offline checkpoints, verified offline | `internal/audit` (chain computed by a DB trigger, never by the app); `internal/checkpoint` (Ed25519 seal, trusted-key verification, `Gap` detection); `TestTamperedRowBreaksChainVerification`, `TestVerifyRejectsForeignKey`, `TestGapDetectsRemovedSeal`, `TestRecoveryDrill` |

A buyer who asks those three questions is not running a procurement checklist.
They are asking whether a record they may have to defend in front of a
regulator, a client, or a court is true. **The product's answer is the product.**

### 1.2 The corollary, which is the sentence this program exists to enforce

> **Every feature is a security decision with a UX surface and an audit surface.**

Concretely, in the shape every feature proposal takes from M2 onward:

```
  feature proposal
    ├─ security decision   what can now be observed, inferred, or acted on that
    │                      could not before, and who can do it
    ├─ UX surface          what the operator is shown, and what a person could
    │                      misread (a degraded path rendered as a working one is
    │                      Sev-1 by 07 §10.1, not a wording nit)
    └─ audit surface       the ledger row it writes, or the explicit statement
                           that it writes none
```

The template above is three questions on a one-pager. **A feature that cannot
answer the audit-surface question does not ship**, because a capability that
changes what a site does without leaving a ledger row breaks the promise in
`00` §4's third question. M1's four exit gates in `01` are all instances of it,
which is the evidence that the rule is real rather than aspirational.

### 1.3 The commercial consequence, stated so nobody softens it later

Security posture is the primary sales asset and the primary cost of every
feature. Both halves:

- **Asset.** `00` §2 point 3: buyer scrutiny of AI in physical security arrived
  before the technology matured, and almost no incumbent can answer those three
  questions. A cheaper investigation product loses to free; a provable one does
  not. Every M1–M2 design-partner conversation is a security conversation with
  a feature demo attached.
- **Cost.** Every control in §9 of this document is a test someone has to write,
  a failure path someone has to reason about, an on-call surface at 03:00, and a
  thing that has to keep working when the model is down. `07` §5 alone — the
  `kill -9` programme, 4–5 engineer-weeks — is the single largest unbudgeted
  quality liability in the plan, and it exists *because* of the trust claim.

The corollary is the one that gets forgotten: **the expensive path is also the
one that has to keep working on the cheapest supported hardware, on an isolated
network, with no GPU.** A control that requires a network, a licence check, or
a model is not a control on the customer's site.

---

## 2. Program scope and the two streams

### 2.1 The two streams

```
  TRUST & SECURITY PROGRAM
  │
  ├── (a) DEFENDING THE PRODUCT
  │     supply chain · AI governance · evidence integrity · the invariants
  │     the build, the release, the archive, the bundle, the model bundle
  │
  └── (b) DEFENDING THE CUSTOMER
        identity · evidence · privacy · their data, their site, their operators
        the things a customer would be harmed by if we got them wrong
```

Every item in this document belongs to exactly one stream, and the two have
different failure owners. (a) is ours alone: if a build is compromised or a
bundle is malicious, we are the only party who can fix it. (b) is shared: the
customer's operator clicks things, their administrator configures things, and
some of the harm lands on a person who is not us. That asymmetry is why (b)
gets the disclosure programme in §8 and (a) gets the build programme in §3.

### 2.2 What the pipeline did while it existed — read from the removed `.github/workflows/ci.yml`

One workflow, six jobs, on `push` to `main` and on every `pull_request`.

| Job | What it does | Security value | Verdict |
|---|---|---|---|
| `contract` | installs protoc 29.3 + `protoc-gen-go`, runs `make contract-check`, then `make generate` and fails if `internal/contracts` is dirty | the wire cannot move without the compat gate noticing; generated code cannot drift | **Real control.** The `git diff --quiet` step is the part worth copying into other pipelines |
| `build` | `go build -trimpath ./...` and `go vet ./...` on `ubuntu-latest` **and** `windows-latest` | cross-platform build, `-trimpath` so the binary does not embed the build path | **Real, and under-used.** A `test` job exists for Linux only (below) |
| `lint` | `golangci-lint-action@v6` with `version: latest`, then a `gofmt -l` emptiness check | style and a linter set | **Weak, see gaps** |
| `test` | `go test -p 1 -race -count=1 ./...` against a `postgres:16` service | the full suite, with the race detector, on a real database | **The most valuable job in the file** |
| `rules` | `go run ./cmd/securityctl rules lint --bundle rules/builtin` | the untrusted-input path is exercised on every change, with no Milestone server | **Real control**, and the reason `AGENTS.md`'s "the suite must pass with no Milestone server reachable" is enforceable |
| `secrets` | `git ls-files \| grep -EIn '(BEGIN (RSA \|EC \|OPENSSH )?PRIVATE KEY\|AKIA[0-9A-Z]{16})'` | a build-time backstop against a committed key | **Real but very narrow** — two patterns against `internal/release`'s six |

The `secrets` job's own comment is honest about it: "A build-time guard. The
stronger statement — the release builder refuses to package a secret — is
asserted by the builder's own tests." That is correct and worth keeping in the
same tone.

### 2.3 Gaps found in the removed pipeline, and what to do about each

Every item below was found by reading the file, not by guessing. Priority is by
what it lets through, not by how easy it is.

| # | Gap | Evidence | Consequence | Action | Owner | By |
|---|---|---|---|---|---|---|
| CI-1 | **RESOLVED, then moot.** The workflow pinned `GO_VERSION: "1.24"` with `GOTOOLCHAIN: local` against a `go 1.26.0` module, so every run failed before a test | the removed `ci.yml` vs `go.mod:3` | Fixed to `1.26` while the workflow still existed. The workflow has since been removed for an unrelated billing reason, so no pipeline demonstrates any control either way | Pin one version in three places (`go.mod`, `ci.yml`, `docs/status.md` — which says 1.27) and add a step asserting `go.mod` against the runner's | E2 | M1, before any other CI work |
| CI-2 | **No `permissions:` block** | absent from the file | The job inherits the repository/organisation default `GITHUB_TOKEN` scope. On `pull_request` from a fork it is read-only, but from a same-repo branch it is whatever the org default is, which is frequently write | `permissions: {contents: read}` at the workflow level, then add scopes per job | E2 | M1 |
| CI-3 | **Actions are pinned to tags, not SHAs** | `actions/checkout@v4`, `actions/setup-go@v5`, `golangci/golangci-lint-action@v6` | A tag is mutable. A compromised or force-moved tag is code execution in CI with the token's scopes. This is exactly attacker profile **P4** in `docs/threat-model.md` §3 | Pin all three to full commit SHAs with a `# v4.2.2` comment; bump deliberately | E2 | M1 |
| CI-4 | **A build tool is installed floating** | `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest` (line 30) and `version: latest` for golangci-lint (line 70) | `latest` resolves to whatever exists at run time. The generated code in `internal/contracts` — which `AGENTS.md` says nothing may hand-edit — is produced by a tool whose version is not recorded | Pin both. Record the versions in the release `Provenance` so an auditor can reconstruct which tool wrote the contract | E2 | M1 |
| CI-5 | **A binary is downloaded with no checksum** | `curl -fsSL -o protoc.zip …/v29.3/protoc-29.3-linux-x86_64.zip` then `unzip` (lines 26-28) | A GitHub release asset fetched over TLS with no hash check. A hijacked release URL, a compromised mirror or a MITM against the runner's egress gets code execution in the job that generates code | Verify against the published `sha256` before unzipping, or vendor protoc | E2 | M1 |
| CI-6 | **The test database image is a floating tag** | `image: postgres:16` (line 80) | A new 16.x minor silently changes the database the whole RLS/audit/chain suite runs against. A green run yesterday and a different green run today | Pin the digest | E2 | M1 |
| CI-7 | **No vulnerability scanning of dependencies** | absent | `govulncheck` (which reads the call graph, not just the module list) and a dependency-review action are the two controls a supply-chain programme is expected to have | Add `govulncheck ./...` as a job. Add dependency review on PRs. Both are configuration, not code | E2 | M1 |
| CI-8 | **No SBOM and no provenance are produced in CI** | absent; see §3.2 | The release manifest *declares* an SBOM (`cmd/securityctl/release.go:87`) and `verify-release` refuses an archive that lacks it (`release.go:175-180`), but **nothing in the tree generates one** — `build-release` takes an explicit file list, so the human must supply `sbom.spdx.json`. `SBOM.SHA256` is never populated and `Provenance.SourceDigest` is never populated | Generate SPDX 2.3 JSON in CI with `anchore/sbom-action` (or `syft`), populate `SBOM.SHA256` and `Provenance.SourceDigest` in the builder, and record the tool version | E2 | M1 |
| CI-9 | **No skip detection** | absent | `07` §9.2 requires "**A skipped test is a failure**" and states the specific failure mode: a green CI run where the database suites skipped "looks like a pass, it has asserted nothing". `internal/evidence`'s `TestSymlinkEscapeIsRefused` **skips on Windows** (07 §6.2, Q-6) and would skip silently | A `go test -json` step that fails on any `Action: skip` in the non-Windows jobs | E2 | M1 |
| CI-10 | **The test password is in a committed file** | `SP_TEST_DATABASE_URL: postgres://security:security@localhost:5432/…` (line 92) | It is a throwaway CI password and the risk is low, but `07` §8.4 rule 4 says `SP_TEST_DATABASE_PASSWORD` "never appears in a committed file". As written, a documented rule and the repository disagree, and a rule somebody has already broken once is a rule nobody checks | Either use a GitHub secret, or amend `07` §8.4 to say "a per-run throwaway service password in a workflow is fine; a real credential is not" — and mean it | E2 | M1 |
| CI-11 | **A commit-time password is in the workflow** | `POSTGRES_PASSWORD: security` (line 83) | Same as CI-10 | as CI-10 | E2 | M1 |
| CI-12 | **`gosec` is disabled** | `.golangci.yml:20-23` — `default: standard` with `gosec` in `disable:` and a comment about test helpers | The rationale is about `fmt.Sprintf` in test files, and the config already has a per-path `exclude-rules` block for exactly that. Disabling the linter **globally** to exempt tests means production code is not scanned for the crypto/path/injection patterns gosec is for | Re-enable gosec globally; the existing `_test\.go$` exclude already covers the stated problem | E2 | M1 |
| CI-13 | **No CODEOWNERS** | `.github/` holds PR and issue templates and dependabot; there is no CODEOWNERS and, with two people, there is no second reviewer to own anything | Two people cannot rely on each other noticing a dependency bump. CODEOWNERS is also how a *required* reviewer is expressed in GitHub | Add both. A one-line CODEOWNERS naming E1 and E2 is the whole file | E2 | M1 |
| CI-14 | **No branch protection, no tag signing** | Actions runs on the public repo; `make verify` delegates to `scripts/verify.ps1` from CI. Branch protection is available on the free plan for public repos, but not yet configured | `12` §1.3 says "`main` is releasable. Always." That is a convention until the branch is protected, and a convention is not a control (`07` §2.2 makes exactly this point about `contract-baseline`) | Configure branch protection: required status checks, no force-push, no direct push. Enforce signed annotated tags for releases | E1 | M1 |
| CI-15 | **Tests run on Linux only** | the `test` job is `ubuntu-latest`; only `build` has the OS matrix | Half our target installs are Windows, and the symlink test skips there (Q-6). "A platform we call supported and do not test on is a platform where the evidence store's escape protection is unverified" — `12` §3.6 | Add a `windows-latest` test job, with `SeCreateSymbolicLinkPrivilege` granted to the runner account (07 §6.2 option 1, the cheapest correct fix) | E2 | M1 |
| CI-16 | **No `concurrency` group** | absent | Two pushes to `main` run overlapping suites against the same design-partner-adjacent state. Low impact today, cheap to add | `concurrency: {group: ci-${{ github.ref }}, cancel-in-progress: true}` | E2 | M1 |

**Fourteen of the sixteen are configuration, not code.** That is the right shape
for a two-person team: the supply-chain programme's first tranche is
configuration, not a build system.

### 2.4 What the program spends

Not a new budget line. A slice of the 40% tax, and it is a *named* slice so
that it can be defended in a scope conversation:

| Activity | Steady-state cost | Peak |
|---|---|---|
| Security review of a change touching an invariant | included in review | — |
| Vulnerability scan + triage | 1 day/quarter | 3 days on a `govulncheck` finding |
| Dependency update + verification | 1 day/quarter | 2 days on a major bump |
| Annual external penetration test (M3+), or a scoped review | 0 until M3 | 3–5 days + the fixes |
| Threat model re-run | 1 day/milestone | 3 days at a new trust boundary |
| AI profile re-evaluation after any model/prompt change | 0.5 day/change | 2 days on a model swap |
| Security disclosure response | 0 normally | 5 days, a person, and the Sev-1 protocol |

---

## 3. Supply-chain security

`docs/threat-model.md` §2 B6 names the boundary: "This is the boundary where a
supply-chain attacker gets code execution on every site at once." Everything in
this section is about that sentence. `internal/release`'s package comment makes
the asymmetry explicit: "a builder that produces a wrong archive is a bug, and a
verifier that accepts one is a compromise."

### 3.1 What is real today

Read from `internal/release/release.go` and `cmd/securityctl/release.go`:

| Control | Where | Test |
|---|---|---|
| Every file listed with size + SHA-256; the verifier recomputes both, in both directions | `release.go:227-289`; "a file present but unlisted is a file nobody reviewed" | `TestABuiltArchiveVerifies`, `TestUnlistedFileIsRefused`, `TestMissingFileIsRefused` |
| Path validation on the string, before any filesystem call, and again on the cleaned name | `ValidateFilePath`, `release.go:127-152`; `unsafePath = (^\/)|(\.\.)|(^~)|([A-Za-z]:)(\\)` | `TestValidateFilePathRejectsEveryEscape`, `TestPathTraversalEntryIsRefused` |
| Manifest signed, Ed25519, and verification takes a **trusted** key | `verifySignature`, `release.go:416-438` | `TestForeignSigningKeyIsRefused`, `TestUnsignedManifestIsRefused`, `TestManifestEditBreaksTheSignature` |
| The signing payload excludes the signature and the SBOM, so a re-sign of an unchanged manifest is stable | `SigningPayload`, `release.go:445-456` | `TestSigningPayloadIsUnambiguous` |
| Secret-shaped material refused, and binary files excluded from the scan so the verifier is not a DoS | `ScanForSecrets`, `release.go:161-200` | `TestSecretMaterialIsRefused`, `TestBinaryFilesAreNotScanned` |
| A contract migration bundled with its expand is refused | `release.go:244-252` | `TestContractStepAlongsideExpandIsRefused` |
| The builder takes an **explicit file list**, never a directory sweep | `BuildInput.Files`, `release.go:473-489` | — (a design property, stated in the comment) |
| A fixed tar `mtime` so two builds of one commit differ only in content | `release.go:556` | — (partial reproducibility; see 3.5) |
| The verifier checks the archive's schema window against the local binary's | `cmd/securityctl/release.go:185-198` | — (prevents a half-upgrade) |
| The build refuses to package a path that is not already valid | `release.go:527-530` | `TestPathTraversalEntryIsRefused` |

### 3.2 What is declared but not implemented

Named here so the gap is a tracked item rather than a sentence in a doc.

| # | Declared | Reality | Action | Owner | By |
|---|---|---|---|---|---|
| SC-1 | An SBOM in every release (`12` §1.2, `release.go:295-300`, `bundle-manifest.schema.json` enumerates `spdx-json`/`spdx-tag-value`/`cyclonedx-json`) | **Nothing generates one.** `build-release` hardcodes `SBOM{Path:"sbom.spdx.json", Format:"spdx-json"}` (`cmd/securityctl/release.go:87`) and takes the file from the human's explicit file list | Generate SPDX 2.3 JSON in CI; make `build-release` **require** the path to exist in the file list and populate `SBOM.SHA256` | E2 | M1 |
| SC-2 | `SBOM.SHA256` field exists | Never populated by the builder. And `SigningPayload` zeroes `clone.SBOM` (`release.go:448`), so the *declared path* is outside the signature. The SBOM file's own hash in `Files` **is** inside, so the content is covered; the declaration is not | Populate the hash. Decide (ADR) whether the declaration is signed; the honest answer is that it should be, and that zeroing it was to make re-signing stable | E2 + ADR | M1 |
| SC-3 | `Provenance{Builder, BuildStartedAt, BuildFinishedAt, SourceDigest}` — "so an auditor can answer 'what built this and from what'" (`12` §1.2) | `SourceDigest` is never set by `build-release`; the field ships empty | Compute it from the tree at build time (a `git` write-tree hash, or a hash of the explicit file list plus the tool versions) and record the toolchain | E2 | M1 |
| SC-4 | Trusted-key verification | `verify-release` **works without** `--trusted-key`: it verifies against the key the archive carries and prints a warning (`cmd/securityctl/release.go:158-163`). "Without this check the signature proves only that they signed their own forgery" — the code's own words | The site install path must hard-require it. A warning is not a control on an operator running a command from a USB stick at 03:00 | E2 | M1, with the installer |
| SC-5 | "The release key is not in the repository and not in CI's environment" (`12` §1.3) | `loadSigningKey` (`cmd/securityctl/release.go:271-300`) **silently generates** a new Ed25519 key when the file is absent, writing it 0600 and returning it. A careless `build-release` therefore produces a release signed by a key whose public half exists on that one machine and nowhere else | Refuse to generate in `build-release`; make `securityctl release-key` the only producer, and have `build-release` refuse an absent key with a named reason | E2 | M1 |
| SC-6 | Reproducible release pipeline (`01` §"Trust and Security") | Partial: fixed `mtime`, sorted file list. Not reproducible: `gzip` header carries no name/time here so that part is fine, but `Provenance.BuildFinishedAt` and `CreatedAt` are `time.Now()` and are **inside the signed manifest**, so two builds of the same commit produce different signatures | Either exclude timestamps from the signed payload, or define "reproducible" as "identical file set, hashes and SBOM" and state that in `12` §1.2 rather than implying byte-identical archives | E2 + ADR | M2 |

### 3.3 SLSA provenance — level PROPOSED, and what it actually requires

SLSA's levels are not a badge you apply; each one is a set of machine-checkable
properties. **We target PROPOSED and we are not there.** Named honestly:

| SLSA property | Status | What is required to reach it |
|---|---|---|
| P1 — the build runs in an isolated environment | **No** | Releases are built on a developer's machine (`12` §1.3: "cut from a clean tree on a known machine"). A hosted or ephemeral builder is the requirement |
| P2 — the build is a script, and parameters are explicit | **Partly.** `securityctl build-release --files <spec.json>` is a script with explicit parameters, run by a human | Run it from CI with the parameters declared in the workflow |
| P3 — provenance is generated and signed, unforgeable | **No.** `Provenance` exists as a struct and is inside the signed manifest, but `SourceDigest` is empty (SC-3) and the builder identity is a string we type | Populate `SourceDigest`; record the builder identity and the toolchain; sign the provenance. The manifest signature already covers the struct — the content is the missing part |
| P4 — a two-party review of the change (required for Build L3) | **No** | CODEOWNERS + branch protection (CI-13, CI-14) |

**What SLSA PROPOSED buys a customer, stated carefully:** a signed attestation
that this artefact came from this source, built by this builder, with these
inputs. It does **not** say the build was free of compromise, and it does not
make the code correct. The claim a customer can make with a PROPOSED-level
attestation is narrower than "this is safe" and much more useful than "trust the
vendor": *I can prove which build I am running and where it came from.* That is
the claim this product is selling anyway.

### 3.4 The dependency policy

`go.mod` today is **two direct dependencies and seven indirect**: `pgx/v5`,
`google.golang.org/protobuf`, and from those `pgpassfile`, `pgservicefile`,
`puddle/v2`, `santhosh-tekuri/jsonschema/v6`, `golang.org/x/crypto`,
`golang.org/x/sync`, `golang.org/x/sys`, `golang.org/x/text`, `gopkg.in/yaml.v3`.
That is a deliberately small surface and it is the shape we defend.

**The policy, in force now, derived from `00` §6 ("No new runtime, no new
datastore, no new protocol family without an ADR") and from
`internal/identity/mfa.go`'s own comment ("every dependency on an air-gapped
site is a supply-chain decision somebody has to sign off on"):**

1. **Every new dependency is an ADR.** Context, why the standard library or an
   existing dependency will not do, the transitive set it brings, the licence,
   whether it is vendored, and what breaks if it is removed. The ADR is written
   **before** the line lands, per `AGENTS.md`'s order for invariants.
2. **The test of a dependency: does a six-month-old release still install on an
   isolated network?** A dependency that needs a registry, a proxy, a runtime
   download, or a licence check is declined on that basis alone. `00` §6: "A
   dependency that breaks that is a regression, not a feature."
3. **Version conflicts are a security finding, not a build inconvenience.** With
   nine modules, the risk is not the dependency's count; it is that two of them
   resolving to the same major version of a shared transitive would force
   `go.mod` to hold a version neither chose. The specific known cases in this
   tree: `golang.org/x/crypto` is the transitive source of Argon2id
   (`internal/identity/crypto.go`), and `golang.org/x/text` and
   `golang.org/x/sys` come along with it. **These four move together. An
   `x/crypto` bump is an ADR, not a patch release**, because it is a
   cryptographic primitive path. `internal/identity` refuses a stored Argon2id
   hash whose parameters are weaker than the build's floor
   (`identity.go:101-106`), so a `x/crypto` change that altered encoding would
   lock operators out rather than weaken them — the failure mode is availability
   of accounts, and that is a Sev-2 in `07` §10.1's scale.
4. **A dependency with a licence that forbids redistribution in an offline bundle
   is declined.** `12` §3.1 states this and the SBOM makes it checkable (§3.2,
   SC-1).
5. **Updates are deliberate and bundled, not automatic.** Monthly patch train,
   `govulncheck` output triaged, `go.sum` diff reviewed. No bot merges to `main`
   (`12` §1.3: no commit lands on `main` that does not pass `make ci`).

### 3.5 Reproducibility, precisely

The word means three different things and we should stop using it for all three:

- **Deterministic inputs** — the same tree, the same module versions, the same
  toolchain. True today, modulo the `GO_VERSION` conflict (CI-1) and the floating
  `@latest` (CI-4).
- **Reproducible content** — the same inputs produce the same file set with the
  same hashes. True today, and the fixed `mtime` plus sorted list is why.
- **Byte-identical archive** — the same inputs produce the same bytes. **Not
  true today**, because `CreatedAt` and `BuildFinishedAt` are wall-clock values
  inside the signed manifest, so the manifest differs and therefore the
  signature differs.

The useful version for us is the second one, plus a **content digest over the
signed payload excluding timestamps**, which is a small change to
`SigningPayload` and gives a number two sites can compare: *did you get the same
product I did?* That is a more valuable customer-facing claim than
byte-identity, and it is achievable. ADR, M2.

### 3.6 Verifying a release artefact offline — the property we already have

This is the strongest supply-chain control in the product and it is
**demonstrated, not designed**:

- `securityctl verify-release` takes an archive, a manifest and a **trusted
  public key**, and performs no network operation and needs no database. It
  recomputes every file hash, re-checks every path, checks the signature against
  the trusted key rather than the embedded one, and refuses credential-shaped
  material.
- The workflow is a USB stick: build on our networked machine, `verify-release`
  as a self-check, print the archive SHA-256, carry it, verify at the site with
  a key that arrived on **different media from a different person**
  (`12` §3.2).
- "An air-gapped site whose only copy of the signing key arrives inside the
  archive it is meant to verify is not verifying anything" (`12` §3.2). That is
  the whole threat model of B6 in one sentence, and it is a process control we
  control.

**The one gap:** the site is told to do this and nothing enforces that they do.
`RUNBOOK-*.md` and `docs/OPERATIONS.md` ship in the archive (`12` §1.2), and the
installer must call the verifier with a required key (SC-4).

### 3.7 Untrusted input: three kinds, one policy

`AGENTS.md`: "Rule bundles, release archives and model bundles are untrusted
input. If you are adding a code path that executes anything derived from them,
the threat model owns the answer, not the convenience of the call site."

| Input | What untrusted means here | Verified before execution by | Test |
|---|---|---|---|
| **Rule bundle** | A customer or a third party authors YAML that decides what alarms | JSON Schema → semantic validator → AST. **No shell, SQL, network, dynamic code or regular expression anywhere in the language.** Stored whole, never edited; activation is a pointer move behind a reviewed simulate-diff digest | `TestNoUnboundedRegexOperatorExists`, `TestFieldValueListIsBounded`, `TestConsumingAnAIObservationRequiresAnExplicitDegradation`, `TestActivationRequiresAReviewedDiffDigest`, `TestStoreDoesNotActivate` |
| **Release archive** | Whoever produced the tarball | Signature under a trusted key, full file list with recomputed hashes, path validation, secret scan, no bundled contract migration | §3.1's table |
| **Model bundle** | A set of model weights plus a runtime, on a site disk | **Designed, not built.** `12` §3.4 specifies signature → file list → hardware profile match → permitted-use check → extract to a versioned directory → **do not activate**. The observation record already carries `ModelBundleSignature` as mandatory attribution | The observation-side half is tested (`TestAttributionIsMandatory`, `TestAIRuntimeCannotAlterAuthoritativeState`). The install path does not exist |

**The rule-language containment is the strongest single control in the product
and it is structural, not procedural.** `07` §7.3 notes the deeper version of
this: `internal/rules`'s `AIObservation` type "has no free-text field at all",
so *a rule cannot match model prose* — not because a review rule forbids it, but
because the type does not permit it. That is the shape to copy.

**The model bundle is the open item.** `12` §3.4 is a specification; M1 ships
the Python VLM runtime as a *stated non-goal* (`docs/status.md`: "The runtime
itself is a Python service and is not written"). So today there is no
model-bundle attack surface, and the moment the runtime exists there is one. The
programme item is: the installer for model bundles is a **milestone gate on the
same date as the runtime**, not a follow-up.

---

## 4. AI governance

### 4.1 The required record, per model and per profile

This is the artefact a customer, an auditor and our own M2/M3 gates all read, and
it is the same artefact. **The record that must exist for every model and every
observation profile:**

| Field | Why it is in the record | Exists today? |
|---|---|---|
| **Model identity and version** | a decision reproduced under a different model is not a reproduction | ✅ `Observation.Model` + `ModelHash`, both mandatory (`observations.go:116-117`, `Validate` refuses empty) |
| **Model hash** | the name is a claim; the hash is the artefact | ✅ `ModelHash` |
| **Runtime version** | the runtime is part of the system that produced the answer | ✅ `RuntimeVersion` |
| **Prompt template id and version** | a prompt edit changes behaviour; `07` §4.2 is the whole argument | ✅ `PromptTemplateID`, `PromptVersion` |
| **Model bundle signature** | proves which bundle, under whose key | ✅ `ModelBundleSignature`, mandatory |
| **Output schema version** | the structured fields a rule may read changed when this changed | ✅ `OutputSchemaVersion` |
| **Input evidence hashes** | ties the answer to the exact bytes it saw | ✅ `InputEvidenceIDs` + `InputEvidenceHashes`; `InputHash()` |
| **Output hash** | recomputable later, which is what makes the stored answer a record rather than a claim | ✅ `OutputHash()`; `TestOutputHashIsStableAndRecomputable` |
| **Declared profile fields** | a rule may only name a field the profile emits; checked at bundle compile time | ✅ `Profile.Fields` |
| **Shadow / production status** | a shadow profile has no authoritative effect | ✅ `Profile.Shadow`, `IsProductionProfile` |
| **Evaluation: labelled cases, FPR, recall, latency, disagreement** | the promotion decision's evidence | ⚠️ **the harness exists (`internal/evaluation`), the record does not.** There is no table, no registry and no persisted `Report`; `Report` is a struct returned by `Evaluate` and, in a build with no Python runtime, nothing calls it |
| **Approved use cases** | "this profile is for a restricted zone on foot traffic after hours" is a narrower and safer claim than "this profile detects intrusion" | ❌ **not modelled anywhere** |
| **Known limitations** | lighting, occlusion, weather, camera height, adversarial dress | ❌ **not modelled** |
| **Deployment history** | which sites, which version, from when, promoted by whom | ❌ **not modelled** |

**The first five rows are implemented and enforced. The last four are the actual
governance gap**, and they are the four a regulator, a customer's risk committee
and the M8 model marketplace all require. Building them is M1 work for the
schema, M2 for the registry, and the "approved use cases" field is the one that
must not be free text: it should be a closed vocabulary of incident classes,
because a free-text use-case field becomes a marketing sentence.

### 4.2 The promotion gate, with shadow mode

The sequence is fixed and none of it is skippable. From `07` §7.4 and
[`master-roadmap.md`](../master-roadmap.md) M1 gate 4:

```
1  declared     profile spec exists, output schema committed, model and prompt
                pinned, degradation behaviour declared        [M1, ✅ exists]
2  evaluated    offline against a labelled dataset; gates run
                individually, every failure named             [harness ✅,
                                                                 run ❌]
3  shadow       runs on live traffic, produces observations, affects nothing,
                marked shadow everywhere it is displayed       [M1, ✅
                                                                 TestShadowProfilesCannotAffectAuthoritativeEffect,
                                                                 TestAIRuntimeCannotAlterAuthoritativeState]
4  14 days      at a design-partner site, evaluation report attached
                                                            [M1 gate 4 — unsignable
                                                             today: no Python runtime]
5  human        promotion decision, named and dated            [process, E1]
6  production
```

**Two structural properties that are better than a procedure:**

- `TestShadowProfilesCannotAffectAuthoritativeState` and
  `schemalaws.TestAIRuntimeCannotAlterAuthoritativeState` are a **grant-level**
  control. "The AI plane's role cannot write authoritative state at all" is not
  a code path somebody forgot to guard; it is a database permission. This is
  stronger than any prompt or policy.
- `Report.Promotable` says who is **eligible**; it never says who was
  **promoted**, and no code path promotes. The promotion decision is a human
  signature with a name and a date on it.

**The gates, verbatim from `internal/evaluation.DefaultGates()`:** FPR ≤ 0.20,
recall ≥ 0.70 on true positives, median latency ≤ 8000 ms, disagreement ≤ 0.25,
labelled cases ≥ 200 (`ShadowGates` = the same with 10 cases, because shadow is
for collecting cases). Every gate is checked independently and every failure is
named — `evaluateGates` returns `[]string` and `TestEveryFailingGateIsNamed`
asserts it. **A profile that is precise and slow has three failures, not a score
of half-passed**, because averaging gates is how a profile nobody should ship
gets promoted.

### 4.3 Model change management

`07` §7.5, and the contract is stated once because it is the rule most likely to
be broken under pressure:

> **Any change to a model, a prompt template, a prompt version, a profile schema
> or a profile's dataset forces step 2 and step 5. There is no diff size below
> which this is skipped, and no emergency release that bypasses it.**

1. `make ci` in full. **A model change must not change a single golden byte
   outside the observation fixture.** If it does, something is inferring at
   decision time and invariant 2 is broken.
2. The profile's dataset evaluation, gates run individually.
3. **Side by side on the same dataset, old vs new, per case.** A version that
   wins on aggregate and loses on a class of case the old one handled is a
   regression with a better average. This is the check that catches the
   replacement that improves the average and loses the thing that matters.
4. Historical site-data replay for the affected profiles — the disagreement rate
   on *this* site's traffic.
5. Shadow for 14 days, as for a new profile.
6. `TestOutputHashIsStableAndRecomputable` re-run against **already-stored**
   observations. "If a model change makes a stored `output_hash` unverifiable,
   invariant 2 is falsified and the change does not ship."

### 4.4 The registry fields, and where they live

A `model_profiles` registry table (expand migration, forward-only, no contract
change needed because it is server-side state, not a wire contract — confirm
with `make contract-check`):

```
  profile_id           text        the id rules name
  version              int         the profile schema version
  shadow               bool        never affects authoritative state
  fields               jsonb       the declared output field set
  model_ref            text        registry entry, see below
  prompt_template_id   text
  prompt_version       text
  output_schema_version int
  approved_use_cases   text[]      CLOSED VOCABULARY, not free text
  known_limitations    text[]      each with a mitigation or a "none known"
  evaluation_report_ref text       the Report serialised, hashed, stored
  last_evaluated_at    timestamptz
  promoted_by          text        a person, not a role
  promoted_at          timestamptz
  superseded_by        text
  deployment_sites     text[]      which sites ran which version, from when
```

Plus a `models` registry: `model_id`, `version`, `content_hash`, `bundle_signature`,
`hardware_profile`, `provenance` (where the weights came from, under what
licence, with what evaluation), `retired_at`. `12` §3.4's "content-addressed and
never overwritten, so a site's observations remain attributable to a model
version that still exists on disk and can still be verified offline" is the
retention rule: **a bundle with a live citation is never swept**, exactly as
`TestSweepRespectsRetentionAndLegalHold` covers evidence.

### 4.5 The Python runtime, honestly

`docs/status.md`: "The Go side is complete … The runtime itself is a Python
service and is not written. It is the one substantial piece of Track F
outstanding."

So today: the promotion harness exists and has never been run on a real profile.
M1 exit gate 4 ("A production VLM profile has passed its promotion gates on a
real GPU, in shadow mode, for at least 14 consecutive days") is **unsignable**.
`01`'s reduced scope explicitly does not make it skippable: "Synthetic fixtures
do not make the VLM gate skippable … if it cannot be met, gate 4 is not met and
M1 has not finished. That is uncomfortable and it is correct."

**The security consequence of the runtime being Python, stated once:** it is the
only component in the product that is not written in the language whose
dependency tree we control, it is the only one with a GPU driver and a native
library stack, and it is the one whose failure must never reach the
availability path. Three requirements follow, and they are the security scope
for the runtime when it is written:

1. Its database role can insert `ai_observations` and read `evidence` and
   **nothing else** — a grant-level control that already exists
   (`schemalaws.TestAIRuntimeCannotAlterAuthoritativeState`).
2. It runs as a **separate OS identity** on a separate host (`12` §2.1), so a
   compromise of the Python process is not a compromise of the service account.
3. Its dependencies are pinned, vendored **into a signed model bundle**, and
   covered by the SBOM (SC-1). A runtime that pip-installs at start is a
   runtime an air-gapped site cannot run, and a supply-chain hole at every site.

---

## 5. Privacy and data protection

### 5.1 The classification of our own data

Stated as a table because "we do not store personal data" is not a position
anybody can check, and this one is checkable.

| Data we hold | Classification | Lawful basis a customer relies on | Retention default | What we must never do |
|---|---|---|---|---|
| Canonical events, incident rows, dispositions, audit ledger | **Personal data** — an event names a device, a time and a place, and a person is the subject | customer's legitimate interest (Art. 6(1)(f) GDPR) or their employer's policy | site-configurable; ledger retention is a **legal** matter, not a log policy | edit history; delete a ledger row; sweep a legal hold |
| Evidence bytes: snapshots and clips | **Personal data**, and **biometric data** whenever a face is present | as above, and the customer's own lawful basis for processing video | `evidence.retention_days`, swept by operator action with a dry run | log it; put it in a golden test; put it in the repository; commit it to CI |
| AI observations | **Derived personal data** if the input is | inherits the input's basis; the model creates nothing new | with the incident | treat prose as authoritative; use it to match on wording |
| Operator identity: username, display name, IP, user agent | **Personal data** | legitimate interest; employment | sessions purged on expiry; the ledger keeps auth events | store a session token in the clear (we do not — `HashToken`) |
| Model bundle, rule bundle, release manifest | not personal data | — | with the release | — |
| Metrics | not personal data; **label cardinality is capped at 64** because a device id from an untrusted source is a way to allocate without bound (`TestLabelCardinalityIsCapped`) | — | 30 days operational (`12` §4.3) | add a per-person label |
| Log lines | not personal data **until** they carry `tenant_id`/`site_id`, which `AGENTS.md` requires | — | 30 days | a secret, a token, a credential, raw biometrics |

### 5.2 The rules, and where they are enforced

| Rule | Enforcement today | Test |
|---|---|---|
| **No secrets, tokens, credentials or raw biometrics in logs** (`AGENTS.md`) | review, plus `Config.DSN()` omitting the password and documented safe to log, plus the release builder's secret scan, plus the site-config schema having no field that can carry a secret | `TestSecretMaterialIsRefused`, `TestSiteConfigSchemaCannotCarryASecret` |
| **No real biometric data in the repository or in CI** (`07` §8.4 rule 1) | process. "The test is not 'is it identifying' — it is 'would a privacy officer need to know it exists', and for a face crop the answer is always yes" | the sanitiser's own tests, not a check on the whole tree — see below |
| One choke point for live data entering the repository | `internal/fixture.SanitizeResponse` is the **only** path; `07` §8.1: "a second path into the repository is a second place to forget, and 'we were careful' is not a control" | `TestRedactsSecretsByKeyName`, `TestRedactsIdentifiers`, `TestRedactsURLs`, `TestRedactsOpaqueLongStrings` |
| Capture → sanitise → human review → record → commit, never capture → commit → sanitise | process, `07` §8.3 | — |
| Retention sweep **fails closed** on an unevaluable precondition | `Store.Sweep`: a `SkipLegalHold` error records the error and skips that object — it does not delete | `TestSweepFailsClosedWhenLegalHoldCannotBeChecked` |
| A sweep is an operator action with a dry run, not a background timer | `Sweep(…, dryRun bool)`; the comment: "deleting evidence on a schedule nobody is watching is how a site discovers its retention policy was wrong after the fact" | — |

**The gap.** The sanitiser protects the *fixture* path. Nothing protects the
*operating* path: a snapshot fetched from a live site into the evidence store is
never run through it, correctly, but there is no automated check that evidence
bytes, observation prose or log lines have not acquired a face. That check is
not something we can automate, and pretending otherwise is a false assurance.
The programme item is a **quarterly manual audit** of a sample of live evidence
objects and observation rows against §5.1's table, by the person who is not the
one who wrote the code, recorded in `docs/` with a date. Named in §6.2's
definition of done.

### 5.3 Privacy zones, anonymisation, selective persistence, regional policy

`01` M6 introduces zones and site intelligence. These four have to be designed
in, not added:

- **Privacy zones.** A zone an operator may not query evidence from, enforced at
  the *capability* layer (`identity.CapEvidenceRead` plus a zone attribute), not
  by hiding a row in the UI. "A surface reads data its caller is not authorised
  to read" is already a real defect in this tree (Q-5: the console's dead-letter
  table uses `requirePrincipal` where the API uses `requireCapability`), so the
  pattern to avoid is established.
- **Anonymisation.** Irreversible, and **recorded**. `07` §8.4 rule 2:
  evaluation datasets are built from synthetic renders, licensed stock footage
  with a recorded licence, or real frames through an irreversible step —
  "the anonymisation is recorded in the dataset's provenance file, because an
  unrecorded anonymisation is an assertion rather than a control."
- **Selective persistence.** The strongest version of this is already the
  architecture: **the video is not ours.** VISION does not sit in the video path
  (`12` §8.7: "It does not hold the cameras, it does not hold the door locks, it
  does not record"). What we persist is a bounded set of snapshots and clips
  cited by incidents, with a cap (`evidence.DefaultMaxBytes`, 512 MiB) and a
  content-addressed store. The M3/M7 feature that breaks this is continuous
  recording, and it is an anti-feature under the current thesis.
- **Regional policy.** A customer's operators are in a jurisdiction; a site's
  events are about people in a jurisdiction; and in some of them the answer to
  "who may see this" is not "who has the capability". The M1 answer is that the
  capability set and the site scope are data (`identity.roleCapabilities` is a
  map, and the database holds the same data), so a regional pack is a data
  change. That is the right design and it needs the use-case vocabulary from
  §4.1 to be enforceable.

### 5.4 Data residency, sovereignty and the open questions

`12` §3.1 is the constraint: no runtime component fetches anything at run time —
no CDN, no licence check, no telemetry, no crash reporter, no update check, no
revocation check that needs the internet. "A product that phones home is a
product an air-gapped site cannot run, and an air-gapped site is a large part of
the market."

**This is our strongest compliance answer and it is a real one**: video,
evidence, the audit ledger and the checkpoints never leave the customer's
network unless the customer moves them. §4 of
[`compliance-and-certifications.md`](compliance-and-certifications.md)
turns this into a sales argument and lists the four places where it is currently
undermined by an open question — telemetry, support bundles, licence validation
and model downloads. **None of the four has a decision date, and that is the
single most actionable item in this section.**

### 5.5 The DPIA-lite, per milestone

EU GDPR Art. 35 requires a DPIA where processing is "likely to result in a high
risk". A video-surveillance intelligence layer on a customer's staff and
visitors is a plausible trigger, and a two-person company cannot afford a
per-request DPIA cycle. So: a **one-page DPIA-lite per milestone**, owned by E1,
reviewed with the design partner, kept in `docs/dpia/`. Ten questions, no more:

```
DPIA-lite  ·  milestone ___  ·  date ___  ·  owner E1

1  NEW DATA. What new categories of data does this milestone persist that
   M1 did not? (List the tables, not the concepts.)
2  NEW INFERENCE. What can the system now say about a person that it could
   not say before? One sentence per profile.
3  NEW AUTOMATION. What can it now DO that changes a person's physical
   situation? (M3 safe actions, M7 autonomy.)
4  NEW RETENTION. What new stored artefact gets a retention default?
5  NEW ROLE. Who can now read or export something they could not before?
6  NEW LEAVING-SITE FLOW. Does any of it leave the customer's network? If so,
   where, under what agreement, with what consent?
7  NEW IDENTIFIERS. Does it create or store a new identifier for a person?
   (M1: no person model — schemalaws.TestNoEntityAliasTableInM1.)
8  SPECIAL-CATEGORY RISK. Can any output become biometric data — a face crop,
   a plate, a gait sequence, a depth frame? Name the path.
9  NEW SUB-PROCESSORS. Does this add anyone outside the two of us and the
   customer who touches the data? (A model vendor's weights are not data; a
   model vendor's *API* is.)
10 HUMAN OVERSIGHT. For every new automated decision: who or what can stop
   it, and what does the operator see that lets them know it ran?

CARRY-OVER FROM LAST MILESTONE: which of the above did we get wrong last time,
and is it fixed?
```

**The rule that makes the DPIA-lite real:** answer 7 and 8 with the schema, not
with intent. If a milestone's diff adds a table or a column that could hold a
face crop, the answer is "yes" and the milestone needs a design conversation
before the migration, not a note afterwards. `internal/schemalaws` is the place
where that becomes mechanical: `TestNoEntityAliasTableInM1` is a test about the
*absence* of a capability, and `AGENTS.md`'s path-traversal rule
("needs an explicit negative test … on every change") is exactly the same shape
applied to privacy. **The DPIA's question 7 wants to become a schema-law test.**

### 5.6 The per-milestone data-flow delta question

One question, asked at every milestone review, in these words:

> **"Compared with the last milestone, what new data crosses a trust boundary
> that did not cross one before — and who is it crossing to?"**

It is the question that catches the drift. `07` §8.4's rule 1 exists because a
face crop is biometric data "under every regulation this customer base operates
in", and the way a company drifts into biometric processing is never a decision;
it is a feature that stores one more frame. The delta question is cheap, it is
asked at a gate that already happens, and it fails loudly on the answer nobody
wanted to give.

---

## 6. Cybersecurity hardening programme

### 6.1 The eight areas, the owners, the cadence

One named owner per area. With two people, "the team" is not an owner.

| # | Area | Owner | Cadence | What "hardened" means, specifically | State today |
|---|---|---|---|---|---|
| H1 | **Adapters** (Milestone, and every adapter after M4) | E2 | quarterly + on any new adapter | Endpoint list with a `Unverified` flag that shrinks only against a real server; one parsing path shared with the replay transport; every route assumption in one file; hostile-input handling that assumes a compromised device | One adapter, one parsing path, **every route `Unverified`** and no Milestone server ever reachable (`docs/status.md`). `securityctl adapter probe` is referenced five times and does not exist (Q-4) |
| H2 | **Auth and session management** | E2 | quarterly | Argon2id with build-held parameters and a floor; MFA mandatory with one audited exception; hashed session tokens rotated on every use; session-bound CSRF; step-up for sensitive dispositions; exponential lockout; **per-source rate limiting** | Argon2id ✅ (`identity.go:32-37`, floor enforced at `:101-106`), mandatory MFA ✅ with the bootstrap exception audited ✅, hashed+rotated tokens ✅, CSRF ✅ **in `internal/identity` and unreachable from the console**, step-up ✅ single-use ✅, exponential lockout ✅ (`TestLockoutIsExponentialAndCapped`). **Per-source rate limiting: declared, not implemented** — see below |
| H3 | **Secrets** | E1 | quarterly + per release | Secrets not in config, not in env, not in logs, not in the archive, not in CI. Presence reported, value never. `SecretStore` interface so no component holds a key it does not need | `Config.DSN()` omits the password and is documented safe to log; `Open(ctx, cfg, password)` takes it out of band; `identity.SecretStore` is an interface with "no implementation here that holds a key"; the release builder refuses credential-shaped material; the site-config schema cannot carry a secret. **CI embeds a test password** (CI-10) |
| H4 | **Certificates and transport** | E2 | quarterly + per release | TLS 1.2 minimum with no plaintext fallback unless a proxy is declared; HSTS; no certificate fetched or validated over the network at run time; **customer-held keys for an air-gapped install** | `internal/api/server.go:371-372` `MinVersion: tls.VersionTLS12`; HSTS `max-age=31536000; includeSubDomains`; `listen.behind_proxy` is the only plaintext path. **No mTLS to Postgres, no client certificate support in the adapter, no certificate rotation story** — all open |
| H5 | **APIs and the console** | E2 | quarterly | Strict CSP with no inline script and no `unsafe-*`; `X-Frame-Options: DENY`; `nosniff`; `Referrer-Policy: no-referrer`; cookies `HttpOnly`+`Secure`+`SameSite=Strict`; body size cap; JSON never content-negotiated into HTML; **every surface enforces a capability, not merely a principal** | Headers ✅, CSP ✅, body cap ✅, no negotiation ✅, capabilities ✅ — `07` §2.7's tests. **No cookies, because there is no session**: the console has no login, no cookie and no token, so a cookie attribute is not a hardening item here but an absent surface. **Q-5 is closed** — the dead-letter and clock-drift tables are capability-gated, not principal-gated. **Q-9 is closed** — the access log carries `tenant_id`, `site_id` and `request_id`, and the site is resolved from the principal's scope rather than read out of a request header. No content assertions (§2.7's "a template that renders an empty table for every incident passes all twelve") |
| H6 | **Evidence and the filesystem** | E2 | quarterly + on every path change | Four path negatives on every evidence path on every change; content addressing; hash verified on read, mismatch reported not served; symlink resolution after each ancestor; cap per object; staging is not the root | Three of four negatives ✅. **`TestSymlinkEscapeIsRefused` skips on Windows** (Q-6) and Windows is a supported platform per `12` §3.6 — the cleanest example of a supported platform that is not tested on |
| H7 | **Installers and update packages** | E2 | per release | Verify before write; extract to a versioned directory; one "current" pointer as the commit point; refuse an out-of-window archive; refuse to proceed without a verified backup; rollback is a pointer flip and is audited; the site verifies the signature locally even when a fleet server offers the version | Builder and verifier ✅. **`install`/`upgrade`/`rollback`/`backup`/`restore` are skeletons that refuse with a named reason** (`docs/status.md`). Specified in `12` §2.3, not built |
| H8 | **Model and rule bundle installation** | E2 | per milestone | §3.7's table | Rule bundles ✅ (store ≠ activate, reviewed diff digest, immutable, rollback). **Model bundles: specified only** |

### 6.2 The definition of done for a hardening item

An item is not done because it was changed. It is done when **all six** are
true, and this is the definition we copy into every ticket that touches a
control:

```
□  1  There is a test that FAILS when this control is removed, and one of us
       has run it and seen it red. Not a claim in the PR description — run.
       (07 §9.2: "a test that has never been seen to fail is not a test.")
□  2  If the control touches an evidence path, the four path-traversal
       negatives are re-read and re-run in the SAME diff, and the ticket names
       which function is the evidence path. (AGENTS.md; 07 §6.4's review rule.)
□  3  If it is a durability change, the kill -9 fault-injection test at the
       stage it touches exists and passes. Not a crash-equivalent substitute.
       (AGENTS.md; 07 §5.)
□  4  docs/threat-model.md §5 is updated if the control set changed: the new
       control, and the named test that fails without it. A control with no
       failing test is a claim.
□  5  The release notes state it, and state its *absence* too if the control
       is not fully in place. (07 §9.3: a known limitation is not a bug and
       does not block a release, but it must be written down.)
□  6  The residual risk is either eliminated or written into §9.2's accepted
       risks with a named owner and a review date.
```

**And one rule for the items that turn out to be a lie in a document.** Three
exist today, all found by reading the tree rather than the docs:

| Finding | Where | Fix |
|---|---|---|
| Per-source login rate limiting is claimed in the threat model (A3.5), in the OpenAPI description, and as a config field `loginRateLimitPerMinute` (default 5) in `site-config.schema.json` — and **there is no implementation**. Only exponential lockout exists. | `docs/threat-model.md` §4 B3; `contracts/openapi/v1/openapi.yaml:42`; `contracts/json-schema/site-config.schema.json:115`; `internal/identity/identity.go:156-176` (lockout only) | Implement it, or amend the three documents. **Implementing is the right answer**: the field already exists in the contract, a site may already be configured against it, and "rate limited per source" is the published description of a control that stops credential stuffing — attacker profile P1, "the most likely profile by a wide margin" |
| The WebAuthn assertion **signature is not verified on the path that authenticates.** `verifyWebAuthnAssertion` decodes, checks the sign counter, and returns `true`; its comment attributes the signature check to "the platform's WebAuthn verifier, which holds the algorithm registry" | `internal/identity/mfa.go:212-239`, reached from `verifyFactor` at `:93` | **Note the current state, because it is mid-change and easy to misread: a real verifier now exists in `internal/identity/webauthn.go` — `VerifyAssertion` and `verifyAssertionSignature`, with COSE key validation, ECDSA/EdDSA support and a real counter check. `verifyFactor` does not call it.** So the remaining work is the wiring plus removing the stub, not the implementation. `docs/status.md` is accurate ("the registration ceremony is missing") and the code comment in `mfa.go` is not. The gate: **either `verifyFactor` verifies the signature, or no build can enrol a WebAuthn credential** |
| The TOTP code uses HMAC-SHA256 (`mfa.go:194`) while the otpauth URI declares `algorithm=SHA1` (`mfa.go:141`). A standard authenticator produces SHA-1 codes that will not verify | `internal/identity/mfa.go` | Pick one. The standard is SHA-1 for 6-digit TOTP; matching authenticators is the requirement, and `07` §10.1 makes an unloginable account a Sev-2 |

**These three are the reason §6.2 exists.** A control documented in three
places and absent from the code is worse than a control nobody mentioned,
because a customer who read it will believe it.

---

## 7. Evidence integrity and media provenance

### 7.1 What we do today — verified in code

| Property | Implementation | Test |
|---|---|---|
| **Content-addressed evidence.** The reference is derived from the hash, not supplied: `<root>/<tenant>/<xx>/<sha256>`. "The reference is generated from the hash rather than taken from the caller, which removes a whole class of question: there is no path for a caller to supply" | `internal/evidence/store.go:216-283` | `TestPutAndOpenRoundTrip`, `TestIdenticalBytesDeduplicateAndCannotCollide` (two tenants' identical bytes must not collide — deduplication must not become a disclosure channel) |
| **A tenant id is one path segment, not a path.** A tenant of `a/b` would nest inside another tenant's tree without leaving the root: "it would not be a traversal and it would still be wrong" | `store.go:217-229` | `TestPutRefusesAnUnusableTenant` |
| **Path validation** — relative, forward-slashed, no `.`/`..`, no drive letter, no UNC, no NUL, no empty segment, ≤512 chars, checked on the string *and* after `filepath.Clean` | `ValidateRef`, `store.go:115-151` | `TestValidateRefRejectsEveryTraversalForm`, `schemalaws.TestEvidenceStorageRefIsAlwaysRelative` |
| **Symlink resolution on every ancestor**, not just the final path | `resolve`, `store.go:160-200`; "checking only the final path would miss an intermediate link" | `TestSymlinkEscapeIsRefused` — **⚠️ skips on Windows** |
| **A changed file is reported, not served.** "An operator looking at evidence that cannot be proven to be the evidence an incident cited is worse off than being told the file is wrong" | `Open`, `store.go:291-330`; `ErrHashMismatch` | `TestAlteredFileIsReportedNotServed` |
| **A crash cannot leave a truncated object that looks like evidence.** Write to `.incoming-*` in the root, hash while copying, `fsync`, then `os.Rename` (atomic) | `store.go:230-281` | `TestStagingFilesAreNotEvidence` |
| **A size cap** so a source offering an unbounded "clip" cannot fill the disk during an incident | `DefaultMaxBytes` = 512 MiB | `TestPutIsBounded` |
| **Retention fails closed**, and never removes an object of unknown age | `Sweep`, `store.go:366-446` | `TestSweepFailsClosedWhenLegalHoldCannotBeChecked`, `TestSweepRespectsRetentionAndLegalHold` |
| **Hash-chained, append-only, trigger-computed ledger** | `internal/audit/audit.go` — the package *never computes a chain hash itself*; `audit_verify_chain($1,$2)` does, in the database, over a length-prefixed encoding | `TestTamperedRowBreaksChainVerification`, `TestAuditLedgerIsAppendOnly`, `TestAuditChainChainsFromGenesis`, `TestAuditChainRejectsGap` |
| **A tamper is detectable even when the storage-layer guard is defeated** | threat model A7.1, and the test "does exactly that, because that is the realistic attacker" | `schemalaws.TestTamperedRowBreaksChainVerification` |
| **Signed offline checkpoints**, verified against a **trusted** key, offline, with no database | `internal/checkpoint`; `VerifyAgainstSeals` is the whole offline audit path | `TestVerifyRejectsForeignKey`, `TestGapDetectsRemovedSeal`, `TestSealerRefusesToSealABrokenChain`, `TestFileSinkRefusesUnsignedAndUnsafe` |
| **The sealer verifies first and seals second, and never seals what did not verify.** A seal over an unverified chain "asserts that somebody signed a chain they had not read, which is worse than no seal at all: it looks like evidence" | `Sealer.SealRange`, `sealer.go:48-126` | `TestSealerRefusesToSealABrokenChain` |
| **A self-check before the write**: "a bug in this package must not produce a seal that its own verifier rejects" | `sealer.go:105-107` | — |
| **A cryptographically unambiguous signature payload** — length-prefixed fields, so a signature cannot be moved between seals | `signingPayload`, `checkpoint.go:171-195` | `TestSigningPayloadIsUnambiguous` |
| **The whole set survives a database loss, and says so** | `internal/dr.TestRecoveryDrill`; a restored ledger is **provably behind** the seal, and the drill *fails if it is not behind* | `TestRecoveryDrill` |

That is a genuinely strong list. The M1 recovery drill is the most impressive
artefact in the repository: it asserts the finding rather than the absence of
one, so it cannot quietly stop being true.

### 7.2 What we do not do: we do not sign the media

**State it plainly, because a customer will ask and the answer must be exact.**

> We integrity-protect the **record** of the media, not the **media** itself.
> Every evidence object has a SHA-256 in the database, the object is
> content-addressed, the row is hash-chained into the audit ledger, and the
> ledger is sealed to a signed offline checkpoint. So: if a stored file changes,
> we detect it and report it rather than serving it. **What we do not do is
> prove that the bytes in our store came from the camera** — that they are the
> output of that device's lens at that timestamp and not a plausible
> substitute. We record what the source *asserted* (`observed_at` is "what the
> source claimed", per `AGENTS.md`), and `provenance.asserted_by` names the
> device. A compromised device can therefore assert an event it did not observe,
> and a LAN attacker (`docs/threat-model.md` P3, P6) can substitute bytes at the
> VMS.

**What the gap actually costs, in the buyer's terms:** in a dispute about
whether a person was in a restricted zone, we can prove *our record was not
altered*. We cannot prove *the camera was not*. The corroboration rules are
what cover the second question in M1 — an incident needs two independent
signals, provenance names the asserting device, and a single source cannot
corroborate itself — so the residual risk is bounded. It is not zero, and
"a camera administrator can produce any frame they like" is a sentence a
customer's lawyer will find for us.

**A second, adjacent gap, stated for the same reason:** we do not detect a
camera that has been physically moved. `12` §8.5 says so: the platform "cannot
detect on its own until M6's drift detection ships", and the honest instruction
to an operator is to re-map the camera and *review the last few hours of
incidents from it, because they were verified against a graph that was already
wrong*. That is a real accuracy gap in the M1 product and it is in the release
notes rather than hidden.

### 7.3 The forward path: adopt, do not build

`01` §"Trust and Security" states the position: "adopt stronger media
provenance as the ecosystem matures, including ONVIF's media-signing direction
and its tightening cybersecurity requirements, rather than building our own."

**That is the right call and the reasoning is worth recording, because it will
be re-litigated:** a proprietary signing scheme we invented is a scheme nobody
else verifies, a scheme with one implementation (ours), and a scheme that
becomes a liability the moment a customer's other tooling needs to check a
frame. An ONVIF-aligned approach means the camera signs, or the ecosystem's
mechanism is the one we consume, and our verification code is written against a
published specification with other implementations to test it.

**The programme, quarterly, E1 accountable:**

| # | What we watch | What changes when it matures | Decision |
|---|---|---|---|
| MP-1 | ONVIF media-signing / stream-signing work, and whether any major VMS implements it | Consume the signature; verify at ingest; record the signature in the observation's attribution and in the evidence row | Adopt when **two** vendors ship it |
| MP-2 | ONVIF cybersecurity requirements and conformance levels (S1/S2/S3/G1…) | A site can ask "is your adapter conformance-tested"; the certification suite (`01` M4) can carry a conformance clause | Adopt at M4 with the SDK |
| MP-3 | Whether the VMS vendors expose a signed-still or signed-clip API | Evidence objects gain a source signature alongside our SHA-256 | Adopt per vendor, opportunistically |
| MP-4 | Whether the *customer's* compliance regime demands it (NIS2, a public-sector tender) | A specific deployment needs it even if the ecosystem is thin | Sell it as an option, scoped, priced — do not block the product on it |
| MP-5 | C2PA / content credentials for image provenance | Relevant to *our* synthetic evaluation data, not to a customer's CCTV. Use it to record how a test image was produced | Adopt for dataset provenance at M2 |

**The review, quarterly, one page, in this document's own §7.3 table:** what
changed, what it would cost, what it would break, and the decision. A quarterly
review that produces "no change" three times in a row is a working process; one
that produces "no change" nine times in a row is a process nobody is running.

### 7.4 The tamper *detection* commitments, so they are not overclaimed

Three sentences we may make, and three we may not, agreed once:

| We may say | We may not say |
|---|---|
| "Editing any audit row fails checkpoint verification." | "The audit record cannot be altered." (an attacker who can truncate the ledger **and** the sink destroys it — accepted risk R1) |
| "A changed evidence file is reported, not served." | "Evidence is tamper-proof." (we detect modification; we do not prove origin — §7.2) |
| "A backup is a snapshot of an earlier moment, and the system tells you exactly how far behind you are." | "Restoring gives you the whole record." (`12` §6.1: "**Never tell a customer their ledger is fine** because the chain verifies") |

The third row is the one that matters most commercially, because "a verifying
chain over a truncated ledger is the exact shape of the claim we exist to make
trustworthy" and conflating *intact* with *complete* "would be the single most
damaging thing this product could do."

---

## 8. Vulnerability management programme

### 8.1 Intake

The disclosure policy is written in full in `12` §10.4 and this section does not
restate it; it states what **we must build** and by when. All of it is currently
unbuilt: `12` §11's table says "❌ not started" for `security.txt`, the PGP key
and the disclosure policy.

| Channel | Artefact | Status | Owner | By |
|---|---|---|---|---|
| **security.txt** | `/.well-known/security.txt` served from the site, shipped in the release archive, and present on the company site. Fields: `Contact`, `Expires`, `Preferred-Languages`, `Canonical`, `Policy` | not started | E1 | M1 — a site must be able to find us *offline*, from the release archive |
| **A published PGP key** | Fingerprinted in `security.txt` **and printed in the release archive's `docs/`**, "so a researcher with only a USB stick can verify it" | not started | E1 | M1 |
| **A published disclosure policy** | A URL, and the same text in the release archive as `docs/SECURITY.md` | not started | E1 | M1 |
| **A monitored mailbox** | `security@captain.security`, on somebody's phone, with a stated response | not started | E1 | M1 |
| **Bug bounty** | **Not offered, and we say so plainly** rather than implying otherwise | deliberately no | E1 | M8 at the earliest, and only if we can afford it. "We will announce it here first" |

**Why the PGP key is a security control and not a formality:** it is used for
licence-grant activation, for disclosure notifications to customers, and for
security reports. A disclosure notice that cannot be authenticated is a
disclosure notice an attacker can forge — and a forged "your installation is
vulnerable, apply this patch" mail to a customer of a security product is a
successful attack using our own name.

### 8.2 Severity

**We do not adopt the CVSS number a reporter brings.** We map it. The scale is
`07` §10.1 and it is specific to this domain, because the generic scale does not
survive contact with a security product:

| Severity | Definition in one line | Example |
|---|---|---|
| **Sev-1** | The product asserts something false about a security event, loses an accepted event, takes an unauthorised action, or a customer cannot prove what happened | a real intrusion that was not raised; a cross-tenant read; an RLS escape; a session surviving a password change; a release archive that verifies against a key it should not; a degraded path rendered as a working one |
| **Sev-2** | It works but is materially wrong, and a customer would be misled or slowed | a sustained false-alarm campaign; evidence fetches failing silently; a path-handling defect a negative test would have caught; the TOTP/SHA mismatch in §6.2 |
| **Sev-3** | Degraded, bounded, with a workaround that does not mislead | a dead console link; a metric that is named but never emitted; a runbook step naming a command that does not exist |
| **Sev-4** | Cosmetic | wording |

**The rule that makes this programme work:** *a security report from a customer
is treated as Sev-1 until disproved.* The cost of a false alarm is one person's
worst week; the cost of a real missed intrusion filed as Sev-3 is a customer who
never trusts the product again. At this market's stakes the asymmetry is not
close.

### 8.3 Response SLIs and the fix SLA

From `12` §10.4, plus the fix SLA stated separately because it is the number a
customer actually holds us to:

| Stage | Commitment | Who is on the clock |
|---|---|---|
| Acknowledge | **2 business days** | E1 |
| First assessment — is it real, what is the blast radius | **5 business days** | E2 |
| Fix or mitigation plan | **15 business days** for a mitigation, **45** for a fix | E2 |
| Fix shipped | **Sev-1: 4 hours to mitigate, patch release immediately** (below) | E2 |
| Notify affected customers | **within 24 hours of a confirmed fix, before the public advisory**, if the fix requires them to act | E1 |
| Actively exploited | **1 hour, any hour, any day** — the same page a product Sev-1 uses (`07` §10.3) | whoever is on the rota |
| Keep the reporter informed | **at least every two weeks until it is fixed, even when there is no news** | E1 |

**The fix SLA, per severity, as dates rather than as intentions:**

| Severity | Mitigation | Fix shipped | Root cause | Disclosure |
|---|---|---|---|---|
| Sev-1 | ≤ 4 h | **≤ 7 calendar days**, as a hotfix | ≤ 5 working days | affected customers ≤ 24 h after the fix ships, before the advisory |
| Sev-2 | ≤ 5 working days | next patch train, or ≤ 30 days if a train is further away | ≤ 3 weeks | affected customers within the release, before the advisory |
| Sev-3 | scheduled, with a date | next minor | scheduled | release notes |

### 8.4 The disclosure decision tree

```
  A vulnerability report arrives
  │
  ├─ Is it real?                          ── no ──→ Reply honestly, say what
  │   (E2, ≤ 5 business days)                    why it is not a vulnerability,
  │                                               keep the reporter informed
  │                                               anyway. Credit them if they
  │                                               want credit.  [12 §10.4]
  │
  ├─ Is it exploited in the wild?         ── yes ─→ IMMEDIATE. 1 hour, any
  │                                               hour. Sev-1 protocol. Tell
  │                                               affected customers before
  │                                               the advisory, and the
  │                                               reporter before anyone.
  │
  ├─ Is a customer affected?              ── yes ─→ Customer first, community
  │   (includes "we are not sure yet")           second, public last. "The
  │                                               ordering is a commitment we
  │                                               can keep because we have the
  │                                               customer list."
  │
  ├─ Does it cross an invariant or weaken a
  │  control?                            ── yes ─→ The Sev-1 protocol
  │   (07 §10.3, T+2: "which of the five           applies AND an ADR is
  │    is broken? write it down")                   required, in AGENTS.md's
  │                                               order: what we believed, why
  │                                               it was wrong, what changed,
  │                                               what test now holds it
  │
  ├─ Can it be fixed without a contract
  │  change or an invariant change?      ── yes ─→ Patch train (12 §1.1). No
  │                                               contract change, no contract
  │                                               migration, no behaviour
  │                                               change outside the fix
  │
  └─ Does the fix need a contract change, a
     migration, or a data change?        ── yes ─→ Minor or major release.
     A patch NEVER contains a contract               Downgrade-to-minor is not
     migration. (12 §1.1: "If a patch               available.
     needs a contract step, it is a minor.")
```

### 8.5 The rule about the calendar

> **A security fix ships as a patch release on the train in
> [`release-sre-and-support.md`](../engineering/release-sre-and-support.md), regardless of the calendar.**

Which means, concretely: a Sev-1 or a Sev-2 security fix does not wait for the
first of the month if the first is ten days away. It is a **hotfix**
(`12` §1.4), and a hotfix is *the same process* run today — built from a branch
off the release tag, a full `make ci` run, a full signed artefact, the same
release gate. "The moment a hotfix skips the gate is the moment the gate stops
existing."

**And it ships to every site, not to the affected one.** `12` §1.1: "One
artefact per train. There is no 'site A gets the patch, site B waits'. A version
is a version." `12` §10.3 step 5: every customer who ran the affected version is
told, whether or not they were affected, "because the customers who were not
affected are the ones who will wonder whether we checked."

### 8.6 What we publish, and what we refuse to

- **Every security fix appears in the release notes with a plain description** of
  the issue and its impact, whether or not it has a CVE. `01` M8's marketplace
  and the OEM programme both depend on the advisories being legible to a
  reseller's customer, "and a bare CVE number is useless to somebody who is not
  running a vulnerability scanner."
- **We never rewrite a customer's history.** If our product recorded something
  wrong, the record stays and a later record says it was wrong and points at
  why. A wrong incident, a wrong disposition and a wrong action are all rows in
  a hash-chained ledger and they are not editable; a correction is a superseding
  row, the same mechanism as a re-evaluated observation. "A migration that
  *edits* history would break invariant 4 to fix an invariant-2 problem, and it
  is never the answer."
- **We never report a restored site as healthy.** See §7.4.

---

## 9. The security control matrix

### 9.1 The five invariants → controls → test → cadence

`AGENTS.md`: "A change that weakens one needs an ADR and a test, in that
order." `07` §3 has the full invariant-to-test tables; this is the security
programme's view — the cadence at which each row is re-examined and by whom. The
test column names a representative test, not the full set.

| Invariant | The security controls that hold it | Representative test | Review cadence | Owner |
|---|---|---|---|---|
| **1 — zero accepted event loss; no duplicate side effect** | Scoped dedupe key taken before the work; DB-unique so callers cannot agree differently; lease with owner and expiry; canonical event + inbox transition + key claim in one transaction; terminal failure to a dead letter *with a reason*; outbox transactional with the state change; at-least-once relay that is idempotent; a poison message isolated | `TestDedupeCrossesPartitions`, `TestConcurrentWritesProduceOneRow`, `TestNormalizeIsAtomicWithTheInboxTransition`, `TestFailReachesDeadLetterWithAReason`, `TestOutboxIsTransactional`, `TestOutboxIsolatesAPoisonMessage` | **Per change**, per `07` §2.3 | E2 |
| **2 — decisions replay from stored facts and the stored `AIObservation`** | The replay path has no model client, no bundle path and no clock; a bundle mismatch is reported, not silently replayed; a degraded decision replays as degraded; observations append-only and supersession is a new row; a disposition never overwrites an observation; an unexplainable decision cannot become an incident; the `AIObservation` type has **no free-text field**, so a rule cannot match prose | `TestReplayReproducesTheDecisionFromStoredFacts`, `TestReplayReportsABundleMismatch`, `TestReplaySurfacesDegradation`, `TestObservationsAreAppendOnly`, `TestDispositionDoesNotOverwriteTheObservation`, `TestCreateIncidentRefusesAnUnexplainableDecision` | **Per change.** Carries the most ADRs in the product because a feature request attacks it most naturally | E1 (decision), E2 (test) |
| **3 — a missing AI observation is never a negative observation** | A rule consuming a Tier C signal must declare `if_ai_unavailable`; a missing observation degrades to `UNVERIFIED`; a degraded observation is not evidence; insufficient evidence is not a negative answer; a shadow profile has no authoritative effect, enforced at the **grant** level; attribution is mandatory; the AI plane is reported and never required | `TestConsumingAnAIObservationRequiresAnExplicitDegradation`, `TestMissingAIObservationDegradesToUnverified`, `TestDegradedObservationIsNotEvidence`, `TestInsufficientEvidenceIsNotANegativeAnswer`, `TestShadowProfilesCannotAffectAuthoritativeState`, `schemalaws.TestAIRuntimeCannotAlterAuthoritativeState`, `TestBuiltInBundleRunsWithNoAIObservationAtAll` | **Per change**, plus a **manual** run with the model stopped at every milestone gate — "a test suite can prove the bundle compiles and evaluates without observations; it cannot prove that no other code path consults the model on the availability path" | E1 |
| **4 — the audit ledger is append-only and hash-chained** | Chain computed by a database trigger over a length-prefixed encoding, never by the application; append-only at the storage layer; a duplicate sequence and a gap are both rejected; per-tenant advisory lock; seals signed Ed25519 and verified against a **trusted** key; a removed seal is detectable as a gap offline; a broken chain cannot be sealed; the sealer self-checks before writing; the sink is outside the database's reach | `schemalaws.TestTamperedRowBreaksChainVerification`, `TestAuditLedgerIsAppendOnly`, `TestAuditChainRejectsGap`, `TestConcurrentAppendsProduceOneChain`, `checkpoint.TestVerifyRejectsForeignKey`, `TestGapDetectsRemovedSeal`, `audit.TestSealerRefusesToSealABrokenChain`, `TestSigningPayloadIsUnambiguous` | **Per change**, plus a **seal-loss drill** semi-annually and a **key-rotation drill** per major (`12` §6.4) | E2 |
| **5 — `tenant_id` is `NOT NULL` everywhere and is never read from a request body** | RLS isolates tenants on the small *and* the large partitioned tables; a cross-tenant write is blocked; a write without tenant context is refused; `security_core` has no `BYPASSRLS` and only the migrator does; the adapter and the AI runtime cannot read the ledger or alter authoritative state; site scope **denies by default**; capabilities are exact, never prefixes; capability **and** assignment are both required; a tenant header cannot select a tenant; a service account can never hold `audit.read` | `schemalaws.TestRLSIsoTenants`, `TestRLSIsoTenantsForLargeTables`, `TestRLSBlocksCrossTenantWrite`, `TestTenantContextIsRequiredForWrites`, `TestMigratorIsTheOnlyRoleThatBypassesRLS`, `TestAdapterCannotReadAuditLedger`, `TestAIRuntimeCannotAlterAuthoritativeState`, `identity.TestSiteScopeDeniesByDefault`, `TestAuthorizeSiteCombinesCapabilityAndAssignment`, `TestServiceAccountsNeverHoldAuditRead`, `api.TestTenantHeaderMismatchIsRefused` | **Per change** + a **static walk of the route table** (see §9.2) | E2 |
| **5b — bundles are untrusted input** (this is `README.md`'s fifth invariant; `AGENTS.md` states it as a working agreement — see §9.4) | No shell, SQL, network, dynamic code or regex in the rule language; a list-valued field is bounded so a bundle cannot allocate without bound; unknown keys, multi-document YAML, schema violations, unknown event types and duplicate rule ids are all refused; the embedded schema is the published schema; storing is not activating; activation needs a reviewed simulate diff digest; a release archive is listed, hashed, path-checked, signed and secret-scanned | `rules.TestNoUnboundedRegexOperatorExists`, `TestFieldValueListIsBounded`, `TestUnknownKeyIsRejected`, `TestMultipleYAMLDocumentsAreRejected`, `TestEmbeddedSchemaMatchesThePublishedContract` + `make contract-sync`, `bundles.TestStoreDoesNotActivate`, `TestActivationRequiresAReviewedDiffDigest`, `release.TestTamperedArchiveIsRefused`, `TestSecretMaterialIsRefused` | **Per change.** `AGENTS.md`: "the threat model owns the answer, not the convenience of the call site" | E2 |

### 9.2 The one structural gap in the matrix

Read the invariant-5 rows and notice what is missing: **every one of those
tests is a real test, and none of them walks every handler and asserts the
tenant came from the session rather than the body.**

`TestTenantHeaderMismatchIsRefused` covers the header. The *body* is covered by
nothing structural, because the pattern is enforced by discipline plus
`dbx.TenantContext` being required at the pool level. **The control is real —
the pool requires a tenant context for a write — and the test that proves no
handler bypasses it is missing.** `07` §3 records it; it belongs here too
because it is the single most likely place for a security researcher to find
something, and §9.2's fix is small: a static walk of the route table asserting
that no handler decodes a `tenantId` from a request body. It is the highest
value-per-hour security test in the M1 backlog.

Two more in the same family, both recorded in `07`/`16`:

- **Q-5**, the console's dead-letter and clock-drift tables use `requirePrincipal`
  where the API uses `requireCapability`. A capability leak: a principal without
  `dead_letter.read` sees the dead-letter list.
- **Q-9**, the HTTP access log omits `tenant_id`, `site_id` and `request_id`,
  which `AGENTS.md` requires. `requestID(r)` is propagated to the client and
  logged by the panic handler, but the access-log line cannot: the middleware
  runs outside principal resolution.

### 9.3 Accepted risks

**Restated from `docs/threat-model.md` §6.** These four are not re-argued here;
the threat model's text is authoritative and `13` does not weaken it.

| # | Risk | Status | Review |
|---|---|---|---|
| **R1** | A single Postgres instance is a single point of compromise. RLS, grants and the hash chain limit what an attacker with database write access can do, and tamper is detectable. But an attacker who can truncate the ledger *and* the checkpoint material destroys history with nothing surviving elsewhere. Not eliminated: a control-plane host that can reach both is a host that can lose both | Accepted. B7 requires the sink to be outside the database's reach; the residual is the host that reaches both | Annually, and on any topology change that adds a host (M5's central plane is the first) |
| **R2** | A site with a single camera type cannot corroborate itself. The production rule requires two independent signals, so such a site gets `UNVERIFIED` incidents. "This is correct: a rule that corroborates a camera with itself is theatre. Such a site gets `UNVERIFIED` incidents and should be told so" | Accepted, and communicated to the site | Per milestone — the number of sites with this shape is a sales fact, not a bug |
| **R3** | The incident feed is eventually consistent. p95 15 s on the AI path. "An operator acting on the last 15 seconds is acting on an incomplete picture" | Accepted; the console states the incident's state rather than implying completeness | Per milestone, with the SLO number |
| **R4** | Synthetic fixtures are not captures. The fixture set proves the pipeline is deterministic; it does not prove the Milestone endpoint mapping is correct, because that mapping is `Unverified` | Accepted, and M1's DoD item 1 stays open until a real server confirms it | **Until M1's gate 1.** Not annually — this one has a date |

**New accepted risks this roadmap creates.** Each is stated with what would
change our mind, because an accepted risk without a tripwire is a wish.

| # | Risk | Why we accept it | What would change our mind | Owner | Review |
|---|---|---|---|---|---|
| **R5** | **A customer-supplied rule bundle is trusted input at the *authoring* level.** The compiler makes a bundle safe to *evaluate* — no shell, SQL, network, dynamic code or regex, bounded lists, closed vocabulary — and activation is gated on a reviewed simulate diff. What it does **not** do is make the bundle *sensible*. A tenant administrator with `rules.activate` can silence every incident at their site, and the ledger will show a clean, correctly-attributed, perfectly-provable activation | The customer owns their site's policy, and `rules.activate` is a deliberate, audited, capability-gated, diff-reviewed act. Refusing to let a site tune its own detection would be a different product | A pattern where a customer asks us to author the rules for them — that moves the risk from "their policy" to "our advice", and needs its own controls | E1 | Annually, and on any change to the `rules.write`/`rules.activate` capability model |
| **R6** | **The model bundle supply chain.** Model weights are third-party artefacts installed on a customer disk, and the runtime is Python. A tampered bundle means a model that reports whatever it likes — attributable, hash-chained, and still false | The AI plane cannot alter authoritative state (a grant-level control, `schemalaws.TestAIRuntimeCannotAlterAuthoritativeState`), an observation is Tier C, and every rule consuming it must declare `if_ai_unavailable`. A lying model degrades verification; it cannot forge a corroborated incident | A model bundle that can affect authoritative state — which is exactly what the shadow gate and the grant model exist to prevent | E2 | Every model bundle change; quarterly for the bundle installer |
| **R7** | **The operator who clicks the wrong disposition.** The product's most-used irreversible act is a human reading a screen at 04:00. A disposition never overwrites an observation and never edits the ledger, so it is recoverable — but the *outcome* at a site is not ours to fix | It is the customer's decision to make, made on evidence we can prove was shown. What we owe is the "what it saw / what it did not decide" block, the AI-unavailable banner, and a degraded state that reads as degraded | Nothing in a certificate changes this. The mitigations are UX and audit, and they are M1/M2 work | E1 | Per milestone, in the human-acceptance gate |
| **R8** | **Our own build pipeline, for two people on one machine.** Releases are cut from a developer's machine (`12` §1.3), the signing key lives on a disk, and CI cannot currently build the module at all (CI-1). A compromised developer laptop is a compromised release path to every site | Accepted until M2. The mitigations are in flight: pinned toolchain, pinned actions, `permissions:`, branch protection, two keys with rotation before M3 GA, and the offline verification that does not depend on us at all | A stolen or misused release key — which is why rotation is a pre-M3 requirement, not a nice-to-have | E2 | Quarterly, and on any change to how releases are produced |
| **R9** | **The Milestone endpoint surface is a hypothesis.** Every route is `Unverified` and no Milestone server has been reachable. The adapter that talks to the customer is the one component that has never touched the real thing | Accepted for the length of M1, and it is a **milestone gate**, not a risk we carry: gate 1 is unsignable until it is closed | A live server that contradicts the mapping. That is an expected outcome, not a surprise, and `securityctl adapter probe` exists to find it in minutes rather than weeks (Q-4) | E2 | At M1 gate 1 |
| **R10** | **`sites.default_locale` is a contradiction between two documents.** `AGENTS.md` says it "stays in the schema … a site that later needs another locale should not require a migration to express it"; `docs/status.md` deviation #9 says it "is removed by migration 0010", and `0010_drop_default_locale.expand.sql` exists | Accepted only as a **documentation** risk, and it is not acceptable: two documents in the same repository disagree about a schema decision, and one of them is the newer intent | A decision. Either revert 0010 (forward-only, so that is an expand that re-adds the column) or amend `AGENTS.md`. This is an ADR, and it is cheap | E1 | **Now.** It is a 20-minute decision that has been open across three documents | 
| **R11** | **The fifth invariant is stated two different ways.** `AGENTS.md`'s invariant 5 is `tenant_id`; `README.md`'s invariant 5 is "bundles are untrusted input" and bundles are not in `AGENTS.md`'s list at all. `07` §3 handles this by carrying an "Invariant 5b" row | Accepted as a documentation risk, with both statements in force — **which is the right outcome**, because both are law and the program needs both | A single canonical list. That is a documentation change with no code consequence, and it is nobody's excuse not to do it | E1 | **Now**, with R10 |
| **R12** | **Console content is unasserted.** `07` §2.7: "a template that renders an empty table for every incident passes all twelve." A console that renders nothing is not a security defect; it is a *trust* defect, because the operator's evidence for a disposition is not on the screen | Accepted, bounded, and named as M1 content-assertion work | A Sev-1 that traces to an operator not seeing a degraded banner or an evidence item | E1 | M1 |

### 9.4 On R10 and R11, specifically

Both are **documentation inconsistencies, not code defects**, and both are
recorded here because the program that owns "what is true about this product's
security posture" is this document and a disagreement inside it is a defect in
the program. The fix for each is one ADR and a one-line edit; neither requires
code, and neither is anybody's excuse.

---

## 10. Threat modelling as an annual practice

### 10.1 The existing model and its scope

`docs/threat-model.md` is M1-scoped by its own first line: "Scope: Milestone 1
of the Physical Security Intelligence Layer. One site, one Postgres instance, one
filesystem evidence directory, a Milestone VMS on the site LAN, an operator
console, an optional Python AI runtime, and an offline release process."

It is 7 trust boundaries, 6 attacker profiles (the header says 5; the body has
P1–P6), 24 abuse cases, 35 controls each mapped to a named test, and 4 accepted
risks. §7 names what is not modelled, which is the section that matters most
for planning: physical access to the evidence host, the Milestone VMS itself,
LAN saturation, and insider collusion across two accounts.

**The model is not annual. It is per-milestone, and the trigger is a new trust
boundary, not a date.** An annual re-run of an unchanged system produces a
document nobody reads.

### 10.2 The schedule

| When | What | Scope | Output | Owner | Time |
|---|---|---|---|---|---|
| **Every milestone review** | Delta re-run: what changed since the last model? | Only the new surface | A new §"M<n> deltas" section appended to `docs/threat-model.md`; controls added to §5 with their tests; accepted risks restated | E1 | 1 day |
| **M2** (Q3 2027) | Full re-run | The M1 model plus the investigation engine: evidence retrieval across cameras, the LLM orchestrator, semantic search, `pgvector` for derived data | New boundary for the LLM plane (B4 widens: it now *plans*, not only observes); new abuse cases for a planner that can be induced to search the wrong window | E1 | 2 days |
| **M3** (Q1 2028) | Full re-run, plus the first pen test | Adds safe actions, PTZ, recording extension, notifications, bounded auto-resolution | **The action boundary is new and it is the first one with physical consequence.** §10.4's pre-mortem for M3 in [`risk-register.md`](../company/risk-register.md) is where this bites | E1 + external | 3 days + the pen test |
| **M4** (Q4 2028) | Full re-run, **plus a per-adapter model** | Adapters, the Adapter SDK, third-party SDK code (§10.3) | Each adapter gets its own boundary and its own controls; the SDK's conformance suite becomes a security control, not just a quality one | E1 | 3 days |
| **M5** (Q3 2029) | Full re-run | The central plane, enterprise identity (OIDC/SCIM/AD/LDAP) (§10.3) | A new boundary (site ↔ control plane) and a new identity boundary, with the constraint that the plane is read-mostly and never on the decision path | E1 | 3 days |
| **M6** (Q2 2030) | Delta | Baselines, site memory, drift detection, camera health | "Site memory" is derived knowledge about a site and a set of people. New data category, new DPIA-lite question | E1 | 1 day |
| **M7** (Q2 2031) | **Full re-run, and the most important one in the plan** | Autonomy, escalation to physical response (§10.3) | The product now takes actions with physical consequences under policy, with no human in the loop. A new abuse class: *the policy is wrong* | E1 + external | 5 days |
| **M8** (2032) | Full re-run | The marketplace: third-party adapters, rule packs, model bundles (§10.3) | A new attacker with a legitimate publishing account | E1 | 5 days |
| **Annually, regardless** | Read-through | Confirm nothing in the model has quietly stopped being true | A dated one-page confirmation in this document, or the diffs | E1 | 0.5 day |
| **On any Sev-1** | Delta re-run of the boundary it crossed | Whatever the Sev-1 touched | An ADR, per `07` §10.3 T+5d | E2 | included in the RCA |

### 10.3 The new trust boundaries, per milestone

**M4 — adapters and third-party SDK code.**

Two boundaries that are genuinely new, and one that is only a scaling of an old
one:

- **B8: site ↔ third-party adapter code.** An Adapter SDK exists so "a third
  party must be able to write a working adapter without reading our source"
  (`01` M4). That means third-party code runs **inside our process**, with our
  service account, on the site. New controls needed: a process boundary or a
  capability-scoped interface; a declared permission set per adapter (what does
  this adapter need?); conformance tests that are *security* tests (a
  conformance suite that only checks output shape is not a control); and a
  revocation story for an adapter that turns out to be hostile. **The question
  to answer at the M4 review: what is the blast radius of a malicious
  adapter?** If the answer is "the service account", the SDK needs a sandbox
  before it is public, not after.
- **B9: site ↔ ONVIF / the ecosystem.** "The Milestone VMS itself. A dependency,
  not a target" was M1's position and it does not survive M4. Once the product
  consumes ONVIF, LPR, access control and sensors from a dozen vendors, some of
  that third-party code is in scope, and the analyst metadata (people, vehicles,
  licence plates) is a new data category. **This is also the point at which
  §7.2's media-provenance gap becomes sharpest**: ONVIF analytics metadata is
  exactly the "a site with a single camera type cannot corroborate itself"
  problem at scale.
- ONVIF itself is P1's problem in a new form: more credentials, on more vendors'
  devices, on an untrusted LAN. The per-credential blast radius needs an ADR.

**M5 — the central plane and enterprise identity.**

- **B10: site ↔ central control plane.** The architectural constraint is a test,
  not an intention: "central connectivity disappearing changes nothing about site
  operation" (`01` M5 gate). Security consequences: the plane is read-mostly
  over sites; a compromised plane must not be able to install anything (§3.6's
  rule that the site still verifies locally); fleet management can change a
  site's rule bundle, model version and configuration, so **the plane is a
  capability-bearing control surface** and its compromise is a policy change
  across an estate. It is also the first host that can reach many sites, so
  threat model **R1** (one host that reaches the database and the sink) acquires
  a fleet-wide form.
- **B11: core ↔ the customer's identity provider.** OIDC, Entra ID, AD, LDAP,
  SCIM. New: a federated identity is an external dependency on the site's
  availability path for *authentication*; a compromised IdP is a site-wide
  account compromise; SCIM provisioning is an account-*creation* path, which is
  a different blast radius from a read path. **The non-negotiable, and already
  decided:** "local authentication remains available for isolated sites,
  always" and "permanently without exception — a site that has lost its IdP
  connection and whose accounts are federated must still have operators who can
  sign in" (`12` §2.2). That is a security control expressed as a product
  constraint, and it is the reason a break-glass account exists and why it needs
  a real password and raises a high-priority audit event on every use.

**M7 — autonomous action and escalation to physical response.**

- **B12: the policy engine ↔ the physical world.** This is the boundary the whole
  plan is walking towards, and it is categorically different from everything
  before it. Every previous action wrote a row in a database. This one moves a
  camera, unlocks a door, or escalates to a person who will respond physically.
  New abuse classes that do not exist today: the policy is wrong; the policy is
  right and the world is different; the action succeeded and we believe it did
  not, or the reverse; two actions race; a *failed action* is recorded as
  completed. `01` M7 answers the last one in advance and the answer is the
  control: "an action that is not verified as having succeeded is a **failed
  action**, not a completed one." And `07` §5 stage 8 is the first fault-
  injection test in the programme's order of value, "before M3's first physical
  action exists", because idempotency must be proven before the PTZ preset ships
  and not after.
- **B13: the exception queue ↔ human attention.** At A4 the operator sees 12
  incidents, not 813. The new attack is against *attention*: a policy tuned to
  minimise the queue is a policy tuned to suppress. Every North Star
  companion metric in `01` M7 exists for this reason, and the honest statement
  is that **automation alone is not success — trusted automation is, and the four
  companions are how we prove the difference rather than assert it.**

**M8 — third-party marketplace code.**

- **B14: us ↔ the marketplace publisher.** A third party ships code, rule packs
  and model bundles that install at a customer's site. The release path already
  exists (`internal/release`) and it is the right shape: signed, listed, hashed,
  path-checked, secret-scanned, and — per `01` M8 — "All signed, all installed
  offline." What is new is the **publisher** as an attacker: an account that is
  legitimate, whose bundle is signed by our key, and whose bundle is hostile to
  the eleventh customer. Controls needed: per-publisher keys, not one key; a
  review process that is not "we read it once"; provenance and reproducibility
  on published bundles; a revocation mechanism that works offline; and a stated
  liability position in the OEM/SDK contract (see `14` §8). `01` M8's reduced
  scope already has the right instinct: "one real third party beats fifty
  unreviewed uploads."

---

## 11. Cadence, owners, and the first 90 days

### 11.1 Standing cadence

| Rhythm | What | Who |
|---|---|---|
| **Per commit** | `make verify`; the security tests; the release gate items that are checks | E2 runs it locally, E2 reviews the diff. There is no automated check: Actions does not run for this repository, so "CI is green" is not a phrase anyone is entitled to use. |
| **Per release** | The 20-line gate in `07` §9.1; the SBOM; the manifest; `verify-release` with a **required** trusted key; two keys honoured | E2 builds, E1 signs the notes |
| **Weekly** | The decision digest ([`decisions-and-process.md`](../engineering/decisions-and-process.md) §7); the risk review agenda item (`16` §1); "is any Sev-1/Sev-2 security finding open?" | both |
| **Monthly** | Dependency update + `govulncheck` triage + `go.sum` diff review; the risk register; the accepted-risk list restated with movement; a manual sample audit against §5.1 | E2 rotates |
| **Quarterly** | The hardening sweep across H1–H8; the media-provenance review (§7.3); a `securityctl audit verify` against a production-shaped dataset; the key-rotation rehearsal; the M3+ external pen test scope | E1 accountable |
| **Per milestone** | The threat-model delta (§10.2); the DPIA-lite (§5.5); the data-flow delta question (§5.6); a manual run with the model stopped; the seal-loss and evidence-loss drills | E1 |
| **Annually** | A full read-through of `docs/threat-model.md` and this document; the full model re-run if a boundary changed | E1 |

### 11.2 The first 90 days, as a list

Everything here is a named item with an owner and a date, and every one of them
is either a configuration change or a small test. **None of them is a project.**

| # | Item | Ref | Owner | By |
|---|---|---|---|---|
| 1 | **Fix the Go version conflict.** CI cannot build the module, so no CI control is currently demonstrated | CI-1 / Q-7 | E2 | week 1 |
| 2 | Re-enable `gosec`; the existing `_test\.go$` exclude already covers the stated problem | CI-12 | E2 | week 1 |
| 3 | Add `permissions: {contents: read}`; pin all three actions to SHAs; pin `protoc-gen-go` and golangci-lint; verify the protoc zip's SHA-256; pin the Postgres digest | CI-2..CI-6 | E2 | week 1 |
| 4 | Add `govulncheck` and a dependency-review job; add CODEOWNERS and dependabot | CI-7, CI-13 | E2 | week 2 |
| 5 | Add skip detection, so 07 §9.2's rule is enforced rather than stated | CI-9 | E2 | week 2 |
| 6 | Add a Windows test job with `SeCreateSymbolicLinkPrivilege`, closing the symlink hole on a supported platform | Q-6, CI-15 | E2 | week 2 |
| 7 | **Fix the three documented-but-absent controls**: wire `verifyFactor` to `webauthn.go`'s real `VerifyAssertion` and delete the stub (or remove WebAuthn enrolment until it is wired); match the TOTP algorithm to the enrolment URI; implement the per-source login rate limit the contract and the threat model both describe | §6.2 | E2 | week 3 |
| 8 | Generate the SPDX SBOM in CI; populate `SBOM.SHA256` and `Provenance.SourceDigest`; make `build-release` require a present SBOM file | SC-1..SC-3 | E2 | week 3 |
| 9 | Make `verify-release --trusted-key` required in the install path; make `build-release` refuse to generate a key | SC-4, SC-5 | E2 | week 3 |
| 10 | The static route walk: no handler decodes `tenantId` from a body | §9.2 | E2 | week 4 |
| 11 | Fix Q-5 (`requirePrincipal` → `requireCapability` on the console's two tables) | Q-5 | E2 | week 4 |
| 12 | Fix Q-9: the access log carries `tenant_id`, `site_id`, `request_id` | Q-9 | E2 | week 4 |
| 13 | `security.txt`, the PGP key, the disclosure policy, in the release archive and on the company site | §8.1 | E1 | week 4 |
| 14 | Branch protection, required checks, signed annotated tags for releases | CI-14 | E1 | week 4 |
| 15 | The ADR for R10 (`sites.default_locale`) and R11 (which invariant is fifth) | §9.3 | E1 | week 2 |
| 16 | The DPIA-lite for M1, run against a design partner | §5.5 | E1 | week 6 |
| 17 | The first threat-model delta for M1's exit gate | §10.2 | E1 | at the gate |
| 18 | The model-profile registry schema: approved use cases as a closed vocabulary, known limitations, evaluation report reference | §4.4 | E2 | M2 |

### 11.3 What this program will not do

Named so that a well-meant suggestion does not add it:

- **A bug bounty before we can afford one.** `12` §10.4 says so in public.
- **A SOC 2 before the evidence exists.** See
  [`compliance-and-certifications.md`](compliance-and-certifications.md)
  §1: SOC 2 Type II needs ~3 months of evidence collection and an audit firm,
  so the real start date is 6+ months before the GA date. Starting the clock
  late is how a two-person company ends up with a half-finished audit and a
  lapsed engagement.
- **A media-signing scheme of our own.** §7.3.
- **Telemetry, ever, in any form that identifies a site.** It contradicts
  `12` §3.1 outright, and it is an open decision with no date — see
  [`pricing-and-packaging.md`](../company/pricing-and-packaging.md) §6.
- **Certificate or attestation theatre.** A badge we cannot defend is worse
  than none, and `README.md`'s evidence rule applies to us first.
- **An agent deciding any of this.** §4.3 of
  [`decisions-and-process.md`](../engineering/decisions-and-process.md)
  is the rules of engagement; an agent may draft a threat-model delta and may
  not accept a risk.

# Release Engineering, SRE and Support

Owner: E2 (Engineering). Status: living. Last revised 2026-09-28.

For the product on a customer's isolated network at 03:00, with nobody from this
company on the phone. Everything in §8 is written to be read by a site operator,
not by us.

`` owns the dates. `` owns the
release quality gate. This document owns the artefact, the topology, the
observability surface, the recovery story, the support hours and the disclosure
policy.

---

## 1. Release engineering

### 1.1 The release train

| Train | Cadence | Contents | Who may ship it |
|---|---|---|---|
| **Patch** | Monthly, on a fixed date, small and boring | Security fixes, dependency updates, bug fixes at Sev-2 and below. No new migrations except an `expand` step adding a nullable column or a new index. No contract change. No behaviour change | E2 builds; E1 reviews the release notes; either may ship |
| **Minor** | Quarterly | New capabilities within the current milestone, a `contract` migration that the preceding minor's expand step made safe, an additive API route behind a capability. Contract changes allowed, with `make contract-check` and a baseline ADR | E2 builds; E1 reviews; E1 ships |
| **Milestone major** | At each M<n> exit gate | Everything the milestone produced, all migrations in the milestone, contract version bumps, the new evidence/rule-bundle formats. The build that a milestone's DoD items were demonstrated on | Both, in the same session, with the exit-gate artefact open |

**Rules on the train, and they are rules rather than preferences:**

- **A patch never contains a `contract` migration.** A contract step may never be
  bundled with the expand step that makes it safe (`AGENTS.md`). If a patch needs
  a contract step, it is a minor.
- **A minor never contains a contract *removal*.** Field removals, enum value
  removals and signature changes are major-only, and a major is at least one
  full minor old. `internal/release`'s `Compatibility{MinSchemaVersion,
  MaxSchemaVersion, MinCoreVersion, MaxCoreVersion}` is how the product enforces
  that, and `TestReleaseCompatibilityWindowCoversMigrations` is what stops a
  binary from claiming a schema window its own migrations fall outside of.
- **A site is never more than one major behind at M3 GA.** A customer two majors
  behind has to take two upgrades, each of which is a service interruption, and
  at two people we cannot be on site for both. Below M3, design partners are
  upgraded by us, in person.
- **The date is the date.** A train that is not ready on its day is not late; it
  is the next train, or it is a hotfix (§1.4). A slipped patch ships the
  following month with its notes appended. It does not ship on a Sunday.
- **One artefact per train.** There is no "site A gets the patch, site B waits".
  A version is a version.

### 1.2 The artefact

**A release is a signed, verified, offline-installable archive, and it is the
unit of everything.** Not a tarball someone un-copies, not a `go install`. The
builder and verifier are `internal/release`, and they work today
(`docs/status.md`: "A signed offline release builder and verifier").

```
release-<version>-<platform>.tar.gz
  manifest.json                    the index and the authenticity record
  signature                        detached Ed25519 over the manifest body
  sbom.spdx.json                   software bill of materials, hashed
  bin/securityctl                  the operator and installer CLI
  bin/security-migrate             the migration runner
  bin/security-core                the service
  rules/builtin/bundle.yaml        the shipped rule bundle
  contracts/                       the published schemas, for a customer's own
                                   reference and for their integrations
  migrations/                      the forward-only SQL, in order
  db/seed.sql                      roles and grants, applied by the migrator
  docs/OPERATIONS.md               the operations playbook (§8)
  docs/RUNBOOK-*.md                the customer-facing runbooks (§7.6)
  VERSION                          the version, for scripts
```

`Manifest` is the thing to read: `SchemaVersion` (currently 1), `Product`
(`"security-platform"` — see `` §9.1 for why the
manifest string is not the brand), `Version`, `Platform`, `CreatedAt`,
`GitCommit`, `Files`, `Migrations`, `Compatibility`, `SBOM`, `Provenance`,
`Signature`.

**The four properties that make it trustworthy**, from the package's own
doc comment, and each with the test that proves it:

| Property | Test |
|---|---|
| Every file in the archive is listed with its size and SHA-256, and the verifier recomputes both. "A file present but unlisted is a file nobody reviewed" | `TestABuiltArchiveVerifies`, `TestUnlistedFileIsRefused`, `TestMissingFileIsRefused` |
| Every listed path is archive-relative, forward-slashed, and free of `..`, a drive letter, a backslash and a leading slash — checked on the string before any filesystem call, and again on the cleaned name | `TestValidateFilePathRejectsEveryEscape`, `TestPathTraversalEntryIsRefused`; `unsafePath = regexp.MustCompile(`(^\/)\|(\.\.)\|(^~)\|([A-Za-z]:)\|(\\)`)` |
| The manifest is signed, and verification takes a **trusted** public key rather than trusting the key the archive carries | `TestForeignSigningKeyIsRefused`, `TestUnsignedManifestIsRefused`, `TestManifestEditBreaksTheSignature` |
| The archive contains no credential-shaped material, and the manifest schema has no field that could carry one — so "the manifest says there is no secret" is a structural claim | `TestSecretMaterialIsRefused`, `TestEmptyManifestIsRefused`, `TestBinaryFilesAreNotScanned` |

Plus `Provenance{Builder, BuildStartedAt, BuildFinishedAt, SourceDigest}` and
`SBOM{Path, SHA256, Format}`, so an auditor can answer "what built this and from
what" without asking us.

### 1.3 Branch and release discipline, for two people

**The rules are short because there is no reviewer but us.**

- **`main` is releasable. Always.** No commit lands on `main` that does not pass
  `make ci` locally with a real database. A red `main` means nobody can take a
  hotfix, which means during an outage the only thing we can do is wait.
- **Long-lived branches are forbidden.** No `develop`, no `release/x.y`, no
  `feature/…` that lives for more than three days. Every change is a branch off
  `main`, merged within a day or two, and deleted. Two people cannot maintain
  four long-lived branches; the merge cost alone exceeds the value of the
  isolation.
- **Every merge is a squash**, so `main`'s history is one line per change with a
  message that names the invariant or the gate it touches. The changelog is
  `git log`, and the release notes are generated from it.
- **`main` is tagged at every release** (`v1.4.2`, `v1.5.0`, `v2.0.0-m1`) and the
  tag is what `GitCommit` in the manifest points at. The tag is annotated with
  the release's evidence: the exit-gate artefact for a major, the SLO run for a
  minor, the Sev fixes for a patch.
- **Releases are cut from a clean tree on a known machine**, with a recorded
  `Provenance.SourceDigest`. A release built on a laptop with an unstaged change
  in it is not a release, and `SourceDigest` is what proves it either way.
- **The release key is not in the repository and not in CI's environment.** It
  lives in an offline store; signing is a deliberate act by a person
  (`securityctl release-key` produces it, `securityctl build-release` consumes
  it). A CI runner that can sign means a compromised runner can ship to every
  site at once, and that is the boundary the package's own comment names: "this is
  the boundary where a supply-chain attacker gets code execution on every site at
  once".
- **Verification is public-key-only and offline.** `securityctl verify-release`
  takes a trusted public key. An air-gapped site that can verify without a
  database, a network or our servers is the whole point, and
  `securityctl audit verify-seals` already demonstrates exactly this shape.
- **Two keys from the start, and a rotation before M3 GA.** A current key and a
  previous key, both distributed to every site, so a compromised current key does
  not brick the fleet. A site that has one key and loses it loses the product.
  Rotation is a minor release, and the previous key's signature remains
  verifiable for a stated period.

### 1.4 Hotfixes

The only process that runs outside the train, and it is narrow on purpose.

```
A hotfix is a fix for a Sev-1 or a Sev-2 security finding.
It is built from a branch off the release tag, not off main.
It gets a full make ci run, a full release artefact, and a signed manifest.
It is a patch train that happens today instead of on the first.
It is never a different process — no "quick build", no unsigned binary, no
   "we will fold it into the next release".
```

The rule that matters: **a hotfix is a release.** Everything in §1.2 and
`` §9 applies without exception, because the moment a
hotfix skips the gate is the moment the gate stops existing.

---

## 2. Deployment topology

### 2.1 A single site, M1–M4

```
┌──────────────────────────────────────────────────────────────────────────────┐
│  SITE — WH-DEPOT-1                          no internet, ever                │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│  │ OPERATOR WORKSTATION (Windows)                                        │  │
│  │  browser → https://security-core:8443                                 │  │
│  │  securityctl (run locally when the service is down)                  │  │
│  └───────────────────────────────┬────────────────────────────────────────┘  │
│                                  │ 127.0.0.1 or site LAN                      │
│  ┌───────────────────────────────▼────────────────────────────────────────┐  │
│  │ APPLICATION HOST  (Windows Server 2019+ or Ubuntu 22.04 LTS)          │  │
│  │  ┌──────────────────────────────────────────────────────────────────┐  │  │
│  │  │ security-core (one process)                                      │  │  │
│  │  │   HTTP/HTTPS :8443  strict CSP, TLS 1.2–1.3, no plaintext       │  │  │
│  │  │   workers: inbox → normalize → outbox relay → VLM → evidence     │  │  │
│  │  │   console templates + console.css embedded (no asset directory)  │  │  │
│  │  └───────────────┬──────────────────────────┬───────────────────────┘  │  │
│  │                  │ unix socket / local TCP │ local TCP 9090            │  │
│  │  ┌───────────────▼──────────────┐  ┌────────▼───────────────────────┐  │  │
│  │  │ PostgreSQL 16                 │  │ VLM RUNTIME (Python)           │  │  │
│  │  │  security / security_core     │  │  optional, GPU box              │  │  │
│  │  │  RLS per tenant, 12 forward-  │  │  NOT on the availability path  │  │  │
│  │  │  only migrations              │  │  stopped ⇒ incidents raise     │  │  │
│  │  │  audit ledger, inbox,         │  │  UNVERIFIED, never dismissed   │  │  │
│  │  │  canonical_events (monthly    │  │  ▲ optional component; the     │  │  │
│  │  │  partitions), jobs, outbox    │  │  ▲ release is installable and  │  │  │
│  │  └───────────────┬───────────────┘  │  ▲ functional without it       │  │  │
│  │                  │                  └────────┬───────────────────────┘  │  │
│  └──────────────────┼───────────────────────────┼──────────────────────────┘  │
│                     │                           │                             │
│  ┌──────────────────▼───────────────┐  ┌────────▼───────────────────────┐  │
│  │ EVIDENCE STORE                   │  │ GPU BOX (M1, separate host)    │  │
│  │  content-addressed               │  │  NVIDIA, 16–24 GB VRAM  ▲EST   │  │
│  │  <root>/<tenant>/<xx>/<sha256>   │  │  the VLM runtime's host only   │  │
│  │  retention sweep + legal hold    │  │  may be off; the site is not    │  │
│  │  ▲ NOT in the database, on       │  │  degraded                       │  │
│  │    purpose: survives a DB loss   │  └────────────────────────────────┘  │
│  └──────────────────────────────────┘                                       │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│  │ CHECKPOINT SINK  (separate disk or a write-once volume)               │  │
│  │  signed seals, verifiable offline with the trusted public key,         │  │
│  │  OUTSIDE the database's reach — a seal an operator with database write  │  │
│  │  access can rewrite proves nothing                                    │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│  │ VMS  (the customer's, already installed)                                │  │
│  │  Milestone / XProtect  ──events──▶  adapter  ──▶  inbox_events         │  │
│  │  ◀────────────  actions (bookmark, PTZ preset, extend recording)        │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────────┘
```

**The three splits that matter, and why each exists:**

1. **Evidence is on a filesystem, not in the database.** `internal/dr`'s
   `TestRecoveryDrill` depends on this and says so: "Evidence lives on a
   filesystem, not in the database. That is the point of the split." A database
   loss is recoverable from a backup; a filesystem loss of a few TB of video is
   not, and a 2,000-camera site's evidence does not fit a backup window at all.
2. **The checkpoint sink is outside the database's reach.** Same drill: "a seal an
   operator with database write access can rewrite proves nothing." The threat
   model accepts the residual risk that a host reaching both can lose both, and
   the drill asserts what survives rather than pretending otherwise.
3. **The AI plane is on a separate host and off the availability path.** A GPU box
   that is out of memory, a driver that needs a reboot, a Python runtime that
   crashes: none of it may stop the site. `TestBuiltInBundleRunsWithNoAIObservationAtAll`
   and `TestHealthExcludesTheAIPlane` are the two tests that hold this, and
   `/v1/health` carries `aiPlane: {note: "AI availability is not an SLO and never
   affects this status."}` so an operator reading it can see the AI plane was
   not consulted.

### 2.2 M5 central plane, for a three-site customer

```
┌───────────────┐   ┌───────────────┐   ┌───────────────┐
│ SITE A        │   │ SITE B        │   │ SITE C        │
│ core + pg +   │   │ core + pg +   │   │ core + pg +   │
│ evidence +    │   │ evidence +    │   │ evidence +    │
│ (optional GPU)│   │               │   │               │
└───┬───────▲───┘   └───┬───────▲───┘   └───┬───────▲───┘
    │       │           │       │           │       │
    │ outbound only     │       │       outbound only
    │ (HTTPS 443)       │       │           │
    ▼       └───────────┴───────┴───────────┘
   ┌────────────────────────────────────────────┐
   │  CENTRAL CONTROL PLANE  (optional)         │
   │  fleet: site/adapter/rule/model versions,  │
   │         health, storage, licences, config  │
   │         drift                              │
   │  identity: OIDC / Entra / AD / LDAP /     │
   │            SCIM  (local auth ALWAYS        │
   │            available, permanently)         │
   │  org-wide graph: same credential at A, B  │
   │                  and C is ONE incident     │
   │  read-mostly over sites; never on the      │
   │  site's decision path                      │
   └────────────────────────────────────────────┘
                    │ optional
                    ▼
              customer's IdP, SIEM, ITSM
```

**The architectural constraint, and it is a test not a intention:** *central
connectivity disappearing changes nothing about site operation.* From M5 the
M5 exit gate is "a three-site customer running production with the control plane
pulled mid-incident and no site degradation", and the fault-injection test
exists for it.

That constraint has concrete consequences the design must keep:

- Every site holds its own complete state. The central plane holds an index, not
  the truth.
- A site never blocks on a central call. Any code path that waits on the central
  plane for an answer before ingesting, verifying or raising an incident is a
  defect, and the correct shape is a local decision now, a reconcile later.
- Local authentication is retained permanently and permanently without exception
  — a site that has lost its IdP connection and whose accounts are federated must
  still have operators who can sign in. This is in `01` M5 as
  "local authentication remains available for isolated sites, always", and it is
  in `` as row #87 with a note that "a non-requirement,
  so it is a requirement".

### 2.3 What the M1 install/upgrade/backup/restore skeletons must become

`docs/status.md` is precise about the current state: the commands "register and
refuse with a named reason"; the release builder and verifier they depend on now
exist and work; the schema window check that stops a half-upgrade is implemented;
what is missing is the installer that extracts the verified archive, the
transactional upgrade, and the backup command. "Those are real work, not a stub,
and they are the next thing to write."

**The specification for what they become.** Each is a state machine, and each
state is recoverable, because a half-upgrade on an isolated site is a truck
roll.

### 2.3.1 `securityctl install`

```
  verify    securityctl verify-release <archive> --key <trusted.pub>
            runs BEFORE anything is written. A failure here writes nothing at all.
            Verifies: signature under the trusted key (not the archive's own),
            every file listed and hashed, no unsafe path, no secret material,
            the Compatibility window, and that the archive's migrations fit
            inside MinSchemaVersion..MaxSchemaVersion.
            → on failure: exit non-zero, name the file and the reason, write
              nothing. securityctl preflight as a dry run of this step.

  extract   to <root>/versions/<version>/, never in place.
            A version is a directory. There is exactly one "current" pointer.

  seed      apply db/seed.sql (roles and grants) via the migrator, not by hand.
            security_core gets no BYPASSRLS. Only the migrator does.
            (TestMigratorIsTheOnlyRoleThatBypassesRLS.)

  migrate   security-migrate up, one step at a time, each recorded.
            Refuses to start against a schema outside its declared window.
            (internal/migrate: TestMigrationsApplyToEmptyDatabase,
             TestUpIsIdempotent, TestVerifyChecksumsCatchesDrift,
             TestFailedStepLeavesEarlierStepsCommitted)
            A failed step leaves earlier steps committed and the current pointer
            UNCHANGED. The service does not start.

  bootstrap  securityctl identity bootstrap-admin --username admin
             Refuses a second administrator. (TestBootstrapRefusesASecondAdministrator.)
             Requires a long passphrase. (TestBootstrapPassphraseMustBeLong.)

  activate  flip the "current" pointer, then start the service.
            The pointer flip is the commit point. Before it: a version is
            installed and inactive. After it: it is live.

  verify    securityctl doctor, then /v1/health/ready, then
            securityctl audit verify, then one end-to-end event through
            fixtures (securityctl incident list on a seeded incident).
```

**The state machine:** `absent → verified → extracted → seeded → migrated →
bootstrapped → current`. Any step can be re-run from the last good state. There
is no step that is destructive except `migrate`, and `migrate` is
forward-only (`AGENTS.md`) with a `contract` step that only ever ships in a
release where a previous release's `expand` step made it safe
(`TestContractStepAlongsideExpandIsRefused`).

### 2.3.2 `securityctl upgrade`

```
  0  state      service running, current = A, database at schema N
  1  verify     the new archive, against the trusted key         [same as install]
  2  preflight  the *compatibility* question, which is the one that matters:
                 · does A's schema fall in the new binary's window?
                 · does the new binary's migration set apply forward from N?
                 · is the new binary compatible with the site's current rule
                   bundle version, and with the evidence directory layout?
                 · has the current version been running long enough to have a
                   backup newer than the release?  ← the rollback question
  3  backup     refuse to proceed without a verified backup taken since the last
                release. This is the single most important line in the upgrade.
  4  extract    to versions/B/
  5  stop       graceful shutdown (§4.4). Not a kill.
  6  migrate    security-migrate up, expand steps first. In a minor release, the
                service can come back up between expand and contract — that is
                the entire reason expand and contract ship separately.
  7  activate   flip the pointer to B
  8  start
  9  verify     /v1/health/ready, securityctl audit verify, audit verify-seals,
                a fixture event end to end, and the SLO panel for 15 minutes
 10  decide     if 9 failed: rollback (flip back to A). A is still on disk and A
                can still read the database *provided step 6 was expand-only*.
                A release that bundled an expand and a contract step cannot be
                rolled back without a restore — which is why it is a major.
```

### 2.3.3 `securityctl rollback`

```
  Refuses if the current schema is above the target version's MaxSchemaVersion.
  That refusal is the correct behaviour and the message says so: "A → B applied
  a contract step; B → A requires a restore from the backup taken at step 3."
  The backup path is printed, with the command.
  Rollback is a pointer flip plus a restart, and it is audited like any other
  state change — a rule bundle rollback is, and
  TestRollbackReturnsToThePreviousVersion exists for exactly this reason.
```

### 2.3.4 `securityctl backup` and `restore`

```
  backup   1  pg_dump --format=custom, consistent snapshot
           2  the evidence store: NOT copied. Evidence is content-addressed
              and immutable, so the backup is the database's references and the
              operator copies or replicates the store separately. Copying TB of
              video into a backup is how a backup stops being taken.
           3  the current rule bundle, exported
           4  the config, with secrets excluded and listed by name only
           5  a manifest: the schema version, the audit ledger's highest
              sequence at snapshot time, the highest checkpoint sealed sequence,
              the bundle version and content hash, the release version
           6  the manifest is signed with the checkpoint key

  restore  1  verify the backup's manifest signature
           2  check the schema version against the binary's window
           3  restore, migrate up
           4  securityctl audit verify          → the chain
           5  securityctl audit verify-seals   → every seal, offline
           6  ***THE FINDING*** the restored ledger may be behind the seal.
              §6.
           7  rebuild from the built-in bundle in the release archive, which is
              why it ships there and not only in the database:
              TestRecoveryDrill asserts the built-in bundle compiles after a
              full schema rebuild, with the database gone.
```

---

## 3. Offline and air-gapped operation

### 3.1 The constraint, stated as a design rule

`00` §6: "a dependency that breaks that is a regression, not a feature" — where
"that" is "a six-month-old release is still installable on a customer's isolated
network". So:

- **No runtime component fetches anything at run time.** No CDN, no licence
  server check, no telemetry, no crash reporter, no update check, no
  certificate revocation check that requires the internet. A product that phones
  home is a product an air-gapped site cannot run, and an air-gapped site is a
  large part of the market.
- **No dependency whose licence forbids redistribution in an offline bundle.**
  The SBOM is shipped in every release so this is checkable rather than assumed.
- **Every artefact a site needs for two years of operation is in the release
  archive or in a separately installed offline bundle:** binaries, migrations,
  the rule bundle, the model bundles, the operations playbook, the runbooks, the
  OpenAPI and JSON Schema, the SBOM, the release key's public half.
- **The product's own verification is the only network operation it ever
  performs**, and it performs none. `securityctl verify-release` and
  `securityctl audit verify-seals` both work with no network and no database.
  `docs/status.md` reports this as demonstrated: "`audit verify-seals` — the
  air-gapped path, no database — accepted it offline and refused the same seal
  against a different key."

### 3.2 No-internet install

The full path, as it is actually performed at a site with a USB stick and no
network:

```
  ON A NETWORKED MACHINE (ours)
    securityctl build-release --out release-<v>-<platform>.tar.gz
    → signed manifest, SBOM, provenance
    securityctl verify-release release-<v>-<platform>.tar.gz   (self-check)
    write to encrypted USB
    print the SHA-256 of the archive and write it down

  AT THE SITE (isolated)
    securityctl verify-release /media/usb/release-<v>.tar.gz --key trusted.pub
    → checks the trusted public key, NOT the one in the archive.
    → recomputes every file hash.
    → refuses an unsafe path, a secret, an unsigned manifest.
    If it fails, nothing has been written and nothing has been touched.
    securityctl preflight      # the dry run
    securityctl install ...    # §2.3.1
```

**The trusted public key is a separate transfer.** It comes on different media
from a different person, or it is already on the site from a prior install. An
air-gapped site whose only copy of the signing key arrives inside the archive it
is meant to verify is not verifying anything.

### 3.3 Offline licence activation — the honest problem statement

**This needs a design decision before M3 GA, and we have not made it.** Stating
the problem precisely, because a licence design chosen under sales pressure is
how a security product ends up requiring the internet to start.

**What is easy:**

- *A licence that does not expire.* A per-site licence, bound to the site's
  stable identity, with no time component. It works offline forever and needs no
  activation. Cost: we cannot stop a customer who stops paying. For a company
  whose first paying customers are design partners who want to be references, and
  whose growth is expansion rather than churn, that is a real and acceptable
  trade at M2.
- *An expiry enforced by the site's own clock.* The licence carries an expiry
  date; the site refuses to operate past it. This works fully offline. It fails
  when a site sets its clock back, which is a threat we already measure — the
  whole clock-drift subsystem exists. It is defeatable, and we would say so.

**What is hard:**

- *Phone-home activation.* Contradicts §3.1 outright, and a site that cannot
  start because our server is down has a security product that is a liability.
- *Online activation with an offline grace window.* Solves the availability
  problem and not the right problem, and it still requires a server.

**The options, and the recommendation:**

| Option | Offline | Enforceable | Customer experience | Cost |
|---|---|---|---|---|
| **A. Perpetual per-site licence, no expiry** | yes | no | never blocked | churn risk; acceptable at M2–M3 |
| B. Expiring licence, enforced by the site clock | yes | weakly | never blocked; degrades at expiry | defeatable by clock manipulation |
| C. Activation with a signed grant, periodic re-issue optional | yes, after first issue | partially | never blocked | needs a decision on what "periodic" means |
| D. Online activation required | **no** | yes | **blocked when we are down** | not viable |
| **E. A + B combined: perpetual, with a contractual renewal date enforced only in the fleet view, not on site** | yes | no | never blocked | we know from our own records who has lapsed |

**Recommendation: E, decided before M3 GA, and the decision is an ADR.** The
product never refuses to operate because a licence lapsed, because a security
product that goes dark is the one failure mode a customer cannot tolerate and we
cannot excuse. Renewal is a commercial relationship tracked in the fleet view
(M5), not an enforcement mechanism on the site. For the first ten customers this
is obviously right, and writing it down now is what stops it being re-litigated
under pressure at M4 with an OEM reseller in the room — and an OEM will ask for
enforcement, which is a different conversation with a different answer, and it
needs a different mechanism (a fleet-side gate on updates, not a site-side gate
on operation).

**Until the ADR lands, every design partner's agreement says in writing that the
licence is perpetual and that the product will not be disabled for
non-payment.** That sentence, in a document a customer holds, is worth more than
whatever enforcement we might have shipped.

### 3.4 Air-gapped model bundle installation

A model bundle is untrusted input (`AGENTS.md`). So:

```
  securityctl model-bundle install <path>.tar.gz --key trusted.pub
    1  verify the signature under the trusted key
    2  verify every file is listed and hashed, no unsafe path, no secret
    3  verify the declared hardware profile matches THIS host: GPU model, VRAM,
       driver, runtime version. A bundle that does not fit is refused here, not
       at first inference.
    4  verify the declared permitted uses, and refuse to install a bundle whose
       permitted uses do not include the profiles we would run
    5  extract to a versioned directory
    6  do NOT activate. Activation is a separate, audited act naming the profiles
       the operator wants to run. TestStoreDoesNotActivate is the rule-bundle
       expression of the same principle and the same test shape applies.
```

**When the model changes, the site keeps the old bundle.** Bundles are
content-addressed and never overwritten, so a site's observations remain
attributable to a model version that still exists on disk and can still be
verified offline. **Deleting a model bundle whose observations are still cited
breaks a replay** — and that is an invariant-2 problem before it is a disk-space
problem. Retention therefore covers bundles with a live citation, exactly as
`TestSweepRespectsRetentionAndLegalHold` covers evidence.

### 3.5 Update package distribution

The distribution path depends on the site's connectivity, and both paths end at
the same verified artefact:

| Site type | Path | Notes |
|---|---|---|
| Air-gapped | Encrypted USB or a physical handover, checksum printed, `verify-release` before anything else | The checksum on paper is the backup for the key: a mismatch is visible to two people |
| Intermittent WAN | Customer pulls the artefact onto their own media, or onto an internal file server, then verifies locally | The product never fetches it itself |
| Connected (M5 fleet) | The central plane lists available versions. **The site still verifies the signature locally before installing** | The fleet is a convenience, not a trust path. A compromised fleet server must not be able to install anything |

**Nobody's site ever receives a binary that has not been verified against a local
trusted key.** That is a one-line rule with no exceptions and it is what makes
the whole chain meaningful.

### 3.6 OS and hardware support matrix

**Decision, taken now, with a stated reason.**

| Platform | Status | Reason |
|---|---|---|
| **Windows Server 2019, 2022** (x86-64) | supported, M1 | `deploy/` was created and removed; a Windows Service is one of the two units `docs/status.md` defers. Windows is where the incumbent VMS control servers and the IT that owns them live. A product that does not run on Windows does not get into a third of the buildings |
| **Ubuntu 22.04, 24.04 LTS** (x86-64) | supported, M1 | systemd, the other half of the buildings, and the cheapest target for a site running a container platform already |
| **Windows 10/11 Pro** | supported for the **operator workstation and `securityctl` only**, M2 | The CLI runs anywhere; the *service* on a workstation is a support incident waiting to happen |
| **Windows Server 2016** | **not supported** | End of extended support. A customer on it needs an OS upgrade conversation first, and we would rather have that conversation than a service unit on an unsupported host |
| **ARM64 (aarch64) — Graviton, Ampere, Apple silicon** | **GATED, decision by M2** | Two problems and neither is the instruction set. First, the VLM runtime and the GPU stack on ARM are a different dependency tree, and `00` §6 requires an ADR for a new runtime. Second, ARM is where the *central* plane and the developer workstations are, not where the sites are, so the sites gain nothing and we take on a support surface. **The gate:** if a design partner or a reseller pipeline names an ARM site by the end of M2, it is an ADR and an M3-quarter project; if not, it stays unsupported and we say so in the support matrix rather than leaving it ambiguous |
| **macOS** | **not supported for the service**; `securityctl` on macOS, M2, for developers | A site runs Windows or Linux. macOS is a developer convenience and saying otherwise invites a support load we cannot carry |
| **32-bit x86** | never | Postgres 16 and Go 1.26+ do not build for it |

**The rule for adding a platform:** a platform is supported when we have a
release artefact, a service unit, a path-traversal negative test on that
platform's filesystem semantics (§6 of `07`), and a customer. Anything short of
all four is "not supported", stated as such in the support matrix, rather than
"probably works". **`TestSymlinkEscapeIsRefused` skipping on Windows is the
concrete example of what "supported" costs** — a platform we call supported and
do not test on is a platform where the evidence store's escape protection is
unverified.

---

## 4. Services, configuration and shutdown

### 4.1 The service definitions

`deploy/` was created and removed; `docs/status.md` says the units are "genuinely
small, and it is deferred rather than absent — nothing else depends on it and it
should be written against a real service manager." §3.6 above is the point at
which nothing-else-depends-on-it stops being true, and there is a customer.

**`deploy/windows/captain-core.xml`** — a Windows Service definition, installed
from the verified archive by `securityctl install`:

```xml
<ServiceInstall Id="captain-core" Name="Captain Core" DisplayName="Captain Security Intelligence"
  Description="Ingest, verification, incident lifecycle, console and AI observation plane."
  Start="auto" ErrorControl="normal" Account="NT SERVICE\CaptainCore" Type="ownProcess">
  <ServiceDependency Id="LanmanWorkstation" />
</ServiceInstall>
<ServiceConfig ServiceSid="none" DisplayName="Captain Core"
  Description="..." Start="auto" FailureActions="restart/5000/restart/10000/reset/60000"
  ResetPeriod="86400" />
```

- `Start="auto"`. A security product that does not come back after a reboot has
  silently stopped protecting a site, and the operator finds out on Monday.
- `FailureActions`: restart at 5s, restart at 10s, give up after 60s. A
  crash-looping service that is restarted for ever is worse than one that stops
  and is noticed, so the third action resets rather than restarting indefinitely.
- `Account="NT SERVICE\CaptainCore"` — a virtual service account, so there is no
  local password to manage, rotate or leak, and the service's own identity is
  the least-privilege one.
- `ServiceSid="none"` — the service does not get a session SID, because it
  serves no session.

**`deploy/systemd/captain-core.service`**:

```ini
[Unit]
Description=Captain Security Intelligence
After=network-online.target postgresql.service
Wants=network-online.target
# A site is degraded without the database, not broken. Bounded restarts, then stop.
StartLimitIntervalSec=600
StartLimitBurst=5

[Service]
Type=notify
User=captain
Group=captain
ExecStart=/opt/captain/current/bin/security-core --config /etc/captain/config.toml
ExecReload=/bin/kill -HUP $MAINPID
TimeoutStopSec=120
KillSignal=SIGTERM
Restart=on-failure
RestartSec=10
# Hardening, in order of how much it actually helps:
NoNewPrivileges=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectSystem=strict
ProtectHome=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictNamespaces=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
ReadWritePaths=/var/lib/captain /var/log/captain /var/lib/captain-evidence
StateDirectory=captain
LogsDirectory=captain

[Install]
WantedBy=multi-user.target
```

- `Type=notify` — the service tells systemd it is ready. With `systemd` and the
  `/v1/health/ready` contract, a site can see the service is up and genuinely
  ready rather than merely listening.
- `TimeoutStopSec=120` and `KillSignal=SIGTERM` — the graceful-shutdown
  contract of §4.4, and a systemd `timeout` that is longer than the application's
  own drain. A stop that times out becomes a `SIGKILL`, which is a crash, and
  invariant 1 applies to crashes.
- `ProtectSystem=strict` with explicit `ReadWritePaths` — the service cannot write
  anywhere it has not been named. On a host with a VMS on it, this is the
  difference between a compromised Captain and a compromised site.
- `After=postgresql.service` but **not** `Requires=`. A database that fails to
  start is a degraded Captain, not an absent one: the console, the CLI and the
  seal verification still work, and an operator can read the evidence they have.
- **There is deliberately no OCI profile in M1**, despite `README.md` mentioning
  "optional OCI profile" in the `deploy/` row. Containers on an isolated site
  require either a registry we cannot reach or a bespoke image the customer's
  team has to maintain, and a site that has to maintain our container is a site
  that will not upgrade. systemd and the Windows Service cover the market. An OCI
  profile is M4 work at the earliest, for resellers who containerise.

### 4.2 Configuration

```
  /etc/captain/config.toml            Linux     (captain install sets 0640 root:captain)
  C:\ProgramData\Captain\config.toml   Windows
```

Precedence, highest first, and it is a fixed list so nobody has to ask:

```
  1  command-line flags            --listen, --config, --version
  2  environment variables         CAPTAIN_<SECTION>_<KEY>, for one-off diagnostics
  3  the config file               the committed site configuration
  4  built-in defaults             internal/dbx.DefaultConfig() etc.
  5  the release archive's shipped defaults        ← last, and only for values
                                                     that are safe to default
```

`internal/dbx`'s `ApplyDSN` already has the right shape and the right comment: "A
field absent from the DSN keeps the Config's value, so a site configuration can
set defaults that a one-off override only partially replaces."

**The keys, and their real values.** From `dbx.DefaultConfig()`, which is "the
single-instance, single-site profile from the capacity target":

| Key | Default | Meaning |
|---|---|---|
| `db.host` | `127.0.0.1` | the local Postgres |
| `db.port` | `5432` | |
| `db.database` | `security` | |
| `db.user` | `security_core` | **no `BYPASSRLS`** (`TestMigratorIsTheOnlyRoleThatBypassesRLS`) |
| `db.sslmode` | `require` | even on loopback, so a misconfiguration cannot silently downgrade |
| `db.max_open_conns` | `32` | |
| `db.max_idle_conns` | `8` | |
| `db.conn_max_lifetime` | `30m` | |
| `db.statement_timeout` | `15s` | a runaway query must not hold a connection for ever |
| `db.application_name` | `security-core` | so `pg_stat_activity` names us |
| `db.health_check_interval` | `30s` | |
| `db.connection_max_wait` | `5s` | |
| `listen.addr` | `:8443` | |
| `listen.tls_cert` / `listen.tls_key` | — | required unless `behind_proxy` |
| `listen.behind_proxy` | `false` | the only way to run plaintext, and it means something else terminates TLS |
| `evidence.root` | `<statedir>/evidence` | content-addressed, on its own disk where possible |
| `evidence.retention_days` | — | retention sweep; **fails closed on legal hold** |
| `checkpoint.sink_dir` | `<statedir>/checkpoints` | **outside the database's reach** |
| `worker.concurrency` | 1 per queue | one worker per queue name, so claim races are within a process or across two, not across nine |
| `metrics.listen` | `127.0.0.1:9090` | loopback by default, so a metrics scrape is not a network exposure |

**Secrets are never in the config file.** `dbx`'s comment is the rule: "The
password is never here: it is read from the encrypted secret store at connect
time and passed to the driver directly, so it cannot land in a config file, a
log line, or a process dump." `Config.DSN()` renders the connection string with
the password omitted and is documented as safe to log. `Open(ctx, cfg, password)`
takes the password out of band, "never read from a config file or an environment
variable that ends up in a diagnostic dump."

So: the database password, the checkpoint signing key, the release signing key
and the TLS private key are **not** in `config.toml` and **not** in the
environment. On Linux they come from the secret store; on Windows from the
DPAPI-protected store or a local file readable only by the service account.
`securityctl doctor` reports *whether* each is present, never its value.

### 4.3 Log rotation

**Format.** `log/slog` JSON to stdout and to a file, one line per event. Every
line carries, per `AGENTS.md`: `tenant_id`, `site_id`, `request_id`. Never a
secret, a token, a credential or raw biometrics. The audit ledger is a **separate
store from the operational log** and is the authoritative one; a log line can be
lost, a ledger row cannot.

**What the HTTP access log actually emits today, and the gap.**
`internal/api`'s `logRequests` writes `method`, `path`, `status`, `duration_ms`,
`remote` — and, correctly, **no query string** ("the API takes nothing secret
there, but a habit is a habit"). But it does **not** write `request_id`,
`tenant_id` or `site_id`. `requestID(r)` reads the `X-Request-Id` header and is
propagated to the client in every `WriteError` body and on the console footer, and
the panic handler logs it — but the access log line does not, and it cannot: the
middleware runs outside the principal resolution. **This is a real gap against
`AGENTS.md` and it is a named quality incident (Q-9 in
`` §11.4).** The fix is a response-writer that captures
the request id the handler generated and a post-handler hook that resolves the
principal once, so a line reads:

```json
{"ts":"2026-03-01T09:00:12Z","level":"INFO","msg":"http","method":"POST",
 "path":"/v1/incidents/inc_01J8XQ4M2R/dispositions","status":201,
 "duration_ms":143,"remote":"10.20.3.44","request_id":"7fq2m1c4",
 "tenant_id":"tn_9c","site_id":"WH-DEPOT-1","actor_id":"usr_4f2a",
 "capability":"incident.disposition","outcome":"escalated"}
```

**Retention.** 30 days for operational logs, compressed after 7, on a
site-determined volume. **The audit ledger's retention is a legal matter, not a
log policy** — it is governed by the site's retention configuration and legal
hold, and it is never swept by a log rotation. That distinction is why they are
two systems.

**Rotation.** `logrotate` on Linux, daily plus a size cap, with
`copytruncate` avoided in favour of a signal-and-reopen so no line is written to
a rotated file. On Windows, the service rotates on size and hands completed
files to a site-retained archive. **A site can set the destination to a SIEM
collector on their own network**; the format is JSON, which is the point of
choosing it.

### 4.4 Graceful shutdown, which is a correctness requirement

`AGENTS.md`'s invariant 1 says `kill -9` at any stage loses no accepted event and
duplicates no side effect. Graceful shutdown is not therefore a nicety — it is
the ordinary path, taken on every restart, every upgrade and every service
manager reload, and it is the case where a well-behaved shutdown is *provably*
lossless rather than accidentally so.

**The contract:**

```
  SIGTERM / Stop-Service received
    1  /v1/health/ready begins returning 503 with {"ready": false,
       "checks": {"shuttingDown": {"ok": false, "detail": "draining"}}}
       IMMEDIATELY. Before anything slow. The service manager and the load
       balancer both watch this, and a site watching the console sees it.
    2  the HTTP listener stops accepting NEW connections. Existing requests are
       allowed to finish, up to the drain timeout. A request already in flight is
       never cut off mid-write: a half-written disposition is worse than a slow
       one, because the operator does not know whether it landed.
    3  workers stop claiming. In-flight work finishes.
       · inbox: finish the current event or let the lease expire. Do not
         release the lease explicitly with a short expiry — let it be the normal
         path, so shutdown and crash take the same code path. One code path for
         two events is how the crash case stays correct.
       · jobs: same. A job that is mid-claim is completed or abandoned; an
         abandoned job is reclaimed when its lease expires, which is
         TestJobClaimIsExclusiveAndRecoverable's mechanism.
       · outbox: do not drain. Draining is the one thing that could take a
         shutdown past the drain timeout. The relay is at-least-once
         (TestOutboxDeliversAtLeastOnceAndTerminally) and an undelivered message
         is delivered on restart. Drain nothing.
    4  the current checkpoint is sealed if one is due. A restart is one of the
       two moments a seal is worth taking, and "take a seal before you stop" is
       a rule an operator can hold in their head.
    5  the pool is closed, flushes and closes cleanly. The audit ledger's last
       transaction is committed before this point.
    6  exit 0
```

**The drain timeout and what happens past it.** `TimeoutStopSec=120` on systemd.
At T+120 systemd sends `SIGKILL`, which is a crash, which invariant 1 covers. The
test for this is the fault-injection test at stage "job claim" in
`` §5 — **a graceful shutdown that silently loses a
claim is worse than a crash that does not, because the crash is tested and the
shutdown is not.** Until that test exists, graceful shutdown is a best effort and
the drain timeout is the real bound. Named, not worked around.

**Why not let workers finish indefinitely.** A worker blocked on an unreachable
VMS holds a shutdown open until the service manager kills it, and the site sees an
unclean stop. A worker that gives up and lets the lease expire is the better
failure, because the reclaim path is the tested one. So: **shutdown is bounded,
and the bound is short enough that the ordinary case never reaches it.**

---

## 5. Observability

### 5.1 The metric names

`internal/observability/metrics.go`, exactly as the plan states them, because
"a dashboard somebody hand-writes against a name this package invents is a
dashboard that quietly stops updating". The names are a closed set
(`knownMetrics`), a typo in a call site fails a test rather than creating a
metric no dashboard will read, and every counter is seeded at zero so a scrape
before the first event still reports every metric
(`TestCountersAreSeededAtZero`).

**Counters** (monotonic — a gauge that goes down is a gauge, and calling it a
counter means the alert fires on a reset and nobody investigates):

```
inbox_accepted_total            inbox_dead_lettered_total
canonical_events_total          corroborated_total
contradicted_total              unverified_total
vlm_invocations_total           vlm_errors_total
evidence_fetched_total          evidence_fetch_failed_total
actions_dispatched_total        dead_letter_visible_total
rule_bundle_activated_total     checkpoints_sealed_total
audit_appends_total
```

**Gauges** (current value, each with a `measuredAt` so a stale gauge is visible
as stale rather than as a real reading — `TestGaugesKnowWhenTheyAreStale`):

```
backlog_depth                   ingest_lag_seconds
outbox_pending                  job_queue_depth
clock_drift_ms                  dead_letter_count
evidence_disk_free_percent      incident_open_count
incident_degraded_count         ai_unavailable
clock_severe_device_count       postgres_conns_in_use
```

**The two rules about the format.** Counters are monotonic and gauges are not;
and **nothing here is on the availability critical path** — "observability that
can take the product down is a worse outage than the one it was watching, so
recording is best-effort and never blocks the pipeline."

**Label cardinality is capped** at 64 values per counter
(`TestLabelCardinalityIsCapped`): "A label value arriving from an untrusted source
is a way to allocate without bound. Past the cap the value is folded into a
single bucket, which still tells an operator something is being generated." A
device id or a zone id is untrusted input, so a per-device label is exactly the
case this rule is for.

**No metrics client is a dependency.** Prometheus is not in the tree because
`Registry.Render()` is "twelve lines of text", and "a metrics client on an
air-gapped site is a supply-chain decision somebody has to sign off on."

### 5.2 The gap, which is the most important sentence in this section

**`internal/observability` is imported by exactly one file: its own test.** There
is no production call site. Every metric name above is *named and never emitted*,
and there is no `/v1/metrics` endpoint — `/v1/health`, `/v1/health/ready` and the
`/static/{file}` handler are the only non-route handlers in `internal/api`.

So today, no operator can scrape a single number, and `01` M1's exit gate 3 —
"Every SLO in `internal/observability/slo.go` is demonstrably met under a recorded
load run" — cannot be met, because the SLOs evaluate against gauges that are
always zero. This is quality incident **Q-3** in ``
§11.4, and it is on the critical path of a milestone gate, which makes it the
highest-priority non-feature in M1.

The wiring is small: seven call sites (ingest, normalise, verify, evidence,
action, seal, adapter) plus a `GET /v1/metrics` handler over the exposition
`Render()` already produces. A new test must assert each metric is **reachable**
from a code path, not merely that its name is in the set —
`TestEveryPlannedMetricExists` currently passes for a registry nobody writes to.

**Until this ships, this document's SLO table is a specification and not an
observation**, and the release notes must say so rather than implying a dashboard
a customer does not have.

### 5.3 Log field standard

From `AGENTS.md`, without softening: "Go errors wrap with context; log lines carry
`tenant_id`, `site_id` and `request_id`. No secrets, tokens, credentials or raw
biometrics in logs. The audit ledger is separate from operational logs and is the
authoritative one."

| Rule | Why | The control |
|---|---|---|
| `tenant_id`, `site_id`, `request_id` on every line | A log line nobody can attribute to a site is a line nobody can act on at 03:00 | **Partly missing today** — §4.3's Q-9 |
| Go errors wrap with context | `fmt.Errorf("normalize: %w", err)` — a bare error at the top of a 12-frame stack is a wasted 20 minutes | review; and the panic handler's generic 500 body deliberately says nothing |
| No query strings in access logs | "a habit is a habit and a future endpoint that does would leak into every log line" | `logRequests` already omits it, with the comment |
| No secrets, tokens, credentials | A log file is a lower-protection store than a secret store, and it gets copied to a SIEM | `Config.DSN()` omits the password and is documented safe to log; `TestSecretMaterialIsRefused` scans release archives; the `contract-sync`-style check for the site config schema is `TestSiteConfigSchemaCannotCarryASecret` |
| **No raw biometrics** | a face crop in a log is biometric data under every regulation this customer base operates in, and a log is copied to places nobody enumerated | review, plus the hard rule in `` §8.4: no real biometric data in the repository *or in CI* |
| A refusal nobody can see is a refusal nobody can investigate | `auditDenied` writes `api.access.denied` to the **ledger**, not the log | `internal/api/server.go:289`; `TestEveryAuthEventIsAudited` |
| The 500 body says nothing about what failed inside | "an error cannot leak a stack by omission" — one `ErrorBody` shape | `WriteError` / `ErrorBody` |

### 5.4 SLO evaluation, and the remedy-per-objective pattern

`internal/observability/slo.go` returns, for each objective, a `Name`, a `Target`,
a `Breached` bool, a `Measured` string and — the pattern — a **`Remedy`**. The
comment: "Every SLO carries one, because an alert with no remedy is an alert
everybody learns to ignore." `TestEverySLOBriefCarriesARemedy` enforces it, and
that test is the one to copy when any new objective is added.

The eight objectives, and what each one's remedy says — which is where the real
operational content is:

| SLO | Target | Remedy, as written |
|---|---|---|
| `ingest_availability` | zero accepted events lost | "check the dead-letter list before anything else. A dead letter is an accepted event the platform could not process, and the reason is recorded on it" |
| `ingest_latency` | p95 under 5s | "a source is falling behind or the normalizer is saturated. Check the backlog gauge before touching anything else" |
| `incident_visibility` | p95 under 15s | "this is the AI path. A model that is slow is an availability problem for incident visibility and is not an SLO for ingest" |
| `ai_plane_availability` | **reported, never required** | "**none, by design.** The core keeps ingesting and verifying without it. This number is how a pilot measures what the AI plane actually contributed" |
| `ingest_backlog` | soft 5000, hard 50000 | "above the hard limit the platform is refusing to accept rather than dropping. Check the source feed first, then the normalizer" |
| `correlation_under_drift` | fewer than 1% of devices in the severe band | "excluded devices do not corroborate, so their events will read as UNVERIFIED. Check the drift view for which devices and how far off they are" |
| `dead_letter_visibility` | visible within 60s | "the console's dead-letter list is the fastest route. A dead letter is never dropped silently and replay is an operator action" |
| `evidence_disk` | 10% free, warning at 25% | "run a retention sweep before deleting anything by hand. A sweep that cannot check legal hold deletes nothing, so a dry run is safe" |

Three of these deserve a note:

- **`ingest_availability` is expressed as a count, not a percentage**, and the
  comment is the reason: "a percentage of zero is still a non-zero number of lost
  events, and an operator reading '0.0000% loss' should be suspicious of the
  measurement rather than reassured by it." That is the correct instinct and it
  generalises to the whole document: **at 03:00, precision that exceeds the
  measurement is worse than a round number.**
- **`ingest_backlog` is the one where the failure is safe.** "the platform is
  refusing to accept rather than dropping" — the soft/hard limits are a
  backpressure design, and an operator reading a breached backlog SLO should read
  it as *the system protecting itself*, not as a failure.
- **Two of the eight do not measure what their target says** —
  `correlation_under_drift` and `dead_letter_visibility`. Quality incident Q-1
  and Q-2 in `` §11.4, with the fix stated there (the
  code should match the stated target, not the reverse, because "a gate that
  fires on one bad camera in a 2,000-camera site trains the operator to ignore
  it").

### 5.5 The operator-facing health surface

Two endpoints, and the split between them is the design.

**`GET /v1/health` — liveness, and deliberately excludes the AI plane.**

```json
{"status":"ok","uptimeSeconds":8412,
 "aiPlane":{"note":"AI availability is not an SLO and never affects this status."}}
```

The comment on the handler: "Core liveness must not depend on something that is
explicitly not an SLO, or a model outage would page somebody about a security
system that is working." `TestHealthExcludesTheAIPlane` asserts the response
*says something about the AI plane*, with the reason: "an operator reading it
should be able to tell the AI plane was not consulted." A health endpoint that
silently omits the AI plane leaves the operator unable to distinguish "we did not
consult it" from "we have no idea".

**`GET /v1/health/ready` — readiness, with the AI runtime named but exempt.**

```json
{"ready": false,
 "checks": {"database":      {"ok": true},
            "evidenceStore": {"ok": true},
            "checkpointSink":{"ok": true, "detail": "not writable"},
            "migrationWindow": {"ok": false, "detail": "schema 14, binary supports 12..13"},
            "aiRuntime":     {"ok": false, "detail": "connection refused"}}}
```

`if ok, _ := entry["ok"].(bool); !ok { if name != "aiRuntime" { ready = false } }`
— a check named `aiRuntime` is informational and **never makes the core
unready**. That single name in that single condition is the enforcement of
invariant 3 at the process boundary: a stopped model cannot take a site off the
air, and a readiness probe that fired on it would be an operator learning to
ignore the probe.

**What the operator sees on the console, and it is more than the two endpoints.**
The console's incident page already carries the two operator-facing health
surfaces that exist: the **dead-letter table** and the **device clock drift
table**, with the copy that matters — "A device past the severe band is excluded
from time-windowed corroboration. The exclusion is recorded on the verification
result, not applied silently." Those two tables are the operator's health
dashboard and they are the most valuable thing on that page. M1 adds the SLO
panel: the eight objectives, each with its `Measured` string and its `Remedy`,
and — this is the design decision — **the remedy is shown, not just the
breach.** An operator who sees "evidence_disk BREACHED, 4.2% free" and nothing
else will page us; one who sees the same line plus "run a retention sweep before
deleting anything by hand. A sweep that cannot check legal hold deletes nothing,
so a dry run is safe" fixes it themselves.

### 5.6 How a site owner debugs without us

**The design constraint: the site owner must be able to answer 90% of "it is
broken" questions with the product, the playbook and the log files on their
machine, with no network to us and no engineer from this company.**

The four commands, in the order an operator should reach for them:

```
securityctl doctor              what is wrong with this installation, in one
                                screen, with remedies. Checks: version, config
                                parse, database reachable + schema window,
                                evidence root present and writable, checkpoint
                                sink present and writable, secret store readable
                                (presence only), migrations pending, rule bundle
                                compiled, disk free, clock, TLS material.
                                Exit code distinguishes "refusing" from
                                "failing" (TestExitCodesDistinguishRefusalFromFailure).

securityctl preflight            the same checks in dry-run form, before an
                                install or an upgrade touches anything.

securityctl audit verify        the hash chain, start to end. "chain broken at
                                sequence N" is a specific, actionable answer.

securityctl audit verify-seals  every signed checkpoint, offline, no database,
                                against the trusted public key. Refuses the same
                                seal under a different key. **This is the one
                                command a customer can run with our servers
                                switched off in our building, and it is the
                                command the whole product's claim rests on.**
```

**`TestDrillRunbookNamesRealCommands` exists because runbooks rot.** It holds a
closed list of commands and asserts the recovery runbook's steps are all in it,
with the comment: "a runbook step naming a command that does not exist is a wasted
minute at the worst possible moment." **Every runbook step in §8 is subject to the
same rule, and the list grows as `deploy/`, the installer and `securityctl model-bundle`
arrive.** Note the inverse finding: `internal/milestone/routes.go` references
`securityctl adapter probe` in five places and the command does not exist
(Q-4). That test's list should be generated from the command tree, not
hand-maintained, or it cannot catch exactly this.

**What the site owner is given, shipped in the release archive:**

| Artefact | Purpose |
|---|---|
| `securityctl doctor` output | the current state of the installation, in one screen |
| `docs/OPERATIONS.md` | the playbook, §8, one page per situation |
| `docs/RUNBOOK-*.md` | per-symptom runbooks, §7.6 |
| `/v1/health`, `/v1/health/ready` | for their monitoring, on their network |
| `/v1/metrics` | for their existing Prometheus, if they have one (M1, Q-3) |
| JSON logs | for their SIEM, in a format every SIEM reads |
| The audit ledger and the seals | for their compliance officer, verifiable offline |
| `securityctl incident explain` / `replay` | for their incident report, months later |
| The SLO panel's remedies | for 03:00, without a phone call |

**And what the site owner is explicitly told they can do without us:** stop the
service, read every log line, read every incident and every evidence item, verify
the audit chain and every seal, export an incident package, and take the
database's own backup. None of those require a licence check, a network call, or
our consent. A customer who suspects we are withholding something can check
everything we know how to check themselves, and that is the point of the
architecture.

---

## 6. Backup, restore, DR, and the honest finding

### 6.1 The finding

`docs/status.md` records it as a demonstration, not a risk:

> "a recovery drill that demonstrates the real finding: after a database loss the
> restored ledger is provably behind the seal, because a backup is a snapshot of
> some earlier moment."

`TestRecoveryDrill` in `internal/dr` is where it is proved, and the comment
explains the design consequence:

> "The seal names a sequence the restored ledger cannot produce, which is how
> somebody learns the restore is behind rather than assuming it is whole. The gap
> is exactly the window between the backup and the failure, and it is a real and
> irreducible property of backups — which is why the plan's answer is a sealed
> offline chain rather than a warmer database."

And the drill asserts the *finding*, not its absence: it checks `chainRows >= seal.ToSequence` and **fails if the restored ledger is not behind** — "the drill was supposed to demonstrate a restore that is behind, and it is not". A drill that stopped finding the gap would have stopped being a real drill, and the test is written to notice.

**So the honest statement, in the words we would give a customer:**

> A backup is a snapshot of some earlier moment. If the site fails after the
> backup was taken, the restored ledger is behind — and the system will tell you
> so rather than letting you assume it is whole. This is not a defect we can fix.
> It is a property of every backup system ever built, and the reason we seal to
> signed offline checkpoints is precisely so that the gap is *visible and
> measurable* instead of silent. The gap is the window between your last backup
> and the failure, and you can size it before you need it, because the seal tells
> you exactly where your ledger ends.

### 6.2 The seal reconciliation procedure

What an operator does with that information, and it has to be a procedure and not
a principle, because they will be doing it at 04:00.

```
SEAL RECONCILIATION — after any restore

  0  BEFORE RESTORING, preserve every seal file. Copy the checkpoint sink
     somewhere the restore cannot reach. A restore that runs before the seals
     are preserved loses the only witness to the gap. This step is not optional
     and it is the one most often skipped.

  1  securityctl audit verify-seals --key trusted.pub --dir <preserved sink>
        → verifies every seal offline, and refuses any under a foreign key.
        → Record the highest sealed sequence: SEQ_SEALED.

  2  securityctl audit verify
        → verifies the restored chain end to end.
        → If it FAILS, the restore is not merely behind, it is broken. Stop.
          Do not seal. Do not activate. Escalate. This is a different problem
          and it is a Sev-1.

  3  securityctl audit checkpoints
        → prints the ledger's highest sequence: SEQ_LEDGER.

  4  COMPARE.
        SEQ_LEDGER >= SEQ_SEALED   → whole. Nothing to reconcile. Record it.
        SEQ_LEDGER <  SEQ_SEALED   → BEHIND. The gap is
                                     SEQ_LEDGER+1 .. SEQ_SEALED
                                     and its size is the number of decisions
                                     and audit rows you do not have.

  5  WRITE IT DOWN, now, before anything else:
        date/time of the failure
        date/time of the last backup
        SEQ_LEDGER, SEQ_SEALED, gap size
        the last incident the restored ledger knows about
        the highest sealed incident

  6  TELL THE OPERATOR AND THE CUSTOMER'S COMPLIANCE OFFICER. The gap is
     a period during which the site has an audit ledger that does not account
     for events that demonstrably happened. That is a fact their compliance
     position depends on, and it is their decision what to do about it, not
     ours. **We do not make that call and we do not suggest they make it
     lightly.** What we give them is the exact size of the gap and the exact
     range of affected records.

  7  CLOSE THE GAP FORWARD, if possible. Re-point the adapters at the live VMS
     and let the system catch up: the inbox resumes, the dedupe keys stop it
     re-ingesting what the backup already has, and the ledger grows forward.
     The sealed range beyond SEQ_LEDGER cannot be reconstructed — those rows are
     gone with the database — but everything after the backup is recoverable,
     and the first re-ingested event after the restore is itself audited, so
     the ledger shows a discontinuity rather than pretending not to have one.

  8  SEAL AGAIN, from the restored ledger, as a new checkpoint.
     Two seals now exist for overlapping ranges. That is correct and expected,
     and verify-seals must handle it. The seal is a record of what the ledger
     said at a moment, and a restore makes the moments disagree. Documenting
     the disagreement is the point.

  9  RECORD IT IN THE INCIDENT. The recovery is an incident, it is in the
     ledger like everything else, and six months later somebody asking "was
     there a period we could not account for?" gets an answer from the product
     rather than from a memory.
```

**What we never do, and this is a product commitment, not a policy:**

- **Never report a restored site as healthy.** Not in the health endpoint, not
  in the console, not in a support ticket. A restored site is *reconciled* or it
  is *behind*, and the console says which.
- **Never silently re-seal over a gap.** Re-sealing without the reconciliation
  record would produce a seal that verifies and means nothing, which is worse
  than no seal.
- **Never tell a customer their ledger is fine** because the chain verifies. The
  chain verifies — that is a real and valuable property — and it says nothing
  about completeness. A verifying chain over a truncated ledger is the exact
  shape of the claim we exist to make trustworthy, and conflating "intact" with
  "complete" would be the single most damaging thing this product could do.

### 6.3 RTO and RPO, per site size

**Sizing inputs**, from `dbx.DefaultConfig()`'s comment — "the single-instance,
single-site profile from the capacity target: roughly 2,000 cameras, 50
alarm-generating devices, 20 events/sec":

| Class | Cameras | Alarm-generating devices | Sustained event rate |
|---|---|---|---|
| S — single-site commercial | 50–300 | 10–40 | ~2–5 events/sec |
| M — the design target | 500–1,000 | 40–50 | ~10 events/sec |
| L — the `DefaultConfig` target | ~2,000 | ~50 | ~20 events/sec |
| XL — outlier, priced and supported but not the target | 2,000+ | 50+ | 20+ events/sec, and the ingest rate is the first thing to profile |

**The targets.**

| | S | M | L |
|---|---|---|---|
| **RPO** (max data loss) | 1 hour | 1 hour | 1 hour |
| **RTO** (max downtime) | 4 hours | 4 hours | 8 hours |
| RPO with the optional continuous-archive addon | 5 minutes | 5 minutes | 15 minutes |
| Sealed-range gap after an RPO-sized loss | up to 1 hour of decisions, exactly measurable | same | same |

**The RPO is set by the backup window and nothing else**, and 1 hour is a
scheduling decision: `pg_dump --format=custom` hourly, retained 24 hourly + 14
daily + 12 monthly. The RTO is set by what a restore actually takes, and
`TestRecoveryDrill` measures it: re-migrate, `restoreRows`, chain verify. **The
restore time is dominated by the database restore, not by the product**, which is
why the RTO for L is double S's: a bigger database restores slower and the
evidence store is not in the path at all.

**These are targets, not measurements.** `01` M1's exit gate 3 requires every SLO
demonstrably met under a recorded load run with the numbers in the release notes,
and the same discipline applies here: **the RTO and RPO in this table are
**ESTIMATE**s from a sizing calculation, and they are not stated to a customer
until measured on a build of that size.** A published RTO we have not met is a
contractual claim in the `README.md` sense — "market claims, benchmark numbers
and vendor comparisons in the source material were never verified... and are not
asserted in the product, the docs or the code."

**What shrinks an RPO, and it is not "a warmer database".** A streaming standby
would cut the RPO to seconds, and it would also mean the site's most valuable
data — its audit ledger — is continuously copied somewhere, which for a customer
buying local-first provability is a worse answer than a stated one-hour gap. The
drill's own comment says it: "the plan's answer is a sealed offline chain rather
than a warmer database." The optional continuous-archive addon above is the
compromise for a customer whose compliance regime requires it, and it is a
**separate encrypted volume the customer owns**, not a database feature.

### 6.4 DR drill cadence

| Drill | Cadence | Who | Artefact |
|---|---|---|---|
| Automated: `TestRecoveryDrill` | **every `make test-race` run** — per commit | E2, automatically | green/red. The comment: "it is a test rather than a runbook so that it cannot quietly stop being true" |
| Automated: `TestDrillRunbookNamesRealCommands` | per commit | E2, automatically | the runbook's commands still exist |
| Full manual drill: total database loss, restore from a real backup, reconcile, seal | **Quarterly**, per milestone at minimum, and **once per design-partner site before that site goes to production** | E2, with the site owner invited | a dated report: timings, SEQ_LEDGER, SEQ_SEALED, gap, and the reconciliation record |
| Evidence-loss drill: destroy the evidence store, confirm what survives and what is reported | **Quarterly** | E2 | the answer must be "the DB rows point at absent objects and the console says so", not silence |
| Seal-loss drill: destroy the checkpoint sink, confirm the system is **still operable** and says so | **Semi-annually** | E2 | the accepted risk: a host reaching both the DB and the sink can lose both. The drill proves the *site keeps running*, which is the property we can defend |
| Key-rotation drill: rotate the checkpoint key, verify old seals under the old key and new under the new | **Per major** | E2 | `TestVerifyRejectsForeignKey` and `TestNewSignerFromKey` are the automated halves |

---

## 7. Support

### 7.1 Severity, from the customer's side

The severity definitions in `` §10 are ours. A customer
reports in their language and we map:

| They say | It is | Our commitment |
|---|---|---|
| "we missed something", "no alert came", "the system said there was nothing there" | **Sev-1** | §10.3's protocol. T+0.5h we tell them what we observed, before we understand it |
| "it's escalated everything", "we can't work, the queue is unusable" | **Sev-2** | T+1 working day to a mitigation |
| "the camera view is wrong", "the time is wrong on the timeline" | **Sev-2** if it affects a decision, **Sev-3** if cosmetic | |
| "how do I…", "where is the button" | **Sev-3** | Next working day, and the runbook gets the answer |
| "can it also do X" | a request, not a support ticket | `` §5.4's three-similar-requests rule |

**Mapping a "missed something" report to a Sev-1 without waiting to be sure is
deliberate.** The cost of treating a false alarm as a Sev-1 is one person's
worst week. The cost of treating a real missed intrusion as a Sev-3 is a
customer who never trusts the product again and tells everyone they know. At
this market's stakes, the asymmetry is not close, and the rule is: **when a
customer says we missed a real event, it is a Sev-1 until disproved.**

### 7.2 Response SLIs

| Severity | First response | Diagnosis | Mitigation | Root cause |
|---|---|---|---|---|
| Sev-1 | **≤ 1 hour** (automated detection, §7.3) | ≤ 2 hours | ≤ 4 hours | ≤ 5 working days |
| Sev-2 | ≤ 1 working day | ≤ 1 working day | ≤ 5 working days | ≤ 3 weeks |
| Sev-3 | ≤ 1 working day | 1 week | scheduled, with a date | scheduled |
| Request | acknowledged in 1 working day | — | entered in `docs/requests/` | three similar requests, then a backlog row |

The number we will hold ourselves to is the **first response**, and a first
response at 04:00 is a phone that rings. Which brings us to the next section.

### 7.3 Support hours — the honest version

**M1 and M2 (now → 2027-09-30): business hours, one person, explicitly.**

- Hours: **09:00–18:00 local to the person on the rota, weekdays, excluding that
  person's declared holidays.** A design partner in a different timezone gets the
  overlap, and we say so in the agreement rather than discovering it in month
  three.
- **A Sev-1 outside those hours is handled by the person on the rota waking up.**
  Not a rota with three people; two people cannot staff a 24/7 rota without one of
  them never sleeping. It is a phone, one person, and it is a real cost we pay
  and should price.
- **What the customer is told, in writing, in the design-partner agreement, in
  these words:**

> Support during the design phase is 09:00–18:00 on weekdays in [timezone].
> Out of hours, a Severity 1 issue — a real security event the platform did not
> raise, a lost event, an unverifiable audit record, or an action the platform
> took that it was not authorised to take — reaches one named engineer by phone
> at any hour. Anything else waits until the next working morning, and we will
> tell you that when you report it rather than after we have looked at it.

That paragraph is uncomfortable to write and it is the single most important
sentence in the contract, because the alternative is a customer discovering at
02:00 that nobody is coming, and a security company that does that once does not
get a second site.

**M3 GA onward (from 2027-10-01): a contracted 24/7 rota, priced, with an SLA.**

- A rota of at least four people, which is a hiring decision and therefore a
  business decision and therefore not something two people can improvise.
- **Priced into the licence, visibly.** A customer paying for 24/7 must see 24/7
  on the invoice, because the alternative is a 24/7 promise funded by a
  five-hour-a-day plan, and that ends badly for the customer.
- The response SLIs above become contractual at M3, with credits, because a
  contractual SLI we have not staffed is a liability.
- **Until that rota exists, we do not sell 24/7.** We would rather decline a deal
  than sign one. `00` §3's anti-segments are about what we become, and becoming a
  company that promises overnight response from two people is the same mistake in
  a different costume.

**The detection commitment is the part we can actually keep.** "≤ 1 hour to
first response" depends on somebody *knowing*. So the Sev-1 detectors must be
automatic and must not depend on a human noticing — the `dead_letter_count`,
`ai_unavailable`, `evidence_disk_free_percent` and `clock_severe_device_count`
gauges, the seal-verification result, and a nightly scheduled
`securityctl doctor` whose failure is an alert. A Sev-1 discovered by a customer
is a detection failure as well as a product failure, and it is logged as both
(`` §10.2).

### 7.4 Escalation to a named human

There is no escalation tree. There are two people, so the tree is:

```
  Tier 0   the site operator, using docs/OPERATIONS.md and securityctl doctor
           (§8). This resolves most things and it is the design goal.

  Tier 1   one named engineer, by the channel in the agreement, within the SLI
           for the severity. Named, not "the support team": two people cannot
           pretend to be a team, and a customer who is told "support" by somebody
           who is then on holiday has learned what "support" means here.

  Tier 2   the other engineer, automatically, when: the incident is a Sev-1, the
           incident commander is the engineer who is already deep in it (§10.3),
           or Tier 1 has said "I need help". The right to say those four words
           without a justification is the whole mechanism.

  Beyond   there is nobody. So the answer to "what happens if both of you are
           sick" is: the customer has the runbooks, the product degrades to
           deterministic-only operation (which is the point of invariant 3 — the
           site keeps working without us AND without the model), and the Sev-1
           waits. M3's 24/7 rota removes this answer entirely, which is the
           strongest argument for the hiring decision.
```

**The named-human rule, stated once:** every customer is given one named person
and one backup, by name, before the first production install. "The support team"
is a phrase that transfers responsibility to nobody, and a customer who cannot
name the person who answers their phone cannot escalate to anyone.

### 7.5 Runbook first

**The rule: a support question that is answered in chat and not written into a
runbook has not been answered.** The same session that resolves it writes the
runbook entry, and the entry goes into the release archive so the next site has
it. This is the mechanism behind `TestDrillRunbookNamesRealCommands`, and it is
why that test asserts against the command tree rather than against a document.

The consequence for the two of us: **the second time we answer the same question
is a defect in our documentation**, and it goes in the milestone's work items, not
in a good intention. At M3 with a 24/7 rota, an unwritten runbook is a person
waking up at 04:00 and improvising, and the customer finds out how well.

### 7.6 The runbooks we ship

In the release archive, readable as Markdown on a USB stick with no tooling.
Every one: a symptom the operator can recognise, the check, the fix, what the
fix costs, and what to do if the fix does not work.

| Runbook | Symptom it opens with | Audience |
|---|---|---|
| `RUNBOOK-service-down.md` | The console does not load | operator |
| `RUNBOOK-console-slow.md` | The incident list takes more than a few seconds | operator |
| `RUNBOOK-database-unreachable.md` | `doctor` reports the database down | operator, on-site IT |
| `RUNBOOK-disk-full.md` | `evidence_disk` breached; evidence fetches failing | operator |
| `RUNBOOK-dead-letters.md` | Dead letters are present and work is not being processed | operator |
| `RUNBOOK-clock-drift.md` | Devices in the severe band; incidents reading UNVERIFIED | operator |
| `RUNBOOK-model-unavailable.md` | The AI plane is down; incidents are degraded | operator |
| `RUNBOOK-noisy-rule.md` | A rule is generating a false-alarm campaign | supervisor |
| `RUNBOOK-upgrade-failed.md` | An upgrade stopped partway | on-site IT, with us on the phone |
| `RUNBOOK-rollback.md` | After an upgrade, go back | on-site IT |
| `RUNBOOK-restore.md` | After a database loss | on-site IT, with us on the phone |
| `RUNBOOK-seal-reconciliation.md` | The restored ledger is behind a seal | compliance officer + operator |
| `RUNBOOK-break-glass.md` | The only account with a second factor is gone | security manager |
| `RUNBOOK-evidence-integrity-failure.md` | A stored object does not match its hash | compliance officer |
| `RUNBOOK-security-incident-on-captain.md` | They think *we* were compromised | security manager, ours first |

---

## 8. The operations playbook

**For the site operator, at 02:00, without us.** One page each. The format is
always the same, because a person at 02:00 should not have to learn a new
structure for the seventh situation of the night:

```
  SITUATION     one sentence an operator can recognise
  IS IT US      is our product involved, or is this the site?
  SAFE?         what can they keep doing while they read this?
  CHECK         the exact command, and what a healthy answer looks like
  DO            numbered, one action per line, no alternatives
  DO NOT        the mistake that turns a nuisance into an incident
  IF IT PERSISTS  the escalation, with what to collect first
  COST          what this action loses, in the customer's terms
```

### 8.1 The product is down

```
SITUATION   The console does not load. The site has no events, no incidents.

IS IT US    Almost certainly. Check the power and the network first — both are
            more common than our software.

SAFE?      The VMS keeps recording and keeps raising its own alarms. This product
           does not control recording, does not hold the cameras, and does not
            sit in the video path. Nothing the cameras see is affected by us
            being down. Say this to whoever is awake.

CHECK      securityctl doctor

           All healthy       → the console, not the service. Go to 8.1a.
           database down     → RUNBOOK-database-unreachable.md
           evidence full     → RUNBOOK-disk-full.md
           schema window     → someone upgraded and something is mid-flight.
                               RUNBOOK-upgrade-failed.md. DO NOT re-run the
                               upgrade.

DO         1  securityctl doctor. Read the whole output before touching anything.
            2  If it names one fault, fix that one thing. Do not fix two.
            3  If the service is stopped, start it:
                 systemctl start captain-core          (Linux)
                 sc start captain-core                 (Windows)
            4  Wait 60 seconds. Then:
                 curl -k https://localhost:8443/v1/health/ready
               Healthy: {"ready":true,...}
            5  If it still fails, leave it stopped. A stopped service that we can
               look at is better than a crash-looping one that we cannot.

DO NOT      · Do not re-run an upgrade. A half-upgrade is the one situation
               where "try it again" makes it worse.
            · Do not delete anything from the evidence store. Ever. Not to free
               space, not to "reset" anything. The evidence store is the record.
            · Do not run a migration by hand to "catch up".
            · Do not restart the database to fix a service.

IF IT       Collect, then call: securityctl doctor output in full, the last 50
PERSISTS   log lines, and the incident that was open when it stopped. Do not
            debug over the phone; the doctor output is 90% of the diagnosis.

COST        Time with no AI correlation and no incident history. Video recording
            and the VMS's own alarms are unaffected.
```

### 8.1a The service is fine, the console is not

```
CHECK      curl -k https://localhost:8443/v1/health          → status ok
           curl -k https://localhost:8443/static/console.css → 200
           Does the browser show a CSP error in the console?

DO         1  Hard refresh. Ctrl+Shift+R, not F5.
            2  Clear cookies for the host and sign in again. Sessions are
               12 hours and a stale session looks like a broken page.
            3  Try a second browser. If it works, it is the browser's cache.
            4  Check the time on the workstation against the site. A browser
               refuses a certificate whose validity window has been missed, and
               the message it gives is unhelpful.
```

### 8.2 The model is unavailable

```
SITUATION   Incidents read UNVERIFIED, and the console says AI UNAVAILABLE with
            a reason and a time window.

IS IT US    Yes, and **this is a designed state, not a fault.** The deterministic
            core is still running.

SAFE?      **Yes. This is the case the product was designed for.** Incidents are
            still raised, still verified against stored facts, still evidenced,
            still audited, still dispositionable. What is missing is one input
            to corroboration. Read the incident yourself: the evidence is there
            and so are the other cameras.

            What changes: an incident that needed two independent sources where
            one was the model's reading is now UNVERIFIED rather than
            CORROBORATED. **Treat UNVERIFIED as "look at it", not as "nothing
            happened".** The product will never tell you a model outage means
            an event did not occur.

CHECK      curl -k https://localhost:8443/v1/health
             → aiPlane.note: "AI availability is not an SLO and never affects
               this status."
           securityctl doctor
             → does it name the AI runtime?

DO         1  Confirm via /v1/health that the core is ok. If yes, keep working.
            2  securityctl doctor. If the GPU box is unreachable, check power and
               network to that host — not the console host.
            3  If the model is slow rather than absent, check the VLM runtime's
               own log. The SLO for this is p95 under 15s to incident visibility.
            4  If you need a decision made now, use the evidence yourself. Every
               incident page has the observations that did and did not
               participate, and a "what it saw / what it did not decide" block.

DO NOT      · Do not switch the site to manual-only mode. There is no such mode;
               the deterministic core is already what you are in.
            · Do not dismiss incidents *because* the model is down. That
               inverts the invariant. The absence of an observation is not
               evidence of absence.
            · Do not restart the console host. The model runs elsewhere.

IF IT       Collect: the reason and window from the console banner, the GPU box's
PERSISTS   own log, and whether the failure is every incident or some.

COST        More incidents need a human eye. That is the whole cost. The
            evidence chain is unbroken and every decision remains replayable
            without the model.
```

### 8.3 Evidence storage is full

```
SITUATION   `evidence_disk` breached. Snapshots and clips are not being captured.

IS IT US    Yes. The site is not losing events — it is losing the pictures.

SAFE?      **Barely, and this is the most serious routine situation in this
            playbook.** Events are still arriving, still normalized, still
            audited. Incidents are still raised. They have no evidence attached.
            **An incident without its evidence is not defensible**, which means
            the decisions you make in this state are ones you could be asked to
            justify with nothing behind them.

CHECK      securityctl doctor
             → disk free percent
           curl -k https://localhost:8443/v1/metrics | grep evidence_disk_free
             → evidence_disk_free_percent

DO         1  RUN A RETENTION SWEEP FIRST. Never delete by hand.
               securityctl evidence sweep --dry-run
             The dry run tells you exactly what it would remove and deletes
             nothing. A sweep that cannot check legal hold deletes nothing at
             all, so the dry run is always safe.
            2  Read the dry-run output. If it proposes to delete something under
               legal hold, STOP — that is a legal question, not an
               operational one, and it needs your compliance officer.
            3  securityctl evidence sweep
            4  Check the free space again.
            5  Still low? Increase the volume. The evidence root is
               <statedir>/evidence and it can be on a larger disk from the next
               install. For now, is there space elsewhere on the host?

DO NOT      · Do not delete files from the evidence store by hand. Objects are
               content-addressed: two incidents may share one object, and a
               hand-deleted file makes both incidents reference something that
               is not there. The system will report the reference as broken —
               but the bytes are gone and the incident is weaker for ever.
            · Do not clear anything in the database to "free space". The
               database is not what is full.

IF IT       Collect: free space before and after the sweep, the dry-run output,
PERSISTS   and the retention configuration.

COST        Old evidence, per the retention policy and legal hold. This is the
            one routine operation in this playbook that permanently loses
            something, and it is why the dry run is step 1 and not an option.
```

### 8.4 The clock drifts

```
SITUATION   Devices in the severe drift band. Incidents read UNVERIFIED and
            corroboration is not happening.

IS IT US    It is the site's devices, not us. We *measure* this; we do not cause
            it.

SAFE?      Nothing is lost and nothing is being hidden. A device past the severe
            band is **excluded from time-windowed corroboration** and the
            exclusion is written on the verification result and shown on the
            incident page. You are seeing the exclusion, not a failure.

            What it means: we refuse to say two events were the same moment when
            the timestamps disagree too much. So the incident stays UNVERIFIED
            and a human looks at it. **The alternative would be a corroboration
            we later cannot defend.**

CHECK      The console's device clock drift table on the incidents page:
             device · site · drift ms · band · excluded from corroboration? ·
               measured at
           curl -k https://localhost:8443/v1/ops/clock-drift
           curl -k https://localhost:8443/v1/metrics | grep clock_severe_device_count

DO         1  Identify the devices. The drift table names them and how far off
               they are.
            2  For each one: is it a camera, a sensor, or the source system?
               · Cameras: NTP against the site's time source. Then
                 securityctl clock remeasure --device <id>   (M1)
               · The VMS itself: its drift is the source system's and
                 `securityctl clock` shows it separately. Fix it in the VMS.
            3  Re-measure. Drift is measured, not assumed.
            4  Watch the incidents flip from UNVERIFIED to CORROBORATED as the
               device comes back inside the band.

DO NOT      · Do not widen the site's skew tolerance to make the problem go away.
               The tolerance exists so a device at 40 seconds off does not
               produce a corroboration that cannot be defended. Raising it
               converts a visible fault into a silent inaccuracy, which is the
               exact opposite of what this product is for.
            · Do not "correct" a device timestamp by hand in the database. It
               would break the integrity hash and the audit chain.

IF IT       Collect: the drift table, which device kinds are involved, and
PERSISTS   whether the site's time source is reachable from that device.

COST        Slower verification and more human review until it is fixed.
```

### 8.5 A camera is moved

```
SITUATION   A camera has been physically relocated, re-aimed, or replaced. Its
            view no longer matches what the platform believes it is looking at.

IS IT US    Usually not. This is the site's world changing, and the platform's
            job is to notice rather than to assume.

SAFE?      Yes, and the system's behaviour is correct: **a camera in a new place
            sees a new set of things.** The graph still asserts the old zone
            relationships, so corroboration across the old adjacency is now wrong
            in a way the platform cannot detect on its own until M6's drift
            detection ships.

CHECK      · Does the camera's view still contain the zone it is mapped to?
            · securityctl device show --id <id>      → its zones, FoV, adjacency
            · The graph's zone paths feed the rule compiler, so a wrong camera
              position produces wrong corroboration, not a wrong camera.

DO         1  Record what changed, and when. This is a change to the site and
               somebody has to be able to account for it later.
            2  Re-map the camera:
                 securityctl device set-zones --id <id> --add <zone> --remove <zone>
               Every change is an edge with provenance, and revocation **hides**
               the old edge rather than deleting it — the graph keeps the record.
            3  Re-measure drift if the move also took the camera off its old
               network or power (§8.4).
            4  Review the last few hours of incidents from that camera. They were
               verified against a graph that was already wrong.

DO NOT      · Do not edit the graph rows directly. An edge with no provenance is
               refused (TestEdgeRequiresProvenance) and rightly so: a
               hand-inserted adjacency has nobody behind it.
            · Do not delete the old edges. Revocation hides them so the history
               of what the platform believed, and when, survives.

IF IT       Collect: the device, its old and new zones, when it was moved and by
PERSISTS   whom, and the incident list for that camera since the move.

COST        The period between the move and the re-map, during which incidents
            from that camera are evaluated against a stale position. This is a
            real accuracy gap until M6, and it is stated in the release notes
            rather than hidden.
```

### 8.6 A rule is noisy

```
SITUATION   A false-alarm campaign. A rule is generating incidents an operator
            dismisses.

IS IT US    Usually ours, and it is fixable in minutes without a release.

SAFE?      Yes, and dismissals are the *designed* feedback. But a feed an
            operator learns to dismiss is worth nothing, whatever its recall —
            so this is urgent even though nothing is broken.

CHECK      · How many of this rule's incidents were dismissed in the last shift?
            · securityctl rules simulate --bundle rules/builtin
            · securityctl rules explain --rule <id>     → what it matches and why

DO         1  Do NOT delete the rule. Deactivate it — the record stays.
               securityctl rules rollback          (to the previous version)
             or deactivate the specific rule in the next bundle.
            2  Count first. "Noisy" is a number, not a feeling:
                 how many raised, how many dismissed, over what period. The
                 profile's false-positive gate is 0.20 and the site's number is
                 the one that matters.
            3  Find the cause. In order of likelihood:
               · a camera moved, obstructed, or is showing something new (§8.5)
               · a door sensor stuck or a door left open all shift
               · a schedule that no longer matches the site's hours
               · clock drift on a device (§8.4)
               · the rule is simply too broad for this site
            4  Fix the cause, not the symptom. Suppressing the rule without
               fixing the cause means the same incidents arrive from a
               different rule next week.
            5  If it must be suppressed for now, that is a *site* decision and it
               is recorded: who suppressed what, when, and why. The console's
               "why am I seeing this" panel lists the suppressed events so a
               later reader sees them.

DO NOT      · Do not widen the condition. Loosening a rule to stop one camera's
               noise loosens it for every camera.
            · Do not leave it running "because it is important". An alert nobody
               reads is not protection; it is the reason the next real alert is
               dismissed too.
            · Do not delete the incidents. The false positives are the evaluation
               data that tells the next version of the rule what to fix.

IF IT       Collect: the rule id, the count, the dismissal outcomes, and the
PERSISTS   console's suppressed-event list for that rule.

COST        Time. And a measurement: this number goes into the site's
            false-positive rate, which is a metric we report on ourselves.
```

### 8.7 The site is under attack and the product must not be the weak link

```
SITUATION   The site is in a real security event. Cameras are being interfered
            with, an intruder may be present, and the platform is involved
            somehow — or the operator simply needs to be certain it is not.

IS IT US    Possibly. This page covers the case where the concern is us.

SAFE?      The single most important fact on this page: **Captain does not sit in
            the video path.** It reads events from the VMS over a network
            protocol and writes evidence to its own store. It does not hold the
            cameras, it does not hold the door locks, it does not record, and it
            cannot stop a VMS alarm from reaching a human. If we are down, the
            site's own security works exactly as it did before we arrived.

            Conversely, an attacker who compromises us gains our read access and
            our evidence store. They do not gain the building.

CHECK      In this order, because the first two are about the real event:
            1  What does the VMS itself say? Its alarms are independent of us.
            2  Are our incidents corroborating what the VMS reports, or
               contradicting it? Contradiction is a finding.
            3  securityctl doctor — is anything about *our* state anomalous?
            4  The audit ledger: has anything been modified? This is the one
               check that is genuinely ours and genuinely useful here.
                 securityctl audit verify
                 → "chain verified" means nobody with database access has
                   edited a row. That is a real, checkable, offline statement
                   about the integrity of our own record.
                 securityctl audit verify-seals --key trusted.pub
                 → same, back to the last sealed checkpoint, offline.

DO         1  Treat this as a real security event, with the site's own
               procedures, first. We are a source of evidence about it, not the
               response to it.
            2  Verify our record's integrity (above). Write down the result and
               the time, whether it passes or fails. **A verified ledger is
               evidence. A failed verification is an urgent finding, and it is
               ours to disclose — see §10.**
            3  Do not disconnect us mid-event. We are the thing that will tell
               the story afterwards, and a disconnected Captain has a gap in its
               ledger exactly when nobody is watching.
            4  If a camera is being interfered with, that is §8.5's answer plus
               the site's own: note the device, the time, the interference, and
               do not trust its view afterwards.
            5  Preserve. Do not run a retention sweep, do not rotate logs, do
               not upgrade, do not reboot anything for the rest of the event.

DO NOT      · Do not treat a Captain incident as the site's incident report. It
               is evidence *about* an event. The incident report is the site's,
               and ours is a supporting document with a hash on it.
            · Do not delete or edit anything. Nothing. Not an incident, not a
               disposition, not a log line, not a video. **The value of this
               product to a customer in this exact situation is that they can
               show somebody what happened without trusting anybody's memory,
               and every edit made now is a hole in that.**
            · Do not let a break-glass account be used to "just get in". If that
               is genuinely the only way, use it, and know that every use raises
               a high-priority alert and is in the ledger. There is no way to use
               one that leaves no trace, and the trace is the feature.

IF IT       What we need, and ask for it early: the site's own incident
PERSISTS   timeline, the VMS's alarms for the window, and the names of the
            people on site. We will need a site visit, and the sooner we know
            the sooner it happens.

COST        A site visit at short notice, and the cost of an engineer off
            roadmap work for a week. We pay it without discussing.
```

---

## 9. Capacity and cost per site

> **Everything in this section is an ESTIMATE.** The sizing *inputs* are real —
> `dbx.DefaultConfig()`'s own comment states the capacity target as "roughly
> 2,000 cameras, 50 alarm-generating devices, 20 events/sec", and the SLOs in
> `slo.go` give the real ingest and backlog numbers. Every number *derived* from
> them below is a calculation, not a measurement, and none of it may be stated to
> a customer or published until a recorded load run says otherwise
> (`` §11.3, and `README.md`'s evidence rule).

### 9.1 Sizing inputs (real)

| Input | Value | Source |
|---|---|---|
| Target site | ~2,000 cameras, ~50 alarm-generating devices, 20 events/sec sustained | `internal/dbx` `DefaultConfig()` comment |
| Peak factor | **ESTIMATE 4× sustained** (an alarm storm, a site event, or a rule change that fires broadly) | — |
| Sustained | 20 events/sec → ~1.7M events/day → ~621M events/year | ESTIMATE (arithmetic from 20/sec) |
| Canonical event row, ESTIMATE | ~0.6 KB with indexes | — |
| Outbox event row, ESTIMATE | ~0.3 KB | — |
| Audit ledger row, ESTIMATE | ~0.5 KB | — |
| AI observation row, ESTIMATE | ~1.5 KB (structured output, attribution, hashes) | — |
| **Evidence**, ESTIMATE | the dominant cost by two orders of magnitude. Snapshot ~180 KB, 8s clip ~1.2 MB (these two are the observed artefact sizes in the console's evidence table) | — |
| Evidence per incident, ESTIMATE | ~4 items → ~2.7 MB | — |
| Evidence per day, ESTIMATE | depends entirely on rule count, and this is the number a sizing conversation is actually about — see below | — |

**The evidence number is a policy decision, not a calculation.** At M1's incident
volume, evidence retention is dominated by *how many incidents a site has*, not
by how many cameras it has. The honest statement to a customer is a formula with
a measured constant, not a number:

```
  evidence/day = incidents/day × items_per_incident × mean_item_size × retention_days
```

with the first two measured per site and the last two configured. A site with 30
incidents a day and 30-day retention needs roughly 73 GB by these figures
(ESTIMATE); a site with 300 needs roughly 730 GB. **The rule pack, not the camera
count, is the variable that moves this by an order of magnitude**, and a sizing
conversation that starts from cameras and never reaches rules is a conversation
that produces a disk that fills in month three — which is `08` of the operations
playbook, and it is the failure mode the dry-run-first rule exists for.

### 9.2 The compute, ESTIMATE

| Component | At the L target (2,000 cameras, 20 ev/s) | Notes |
|---|---|---|
| **Application host** | 8 vCPU, 32 GB RAM, 100 GB OS+state | The pipeline is I/O-bound on the database, not CPU-bound. The VLM is the only compute-heavy part and it is on the GPU box |
| **Postgres** | 8 vCPU, 32 GB RAM, separate 500 GB NVMe | The real database figure comes from the load run, not this table |
| **Evidence store** | 1–8 TB, RAID-6 or better | See above. This is the item a customer budgets for and the one we most often get wrong |
| **GPU box (M1)** | 1× NVIDIA GPU with 16–24 GB VRAM, ESTIMATE | Model-agnostic by design (`01` M8's model marketplace). A smaller model on smaller hardware is a supported configuration, not a downgrade |
| **Console workstation** | whatever the operator already has | We do not sell hardware. `01`'s anti-segments: not a hardware ecosystem |

**`dbx`'s connection defaults are sized for exactly one of these:** `MaxOpenConns
32`, `MaxIdleConns 8`, `ConnMaxLifetime 30m`, `StatementTimeout 15s`,
`HealthCheckInterval 30s`. 32 connections is a small pool for a 20 ev/s ingest
rate, and that is the correct posture — a small pool that queues is visible
through `postgres_conns_in_use` and `backlog_depth`, whereas a large pool that
exhausts the server's memory takes everything down at once. **If the load run
shows the pool is the bottleneck, the answer is more app hosts, not a bigger
pool**, because the state that must be serialised (the audit chain, per tenant)
does not parallelise anyway.

### 9.3 The cost, ESTIMATE

Not a price list — a shape, so that the commercial conversation starts from the
same place the engineering conversation does. Public list prices at the time of
writing, subject to change, and **not to be quoted to a customer without checking
current pricing**.

| Item | Role | Note |
|---|---|---|
| Application host | Appliance or customer's existing server | Often the customer's own hardware. `01`'s position is local-first, not appliance-selling |
| Postgres | The authoritative store | Free and open. The cost is the disk and the ops |
| Evidence storage | The dominant cost | The number a customer is actually surprised by, and the reason §8.3 is a page |
| GPU box, M1 only | The AI plane | **Optional.** A site runs correctly without it, at the cost of degraded verification. This is the single clearest expression of invariant 3 in a budget: the AI plane is a line item a customer can decline, and declining it does not produce a product that cannot work |
| Our licence, M2 | Paid pilot | Per site, with §3.3's ADR deciding whether it expires |
| Our support, M3 | 24/7 rota | A visible line item on the invoice (§7.3). Priced in, not absorbed |

**The commercial shape this produces, and it is deliberate:** a customer can run
the entire product — ingest, verification, evidence, incidents, operator
disposition, audit ledger, sealed checkpoints, replay — on hardware they own,
with no GPU, and get every claim the product makes. The GPU buys corroboration
quality. **If the GPU were required, the trust claim would be a claim about a
purchase decision, and it would be worth less.**

### 9.4 What must be measured, and when

The ESTIMATE labels come off, in this order, as `01` M1 gate 3 and its successors
require:

| # | Measure | When | Method |
|---|---|---|---|
| 1 | Sustained event rate the host actually handles at p95 < 5s | M1 | the load generator, with the run and its numbers in the release notes |
| 2 | Backlog behaviour at and beyond the hard limit (5,000/50,000) | M1 | deliberately exceed it, confirm the system refuses rather than drops |
| 3 | Database growth per day per class of site | M1 | a recorded run, then a projection stated as a projection |
| 4 | The evidence-per-incident constant, measured at 3 design partners | M2 | 30 days of real data, per site |
| 5 | VLM latency and VRAM per profile, on the customer's hardware | M1 gate 4 | the evaluation report |
| 6 | Restore time, per site class | M1, then per quarter | `TestRecoveryDrill` extended to a real backup at real volume |
| 7 | Concurrent console users | M3 | the exception queue is a shared surface; a shift of 6 on one screen is not one user |

**Until items 1, 2, 3 and 6 are measured, the capacity and RTO figures in this
document are labelled ESTIMATE and are not to be printed on a proposal.**

---

## 10. When our product is wrong

### 10.1 The disclosure policy, in one paragraph

**If the product made a decision that was wrong, and that decision affected a
customer's understanding of a security event, we tell them — before they find
out, and before we understand it.** Not a changelog entry. Not "this was fixed in
1.4.1". A notification, to a named person, with what we observed, what we believe,
what we are doing, and when we will next update. The severity, the clock and the
protocol are `` §10.3's, and the T+0.5h line is
"**TELL THE CUSTOMER. Before we understand it. This is not optional and it is not
negotiable.**"

**The reason is not virtue. It is that a customer's entire decision to buy us
rests on our records being true, and the fastest way to destroy that is to be
caught having hidden something.** A customer who finds a Sev-1 in their own data
and learns we knew is finished, permanently, as a reference — and references are
the entire M1 and M2 commercial strategy.

### 10.2 What we tell a customer

Five sections, every time, in this order. The template is short enough to be used
at 04:00.

```
WHAT HAPPENED
  The observable fact, in plain language, with times and ids. No cause yet, and
  no hedging. "Between 02:14 and 02:31 the platform raised 4 incidents from
  camera CAM-7 and dismissed 3 of them without operator input. Operators later
  confirmed one of the three was a real intrusion."  ← say it that plainly.

WHAT IT MEANS FOR YOUR RECORDS
  The part only we can answer, and the reason they bought us:
  · Which incidents are affected, by id, with their dispositions
  · Which audit rows cover them and which do not
  · Whether any of it is reconstructable, and how
  · Whether any notification you sent or received from us was wrong
  · Whether the sealed checkpoints are affected
  **If a wrong disposition sent an escalation to somebody, say that they need
  to know, and say it in the first section.**

WHAT WE ARE DOING
  Containment, in order, with the blast radius of each. Rollback, deactivated
  bundle, dropped autonomy level, stopped action queue. The customer should be
  able to check the containment themselves.

WHAT WE DO NOT YET KNOW
  Honestly, and specifically. "We do not yet know whether the second incident was
  also a real intrusion." A vague "we are still investigating" is worse than an
  admitted unknown, because it leaves them planning around a question we have
  not answered.

NEXT UPDATE
  A time, and a person. Not "as soon as we know". A date and a name, and we
  meet it or we tell them before it passes.
```

### 10.3 Correction

Correction is a **product change, not a communication.** A notification is
owed; a fix is what makes the second notification unnecessary.

```
  1  the Sev-1 protocol, §10.3 of 07
  2  the test that fails when the control is removed, in the same change as the
     fix. If the test is not there, the fix is not finished.
  3  the ADR, in the order AGENTS.md requires: what we believed, why it was
     wrong, what changed, what test now holds it
  4  a migration if the bad data is persisted. **A wrong incident, a wrong
     disposition and a wrong action are all rows in a hash-chained ledger, and
     they are not editable.** A correction is a superseding row — the same
     mechanism as a re-evaluated observation
     (TestReEvaluationSupersedesRatherThanOverwrites) and the same rule as the
     UI's undo: both replay, the earlier one does not disappear. A migration
     that *edits* history would break invariant 4 to fix an invariant-2
     problem, and it is never the answer.
  5  every other customer who ran the affected version is told, whether or not
     they were affected. A defect that only bit one site is a defect that
     *appears* to have bitten one site, and the customers who were not affected
     are the ones who will wonder whether we checked.
  6  the retrospective at T+30d, and the class of defect it belongs to: design,
     review, test coverage, or honesty about what was verified. The fix is
     different in each case, and naming it is the whole point.
```

**The rule about historical data, stated once because it is the hardest call we
will face:** *we do not rewrite a customer's history.* If our product recorded
something wrong, that record stays, and what changes is that a later record
says it was wrong and points at why. This is uncomfortable for a customer whose
report needs to be clean. It is the same property that makes the record worth
anything: a system that can be edited after the fact is a system nobody has to
trust, only believe, and "believe" is not a category this product sells into.

### 10.4 Coordinated disclosure and the security intake

**A security researcher is the most valuable person who will ever contact us, and
the handling of that contact is a product feature.** A security product that
handles a vulnerability report badly earns a reputation it cannot spend.

**`/.well-known/security.txt`** — served from the site, shipped in the release
archive, and present on the company site:

```
Contact: mailto:security@captain.security
Expires: 2031-01-01T00:00:00.000Z
Preferred-Languages: en
Canonical: https://captain.security/.well-known/security.txt
Policy:   https://captain.security/security-disclosure
```

**PGP.** A published public key at
`https://captain.security/security-disclosure.asc`, fingerprinted in
`security.txt` and printed in the release notes, whose fingerprint is also
**published in the release archive's `docs/`** so a researcher with only a USB
stick can verify it. The key is used for: security reports, licence grant
activation (§3.4), and the disclosure notifications of §10.3 — including
notifications to customers, so a customer can verify that the message claiming
to be from us is from us and not from someone who found our address. A disclosure
notice that cannot be authenticated is a disclosure notice an attacker can forge.

**The policy, in the terms it is published in:**

> If you have found a vulnerability in Captain, we want to hear from you, and we
> will not threaten you or your employer for finding one.
>
> **What to do.** Email `security@captain.security` with the PGP key above. Tell
> us what you found, how to reproduce it, and what an attacker gets. You do not
> need to have a PoC, and you do not need to be right.
>
> **What we will do.** We will acknowledge within **2 business days** and give
> you a tracking reference. We will not share your name with anyone without your
> written permission, including in the advisory. We will keep you informed at
> least every two weeks until it is fixed, even when there is no news. When a
> fix ships, we will tell you first, before the advisory is public, and credit
> you if you want to be credited.
>
> **Timing.** We ask for **90 days** from our acknowledgement before public
> disclosure, and we will not go to court to extend it. We may publish sooner if
> a customer is being harmed, and we will tell you at the time. If you find
> something that is being exploited, skip us and tell the affected customers
> directly — that is not a breach of this policy, it is the right thing to do.
>
> **Safe harbour.** We will not pursue legal action for good-faith research that
> respects customer data: no access to production data, no denial of service, no
> social engineering of staff or customers, no persistence, and no disclosure
> before the period above. If you are unsure whether something is in scope, ask
> first; we will answer in writing and we will hold you to that answer.
>
> **What is in scope.** The product as shipped: the release verifier, the
> installer, the console, the API, the adapter, the evidence store, the rule
> engine, the migration runner, the model-bundle path. Customer configurations
> and sites are not in scope and you must not test against one without written
> permission — ask us and we will arrange a test site.
>
> **What is out of scope.** Findings that require an administrator to install
> something hostile first; denial of service by volume; missing hardening headers
> on a deployment the administrator configured; and anything a customer
> configured insecurely on purpose. Tell us about these anyway; we will tell you
> honestly whether we will fix them.
>
> **Bounties.** We do not run a bug bounty programme yet. We will say so
> plainly rather than imply otherwise, and we will credit every report in
> release notes with your permission. **When we can afford one, we will announce
> it here first.**

**Response SLIs for security reports, which are the same as the product's:**

| Stage | Commitment |
|---|---|
| Acknowledge | 2 business days |
| First assessment (is it real, what is the blast radius) | 5 business days |
| Fix or mitigation plan | 15 business days for a mitigation, 45 for a fix |
| Notification to affected customers | within 24 hours of a confirmed fix, **before** the public advisory if the fix requires them to act |
| **A report of an actively exploited vulnerability** | **1 hour, any hour, any day** — the same page that a Sev-1 uses |

**Disclosure to customers, and the priority order.** If our product is
vulnerable and a customer is affected, the customer is told **before** the
advisory is public, because the advisory's audience is the research community
and the customer's audience is an operator at 02:00 who needs to know their
evidence chain is incomplete. We tell affected customers first, the community
second, and the general public last — and the ordering is a commitment we can
keep because we have the customer list.

**A disclosure is also a release note.** Every security fix appears in the
release notes with a plain description of the issue and its impact, whether or
not it has a CVE. `01` M8's marketplace and the OEM programme both depend on the
advisories being legible to a reseller's customer, and a bare CVE number is
useless to somebody who is not running a vulnerability scanner.

---

## 11. The state of the release story, honestly

| Piece | State | Reference |
|---|---|---|
| Signed release builder and verifier | ✅ works | `internal/release`, 13 tests, `docs/status.md` |
| `verify-seals` offline with no database | ✅ demonstrated | `docs/status.md` |
| Recovery drill producing the restore-behind-seal finding | ✅ as a test | `internal/dr`, `TestRecoveryDrill` |
| Runbook command names asserted against real commands | ✅ as a test | `TestDrillRunbookNamesRealCommands` |
| Metric names defined | ✅ | `internal/observability/metrics.go`, closed set |
| **Metrics emitted; `/v1/metrics`** | ❌ **not wired** — Q-3 | no production call site; the observability package is imported by its own test only |
| SLO evaluation with remedy per objective | ✅ as code | `internal/observability/slo.go` |
| **SLOs observed** | ❌ — Q-3 and gate 3 | no values to evaluate |
| **Two SLOs measure something other than their stated target** | ❌ Q-1, Q-2 | `slo.go:115-122`, `:127-134` |
| **HTTP access log carries `tenant_id` / `site_id` / `request_id`** | ❌ Q-9 | `internal/api`'s `logRequests` omits all three |
| Windows Service unit | ❌ deferred | `deploy/` created and removed |
| systemd unit | ❌ deferred | as above |
| `install` / `upgrade` / `rollback` / `backup` / `restore` | ❌ skeletons that refuse with a named reason | `docs/status.md`; specified in §2.3 |
| OCI profile | ❌ not started, and §4.1 explains why | `README.md` mentions it in the `deploy/` row |
| Load generator | ❌ not started | needed for gate 3 |
| `securityctl adapter probe` | ❌ referenced in 5 places, does not exist | `internal/milestone/routes.go`; Q-4 |
| **CI can build the module as pinned** | ❌ Q-7 | `go.mod` says 1.26.0, CI pins 1.24 with `GOTOOLCHAIN: local` |
| Licence activation design | ❌ **not decided** | §3.3; ADR required before M3 GA |
| `security.txt`, PGP key, disclosure policy | ❌ not started | §10.4 |
| Support hours, contracted | ❌ business hours, written down in §7.3 | 24/7 from M3, priced |

**Eleven of the twenty-one lines are incomplete, and six of them are on the path
to an M1 exit gate.** That is the honest state of the release story, and it is
consistent with `docs/status.md` and with `` §2.1's
finding that M1's backlog is roughly 2.5× the available capacity. The M1
reduced-scope cut is applied to items 31–34 and 36–37 (the installer and the
service units) per `01`, and the fault-injection harness
(`` §5) is the last thing cut and never below the four
stages that have a partial substitute.

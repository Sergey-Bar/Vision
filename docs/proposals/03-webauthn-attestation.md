# Proposal 03 — Attestation trust chain for WebAuthn enrolment

**Status:** proposal. Nothing is implemented. A human decides.

## The gap

`docs/status.md`, under "What is not done, and why":

> **Attestation trust chain for WebAuthn.** Registration, challenge binding,
> COSE key validation and ES256/EdDSA signature verification are implemented and
> tested. There is no attestation certificate chain check: the replay defence
> here is the authenticator's sign counter, not the certificate, and the
> enrolment command says so rather than leaving an operator to assume it.

That last clause is the right instinct and should be preserved. What is
proposed here is not a change to the replay defence; it is the option to add a
second, independent one, and to stop maintaining a hand-rolled COSE parser to do
it.

## Why we need it

`internal/identity` contains 60 references to WebAuthn, attestation and COSE.
Per the status document, the implemented-and-tested surface includes **COSE key
validation and ES256/EdDSA signature verification** — that is a COSE parser,
maintained in-tree, in the component that holds identity data.

Two costs follow, and both are ordinary:

1. **Correctness surface.** A hand-written COSE parser is exactly the kind of
   code that is subtle, security-relevant, and permanently yours to maintain
   against a specification that has three levels and keeps gaining formats.
2. **A stated gap with no upgrade path.** The document is candid that no chain
   check exists. Candidty is right; the question is whether closing it is
   intended, and if so, why not from a conformant implementation.

## The candidate

**`go-webauthn/webauthn`** — **BSD-3-Clause**, v0.18.0, Go 1.25, forked from
`duo-labs/webauthn`.

It is FIDO2 conformant and conformance-tested against the FIDO conformance
tools. Its attestation table is the reason it is relevant:

| Format | Status |
|---|---|
| Packed (§8.2) | supported, spec level 3 |
| TPM (§8.3) | supported, level 3 |
| Apple Anonymous (§8.8) | supported, level 3 |
| Compound (§8.9) | supported, level 3 |
| Android Key / SafetyNet, FIDO U2F, None | supported |

It also ships `metadata.Provider` for verification against the **FIDO Metadata
Service (MDS3)**, which is the mechanism for deciding whether a given
authenticator model is acceptable — the thing an attestation chain is *for*.

The successor repository is explicitly maintained; the original is marked
**deprecated**.

## What we would build

Nothing immediately. The first step is a decision, not a dependency:

1. Establish whether attestation is in scope for this product at all. For an
   operator console where the authenticator is the operator's own phone or
   hardware key, the sign counter may genuinely be the right and sufficient
   defence — and `status.md` already argues exactly that.
2. **If** it is in scope, evaluate `go-webauthn/webauthn` against the existing
   implementation with a written comparison, not a spike.
3. **Only then** decide whether to replace the in-tree COSE parser, and what
   happens to the existing tests.

## What we would not do

- **Not add the dependency speculatively.** An unused security dependency is a
  permanent upgrade obligation and a permanent CVE surface for a capability
  nobody uses. That is a net loss if the decision in step 1 is "not needed".
- **Not weaken the existing enrolment message.** If attestation is never
  verified, the operator-facing text must keep saying so. Adding the library
  must not remove that sentence unless the chain check is genuinely enforced.
- **Not change what is stored.** Credential storage and the `ProtocolValue` flag
  pattern are already right per the library's own guidance; this proposal does
  not touch them.

## Risks

- **A dependency on the identity path.** `internal/identity` may not import
  `internal/api`; nothing says the reverse about third-party packages. But this
  is the code that gates operator access, so it deserves the highest bar in the
  repository and a correspondingly high review burden.
- **MDS is a network dependency in an offline product.** This product installs
  and runs at sites with no network. An MDS-backed policy check must degrade to
  a pinned, vendored metadata blob, and that decision belongs in the threat
  model, not in the integration.
- **False assurance.** The library's own docs note that verifying a credential
  against metadata is domain logic it deliberately does not implement. Adopting
  it does not by itself close the gap `status.md` names.

## Confidence

High that the library exists, is maintained, is BSD-3, and supports the formats
listed — read from its README. Low confidence that we should adopt it, because
the case for adoption depends on a product decision this proposal cannot make.

## Decision required

Is attestation a requirement for this product? If no, close this and record the
reason. If yes, the MDS-in-an-offline-site question must be answered before the
library is chosen, not after.

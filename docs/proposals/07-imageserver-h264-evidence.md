# Proposal 07 — ImageServer H.264 for evidence capture

**Status:** proposal. Nothing is implemented. A human decides.
**Licence of the reference project is unverified. This blocks adoption.**

## The gap

`internal/evidence` is a content-addressed filesystem store and the only code
that touches the evidence root. It stores whatever is handed to it. What is
handed to it, on the incident path, is a snapshot pulled from the VMS.

Every route in `internal/milestone/routes.go` returns JSON, and two of them
carry a media reference: `ImagePath` on the event feed, and the video recording
start time. None of them returns a frame. The adapter has no video path at all,
and `routes.go` models no ImageServer endpoint.

So the likely current shape is JPEG-per-event. That is a size decision with a
retention consequence: an incident archive is a database dump plus an evidence
directory, and `securityctl backup` records whether the two are self-consistent
because an archive without the evidence is an incident citing bytes that are not
in it. Evidence volume is therefore a first-class cost in every site.

## The candidate

**`conticomp/xprotect-export`** — MIT status **not verified**; this is the first
thing to check and it is a hard gate.

Its claimed advantage is specific and large: pulling **raw H.264 through the
ImageServer protocol** instead of decoded JPEG frames, with **codec
passthrough** — no re-encode. Its README states roughly 3.8× smaller output and
near-instant export versus JPEG, with a JPEG fallback where passthrough is not
available.

The second thing worth borrowing is architectural, not technical. The README
documents that XProtect requires **two different tokens**: an OAuth token from
`/API/IDP/connect/token` for REST calls, and a separate ImageServer token
obtained via a SOAP `Login()`, in the form
`TOKEN#guid#hostname//ServerConnector#…`. Our route table models no
authentication whatsoever. If evidence capture is built, that dual-token reality
has to be designed in from the start, not discovered later.

## Where the same knowledge exists, more safely

`milestonesys/mipsdk-samples-protocol` (MIT, vendor-published) contains a
`TCP Video Viewer` sample demonstrating the ImageServer protocol. It is a
reference implementation of the protocol, without a third party's opinions
about export pipelines.

That is the better starting point for a protocol, and the other project is the
better starting point for the *efficiency claim*, because it is the one making
the claim.

## What we would build

Nothing yet. In order:

1. Verify the licence of `conticomp/xprotect-export`. If it is not permissive,
   this proposal reduces to the MIP sample and the size question is reopened.
2. Read the ImageServer protocol from the vendor's `TCP Video Viewer` sample.
3. Prototype against a real installation and **measure** the size and latency
   difference. The 3.8× figure is the vendor-of-the-tool's claim about its own
   tool, and `README.md` of this repository requires unverified benchmark
   numbers to be marked as such with a source and a date. That applies here too.
4. Only then decide whether evidence capture joins the adapter or lives beside
   it as a separate `evidence` concern.

## What we would not do

- **Not add an FFmpeg dependency to the Go core.** `internal/evidence` is
  currently pure Go and content-addressed. Transcoding inside it would make the
  evidence store's identity depend on a codec.
- **Not store re-encoded video as evidence.** The evidence store is
  content-addressed precisely so that the bytes can be re-verified. If capture
  transforms the media, the original is gone and the hash no longer describes
  what the operator saw.
- **Not pull raw video for every event.** That is a bandwidth and storage
  decision the rules engine should make, which means it is a bundle and contract
  change, not an adapter change.

## Risks

- **Evidence on the critical path.** A large evidence path that can stall is a
  path that will stall. `AGENTS.md`'s invariant 1 is about accepted events, but
  the same discipline applies: capture is an asynchronous concern and must not
  sit between `inbox` and `normalize`.
- **Codec availability.** H.264 passthrough depends on how the recording server
  stored the stream. Dual-codec sites, exotic sub-streams and MJPEG cameras will
  exercise the fallback path, and the fallback path needs its own test.
- **The claim is unverified.** Everything above is contingent on step 3, and
  step 3 has not happened.

## Confidence

The architectural point — XProtect needs two token types for media — is high;
it is documented by two independent sources. The efficiency claim is
**low and second-hand**, and is the entire reason this proposal exists.

## Decision required

Is evidence size a real constraint at pilot scale, or is a per-event JPEG
acceptable? Measure the current archive footprint on one real site first; if
the number is fine, this proposal closes.

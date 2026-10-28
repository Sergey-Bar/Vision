# syntax=docker/dockerfile:1
#
# Two images, one gate.
#
# Targets:
#   runtime   the product. Three binaries on a base with no shell, no package
#             manager and no passwd entry to get wrong.
#   verify    the gate. The SAME scripts/verify.ps1, run under PowerShell on
#             Debian, so the gate that decides whether this product is sound is
#             the gate a judge can run with one command.
#
# There is deliberately no `verify-linux.sh` and no `.github/workflows`. Both were
# considered and both would have been a second gate, and a second gate is a gate
# that drifts: it agrees with the first one until somebody edits only the first.
# `scripts/verify.ps1` is the authority (see AGENTS.md) and the fix when it breaks
# on Linux is to make the PowerShell portable.
#
# `poc/Dockerfile` is a separate file for a separate reason: the proof of concept
# needs ffmpeg and libonnxruntime, which is several hundred megabytes of C
# libraries this product never loads. The product must stay small enough to ship
# to an air-gapped site, and "small enough to ship" is a property you lose by
# making one image carry both.

# --- pinned inputs ------------------------------------------------------------
#
# Digests, not tags, and the digest is the MANIFEST LIST's rather than one
# platform's — so the pin holds on arm64 as well as amd64 and a rebuild on
# another machine resolves to the same bits.
#
# golang is pinned to the minor the module declares. `go.mod` says `go 1.26.0`,
# and a builder below that directive is a hard build failure rather than a
# warning, so this ARG and `go.mod` have to move together (AGENTS.md says so and
# means it).
ARG GO_IMAGE=golang:1.26-bookworm@sha256:a688600ca24f8a4d3ca77f95b0dd40704a9fc787c826660eb7ba0b641b8b175d
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

# --- the product --------------------------------------------------------------
FROM ${GO_IMAGE} AS build

# CGO_ENABLED=0 is not a size optimisation here, it is a claim: the product
# cross-compiles to linux/amd64 from a Windows workstation with no C toolchain,
# which means there is no cgo in any build path and therefore no libc dependency
# to satisfy and no `.so` to ship. The one file that is Windows-only is
# `internal/pipeline/disk_windows.go`, which has a linux sibling and is selected
# by build tag rather than by runtime detection.
#
# The claim used to be that no build path contains cgo AT ALL. It no longer is,
# and the difference is the detector: `internal/ortdetect` loads ONNX Runtime
# through cgo, so the module has one package that needs a C compiler and one that
# refuses by name without one. `CGO_ENABLED=0` selects the refusing build --
# `internal/ortdetect/session_nocgo.go` -- so this image ships a product whose
# detector says `ortdetect.NoCgoRefusal` rather than one that silently has no
# detector. The product image is the SERVER; the detector runs in the VMS
# emulator, which is a separate process with its own image.
ENV CGO_ENABLED=0 \
    GOFLAGS=-mod=readonly

WORKDIR /src

# The whole tree goes in before the module cache is warmed. The dependency
# graph changed shape when the detector moved out of the POC module: this module
# no longer has a `replace` pointing at `./poc/active-monitoring`, and the POC
# module now has one pointing at THIS module -- the direction is the other way
# round, because the detector, the taxonomy and the sidecar wire moved into the
# product and the POC reached them through `shared/`.
#
# A local replace is resolved during the module graph read, so a
# dependency-first layer that copies only `go.mod`/`go.sum` and runs
# `go mod download` fails outright when one is present. The split is still worth
# most of what it was worth: `COPY . .` is the layer a source edit invalidates,
# so the module cache still survives one, because `go mod download` now sits
# *after* the copy it needed. `-mod=readonly` still means a go.mod that
# disagrees with go.sum fails the build rather than silently rewriting either —
# `AGENTS.md` calls a hand-edited dependency list a human-review item.
COPY . .
RUN go mod download

# The console bundle is embedded into the security-core binary by
# `//go:embed consoleui` in web/routes.go, so the SPA has to be built
# before `go build`. Node is not part of the runtime image; it only needs
# to live in this build stage long enough to produce web/consoleui/dist.
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends nodejs npm ca-certificates; \
    rm -rf /var/lib/apt/lists/*; \
    cd web/spa && npm ci && npm run build

# -buildvcs=false because the build context may or may not carry a .git
# directory, and a build whose binary stamp depends on which context someone sent
# is a build nobody can reproduce. -trimpath for the same reason in a different
# place: an absolute build path inside a binary is both a leak and a source of
# "works on my machine" for anything that reads paths out of a stack trace.
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ENV GOOS=${TARGETOS} GOARCH=${TARGETARCH}

RUN --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    set -eu; \
    for cmd in security-core securityctl security-migrate security-seed; do \
      go build -buildvcs=false -trimpath -ldflags "-s -w" \
        -o "/out/${cmd}" "./cmd/${cmd}"; \
    done; \
    ls -l /out

# Empty directories for the runtime image to copy. 0700, because their contents are
# the AES key and the console's private key; see the runtime stage for why they
# exist at all.
RUN mkdir -p /staging/run /staging/evidence && chmod 0700 /staging/run /staging/evidence

FROM ${RUNTIME_IMAGE} AS runtime

# `/out` rather than copying each binary: four COPY lines is four places to forget
# one when a fifth command is added.
COPY --from=build /out/ /usr/local/bin/

# Dispatch destinations: rules ask for notify_operator / notify_supervisor and
# the core refuses to start until a destination is configured for each target.
COPY deploy/dispatch-destinations.yaml /etc/security-platform/dispatch-destinations.yaml

# The rule bundle and the committed fixtures, because the seed reads them and
# `securityctl rules lint` and `rules simulate` accept a path. They are data the
# release carries, not build inputs — a site install gets the same bundle the
# activation digest was computed against.
COPY --from=build /src/rules/ /opt/security-platform/rules/
COPY --from=build /src/testdata/ /opt/security-platform/testdata/
# The three committed clips live in the POC's testdata. They are committed
# fixtures of THIS repository — the plan names all three — and importing the POC's
# Go types to read a `.zones.json` is not worth a dependency, so the media is
# copied rather than the module.
COPY --from=build /src/poc/active-monitoring/testdata/ /opt/security-platform/clips/

# The two directories the product writes into, owned by the nonroot user.
#
# distroless has no shell, so there is no `RUN mkdir` here. There is also no
# reason to want one: a named volume created at a path the IMAGE already contains
# inherits that directory's contents AND its ownership, which is the only way to
# get a volume owned by uid 65532 without a shell, an init container, or a chown
# step that would be a third thing to remember.
#
#   /run/security-platform            0700. The MFA secret key and the console
#                                    private key live here, and `identity`'s
#                                    keystore REFUSES a key file readable by
#                                    anyone else — mode 0400 — so a directory
#                                    owned by root at 0755 would make the product
#                                    refuse to start rather than weaken the check.
#   /var/lib/security-platform/evidence  the evidence root. A named VOLUME in
#                                    compose, because three containers share it:
#                                    the seed writes the bytes and the core reads
#                                    them, so it cannot be per-process private.
#
# These are named volumes and not tmpfs because the key has to be SHARED: the seed
# generates it, and `core` and `console` both read it. A per-container tmpfs would
# give each of them a different key, which fails in a way that looks like a
# decryption bug. `docker compose down -v` throws them away; plain `down` does not.
COPY --from=build --chown=65532:65532 /staging/run /run/security-platform
COPY --from=build --chown=65532:65532 /staging/evidence /var/lib/security-platform/evidence

USER 65532:65532
VOLUME ["/var/lib/security-platform/evidence"]

ENV SP_EVIDENCE_ROOT=/var/lib/security-platform/evidence \
    SP_LISTEN_ADDR=0.0.0.0:8443 \
    SP_RULES_BUNDLE_PATH=/opt/security-platform/rules/builtin \
    SP_SEED_FIXTURES=/opt/security-platform/testdata/fixtures \
    SP_SEED_CLIPS=/opt/security-platform/clips \
    SP_SEED_BUNDLE=/opt/security-platform/rules/builtin \
    SP_LOG_FORMAT=json

EXPOSE 8443

# A healthcheck is a command run INSIDE the container, and this base has no shell,
# no curl and no busybox — verified, not assumed: `docker run --entrypoint
# /busybox/sh <this image> -c true` fails with "no such file or directory". So the
# probe is a binary that is already here, which is the same move `grpc_health_probe`
# makes for a gRPC service. `--ca` is the site's own certificate rather than a
# blanket "do not verify", because a security product whose probe cannot verify a
# certificate is a probe that reports the wrong server as healthy.
#
# The liveness endpoint, not readiness: a container that restarts because a
# downstream dependency is briefly unavailable turns one slow dependency into an
# outage. `--ready` is what an orchestrator readinessProbe should ask.
HEALTHCHECK --interval=10s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/usr/local/bin/securityctl", "healthcheck", \
       "--url", "https://127.0.0.1:8443", \
       "--ca", "/run/security-platform/console.crt", \
       "--timeout", "4s"]

ENTRYPOINT ["/usr/local/bin/security-core"]

# --- the gate -----------------------------------------------------------------
#
# Deliberately NOT built from the runtime image. The gate needs a compiler (for
# `-race`), `gofmt`, `protoc`, `protoc-gen-go`, `golangci-lint` and Python, and
# layering those onto the runtime base would defeat the point of the runtime base
# and ship a compiler to sites that must not have one.
FROM ${GO_IMAGE} AS verify

ENV DEBIAN_FRONTEND=noninteractive \
    CGO_ENABLED=1 \
    GOFLAGS=-mod=readonly

# PowerShell, from Microsoft's own package repository, which is where Microsoft's
# documentation says to get it.
#
# Microsoft's method is to install their repo *config* package and then install
# powershell from it. That was not the first thing tried here and the reason is
# worth recording, because each wrong attempt fails with an apt error that does not
# name the mistake:
#
#   * The suite is the CODENAME. The live tree is
#     `debian/12/prod/dists/bookworm/` — the URL path is the release number and the
#     suite is `bookworm`. `.../12/prod 12-packages` is a 404 that apt reports as
#     "does not have a Release file".
#   * A component IS required by Debian 12's apt 2.6, even though Microsoft's own
#     documented line has none, and it reports that as
#     "Malformed entry 1 ... (Component)" rather than as a missing field.
#   * `debian/bookworm/prod bookworm-prod`, which the URL shape suggests, is also a
#     404. Two of the three guesses are plausible and both are wrong.
#
# So the config package is used instead of a hand-written line: it carries the key
# AND the list file, so there is nothing here to get wrong, and its SHA-256 is
# pinned so a compromised mirror cannot substitute a repository configuration for
# this build.
#
# The key it installs is verified by apt from the signed Release file, which is the
# property a tarball from a release page does not have.
# `.proto` files. Debian splits them: `protobuf-compiler` installs `protoc` and
# nothing else, so `contracts/protobuf/security/v1/action.proto`'s
# `import "google/protobuf/timestamp.proto"` fails with "File not found" and the
# generated-code gate reports a missing dependency as a tree that does not match its
# generated output. The Windows protoc carries its own copy of the well-known types
# inside its distribution directory, which is why this gate has passed on a
# workstation for the whole life of the repository and fails in a container. The
# split is invisible until it is not.
ARG MS_REPO_DEB_SHA256=8434dcb8c346dc95fbd63dbece056c343704590b58b6a5c323d39acf52bf0b48
# `ffmpeg` here and NOT in the product image, for one reason.
#
# Four tests skip when they cannot find a decoder: `internal/mp4`'s two
# "readable by an independent decoder" cases and its byte-cap case, and
# `securityctl`'s clip test. `internal/skipaudit` then fails the run, because an
# undeclared skip is a failure — which is correct, and it is how this was found.
#
# Those tests are the only thing in the gate that checks this repository writes H.264
# a decoder agrees with. Running the gate without a decoder means the gate passes on a
# tree whose video output nobody has decoded, so the decoder belongs in the GATE and
# the hundreds of megabytes do not belong in the product. That split is the reason
# there are two targets rather than one.
#
# `gcc` and `libc6-dev` are here for cgo, which the gate needs and the product does
# not. The `test` gate runs `-race` (unless `-NoRace`), the race detector is cgo
# only, and `go run ./cmd/security-migrate` fails at link time without them. The
# product image sets `CGO_ENABLED=0` and needs none of this; the separation is the
# point of having two targets.
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      ca-certificates curl gnupg \
      python3 \
      gcc libc6-dev \
      postgresql-client \
      ffmpeg \
      unzip; \
    . /etc/os-release; \
    curl -fsSL -o /tmp/ms-prod.deb \
      "https://packages.microsoft.com/config/debian/${VERSION_ID}/packages-microsoft-prod.deb"; \
    echo "${MS_REPO_DEB_SHA256}  /tmp/ms-prod.deb" | sha256sum -c -; \
    dpkg -i /tmp/ms-prod.deb; \
    rm /tmp/ms-prod.deb; \
    cat /etc/apt/sources.list.d/microsoft-prod.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends powershell; \
    rm -rf /var/lib/apt/lists/*

# protoc, pinned by content hash to the release the workstation's protoc is.
#
# NOT Debian's `protobuf-compiler`, for two reasons that only appear when the gate
# is run somewhere it has never been run.
#
#   1. Version. `protoc` writes its own version into the generated file's header, so
#      Debian's 3.21.12 against a file generated by 36.0 makes the generated-code
#      gate report a clean tree as stale. The gate is right to do that: the point of
#      the check is that the committed bytes are what this toolchain emits.
#   2. The well-known types. `action.proto` imports
#      `google/protobuf/timestamp.proto`. Debian splits those out of
#      `protobuf-compiler` into `libprotobuf-dev`, so the compiler alone fails with
#      "File not found" — a missing dependency reported as a mismatched tree. The
#      release zip carries its own `include/` directory with them, which is why the
#      Windows protoc has never had this problem and only the container did.
#
# SHA-256 rather than a digest: GitHub release assets have no OCI manifest to pin a
# digest against, so the published hash is the strongest available statement that
# this build got the bytes it asked for.
ARG PROTOC_VERSION=36.0
ARG PROTOC_SHA256=bc8211ce760bd43ee21ddc145d6d9dbaeeabae205267a79d9054a240e367d4b4
RUN set -eux; \
    curl -fsSL -o /tmp/protoc.zip \
      "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-x86_64.zip"; \
    echo "${PROTOC_SHA256}  /tmp/protoc.zip" | sha256sum -c -; \
    rm -rf /usr/local/include /usr/local/bin/protoc; \
    unzip -q /tmp/protoc.zip -d /usr/local; \
    rm /tmp/protoc.zip; \
    # Unzip already carried the executable bit, so there is no `install` here to
    # set it. There was one, `install -m 0755 /usr/local/bin/protoc
    # /usr/local/bin/protoc`, which is a file installed onto itself — install says
    # so and exits 1, and the build log for that is four screens of unrelated apt
    # output ending in the word `protoc`.
    chmod 0755 /usr/local/bin/protoc; \
    # Prove the well-known types came with it, rather than discovering it as a
    # "File not found" three gates later.
    test -f /usr/local/include/google/protobuf/timestamp.proto; \
    protoc --version

# golangci-lint, pinned by version.
#
# `internal/deadexport` and the lint gate both treat the tool's findings as
# evidence. A floating `latest` would make the findings of two runs incomparable,
# which for a gate means "it passed yesterday" is not a claim anybody can make.
#
# The pin below is the release `scripts/verify.ps1` resolves; if you change one,
# change the other. verify.ps1 reports the version it used in the JSON record for
# exactly this reason.
ARG GOLANGCI_LINT_VERSION=v2.14.0
RUN set -eux; \
    arch="$(dpkg --print-architecture)"; \
    # The RELEASE TAG carries a leading `v` and the ASSET does not:
    # `.../download/v2.14.0/golangci-lint-2.14.0-linux-amd64.tar.gz`. Using the tag
    # verbatim in the asset name is a 404, and curl's exit 22 with no message is a
    # long way from "you doubled a letter". The tag is used for the path and the
    # bare version for the filename, which is the only place in this file where
    # those two differ.
    asset="${GOLANGCI_LINT_VERSION#v}"; \
    curl -fsSL -o /tmp/lint.tar.gz \
      "https://github.com/golangci/golangci-lint/releases/download/${GOLANGCI_LINT_VERSION}/golangci-lint-${asset}-linux-${arch}.tar.gz"; \
    tar -xzf /tmp/lint.tar.gz -C /tmp; \
    install -m 0755 "/tmp/golangci-lint-${asset}-linux-${arch}/golangci-lint" \
      /usr/local/bin/golangci-lint; \
    rm -rf /tmp/lint.tar.gz "/tmp/golangci-lint-${asset}-linux-${arch}"; \
    golangci-lint --version

# protoc-gen-go, pinned to the same release the repo's generated code was built
# with. `go install` with an explicit version rather than `@latest`, for the same
# reason as above.
ARG PROTOC_GEN_GO_VERSION=v1.36.12
RUN GOBIN=/usr/local/bin go install \
      "google.golang.org/protobuf/cmd/protoc-gen-go@${PROTOC_GEN_GO_VERSION}" && \
    protoc-gen-go --version

WORKDIR /src
COPY . .

# The gate writes `dist/verification` and needs the tree it is gating to be the
# one it just copied. It also mutates source files and restores them — that is
# what the red-check table does — so the tree must be writable, which it is here
# because this stage never runs as nonroot.
RUN pwsh -NoProfile -Command \
      '$ErrorActionPreference = "Stop"; \
        if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw "no go" }; \
        if (-not (Get-Command pwsh)) { throw "no pwsh" }; \
        if (-not (Get-Command protoc)) { throw "no protoc" }; \
        if (-not (Get-Command golangci-lint)) { throw "no golangci-lint" }; \
        if (-not (Get-Command ffmpeg)) { throw "no ffmpeg: four tests would skip" }; \
        "verify image toolchain:"; \
        (go version); \
        (protoc --version); \
        (golangci-lint --version); \
        (pwsh -NoProfile -Command $PSVersionTable.PSVersion.ToString())'

ENV SP_EVIDENCE_ROOT=/var/lib/security-platform/evidence \
    PATH="/usr/local/go/bin:${PATH}"

ENTRYPOINT ["pwsh", "-NoProfile", "-File", "/src/scripts/verify.ps1"]
CMD ["-NoRace"]

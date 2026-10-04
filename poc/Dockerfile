# syntax=docker/dockerfile:1
#
# The proof of concept's image. Separate from the product's, on purpose.
#
# The two differ by several hundred megabytes of C libraries. The product builds
# `CGO_ENABLED=0` onto a base with no libc at all; this one links against
# `libonnxruntime.so`, which is the whole point of a POC that runs a detector. If
# they shared an image the product would ship a 300 MB ONNX runtime to an air-gapped
# site that has no model, and "small enough to ship" is a property you lose by
# making one image carry both.
#
# Targets:
#   runtime   pocctl, poc, watch, fixtureprobe, injectionfixture on debian-slim,
#             with ffmpeg and libonnxruntime.so.
#   verify    the POC module's OWN gate, `poc/active-monitoring/scripts/verify.ps1`,
#             run under PowerShell. Its 77 red-check controls live in
#             `redcheck-transcripts.txt` beside that script and nowhere else, which
#             is the arrangement `internal/treestate` asserts for a second module.
#
# Neither target copies the product's Go module. The POC has its own `go.mod`,
# its own dependency versions, and its own 77 controls; sharing a build context
# would mean `go build ./...` at the root could reach a package the POC's gate
# never sees.

ARG GO_IMAGE=golang:1.26-bookworm@sha256:a688600ca24f8a4d3ca77f95b0dd40704a9fc787c826660eb7ba0b641b8b175d
ARG RUNTIME_IMAGE=debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251

# onnxruntime, pinned by content hash.
#
# GitHub release assets carry no OCI manifest, so there is no digest to pin the way
# the product's base images are pinned. The published SHA-256 of the archive is the
# strongest available statement that this build got the bytes it asked for.
#
# The version is 1.23.0 because `pocctl`'s own `runtime_test.go` uses
# `libonnxruntime.so.1.23.0` as its example of a versioned SONAME, and because
# `internal/ortdetect`'s `session_cgo.go` loads the library `cmd/pocctl
# fetch-runtime` verified. Changing one of those without the other is how a POC ends
# up with a decoder that produces a different number from the one in the report.
ARG ONNXRUNTIME_VERSION=1.23.0
ARG ONNXRUNTIME_SHA256=b6deea7f2e22c10c043019f294a0ea4d2a6c0ae52a009c34847640db75ec5580

# --- the build ----------------------------------------------------------------
FROM ${GO_IMAGE} AS build

ENV CGO_ENABLED=1 \
    GOFLAGS=-mod=readonly

WORKDIR /src
COPY poc/active-monitoring/go.mod poc/active-monitoring/go.sum ./
RUN go mod download
COPY poc/active-monitoring/ ./

RUN --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    set -eu; \
    for cmd in pocctl poc watch fixtureprobe injectionfixture; do \
      go build -buildvcs=false -trimpath -ldflags "-s -w" -o "/out/${cmd}" "./cmd/${cmd}"; \
    done; \
    ls -l /out

# --- the runtime --------------------------------------------------------------
FROM ${RUNTIME_IMAGE} AS runtime

# glibc is here because this image is the one that needs it: the POC's binaries are
# cgo, linked against libonnxruntime, and cannot be made static the way the
# product's are.
#
# `ca-certificates` is here because an air-gapped site that will never reach the
# internet should not carry a trust store that implies it might.
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      ca-certificates \
      ffmpeg \
      python3 \
      libgomp1; \
    rm -rf /var/lib/apt/lists/*

# ffmpeg is not optional here, and the POC's gate says so in the same words:
# "D2 makes it a hard runtime dependency: without it there is no source, no recording
# and no frames. The tests that need it FAIL rather than skip, on purpose." So it is
# installed here for the same reason the verify image installs it, and the reason
# the product image does not is the size, not the dependency.

ARG ONNXRUNTIME_VERSION
ARG ONNXRUNTIME_SHA256
RUN set -eux; \
    arch="$(dpkg --print-architecture)"; \
    case "$arch" in \
      amd64) ortarch=x64 ;; \
      arm64) ortarch=aarch64 ;; \
      *) echo "no ONNX Runtime build published for $arch" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /tmp/ort.tgz \
      "https://github.com/microsoft/onnxruntime/releases/download/v${ONNXRUNTIME_VERSION}/onnxruntime-linux-${ortarch}-${ONNXRUNTIME_VERSION}.tgz"; \
    echo "${ONNXRUNTIME_SHA256}  /tmp/ort.tgz" | sha256sum -c -; \
    mkdir -p /opt/onnxruntime/lib; \
    tar -xzf /tmp/ort.tgz -C /tmp; \
    install -m 0755 "/tmp/onnxruntime-linux-${ortarch}-${ONNXRUNTIME_VERSION}/lib/libonnxruntime.so.${ONNXRUNTIME_VERSION}" \
      "/opt/onnxruntime/lib/libonnxruntime.so.${ONNXRUNTIME_VERSION}"; \
    # The unversioned name is what `ort.SetSharedLibraryPath` is given, and the
    # SONAME inside the library is the versioned one, so the loader needs both.
    ln -s "libonnxruntime.so.${ONNXRUNTIME_VERSION}" /opt/onnxruntime/lib/libonnxruntime.so; \
    ldconfig /opt/onnxruntime/lib; \
    rm -rf /tmp/ort.tgz "/tmp/onnxruntime-linux-${ortarch}-${ONNXRUNTIME_VERSION}"; \
    # Prove the library loads, rather than discovering a link error in the first
    # detector call three stages later.
    test -r /opt/onnxruntime/lib/libonnxruntime.so

COPY --from=build /out/ /usr/local/bin/
# The committed inputs, and nothing else from the tree. `pyworker/` and not a glob:
# the Python lives there and not at the module root, and this file's first two
# attempts guessed at paths that do not exist — `rules/` and `contracts/`, both of
# which are not directories in this module — and each failure named the path and
# not the reason, which is a waste of a build.
COPY poc/active-monitoring/testdata/ /opt/poc/testdata/
COPY poc/active-monitoring/pyworker/ /opt/poc/pyworker/

ENV SP_POC_ORT_LIB=/opt/onnxruntime/lib/libonnxruntime.so \
    SP_POC_RUNTIME_ROOT=/opt/poc \
    PATH="/usr/local/bin:/opt/onnxruntime/lib:${PATH}"

WORKDIR /opt/poc
EXPOSE 8554

# `pocctl` has no long-running health endpoint; it is a CLI and a source. The
# image's smoke test is that the binary runs and the library resolves, which the
# build stage already asserted, so there is no HEALTHCHECK here to be decorative.
ENTRYPOINT ["/usr/local/bin/pocctl"]

# --- the gate -----------------------------------------------------------------
FROM ${GO_IMAGE} AS verify

ENV DEBIAN_FRONTEND=noninteractive \
    CGO_ENABLED=1 \
    GOFLAGS=-mod=readonly

# Same reasoning as the product's verify target, and the same two mistakes worth not
# repeating: PowerShell comes from Microsoft's signed apt repository (its config
# package, SHA-256 pinned, because a hand-written `.list` line got the URL path, the
# suite and the component wrong on the first attempt and each failure is an apt error
# that does not name the mistake), and `protobuf-compiler` alone would not carry the
# well-known `.proto` files.
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      ca-certificates curl gnupg \
      ffmpeg python3 \
      gcc libc6-dev unzip; \
    . /etc/os-release; \
    curl -fsSL -o /tmp/ms-prod.deb \
      "https://packages.microsoft.com/config/debian/${VERSION_ID}/packages-microsoft-prod.deb"; \
    echo "8434dcb8c346dc95fbd63dbece056c343704590b58b6a5c323d39acf52bf0b48  /tmp/ms-prod.deb" | sha256sum -c -; \
    dpkg -i /tmp/ms-prod.deb; \
    rm /tmp/ms-prod.deb; \
    apt-get update; \
    apt-get install -y --no-install-recommends powershell; \
    rm -rf /var/lib/apt/lists/*

ARG ONNXRUNTIME_VERSION
ARG ONNXRUNTIME_SHA256
RUN set -eux; \
    curl -fsSL -o /tmp/ort.tgz \
      "https://github.com/microsoft/onnxruntime/releases/download/v${ONNXRUNTIME_VERSION}/onnxruntime-linux-x64-${ONNXRUNTIME_VERSION}.tgz"; \
    echo "${ONNXRUNTIME_SHA256}  /tmp/ort.tgz" | sha256sum -c -; \
    mkdir -p /src/models; \
    tar -xzf /tmp/ort.tgz -C /tmp; \
    cp "/tmp/onnxruntime-linux-x64-${ONNXRUNTIME_VERSION}/lib/libonnxruntime.so.${ONNXRUNTIME_VERSION}" /src/models/; \
    ln -s "libonnxruntime.so.${ONNXRUNTIME_VERSION}" /src/models/libonnxruntime.so; \
    rm -rf /tmp/ort.tgz; \
    test -r /src/models/libonnxruntime.so

# The tree bind-mounted by compose, not copied: the POC's gate mutates source files
# for its 77 red-check controls and restores them, and a gate that restores into a
# snapshot of the tree rather than into the tree itself has nothing to prove.
WORKDIR /src
ENTRYPOINT ["pwsh", "-NoProfile", "-File", "/src/scripts/verify.ps1"]
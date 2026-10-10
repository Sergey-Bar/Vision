#!/bin/sh
# The Security Core's script runner: backend, no binary in the checkout.
#
#   ./scripts/run-core.sh [-listen host:port] [-tenant T] [-site S] [-integration I]
#                          [-evidence DIR] [-cert F -key F | -behind-proxy -trusted-proxy C]
#                          [-smoke] [-open]
#
# It starts the core with `go run ./cmd/security-core` and waits for
# /v1/health. Nothing is compiled into the repository or into an output
# directory: `go run` builds into the toolchain's own cache and runs the
# result, so what an operator keeps is source. This is the script replacement
# for `go build -o security-core.exe ./cmd/security-core` — which on a Windows
# development machine is also the difference between a runnable command and a
# file Windows may block or quarantine.
#
# The core's own configuration comes from the environment, exactly as it does
# under the installed service: SP_TENANT_ID, SP_SITE_ID, SP_INTEGRATION_ID and
# SP_EVIDENCE_ROOT are required by its own validation, and this script refuses
# by name when one is missing rather than letting the process die part-way
# through a boot.
set -eu

cd "$(dirname "$0")/.."

LISTEN=127.0.0.1:8443
TENANT=dev-tenant
SITE=dev-site
INTEGRATION=dev
EVIDENCE=""
SMOKE=0
OPEN=0
CERT="${SP_TLS_CERT:-}"
KEY="${SP_TLS_KEY:-}"
BEHIND_PROXY_RAW="${SP_BEHIND_PROXY:-}"
TRUSTED_PROXY="${SP_TRUSTED_PROXY_CIDRS:-}"
while [ $# -gt 0 ]; do
    case "$1" in
        -listen)       LISTEN="${2:-}"; shift 2 ;;
        -tenant)       TENANT="${2:-}"; shift 2 ;;
        -site)         SITE="${2:-}"; shift 2 ;;
        -integration)  INTEGRATION="${2:-}"; shift 2 ;;
        -evidence)     EVIDENCE="${2:-}"; shift 2 ;;
        -cert)         CERT="${2:-}"; shift 2 ;;
        -key)          KEY="${2:-}"; shift 2 ;;
        -behind-proxy) BEHIND_PROXY_RAW=true; shift ;;
        -trusted-proxy) TRUSTED_PROXY="${2:-}"; shift 2 ;;
        -smoke)        SMOKE=1; shift ;;
        -open)         OPEN=1; shift ;;
        -h|--help)     sed -n '2,28p' "$0"; exit 0 ;;
        *) echo "run-core.sh: unknown argument '$1' (see --help)" >&2; exit 2 ;;
    esac
done
[ -n "$EVIDENCE" ] || EVIDENCE="$(pwd)/data/evidence"

# The toolchain is not on PATH on a default Windows shell, and the container
# image puts it in /usr/local/go/bin. Resolve it by name rather than assuming.
GO=go
if ! command -v go >/dev/null 2>&1; then
    for candidate in /usr/local/go/bin/go /usr/lib/go/bin/go; do
        if [ -x "$candidate" ]; then GO="$candidate"; break; fi
    done
fi
if ! command -v "$GO" >/dev/null 2>&1 && [ ! -x "$GO" ]; then
    echo "run-core.sh: the Go toolchain was not found. Install Go 1.26+ or put it on PATH." >&2
    exit 1
fi

# The core's own required set, refused by name before the process starts.
missing=""
[ -n "$TENANT" ] || missing="$missing TENANT(SP_TENANT_ID)"
[ -n "$SITE" ] || missing="$missing SITE(SP_SITE_ID)"
[ -n "$INTEGRATION" ] || missing="$missing INTEGRATION(SP_INTEGRATION_ID)"
[ -n "$EVIDENCE" ] || missing="$missing EVIDENCE(SP_EVIDENCE_ROOT)"
if [ -n "$missing" ]; then
    echo "run-core.sh: the core will not start without:$missing" >&2
    echo "              tenant_id is never read from a request body, so there is nowhere else for it to learn these." >&2
    exit 1
fi

if [ -n "$CERT" ] && [ -z "$KEY" ]; then
    echo "run-core.sh: -cert is set but -key is not; a certificate with no key is a refusal." >&2
    exit 1
fi
if [ -n "$KEY" ] && [ -z "$CERT" ]; then
    echo "run-core.sh: -key is set but -cert is not; a key with no certificate is a refusal." >&2
    exit 1
fi
# The operator's string is opaque and is passed through: config.go's boolEnv
# (1/true/yes/on, case-insensitive) is the single judge, and a runner that
# re-implements the token set answers differently from the process it starts.
# The script only needs to know whether to poll http or https, which follows
# from the certificate, not from the flag.
if [ -z "$CERT" ] && [ -z "$BEHIND_PROXY_RAW" ]; then
    # The core refuses this itself, and it is right to: serving plaintext with
    # nothing upstream declaring TLS is a posture, and the script does not get
    # to choose one on the operator's behalf.
    echo "run-core.sh: the core refuses plaintext with no declared TLS posture. Pick one:" >&2
    echo "              -cert <file> -key <file>     serve HTTPS with a certificate" >&2
    echo "              -behind-proxy -trusted-proxy ..  declare a TLS-terminating proxy and its CIDRs" >&2
    echo "              The second is a statement about the deployment, not a dev-mode toggle." >&2
    exit 1
fi
if [ -n "$BEHIND_PROXY_RAW" ] && [ -z "$TRUSTED_PROXY" ]; then
    echo "run-core.sh: -behind-proxy needs -trusted-proxy, and the core refuses without it:" >&2
    echo "              with no trusted-proxy list the only client address left is the leftmost" >&2
    echo "              X-Forwarded-For entry, which the caller wrote. For a loopback proxy:" >&2
    echo "              -trusted-proxy 127.0.0.1/32" >&2
    exit 1
fi
if [ -n "$CERT" ]; then SCHEME=https; else SCHEME=http; fi
BASE="$SCHEME://$LISTEN"
CURL="curl -s -o /dev/null"
[ "$SCHEME" = "https" ] && CURL="curl -sk -o /dev/null"

# The port is checked before anything starts, so this run's output cannot be
# another process's.
if $CURL "$BASE/v1/health" 2>/dev/null; then
    echo "run-core.sh: $BASE is already serving. Stop that process first, or this run will read it instead of its own." >&2
    exit 1
fi

# The environment is exported rather than inherited ad hoc, so a run and the
# installed service read the same variables the same way.
export SP_LISTEN_ADDR="$LISTEN"
export SP_TENANT_ID="$TENANT"
export SP_SITE_ID="$SITE"
export SP_INTEGRATION_ID="$INTEGRATION"
export SP_EVIDENCE_ROOT="$EVIDENCE"
if [ -n "$BEHIND_PROXY_RAW" ]; then export SP_BEHIND_PROXY="$BEHIND_PROXY_RAW"; fi
if [ -n "$TRUSTED_PROXY" ]; then export SP_TRUSTED_PROXY_CIDRS="$TRUSTED_PROXY"; fi
if [ -n "$CERT" ]; then export SP_TLS_CERT="$CERT"; fi
if [ -n "$KEY" ]; then export SP_TLS_KEY="$KEY"; fi
[ -n "${SP_HANDOVER_DIR:-}" ] || export SP_HANDOVER_DIR="$(pwd)/data/dutylog"

echo "==> Security Core backend"
echo "    starting with: $GO run ./cmd/security-core"
echo "    health: $BASE/v1/health"

$GO run ./cmd/security-core &
GO_PID=$!

# The cleanup that makes this safe to re-run: the child first, then the wrapper.
cleanup() {
    pkill -TERM -P "$GO_PID" 2>/dev/null || true
    kill -TERM "$GO_PID" 2>/dev/null || true
    wait "$GO_PID" 2>/dev/null || true
    if $CURL "$BASE/v1/health" 2>/dev/null; then
        echo "run-core.sh: $BASE is still serving after the stop; the process leaked." >&2
    fi
}
trap 'cleanup; exit 0' INT TERM
trap 'cleanup' EXIT

# Readiness is waited for, never slept through: a cold build of the core takes
# tens of seconds, and a fixed sleep is how a demo ends up measuring a dead
# port.
READY=""
i=0
while [ $i -lt 90 ]; do
    if $CURL "$BASE/v1/health" 2>/dev/null; then
        READY=yes; break
    fi
    if ! kill -0 "$GO_PID" 2>/dev/null; then
        echo "run-core.sh: the core exited before it answered /v1/health." >&2
        echo "              Check SP_TENANT_ID, SP_SITE_ID, SP_INTEGRATION_ID and SP_EVIDENCE_ROOT." >&2
        exit 1
    fi
    i=$((i + 1))
    sleep 1
done
if [ -z "$READY" ]; then
    echo "run-core.sh: the core never answered $BASE/v1/health." >&2
    exit 1
fi

cat <<EOF

==> Security Core up.

EOF
# One endpoint file both runners print, so the two platforms cannot print
# different URLs for the same backend.
sed -e "s/127\.0\.0\.1:8443/$LISTEN/g" "$(dirname "$0")/core-endpoints.txt" | sed -e "s/^/    /"

cat <<EOF2

EOF2

if [ "$OPEN" = "1" ]; then
    (xdg-open "$BASE" 2>/dev/null || open "$BASE" 2>/dev/null || start "$BASE" 2>/dev/null) || true
fi

if [ "$SMOKE" = "1" ]; then
    echo "==> smoke: one health probe, then the backend stops."
    curl -s ${CERT:+-k} "$BASE/v1/health" || true
    echo
    exit 0
fi

cat <<EOF
    Ctrl-C stops the backend and the compiled server behind it.
EOF

wait "$GO_PID"
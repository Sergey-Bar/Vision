#!/bin/sh
# The POC's script runner: backend and frontend, no binary in the checkout.
#
#   ./scripts/run-poc.sh [-mode demo|auto|live] [-config <file>] [-smoke]
#
# -smoke asks one question of the investigator and stops the backend, for a
# check that the pipeline is alive without leaving a server on the port.
#
# It starts the backend with `go run ./poc` and prints the frontend's URL.
# Nothing is compiled into the repository or into an output directory: `go run`
# builds into the toolchain's own cache and runs the result, so what an operator
# keeps is source. This matters on a Windows development machine as well as being
# the right shape: a compiled exe dropped in the checkout is a file Windows may
# block or quarantine, and the "backend and frontend as scripts" form — the
# backend is this runner, the frontend is the dashboard the runner points at —
# has no such artefact to block.
#
# For the same reason, do NOT `go build -o poc.exe ./poc` when working in this
# tree: `go vet ./...` and `go test ./...` are the compile checks and neither
# writes a binary into the checkout.
#
# Why the cleanup is spelled out: `go run`'s own child is the compiled server,
# and killing the `go run` wrapper alone leaves that server holding the port.
# The trap below kills both, and the script says so when it cannot.
set -eu

cd "$(dirname "$0")/.."
MODE=demo
CONFIG=poc/config.yaml
# The port the POC binds, read from its own `web.port` so the busy check, the
# readiness loop and the printed URLs all come from the value the server
# binds. `websocket.port` sits above it in the same file, so the match is
# anchored to the web block rather than taking the first port in the file —
# the naive version of this picked 8081 and waited 90 seconds for a listener
# that does not exist.
poc_port() {
    awk '
        /^[a-zA-Z]/ { block = $1 }
        block == "web:" && /^[[:space:]]*port:/ {
            gsub(/[^0-9]/, "", $2); print $2; exit
        }
    ' "$1" 2>/dev/null
}

PORT=$(poc_port poc/config.yaml)
[ -n "$PORT" ] || PORT=8082
BASE="http://127.0.0.1:$PORT"
SMOKE=0
while [ $# -gt 0 ]; do
    case "$1" in
        -mode)   MODE="${2:-demo}"; shift 2 ;;
        -config) CONFIG="${2:-poc/config.yaml}"; shift 2 ;;
        -smoke)  SMOKE=1; shift ;;
        -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
        *) echo "run-poc.sh: unknown argument '$1' (see --help)" >&2; exit 2 ;;
    esac
done

# The toolchain is not on PATH on a default Windows shell, and the container
# image puts it in /usr/local/go/bin. Resolve it by name rather than assuming.
GO=go
if ! command -v go >/dev/null 2>&1; then
    for candidate in /usr/local/go/bin/go /usr/lib/go/bin/go; do
        if [ -x "$candidate" ]; then GO="$candidate"; break; fi
    done
fi
if ! command -v "$GO" >/dev/null 2>&1 && [ ! -x "$GO" ]; then
    echo "run-poc.sh: the Go toolchain was not found. Install Go 1.26+ or put it on PATH." >&2
    exit 1
fi

echo "==> POC backend (mode=$MODE, config=$CONFIG)"
echo "    starting with: $GO run ./poc -config $CONFIG -mode $MODE"

# The port is checked before anything starts: a leftover server means the steps
# that follow would read a previous run, which is the failure mode this script
# exists to avoid.
if curl -s -o /dev/null "http://127.0.0.1:$PORT/api/healthz" 2>/dev/null; then
    echo "run-poc.sh: something is already serving http://127.0.0.1:$PORT." >&2
    echo "              Stop it first, or this run's output will be that process's, not this build's." >&2
    exit 1
fi

# `go run` in the background; its child is the compiled server.
"$GO" run ./poc -config "$CONFIG" -mode "$MODE" &
GO_PID=$!

# The cleanup that makes this safe to re-run: the child first, then the wrapper.
cleanup() {
    pkill -TERM -P "$GO_PID" 2>/dev/null || true
    kill -TERM "$GO_PID" 2>/dev/null || true
    wait "$GO_PID" 2>/dev/null || true
    if curl -s -o /dev/null "http://127.0.0.1:$PORT/api/healthz" 2>/dev/null; then
        echo "run-poc.sh: the port is still serving after the stop; the process leaked." >&2
    fi
}
trap 'cleanup; exit 0' INT TERM
trap 'cleanup' EXIT

# Readiness is waited for, never slept through: a cold build of ./poc takes
# tens of seconds, and a fixed sleep is how a demo measures a dead port and
# records it as a pass.
READY=""
i=0
while [ $i -lt 90 ]; do
    if curl -s -o /dev/null "http://127.0.0.1:$PORT/api/healthz" 2>/dev/null; then
        READY=yes
        break
    fi
    if ! kill -0 "$GO_PID" 2>/dev/null; then
        echo "run-poc.sh: the backend exited before it answered /api/healthz." >&2
        exit 1
    fi
    i=$((i + 1))
    sleep 1
done
if [ -z "$READY" ]; then
    echo "run-poc.sh: the backend never answered http://127.0.0.1:$PORT/api/healthz." >&2
    exit 1
fi

cat <<EOF

==> POC backend up (mode=$MODE).

EOF
# One file, one reader, no PowerShell variables. The first version of this line
# read `$PSScriptRoot/poc-endpoints.txt` — an automatic variable that exists in
# PowerShell and not in sh — which under `set -u` aborted this script on both
# bash and dash, after the reader had waited up to 90 seconds for readiness and
# before a single URL was printed. The script's own directory is the only one
# that can be right here.
sed -e 's/^/    /' "$(dirname "$0")/poc-endpoints.txt"

if [ "$SMOKE" = "1" ]; then
    # A smoke run answers one question, prints it, and stops the backend. The
    # previous flag for this was `-once`, which did the opposite of what its
    # name suggested: it waited for readiness, printed the URLs above, and then
    # the EXIT trap killed the backend it had just waited up to 90 seconds for —
    # so the operator got a printed address for a server that was already
    # stopped. `-smoke` says what it does, and this branch says what it just
    # learned rather than leaving the output unexplained.
    echo "==> smoke: one question, then the backend stops."
    curl -s -X POST "$BASE/api/investigate" \
        -H 'Content-Type: application/json' \
        -d '{"question":"which cameras are degraded and why?"}' || true
    echo
    exit 0
fi

cat <<EOF
    Ctrl-C stops the backend and the compiled server behind it.
EOF

wait "$GO_PID"

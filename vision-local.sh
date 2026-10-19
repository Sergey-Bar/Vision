#!/usr/bin/env bash
# vision-local.sh — מבצע הכל כדי להריץ את Vision ב-localhost בלי לוגין ובלי MFA
# הרץ ב-Git Bash או ב-WSL על המחשב שלך.
set -euo pipefail

cd "$(dirname "$0")"

# --- משתנים נדרשים לשרת ---
export SP_DATABASE_URL='postgres://postgres@127.0.0.1:5432/security_test?sslmode=disable'
export SP_DATABASE_PASSWORD='postgres'
export SP_LISTEN_ADDR='127.0.0.1:8080'
export SP_LOG_LEVEL='info'
export SP_TENANT_ID='tenant-demo'
export SP_SITE_ID='site-demo'
export SP_INTEGRATION_ID='demo-integration'
export SP_EVIDENCE_ROOT="${HOME}/vision-evidence"
export SP_BEHIND_PROXY='true'
export SP_TRUSTED_PROXY_CIDRS='127.0.0.1/32'

mkdir -p "$SP_EVIDENCE_ROOT"

# --- מסלול ל-binary של Go (כי הוא לא ב-PATH כברירת מחדל ב-Windows) ---
if [ -z "${GO:-}" ]; then
  if [ -x "/c/Program Files/Go/bin/go.exe" ]; then
    export GO="/c/Program Files/Go/bin/go.exe"
  elif command -v go >/dev/null 2>&1; then
    export GO="$(command -v go)"
  else
    echo "שגיאה: לא נמצא binary של Go" >&2
    exit 1
  fi
fi

echo "===> בודק/מפעיל Postgres בדוקר (פורט 5432)..."
if ! docker ps --format '{{.Names}}' | grep -q '^sp-pg$'; then
  docker run -d --name sp-pg \
    -e POSTGRES_USER=postgres \
    -e POSTGRES_PASSWORD=postgres \
    -e POSTGRES_DB=security_test \
    -p 5432:5432 \
    postgres:16 -c max_connections=300
  echo "הושק קונטיינר sp-pg, ממתין ל-Postgres..."
  for i in $(seq 1 30); do
    if docker exec sp-pg pg_isready -U postgres >/dev/null 2>&1; then
      echo "Postgres מוכן."
      break
    fi
    sleep 1
  done
else
  echo "קונטיינר sp-pg כבר רץ."
fi

echo "===> מפעיל הגירות של מסד הנתונים..."
"$GO" run ./cmd/security-migrate up

echo "===> מפעיל את שרת security-core ב-$SP_LISTEN_ADDR (בלי לוגין, בלי MFA)..."
exec "$GO" run ./cmd/security-core

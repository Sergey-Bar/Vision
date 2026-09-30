#!/usr/bin/env bash
# make-one-pager.sh — one page, in words, for someone who will not read the
# report.
#
# Every number in it comes from report.md, which comes from the database. The
# script FAILS if the report is missing a required sentence, because a one-pager
# that silently drops the "what this does not prove" section is a one-pager that
# drops the part that matters.
set -euo pipefail

REPORT="${1:-.demo-data/report.md}"
OUT="${2:-docs/one-pager.md}"

missing=0
provisional_line=""

if [ ! -f "$REPORT" ]; then
  echo "make-one-pager: $REPORT does not exist." >&2
  echo "  Produce a report first: 'pocctl demo' takes about three minutes, and" >&2
  echo "  'pocctl report --data-root <root>' regenerates one from the store." >&2
  exit 1
fi

echo "checking the report says the things a one-pager must not be able to omit"

# The strings are matched case-INSENSITIVELY. The first version matched them
# exactly and refused every report in existence, because the report writes
# "there is no ground truth" inside a sentence and the check wanted it at the
# start of one. A guard that fires on every input is not a guard.
need() {
  if grep -qiF "$1" "$REPORT"; then
    echo "  present: $1"
  else
    echo "  MISSING from the report: $1" >&2
    missing=1
  fi
}

need "push target: local simulator (xpsim). No XProtect server was reachable during this run."
need "What this report does not prove"
need "there is no ground truth"
need "no human marked any event as false"
need "provenance:"

# PROVISIONAL is CONDITIONAL. A run that reached its target correctly has no
# PROVISIONAL marker, and demanding one would refuse exactly the runs that did
# what they were asked. What must always be present is the verdict machinery:
# three verdicts, and the statement that NOT RUN is never PASS.
need "Three verdicts only"
need "is never"

if grep -qi "provisional" "$REPORT"; then
  echo "  present: the run is marked PROVISIONAL (it did not reach its target)"
  provisional_line=$(
    grep -i "provisional" "$REPORT" | head -1
  )
  echo "$provisional_line"
else
  echo "  present: the run reached its target, so PROVISIONAL does not apply"
  provisional_line=""
fi

if [ "$missing" -ne 0 ]; then
  echo >&2
  echo "make-one-pager: the report is missing sentences a one-pager must not omit." >&2
  echo "  A one-pager that quietly drops them is a one-pager that drops the part that" >&2
  echo "  matters. This is a refusal, not a warning." >&2
  exit 1
fi

verdict=$(grep -m1 -oE '\*\*(PASS|FAIL|NOT RUN)\*\*' "$REPORT" | tr -d '*' || true)
[ -n "$verdict" ] || verdict="(not stated)"
provisional=""
grep -q "PROVISIONAL" "$REPORT" && provisional=" — PROVISIONAL: the run has not reached its target, so nothing here concludes"

detections=$(grep -m1 -E '^\| detections \|' "$REPORT" | sed 's/.*| *\([0-9]*\) *|.*/\1/' || echo 0)
pushes=$(grep -m1 -E '^\| push attempts \|' "$REPORT" | sed 's/.*| *\([0-9]*\) *|.*/\1/' || echo 0)
faults=$(grep -m1 -E '^\| faults \|' "$REPORT" | sed 's/.*| *\([0-9]*\) *|.*/\1/' || echo 0)
elapsed=$(grep -m1 -E '^\- \*\*elapsed\*\*:' "$REPORT" | sed 's/.*: //' || echo "unknown")
source=$(grep -m1 -E '^- \*\*mode\*\*' "$REPORT" | sed 's/.*: *//' || echo "not stated")

mkdir -p "$(dirname "$OUT")"

cat > "$OUT" <<EOF
# Active Monitoring POC — עמוד אחד

> ⚠️ **קרא את הקטע האחרון לפני שאתה מציג את זה.**
> ה־POC מוכיח שהמערכת יכולה להפיק אירוע, לשמור קליפ מאומת ולדחוף אירוע
> דרך ה־API של Milestone בלי לאבד אירוע ובלי להמציא תשובה.
> הוא **לא** מוכיח שהמערכת מזהה עופות, מסובכת גבוהה, או שהאירוע הגיע
> ל־Smart Client.

## התוצאה

| | |
|---|---|
| **משך** | $elapsed |
| **מצב ריצה** | \`$source\` |
| **פעולה** | **$verdict**$provisional |
| **אירועים** | $detections |
| **ניסיונות דחיפה** | $pushes |
| **תקלות** | $faults |

## מה זה עושה

\`ffmpeg\` אחד קורא את הווידאו פעם אחת ומוציא שני דברים: קלטה מתגלגלת, וצינור
MJPEG לניתוח. Python מקבל פריימים גולמיים ומחזיר תיבות. Go עושה זיהוי
חציית־קו, חותך קליט לכל אירוע, **מתמלל אותו ומאמת שהבייטים תואמים את ה־SHA-256**,
ואז דוחק אירוע ל־API ו**מאמת בקריאת חזרה שהוא באמת שם**.

שלושה כללים שלא נגעים בהם: קורא אחד ושעון אחד; רישום לפני כל אפקט; תשובה
חסרה אינה תשובה שלילית.

## מה זה **לא** מוכיח

$(sed -n '/^## What this report does not prove/,$p' "$REPORT" | sed -n '3,$p' | grep '^- ' | sed 's/^- /- /' | head -8)

## איך להריץ שבוע

\`\`\`bash
# שער, כולל red check
powershell -ExecutionPolicy Bypass -File scripts/verify.ps1

# שער D, לפני השעון
./bin/pocctl.exe rehearse --data-root ./poc-data

export SP_POC_SOAK_TARGET=168h
./bin/watch.exe

./bin/pocctl.exe report --data-root ./poc-data
\`\`\`

ב־PowerShell אותו דבר עם \$env: במקום export.

---

*נוצר מתוך \`$REPORT\`. כל מספר כאן נקרא משורה במסד הנתונים. אם הדוח חסר משפט
מהרשימה האחרונה, הסקריפט \`make-one-pager.sh\` **נכשל** ולא מייצר את העמוד.*
EOF

echo
echo "wrote $OUT"
echo
echo "The list above is copied out of the report, not retyped. It is the list the"
echo "report generated, so the one-pager cannot drift from the measurement."

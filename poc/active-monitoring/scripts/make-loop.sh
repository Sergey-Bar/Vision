#!/usr/bin/env bash
# make-loop.sh — build the recording loop the seven-day run plays.
#
# A run over a 20 second clip produces a handful of events a day, which checks
# almost nothing. This script assembles a loop that yields at least
# EXPECTED_EVENTS_PER_DAY accepted crossings, because a run with no events
# cannot exercise AC2 or AC4 at all: both of them are about what happened to
# events, and there are none.
#
# It prints the expected rate before you start, and it refuses to claim a rate it
# has not computed.
set -euo pipefail

OUT="${1:-testdata/loop.mp4}"
INPUTS="${*:-}"
CONCAT_LIST="${CONCAT_LIST:-testdata/.loop-concat.txt}"
CYCLE_SECONDS="${CYCLE_SECONDS:-90}"
CROSSINGS_PER_PASS="${CROSSINGS_PER_PASS:-2}"
EXPECTED_EVENTS_PER_DAY="${EXPECTED_EVENTS_PER_DAY:-10}"

# The inputs are the labelled clips in testdata/, in the order they should play.
CLIPS=(
  "${CLIP_A:-testdata/car-crossing.mp4}"
  "${CLIP_B:-testdata/bird-synthetic.mp4}"
)
[ -n "${CLIP_C:-}" ] && CLIPS+=("$CLIP_C")

for c in "${CLIPS[@]}"; do
  if [ ! -f "$c" ]; then
    echo "make-loop: $c is missing." >&2
    echo "  Build the labelled material first:" >&2
    echo "    scripts\\make-bird-clip.sh" >&2
    echo "  and obtain a licensed real-motion clip, recording its source, licence and" >&2
    echo "  retrieval date in testdata/labels.json." >&2
    exit 1
  fi
done

pass_len=0
: > "$CONCAT_LIST"
for c in "${CLIPS[@]}"; do
  echo "file '$c'" >> "$CONCAT_LIST"
  d=$(ffprobe -v error -show_entries format=duration -of default=nw=1:nk=1 "$c")
  pass_len=$(awk -v a="$pass_len" -v b="$d" 'BEGIN{printf "%.3f", a+b}')
done

if [ "$pass_len" -lt 1 ]; then
  echo "make-loop: the assembled pass is $pass_len s long, which cannot produce events" >&2
  exit 1
fi

# Repeat the pass until it is at least CYCLE_SECONDS long, then re-encode once so
# the loop is a single self-contained file rather than a concat list ffmpeg has to
# re-read on every loop. A concat demuxer list is fine for a demo and a bad idea
# for a week: it is one more thing that can be wrong at hour three of hour one
# hundred and sixty-eight.
copies=$(( CYCLE_SECONDS / pass_len + 1 ))
: > "$CONCAT_LIST"
for _ in $(seq 1 "$copies"); do
  cat <(for c in "${CLIPS[@]}"; do echo "file '$c'"; done) >> "$CONCAT_LIST"
done

echo "assembling $OUT"
echo "  clips:      ${CLIPS[*]}"
echo "  one pass:   ${pass_len}s"
echo "  copies:     $copies"
echo "  cycle:      about $(( copies * pass_len ))s"
ffmpeg -hide_banner -loglevel error -y -f concat -safe 0 -i "$CONCAT_LIST" -c copy "$OUT"
rm -f "$CONCAT_LIST"

cycle=$(ffprobe -v error -show_entries format=duration -of default=nw=1:nk=1 "$OUT" | awk '{printf "%.0f", $1}')
if [ "$cycle" -lt 1 ]; then
  echo "make-loop: the produced loop reports a duration of ${cycle}s" >&2
  exit 1
fi

# The arithmetic, printed. A claimed rate that was not computed is a claim.
events_per_cycle=$(( CROSSINGS_PER_PASS ))
per_day=$(( events_per_cycle * 86400 / cycle ))

echo
echo "expected_events_per_day >= $per_day   (target: >= $EXPECTED_EVENTS_PER_DAY)"
echo
if [ "$per_day" -lt "$EXPECTED_EVENTS_PER_DAY" ]; then
  echo "THAT IS BELOW THE TARGET." >&2
  echo "  A loop producing $per_day events a day checks almost nothing: AC2 and AC4 are" >&2
  echo "  about what happened to events, and there are almost none." >&2
  echo "  Shorten the cycle or add another clip. CROSSINGS_PER_PASS and CYCLE_SECONDS" >&2
  echo "  are the two knobs, and the arithmetic above is what they do." >&2
  exit 1
fi
echo "The arithmetic above is what the run will produce, from the loop's own measured"
echo "duration. If the model finds fewer crossings than CROSSINGS_PER_PASS, the run will"
echo "show it in the report, which is the point of printing the expectation first."

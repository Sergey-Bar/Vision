#!/usr/bin/env bash
# make-fixtures.sh — the QA lab's material, generated, not downloaded.
#
# WHY EVERYTHING HERE IS GENERATED: a fixture that nobody can reproduce is a claim.
# `make-bird-clip.sh` already proved the property this script depends on — the same
# command twice gives the same bytes. Everything below is `ffmpeg` drawing its own
# frames, so there is no third-party footage anywhere in the lab, no licence to
# record, and nothing that may not be shown to anyone.
#
# THE ONE THING THAT MATTERS HERE, and it is not obvious. The shipped trigger is a
# NOVELTY engine: it diffs consecutive frames, throws away a global illumination
# shift, thresholds the residual, and reports the FRACTION OF THE WHOLE FRAME that
# changed. `SP_POC_NOVELTY_THRESHOLD` is 0.35, so a candidate only exists when more
# than a third of the frame moved between two consecutive frames.
#
# A 170x110 box in a 1280x720 frame is 2% of it. It is a car-sized object and it
# still can never produce a candidate. This is a property of the shipped DETECTOR,
# not of the material: a novelty detector fires on "a lot of the picture changed",
# which is what a large near-camera object looks like and is not what a distant one
# looks like. So the fixtures below are built to produce that change, and every one
# of them says in `scenarios.json` WHICH property of the detector it exercises.
#
# Usage:  make-fixtures.sh <outdir>   (default: qa/fixtures)
set -euo pipefail

OUT="${1:-qa/fixtures}"
W=1280
H=720
# 10 fps, and that is a sizing decision rather than a default.
#
# The engine measures a CHANGED FRACTION between CONSECUTIVE frames, so the geometry
# has to satisfy two constraints at once, and at 15fps they contradict:
#
#   the bar:      2*D/W > 0.35        -> D > 224 px per frame at W=1280
#   the dwell:    the object must stay on screen for TRACK_MIN_FRAMES=3 frames
#
# At 15fps, D=300 crosses the frame in 5.7 frames = 0.38s. It is measurable, but
# three frames of tracker history on a quarter-second event is a coin flip. At
# 10fps the same D gives 0.57s and at 5fps 1.1s. 10 is the slowest rate that still
# samples a real camera at a plausible rate, so 10 it is.
#
# 10fps is also what makes `fixtureprobe` usable by hand: a probe at t=1 lands
# inside the crossing instead of after it.
FPS=10

mkdir -p "$OUT"

# --- the geometry that matters, stated once so the numbers below are readable ---

# A full-height object W wide moving D px per frame changes 2*D*H px between two
# consecutive frames (the leading and trailing edges; the interior is the same
# object in both). As a fraction of the frame that is 2*D/W.
#
#   D = 300, W = 1280  ->  600/1280 = 0.469   above the 0.35 bar
#   D = 150, W = 1280  ->  300/1280 = 0.234   below it
#
# So D >= 224 px/frame at this width. At 10 fps that is 3000 px/s, which for a
# vehicle a few metres from a camera is an ordinary speed. The fixture moves a
# full-height block at 300 px/frame, which is the honest shape of "something large
# passed close to the lens".
OBJ_W=420
OBJ_D=300

# The crossing runs long enough for the object to be visible on both sides of the
# line, and short enough that a demo window sees it without sitting through it.
#
# Object width 420, so travel is 1280+420 = 1700px at 300px/frame = 5.7 frames, and
# at 10fps that is 0.57 seconds. The clip is 20s so a 60-second demo sees it three
# times and a 150-second one seven.
CROSS_SECS=20

# EVERY fixture is built on a DARK FLAT background, and that is not cosmetic.
#
# The novelty engine subtracts a uniform illumination offset before thresholding,
# precisely so a global lighting change does not read as a change. That subtraction
# takes the MEDIAN of the per-cell differences, and on a busy background the median
# already contains the object: `testsrc` is an ANIMATED test pattern, so the whole
# frame moves every frame and a bright box on it barely shifts the median.
#
# Measured, not assumed. On `testsrc`, a full-height object 300px wide travelling
# 300px/frame produced a peak changed fraction of **0.018** against a 0.35 bar —
# not the 0.47 the geometry predicts, and not enough to produce a candidate at any
# threshold. On a flat background the same geometry is the whole signal.
#
# The fix is also the honest one: a road at night IS a flat dark field, and a flat
# field is what lets the detector's illumination subtraction be tested at all (S2).
DARK="color=c=0x141a22:size=${W}x${H}:rate=${FPS}"
dark() { echo "${DARK}:duration=${1}"; }

mk() { # mk <name> <seconds> <filter> [background]
  local name="$1" secs="$2" filter="$3"
  local bg="${4:-$(dark "$secs")}"
  echo "  -> $OUT/$name.mp4"
  ffmpeg -hide_banner -loglevel error -y \
    -f lavfi -i "$bg" \
    -vf "$filter" -c:v libx264 -preset veryfast -crf 20 -pix_fmt yuv420p \
    -g 10 -r $FPS "$OUT/$name.mp4"
}

# The motion expression, named once and reused.
#
# `mod(...,PERIOD)` is what makes the object come BACK, and without it the clip is
# 0.57 seconds of motion followed by 19 seconds of nothing — which is why the first
# six probes of this fixture all reported 0.0000 while the file still contained a
# white block. A demo window that shows one crossing in twenty seconds looks like a
# system that does not work.
moving() {
  echo "drawbox=x='mod(t*$FPS*$OBJ_D,$((W + OBJ_W)))-$OBJ_W':y=0:w=$OBJ_W:h=$H:color=white@0.85:t=fill"
}

# ---------------------------------------------------------------------------
# S1 — the critical one. A large object crosses a vertical line.
#
# The vertical line is right because the object travels left-to-right and the
# scene is the reason: on a road the carriageway runs across the frame and the
# line a vehicle crosses is perpendicular to it. A horizontal line here would
# never be crossed by anything, and a run with no events and no error is the most
# expensive silence there is.
# ---------------------------------------------------------------------------
mk crossing "$CROSS_SECS" "$(moving)"

# ---------------------------------------------------------------------------
# S2 — the same crossing at night. `eq` is a GLOBAL brightness change and the
# novelty engine subtracts a uniform illumination offset, so it must NOT produce
# a candidate: a detector that cries wolf when the sun goes in is worse than one
# that never fires. This fixture PROVES the subtraction works, by producing
# nothing.
# ---------------------------------------------------------------------------
mk illumination "$CROSS_SECS" "$(moving),eq=brightness=-0.45"

# ---------------------------------------------------------------------------
# S3 — noise. `noise` is per-pixel and uncorrelated, so it survives the uniform
# illumination subtraction and DOES produce candidates. This is the false
# positive fixture: an event that a person marks `false_positive` is exactly this,
# and the false-positive rate this project has to publish is measured on footage
# that looks like it.
#
# Built on a DARK FLAT background at 640x360, not on testsrc at 1280x720. Two
# reasons, one of them load-bearing:
#   - a road at night is a flat field with sensor noise on it, which is both the
#     realistic false-positive scenario and something x264 shrinks to kilobytes.
#   - the novelty engine measures on a `SP_POC_NOVELTY_GRID`-wide luma grid, 64
#     cells across by default. At 640 wide the grid is half the pixels per cell, and
#     for a detector that only ever sees the grid, 640x360 and 1280x720 are the same
#     measurement. Building the noise fixture at 1280 produced 55 MB of
#     incompressible video for twelve seconds to answer a question the detector
#     cannot tell apart.
mk noisy 6 "noise=alls=14:allf=t" "color=c=0x141a22:size=640x360:rate=${FPS}:duration=6"

# ---------------------------------------------------------------------------
# S4 — a static scene with nothing in it. Zero candidates, zero events, zero
# errors. The empty road, which must be indistinguishable from a run nobody
# looked at: the report prints both as PASS and says which was which.
mk empty 12 "null" "$(dark 12)"

# ---------------------------------------------------------------------------
# S5 — a frozen frame. `tpad` clones the last frame, so from t=4 onward nothing
# changes at all. The detector must report nothing, and the brief must show the
# frames-per-second counter flat rather than falling.
# ---------------------------------------------------------------------------
mk frozen 12 "tpad=stop_mode=clone:stop_duration=8"

# ---------------------------------------------------------------------------
# S6 — a frame-rate drop. `mpdecimate` discards near-duplicate frames, so the
# decoder is fed less often. Nothing should fail: this is a transport property,
# and the report's frame counter is the thing that must tell the truth about it.
# ---------------------------------------------------------------------------
mk thin 12 "mpdecimate=hi=200:lo=100:frac=0.4"

echo
echo "provenance: synthetic"
echo "  every fixture is ffmpeg drawing its own frames. No third-party footage is"
echo "  involved at any point, so no licence is inherited and nothing here is"
echo "  restricted from being shown to anyone."
echo
echo "  NONE of this is evidence that the system detects anything in the world. A"
echo "  white rectangle crossing a line is a measurement of the PIPELINE. The claim"
echo "  that the pipeline sees real traffic needs real traffic, and the report says" \
     "'synthetic' next to every number derived from these files."
echo
echo "next: qa/run.py --list"
#!/usr/bin/env bash
# make-bird-clip.sh — build the synthetic bird case, bit for bit.
#
# The bird case in testdata/ is SYNTHETIC. It is composed by this script from
# ffmpeg's own generated frames, and it is labelled as synthetic everywhere: in
# labels.json, on the screen, in the log and in the report. Nothing in this
# repository is evidence that the system detects birds in the world.
#
# Re-running this script reproduces the clip byte for byte, which is what makes
# the label meaningful: a labelled material nobody can reproduce is a claim, not
# a fixture.
set -euo pipefail

OUT_DIR="${1:-testdata}"
NAME="${2:-bird-synthetic}"
SIZE="${SIZE:-640x360}"
FPS="${FPS:-10}"
SECONDS_TOTAL="${SECONDS_TOTAL:-8}"

mkdir -p "$OUT_DIR"

echo "building the synthetic bird clip -> $OUT_DIR/$NAME.mp4"
echo "  size=$SIZE fps=$FPS seconds=$SECONDS_TOTAL"
echo

# A dark, static background with a visible horizon, so the line drawn on the
# still has something to sit on and a viewer can see the bird move relative to
# it. Everything is generated: no downloaded footage, no third-party material.
#
# The bird is a bright blob that travels from left to right across the middle
# third of the frame. It is NOT a bird. It is a shape with bird-like motion and
# bird-like proportions, and calling it anything else would be a claim the
# material cannot support.
ffmpeg -hide_banner -loglevel error -y \
  -f lavfi -i "color=c=0x1b2430:s=$SIZE:d=$SECONDS_TOTAL:r=$FPS" \
  -f lavfi -i "color=c=0x33465c:s=$SIZE:d=$SECONDS_TOTAL:r=$FPS,drawbox=x=0:y=ih*0.62:w=iw:h=ih*0.38:color=0x2c3a4d@1:t=fill" \
  -filter_complex "\
    [0:v][1:v]overlay=0:0:shortest=1,\
    drawgrid=w=iw/10:h=ih/10:t=1:c=white@0.12,\
    [bg]nullsink;" \
  -map 0:v -frames:v 1 "$OUT_DIR/.bird-bg.png" 2>/dev/null || true

# The moving object. drawbox with a time expression moves a rectangle; its width
# and height are the bird's apparent size.
ffmpeg -hide_banner -loglevel error -y \
  -f lavfi -i "color=c=0x1b2430:s=$SIZE:d=$SECONDS_TOTAL:r=$FPS" \
  -f lavfi -i "color=c=0x33465c:s=$SIZE:d=$SECONDS_TOTAL:r=$FPS,drawbox=x=0:y=ih*0.62:w=iw:h=ih*0.38:color=0x2c3a4d@1:t=fill" \
  -filter_complex "\
    [0:v][1:v]overlay=0:0:shortest=1[bg];\
    [bg]drawbox=x='(iw*0.10)+(t/($SECONDS_TOTAL))*iw*0.72':y=ih*0.34:w=iw*0.09:h=ih*0.14:color=0xf0c419@1:t=fill,\
        drawbox=x='(iw*0.10)+(t/($SECONDS_TOTAL))*iw*0.72+iw*0.02':y=ih*0.30:w=iw*0.05:h=ih*0.07:color=0xf0c419@1:t=fill,\
        drawgrid=w=iw/10:h=ih/10:t=1:c=white@0.10[v]" \
  -map "[v]" -c:v libx264 -preset veryslow -crf 18 -pix_fmt yuv420p -g 10 -r $FPS "$OUT_DIR/$NAME.mp4"

echo "wrote $OUT_DIR/$NAME.mp4"
echo
echo "provenance: synthetic"
echo "  built by this script from ffmpeg's own generated frames; no third-party footage"
echo "  is involved and no licence is inherited from anywhere"
echo "  the moving object is a rectangle, not a bird, and the report says 'synthetic'"
echo "  next to every number derived from this material"
echo
echo "labels.json next to it records the line, the expected direction and the time window."

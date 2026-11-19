#!/bin/sh
# Makes server/testdata/clip-h264-opus.mp4, the 10-second fixture the Clips
# tests feed through the ring, export and trim: H.264 High with no B-frames
# and a keyframe every 2 s, plus stereo Opus. Dev-only; needs ffmpeg with
# libx264 and libopus. The output is committed, so builds never need this.
set -eu
cd "$(dirname "$0")/.."
mkdir -p server/testdata
ffmpeg -y -f lavfi -i "testsrc2=size=320x180:rate=15" -f lavfi -i "sine=frequency=440:beep_factor=4:sample_rate=48000" \
  -t 10 -c:v libx264 -profile:v high -preset veryslow -bf 0 -g 30 -keyint_min 30 -sc_threshold 0 -b:v 60k \
  -pix_fmt yuv420p -c:a libopus -b:a 16k -ac 2 -movflags +faststart server/testdata/clip-h264-opus.mp4

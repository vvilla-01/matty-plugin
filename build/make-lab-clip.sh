#!/bin/sh
# Makes webapp/lab/media/sample-clip.mp4, the two-minute clip the lab's mock
# server hands out as every draft and clip: 256x144 at 12 fps with a running
# clock burnt in, H.264 High with a 2 s GOP and no B-frames, stereo Opus.
# Dev-only; needs ffmpeg with libx264, libopus and drawtext. The output is
# committed, and only the lab imports it - never the plugin bundle.
set -eu
cd "$(dirname "$0")/.."
mkdir -p webapp/lab/media
ffmpeg -y -f lavfi -i "testsrc2=size=256x144:rate=12,drawtext=text='%{pts\:hms}':x=6:y=6:fontsize=16:fontcolor=white:box=1:boxcolor=black@0.6" \
  -f lavfi -i "sine=frequency=440:beep_factor=4:sample_rate=48000" -t 120 \
  -c:v libx264 -profile:v high -preset veryslow -bf 0 -g 24 -keyint_min 24 -sc_threshold 0 -b:v 18k -maxrate 40k -bufsize 80k \
  -pix_fmt yuv420p -c:a libopus -b:a 8k -ac 2 -movflags +faststart webapp/lab/media/sample-clip.mp4

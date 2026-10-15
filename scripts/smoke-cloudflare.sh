#!/usr/bin/env bash
# Live smoke test: runs every Cloudflare-backed verb end to end and checks the
# produced files with ffprobe. Requires Cloudflare credentials (CLOUDFLARE_API_TOKEN
# or `wrangler login`) and prepaid AI Gateway credits for third-party models.
#
#   scripts/smoke-cloudflare.sh [path/to/creative]   (default: builds ./creative)
#
# SMOKE_SKIP="video upscale" skips verbs (space separated) to save credits.
set -euo pipefail

BIN=${1:-}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
if [[ -z "$BIN" ]]; then
  (cd "$ROOT" && go build -o creative .)
  BIN="$ROOT/creative"
fi
OUT=$(mktemp -d)
trap 'echo "artifacts: $OUT"' EXIT
SKIP=" ${SMOKE_SKIP:-} "
pass=0; fail=0; skipped=0

run() { # name, check-file, command...
  local name=$1 file=$2; shift 2
  if [[ "$SKIP" == *" $name "* ]]; then echo "SKIP $name"; skipped=$((skipped+1)); return; fi
  echo "---- $name"
  if "$@" >"$OUT/$name.json" 2>"$OUT/$name.log" && { [[ -z "$file" ]] || [[ -s "$file" ]]; }; then
    echo "PASS $name $(head -c 300 "$OUT/$name.json")"; pass=$((pass+1))
  else
    echo "FAIL $name"; tail -5 "$OUT/$name.log"; fail=$((fail+1))
  fi
}

media_ok() { ffprobe -v error -show_entries format=duration -of csv=p=0 "$1" >/dev/null; }

# Local sample clip with speech-like audio for transcribe/caption/analyze/upscale.
ffmpeg -v error -y -f lavfi -i testsrc=size=480x270:rate=24:duration=4 \
  -f lavfi -i "sine=frequency=330:duration=4" -c:v libx264 -pix_fmt yuv420p -c:a aac -shortest "$OUT/sample.mp4"

run image       "$OUT/image.png"   "$BIN" image --prompt "a red cube on a white table, studio light" --quality low --size 1024x1024 --out "$OUT/image.png"
run image-flux  "$OUT/flux.png"    "$BIN" image --model flux-2-klein-9b --prompt "a red cube on a white table" --size 768x768 --out "$OUT/flux.png"
run speech      "$OUT/speech.mp3"  "$BIN" speech --text "Hello from Cloudflare. This is a creative CLI smoke test." --out "$OUT/speech.mp3"
run music       "$OUT/music.mp3"   "$BIN" music --prompt "short upbeat lofi hip hop loop" --duration-seconds 10 --instrumental --out "$OUT/music.mp3"
run transcribe  ""                 "$BIN" transcribe "$OUT/speech.mp3" --format srt
run caption     "$OUT/caption.mp4" bash -c "ffmpeg -v error -y -i '$OUT/sample.mp4' -i '$OUT/speech.mp3' -map 0:v -map 1:a -c:v copy -c:a aac -shortest '$OUT/talk.mp4' && '$BIN' caption '$OUT/talk.mp4' --out '$OUT/caption.mp4'"
run analyze     ""                 "$BIN" analyze "$OUT/sample.mp4" --max-output-tokens 1024
run video       "$OUT/video.mp4"   "$BIN" video --prompt "slow dolly shot of a coffee cup with steam" --duration-seconds 4 --resolution 480p --no-generate-audio --out "$OUT/video.mp4"
run upscale     "$OUT/upscaled.mp4" "$BIN" upscale "$OUT/sample.mp4" --target 720p --out "$OUT/upscaled.mp4"

for f in speech.mp3 music.mp3 caption.mp4 video.mp4 upscaled.mp4; do
  [[ -s "$OUT/$f" ]] && { media_ok "$OUT/$f" && echo "ffprobe ok: $f" || { echo "ffprobe FAILED: $f"; fail=$((fail+1)); }; }
done
echo "pass=$pass fail=$fail skipped=$skipped"
[[ $fail -eq 0 ]]

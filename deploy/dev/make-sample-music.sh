#!/usr/bin/env bash
# Generates a small, tagged library of synthetic tracks for the dev Navidrome,
# so nobody needs real music or real credentials to develop.
set -euo pipefail
cd "$(dirname "$0")"
out=music
if [[ -d $out ]]; then exit 0; fi

if command -v ffmpeg >/dev/null; then
  ff() { ffmpeg -hide_banner -loglevel error "$@"; }
else
  ff() { docker run --rm -v "$PWD:/w" -w /w linuxserver/ffmpeg -hide_banner -loglevel error "$@"; }
fi

# artist|album|year|format|title:hz:seconds;title:hz:seconds;...
library=(
  "Sine Wave Collective|First Contact|2021|mp3|Hello Hertz:220:20;Octave Up:440:25;Beat Frequency:330:30"
  "The Square Roots|Low Pass|2023|flac|Rolloff:110:20;Resonance:165:25"
  "DJ Nyquist|Aliasing|2025|opus|Fold Back:550:20;Sample Rate:660:20;Dither:770:25"
)

tmp=$(mktemp -d "$out.XXXX")
trap 'rm -rf "$tmp"' EXIT

for entry in "${library[@]}"; do
  IFS='|' read -r artist album year fmt tracks <<<"$entry"
  dir="$tmp/$artist/$album"
  mkdir -p "$dir"
  IFS=';' read -r -a items <<<"$tracks"
  n=0
  for item in "${items[@]}"; do
    IFS=':' read -r title hz secs <<<"$item"
    n=$((n + 1))
    file="$dir/$(printf '%02d' "$n") - $title.$fmt"
    echo "  ${file#"$tmp"/}"
    ff -f lavfi -i "sine=frequency=$hz:duration=$secs" -af volume=0.2 \
      -metadata artist="$artist" -metadata album_artist="$artist" -metadata album="$album" \
      -metadata title="$title" -metadata track="$n" -metadata date="$year" -metadata genre=Test \
      "$file"
  done
done

mv "$tmp" "$out"
trap - EXIT

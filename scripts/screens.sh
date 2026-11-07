#!/bin/sh
set -eu
out=${1:-screens}
mkdir -p "$out"
# go test runs inside the package folder, so pass an absolute path.
out=$(cd "$out" && pwd)
go test ./internal/tui -run '^TestScreens$' -screens "$out"
# An optional scene glob avoids rendering unrelated PNGs. Custom fonts need
# Freeze's librsvg backend; the bundled resvg backend only loads JetBrains Mono.
pattern=${2:-*}
for scene in "$out"/$pattern.ansi; do
  set -- --language ansi
  if [ -n "${SCREENS_FONT_FAMILY:-}" ]; then
    set -- "$@" --font.family "$SCREENS_FONT_FAMILY"
  fi
  if [ -n "${SCREENS_FONT_FILE:-}" ]; then
    set -- "$@" --font.file "$SCREENS_FONT_FILE"
  fi
  go run github.com/charmbracelet/freeze@v0.2.2 "$@" --output "${scene%.ansi}.png" "$scene"
done

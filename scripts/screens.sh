#!/bin/sh
set -eu
out=${1:-screens}
mkdir -p "$out"
go test ./internal/tui -run '^TestScreens$' -screens "$out"
for scene in "$out"/*.ansi; do
  go run github.com/charmbracelet/freeze@v0.2.2 --language ansi --output "${scene%.ansi}.png" "$scene"
done

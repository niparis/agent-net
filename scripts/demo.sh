#!/bin/sh
# Regenerate docs/img/demo.svg, the README animation of the real TUI.
#
# The frames come from the golden screen harness (TestDemoScenes). Freeze
# renders each one to SVG, and scripts/demo_svg.py keeps one font and turns the
# frames into a 12 second CSS animation. Needs go and python3; it adds nothing
# to the agent-net binary or go.mod.
set -eu
cd "$(dirname "$0")/.."

out=${1:-docs/img/demo.svg}
mkdir -p "$(dirname "$out")"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/agent-net-demo.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/ansi"

AGENT_NET_SCREEN_CHILD=1 go test ./internal/tui -run '^TestDemoScenes$' -count=1 -screens "$tmp/ansi"

for scene in "$tmp"/ansi/demo_*.ansi; do
  name=$(basename "$scene" .ansi)
  go run github.com/charmbracelet/freeze@v0.2.2 --language ansi --output "$tmp/$name.svg" "$scene"
done

cp "$tmp/ansi/demo_timeline.txt" "$tmp/demo_timeline.txt"
python3 scripts/demo_svg.py "$tmp" "$out"
echo "wrote $out"

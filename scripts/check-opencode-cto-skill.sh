#!/bin/sh
# Confirm that OpenCode 2.3 loads a CTO skill from .claude/skills in the folder
# its private server runs in. The daemon writes agent-net-setup there for an
# OpenCode CTO, so this checks the path on a real OpenCode release.
#
# Manual check only. It starts a private OpenCode server in a throwaway folder
# and makes no model calls. Usage: scripts/check-opencode-cto-skill.sh
set -eu

version=$(opencode --version 2>/dev/null || true)
case "$version" in
*2.3*) ;;
*)
	echo "need OpenCode 2.3, found: ${version:-none}" >&2
	exit 2
	;;
esac

tmp=$(mktemp -d "${TMPDIR:-/tmp}/agent-net-opencode-skill.XXXXXX")
port=$((40000 + $$ % 10000))
pid=
cleanup() {
	if [ -n "$pid" ]; then
		kill "$pid" 2>/dev/null || true
	fi
	rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

mkdir -p "$tmp/root/.claude/skills/agent-net-setup" "$tmp/config" "$tmp/data" "$tmp/state" "$tmp/cache" "$tmp/home"
printf -- '---\nname: agent-net-setup\ndescription: probe\n---\n\nProbe.\n' >"$tmp/root/.claude/skills/agent-net-setup/SKILL.md"
cd "$tmp/root"

XDG_CONFIG_HOME="$tmp/config" XDG_DATA_HOME="$tmp/data" XDG_STATE_HOME="$tmp/state" \
	XDG_CACHE_HOME="$tmp/cache" HOME="$tmp/home" OPENCODE_SERVER_PASSWORD=probe \
	opencode serve --hostname 127.0.0.1 --port "$port" >"$tmp/serve.log" 2>&1 &
pid=$!

i=0
found=0
while [ "$i" -lt 120 ]; do
	if curl -fsS -u opencode:probe "http://127.0.0.1:$port/api/skill" >"$tmp/skills.json" 2>/dev/null &&
		grep -q "$tmp/root/.claude/skills/agent-net-setup/SKILL.md" "$tmp/skills.json"; then
		found=1
		break
	fi
	i=$((i + 1))
	sleep 0.5
done
if [ "$found" -ne 1 ]; then
	echo "OpenCode did not discover .claude/skills in its working folder" >&2
	cat "$tmp/skills.json" 2>/dev/null || true
	tail -20 "$tmp/serve.log" >&2 || true
	exit 1
fi
echo "OK: OpenCode $version reads .claude/skills from its working folder"
#!/bin/sh
# scripts/dev/gocache-trim-test.sh: check what gocache-trim.sh removes.
#
# Every case runs the real script on temporary folders, with mtimes set by
# touch -d, so the real /tmp/agent-net-* folders are never touched. The two
# size limits are arguments, so a cache of a few KB can reach them.
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
script=$root/scripts/dev/gocache-trim.sh
work=$(mktemp -d "${TMPDIR:-/tmp}/gocache-trim-test.XXXXXX")
# A case can leave a read-only folder behind, as a sandbox does.
trap 'chmod -R u+w "$work" 2>/dev/null; rm -rf "$work"' EXIT HUP INT TERM

# no_limit is above any cache a case builds, so the size pass does not run.
no_limit=1099511627776

fail() {
	echo "gocache-trim-test: FAIL: $*" >&2
	exit 1
}

# new_case makes an empty temp root, cache and gotmp folder for one case.
new_case() {
	case_root=$(mktemp -d "$work/case.XXXXXX")
	cache=$case_root/agent-net-gocache
	gotmp=$case_root/agent-net-gotmp
	mkdir -p "$cache" "$gotmp"
	: >"$cache/README"
}

# cache_file writes a cache entry at <hash>/<name>, <age> minutes old, <bytes>.
cache_file() {
	mkdir -p "$cache/${1%/*}"
	dd if=/dev/zero of="$cache/$1" bs="$3" count=1 2>/dev/null
	touch -d "-$2 minutes" "$cache/$1"
}

# run_trim runs the script on this case's folders with the given size limits.
run_trim() {
	status=0
	AGENT_NET_GOCACHE="$cache" AGENT_NET_GOTMPDIR="$gotmp" AGENT_NET_TRIM_ROOT="$case_root" \
		AGENT_NET_GOCACHE_MAX_BYTES="$1" AGENT_NET_GOCACHE_LOW_BYTES="$2" \
		sh "$script" >"$case_root/out" 2>&1 || status=$?
	[ "$status" = 0 ] || fail "the script exited $status: $(cat "$case_root/out")"
}

# The age pass removes a cache file unused for three hours and keeps a fresh one
# and a file used 90 minutes ago. The hash folder stays, so the cache keeps its
# layout.
new_case
cache_file aa/unused 180 4096
cache_file bb/fresh 10 4096
cache_file cc/half 90 4096
run_trim "$no_limit" "$no_limit"
[ ! -e "$cache/aa/unused" ] || fail "the age pass kept a cache file unused for three hours"
[ -e "$cache/bb/fresh" ] || fail "the age pass removed a cache file used ten minutes ago"
[ -e "$cache/cc/half" ] || fail "the age pass removed a cache file used 90 minutes ago"
[ -d "$cache/aa" ] || fail "the age pass removed a cache hash folder"

# The size pass never removes a file used in the last 70 minutes, and it says in
# one line that it stopped for that reason. A file used 90 minutes ago is
# younger than the two-hour age pass, so only the size pass can remove it.
new_case
cache_file aa/old 90 4096
cache_file bb/hot 65 4096
run_trim 1 0
[ ! -e "$cache/aa/old" ] || fail "the size pass kept a cache file unused for 90 minutes"
[ -e "$cache/bb/hot" ] || fail "the size pass removed a cache file used 65 minutes ago"
lines=$(grep -c 'only files used in the last 70 minutes are left' "$case_root/out" || true)
[ "$lines" = 1 ] ||
	fail "the size pass did not say in one line that only files used in the last 70 minutes are left: $(cat "$case_root/out")"

# When the size pass reaches the low limit, it says nothing about the window.
# The limits sit just under the cache size, so one removal reaches the low one
# on any filesystem, whatever a folder costs there.
new_case
cache_file aa/old 90 4096
size=$(du -sb "$cache" | cut -f1)
run_trim $((size - 500)) $((size - 1000))
[ ! -e "$cache/aa/old" ] || fail "the size pass kept a cache file unused for 90 minutes"
[ -z "$(cat "$case_root/out")" ] || fail "the size pass printed a line when it reached the low limit"

# A gotmp entry with a file written ten minutes ago stays, even when the folder
# itself looks idle. Entries idle for 115 minutes stay and for 125 minutes go.
new_case
mkdir -p "$gotmp/busy" "$gotmp/slow" "$gotmp/idle/inner"
dd if=/dev/zero of="$gotmp/busy/file" bs=1 count=1 2>/dev/null
touch -d '-10 minutes' "$gotmp/busy/file"
touch -d '-3 hours' "$gotmp/busy"
dd if=/dev/zero of="$gotmp/slow/file" bs=1 count=1 2>/dev/null
touch -d '-115 minutes' "$gotmp/slow/file" "$gotmp/slow"
dd if=/dev/zero of="$gotmp/idle/inner/file" bs=1 count=1 2>/dev/null
touch -d '-125 minutes' "$gotmp/idle/inner/file" "$gotmp/idle/inner" "$gotmp/idle"
run_trim "$no_limit" "$no_limit"
[ -d "$gotmp/busy" ] || fail "a gotmp entry written ten minutes ago was removed"
[ -d "$gotmp/slow" ] || fail "a gotmp entry written 115 minutes ago was removed"
[ ! -e "$gotmp/idle" ] || fail "a gotmp entry idle for 125 minutes was kept"
[ -d "$gotmp" ] || fail "the gotmp folder itself was removed"

# Other agent-net-* temp folders follow the installed copy's rule: nothing
# written for six hours, made writable first. A folder used 350 minutes ago
# stays, and a folder that is not named agent-net-* is left alone.
new_case
mkdir -p "$case_root/agent-net-idle/private"
dd if=/dev/zero of="$case_root/agent-net-idle/private/file" bs=1 count=1 2>/dev/null
touch -d '-370 minutes' "$case_root/agent-net-idle/private/file" "$case_root/agent-net-idle/private" "$case_root/agent-net-idle"
chmod a-w "$case_root/agent-net-idle/private"
mkdir -p "$case_root/agent-net-recent"
dd if=/dev/zero of="$case_root/agent-net-recent/file" bs=1 count=1 2>/dev/null
mkdir -p "$case_root/agent-net-five-hours"
touch -d '-350 minutes' "$case_root/agent-net-five-hours"
mkdir -p "$case_root/other-idle"
touch -d '-7 hours' "$case_root/other-idle"
# The cache and gotmp folder look idle too, so the sweep has to skip them by
# name rather than by age.
touch -d '-7 hours' "$cache" "$cache/README" "$gotmp"
run_trim "$no_limit" "$no_limit"
[ ! -e "$case_root/agent-net-idle" ] || fail "an agent-net temp folder idle for 370 minutes was kept"
[ -d "$case_root/agent-net-recent" ] || fail "an agent-net temp folder written now was removed"
[ -d "$case_root/agent-net-five-hours" ] || fail "an agent-net temp folder written 350 minutes ago was removed"
[ -d "$case_root/other-idle" ] || fail "a folder not named agent-net-* was removed"
[ -f "$cache/README" ] || fail "the temp folder sweep removed the cache folder"
[ -d "$gotmp" ] || fail "the temp folder sweep removed the gotmp folder"

# A trailing slash on the folders does not make the sweep take the cache or the
# gotmp folder for just another agent-net-* folder.
new_case
touch -d '-7 hours' "$cache" "$cache/README" "$gotmp"
status=0
AGENT_NET_GOCACHE="$cache/" AGENT_NET_GOTMPDIR="$gotmp/" AGENT_NET_TRIM_ROOT="$case_root/" \
	sh "$script" >"$case_root/out" 2>&1 || status=$?
[ "$status" = 0 ] || fail "the script with trailing slashes exited $status: $(cat "$case_root/out")"
[ -f "$cache/README" ] || fail "a trailing slash made the sweep remove the cache folder"
[ -d "$gotmp" ] || fail "a trailing slash made the sweep remove the gotmp folder"

echo "gocache-trim-test: ok"

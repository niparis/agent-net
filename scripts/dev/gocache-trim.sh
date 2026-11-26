#!/bin/sh
# Trim the Go build cache, build temp folder and other worker temp folders that
# agent-net workers share on a development machine. Run it every hour from a
# timer, see scripts/dev/README.md. Users of agent-net never need this.
set -eu
cache=${AGENT_NET_GOCACHE:-/tmp/agent-net-gocache}
gotmp=${AGENT_NET_GOTMPDIR:-/tmp/agent-net-gotmp}
trim_root=${AGENT_NET_TRIM_ROOT:-/tmp}
# A trailing slash would make the glob and the skip list below disagree, and the
# sweep would take the cache for just another agent-net-* folder.
cache=${cache%/}
gotmp=${gotmp%/}
trim_root=${trim_root%/}
gib=1073741824
# Go refreshes a cache file's time only when the file is about an hour old, so a
# file a build is reading can look up to an hour old. No pass may remove a file
# younger than this; 70 minutes leaves room for a build that read it just now.
hot=70
max_bytes=${AGENT_NET_GOCACHE_MAX_BYTES:-$((15 * gib))}
low_bytes=${AGENT_NET_GOCACHE_LOW_BYTES:-$((10 * gib))}

if [ -d "$cache" ]; then
	# Go refreshes a file's time when it uses it, so an old time means unused.
	# Files sit in hash folders; delete files, keep the folders.
	[ -f "$cache/README" ] || { echo "not a Go build cache: $cache" >&2; exit 1; }
	find "$cache" -mindepth 2 -type f -mmin +120 -delete
	# Safety net: above the high limit, delete the least recently used files
	# until the cache is under the low limit. Stop at the window above, so a
	# file a running build is reading is never removed.
	size=$(du -sb "$cache" | cut -f1)
	if [ "$size" -gt "$max_bytes" ]; then
		size=$(find "$cache" -mindepth 2 -type f -mmin +"$hot" -printf '%T@ %s %p\n' | sort -n |
			{
				while read -r _ bytes path; do
					[ "$size" -le "$low_bytes" ] && break
					rm -f "$path"
					size=$((size - bytes))
				done
				printf '%s\n' "$size"
			})
		if [ "$size" -gt "$low_bytes" ]; then
			echo "gocache-trim: $cache: size pass stopped; only files used in the last $hot minutes are left"
		fi
	fi
fi

if [ -d "$gotmp" ]; then
	# Each entry is one build's folder. Delete it only when nothing in it was
	# written in the last two hours, so a running build keeps its folder.
	find "$gotmp" -mindepth 1 -maxdepth 1 -mmin +120 | while read -r entry; do
		if [ -z "$(find "$entry" -mmin -120 -print -quit)" ]; then
			rm -rf "$entry"
		fi
	done
fi

# Sandboxes and test scripts make other folders next to those two. A folder can
# be read-only when a sandbox made it, so make it writable first. Remove one
# only when nothing inside it was written in the last six hours.
for entry in "$trim_root"/agent-net-*; do
	[ -e "$entry" ] || continue
	case $entry in
	"$cache" | "$gotmp") continue ;;
	esac
	if [ -z "$(find "$entry" -mmin -360 -print -quit 2>/dev/null)" ]; then
		chmod -R u+w "$entry" 2>/dev/null || true
		rm -rf "$entry" 2>/dev/null || true
	fi
done
exit 0

#!/bin/sh
# Trim the Go build cache and build temp folder that agent-net workers share
# on a development machine. Run it every hour from a timer, see
# scripts/dev/README.md. Users of agent-net never need this.
set -eu
cache=${AGENT_NET_GOCACHE:-/tmp/agent-net-gocache}
gotmp=${AGENT_NET_GOTMPDIR:-/tmp/agent-net-gotmp}
gib=1073741824

if [ -d "$cache" ]; then
	# Go refreshes a file's time when it uses it, so an old time means unused.
	# Files sit in hash folders; delete files, keep the folders.
	[ -f "$cache/README" ] || { echo "not a Go build cache: $cache" >&2; exit 1; }
	find "$cache" -mindepth 2 -type f -mmin +120 -delete
	# Safety net: above 15 GiB, delete the least recently used files until
	# the cache is under 10 GiB.
	size=$(du -sb "$cache" | cut -f1)
	if [ "$size" -gt $((15 * gib)) ]; then
		find "$cache" -mindepth 2 -type f -printf '%T@ %s %p\n' | sort -n |
			while read -r _ bytes path; do
				[ "$size" -le $((10 * gib)) ] && break
				rm -f "$path"
				size=$((size - bytes))
			done
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
exit 0

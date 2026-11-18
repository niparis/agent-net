#!/bin/sh
set -eu
if [ "$#" -ne 1 ]; then
  printf 'usage: %s /path/to/snapshot.db\n' "$0" >&2
  exit 2
fi
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
export LC_ALL=C TZ=UTC GOMAXPROCS=1
printf 'Build: %s\n' "$(git -C "$root" rev-parse HEAD)"
printf 'Step: replay the recorded rosters with V2 and floor sets.\n'
go -C "$root/tools/pickerreplay" run . -db "$1"
printf '\nErrors: none\n'

#!/usr/bin/env bash
# Check the shard planner without building or running Go tests.
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
script=${TEST_SCRIPT:-$root/scripts/test.sh}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/bin"
cat >"$work/bin/go" <<'GO'
#!/usr/bin/env bash
set -eu
if [[ $1 == list ]]; then
  printf '%s\n' github.com/niparis/agent-net/internal/{daemon,gateway,setup,tui} github.com/niparis/agent-net/cmd/agent-net github.com/niparis/agent-net/internal/protocol
  exit 0
fi
if [[ $1 == test && $2 == -race ]]; then
  printf '%s\n' "$*" >>"$SHARD_TEST_RUN_LOG"
  exit 0
fi
[[ $1 == test && $2 == -list && $3 == '.*' ]]
case "$4" in
  github.com/niparis/agent-net/internal/daemon|github.com/niparis/agent-net/internal/gateway|github.com/niparis/agent-net/internal/setup|github.com/niparis/agent-net/internal/tui|github.com/niparis/agent-net/cmd/agent-net) ;;
  *) echo "unexpected package: $4" >&2; exit 1 ;;
esac
printf '%s\n' "$4" >>"$SHARD_TEST_LOG"
printf '%s\n' TestAlpha TestBeta TestGamma TestDelta
GO
chmod +x "$work/bin/go"
export PATH="$work/bin:$PATH" SHARD_TEST_LOG="$work/log" SHARD_TEST_RUN_LOG="$work/runs"
bash "$script" --shards 2 --check-only >"$work/output"
for name in daemon gateway setup tui agent-net; do
  grep -Fx "Verified 4 $name tests across 2 shards exactly once." "$work/output"
done
[[ $(wc -l <"$work/log") == 5 ]]
for name in setup tui agent-net; do
  bash "$script" --package "$name" --shard-index 1 --shard-count 2 --check-only
done
if bash "$script" --package agent-net --shard-index 2 --shard-count 2 --check-only >"$work/error" 2>&1; then
  echo 'accepted an out-of-range shard' >&2
  exit 1
fi
grep -F 'shard index must be less than shard count' "$work/error"

# All-mode must execute both shards of every selected package and leave only
# the remaining package in its final job.
bash "$script" --shards 2 >"$work/run-output"
[[ $(wc -l <"$work/runs") == 11 ]]
for package in internal/daemon internal/gateway internal/setup internal/tui cmd/agent-net; do
  [[ $(grep -Fc " github.com/niparis/agent-net/$package" "$work/runs") == 2 ]]
done
grep -Fx 'test -race -count=1 github.com/niparis/agent-net/internal/protocol' "$work/runs"

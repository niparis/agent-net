#!/usr/bin/env bash

set -u

usage() {
  cat <<'EOF'
Usage: scripts/test.sh [--shards N | --shard-index I --shard-count N]
                      [--check-only] [-- go-test-args...]

Run the test suite with the internal/daemon tests split across processes.
TEST_SHARDS sets the local shard count. The default is half the available CPUs,
rounded down, with a minimum of one and a maximum of eight.

Shard indexes are zero-based. Indexed mode runs only that daemon shard, for CI.
Arguments after -- are passed to go test.
EOF
}

fail() {
  echo "scripts/test.sh: $*" >&2
  exit 2
}

is_positive_integer() {
  [[ $1 =~ ^[0-9]+$ ]] && ((10#$1 > 0))
}

is_nonnegative_integer() {
  [[ $1 =~ ^[0-9]+$ ]]
}

cpu_count() {
  local count
  count=$(getconf _NPROCESSORS_ONLN 2>/dev/null || printf '1')
  [[ $count =~ ^[0-9]+$ ]] || count=1
  printf '%s\n' "$count"
}

requested_shards=${TEST_SHARDS:-}
shard_index=
shard_count=
check_only=0
go_test_args=()

while (($#)); do
  case "$1" in
    --)
      shift
      go_test_args=("$@")
      break
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    --shards)
      (($# >= 2)) || fail "--shards requires a number"
      requested_shards=$2
      shift 2
      ;;
    --shard-index)
      (($# >= 2)) || fail "--shard-index requires a number"
      shard_index=$2
      shift 2
      ;;
    --shard-count)
      (($# >= 2)) || fail "--shard-count requires a number"
      shard_count=$2
      shift 2
      ;;
    --check-only)
      check_only=1
      shift
      ;;
    -*)
      go_test_args=("$@")
      break
      ;;
    *)
      fail "unexpected argument: $1"
      ;;
  esac
done

if [[ -n $shard_index || -n $shard_count ]]; then
  [[ -n $shard_index && -n $shard_count ]] || fail "--shard-index and --shard-count must be used together"
  is_nonnegative_integer "$shard_index" || fail "shard index must be a non-negative integer"
  is_positive_integer "$shard_count" || fail "shard count must be a positive integer"
  ((10#$shard_index < 10#$shard_count)) || fail "shard index must be less than shard count"
  [[ -z $requested_shards ]] || fail "use either --shards or indexed shard options"
  shard_mode=indexed
  shards=$((10#$shard_count))
  shard_index=$((10#$shard_index))
else
  shard_mode=all
  if [[ -z $requested_shards ]]; then
    cpus=$(cpu_count)
    requested_shards=$((cpus / 2))
    ((requested_shards >= 1)) || requested_shards=1
    ((requested_shards <= 8)) || requested_shards=8
  fi
  is_positive_integer "$requested_shards" || fail "shard count must be a positive integer"
  shards=$((10#$requested_shards))
fi

work=$(mktemp -d "${TMPDIR:-/tmp}/agent-net-test.XXXXXX") || fail "could not create temporary directory"
cleanup() {
  rm -rf "$work"
}
trap cleanup EXIT HUP INT TERM

daemon_pkg=github.com/niparis/agent-net/internal/daemon
mkdir -p "$work/listing"
if ! env \
  AGENT_NET_CONFIG_DIR="$work/listing/config" \
  AGENT_NET_STATE_DIR="$work/listing/state" \
  AGENT_NET_SOCKET="$work/listing/agent-net.sock" \
  AGENT_NET_GATEWAY_DATA="$work/listing/gateway" \
  AGENT_NET_CLAUDE_SESSIONS_DIR="$work/listing/claude-sessions" \
  go test -list '.*' "$daemon_pkg" >"$work/list-output"; then
  cat "$work/list-output" >&2
  fail "could not list tests in $daemon_pkg"
fi
awk '/^(Test|Example)[[:alnum:]_]*$/ { print }' "$work/list-output" >"$work/tests"
test_count=$(wc -l <"$work/tests" | tr -d ' ')
((test_count > 0)) || fail "no tests found in $daemon_pkg"

if ((shards > test_count)); then
  if [[ $shard_mode == indexed ]]; then
    fail "shard count $shards exceeds the $test_count daemon tests"
  fi
  shards=$test_count
fi

# Go's per-argument size limit is 128 KiB on common Unix systems. Keep the
# generated -run pattern below 100 KiB to leave room for the rest of argv.
regex_limit=100000
while :; do
  oversized=0
  for ((i=0; i<shards; i++)); do
    awk -v n="$shards" -v i="$i" '((NR - 1) % n) == i { print }' "$work/tests" >"$work/shard-$i"
    regex=$(paste -sd '|' "$work/shard-$i")
    regex_length=$(printf '%s' "$regex" | wc -c | tr -d ' ')
    if ((regex_length + 4 > regex_limit)); then
      oversized=1
    fi
  done
  if ((oversized == 0)); then
    break
  fi
  if [[ $shard_mode == indexed ]]; then
    fail "shard regex exceeds ${regex_limit} bytes; increase the CI shard count"
  fi
  shards=$((shards + 1))
done

cat "$work"/shard-* | sort >"$work/union"
sort "$work/tests" >"$work/tests-sorted"
if ! diff -u "$work/tests-sorted" "$work/union" >"$work/partition-diff"; then
  cat "$work/partition-diff" >&2
  fail "daemon shard lists do not contain each test exactly once"
fi
echo "Verified $test_count daemon tests across $shards shards exactly once."

if ((check_only)); then
  exit 0
fi

job_count=0
job_pids=()
job_labels=()

run_job() {
  local job_id=$1 output=$2
  shift 2
  local start end status run_root="$work/run-$job_id"
  mkdir -p "$run_root"
  start=$(date +%s)
  env \
    AGENT_NET_CONFIG_DIR="$run_root/config" \
    AGENT_NET_STATE_DIR="$run_root/state" \
    AGENT_NET_SOCKET="$run_root/agent-net.sock" \
    AGENT_NET_GATEWAY_DATA="$run_root/gateway" \
    AGENT_NET_CLAUDE_SESSIONS_DIR="$run_root/claude-sessions" \
    "$@" >"$output" 2>&1
  status=$?
  end=$(date +%s)
  printf '%s %s\n' "$status" "$((end - start))" >"$work/status-$job_id"
}

launch_job() {
  local label=$1 output=$2
  shift 2
  local job_id=$job_count
  job_count=$((job_count + 1))
  job_labels+=("$label")
  run_job "$job_id" "$output" "$@" &
  job_pids+=("$!")
}

if [[ $shard_mode == all ]]; then
  for ((i=0; i<shards; i++)); do
    regex=$(paste -sd '|' "$work/shard-$i")
    launch_job "daemon shard $((i + 1))/$shards" "$work/output-$job_count" \
      go test -race -count=1 -run "^($regex)$" "${go_test_args[@]}" "$daemon_pkg"
  done

  if ! go list ./... >"$work/packages"; then
    echo "scripts/test.sh: could not list Go packages" >&2
    exit 1
  fi
  other_packages=()
  while IFS= read -r package; do
    [[ $package == */internal/daemon || $package == */internal/daemon/* ]] || other_packages+=("$package")
  done <"$work/packages"
  if ((${#other_packages[@]})); then
    launch_job "other packages" "$work/output-$job_count" \
      go test -race -count=1 "${go_test_args[@]}" "${other_packages[@]}"
  fi
else
  regex=$(paste -sd '|' "$work/shard-$shard_index")
  launch_job "daemon shard $((shard_index + 1))/$shards" "$work/output-$job_count" \
    go test -race -count=1 -run "^($regex)$" "${go_test_args[@]}" "$daemon_pkg"
fi

# Preserve all output until every process finishes so failures can be printed
# in full while successful runs stay compact.
failed=0
for pid in "${job_pids[@]}"; do
  wait "$pid" || failed=1
done

for ((job_id=0; job_id<job_count; job_id++)); do
  read -r status duration <"$work/status-$job_id"
  if ((status == 0)); then
    printf '%s: PASS (%ss)\n' "${job_labels[$job_id]}" "$duration"
  else
    failed=1
    printf '%s: FAIL (%ss)\n' "${job_labels[$job_id]}" "$duration"
    cat "$work/output-$job_id"
  fi
done

exit "$failed"

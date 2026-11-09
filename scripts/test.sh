#!/usr/bin/env bash

set -u

usage() {
  cat <<'EOF'
Usage: scripts/test.sh [--shards N | --package NAME --shard-index I --shard-count N]
                      [--check-only] [-- go-test-args...]

Run the test suite. The tests of the sharded packages, internal/daemon and
internal/gateway, are split across processes by test name. Every other package
runs in one go test process.
TEST_SHARDS sets the local shard count for each sharded package. The default is
half the available CPUs, rounded down, with a minimum of one and a maximum of
eight.

Indexed mode runs one shard of one sharded package, for CI. --package names the
package (daemon or gateway). Shard indexes are zero-based.
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

# Packages under internal/ whose tests are split into shards by test name.
sharded_packages=(daemon gateway)
module_internal=github.com/niparis/agent-net/internal

is_sharded_package() {
  local name
  for name in "${sharded_packages[@]}"; do
    [[ $1 == "$name" ]] && return 0
  done
  return 1
}

requested_shards=${TEST_SHARDS:-}
package_name=
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
    --package)
      (($# >= 2)) || fail "--package requires a name"
      package_name=$2
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
  [[ -n $package_name ]] || fail "--package is required with --shard-index and --shard-count"
  is_sharded_package "$package_name" || fail "unknown package $package_name; use one of: ${sharded_packages[*]}"
  is_nonnegative_integer "$shard_index" || fail "shard index must be a non-negative integer"
  is_positive_integer "$shard_count" || fail "shard count must be a positive integer"
  ((10#$shard_index < 10#$shard_count)) || fail "shard index must be less than shard count"
  [[ -z $requested_shards ]] || fail "use either --shards or indexed shard options"
  shard_mode=indexed
  shards=$((10#$shard_count))
  shard_index=$((10#$shard_index))
else
  shard_mode=all
  [[ -z $package_name ]] || fail "--package needs --shard-index and --shard-count"
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

# plan_package NAME COUNT lists the tests of internal/NAME, splits them into
# COUNT shards by test name and checks that the shards hold each test exactly
# once. Shard files go to $work/NAME/. The number of shards used is left in
# planned_count, which can be lower than COUNT when there are fewer tests.
plan_package() {
  local name=$1 wanted=$2
  local pkg="$module_internal/$name" dir="$work/$name"
  local test_count i count oversized regex regex_length
  mkdir -p "$dir/listing"
  if ! env \
    AGENT_NET_CONFIG_DIR="$dir/listing/config" \
    AGENT_NET_STATE_DIR="$dir/listing/state" \
    AGENT_NET_SOCKET="$dir/listing/agent-net.sock" \
    AGENT_NET_GATEWAY_DATA="$dir/listing/gateway" \
    AGENT_NET_CLAUDE_SESSIONS_DIR="$dir/listing/claude-sessions" \
    go test -list '.*' "$pkg" >"$dir/list-output"; then
    cat "$dir/list-output" >&2
    fail "could not list tests in $pkg"
  fi
  awk '/^(Test|Example)[[:alnum:]_]*$/ { print }' "$dir/list-output" >"$dir/tests"
  test_count=$(wc -l <"$dir/tests" | tr -d ' ')
  ((test_count > 0)) || fail "no tests found in $pkg"

  count=$wanted
  if ((count > test_count)); then
    if [[ $shard_mode == indexed ]]; then
      fail "shard count $count exceeds the $test_count $name tests"
    fi
    count=$test_count
  fi

  # Go's per-argument size limit is 128 KiB on common Unix systems. Keep the
  # generated -run pattern below 100 KiB to leave room for the rest of argv.
  local regex_limit=100000
  while :; do
    oversized=0
    for ((i=0; i<count; i++)); do
      awk -v n="$count" -v i="$i" '((NR - 1) % n) == i { print }' "$dir/tests" >"$dir/shard-$i"
      regex=$(paste -sd '|' "$dir/shard-$i")
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
    count=$((count + 1))
  done

  cat "$dir"/shard-* | sort >"$dir/union"
  sort "$dir/tests" >"$dir/tests-sorted"
  if ! diff -u "$dir/tests-sorted" "$dir/union" >"$dir/partition-diff"; then
    cat "$dir/partition-diff" >&2
    fail "$name shard lists do not contain each test exactly once"
  fi
  echo "Verified $test_count $name tests across $count shards exactly once."
  planned_count=$count
}

if [[ $shard_mode == indexed ]]; then
  run_names=("$package_name")
else
  run_names=("${sharded_packages[@]}")
fi
run_counts=()
for name in "${run_names[@]}"; do
  plan_package "$name" "$shards"
  run_counts+=("$planned_count")
done

if ((check_only)); then
  exit 0
fi

# Tests must write under t.TempDir(). Note the untracked files before the run,
# so a file a test leaves in the source tree fails the run instead of landing
# in a commit.
untracked_files() {
  git ls-files --others --exclude-standard | LC_ALL=C sort
}
in_git=0
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  in_git=1
  untracked_files >"$work/untracked-before"
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

for idx in "${!run_names[@]}"; do
  name=${run_names[$idx]}
  count=${run_counts[$idx]}
  first=0
  last=$((count - 1))
  if [[ $shard_mode == indexed ]]; then
    first=$shard_index
    last=$shard_index
  fi
  for ((i=first; i<=last; i++)); do
    regex=$(paste -sd '|' "$work/$name/shard-$i")
    launch_job "$name shard $((i + 1))/$count" "$work/output-$job_count" \
      go test -race -count=1 -run "^($regex)$" "${go_test_args[@]}" "$module_internal/$name"
  done
done

if [[ $shard_mode == all ]]; then
  if ! go list ./... >"$work/packages"; then
    echo "scripts/test.sh: could not list Go packages" >&2
    exit 1
  fi
  other_packages=()
  while IFS= read -r package; do
    sharded=0
    for name in "${sharded_packages[@]}"; do
      [[ $package == */internal/$name ]] && sharded=1
    done
    ((sharded)) || other_packages+=("$package")
  done <"$work/packages"
  if ((${#other_packages[@]})); then
    launch_job "other packages" "$work/output-$job_count" \
      go test -race -count=1 "${go_test_args[@]}" "${other_packages[@]}"
  fi
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

if ((in_git)); then
  untracked_files >"$work/untracked-after"
  LC_ALL=C comm -13 "$work/untracked-before" "$work/untracked-after" >"$work/untracked-new"
  if [[ -s $work/untracked-new ]]; then
    failed=1
    echo "scripts/test.sh: tests left new files in the source tree; write them under t.TempDir():" >&2
    cat "$work/untracked-new" >&2
  fi
fi

exit "$failed"

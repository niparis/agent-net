#!/usr/bin/env bash
# Backtest the task-difficulty analyzer against workers that already ran.
#
# For every task that ended in a date range it runs `agent-net dev assess` on
# the task text and writes the six scores, D, the new label and the worker's
# outcome to one CSV row. It then says how many labels would move.
#
# This calls real models. It is a developer tool: run it on a development
# machine, with a built binary, and never from a test.
set -euo pipefail

usage() {
	cat <<'EOF'
usage: difficulty-backtest.sh --project DIR [options]

Assesses the worker tasks that ended in a date range and compares the new
label with the difficulty the worker was started at.

  --project DIR        repository folder the analyzer may read (required)
  --since YYYY-MM-DD   first day, default today
  --until YYYY-MM-DD   last day, inclusive, default the same day as --since
  --csv PATH           CSV output, - for stdout (default difficulty-backtest-DATE.csv)
  --model M            Codex model for the analyzer and judge (default: the
                       cheapest Codex profile of the worker roster)
  --timeout SECONDS    time limit for one task, default 180
  --limit N            assess only the first N tasks
  --gateway-db PATH    gateway database (default: $AGENT_NET_GATEWAY_DATA/gateway.db)
  --daemon-db PATH     local daemon database (default: $AGENT_NET_STATE_DIR/daemon.db)
  --binary PATH        agent-net binary (default: $AGENT_NET_BIN, else agent-net on PATH)
  --dry-run            print the tasks that would be assessed, call no model
  -h, --help           this text
EOF
}

die() { echo "difficulty-backtest: $*" >&2; exit 1; }

since=""
until=""
project=""
csv=""
model=""
timeout_s=180
limit=""
gateway_db=""
daemon_db=""
binary="${AGENT_NET_BIN:-agent-net}"
dry_run=0

while [ $# -gt 0 ]; do
	case "$1" in
	--project) project="${2:-}"; shift 2 ;;
	--since) since="${2:-}"; shift 2 ;;
	--until) until="${2:-}"; shift 2 ;;
	--csv) csv="${2:-}"; shift 2 ;;
	--model) model="${2:-}"; shift 2 ;;
	--timeout) timeout_s="${2:-}"; shift 2 ;;
	--limit) limit="${2:-}"; shift 2 ;;
	--gateway-db) gateway_db="${2:-}"; shift 2 ;;
	--daemon-db) daemon_db="${2:-}"; shift 2 ;;
	--binary) binary="${2:-}"; shift 2 ;;
	--dry-run) dry_run=1; shift ;;
	-h | --help) usage; exit 0 ;;
	*) die "unknown argument $1 (see --help)" ;;
	esac
done

[ -n "$project" ] || die "--project is required (see --help)"
[ -d "$project" ] || die "project folder $project does not exist"
project=$(cd "$project" && pwd)

today=$(date +%Y-%m-%d)
[ -n "$since" ] || since="$today"
[ -n "$until" ] || until="$since"
for day in "$since" "$until"; do
	date -d "$day 00:00:00" +%s >/dev/null 2>&1 || die "not a date: $day"
done
[ "$(date -d "$since 00:00:00" +%s)" -le "$(date -d "$until 00:00:00" +%s)" ] ||
	die "--since $since is after --until $until"
since_epoch=$(date -d "$since 00:00:00" +%s)
until_epoch=$(date -d "$until 00:00:00" +%s)
until_epoch=$((until_epoch + 86400))

state_dir="${AGENT_NET_STATE_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/agent-net/daemon}"
[ -n "$daemon_db" ] || daemon_db="$state_dir/daemon.db"
gateway_data="${AGENT_NET_GATEWAY_DATA:-$HOME/.local/state/agent-net/gateway}"
[ -n "$gateway_db" ] || gateway_db="$gateway_data/gateway.db"
[ -f "$gateway_db" ] || die "gateway database not found: $gateway_db (is this the gateway machine?)"
[ -f "$daemon_db" ] || die "daemon database not found: $daemon_db"

if [ "$dry_run" -eq 0 ]; then
	if printf '%s' "$binary" | grep -q /; then
		[ -x "$binary" ] || die "binary not executable: $binary"
	else
		binary=$(command -v "$binary" || true)
		[ -n "$binary" ] || die "agent-net is not on PATH; build it or pass --binary"
	fi
fi

if [ -z "$csv" ]; then
	if [ "$since" = "$until" ]; then
		csv="difficulty-backtest-$since.csv"
	else
		csv="difficulty-backtest-$since-to-$until.csv"
	fi
fi

work=$(mktemp -d "${TMPDIR:-/tmp}/difficulty-backtest.XXXXXX")
trap 'rm -rf "$work"' EXIT
records="$work/records.tsv"
skipped="$work/skipped.txt"

# Read the runs and join the local worker records (full task text) with the
# gateway records (outcome, rating and late defects). One row per task: a
# continued worker folds into the root task's session ID.
python3 - "$gateway_db" "$daemon_db" "$since_epoch" "$until_epoch" "$records" "$skipped" "$limit" <<'PY'
import base64
import json
import sqlite3
import sys

gateway_db, daemon_db, since, until, records_path, skipped_path = sys.argv[1:7]
limit = int(sys.argv[7] or 0)


def connect(path):
    conn = sqlite3.connect("file:" + path + "?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    return conn


gw = connect(gateway_db)
local = connect(daemon_db)

runs = gw.execute(
    """SELECT session_id, task_id, caller, model, harness, difficulty, difficulty_used,
              outcome, started_at, ended_at, run_seconds, continue_count, fix_count,
              midrun_count, auto_continue_count, touches
       FROM worker_runs WHERE ended_at >= ? AND ended_at < ?
       ORDER BY ended_at, id""",
    (int(since), int(until)),
).fetchall()

# Full task text and the daemon worker id, by worker session.
workers = {}
for row in local.execute(
    "SELECT id, session_id, task_full, task_text, started_at, last_active FROM workers"
):
    if row["session_id"]:
        workers[row["session_id"]] = row

# Fallbacks for a worker the daemon has already cleaned up.
session_tasks = {}
for row in gw.execute("SELECT id, worker FROM sessions WHERE worker <> ''"):
    try:
        data = json.loads(row["worker"])
    except ValueError:
        continue
    if isinstance(data, dict) and data.get("task"):
        session_tasks[row["id"]] = data["task"]
profile_tasks = {}
for row in gw.execute("SELECT session_id, task FROM profiles WHERE task <> ''"):
    profile_tasks[row["session_id"]] = row["task"]

ratings = {}
for row in gw.execute("SELECT session_id, score FROM worker_ratings"):
    ratings[row["session_id"]] = row["score"]
defects = {}
for row in gw.execute("SELECT session_id, severity FROM worker_defects"):
    defects.setdefault(row["session_id"], []).append(row["severity"])

outcomes = {}
groups = []
for run in runs:
    sid = run["session_id"]
    if not sid:
        continue
    root = run["task_id"] or sid
    group = outcomes.get(root)
    if group is None:
        group = {
            "root": root,
            "sessions": [],
            "model": "",
            "harness": "",
            "difficulty": "",
            "outcome": "",
            "cont": 0,
            "fix": 0,
            "run_seconds": 0,
        }
        outcomes[root] = group
        groups.append(group)
    if sid not in group["sessions"]:
        group["sessions"].append(sid)
    if not group["model"] and (sid == root or not group["model"]):
        group["model"] = run["model"] or ""
        group["harness"] = run["harness"] or ""
        group["difficulty"] = run["difficulty_used"] or run["difficulty"] or ""
    if sid == root or not group["outcome"]:
        group["outcome"] = run["outcome"] or group["outcome"]
    elif group["outcome"] == "stopped" and run["outcome"] != "stopped":
        group["outcome"] = run["outcome"]
    group["cont"] += run["continue_count"] or 0
    group["fix"] += run["fix_count"] or 0
    group["run_seconds"] += run["run_seconds"] or 0

skip_reasons = []
rows = []
for group in groups:
    root = group["root"]
    sessions = group["sessions"]
    task = ""
    worker_id = ""
    for sid in [root] + sessions:
        worker = workers.get(sid)
        if worker is None:
            continue
        if not worker_id:
            worker_id = worker["id"]
        if not task and worker["task_full"]:
            task = worker["task_full"]
    if not task:
        for sid in [root] + sessions:
            task = session_tasks.get(sid) or profile_tasks.get(sid) or ""
            if task:
                break
    task = (task or "").strip()
    if not task:
        skip_reasons.append("%s: no stored task text" % root)
        continue

    rating = ""
    late = 0
    worst = 0
    defect_minutes = 0
    minutes = {1: 10, 2: 30, 3: 60, 4: 180}
    for sid in sessions:
        if sid in ratings:
            rating = ratings[sid]
        for severity in defects.get(sid, []):
            late += 1
            worst = max(worst, severity)
            defect_minutes += minutes.get(severity, 0)
    headache = 2 * group["cont"] + 10 * group["fix"] + defect_minutes

    rows.append(
        {
            "worker": worker_id or root,
            "session": root,
            "model": group["model"],
            "harness": group["harness"],
            "difficulty": group["difficulty"],
            "outcome": group["outcome"],
            "cont": group["cont"],
            "fix": group["fix"],
            "rating": rating,
            "late": late,
            "worst": worst,
            "headache": headache,
            "task": task,
        }
    )

if limit > 0:
    rows = rows[:limit]

with open(records_path, "w", encoding="utf-8") as out:
    for row in rows:
        task = base64.b64encode(row["task"].encode("utf-8")).decode("ascii")
        fields = [
            row["worker"],
            row["session"],
            row["model"],
            row["harness"],
            row["difficulty"],
            row["outcome"],
            str(row["cont"]),
            str(row["fix"]),
            "" if row["rating"] == "" else str(row["rating"]),
            str(row["late"]),
            str(row["worst"]),
            str(row["headache"]),
            task,
        ]
        out.write("\x1f".join(fields) + "\n")

with open(skipped_path, "w", encoding="utf-8") as out:
    for reason in skip_reasons:
        out.write(reason + "\n")
PY

total=$(wc -l <"$records" | tr -d ' ')
skipped_count=$(wc -l <"$skipped" | tr -d ' ')
[ "$total" -gt 0 ] || die "no worker tasks with stored task text in $since..$until"

if [ "$dry_run" -eq 1 ]; then
	echo "dry run: $total tasks in $since..$until, project $project"
	echo
	while IFS=$'\x1f' read -r worker session model harness difficulty outcome cont fix rating late worst headache task_b64; do
		printf '%s' "$task_b64" | base64 --decode >"$work/task.txt"
		printf '%s  %s  %s  %s  %s\n' "$worker" "$session" "${model:--}" "${difficulty:--}" "$(head -n 1 "$work/task.txt")"
	done <"$records"
	if [ "$skipped_count" -gt 0 ]; then
		echo
		echo "skipped $skipped_count tasks:"
		sed 's/^/  /' "$skipped"
	fi
	exit 0
fi

csv_file="$csv"
summary_stream=stdout
if [ "$csv" = "-" ]; then
	csv_file="$work/rows.csv"
	summary_stream=stderr
fi
header="worker_id,session_id,model,harness,difficulty_used,outcome,continues,fixes,rating,late_defects,worst_severity,headache_minutes,reasoning,scope,coupling,uncertainty,verification,risk,D,new_label,label_move,bottleneck,confidence,notes,analyzer_model,judge_model,elapsed_ms,assess_error"
printf '%s\n' "$header" >"$csv_file"

done_count=0
error_count=0
model_args=()
[ -z "$model" ] || model_args=(--model "$model")
while IFS=$'\x1f' read -r worker session model_used harness difficulty outcome cont fix rating late worst headache task_b64; do
	printf '%s' "$task_b64" | base64 --decode >"$work/task.txt"
	done_count=$((done_count + 1))
	rowjson="$work/assess.json"
	errfile="$work/assess.err"
	errtext=""
	rm -f "$rowjson" "$errfile"
	rc=0
	timeout "$timeout_s" "$binary" dev assess --project "$project" --task-file "$work/task.txt" --json "${model_args[@]}" >"$rowjson" 2>"$errfile" || rc=$?
	if [ "$rc" -ne 0 ]; then
		errtext="exit $rc: $(tr '\n' ' ' <"$errfile" 2>/dev/null | cut -c1-300)"
		error_count=$((error_count + 1))
		[ -s "$rowjson" ] || rm -f "$rowjson"
	fi
	python3 - "$csv_file" "$worker" "$session" "$model_used" "$harness" "$difficulty" "$outcome" \
		"$cont" "$fix" "$rating" "$late" "$worst" "$headache" "$rowjson" "$errtext" <<'PY'
import csv
import json
import os
import sys

(
    path, worker, session, model, harness, difficulty, outcome,
    cont, fix, rating, late, worst, headache, rowjson, errtext,
) = sys.argv[1:16]

order = ["trivial", "simple", "normal", "hard", "very_complex"]
a = {}
if rowjson and os.path.exists(rowjson) and os.path.getsize(rowjson) > 0:
    try:
        with open(rowjson, encoding="utf-8") as f:
            a = json.load(f)
    except ValueError as e:
        errtext = (errtext + " " if errtext else "") + "bad JSON: %s" % e

scores = a.get("scores") or {}

def text(value):
    if value is None:
        return ""
    return str(value).replace("\r", " ").replace("\n", " ").strip()

def number(value):
    return value if isinstance(value, (int, float)) else ""

new_label = text(a.get("label"))
move = "unknown"
if new_label in order and difficulty in order:
    move = "keep"
    if order.index(new_label) > order.index(difficulty):
        move = "up"
    elif order.index(new_label) < order.index(difficulty):
        move = "down"

elapsed = a.get("elapsed")
elapsed_ms = round(elapsed / 1e6, 1) if isinstance(elapsed, (int, float)) else ""

row = [
    worker, session, model, harness, difficulty, outcome, cont, fix, rating, late, worst, headache,
    number(scores.get("Reasoning", scores.get("reasoning"))),
    number(scores.get("Scope", scores.get("scope"))),
    number(scores.get("Coupling", scores.get("coupling"))),
    number(scores.get("Uncertainty", scores.get("uncertainty"))),
    number(scores.get("Verification", scores.get("verification"))),
    number(scores.get("Risk", scores.get("risk"))),
    number(a.get("D", a.get("d"))),
    new_label,
    move,
    text(a.get("bottleneck")),
    text(a.get("confidence")),
    text(a.get("notes")),
    text(a.get("analyzer_model")),
    text(a.get("judge_model")),
    elapsed_ms,
    text(errtext),
]
with open(path, "a", newline="", encoding="utf-8") as f:
    csv.writer(f, lineterminator="\n").writerow(row)
PY
	label=$(python3 - "$rowjson" <<'PY'
import json, sys
try:
    with open(sys.argv[1], encoding="utf-8") as f:
        print(json.load(f).get("label", "?"))
except Exception:
    print("?")
PY
)
	printf '[%d/%d] %s %s %s\n' "$done_count" "$total" "$worker" "${label}" "$(head -n 1 "$work/task.txt" | cut -c1-70)" >&2
done <"$records"

if [ "$csv" = "-" ]; then
	cat "$csv_file"
fi

python3 - "$csv_file" "$summary_stream" "$total" "$skipped_count" "$error_count" <<'PY'
import csv
import sys
from collections import defaultdict

path, stream, total, skipped, errors = sys.argv[1:6]
order = ["trivial", "simple", "normal", "hard", "very_complex"]
moves = {"up": 0, "down": 0, "keep": 0, "unknown": 0}
headache = defaultdict(float)
tasks = defaultdict(int)
with open(path, encoding="utf-8") as f:
    for row in csv.DictReader(f):
        moves[row["label_move"]] = moves.get(row["label_move"], 0) + 1
        if row["new_label"]:
            tasks[row["new_label"]] += 1
            try:
                headache[row["new_label"]] += float(row["headache_minutes"])
            except ValueError:
                pass

lines = [
    "tasks assessed: %s (skipped %s without task text, %s failed)" % (total, skipped, errors),
    "label moves: up %d, down %d, keep %d, unknown %d" % (
        moves["up"], moves["down"], moves["keep"], moves["unknown"]),
    "average headache per new label:",
]
for label in order:
    if not tasks[label]:
        continue
    lines.append("  %-13s %6.1f min over %d tasks" % (
        label, headache[label] / tasks[label], tasks[label]))
out = sys.stdout if stream == "stdout" else sys.stderr
out.write("\n".join(lines) + "\n")
PY

if [ "$csv" != "-" ]; then
	echo
	echo "csv: $csv"
fi

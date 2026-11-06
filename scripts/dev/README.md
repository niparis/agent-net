# Development machine scripts

These scripts are for machines where agent-net itself is developed. Users of
agent-net never need them.

## gocache-trim.sh

Workers that build agent-net share one Go build cache, `/tmp/agent-net-gocache`,
and one build temp folder, `/tmp/agent-net-gotmp` (see `.agent-net/worker.md`).
Go never trims its cache by size, so the script:

- deletes cache files unused for two hours;
- above 15 GiB, deletes the least recently used files until the cache is under 10 GiB;
- deletes build temp folders with nothing written in the last two hours.

Run it every hour with a systemd user timer:

```
install -m 755 scripts/dev/gocache-trim.sh ~/.local/bin/agent-net-gocache-trim
mkdir -p ~/.config/systemd/user
printf '[Unit]\nDescription=Trim the agent-net dev Go cache\n\n[Service]\nType=oneshot\nExecStart=%%h/.local/bin/agent-net-gocache-trim\n' > ~/.config/systemd/user/agent-net-gocache-trim.service
printf '[Unit]\nDescription=Trim the agent-net dev Go cache every hour\n\n[Timer]\nOnBootSec=10min\nOnUnitActiveSec=1h\n\n[Install]\nWantedBy=timers.target\n' > ~/.config/systemd/user/agent-net-gocache-trim.timer
systemctl --user daemon-reload
systemctl --user enable --now agent-net-gocache-trim.timer
```

To stop it: `systemctl --user disable --now agent-net-gocache-trim.timer`.

## difficulty-backtest.sh

Compares the task-difficulty analyzer with workers that already ran. It reads
the tasks that ended in a date range, runs the hidden `agent-net dev assess`
command on each task text, and writes one CSV row per task.

This calls real models, so it is not part of CI. Build the binary first, then
run it on the machine that holds the gateway records:

```
go build -o /tmp/agent-net ./cmd/agent-net
scripts/dev/difficulty-backtest.sh --project /path/to/repo \
  --since 2026-11-02 --until 2026-11-02 --binary /tmp/agent-net
```

Options:

| Option | Meaning |
|---|---|
| `--project DIR` | project folder the analyzer may read (required) |
| `--since DATE` | first day, default today |
| `--until DATE` | last day, inclusive; default the same day as `--since` |
| `--csv PATH` | CSV output; `-` writes it to stdout and the summary to stderr |
| `--model M` | Codex model for the analyzer and judge; default is the cheapest Codex profile of the worker roster |
| `--timeout SECONDS` | time limit for one task, default 180 |
| `--limit N` | assess only the first N tasks |
| `--gateway-db PATH` | gateway database; default `$AGENT_NET_GATEWAY_DATA/gateway.db` |
| `--daemon-db PATH` | local daemon database; default `$AGENT_NET_STATE_DIR/daemon.db` |
| `--binary PATH` | `agent-net` binary; default `$AGENT_NET_BIN`, else `agent-net` on PATH |
| `--dry-run` | print the tasks that would be assessed, call no model |

The default CSV is `difficulty-backtest-DATE.csv` in the current folder.

### Where the records come from

The script reads two SQLite databases in read-only mode and joins them on the
worker session ID:

- the local daemon database (`workers` table): the full first task text, the
  worker ID, the harness, the model and the difficulty that was used;
- the gateway database (`worker_runs`, `worker_ratings`, `worker_defects`):
  the outcome, run time, continues, fixes, rating and late defects.

Both files are opened with `mode=ro`; the script never writes to them. If the
daemon database is not on this machine, copy it here or pass `--daemon-db`.

### CSV columns

| Column | Meaning |
|---|---|
| `worker_id` | the worker record ID on its machine |
| `session_id` | the root worker session (a continued task folds into it) |
| `model`, `harness` | what ran the task |
| `difficulty_used` | the difficulty the worker was started at |
| `outcome` | `complete`, `partial`, `failed`, `timed_out` or `stopped` |
| `continues`, `fixes` | the caller's rework counters |
| `rating` | the caller's 1 to 5 rating, empty when there is none |
| `late_defects`, `worst_severity` | defects reported later, and the worst severity (1 to 4) |
| `headache_minutes` | continue 2 + fix 10 + late defect 10/30/60/180 by severity, as the Quotas panel counts it |
| `reasoning`, `scope`, `coupling`, `uncertainty`, `verification`, `risk` | the six judge scores, 1 to 5 |
| `D` | the weighted score |
| `new_label` | the label the analyzer would give |
| `label_move` | `up`, `down`, `keep` or `unknown` against `difficulty_used` |
| `bottleneck`, `confidence`, `notes` | the assessment notes |
| `analyzer_model`, `judge_model` | the one-shot models |
| `elapsed_ms` | how long the assessment took |
| `assess_error` | why a task could not be assessed, empty on success |

The summary prints how many labels would move up, down or stay, and the
average headache per new label.

A dry run prints the tasks without starting a model:

```
scripts/dev/difficulty-backtest.sh --project /path/to/repo --dry-run
```


## capability-backtest.py

Shows what the model capability plan (`docs/design/model-capability.md`) would
pick with today's data: each model's capability C and the odds for sample D
values, next to today's picker. It reads a copy of the gateway database and
never the live one.

```
cp ~/.local/state/agent-net/gateway/gateway.db* /tmp/gw/
python3 scripts/dev/capability-backtest.py /tmp/gw/gateway.db
```

## worker-data-review.py

Prints every table in `docs/design/model-capability-review.md`: outcomes by
model, effort and harness, outcomes by level and by D bucket, the judge's
correlations with outcomes, the level cut-off windows, the review results, the
picker counts and the stored plan rebuilt on today's data.

```
cp ~/.local/state/agent-net/gateway/gateway.db* /tmp/gw/
python3 scripts/dev/worker-data-review.py /tmp/gw/gateway.db
```

The script opens the copy read only, uses only the standard library, and writes
nothing. It folds the repeated `worker_runs` rows for one session into one
task, because each report writes another row with the same start time.

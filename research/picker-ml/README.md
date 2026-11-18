# picker-ml

An offline study. It asks whether a model trained on agent-net's own worker
records can predict, before a worker starts, how each candidate model will do
on a task, better than the picker's binned table. The answer and the numbers
are in [RESULTS.md](RESULTS.md).

It is research only. It is not part of the Go module, the binary or CI, and it
changes nothing in the live picker.

## Run

You need [uv](https://docs.astral.sh/uv/) and a snapshot copy of the gateway
database (never the live store):

    uv run python -m picker_ml.study /path/to/gateway-snapshot.db

It runs in about 10 seconds on one core and writes `results/tables.md` and
`results/results.json`. The run is deterministic: the same snapshot gives the
same files. The snapshot is opened read only with SQLite's `immutable=1`, so
nothing is written next to it.

The benchmark scores come from `internal/protocol/scores.json` in this
repository, so run it from a checkout.

## Files

| File | What |
|---|---|
| `picker_ml/data.py` | rebuilds tasks the way `internal/gateway/picker.go` does, with the time of every later event, and the price book |
| `picker_ml/labels.py` | today's score, option A's score and the clean label, each as of a given time |
| `picker_ml/features.py` | pre-start features, and when each one is known |
| `picker_ml/models.py` | today's table, the partially pooled models, the two-part model and the gradient boosting check |
| `picker_ml/metrics.py` | MAE, RMSE, log loss, Brier, AUC, calibration and the paired bootstrap |
| `picker_ml/study.py` | the split, tuning, every table in `results/tables.md` |
| `results/tables.md` | generated; do not edit by hand |

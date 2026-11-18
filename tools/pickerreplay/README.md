# picker replay

An offline replay of the worker picker against a read-only copy of the gateway
store. It exists for decision [0173](../../docs/decisions/0173-picker-quality.md):
it runs today's rule and the opt-in rule at several alpha values and clean-rate
floors, and prints the tables
that decision reports.

It is not wired into the daemon or the gateway, and it adds nothing to the
agent-net binary: it is its own Go module.

## Run

    go -C tools/pickerreplay run . -db /path/to/gateway-snapshot.db

Flags:

- `-db` (required): path to a copy of the gateway store. Never point it at the
  live store.
- `-alphas` (default `0,2,4,6,8,10`): opt-in alpha values.
- `-floors` (default `A,B,C`): floor sets to replay. Pass an empty value to omit them.

## What it reads

The snapshot must hold `worker_runs`, `worker_assessments`, `worker_defects`,
`worker_defect_auto`, `delivery_changes`, `delivery_evidence`, `worker_usage`,
`owner_settings`, `model_prices` and `pick_candidates`. The tool opens the file
with `mode=ro` and `query_only`, and never calls a gateway `Store` method,
because a store open migrates the file. A test hashes the file before and after
a replay to pin this.

## How it reproduces today's picks

For each recorded pick it rebuilds the picker table as it stood at that moment:
only runs that had ended, judge results that had arrived, and defects, failed
steps and merges that had been recorded by then. It reuses the live scoring code
where it can: `protocol.PickerTaskScore`, `protocol.PickerTaskScoreV2`, `protocol.BuildPickerTable`,
`protocol.PickerBucketVerdicts` and `protocol.WorkerBenchmarkCeiling`. The cost
rule is shared with the live picker through `protocol.PickerExpectedCosts`.

The output starts with the replay check (how many recorded picks the rule
reproduces and why the rest differ), then the model counts and mean clean p and
expected cost per method, then the D 3.20 case.

## Tests

    go -C tools/pickerreplay vet ./...
    go -C tools/pickerreplay test -run '^(TestOptionAScoreCountsEachFailureOnce|TestPickTodayTakesTheLowestExpectedCost|TestPickOptionAWeighsQualityAgainstCost|TestReplayReadsSnapshotAndMatchesTheRecordedPick|TestTasksAtLeavesOutWhatDidNotExistYet)$'

The tests build a throwaway store copy in a temporary directory. They never
touch the real setup.
The replay uses each recorded candidate roster and quota pace factor. It does
not simulate exploration. A floor applies to the judge level selected by D;
when no qualifying candidate meets it, the replay uses today's pick and counts
the fallback. A fallback's expected cost uses today's cost rule.

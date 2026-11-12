# 0132 Live model lookup instead of a scanned inventory

Date: 2026-11-11

## Decision

agent-net does not keep a periodically scanned model inventory as the source of
truth. A daemon reads a harness's model list live when someone needs it:

- `agent_machine_get` with `include: ["models"]` forwards a `node.models`
  request to that machine. The machine runs the harness there and then. An
  offline machine answers `machine <name> is offline, cannot list live`; the
  last reported list is not used as a substitute.
- `worker_profile_add` checks the model against the live list on each chosen
  machine, or on every online machine with the harness when it names none.
- A failed worker start reports the harness's own error, then one live lookup on
  that machine filtered to the model's base name.

One live read per machine and harness is kept for at most five minutes, at the
daemon and at the gateway. This is only a burst cache: a second call inside the
window does not run the harness again.

The six hour scan and its empty-probe retry loop are gone. Harness detection
still records which binaries exist, their paths and versions, and runs at start
and when a harness update check needs it. A stored model list may still appear
in the old inventory for display, but no decision refuses a start because of it.

A worker profile's `available_on` comes from its configured `machines` plus the
machines where it started a worker successfully before (`worker_runs`). The
picker uses the same availability.

## Why

The Alibaba provider appeared in the inventory only after a daemon restart,
because the scan ran every six hours. In the other direction, a scan can keep
offering a model whose login expired or whose monthly limit is spent. The real
test of a model is a start. The owner asked for checks at the moment of use.

## Consequences

- `opencode models`, `pi --list-models` and `agy models` run when a caller asks,
  so a call can take as long as the harness probe (bounded by the existing 20
  second limit).
- A model that a stored list lacks is launched anyway. When the harness rejects
  it, the notice explains why instead of a pre-launch refusal.
- No new setting was added. Quota checks are unchanged.
# 0023: Model lists for every harness, and recent success

Status: accepted. Amends W17 of [0002](0002-workers.md).

## The problem

Only OpenCode reported a model list, so a loose name for another harness was
passed through unchanged. A Claude session asked for a Codex worker with
"luna 6"; Codex refused it, and the session had to find `gpt-6-luna` itself
and start again. The second start then waited in the admission queue because
the failed start still counted towards the 60 second gap.

## What we do

- **Lists.** The daemon inventory reports a model list for every harness.
  OpenCode: `opencode models`. Pi: `pi --list-models` (its table gives
  `provider/model`). Codex: `codex app-server` over stdio, JSON-RPC
  `model/list`, including hidden models and following `nextCursor`. Claude
  Code has no list command, so its aliases and the full names we know are a
  small documented table. Each read is bounded like the OpenCode one. A
  harness whose list cannot be read reports no list, and its model names pass
  through.
- **Resolution.** The gateway uses the same resolver for every harness,
  against the list of the machine that runs the worker. The start result says
  `model: luna 6 -> gpt-6-luna`. An exact id still wins. A Pi `:thinking`
  suffix is kept while the base id is resolved.
- **Recent success.** A model that finished a worker with a hand-back status
  done on that node and harness ranks before models without such a success.
  The gateway remembers it for `ModelSuccessDays` in `model_successes`, like
  `model_failures` (24 hours, still ranked last, which still wins over a
  success).
- **No match.** The start is refused before a worker runs, with up to five
  closest names: `no codex model matches "luna 6" on research; closest:
  gpt-6-luna, gpt-6.1-luna`.
- **Gap.** Only a start that ran holds the 60 second gap. A start that failed,
  or a worker that ended within 30 seconds, is cleared so the next start need
  not wait.

## Later

The Claude table is static until Claude Code exposes a list. The success
window (7 days) is a guess; the failure window stays 24 hours.

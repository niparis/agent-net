# 0133 - agent-net is the only harness updater

**Date:** 2026-11-11
**Status:** Accepted.

## Context

Two updaters ran on the same Claude Code install. Claude Code's own background
updater runs in every session, and the daemon runs `claude update` about every
30 minutes. When the in-session updater lost the race it showed "update failed
/ run claude doctor", and the owner saw a failure that was not one.

On the Mac, Claude Code 2.1.289 was installed by Homebrew. `claude update`
defers to the package manager and does not change the version, so the daemon
reported "already current" every round while research ran 2.1.322 from the
native installer. OpenCode was the only harness with a Homebrew branch, and it
reported "update by hand" without running anything.

The version probe has a five second limit. On a loaded machine (`opencode
--version` takes about 0.1 s normally, load was near 127) it returned nothing,
and the daemon reported "failed: version probe failed" for a harness that was
fine.

## Decision

1. agent-net's daemon is the only updater for every harness on every machine.
   Every Claude Code process agent-net starts carries `DISABLE_AUTOUPDATER=1`
   in its environment: owner starts and resumes, workers, the merger, the CTO,
   sign-in panes and the compaction attach. One helper in
   `internal/claudelaunch` adds it, and every launch path calls that helper, so
   a new path cannot forget it.
2. The same key is written to `env` in the user's Claude settings
   (`~/.claude/settings.json`, or `$CLAUDE_CONFIG_DIR/settings.json`), with the
   same ownership rules as `CLAUDE_CODE_DISABLE_MOUSE` (decision 0128): merge
   only that key, keep the file mode, write through a temp file and a rename,
   put a drifted value back, never touch a file that is not valid JSON, and
   remove it on uninstall only while its value is still `"1"`. Setup writes it
   on a new machine.
3. A harness whose binary resolves into a Homebrew prefix is upgraded with
   `brew upgrade`, not with its own updater: `brew upgrade --cask claude-code`
   for the Claude Code cask (a formula is `brew upgrade claude-code`), and
   `brew upgrade <name>` for the other harnesses. Busy sessions are still
   skipped and the same five minute limit applies. An updater whose output says
   a package manager owns the install is never reported as "already current".
4. A version probe stopped by its deadline reports "timed out, retrying", not
   "failed", and the harness stays pending so its probe runs again on the next
   round. A probe that ran and failed still reports "failed".

## Consequences

A running session keeps its old environment until it starts again. Claude
Code's background service inherits the launching environment, so the daemon's
launches keep it clean. `agent-net nodes --wide` and the graph show the update
result, including the new "timed out, retrying" line. Homebrew copies are
upgraded by the package manager that owns them, so the reported version
actually changes after a successful upgrade.
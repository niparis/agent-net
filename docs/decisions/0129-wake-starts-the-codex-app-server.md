# 0129 - A wake starts the Codex app server when it is not running

**Date:** 2026-11-09
**Status:** Built.
**Context:** On 2026-11-09 at about 23:24 UTC, a message to the chief of staff (an offline Codex session) failed to wake it: `cannot resume the Codex thread: lstat ~/.codex/app-server-control/app-server-control.sock: no such file or directory`. The machine had restarted at 23:20, and nothing had started the shared Codex app server. [Decision 0012](0012-codex-peers.md) says agent-net never starts the shared daemon, so a wake gave up.

**Decision.** Only a wake starts it. When `thread/resume` cannot reach the control socket (the socket is missing, or nothing listens on it), the wake runs `codex app-server daemon start`, waits for the control socket until the worker start timeout, and tries the resume once more. `daemon start` is Codex's own command: it does nothing when the daemon already runs. It runs with `CODEX_HOME` set when the configured Codex home is not the default. If it fails, the wake fails with the command's output, cut to 300 characters with secrets masked.

**What does not change.** Discovery stays a read-only client and still starts nothing. Agent-net writes no Codex file or setting itself. Session start and Enter do not start the daemon. The wake limits are unchanged: autonomy or `wake_on_message`, never a worker, one attempt per session per 10 minutes.

**Checked.** With codex-cli 0.167.0 in a temporary `CODEX_HOME`, `codex app-server daemon start` creates `$CODEX_HOME/app-server-control/app-server-control.sock` and returns at once. The daemon outlives the command.

# Idle compaction for background Claude sessions

Claude Code's idleCompaction setting covers interactive sessions. Background
sessions can instead lose their prompt cache and be stopped by the supervisor
before the owner returns. The daemon checks local Claude sessions once a minute.

Compact only a session whose file says kind bg and supplies a jobId, whose status
is idle, whose last session-file or transcript write is at least 50 minutes old,
and whose latest assistant context is at least 200000 tokens. Context is input
plus cache read plus cache creation, the same calculation used for workers.
These three settings are live recipe advice: sessions.idle_compaction (true),
sessions.idle_compaction_after (50m), and sessions.idle_compaction_min_tokens
(200000). The owner edits them in Settings. The CTO can apply them immediately.

Use ps -axww -o pid=,args= on Linux and macOS to find a running claude attach jobId
client. Match the executable name and complete job ID as words. Skip attached
sessions, and skip the scan if ps fails. Check again before typing, excluding our
own attach PID. This check cannot prevent a client attaching in the instant
between that check and the write.

Inbox delivery cannot invoke /compact: Claude treats it as a peer message.
Instead run claude attach jobId in a 160 by 40 pseudo-terminal, wait up to 30
seconds for the prompt, write /compact followed by carriage return, and wait up
to five minutes for a new compact_boundary transcript record. Close the terminal,
terminate and reap only the attach client, then check the background PID lives.
Log success or failure. A successful session row says compacted while idle HH:MM
in the owner's display time zone until its next busy stretch.

Attempt at most once per idle stretch, including failures. Discovery observes
busy transitions every poll and resets the guard for the next idle stretch.
Ignore the compaction's own busy transition until it returns to idle. The guard
is in daemon memory and resets on daemon restart. The CTO's existing compaction
loop controls OpenCode only, so no Claude CTO session is already covered by it.

Tests use a fake attach binary and temporary session files and transcripts.
No test attaches to an owner's session or uses model credits.

# 0084. Keep subagent trees visible on the Sessions tab

Date: 2026-11-08

Decision 0079 folds unpinned idle trees into a count. That hid a project's
merger when its harness was idle, so the owner could not see the merge queue.
Subagents are never folded now.

A worker or merger is always drawn under its lead, whatever its own status,
and the lead row stays as context even when it is idle. The whole tree is
therefore shown. z and Z still collapse trees when the owner chooses.

A merger hangs under, in order:

1. the explicit online repo lead of its project;
2. the online agent of the project that most recently called one of its open
   changes (the caller of the freshest open change);
3. the session that started it.

With none of those it stays at the top level, and it is still shown.

A merger with a ready or running step stays in the Working group even when its
harness reports idle or shell. Only top-level idle agents that are not
subagents fold into the idle count.

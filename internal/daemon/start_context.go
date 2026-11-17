package daemon

import (
	"context"
)

// The start block is the standing orders, the note titles and the charter of a
// session. Each harness gets it at start (ctoFirstTurn and the system prompt for
// the CTO, the thread instructions for Codex). Claude gets it again after a
// compaction from its PostCompact hook, which is the charter's per-turn path
// (policy.go). The other harnesses do not have that hook, so after their
// compaction event the next turn carries the block again (decision 0171, part b).

// markStartContextDue records that a session compacted, so its next turn carries
// the start block again. An empty session ID changes nothing.
func (d *Daemon) markStartContextDue(sid string) {
	if sid == "" {
		return
	}
	d.startDueMu.Lock()
	defer d.startDueMu.Unlock()
	if d.startDue == nil {
		d.startDue = map[string]bool{}
	}
	d.startDue[sid] = true
}

// takeStartContextDue reports whether the session's next turn must carry the
// start block, and clears the mark: one compaction sends the block once.
func (d *Daemon) takeStartContextDue(sid string) bool {
	d.startDueMu.Lock()
	defer d.startDueMu.Unlock()
	due := d.startDue[sid]
	delete(d.startDue, sid)
	return due
}

// prependStartBlock puts a start block in front of a turn's text.
func prependStartBlock(block, text string) string {
	if block == "" {
		return text
	}
	return block + "\n\n" + text
}

// codexStartBlock is what a Codex thread gets as developer instructions when it
// starts, and what it gets again after a compaction: the charter of its project
// and the titles of its notes. A session with no gateway ID has no notes yet.
func (d *Daemon) codexStartBlock(ctx context.Context, sid, project string) string {
	notes := ""
	if sid != "" {
		_, notes = d.sessionContext(ctx, sid, project)
	}
	return joinCharter(d.codexThreadInstructions(ctx, project), notes)
}

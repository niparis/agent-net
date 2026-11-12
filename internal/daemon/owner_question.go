package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// An owner question shows in the agent's own chat AND follows the route to the
// chief and the Desk (decision 0150). When a turn ends on a question to the
// owner (an agent_owner_ask left open, or a final line marked "Owner
// question:"), the gateway records it as a chief-bound ask. The turn ends as
// it is. Claude Code reports the turn end from its Stop hook; Pi and OpenCode
// at the start of their next turn, with the policy.

// turnEndRecheck is how long a question the gateway did not route is left
// alone before it is checked again.
const turnEndRecheck = 5 * time.Minute

// turnEndKeep is how many questions are remembered per session.
const turnEndKeep = 16

// turnEndAsks remembers, per session, the questions already checked at a turn
// end. A routed question is never sent again.
type turnEndAsks struct {
	mu sync.Mutex
	by map[string][]turnEndMark
}

type turnEndMark struct {
	key    string
	at     time.Time
	routed bool
}

// seen tells whether the question needs no check now.
func (t *turnEndAsks) seen(session, key string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, m := range t.by[session] {
		if m.key == key {
			return m.routed || now.Sub(m.at) < turnEndRecheck
		}
	}
	return false
}

func (t *turnEndAsks) mark(session, key string, now time.Time, routed bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.by == nil {
		t.by = map[string][]turnEndMark{}
	}
	list := t.by[session][:0:0]
	for _, m := range t.by[session] {
		if m.key != key {
			list = append(list, m)
		}
	}
	list = append(list, turnEndMark{key: key, at: now, routed: routed})
	if len(list) > turnEndKeep {
		list = list[len(list)-turnEndKeep:]
	}
	t.by[session] = list
}

// recordTurnEndQuestion sends the question a session's turn ended on to the
// gateway, which routes it. last is the turn's final message when the harness
// gives it. Every failure is ignored: the turn ends either way.
func (d *Daemon) recordTurnEndQuestion(ctx context.Context, ls *localSession, sid, last string) {
	if d.workerBySession(sid) != nil {
		return
	}
	question, asked := "", false
	if w := d.infoOf(ls).Waiting; w != nil && w.Explicit && strings.TrimSpace(w.Text) != "" {
		question, asked = w.Text, true
	} else {
		question = protocol.OwnerQuestionLine(last)
	}
	if question == "" {
		return
	}
	key := protocol.NormalizeAskText(question)
	now := time.Now()
	if d.turnEnds.seen(sid, key, now) {
		return
	}
	raw, err := d.call(ctx, "ask.turn_end", map[string]any{"session_id": sid, "text": question, "asked": asked})
	if err != nil {
		return
	}
	var res struct {
		Routed bool `json:"routed"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return
	}
	d.turnEnds.mark(sid, key, now, res.Routed)
}

package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// No agent asks the owner in its own chat (decision 0150). When a turn ends on
// a question to the owner (an agent_owner_ask left open, or a final line
// marked "Owner question:") and the owner is not working in that session, the
// gateway makes it a chief-bound ask and the session is told once to carry
// on. Claude Code hears it from its Stop hook; Pi and OpenCode at the start of
// their next turn, with the policy.

// turnEndRecheck is how long a question the gateway did not redirect (the
// owner was working in the session) is left alone before it is checked again.
const turnEndRecheck = 5 * time.Minute

// turnEndKeep is how many questions are remembered per session.
const turnEndKeep = 16

// turnEndAsks remembers, per session, the questions already checked at a turn
// end. A redirected question is never redirected again, so a session cannot
// loop on it.
type turnEndAsks struct {
	mu sync.Mutex
	by map[string][]turnEndMark
}

type turnEndMark struct {
	key        string
	at         time.Time
	redirected bool
}

// seen tells whether the question needs no check now.
func (t *turnEndAsks) seen(session, key string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, m := range t.by[session] {
		if m.key == key {
			return m.redirected || now.Sub(m.at) < turnEndRecheck
		}
	}
	return false
}

func (t *turnEndAsks) mark(session, key string, now time.Time, redirected bool) {
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
	list = append(list, turnEndMark{key: key, at: now, redirected: redirected})
	if len(list) > turnEndKeep {
		list = list[len(list)-turnEndKeep:]
	}
	t.by[session] = list
}

// turnEndQuestion checks the question a session's turn ended on. last is the
// turn's final message when the harness gives it. It returns the line to tell
// the session, or "" to let the turn end. Every failure lets the turn end: a
// hook must never trap a session.
func (d *Daemon) turnEndQuestion(ctx context.Context, ls *localSession, sid, last string) string {
	if d.workerBySession(sid) != nil {
		return ""
	}
	question, asked := "", false
	if w := d.infoOf(ls).Waiting; w != nil && w.Explicit && strings.TrimSpace(w.Text) != "" {
		question, asked = w.Text, true
	} else {
		question = protocol.OwnerQuestionLine(last)
	}
	if question == "" {
		return ""
	}
	key := protocol.NormalizeAskText(question)
	now := time.Now()
	if d.turnEnds.seen(sid, key, now) {
		return ""
	}
	raw, err := d.call(ctx, "ask.turn_end", map[string]any{"session_id": sid, "text": question, "asked": asked})
	if err != nil {
		return ""
	}
	var res struct {
		Redirect bool `json:"redirect"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return ""
	}
	d.turnEnds.mark(sid, key, now, res.Redirect)
	if !res.Redirect {
		return ""
	}
	return protocol.OwnerQuestionRedirect
}

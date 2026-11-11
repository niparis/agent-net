package gateway

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const nudgeLookahead = time.Hour

type nudgeCollectionKey struct{}
type nudgeDueKey struct{}
type nudgeSeedKey struct{}
type nudgeCollection struct{ messages []protocol.Message }

// dueNudgeSessions supplies triggers from mechanisms other than step tasks.
// Future candidates alone never trigger a send.
func (s *Store) dueNudgeSessions(stuck time.Duration) (map[string]bool, error) {
	out := map[string]bool{}
	rows, err := s.db.Query(`SELECT DISTINCT q.recipient`+remindableRequest+` AND q.reminded=0 AND dst.online=1`, s.reminderArgs()...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		out[id] = true
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if stuck <= 0 {
		return out, nil
	}
	rows, err = s.db.Query(`SELECT DISTINCT c.caller FROM delivery_step_tasks t JOIN delivery_changes c ON c.id=t.change_id AND c.revision=t.revision
 WHERE `+s.stepNotWaitingAt()+` AND t.completed=0 AND t.claimed_by='' AND (t.step<>'verify' OR t.after_merge=0 OR t.stuck_notified=0)
 AND ((t.after_merge=0 AND c.state='open') OR (t.after_merge=1 AND c.state='merged'))
 AND c.caller<>'' AND t.created_at<=? AND EXISTS(SELECT 1 FROM sessions WHERE id=c.caller)
 AND NOT EXISTS(SELECT 1 FROM messages WHERE sender='agent-net' AND idem_key='delivery:stuck:' || t.change_id || ':' || t.revision || ':' || t.step || ':' || t.for_step || ':' || t.after_merge)`, s.now().Add(-stuck).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (g *Gateway) dispatchNudge(ctx context.Context, m protocol.Message) {
	if batch, ok := ctx.Value(nudgeCollectionKey{}).(*nudgeCollection); ok {
		batch.messages = append(batch.messages, m)
		return
	}
	// Quiet-window flushes enter the same pass with their already stored message.
	g.dispatchStepTasks(context.WithValue(ctx, nudgeSeedKey{}, m))
}

func (g *Gateway) flushNudges(ctx context.Context, batch *nudgeCollection) {
	groups := map[string][]protocol.Message{}
	for _, m := range batch.messages {
		groups[m.To] = append(groups[m.To], m)
	}
	for _, ms := range groups {
		m, err := g.store.combineNudges(ms)
		if err != nil {
			g.logLine("error", "where", "combine nudges", "err", err)
			continue
		}
		if m.ID != "" {
			g.dispatch(ctx, m)
		}
	}
}

// combineNudges uses the existing durable batch links. The original reminder
// flags and task associations remain attached to their own audit messages.
func (s *Store) combineNudges(ms []protocol.Message) (protocol.Message, error) {
	if len(ms) == 1 {
		return ms[0], nil
	}
	sort.SliceStable(ms, func(i, j int) bool {
		return strings.HasPrefix(ms[i].IdempotencyKey, "step_") && !strings.HasPrefix(ms[j].IdempotencyKey, "step_")
	})
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Message{}, err
	}
	defer tx.Rollback()
	m, err := s.insertBusyBatch(tx, ms[0].To, ms, s.now())
	if err != nil {
		return protocol.Message{}, err
	}
	m.IdempotencyKey = "nudge:" + m.ID
	m.Body = nudgeBody(ms)
	if _, err = tx.Exec(`UPDATE messages SET body=?,idem_key=? WHERE id=?`, m.Body, m.IdempotencyKey, m.ID); err != nil {
		return protocol.Message{}, err
	}
	s.event(tx, "msg.stored", "", m.From, m.ID, "by=agent-net nudge batch")
	if err = tx.Commit(); err != nil {
		return protocol.Message{}, err
	}
	s.logMessage(m)
	return m, nil
}

func nudgeBody(ms []protocol.Message) string {
	bodies := make([]string, 0, len(ms))
	for _, m := range ms {
		bodies = append(bodies, m.Body)
	}
	return strings.Join(bodies, "\n\n")
}

// Recheck every original independently when a queued envelope reaches dispatch.
// A completed step must not hide an unrelated, still-open reply reminder.
func (s *Store) currentNudge(m protocol.Message) (protocol.Message, bool, error) {
	if m.From != "agent-net" || !strings.HasPrefix(m.IdempotencyKey, "nudge:") {
		return m, true, nil
	}
	kept := make([]protocol.Message, 0, len(m.Batch))
	for _, part := range m.Batch {
		ok := true
		var err error
		if needsReminderCheck(part) {
			ok, err = s.reminderEligible(part.ParentID)
		} else if strings.HasPrefix(part.IdempotencyKey, "step_") {
			ok, err = s.stepMessageCurrent(part.ID)
		}
		if err == nil && ok {
			// A pause applied after the batch was built still holds each part.
			ok, err = s.allowNudgeDelivery(part)
		}
		if err != nil {
			return m, false, err
		}
		if ok {
			kept = append(kept, part)
		}
	}
	if len(kept) == 0 {
		return m, false, nil
	}
	m.Batch = kept
	m.Body = nudgeBody(kept)
	return m, true, nil
}

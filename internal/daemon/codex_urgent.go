package daemon

import (
	"context"

	"github.com/niparis/agent-net/internal/protocol"
)

// An urgent message to a busy Codex thread (decision 0180). The app server
// takes input into the active turn with turn/steer, so the daemon sends the
// message there as soon as the gateway says it is held.

// setTurn records the active turn of a thread, or clears it.
func (a *codexAdapter) setTurn(thread, turn string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if turn == "" {
		delete(a.turns, thread)
		return
	}
	if a.turns == nil {
		a.turns = map[string]string{}
	}
	a.turns[thread] = turn
}

// activeTurn is the thread's running turn, or "" when none is known. A daemon
// that started during a turn does not know it, and the message then waits for
// the turn end as in decision 0130.
func (a *codexAdapter) activeTurn(thread string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.turns[thread]
}

// DeliverInline steers the active turn with the message. When the turn ended
// in the meantime, or the app server refuses, the message goes into the
// thread's queue as a normal delivery, so it is never lost.
func (a *codexAdapter) DeliverInline(ctx context.Context, target protocol.SessionInfo, m protocol.Message) (string, Result) {
	turn := a.activeTurn(target.NativeID)
	a.mu.Lock()
	rpc := a.rpc
	a.mu.Unlock()
	if turn != "" && rpc != nil {
		text := "[agent-net] An urgent message arrived while you were working. Read it now, before your next step, and act on it. Do not wait for the end of your turn.\n\n" + codexFrame(m)
		_, err := rpc.call(ctx, "turn/steer", map[string]any{
			"threadId":            target.NativeID,
			"expectedTurnId":      turn,
			"clientUserMessageId": m.ID,
			"input":               []map[string]any{{"type": "text", "text": text, "text_elements": []any{}}},
		})
		if err == nil {
			return "", Result{Delivered: true, Detail: "steered the active turn"}
		}
		a.d.Logf("codex: turn/steer for %s failed, queueing it: %v", m.ID, err)
	}
	return "", a.Deliver(ctx, target, m)
}

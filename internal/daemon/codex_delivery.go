package daemon

import (
	"context"
	"encoding/json"

	"github.com/niparis/agent-net/internal/protocol"
)

// A message agent-net queues in a Codex thread carries its own ID as the
// clientId of the user message item that starts its turn. The app server reports
// that item with item/started, so the delivery check needs no read: it watches
// for the notice (decision 0171, part b). The shape is checked against a real
// app server in codex_real_test.go.

// queueAdd puts one message in the thread's queue.
func (a *codexAdapter) queueAdd(ctx context.Context, rpc *codexRPC, thread, msgID, text string) error {
	return codexQueueAdd(ctx, rpc, thread, msgID, text)
}

// codexQueueAdd puts one message in a thread's queue, with its ID as the
// client ID of the user message item that takes it.
func codexQueueAdd(ctx context.Context, rpc *codexRPC, thread, msgID, text string) error {
	_, err := rpc.call(ctx, "thread/queue/add", map[string]any{
		"threadId":            thread,
		"clientUserMessageId": msgID,
		"input":               []map[string]any{{"type": "text", "text": text, "text_elements": []any{}}},
	})
	return err
}

// watchQueuedDelivery starts the check of one queued message of a Codex session.
func (a *codexAdapter) watchQueuedDelivery(target protocol.SessionInfo, m protocol.Message, text string) {
	native, msgID := target.NativeID, m.ID
	a.mu.Lock()
	if a.queued == nil {
		a.queued = map[string]bool{}
	}
	a.queued[msgID] = false
	a.mu.Unlock()
	a.d.confirmDelivery("codex\x00"+native, deliveryWatch{
		label: "session",
		msgID: msgID,
		gone:  func() bool { return !a.isThread(native) },
		busy: func(ctx context.Context) bool {
			a.d.mu.Lock()
			ls := a.d.sessions[key("codex", native)]
			a.d.mu.Unlock()
			return ls != nil && a.d.infoOf(ls).Status == "busy"
		},
		taken: func(ctx context.Context) bool {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.queued[msgID]
		},
		redeliver: func(ctx context.Context) bool {
			a.mu.Lock()
			rpc := a.rpc
			a.mu.Unlock()
			return rpc != nil && a.queueAdd(ctx, rpc, native, msgID, text) == nil
		},
		restart: func(ctx context.Context) { a.restartQueuedDelivery(ctx, native, msgID, text) },
		done: func() {
			a.mu.Lock()
			delete(a.queued, msgID)
			a.mu.Unlock()
		},
	})
}

// noteQueuedTaken records the user message item that carries a queued message's
// ID, so the check of that message sees it. Typed prompts carry no ID and are
// not for the check.
func (a *codexAdapter) noteQueuedTaken(raw json.RawMessage) {
	var p struct {
		Item struct {
			Type     string  `json:"type"`
			ClientID *string `json:"clientId"`
		} `json:"item"`
	}
	if json.Unmarshal(raw, &p) != nil || p.Item.Type != "userMessage" || p.Item.ClientID == nil || *p.Item.ClientID == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, watching := a.queued[*p.Item.ClientID]; watching {
		a.queued[*p.Item.ClientID] = true
	}
}

// restartQueuedDelivery starts a turn in the thread with the message as its
// prompt, since its queued copy was not taken.
func (a *codexAdapter) restartQueuedDelivery(ctx context.Context, native, msgID, text string) {
	a.mu.Lock()
	rpc := a.rpc
	a.mu.Unlock()
	if rpc == nil || !a.isThread(native) {
		a.d.Logf("session.delivery_unconfirmed msg=%s reason=cto_changed", msgID)
		return
	}
	if _, err := rpc.call(ctx, "turn/start", map[string]any{
		"threadId": native,
		"input":    []map[string]any{{"type": "text", "text": text, "text_elements": []any{}}},
	}); err != nil {
		a.d.Logf("session.delivery_unconfirmed msg=%s reason=restart_failed detail=%q", msgID, err.Error())
	}
}

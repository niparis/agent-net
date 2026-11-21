package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Urgent messages inside a turn (decision 0180). The gateway holds a message
// for a busy session. When the message is urgent it also tells this daemon, and
// the Claude Code hook of that session, run at each tool call, collects it and
// shows it to the model before its next step.

// InlineDeliverer is an adapter that can hand a message to the harness inside
// a turn instead of writing it to the inbox. It does everything Deliver does
// except the write, and returns the text the model is shown.
type InlineDeliverer interface {
	DeliverInline(ctx context.Context, target protocol.SessionInfo, m protocol.Message) (string, Result)
}

// urgentTakeBudget bounds one hook call. The hook runs inside a turn and the
// gateway link may be slow, so it gives up and the message goes at turn end.
const urgentTakeBudget = 1500 * time.Millisecond

// onUrgentHeld remembers that the gateway holds an urgent message for a local
// session. It is the only reason a hook call reaches the gateway.
func (d *Daemon) onUrgentHeld(sessionID string) {
	if sessionID != "" {
		d.urgentHeld.Store(sessionID, struct{}{})
	}
}

// inlineCapture receives the text of a message delivered inside a turn.
type inlineCapture struct{ text string }

// urgentTakeCall serves MethodUrgentTake for a command-line client. It is
// silent when the session is unknown or nothing is waiting: a hook must never
// disturb the turn that runs it.
func (lc *localConn) urgentTakeCall(ctx context.Context, client string, raw json.RawMessage) (any, error) {
	if client != "cli" {
		return nil, errors.New("this method is for a harness hook")
	}
	var p protocol.UrgentTakeRequest
	if json.Unmarshal(raw, &p) != nil || p.NativeID == "" {
		return protocol.UrgentTakeResult{}, nil
	}
	d := lc.d
	d.mu.Lock()
	ls := d.sessions[key(p.Harness, p.NativeID)]
	d.mu.Unlock()
	if ls == nil || ls.sessionID == "" {
		return protocol.UrgentTakeResult{}, nil
	}
	sid := ls.sessionID
	if _, ok := d.urgentHeld.LoadAndDelete(sid); !ok {
		return protocol.UrgentTakeResult{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, urgentTakeBudget)
	defer cancel()
	res, err := d.call(ctx, protocol.OpUrgentTake, protocol.UrgentTake{SessionID: sid})
	if err != nil {
		// Ask again at the next tool call.
		d.urgentHeld.Store(sid, struct{}{})
		d.Logf("urgent take for %s: %v", sid, err)
		return protocol.UrgentTakeResult{}, nil
	}
	var taken protocol.UrgentTaken
	if json.Unmarshal(res, &taken) != nil {
		return protocol.UrgentTakeResult{}, nil
	}
	var texts []string
	for _, m := range taken.Messages {
		capture := &inlineCapture{}
		d.inlineCaptures.Store(m.ID, capture)
		// The normal delivery path records the message and reports it delivered
		// to the gateway; only the last step, the write, is replaced.
		d.onDeliver(context.WithoutCancel(ctx), m)
		d.inlineCaptures.Delete(m.ID)
		if capture.text != "" {
			texts = append(texts, capture.text)
		}
	}
	return protocol.UrgentTakeResult{Text: strings.Join(texts, "\n\n")}, nil
}

// deliverInline is deliverTo for a message a hook is collecting. It reports
// false when no hook is collecting this message or the adapter cannot show a
// message inside a turn, and the caller delivers normally.
func (d *Daemon) deliverInline(ctx context.Context, a Adapter, info protocol.SessionInfo, m protocol.Message) (Result, bool) {
	v, ok := d.inlineCaptures.Load(m.ID)
	if !ok {
		return Result{}, false
	}
	il, ok := a.(InlineDeliverer)
	if !ok {
		return Result{}, false
	}
	text, res := il.DeliverInline(ctx, info, m)
	v.(*inlineCapture).text = text
	return res, true
}

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
	if sessionID == "" {
		return
	}
	d.urgentHeld.Store(sessionID, struct{}{})
	// Codex has no hook at a tool call, but its app server takes input into the
	// active turn (turn/steer). The daemon pushes the message at once.
	ls := d.sessionByID(sessionID)
	if ls == nil || d.infoOf(ls).Harness != "codex" {
		return
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		if a, ok := d.adapters["codex"].(*codexAdapter); ok && a.activeTurn(d.infoOf(ls).NativeID) != "" {
			d.takeUrgentAndDeliver(context.Background(), sessionID)
		}
	}()
}

// inlineCapture receives a message delivered inside a turn: the text an
// adapter built (Claude Code), or the message itself for a plugin that formats
// it (Pi, OpenCode).
type inlineCapture struct {
	text string
	m    *protocol.Message
}

// urgentTakeCall serves MethodUrgentTake. A command-line client (the Claude
// Code hook) names the session. A plugin (Pi, OpenCode) is bound to its own.
// It is silent when the session is unknown or nothing is waiting: a hook must
// never disturb the turn that runs it.
func (lc *localConn) urgentTakeCall(ctx context.Context, client string, ls *localSession, raw json.RawMessage) (any, error) {
	d := lc.d
	if client == "cli" {
		var p protocol.UrgentTakeRequest
		if json.Unmarshal(raw, &p) != nil || p.NativeID == "" {
			return protocol.UrgentTakeResult{}, nil
		}
		d.mu.Lock()
		ls = d.sessions[key(p.Harness, p.NativeID)]
		d.mu.Unlock()
	} else if ls == nil {
		return nil, errors.New("this method needs a bound session")
	}
	if ls == nil || ls.sessionID == "" {
		return protocol.UrgentTakeResult{}, nil
	}
	texts, msgs := d.takeUrgentAndDeliver(ctx, ls.sessionID)
	return protocol.UrgentTakeResult{Text: strings.Join(texts, "\n\n"), Messages: msgs}, nil
}

// takeUrgentAndDeliver fetches the urgent messages held for a session and runs
// each through the normal delivery path with its last step replaced: the
// adapter shows it inside the turn (DeliverInline), or a plugin gets the
// message in the answer. It returns what to show, and nothing when no urgent
// message was waiting.
func (d *Daemon) takeUrgentAndDeliver(ctx context.Context, sid string) (texts []string, msgs []protocol.Message) {
	if _, ok := d.urgentHeld.LoadAndDelete(sid); !ok {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, urgentTakeBudget)
	defer cancel()
	res, err := d.call(ctx, protocol.OpUrgentTake, protocol.UrgentTake{SessionID: sid})
	if err != nil {
		// Ask again at the next tool call.
		d.urgentHeld.Store(sid, struct{}{})
		d.Logf("urgent take for %s: %v", sid, err)
		return nil, nil
	}
	var taken protocol.UrgentTaken
	if json.Unmarshal(res, &taken) != nil {
		return nil, nil
	}
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
		if capture.m != nil {
			msgs = append(msgs, *capture.m)
		}
	}
	return texts, msgs
}

// capturePluginInline is deliverTo for a plugin session whose message a tool
// call is collecting: the plugin gets the message in the answer and shows it
// itself, so nothing is pushed. It is not a turn start, so the OpenCode
// delivery check does not apply.
func (d *Daemon) capturePluginInline(m protocol.Message) bool {
	v, ok := d.inlineCaptures.Load(m.ID)
	if !ok {
		return false
	}
	v.(*inlineCapture).m = &m
	return true
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

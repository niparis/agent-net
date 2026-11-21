package gateway

import (
	"context"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// closedOpenCode adds an OpenCode session that the owner closed: offline with
// status 'ended', the state the dotfiles session had.
func (p *wakeProbe) closedOpenCode(t *testing.T) string {
	t.Helper()
	id := p.offline(t, "opencode", "", protocol.AutonomyOff)
	if err := p.s.SetSessionClosed(p.node, id, true); err != nil {
		t.Fatal(err)
	}
	return id
}

// A request to an ended OpenCode session with wake on asks its machine to wake it.
func TestRequestToEndedOpenCodeSessionWakesIt(t *testing.T) {
	p := newWakeProbe(t)
	id := p.closedOpenCode(t)
	m, _, err := p.s.StoreMessage(protocol.MsgSend{Sender: p.from, Recipient: id, Type: protocol.TypeRequest, Body: "do it", IdempotencyKey: "req-ended"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.g.dispatch(context.Background(), m) {
		t.Fatal("dispatch did not start a wake")
	}
	p.expectWake(t, id)
}

package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/protocol"
)

type signInSource struct {
	inboxOpsSource
	plan protocol.EnterPlan
}

func (s *signInSource) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	if method == protocol.OpSignInOpen {
		s.calls = append(s.calls, inboxOpsCall{method: method, params: params})
		return json.Marshal(s.plan)
	}
	return s.inboxOpsSource.Call(context.Background(), method, params)
}

// The Desk row of a sign-in ask has one action, Open sign-in. It never shows a
// command to type, and the click asks the daemon to open the pane.
func TestDeskSignInRowOpensPaneFromOneAction(t *testing.T) {
	src := &signInSource{
		inboxOpsSource: inboxOpsSource{fakeSource: fakeSource{snap: fakeSnapshot()}},
		plan:           protocol.EnterPlan{OpenedHerdr: true, HerdrPane: "w1:p7", Note: "opened sign in: Alibaba Bailian CLI in herdr"},
	}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 36})
	m.tab = inboxTab
	m.asks = []protocol.Ask{{
		ID: "i_signin", Kind: protocol.AskKindSignIn, State: protocol.AskStateOpen,
		FromKind: protocol.AskFromGateway, From: "agent-net", Node: "n_mac",
		Question: "Sign in to Alibaba Bailian CLI on Nicolas-MBP",
		Detail:   "the Alibaba token-plan quota needs your console login",
		Next:     protocol.AskNextOwner, CreatedAt: t0,
	}}
	rows := m.inboxRows()
	if len(rows) != 1 || rows[0].kind != "signin" {
		t.Fatalf("sign-in rows: %+v", rows)
	}
	hints := m.inboxItemHints(rows[0])
	var found bool
	for _, h := range hints {
		if h.key == "i" && h.label == "Open sign-in" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Open sign-in hint missing: %+v", hints)
	}
	if lines := strings.Join(m.inboxDetailLines(rows[0]), "\n"); strings.Contains(lines, "bl auth") {
		t.Fatalf("the detail shows a command to type: %q", lines)
	}
	msg, ok := m.inboxSignIn(rows[0])().(inboxSignInMsg)
	if !ok || msg.err != nil || !msg.plan.OpenedHerdr {
		t.Fatalf("sign-in answer: %+v", msg)
	}
	call := src.method(protocol.OpSignInOpen)
	if call == nil {
		t.Fatal("sign_in_open was not called")
	}
	req, ok := call.params.(protocol.SignInOpenRequest)
	if !ok || req.AskID != "i_signin" {
		t.Fatalf("sign-in request: %+v", call.params)
	}
}

package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

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
	if lines := strings.Join(m.inboxDetailLines(rows[0]), "\n"); strings.Contains(lines, "bl auth") || !strings.Contains(lines, "Firefox") || !strings.Contains(lines, "Safari") {
		t.Fatalf("the detail must tell the owner to use Firefox, not Safari: %q", lines)
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

func TestDeskSignInCompletionCallsDaemon(t *testing.T) {
	src := &signInSource{inboxOpsSource: inboxOpsSource{fakeSource: fakeSource{snap: fakeSnapshot()}}}
	m := New(src)
	m.fetching = true
	_, cmd := m.onEnterDone(enterDoneMsg{name: "sign in: Firefox, not Safari", signInAskID: "i_signin"})
	if cmd == nil {
		t.Fatal("no completion call")
	}
	msg, ok := cmd().(signInCompleteMsg)
	if !ok || msg.err != nil {
		t.Fatalf("completion: %+v", msg)
	}
	call := src.method(protocol.OpSignInComplete)
	if call == nil {
		t.Fatal("sign-in completion was not sent")
	}
	if q := call.params.(protocol.SignInOpenRequest); q.AskID != "i_signin" || q.Cancel {
		t.Fatalf("completion request %+v", q)
	}
}

func TestDeskSignInProofScenario(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		src := &signInSource{inboxOpsSource: inboxOpsSource{fakeSource: fakeSource{snap: fakeSnapshot()}}, plan: protocol.EnterPlan{Argv: []string{"bl", "auth", "login", "--console", "--no-browser", "--console-site", "international"}, SignInAskID: "i_signin"}}
		m := New(src)
		m.loc, m.now = time.UTC, func() time.Time { return t0 }
		m = send(m, tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m.tab, m.inboxDetail = inboxTab, true
		m.asks = []protocol.Ask{{ID: "i_signin", Kind: protocol.AskKindSignIn, State: protocol.AskStateOpen, FromKind: protocol.AskFromGateway, From: "agent-net", Node: "n_test", Question: "Sign in to Alibaba Bailian CLI on work-box", Detail: "the Alibaba token-plan quota needs your console login", Next: protocol.AskNextOwner, CreatedAt: t0}}
		frame := ansi.Strip(m.testView())
		if !strings.Contains(frame, "Firefox") || !strings.Contains(frame, "Safari") {
			t.Fatalf("%dx%d detail: %s", size.w, size.h, frame)
		}
		writeHerdrProof(t, size.w, size.h, "signin-open", "open the sign-in ask in Desk", "the ask says to open the link in Firefox, not Safari", frame)

	}
}

func TestDeskSignInHandoverNamesFirefoxInTitle(t *testing.T) {
	m := New(&signInSource{inboxOpsSource: inboxOpsSource{fakeSource: fakeSource{snap: fakeSnapshot()}}})
	got, _, _, _ := captureName(&m, "sign in: Firefox, not Safari")
	plan := protocol.EnterPlan{Argv: []string{"bl", "auth", "login", "--console", "--no-browser", "--console-site", "international"}, SignInAskID: "i_signin"}
	_, cmd := m.onEnter(enterMsg{name: "sign in: Firefox, not Safari", plan: plan})
	if cmd == nil || strings.Join(*got, " ") != strings.Join(plan.Argv, " ") {
		t.Fatalf("sign-in handover %v", *got)
	}
}

func TestDeskSignInRequestFromWebOpensLocalLogin(t *testing.T) {
	src := &signInSource{inboxOpsSource: inboxOpsSource{fakeSource: fakeSource{snap: fakeSnapshot()}}, plan: protocol.EnterPlan{Argv: []string{"bl"}, SignInAskID: "i_signin"}}
	m := New(src)
	_, cmd := m.applyCTOUI(protocol.UIRequest{SignInAskID: "i_signin"})
	if cmd == nil {
		t.Fatal("no local sign-in request")
	}
	msg, ok := cmd().(inboxSignInMsg)
	if !ok || msg.err != nil || msg.plan.SignInAskID != "i_signin" {
		t.Fatalf("sign-in result %+v", msg)
	}
	if call := src.method(protocol.OpSignInOpen); call == nil || call.params.(protocol.SignInOpenRequest).AskID != "i_signin" {
		t.Fatal("did not ask local daemon")
	}
}

// The research Alibaba sign-in ask (open, waits for the owner, 5 days old)
// must be on the first screen of the Desk, with the cursor on it, even when
// newer questions, approvals and notices wait too. Its kind used to have no
// place in the Needs you order, so it came last, behind "... N more" in the
// box, and the owner never saw it.
func TestDeskShowsSignInAskFirstAmongNewerAsks(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := deskChiefModel(t, size[0], size[1])
		m.asks = append(m.asks, protocol.Ask{
			ID: "i_58e1174b8496860a", Kind: protocol.AskKindSignIn, State: protocol.AskStateOpen,
			Next: protocol.AskNextOwner, Owner: protocol.DeskOwnerChief, DeskState: protocol.DeskReady,
			Category: protocol.CategorySecurity, WhyYou: "Only you can complete this login.",
			Ref: "sign_in:n_research:alibaba", Question: "Sign in to Alibaba Bailian CLI on research",
			Detail: "the Alibaba token-plan quota needs your console login", CreatedAt: t0.Add(-5 * 24 * time.Hour),
		})
		for i, kind := range []string{protocol.AskKindQuestion, protocol.AskKindApproval, protocol.AskKindStall, protocol.AskKindMachine} {
			m.asks = append(m.asks, protocol.Ask{
				ID: fmt.Sprintf("i_new%d", i), Kind: kind, State: protocol.AskStateOpen, Next: protocol.AskNextOwner,
				FromKind: protocol.AskFromSession, FromSession: "s_0000000000000004", From: "strategies",
				Question: fmt.Sprintf("Newer ask number %d", i), CreatedAt: t0.Add(-time.Duration(i+1) * time.Minute),
			})
		}
		frame := m.testView()
		view := ansi.Strip(frame)
		t.Logf("SIZE %dx%d:\n%s", size[0], size[1], view)
		for _, want := range []string{"Sign in to Alibaba Bailian CLI on research", "i Open sign-in", "[sign-in]"} {
			if !strings.Contains(view, want) {
				t.Errorf("%dx%d: missing %q on the Desk", size[0], size[1], want)
			}
		}
		flightCount := len(m.deliveryVisible())
		shown := 0
		for _, change := range m.deliveryVisible() {
			if title := deliveryTitle(change); strings.Contains(view, title) {
				shown++
			}
		}
		var overflowFound bool
		inFlight := false
		for _, line := range strings.Split(view, "\n") {
			if strings.Contains(line, "In flight") {
				inFlight = true
				if !strings.Contains(line, fmt.Sprintf("In flight · %d", flightCount)) {
					t.Errorf("%dx%d: wrong flight header: %s", size[0], size[1], line)
				}
			}
			if inFlight {
				if strings.Contains(line, "╰") {
					inFlight = false
					continue
				}
				if at := strings.Index(line, "… "); at >= 0 && strings.Contains(line, "more") {
					var overflow int
					if _, err := fmt.Sscanf(line[at:], "… %d more", &overflow); err == nil {
						overflowFound = true
						if overflow != flightCount-shown {
							t.Errorf("%dx%d: overflow says %d more, but %d of %d changes are shown", size[0], size[1], overflow, shown, flightCount)
						}
					}
				}
			}
		}
		if !overflowFound {
			t.Errorf("%dx%d: expected an In flight overflow row", size[0], size[1])
		}
		step := "desk-open-80"
		action := "open Desk in an 80x24 terminal"
		if size[0] == 120 {
			step = "desk-open-120"
			action = "open Desk in a 120x34 terminal"
		}
		writeHerdrProof(t, size[0], size[1], step, action, "sign-in has a [sign-in] tag and the In flight overflow matches its header count", view)
		golden(t, fmt.Sprintf("desk_signin_%dx%d", size[0], size[1]), frame)
	}
}

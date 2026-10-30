package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

func keyT() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")} }

func TestTrustTurnsOnAfterAskingAndOffAtOnce(t *testing.T) {
	m, src := workerModel(120, 30)
	const target = "s_0000000000000001"
	m = selectID(m, target)
	m, cmd := press(m, keyT())
	if m.mode != modeTrust || cmd != nil || len(src.trusts) != 0 {
		t.Fatalf("t must ask first: mode %v trusts %v", m.mode, src.trusts)
	}
	if h := lines(m.View()); !strings.Contains(strings.Join(h, "\n"), "act on requests from your other agents without asking? y/n") {
		t.Errorf("question not shown:\n%s", strings.Join(h, "\n"))
	}
	// n and esc leave it alone; other keys are ignored.
	for _, k := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("n")}, {Type: tea.KeyEsc}} {
		mm, cmd := press(m, k)
		if mm.mode != modeNormal || cmd != nil || len(src.trusts) != 0 || mm.notice.text != "Peer trust unchanged." {
			t.Errorf("%v: %v %v %q", k, mm.mode, src.trusts, mm.notice.text)
		}
	}
	if mm, _ := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); mm.mode != modeTrust || mm.quitting {
		t.Error("q must not quit while asking")
	}
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = finish(t, m, cmd)
	if len(src.trusts) != 1 || src.trusts[0] != (trustCall{target, true}) || m.notice.err {
		t.Fatalf("turn on: %v %+v", src.trusts, m.notice)
	}

	// The next snapshot says it is on: the column and the detail pane show it,
	// and t turns it off without a question.
	for i := range src.snap.Agents {
		if src.snap.Agents[i].SessionID == target {
			src.snap.Agents[i].PeerTrust = true
			src.snap.Agents[i].Profile.Expertise = "billing API and its deploys"
		}
	}
	m = refresh(m, src)
	screen := strings.Join(lines(m.View()), "\n")
	for _, want := range []string{"TRUST", "peer", "expert ", "billing API and its deploys", "acts on peer requests without asking"} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen)
		}
	}
	m, cmd = press(m, keyT())
	if m.mode != modeNormal {
		t.Fatal("turning off must not ask")
	}
	m = finish(t, m, cmd)
	if len(src.trusts) != 2 || src.trusts[1] != (trustCall{target, false}) {
		t.Fatalf("turn off: %v", src.trusts)
	}
}

func TestTrustErrorsAndTheOwnerRow(t *testing.T) {
	m, src := workerModel(120, 30)
	src.trustErr = errors.New("method \"session_trust\" is not available to cli clients")
	m = selectID(m, "s_0000000000000001")
	m, _ = press(m, keyT())
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = finish(t, m, cmd)
	if !m.notice.err || !strings.HasPrefix(m.notice.text, "Peer trust not changed:") {
		t.Errorf("error line: %+v", m.notice)
	}
	// Nothing selected: a message, no question.
	empty := &fakeSource{snap: Snapshot{Connected: true}}
	e := New(empty)
	e = send(e, tea.WindowSizeMsg{Width: 100, Height: 24})
	e, _ = press(e, keyT())
	if e.mode != modeNormal || !e.notice.err {
		t.Errorf("empty selection: %v %+v", e.mode, e.notice)
	}
}

func TestTrustKeyIsInHelpAndFooter(t *testing.T) {
	m, _ := workerModel(160, 40)
	foot := lines(m.View())
	if !strings.Contains(foot[len(foot)-1], "t trust") {
		t.Errorf("footer: %q", foot[len(foot)-1])
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !strings.Contains(strings.Join(lines(m.View()), "\n"), "peer trust on/off") {
		t.Error("help lacks the t key")
	}
	_ = protocol.OpSessionTrust
}

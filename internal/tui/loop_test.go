package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// loopGraph gives the tenebrae node a short charter and a standing loop, so
// the loop block is in view on a small card.
func loopGraph(loop *protocol.LoopView) protocol.GraphView {
	g := fakeGraph()
	for i := range g.Nodes {
		if g.Nodes[i].Name == "tenebrae" {
			g.Nodes[i].Charter = protocol.Charter{Purpose: "Research library for the trading desk.", Maturity: "mature"}
			g.Nodes[i].Loop = loop
		}
	}
	return g
}

func sampleLoop() *protocol.LoopView {
	return &protocol.LoopView{Every: "4h", LastTick: t0.Add(-5 * time.Minute).Unix(), NextTick: t0.Add(3*time.Hour + 55*time.Minute).Unix(),
		TicksToday: 3, ProgressToday: 2, LastResult: "parser: wrote the lexer tests, parser next"}
}

func TestMapCardShowsTheLoopBlock(t *testing.T) {
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		m := openMap(t, mapModel(size[0], size[1], loopGraph(sampleLoop())))
		m.mp.selected = "tenebrae"
		view := m.testView()
		golden(t, "loop_card_"+itoa(size[0])+"x"+itoa(size[1]), view)
		plain := ansi.Strip(view)
		for _, want := range []string{"loop every 4h", "last 14:00", "next 18:00", "today 3 ticks, 2 made progress", "last result: parser: wrote the lexer tests"} {
			if !strings.Contains(strings.Join(strings.Fields(plain), " "), want) {
				t.Errorf("%dx%d: missing %q:\n%s", size[0], size[1], want, plain)
			}
		}
		if strings.Contains(plain, "tokens") {
			t.Errorf("tokens shown while unknown:\n%s", plain)
		}
	}
}

func TestMapCardLoopBlockShowsTokensPausedAndNone(t *testing.T) {
	l := sampleLoop()
	l.Tokens = 45000
	m := openMap(t, mapModel(120, 34, loopGraph(l)))
	m.mp.selected = "tenebrae"
	if v := ansi.Strip(m.testView()); !strings.Contains(v, "~45k tokens today") {
		t.Fatal(v)
	}
	l.Paused = true
	m = openMap(t, mapModel(120, 34, loopGraph(l)))
	m.mp.selected = "tenebrae"
	if v := ansi.Strip(m.testView()); !strings.Contains(v, "paused") || strings.Contains(v, "next 18:00") {
		t.Fatal(v)
	}
	l.Paused, l.Mode = false, protocol.LoopModeOnChange
	m = openMap(t, mapModel(120, 34, loopGraph(l)))
	m.mp.selected = "tenebrae"
	if v := ansi.Strip(m.testView()); !strings.Contains(v, "loop every 4h on change") {
		t.Fatal(v)
	}
	m = openMap(t, mapModel(120, 34, loopGraph(nil)))
	m.mp.selected = "tenebrae"
	if v := ansi.Strip(m.testView()); strings.Contains(v, "loop every") {
		t.Fatal(v)
	}
}

func TestMapCardLoopKeyCyclesTheInterval(t *testing.T) {
	m := openMap(t, mapModel(120, 34, loopGraph(nil)))
	m.mp.selected = "tenebrae"
	updated, cmd := m.update(tea.KeyPressMsg{Text: "L", Code: []rune("L")[0]})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("L did not start a loop change")
	}
	m = send(m, cmd())
	src := m.src.(*fakeSource)
	if len(src.loopCalls) != 1 || src.loopCalls[0].node != "tenebrae" || src.loopCalls[0].value != "1h" {
		t.Fatalf("loop calls = %+v", src.loopCalls)
	}
	if !strings.Contains(m.notice.text, "Loop 1h") {
		t.Fatalf("notice = %q", m.notice.text)
	}
	for current, want := range map[string]string{"": "1h", "1h": "4h", "4h": "12h", "12h": "24h", "24h": "", "90m": ""} {
		if got := nextLoopStep(current); got != want {
			t.Errorf("after %q = %q, want %q", current, got, want)
		}
	}
}

func TestLoopKeyIsInTheCardHelp(t *testing.T) {
	m := openMap(t, mapModel(120, 34, loopGraph(nil)))
	m.mp.selected = "tenebrae"
	m.openHelp()
	if v := ansi.Strip(m.testView()); !strings.Contains(v, "standing loop") {
		t.Fatal(v)
	}
}

func TestSessionsMarkTheAgentWithALoop(t *testing.T) {
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		m := quietFixtures(newModel(size[0], size[1]))
		agents := append([]protocol.AgentView(nil), m.snap.Agents...)
		for i := range agents {
			if agents[i].SessionID == "s_0000000000000001" {
				agents[i].Loop = sampleLoop()
			}
		}
		m.snap.Agents = agents
		view := m.testView()
		golden(t, "loop_sessions_"+itoa(size[0])+"x"+itoa(size[1]), view)
		plain := ansi.Strip(view)
		if got := strings.Count(plain, "↻"); got != 1 {
			t.Errorf("%dx%d: %d loop marks, want 1:\n%s", size[0], size[1], got, plain)
		}
		for _, l := range strings.Split(plain, "\n") {
			if strings.Contains(l, "↻") && !strings.Contains(l, "tenebrae") {
				t.Errorf("the mark is on the wrong row: %q", l)
			}
		}
	}
}

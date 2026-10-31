package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// The key line is a short list of the keys used most on the screen. The full
// list lives in the help overlay; no line may grow past seven hints.
func TestKeyLineHasAtMostSevenHintsOnEveryTab(t *testing.T) {
	for _, tc := range []struct{ name, key string }{
		{"Sessions", "1"}, {"Messages", "2"}, {"Quotas", "3"},
		{"Map", "4"}, {"Tools", "5"}, {"Graph", "6"},
	} {
		m := keys(newModel(120, 34), tc.key)
		line := ansi.Strip(m.helpLine(120))
		hints := strings.Split(line, "  ")
		if len(hints) > 7 {
			t.Errorf("%s: %d hints: %q", tc.name, len(hints), line)
		}
		if !strings.Contains(line, "? help") {
			t.Errorf("%s: key line has no ? help: %q", tc.name, line)
		}
		if !strings.Contains(line, "q quit") {
			t.Errorf("%s: key line has no q quit: %q", tc.name, line)
		}
		if strings.Contains(line, "…") {
			t.Errorf("%s: key line cuts a word: %q", tc.name, line)
		}
	}
}

func TestKeyLineContentPerTab(t *testing.T) {
	for _, tc := range []struct{ name, key, want string }{
		{"Sessions", "1", "up/down select  enter open  m msg  / filter  tab CTO  ? help  q quit"},
		{"Messages", "2", "up/down thread  left/right msg  enter latest  v body  tab CTO  ? help  q quit"},
		{"Quotas", "3", "up/down select  p protect  s show unset  r refresh  tab CTO  ? help  q quit"},
		{"Map", "4", "up/down move  left/right follow  enter open  / find  z overview  ? help  q quit"},
		{"Tools", "5", "j/k move  enter fix  x accept  [ ] screen  ? help  q quit"},
		{"Graph", "6", "1-6 tabs  tab CTO  ? help  q quit"},
	} {
		m := keys(newModel(120, 34), tc.key)
		if got := ansi.Strip(m.helpLine(120)); got != tc.want {
			t.Errorf("%s key line:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// Every key that left a key line is still listed in the help overlay, so
// nothing becomes hidden from the owner.
func TestKeysRemovedFromKeyLineStayInHelp(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		removed   []string
	}{
		{"Sessions", "1", []string{"1-6", "s", "x", "h", "H", "t", "p", "N", "L", "o", "a", "d", "r"}},
		{"Messages", "2", []string{"w", "e", "l"}},
		{"Quotas", "3", []string{"1-6"}},
		{"Map", "4", []string{"D", "[ ]", "U", "u", "h", "H", "c"}},
		{"Tools", "5", []string{".", "r", "tab"}},
	} {
		m := keys(newModel(120, 34), tc.key)
		help := ansi.Strip(strings.Join(m.helpContent(120), "\n"))
		for _, key := range tc.removed {
			if !strings.Contains(help, key) {
				t.Errorf("%s help does not list %q:\n%s", tc.name, key, help)
			}
		}
	}
}

// A selected row may replace a less relevant hint with the action that
// matters right then, and the line still keeps seven hints at most.
func TestSessionKeyLineShowsTheRelevantRowAction(t *testing.T) {
	worker := newModel(120, 34)
	worker.snap.Agents = append(worker.snap.Agents, protocol.AgentView{
		SessionID: "s_worker", NodeID: "n_mac", Online: true,
		Worker: &protocol.WorkerInfo{ID: "w_0001"},
	})
	worker.selected = "s_worker"
	if line := ansi.Strip(worker.helpLine(120)); !strings.Contains(line, "x stop") {
		t.Errorf("worker row: %q", line)
	}

	waiting := newModel(120, 34)
	waiting.snap.Agents[0].Waiting = &protocol.Waiting{Kind: protocol.WaitingInput, Text: "Which branch?"}
	waiting.selected = waiting.snap.Agents[0].SessionID
	if line := ansi.Strip(waiting.helpLine(120)); !strings.Contains(line, "enter answer") {
		t.Errorf("waiting row: %q", line)
	}

	held := newModel(120, 34)
	held.snap.Held = []protocol.Message{{ID: "m_held"}}
	held.selected = "m_held"
	line := ansi.Strip(held.helpLine(120))
	for _, want := range []string{"a approve", "r refuse"} {
		if !strings.Contains(line, want) {
			t.Errorf("held row: %q missing %q", line, want)
		}
	}
	if len(strings.Split(line, "  ")) > 7 {
		t.Errorf("held row: too many hints: %q", line)
	}

	learned := newModel(120, 34)
	learned.selected = "learned:1"
	line = ansi.Strip(learned.helpLine(120))
	for _, want := range []string{"a accept", "r reject"} {
		if !strings.Contains(line, want) {
			t.Errorf("learned row: %q missing %q", line, want)
		}
	}
}

func TestGraphKeyLineIsShortAndHelpKeepsTheRest(t *testing.T) {
	line := ansi.Strip(graphHintLine(120, false))
	if hints := strings.Split(strings.TrimSpace(line), "  "); len(hints) > 7 {
		t.Errorf("graph key line: %d hints: %q", len(hints), line)
	}
	if !strings.Contains(line, "? help") {
		t.Errorf("graph key line has no ? help: %q", line)
	}

	m := graphModel(120, 34)
	m.graphHelp = true
	help := ansi.Strip(strings.Join(m.graphView(120, 20), " "))
	for _, want := range []string{"hjkl", "shift+arrows", "[ ]", "space", "o orbit", "q quit"} {
		if !strings.Contains(help, want) {
			t.Errorf("graph help does not list %q: %s", want, help)
		}
	}
}

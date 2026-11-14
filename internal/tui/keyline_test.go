package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// The key line is a short list of the keys used most on the screen. The full
// list lives in the help overlay; Sessions keeps eight hints with tree controls; other tabs keep seven.
func TestKeyLineHasBoundedHintsOnEveryTab(t *testing.T) {
	for _, tc := range []struct{ name, key string }{
		{"Sessions", "1"}, {"Messages", "4"}, {"Quotas", "3"},
		{"Graph", "2"},
	} {
		m := keys(newModel(120, 34), tc.key)
		line := ansi.Strip(m.helpLine(120))
		hints := strings.Split(line, "  ")
		limit := 7
		if tc.key == "1" {
			limit = 8
		}
		if len(hints) > limit {
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
		{"Sessions", "1", "up/down select  enter open (ctrl+] back)  m msg  x stop or remove  G groups  p pin  ? help  q quit"},
		{"Messages", "4", "up/down thread  left/right msg  enter body  end latest  c CTO  ? help  q quit"},
		{"Quotas", "3", "up/down select  p protect  s show unset  c CTO  ? help  q quit"},
		{"Graph", "2", "1-7 tabs  c CTO  ? help  q quit"},
	} {
		m := keys(newModel(120, 34), tc.key)
		if got := ansi.Strip(m.helpLine(120)); got != tc.want {
			t.Errorf("%s key line:\n got %q\nwant %q", tc.name, got, tc.want)
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
	held.tab = inboxTab
	held.asks = append(held.asks, askFromHeld(protocol.Message{ID: "m_held"}))
	line := ansi.Strip(held.helpLine(120))
	for _, want := range []string{"a send it to the other agent", "r keep it held"} {
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
	for _, want := range []string{"a accept it", "r reject it"} {
		if !strings.Contains(line, want) {
			t.Errorf("learned row: %q missing %q", line, want)
		}
	}
}

func TestGraphKeyLineIsShortAndHelpKeepsTheRest(t *testing.T) {
	line := ansi.Strip(graphHintLine("all", 120))
	if hints := strings.Split(strings.TrimSpace(line), "  "); len(hints) > 7 {
		t.Errorf("graph key line: %d hints: %q", len(hints), line)
	}
	if !strings.Contains(line, "? help") {
		t.Errorf("graph key line has no ? help: %q", line)
	}

	m := graphModel(120, 34)
	m = keys(m, "?")
	help := ansi.Strip(strings.Join(m.helpContent(120), " "))
	for _, want := range []string{"shift+arrows", ", .", "open the selected agent card", "quit"} {
		if !strings.Contains(help, want) {
			t.Errorf("graph help does not list %q: %s", want, help)
		}
	}
}

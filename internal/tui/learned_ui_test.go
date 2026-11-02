package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

type learnedSource struct {
	fakeSource
	method string
	params any
}

func (s *learnedSource) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	s.method, s.params = method, params
	return json.RawMessage(`{}`), nil
}
func TestLearnedMapShowsEvidenceAndUndo(t *testing.T) {
	m := openMap(t, mapModel(120, 40, fakeGraph()))
	m.snap.Learned = []protocol.LearnedSuggestion{{ID: 11, Node: "cogforge", Field: "needs", Phrase: "ops backups", NewValue: "ops backups", Status: "applied", By: "learned", HistoryID: 42, Unseen: true, DecidedAt: t0.UnixNano(), Evidence: []protocol.LearnedEvidence{{ID: "m1", Excerpt: "ops owns backups"}}}}
	m.mp.selected = "cogforge"
	view := m.View()
	for _, want := range []string{"● 1 learned", "needs", "ops backups", "ops owns backups", "undo"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q: %s", want, view)
		}
	}
	src := &learnedSource{}
	m.src = src
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	if cmd == nil {
		t.Fatal("missing undo command")
	}
	cmd()
	if src.method != "graph_undo" {
		t.Fatalf("called %s", src.method)
	}
	b, _ := json.Marshal(src.params)
	if !strings.Contains(string(b), "42") {
		t.Fatalf("undo %s", b)
	}
}
func TestLearnedMajorNeedsActions(t *testing.T) {
	for _, key := range []string{"a", "r"} {
		t.Run(key, func(t *testing.T) {
			src := &learnedSource{fakeSource: fakeSource{snap: fakeSnapshot()}}
			m := New(src)
			m.snap = src.snap
			m.snap.Learned = []protocol.LearnedSuggestion{{ID: 7, Node: "ops", Field: "refuses", Phrase: "deploys", OldValue: "", NewValue: "deploys", Status: "pending", Severity: "major"}}
			m.tab = inboxTab
			if view := m.View(); !strings.Contains(view, "ops") || !strings.Contains(view, "deploys") {
				t.Fatalf("needs missing: %s", view)
			}
			_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
			if cmd == nil {
				t.Fatal("missing action")
			}
			cmd()
			if src.method != "learned_answer" {
				t.Fatalf("called %q", src.method)
			}
			b, _ := json.Marshal(src.params)
			want := "accept"
			if key == "r" {
				want = "reject"
			}
			if !strings.Contains(string(b), want) {
				t.Fatalf("answer %s", b)
			}
		})
	}
}

func TestLearnedMapMarksViewedOnLeavingProject(t *testing.T) {
	src := &learnedSource{fakeSource: fakeSource{snap: fakeSnapshot(), graph: fakeGraph()}}
	m := New(src)
	m.width, m.height = 120, 40
	m.mapOn = true
	m.tab = graphTab
	m.mp.loaded = true
	m.mp.nodes = fakeGraph().Nodes
	m.mp.selected = "cogforge"
	m.snap.Learned = []protocol.LearnedSuggestion{{ID: 11, Node: "cogforge", Field: "needs", Phrase: "ops backups", Status: "applied", By: "learned", HistoryID: 42, Unseen: true}}
	_, cmd := m.update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("missing viewed command")
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				c()
			}
		}
	}
	if src.method != "learned_view" {
		t.Fatalf("called %q", src.method)
	}
	b, _ := json.Marshal(src.params)
	if !strings.Contains(string(b), "cogforge") || !strings.Contains(string(b), "11") {
		t.Fatalf("view %s", b)
	}
}

func TestLearnedMapMarksViewedWhenChangingTabs(t *testing.T) {
	src := &learnedSource{fakeSource: fakeSource{snap: fakeSnapshot(), graph: fakeGraph()}}
	m := New(src)
	m.width, m.height = 120, 40
	m.mapOn = true
	m.tab = graphTab
	m.mp.loaded = true
	m.mp.nodes = fakeGraph().Nodes
	m.mp.selected = "cogforge"
	m.snap.Learned = []protocol.LearnedSuggestion{{ID: 11, Node: "cogforge", Field: "needs", Phrase: "ops backups", Status: "applied", By: "learned", HistoryID: 42, Unseen: true}}
	_, cmd := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	if cmd == nil {
		t.Fatal("missing viewed command")
	}
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			for _, next := range msg {
				run(next)
			}
		}
	}
	run(cmd)
	if src.method != "learned_view" {
		t.Fatalf("called %q", src.method)
	}
}

func TestGoldenLearnedMapAndInbox(t *testing.T) {
	m := openMap(t, mapModel(120, 34, fakeGraph()))
	m.mp.selected = "cogforge"
	m.snap.Learned = []protocol.LearnedSuggestion{{ID: 11, Node: "cogforge", Field: "needs", Phrase: "ops backups", NewValue: "ops backups", Status: "applied", By: "learned", HistoryID: 42, Unseen: true, DecidedAt: t0.UnixNano(), Evidence: []protocol.LearnedEvidence{{ID: "m1", Excerpt: "ops owns backups"}}}}
	golden(t, "map_learned_120x34", m.View())
	n := newModel(100, 30)
	n.snap.Learned = []protocol.LearnedSuggestion{{ID: 7, Node: "ops", Field: "refuses", Phrase: "deploys", Kind: "add", NewValue: "deploys", Evidence: []protocol.LearnedEvidence{{Excerpt: "ops refuses deploys"}}, Status: "pending", Severity: "major"}}
	n = keys(n, "7")
	golden(t, "inbox_learned_100x30", n.View())
}

func TestLearnedMajorWording(t *testing.T) {
	cases := []struct {
		name string
		s    protocol.LearnedSuggestion
		want string
	}{
		{"add owner words", protocol.LearnedSuggestion{Node: "ops", Field: "refuses", Kind: "add", Phrase: "deployment work", RawPhrase: "deploys", Origin: "owner", Evidence: []protocol.LearnedEvidence{{Excerpt: "owner said deploys"}}}, `add "deploys" to what it refuses`},
		{"remove", protocol.LearnedSuggestion{Node: "app", Field: "accepts", Kind: "remove", Phrase: "backups", Evidence: []protocol.LearnedEvidence{{Excerpt: "moved to ops"}}}, `remove "backups" from what it accepts`},
		{"edge", protocol.LearnedSuggestion{Node: "ops", Field: "edge", EdgeKind: "deploys", Kind: "add", Phrase: "app", Evidence: []protocol.LearnedEvidence{{Excerpt: "ops deploys app"}}}, "add a deploys edge to app"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(160, 30)
			tc.s.ID = 7
			tc.s.Status = "pending"
			tc.s.Severity = "major"
			m.snap.Learned = []protocol.LearnedSuggestion{tc.s}
			var got string
			for _, row := range m.inboxRows() {
				if row.kind == "learned" {
					got = ansi.Strip(row.text)
				}
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("want %q in %q", tc.want, got)
			}
		})
	}
}

package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/niparis/agent-net/internal/protocol"
)

func draftSuggestion(id int64, field, value string) protocol.LearnedSuggestion {
	return protocol.LearnedSuggestion{ID: id, Node: "cogforge", Field: field, Kind: "add", Phrase: value, NewValue: value,
		Status: "pending", Severity: "major", Origin: "draft", Level: "repo", Scope: "cogforge", By: "AGENTS.md",
		Evidence: []protocol.LearnedEvidence{{ID: "draft", Excerpt: "Drafted from AGENTS.md"}}}
}

// The charter card shows a draft as a suggestion with its source, and the
// owner accepts or rejects the selected one from the card (decision 0087).
func TestCharterCardShowsDraftWithAcceptAndReject(t *testing.T) {
	for _, tc := range []struct{ key, action string }{{"y", "accept"}, {"x", "reject"}} {
		t.Run(tc.action, func(t *testing.T) {
			m := openMap(t, mapModel(120, 40, fakeGraph()))
			m.snap.Learned = []protocol.LearnedSuggestion{draftSuggestion(-3, "purpose", "Generic endpoints for the shop"), draftSuggestion(-4, "refuses", "Client-specific endpoints")}
			m.mp.selected = "cogforge"
			view := m.testView()
			for _, want := range []string{"Drafted from AGENTS.md", "purpose", "Generic endpoints for the shop", "y accept", "x reject"} {
				if !strings.Contains(view, want) {
					t.Errorf("card lacks %q:\n%s", want, view)
				}
			}
			// . selects the second draft.
			m = keys(m, ".")
			src := &learnedSource{}
			m.src = src
			_, cmd := m.Update(tea.KeyPressMsg{Text: tc.key, Code: []rune(tc.key)[0]})
			if cmd == nil {
				t.Fatalf("%s did nothing", tc.key)
			}
			cmd()
			raw, _ := json.Marshal(src.params)
			if src.method != "learned_answer" || !strings.Contains(string(raw), `"suggestion_id":-4`) || !strings.Contains(string(raw), `"action":"`+tc.action+`"`) {
				t.Fatalf("called %s %s", src.method, raw)
			}
		})
	}
}

func TestCharterCardDraftsOfOtherNodesStayAway(t *testing.T) {
	m := openMap(t, mapModel(120, 40, fakeGraph()))
	other := draftSuggestion(-3, "purpose", "Belongs elsewhere")
	other.Scope, other.Node = "other-repo", "other-repo"
	m.snap.Learned = []protocol.LearnedSuggestion{other}
	m.mp.selected = "cogforge"
	if view := m.testView(); strings.Contains(view, "Belongs elsewhere") || strings.Contains(view, "Drafted from") {
		t.Fatalf("foreign draft on the card:\n%s", view)
	}
}

func TestInboxShowsDraftAsADecision(t *testing.T) {
	src := &learnedSource{fakeSource: fakeSource{snap: fakeSnapshot()}}
	m := New(src)
	m.snap = src.snap
	m.snap.Learned = []protocol.LearnedSuggestion{draftSuggestion(-9, "purpose", "Generic endpoints for the shop")}
	m.tab = inboxTab
	view := m.testView()
	for _, want := range []string{"cogforge", "Generic endpoints for the shop", "AGENTS.md"} {
		if !strings.Contains(view, want) {
			t.Fatalf("inbox lacks %q:\n%s", want, view)
		}
	}
	_, cmd := m.Update(tea.KeyPressMsg{Text: "a", Code: []rune("a")[0]})
	if cmd == nil {
		t.Fatal("accept missing")
	}
	cmd()
	raw, _ := json.Marshal(src.params)
	if src.method != "learned_answer" || !strings.Contains(string(raw), `"suggestion_id":-9`) {
		t.Fatalf("called %s %s", src.method, raw)
	}
}

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
// owner accepts or rejects the selected one from the card (decision 0088).
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
	m.asks = []protocol.Ask{askFromLearned(m.snap.Learned[0])}
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

// The ask of a node holds all its drafted fields. The Desk shows it once, with
// the fields in the detail, and accept or reject answers the ask itself.
func TestDeskShowsGroupedDraftAsOneDecision(t *testing.T) {
	for _, tc := range []struct{ key, answer string }{{"a", "accept"}, {"r", "reject"}} {
		t.Run(tc.answer, func(t *testing.T) {
			src := &learnedSource{fakeSource: fakeSource{snap: fakeSnapshot()}}
			m := New(src)
			m.snap = src.snap
			m.asks = []protocol.Ask{{
				ID: "i_group", Kind: protocol.AskKindDraft, State: protocol.AskStateOpen, FromKind: protocol.AskFromGateway, From: "charter draft",
				AboutKind: protocol.AskAboutProject, AboutID: "cogforge", Question: "Charter draft for cogforge: 2 fields",
				Detail:  "Drafted from AGENTS.md:\n- repo purpose = Generic endpoints for the shop\n- agent accepts = bug fixes",
				Options: []string{"accept", "reject"}, Next: protocol.AskNextOwner, Ref: "drafts:node:cogforge",
			}}
			m.tab = inboxTab
			view := m.testView()
			for _, want := range []string{"cogforge", "Charter draft for cogforge: 2 fields"} {
				if !strings.Contains(view, want) {
					t.Fatalf("inbox lacks %q:\n%s", want, view)
				}
			}
			rows := m.inboxRows()
			if len(rows) != 1 {
				t.Fatalf("want one row, got %d", len(rows))
			}
			var choices []string
			for _, h := range m.inboxItemHints(rows[0]) {
				choices = append(choices, h.key+" "+h.label)
			}
			if got := strings.Join(choices, "  "); !strings.Contains(got, "accept the draft") || !strings.Contains(got, "reject the draft") {
				t.Fatalf("draft choices do not say what they do: %q", got)
			}
			var fieldLines int
			for _, line := range strings.Split(strings.Join(m.inboxDetailLines(rows[0]), "\n"), "\n") {
				if strings.HasPrefix(line, "- repo purpose = ") || strings.HasPrefix(line, "- agent accepts = ") {
					fieldLines++
				}
			}
			if fieldLines != 2 {
				t.Fatalf("detail does not list each field on its own line: %q", m.inboxDetailLines(rows[0]))
			}
			_, cmd := m.Update(tea.KeyPressMsg{Text: tc.key, Code: []rune(tc.key)[0]})
			if cmd == nil {
				t.Fatalf("%s missing", tc.answer)
			}
			cmd()
			raw, _ := json.Marshal(src.params)
			if src.method != "inbox_answer" || !strings.Contains(string(raw), `"id":"i_group"`) || !strings.Contains(string(raw), `"answer":"`+tc.answer+`"`) {
				t.Fatalf("called %s %s", src.method, raw)
			}
		})
	}
}

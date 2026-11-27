package tui

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/niparis/agent-net/internal/protocol"
)

func testDraft(id int64, attr, value string) protocol.CharterDraft {
	return protocol.CharterDraft{ID: id, Level: "repo", Scope: "cogforge", Name: "cogforge", Attr: attr, Value: value,
		Source: "AGENTS.md", Status: "pending"}
}

// callSource records the last call a key made.
type callSource struct {
	fakeSource
	mu     sync.Mutex // a refresh asks its reads together
	method string
	params any
}

func (s *callSource) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	s.mu.Lock()
	s.method, s.params = method, params
	s.mu.Unlock()
	return json.RawMessage(`{}`), nil
}

// The charter card shows a draft as a suggestion with its source, and the
// owner accepts or rejects the selected one from the card (decision 0088).
func TestCharterCardShowsDraftWithAcceptAndReject(t *testing.T) {
	for _, tc := range []struct{ key, action string }{{"y", "accept"}, {"x", "reject"}} {
		t.Run(tc.action, func(t *testing.T) {
			m := openMap(t, mapModel(120, 40, fakeGraph()))
			m.snap.Drafts = []protocol.CharterDraft{testDraft(3, "purpose", "Generic endpoints for the shop"), testDraft(4, "refuses", "Client-specific endpoints")}
			m.mp.selected = "cogforge"
			view := m.testView()
			for _, want := range []string{"Drafted from AGENTS.md", "purpose", "Generic endpoints for the shop", "y accept", "x reject"} {
				if !strings.Contains(view, want) {
					t.Errorf("card lacks %q:\n%s", want, view)
				}
			}
			// . selects the second draft.
			m = keys(m, ".")
			src := &callSource{}
			m.src = src
			_, cmd := m.Update(tea.KeyPressMsg{Text: tc.key, Code: []rune(tc.key)[0]})
			if cmd == nil {
				t.Fatalf("%s did nothing", tc.key)
			}
			cmd()
			raw, _ := json.Marshal(src.params)
			if src.method != protocol.OpDraftAnswer || !strings.Contains(string(raw), `"draft_id":4`) || !strings.Contains(string(raw), `"action":"`+tc.action+`"`) {
				t.Fatalf("called %s %s", src.method, raw)
			}
		})
	}
}

func TestCharterCardDraftsOfOtherNodesStayAway(t *testing.T) {
	m := openMap(t, mapModel(120, 40, fakeGraph()))
	other := testDraft(3, "purpose", "Belongs elsewhere")
	other.Scope, other.Name = "other-repo", "other-repo"
	m.snap.Drafts = []protocol.CharterDraft{other}
	m.mp.selected = "cogforge"
	if view := m.testView(); strings.Contains(view, "Belongs elsewhere") || strings.Contains(view, "Drafted from") {
		t.Fatalf("foreign draft on the card:\n%s", view)
	}
}

// Scenario: the owner opens the cogforge card, which has two pending drafts,
// and presses . to select the second one. The frames are the 80x24 and 120x34
// goldens map_card_drafts_*.
func TestGoldenCharterCardDrafts(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := openMap(t, mapModel(size[0], size[1], fakeGraph()))
		m.snap.Drafts = []protocol.CharterDraft{testDraft(3, "purpose", "Generic endpoints for the shop"), testDraft(4, "refuses", "Client-specific endpoints")}
		m.mp.selected = "cogforge"
		m = keys(m, ".")
		golden(t, "map_card_drafts_"+strconv.Itoa(size[0])+"x"+strconv.Itoa(size[1]), m.testView())
	}
}

package tui

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// toolFile is a managed prompt or agent item, like toolSkill is for skills.
func toolFile(kind, name, hash string) protocol.ToolItem {
	it := toolSkill(name, hash)
	it.Kind, it.ID = kind, kind+":"+name+":"+hash
	return it
}

func promptsToolsFixture(w, h int) (Model, *fakeTools) {
	m, s := newToolsModel(w, h)
	m.snap.Nodes[1].Hostname = "laptop"
	s.items = []protocol.ToolItem{
		toolManaged(toolSkill("review", "s"), "global"),
		toolManaged(toolFile("prompt", "review", "p"), "projects", "tenebrae"),
		toolManaged(toolFile("agent", "planner", "a"), "projects", "homelab"),
	}
	for i := range s.items {
		s.items[i].StartTokens = 100
	}
	m = pressKey(m, "5")
	for i, p := range m.toolsProjects() {
		if p == "homelab" {
			m.tl.agentRow = i
		}
	}
	return m, s
}

// A command and an agent are rows of the project view next to skills, with the
// same name allowed, and each row switches its own item.
func TestProjectViewListsPromptsAndAgents(t *testing.T) {
	m, s := promptsToolsFixture(120, 34)
	text := plain(m)
	for _, want := range []string{"[x] review", "[ ] /review", "[x] planner (agent)"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	rows := m.agentSkills()
	if len(rows) != 3 || rows[0].Kind != "skill" || rows[1].Kind != "prompt" || rows[2].Kind != "agent" {
		t.Fatalf("rows: %+v", rows)
	}
	m = pressKey(m, "right")
	m = pressKey(m, "down")
	m = pressKey(m, " ")
	if len(s.toggles) != 1 || s.toggles[0].Item != "prompt:review:p" || s.toggles[0].Project != "homelab" || !s.toggles[0].On {
		t.Fatalf("toggle: %+v", s.toggles)
	}
	pending := m.tl.agentPending["homelab"]
	if len(pending) != 1 || !pending["prompt:review"] {
		t.Fatalf("the prompt and the skill of the same name must have separate pending keys: %v", pending)
	}
	// The same name as a skill does not turn the skill off.
	if _, ok := pending["review"]; ok {
		t.Fatalf("skill row changed: %v", pending)
	}
}

func TestAllToolsListShowsKindOfPromptsAndAgents(t *testing.T) {
	m, _ := promptsToolsFixture(120, 34)
	m = pressKey(m, "tab")
	text := plain(m)
	for _, want := range []string{"/review", "planner (agent)"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
}

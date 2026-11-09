package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// updateTools adds the three update calls to the shared fake source.
type updateTools struct {
	*fakeTools
	saved   []protocol.ToolImport
	updated []protocol.ToolImportLocation
	plan    protocol.ToolUpdatePlan
}

func (f *updateTools) ToolsSaveChanged(_ context.Context, p protocol.ToolImport) error {
	f.saved = append(f.saved, p)
	return nil
}
func (f *updateTools) ToolsUpdatePlan(context.Context, string) (protocol.ToolUpdatePlan, error) {
	return f.plan, nil
}
func (f *updateTools) ToolsUpdateCopies(_ context.Context, item string, copies []protocol.ToolImportLocation) (protocol.ToolUpdatePlan, error) {
	f.updated = append(f.updated, copies...)
	out := f.plan
	out.Copies = nil
	for _, c := range f.plan.Copies {
		if c.State == protocol.UpdateWillReplace {
			c.State = protocol.UpdateApplied
			out.Copies = append(out.Copies, c)
		}
	}
	return out, nil
}

func changedFixture(t *testing.T) (Model, *updateTools, protocol.ToolLocation) {
	t.Helper()
	m, base := newToolsModel(140, 40)
	edited := toolSkill("code-review", "sha256:4444444eeee", toolLoc(toolNodeM, "Nicolas-MBP", "/Users/n/.claude/skills/code-review", "user", "", "claude"))
	edited.Locations[0].ChangedHere = true
	edited.Locations[0].DeployedHash = "sha256:1111111cccc"
	edited.Locations[0].DeployedNo = 1
	// The Mac's copy is the one that was edited: it replaces its v1 copy.
	var items []protocol.ToolItem
	for _, it := range base.items {
		if it.Name == "code-review" && it.Hash == "sha256:1111111cccc" {
			continue
		}
		items = append(items, it)
	}
	base.items = append(items, edited)
	src := &updateTools{fakeTools: base}
	src.plan = protocol.ToolUpdatePlan{Item: edited.ID, Name: "code-review", No: 4, Hash: edited.Hash, Copies: []protocol.ToolUpdateCopy{
		{NodeID: toolNodeR, Machine: "research", Location: "/home/n/.claude/skills/code-review", FromNo: 3, State: protocol.UpdateWillReplace},
		{NodeID: toolNodeM, Machine: "Nicolas-MBP", Location: "/Users/n/.claude/skills/code-review", State: protocol.UpdateKept, Reason: "changed here: save it as a version first"},
	}}
	m = refresh(m, base)
	m.src = src
	return m, src, edited.Locations[0]
}

func TestChangedCopyShowsOnRowAndSavesThenUpdates(t *testing.T) {
	m, src, loc := changedFixture(t)
	m = openTools(m)
	m = toolFilter(m, "code-review")
	screen := plain(m)
	if !strings.Contains(screen, "changed here on Nicolas-MBP") {
		t.Fatalf("row does not say the copy changed:\n%s", screen)
	}
	m = pressKey(m, "enter")
	if !strings.Contains(plain(m), "Changed here") && !strings.Contains(plain(m), "changed here") {
		t.Logf("detail pane:\n%s", plain(m))
	}
	m = toolChoose(t, m, "Save the copy on Nicolas-MBP as the next version")
	if len(src.saved) != 0 {
		t.Fatal("saved before the owner confirmed")
	}
	if !strings.Contains(plain(m), "Nothing is deployed yet") {
		t.Fatalf("no confirmation:\n%s", plain(m))
	}
	m = pressKey(m, "enter")
	if len(src.saved) != 1 || src.saved[0].Location.Location != loc.Location || src.saved[0].Location.NodeID != toolNodeM {
		t.Fatalf("save calls: %+v", src.saved)
	}
	out := plain(m)
	if !strings.Contains(out, "Saved as v4") || !strings.Contains(out, "replace v3 -> v4 on research") || !strings.Contains(out, "keep on Nicolas-MBP") {
		t.Fatalf("plan not shown:\n%s", out)
	}
	if len(src.updated) != 0 {
		t.Fatal("updated before the owner confirmed the plan")
	}
	m = pressKey(m, "enter")
	if len(src.updated) != 1 || src.updated[0].NodeID != toolNodeR || src.updated[0].Location != "/home/n/.claude/skills/code-review" {
		t.Fatalf("update calls: %+v", src.updated)
	}
	if !strings.Contains(plain(m), "updated research to v4") {
		t.Fatalf("no result:\n%s", plain(m))
	}
}

func TestUneditedSkillOffersNoSaveAction(t *testing.T) {
	m, _, _ := changedFixture(t)
	m = openTools(m)
	m = toolFilter(m, "using-git-worktrees")
	m = pressKey(m, "enter")
	for _, a := range m.tl.modal.menu.actions {
		if strings.HasPrefix(a.label, "Save the copy") {
			t.Fatalf("unedited skill offers %q", a.label)
		}
	}
}

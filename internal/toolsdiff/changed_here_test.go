package toolsdiff

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func changedHere(it protocol.ToolItem, deployedHash string, deployedNo int) protocol.ToolItem {
	for i := range it.Locations {
		it.Locations[i].ChangedHere = true
		it.Locations[i].DeployedHash = deployedHash
		it.Locations[i].DeployedNo = deployedNo
	}
	return it
}

func storeRow(t *testing.T, c Catalog) Row {
	t.Helper()
	for _, r := range c.Rows {
		if r.Origin == OriginStore {
			return r
		}
	}
	t.Fatalf("no store row in %+v", c.Rows)
	return Row{}
}

func TestChangedHereCopyIsListedOnTheRow(t *testing.T) {
	v2 := versioned(withDesired(skill("code-review", "sha:aaaaaaaa11", loc("n_r", "research", "/h/.claude/skills/code-review", "user", "")), "global"), 2)
	edited := changedHere(skill("code-review", "sha:cccccccc33", loc("n_m", "mac", "/m/.claude/skills/code-review", "user", "")), "sha:aaaaaaaa11", 2)
	row := storeRow(t, Build([]protocol.ToolItem{v2, edited}, machines))
	if len(row.Changed) != 1 {
		t.Fatalf("changed: %+v", row.Changed)
	}
	c := row.Changed[0]
	if c.Machine != "mac" || c.Location != "/m/.claude/skills/code-review" || c.DeployedNo != 2 || c.Item != edited.ID || c.NodeID != "n_m" || c.Hash != "sha:cccccccc33" {
		t.Errorf("copy: %+v", c)
	}
	if !strings.Contains(row.Note, "changed on mac") {
		t.Errorf("note %q", row.Note)
	}
	if !strings.Contains(row.Detail, "Changed here") || !strings.Contains(row.Detail, "mac") || !strings.Contains(row.Detail, "v2") {
		t.Errorf("detail %q", row.Detail)
	}
}

func TestUneditedCopyIsNotListedAsChanged(t *testing.T) {
	v2 := versioned(withDesired(skill("code-review", "sha:aaaaaaaa11", loc("n_r", "research", "/h/.claude/skills/code-review", "user", "")), "global"), 2)
	row := storeRow(t, Build([]protocol.ToolItem{v2}, machines))
	if len(row.Changed) != 0 || strings.Contains(row.Note, "changed") {
		t.Fatalf("row %+v", row)
	}
}

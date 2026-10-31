package toolsdiff

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func versioned(it protocol.ToolItem, n int) protocol.ToolItem {
	it.VersionNo = n
	for i := range it.Locations {
		it.Locations[i].VersionNo = n
	}
	return it
}

func TestDriftNamesStoreVersionsPerMachine(t *testing.T) {
	v3 := versioned(withDesired(skill("code-review", "sha:aaaaaaaa11", loc("n_r", "research", "/h/.claude/skills/code-review", "user", "")), "global"), 3)
	v1 := versioned(skill("code-review", "sha:bbbbbbbb22", loc("n_m", "mac", "/m/.claude/skills/code-review", "user", "")), 1)
	d := find(Differences([]protocol.ToolItem{v3, v1}, machines), KindDrift, "code-review")
	if d == nil {
		t.Fatal("no drift")
	}
	if d.Title != "code-review differs: mac on v1, research on v3" {
		t.Errorf("title %q", d.Title)
	}
	if d.Fix != "make every machine use v3" {
		t.Errorf("fix %q", d.Fix)
	}
}

func TestDriftCallsUnknownContentChanged(t *testing.T) {
	v3 := versioned(withDesired(skill("code-review", "sha:aaaaaaaa11", loc("n_r", "research", "/h/.claude/skills/code-review", "user", "")), "global"), 3)
	edited := skill("code-review", "sha:cccccccc33", loc("n_m", "mac", "/m/.claude/skills/code-review", "user", ""))
	d := find(Differences([]protocol.ToolItem{v3, edited}, machines), KindDrift, "code-review")
	if d == nil || d.Title != "code-review differs: mac on changed, research on v3" {
		t.Fatalf("%+v", d)
	}
}

func TestCatalogShowsVersionNumbersInRowAndCell(t *testing.T) {
	v3 := versioned(withDesired(skill("code-review", "sha:aaaaaaaa11", loc("n_r", "research", "/h/.claude/skills/code-review", "user", "")), "global"), 3)
	v1 := versioned(skill("code-review", "sha:bbbbbbbb22", loc("n_m", "mac", "/m/.claude/skills/code-review", "user", "")), 1)
	edited := skill("code-review", "sha:cccccccc33", loc("n_x", "box", "/x/.claude/skills/code-review", "user", ""))
	ms := append([]Machine{{ID: "n_x", Name: "box", Online: true}}, machines...)
	c := Build([]protocol.ToolItem{v3, v1, edited}, ms)
	var row *Row
	for i := range c.Rows {
		if c.Rows[i].Origin == OriginStore {
			row = &c.Rows[i]
		}
	}
	if row == nil || row.Version != "v3" || row.From != "agent-net v3" {
		t.Fatalf("row %+v", row)
	}
	labels := map[string]string{}
	for i, m := range c.Machines {
		labels[m.Name] = row.Cells[i].Label
	}
	if labels["mac"] != "v1" || labels["box"] != "changed" {
		t.Errorf("labels %v", labels)
	}
}

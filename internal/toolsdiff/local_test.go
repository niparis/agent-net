package toolsdiff

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func projectCopy(name, hash, project string, tracked bool) protocol.ToolItem {
	l := loc("n_r", "research", "/src/"+project+"/.claude/skills/"+name, "project", project)
	l.Tracked = tracked
	return skill(name, hash, l)
}

func rowFor(c Catalog, name, origin string) *Row {
	for i := range c.Rows {
		if c.Rows[i].Name == name && c.Rows[i].Origin == origin {
			return &c.Rows[i]
		}
	}
	return nil
}

func TestTrackedProjectCopyIsRepoAndUntrackedIsLocal(t *testing.T) {
	items := []protocol.ToolItem{
		projectCopy("committed", "sha:1111111", "sovl", true),
		projectCopy("scratch", "sha:2222222", "sovl", false),
	}
	c := Build(items, machines)
	repo := rowFor(c, "committed", OriginRepo)
	if repo == nil || repo.From != "repo sovl" {
		t.Fatalf("tracked copy: %+v", c.Rows)
	}
	if text, _ := repo.ProjectSummary(); !strings.HasPrefix(text, "⌂") {
		t.Errorf("repo summary %q", text)
	}
	local := rowFor(c, "scratch", OriginLocal)
	if local == nil || local.From != "local sovl" || rowFor(c, "scratch", OriginRepo) != nil {
		t.Fatalf("untracked copy: %+v", c.Rows)
	}
	if text, _ := local.ProjectSummary(); strings.Contains(text, "⌂") || !strings.Contains(text, "local") {
		t.Errorf("local summary %q", text)
	}
	if !strings.Contains(local.Detail, "not committed") {
		t.Errorf("detail %q", local.Detail)
	}
	// The machine that holds the local copy shows it, the other does not.
	for i, m := range c.Machines {
		want := CellOff
		if m.Name == "research" {
			want = CellFound
		}
		if local.Cells[i].State != want {
			t.Errorf("%s cell %v, want %v", m.Name, local.Cells[i].State, want)
		}
	}
}

func TestLocalCopyHidingYourSkillSaysLocalNotRepo(t *testing.T) {
	managed := withDesired(skill("web", "sha:aaaaaaa", loc("n_r", "research", "/h/.claude/skills/web", "user", "")), "global")
	local := projectCopy("web", "sha:bbbbbbb", "sovl", false)
	tracked := projectCopy("web", "sha:ccccccc", "other", true)
	ds := Differences([]protocol.ToolItem{managed, local, tracked}, machines)
	var l, r *Difference
	for i := range ds {
		if ds[i].Kind != KindHidden {
			continue
		}
		if ds[i].Projects[0] == "sovl" {
			l = &ds[i]
		} else {
			r = &ds[i]
		}
	}
	if l == nil || r == nil {
		t.Fatalf("hidden differences: %+v", ds)
	}
	if !strings.Contains(l.Title, "local copy") || l.Action.Kind == ActRemoveRepo {
		t.Errorf("local: %+v", l)
	}
	if !strings.Contains(r.Title, "repo") || r.Action.Kind != ActRemoveRepo {
		t.Errorf("repo: %+v", r)
	}
}

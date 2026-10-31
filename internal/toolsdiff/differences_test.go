package toolsdiff

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

var machines = []Machine{{ID: "n_r", Name: "research", Online: true}, {ID: "n_m", Name: "mac", Online: false}}

func loc(node, machine, path, scope, project string) protocol.ToolLocation {
	return protocol.ToolLocation{NodeID: node, Machine: machine, GraphNode: project, InventoryItem: protocol.InventoryItem{Harness: "claude", Scope: scope, Project: project, Location: path, Source: protocol.InventoryNative, State: "ok"}}
}

func skill(name, hash string, locs ...protocol.ToolLocation) protocol.ToolItem {
	for i := range locs {
		locs[i].Name, locs[i].Kind, locs[i].Hash = name, "skill", hash
	}
	return protocol.ToolItem{ID: name + "@" + hash, Kind: "skill", Name: name, Hash: hash, Locations: locs}
}

func withDesired(it protocol.ToolItem, scope string, projects ...string) protocol.ToolItem {
	it.Desired = &protocol.ToolRevision{Item: it.ID, Revision: 1, Version: it.Hash, Assignment: protocol.ToolAssignment{Scope: scope, Projects: projects}}
	return it
}

func find(ds []Difference, kind, name string) *Difference {
	for i := range ds {
		if ds[i].Kind == kind && ds[i].Name == name {
			return &ds[i]
		}
	}
	return nil
}

func TestDriftBetweenMachinesSuggestsManagedVersion(t *testing.T) {
	a := withDesired(skill("code-review", "sha:aaaaaaaa11", loc("n_r", "research", "/h/.claude/skills/code-review", "user", "")), "global")
	b := skill("code-review", "sha:bbbbbbbb22", loc("n_m", "mac", "/m/.claude/skills/code-review", "user", ""))
	ds := Differences([]protocol.ToolItem{a, b}, machines)
	d := find(ds, KindDrift, "code-review")
	if d == nil {
		t.Fatalf("no drift in %+v", ds)
	}
	if !strings.Contains(d.Title, "aaaaaa on research") || !strings.Contains(d.Title, "bbbbbb on mac") {
		t.Errorf("title %q", d.Title)
	}
	if d.Fix != "make every machine use aaaaaa" || d.Action.Kind != ActAssign || d.Action.Item != a.ID {
		t.Errorf("fix %q action %+v", d.Fix, d.Action)
	}
	if d.Key != "drift:code-review" || d.Severity != SevWarn {
		t.Errorf("key %q sev %q", d.Key, d.Severity)
	}
}

func TestDriftWithoutManagedCopyOffersToChoose(t *testing.T) {
	a := skill("x", "sha:1111111", loc("n_r", "research", "/h/x", "user", ""))
	b := skill("x", "sha:2222222", loc("n_m", "mac", "/m/x", "user", ""))
	d := find(Differences([]protocol.ToolItem{a, b}, machines), KindDrift, "x")
	if d == nil || d.Action.Kind != ActChoose || len(d.Action.Copies) != 2 {
		t.Fatalf("%+v", d)
	}
}

func TestTwoDifferentCopiesOnOneMachine(t *testing.T) {
	a := skill("design", "sha:aaaaaaa", loc("n_r", "research", "/h/.claude/skills/design", "user", ""))
	b := skill("design", "sha:bbbbbbb", loc("n_r", "research", "/h/.agents/skills/design", "user", ""))
	ds := Differences([]protocol.ToolItem{a, b}, machines)
	d := find(ds, KindTwoCopies, "design")
	if d == nil {
		t.Fatalf("%+v", ds)
	}
	if d.Severity != SevErr || d.Fix != "compare, then keep one" || len(d.Action.Copies) != 2 || d.Action.Kind != ActChoose {
		t.Errorf("%+v", d)
	}
	if find(ds, KindDrift, "design") != nil {
		t.Error("two copies on one machine must not also be reported as drift")
	}
}

func TestRepoCopyHidesManagedCopy(t *testing.T) {
	m := withDesired(skill("web", "sha:aaaaaaa", loc("n_r", "research", "/h/web", "user", "")), "global")
	r := skill("web", "sha:ccccccc", loc("n_r", "research", "/src/sovl/.claude/skills/web", "project", "sovl-clone"))
	ds := Differences([]protocol.ToolItem{m, r}, machines)
	d := find(ds, KindHidden, "web")
	if d == nil || d.Action.Kind != ActRemoveRepo || d.Action.Project != "sovl-clone" || len(d.Action.Copies) != 1 {
		t.Fatalf("%+v", ds)
	}
	if !strings.Contains(d.Title, "sovl-clone's repo has its own web; it hides yours") {
		t.Error(d.Title)
	}
}

func TestSameContentUnderTwoNamesIsADuplicate(t *testing.T) {
	a := withDesired(skill("clean-architecture-python", "sha:same", loc("n_r", "research", "/h/a", "user", "")), "global")
	b := skill("imnplementing-clean-architecture", "sha:same", loc("n_r", "research", "/h/b", "user", ""))
	d := find(Differences([]protocol.ToolItem{a, b}, machines), KindDuplicate, "imnplementing-clean-architecture")
	if d == nil || d.Fix != "remove imnplementing-clean-architecture" || d.Action.Kind != ActRemove {
		t.Fatalf("%+v", d)
	}
	if !strings.Contains(d.Title, "copy of clean-architecture-python") {
		t.Error(d.Title)
	}
}

func TestUnmanagedSkillsOnSomeMachinesAreOneLine(t *testing.T) {
	var items []protocol.ToolItem
	for _, n := range []string{"a", "b", "c"} {
		items = append(items, skill(n, "sha:"+n+"0000000", loc("n_r", "research", "/h/"+n, "user", "")))
	}
	items = append(items, skill("everywhere", "sha:e", loc("n_r", "research", "/h/e", "user", ""), loc("n_m", "mac", "/m/e", "user", "")))
	ds := Differences(items, machines)
	var found *Difference
	for i := range ds {
		if ds[i].Kind == KindUnmanaged {
			found = &ds[i]
		}
	}
	if found == nil {
		t.Fatalf("%+v", ds)
	}
	if found.Title != "3 skills are only on research and not managed by agent-net" || found.Action.Kind != ActImportAll || len(found.Action.Copies) != 3 {
		t.Errorf("%+v", found)
	}
	if len(ds) != 1 {
		t.Errorf("a skill on every machine is not a difference: %+v", ds)
	}
}

func TestPendingAndFailedTargets(t *testing.T) {
	it := withDesired(skill("rbp", "sha:aaaaaaa", loc("n_r", "research", "/h/rbp", "user", "")), "global")
	it.Results = []protocol.ToolResult{{Item: it.ID, NodeID: "n_r", State: "applied"}, {NodeID: "n_m", State: "pending"}}
	ds := Differences([]protocol.ToolItem{it}, machines)
	d := find(ds, KindPending, "rbp")
	if d == nil || !d.Info || d.Title != "rbp waits for mac" {
		t.Fatalf("%+v", ds)
	}
	it.Results[1] = protocol.ToolResult{NodeID: "n_m", State: "conflict", Reason: "destination differs"}
	ds = Differences([]protocol.ToolItem{it}, machines)
	d = find(ds, KindFailed, "rbp")
	if d == nil || d.Info || !strings.Contains(d.Title, "destination differs") {
		t.Fatalf("%+v", ds)
	}
	if find(ds, KindPending, "rbp") != nil {
		t.Error("a failed target is not also pending")
	}
}

func TestMCPNativeAndLeftover(t *testing.T) {
	native := protocol.ToolItem{ID: "mcp@1", Kind: "mcp", Name: "tenebrae", Hash: "h1", Locations: []protocol.ToolLocation{func() protocol.ToolLocation {
		l := loc("n_r", "research", "/h/.claude.json", "user", "")
		l.Import = &protocol.MCPImportInfo{Class: protocol.MCPImportable}
		return l
	}()}}
	ds := Differences([]protocol.ToolItem{native}, machines)
	if d := find(ds, KindMCPNative, "tenebrae"); d == nil || d.Action.Kind != ActMCPImport {
		t.Fatalf("%+v", ds)
	}
	fed := protocol.ToolItem{ID: "mcp@f", Kind: "mcp", Name: "tenebrae", Hash: "federated-1", Federated: true, NativeStillPresent: true}
	ds = Differences([]protocol.ToolItem{native, fed}, machines)
	if find(ds, KindMCPNative, "tenebrae") != nil {
		t.Error("an imported server is not a hand-made one anymore")
	}
	if d := find(ds, KindMCPLeftOld, "tenebrae"); d == nil || d.Action.Kind != ActMCPNativeRemove {
		t.Fatalf("%+v", ds)
	}
}

func TestOrderAndCounts(t *testing.T) {
	a := skill("design", "sha:aaaaaaa", loc("n_r", "research", "/h/a", "user", ""))
	b := skill("design", "sha:bbbbbbb", loc("n_r", "research", "/h/b", "user", ""))
	c := withDesired(skill("rbp", "sha:ccccccc", loc("n_r", "research", "/h/c", "user", "")), "global")
	c.Results = []protocol.ToolResult{{NodeID: "n_m", State: "pending"}}
	ds := Differences([]protocol.ToolItem{a, b, c}, machines)
	if len(ds) != 2 || ds[0].Kind != KindTwoCopies || !ds[1].Info {
		t.Fatalf("order %+v", ds)
	}
	if n := Counts(ds, nil); n != 1 {
		t.Errorf("count %d", n)
	}
	if n := Counts(ds, map[string]bool{ds[0].Key: true}); n != 0 {
		t.Errorf("accepted count %d", n)
	}
}

func TestCatalogCellsAndOrigins(t *testing.T) {
	a := withDesired(skill("review", "sha:aaaaaaa", loc("n_r", "research", "/h/r", "user", ""), loc("n_m", "mac", "/m/r", "user", "")), "global")
	a.Locations[1].Unsupported = []string{"scripts folder is not used by one harness"}
	b := skill("found", "sha:fffffff", loc("n_r", "research", "/h/f", "user", ""))
	p := withDesired(skill("react", "sha:rrrrrrr"), "projects", "wbg", "other")
	p.Results = []protocol.ToolResult{{Project: "wbg", State: "applied"}}
	repo := skill("deploy", "sha:ddddddd", loc("n_r", "research", "/src/homelab/.claude/skills/deploy", "project", "homelab"))
	fed := protocol.ToolItem{ID: "f", Kind: "mcp", Name: "lib", Hash: "federated-1", Federated: true, Host: "research", HostOnline: true}
	c := Build([]protocol.ToolItem{a, b, p, repo, fed}, machines)
	row := func(name string) Row {
		for _, r := range c.Rows {
			if r.Name == name {
				return r
			}
		}
		t.Fatalf("no row %s", name)
		return Row{}
	}
	if r := row("review"); r.Origin != OriginStore || r.From != "agent-net aaaaaa" || r.Cells[0].State != CellOn || r.Cells[1].State != CellPartial || r.Cells[1].Note == "" {
		t.Errorf("review %+v", r)
	}
	if r := row("found"); r.Origin != OriginMachines || r.Cells[0].State != CellFound || r.Cells[1].State != CellOff {
		t.Errorf("found %+v", r)
	}
	if r := row("react"); r.Cells[0].State != CellOff {
		t.Errorf("react %+v", r)
	} else if s, warn := r.ProjectSummary(); s != "2 projects · 1 pending" || !warn {
		t.Errorf("summary %q", s)
	}
	if r := row("deploy"); r.Origin != OriginRepo || r.Repo != "homelab" || r.From != "repo homelab" {
		t.Errorf("deploy %+v", r)
	}
	if r := row("lib"); r.Origin != OriginGateway || r.Cells[0].State != CellOn || r.Cells[1].State != CellOn {
		t.Errorf("lib %+v", r)
	}
	for _, r := range c.Rows {
		for _, s := range []string{r.Note, r.From} {
			if strings.Contains(strings.ToLower(s), "claude") || strings.Contains(s, "codex") {
				t.Errorf("harness name in the grid text of %s: %q", r.Name, s)
			}
		}
	}
}

func TestPluginAndSystemCopiesAreNotDifferences(t *testing.T) {
	a := withDesired(skill("sys", "sha:aaaaaaa", loc("n_r", "research", "/h/a", "user", "")), "uninstalled")
	b := skill("sys", "sha:bbbbbbb", loc("n_m", "mac", "/m/b", "user", ""))
	b.Locations[0].Source = protocol.InventoryPlugin
	if ds := Differences([]protocol.ToolItem{a, b}, machines); len(ds) != 0 {
		t.Fatalf("%+v", ds)
	}
}

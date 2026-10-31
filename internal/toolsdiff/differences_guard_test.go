package toolsdiff

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func kindsOf(ds []Difference) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Kind+":"+d.Name)
	}
	return out
}

// The drift fix names the managed version, not the most common one.
func TestDriftFixNamesTheManagedVersionNotTheMostCommon(t *testing.T) {
	managedItem := withDesired(skill("x", "sha:bbbbbbbb22", loc("n_m", "mac", "/m/x", "user", "")), "global")
	other := skill("x", "sha:aaaaaaaa11", loc("n_r", "research", "/h/x", "user", ""))
	d := find(Differences([]protocol.ToolItem{managedItem, other}, machines), KindDrift, "x")
	if d == nil || d.Fix != "make every machine use bbbbbb" || d.Action.Item != managedItem.ID {
		t.Fatalf("%+v", d)
	}
}

// A project's own copy hides nothing when it is the same as a machine-wide one,
// when it comes from a read-only source, or when the managed version is it.
func TestRepoCopyIsNotHiddenWhenItMatchesOrIsReadOnly(t *testing.T) {
	user := skill("web", "sha:aaaaaaa", loc("n_r", "research", "/h/web", "user", ""))
	same := skill("web", "sha:aaaaaaa", loc("n_r", "research", "/src/p/.claude/skills/web", "project", "p"))
	if ds := Differences([]protocol.ToolItem{user, same}, machines); find(ds, KindHidden, "web") != nil {
		t.Errorf("a repo copy equal to the machine copy was reported: %v", kindsOf(ds))
	}
	// A plugin copy in a project is not the owner's to move.
	plugin := skill("web", "sha:ccccccc", loc("n_r", "research", "/src/p/plugin/web", "project", "p"))
	plugin.Locations[0].Source = protocol.InventoryPlugin
	if ds := Differences([]protocol.ToolItem{user, plugin}, machines); find(ds, KindHidden, "web") != nil {
		t.Errorf("a read-only repo copy was reported: %v", kindsOf(ds))
	}
	// The managed version counts as the machine-wide one even where no machine
	// holds it yet.
	other := skill("web", "sha:eeeeeee", loc("n_r", "research", "/h/other", "user", ""))
	store := skill("web", "sha:ddddddd")
	repo := skill("web", "sha:ddddddd", loc("n_r", "research", "/src/p/.claude/skills/web", "project", "p"))
	if ds := Differences([]protocol.ToolItem{other, store, repo}, machines); find(ds, KindHidden, "web") != nil {
		t.Errorf("a repo copy equal to the managed version was reported: %v", kindsOf(ds))
	}
}

func TestPendingAndFailedRespectTheDesiredStateAndTheResult(t *testing.T) {
	// A skill the owner turned off waits for nothing.
	off := withDesired(skill("old", "sha:aaaaaaa", loc("n_r", "research", "/h/old", "user", "")), "uninstalled")
	off.Results = []protocol.ToolResult{{NodeID: "n_m", State: "pending"}, {NodeID: "n_r", State: "failed", Reason: "busy"}}
	if ds := Differences([]protocol.ToolItem{off}, machines); len(ds) != 0 {
		t.Errorf("an uninstalled skill left differences: %v", kindsOf(ds))
	}
	// A failed target is a failure with its reason, as a conflict is.
	it := withDesired(skill("rbp", "sha:aaaaaaa", loc("n_r", "research", "/h/rbp", "user", "")), "global")
	it.Results = []protocol.ToolResult{{NodeID: "n_m", State: "failed", Reason: "disk full"}}
	d := find(Differences([]protocol.ToolItem{it}, machines), KindFailed, "rbp")
	if d == nil || !strings.Contains(d.Title, "disk full") || len(d.Machines) != 1 || d.Machines[0] != "mac" {
		t.Errorf("failed target: %+v", d)
	}
	// An apply that has no results yet waits for every machine.
	waiting := withDesired(skill("new", "sha:bbbbbbb", loc("n_r", "research", "/h/new", "user", "")), "global")
	waiting.Applied = "pending"
	d = find(Differences([]protocol.ToolItem{waiting}, machines), KindPending, "new")
	if d == nil || d.Title != "new waits for mac, research" {
		t.Errorf("pending without results: %+v", d)
	}
}

func TestDuplicateRules(t *testing.T) {
	// The managed name is the original, even when it sorts last.
	alpha := skill("alpha", "sha:same", loc("n_r", "research", "/h/alpha", "user", ""))
	zeta := withDesired(skill("zeta", "sha:same", loc("n_r", "research", "/h/zeta", "user", "")), "global")
	d := find(Differences([]protocol.ToolItem{alpha, zeta}, machines), KindDuplicate, "alpha")
	if d == nil || !strings.Contains(d.Title, "copy of zeta") {
		t.Errorf("managed original: %+v", d)
	}
	// Two skills with no content hash are not copies of each other.
	a := skill("a", "", loc("n_r", "research", "/h/a", "user", ""))
	b := skill("b", "", loc("n_r", "research", "/h/b", "user", ""))
	for _, ds := range [][]Difference{Differences([]protocol.ToolItem{a, b}, machines)} {
		for _, x := range ds {
			if x.Kind == KindDuplicate {
				t.Errorf("empty hashes reported as duplicates: %+v", x)
			}
		}
	}
	// A project copy under another name is not a machine copy.
	user := skill("one", "sha:same", loc("n_r", "research", "/h/one", "user", ""))
	repo := skill("two", "sha:same", loc("n_r", "research", "/src/p/two", "project", "p"))
	for _, x := range Differences([]protocol.ToolItem{user, repo}, machines) {
		if x.Kind == KindDuplicate {
			t.Errorf("a project copy was reported as a duplicate: %+v", x)
		}
	}
}

func TestUnmanagedIgnoresProjectCopies(t *testing.T) {
	repo := skill("only-in-repo", "sha:aaaaaaa", loc("n_r", "research", "/src/p/.claude/skills/only-in-repo", "project", "p"))
	for _, x := range Differences([]protocol.ToolItem{repo}, machines) {
		if x.Kind == KindUnmanaged {
			t.Errorf("a repo-only skill was reported as unmanaged on a machine: %+v", x)
		}
	}
}

func TestMCPLeftoverNeedsAHandMadeEntryThatRemains(t *testing.T) {
	native := protocol.ToolItem{ID: "mcp@1", Kind: "mcp", Name: "tenebrae", Hash: "h1", Locations: []protocol.ToolLocation{func() protocol.ToolLocation {
		l := loc("n_r", "research", "/h/.claude.json", "user", "")
		l.Import = &protocol.MCPImportInfo{Class: protocol.MCPImportable}
		return l
	}()}}
	// The federated server says its hand-made entry is gone: nothing to remove.
	fed := protocol.ToolItem{ID: "mcp@f", Kind: "mcp", Name: "tenebrae", Hash: "federated-1", Federated: true}
	for _, x := range Differences([]protocol.ToolItem{native, fed}, machines) {
		if x.Kind == KindMCPLeftOld || x.Kind == KindMCPNative {
			t.Errorf("unexpected %+v", x)
		}
	}
	// A hand-made entry the scan cannot import is not offered for import.
	native.Locations[0].Import = &protocol.MCPImportInfo{Class: protocol.MCPUnsupported, Reason: "OAuth"}
	for _, x := range Differences([]protocol.ToolItem{native}, machines) {
		if x.Kind == KindMCPNative {
			t.Errorf("an unsupported entry was offered for import: %+v", x)
		}
	}
}

// Errors come before warnings, and a line with nothing to do comes last even
// when it shares the quietest severity with a line that has something to do.
func TestDifferencesAreOrderedBySeverityThenInfo(t *testing.T) {
	two1 := skill("two", "sha:aaaaaaa", loc("n_r", "research", "/h/a", "user", ""))
	two2 := skill("two", "sha:bbbbbbb", loc("n_r", "research", "/h/b", "user", ""))
	drift1 := skill("drift", "sha:ccccccc", loc("n_r", "research", "/h/c", "user", ""))
	drift2 := skill("drift", "sha:ddddddd", loc("n_m", "mac", "/m/d", "user", ""))
	wait := withDesired(skill("wait", "sha:eeeeeee", loc("n_r", "research", "/h/e", "user", "")), "global")
	wait.Results = []protocol.ToolResult{{NodeID: "n_m", State: "pending"}}
	lone := skill("lone", "sha:fffffff", loc("n_r", "research", "/h/f", "user", ""))
	ds := Differences([]protocol.ToolItem{two1, two2, drift1, drift2, wait, lone}, machines)
	var order []string
	for _, d := range ds {
		order = append(order, d.Kind)
	}
	want := []string{KindTwoCopies, KindDrift, KindUnmanaged, KindPending}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order %v, want %v", order, want)
	}
}

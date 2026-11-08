package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestToolPlanDestinationAndCollision(t *testing.T) {
	home := t.TempDir()
	d := &Daemon{cfg: Config{InventoryHome: home}}
	req := protocol.ToolPlanProbe{Name: "demo", Harness: "claude"}
	got := d.probeToolDestination(req)
	want := filepath.Join(home, ".claude/skills/demo")
	if got.Destination != want || got.Conflict != "" {
		t.Fatalf("destination: %+v", got)
	}
	if err := os.MkdirAll(want, 0700); err != nil {
		t.Fatal(err)
	}
	got = d.probeToolDestination(req)
	if got.Conflict == "" {
		t.Fatal("existing foreign destination not detected")
	}
}

func TestToolsPlanAndApplyOwnerGate(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/skills/demo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("demo"), 0600); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory, cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	var item protocol.ToolItem
	eventually(t, "tool inventory", func() bool {
		raw, err := cli.call("tools_items", map[string]any{"kind": "skill"})
		if err != nil {
			return false
		}
		var p protocol.ToolsPage
		if json.Unmarshal(raw, &p) != nil {
			return false
		}
		for _, v := range p.Items {
			if v.Name == "demo" {
				item = v
				return true
			}
		}
		return false
	})
	req := protocol.ToolPlanRequest{Item: item.ID, Target: protocol.ToolAssignment{Scope: "global"}}
	raw, err := cli.call("tools_plan", req)
	if err != nil {
		t.Fatal(err)
	}
	var plan protocol.ToolPlan
	if err = json.Unmarshal(raw, &plan); err != nil || plan.ID == "" {
		t.Fatalf("plan: %s %v", raw, err)
	}
	pi, _ := helloPi(t, n, "agent-plan", "/work", true)
	if _, err = pi.call("tools_plan", req); err == nil {
		t.Fatal("agent planned assignment")
	}
	if _, err = pi.call("tools_apply", protocol.ToolPlanID{ID: plan.ID}); err == nil {
		t.Fatal("agent applied assignment")
	}
	if _, err = cli.call("tools_apply", protocol.ToolPlanID{ID: plan.ID}); err != nil {
		t.Fatal(err)
	}
	raw, err = cli.call("tools_history", protocol.ToolHistoryRequest{Item: item.ID})
	if err != nil || !strings.Contains(string(raw), `"action":"apply"`) {
		t.Fatalf("history: %s %v", raw, err)
	}
	if _, err = pi.call("tools_history", protocol.ToolHistoryRequest{Item: item.ID}); err == nil {
		t.Fatal("agent read history")
	}
}

func TestCTOToolPlanAuditAndNeeds(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/skills/demo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("demo"), 0600); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	var item protocol.ToolItem
	eventually(t, "tool inventory", func() bool {
		raw, err := cli.call("tools_items", map[string]any{})
		if err != nil {
			return false
		}
		var p protocol.ToolsPage
		if json.Unmarshal(raw, &p) != nil {
			return false
		}
		for _, v := range p.Items {
			if v.Name == "demo" {
				item = v
				return true
			}
		}
		return false
	})
	if err := g.gw.Store().AddArchitect("architect", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetCharter("architect", "rules", "Need a review skill", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := n.d.runCTOToolRaw(context.Background(), "tools_list", json.RawMessage(`{}`)); err == nil {
		t.Fatal("CTO read owner-only tools inventory")
	}
	raw, err := cli.call("tools_items", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var page protocol.ToolsPage
	if json.Unmarshal(raw, &page) != nil {
		t.Fatal("bad list")
	}
	for _, v := range page.Items {
		if v.ID == item.ID && v.Desired != nil {
			t.Fatal("charter need created assignment")
		}
	}
	req, _ := json.Marshal(map[string]any{"action": "plan", "item": item.ID, "target": protocol.ToolAssignment{Scope: "global"}})
	value, err := legacyProposalFixture(context.Background(), n.d, "tools_change", req)
	if err != nil {
		t.Fatal(err)
	}
	b := approveCTOToolResult(t, n.d, value)
	var p protocol.ToolPlan
	if json.Unmarshal(b, &p) != nil || !strings.HasPrefix(p.Actor, "owner (CTO proposal p_") {
		t.Fatalf("CTO actor: %s", b)
	}
	apply, _ := json.Marshal(map[string]string{"action": "apply", "plan_id": p.ID})
	if value, err = legacyProposalFixture(context.Background(), n.d, "tools_change", apply); err != nil {
		t.Fatal(err)
	}
	approveCTOToolResult(t, n.d, value)
	history, err := g.gw.Store().ToolHistory(item.ID)
	if err != nil || len(history) != 2 || !strings.HasPrefix(history[1].Actor, "owner (CTO proposal p_") {
		t.Fatalf("CTO audit: %+v %v", history, err)
	}
}

func TestCTOToolsApplyInboxUndo(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/skills/demo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("demo"), 0600); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	var item protocol.ToolItem
	eventually(t, "tool inventory", func() bool {
		raw, err := cli.call("tools_items", map[string]any{})
		var page protocol.ToolsPage
		if err != nil || json.Unmarshal(raw, &page) != nil {
			return false
		}
		for _, v := range page.Items {
			if v.Name == "demo" {
				item = v
				return true
			}
		}
		return false
	})
	var plan protocol.ToolPlan
	if err := json.Unmarshal(cli.mustCall("tools_plan", protocol.ToolPlanRequest{Item: item.ID, Target: protocol.ToolAssignment{Scope: "global"}}), &plan); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"action": "apply", "plan_id": plan.ID})
	if _, err := n.d.runCTOToolRaw(ctx, "tools_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := inboxAndLog(g.gw.Store())
	if err != nil || len(items) != 1 || len(items[0].Options) != 2 || items[0].Options[0] != "tools_undo" {
		t.Fatalf("missing tool apply undo: %+v %v", items, err)
	}
	var undo protocol.ToolUndoRequest
	if err = json.Unmarshal([]byte(items[0].Options[1]), &undo); err != nil || undo.Item != item.ID {
		t.Fatalf("wrong tool undo: %+v %v", undo, err)
	}
	cli.mustCall("tools_undo", undo)
	var before []protocol.ToolProjectState
	if err = json.Unmarshal(cli.mustCall("tools_projects", map[string]any{}), &before); err != nil {
		t.Fatal(err)
	}
	var original bool
	for _, state := range before {
		if state.Item == item.ID {
			original = state.NewProjects
		}
	}
	args, _ = json.Marshal(map[string]any{"action": "new_projects", "item": item.ID, "on": !original})
	if _, err = n.d.runCTOToolRaw(ctx, "tools_change", args); err != nil {
		t.Fatal(err)
	}
	items, err = inboxAndLog(g.gw.Store())
	if err != nil || len(items) != 2 || len(items[1].Options) != 2 || items[1].Options[0] != "tools_new_projects" {
		t.Fatalf("missing new-projects undo: %+v %v", items, err)
	}
	cli.mustCall("tools_new_projects", json.RawMessage(items[1].Options[1]))
	var restored []protocol.ToolProjectState
	if err = json.Unmarshal(cli.mustCall("tools_projects", map[string]any{}), &restored); err != nil {
		t.Fatal(err)
	}
	for _, state := range restored {
		if state.Item == item.ID && state.NewProjects != original {
			t.Fatalf("new-projects setting not restored: %+v", state)
		}
	}
}

func TestToolPlanDestinationFingerprintChanges(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude/skills/demo")
	if err := os.MkdirAll(dest, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dest, "SKILL.md")
	if err := os.WriteFile(file, []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{InventoryHome: home}}
	req := protocol.ToolPlanProbe{Name: "demo", Harness: "claude"}
	a := d.probeToolDestination(req)
	if a.Fingerprint == "" {
		t.Fatal("no destination fingerprint")
	}
	if err := os.WriteFile(file, []byte("different"), 0600); err != nil {
		t.Fatal(err)
	}
	b := d.probeToolDestination(req)
	if a.Fingerprint == b.Fingerprint {
		t.Fatal("destination change was missed")
	}
}

func TestToolApplyRejectsChangedDestination(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/skills/demo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("demo"), 0600); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	var item protocol.ToolItem
	eventually(t, "tool inventory", func() bool {
		raw, err := cli.call("tools_items", map[string]any{})
		if err != nil {
			return false
		}
		var p protocol.ToolsPage
		if json.Unmarshal(raw, &p) != nil {
			return false
		}
		for _, v := range p.Items {
			if v.Name == "demo" {
				item = v
				return true
			}
		}
		return false
	})
	raw, err := cli.call("tools_plan", protocol.ToolPlanRequest{Item: item.ID, Target: protocol.ToolAssignment{Scope: "global"}})
	if err != nil {
		t.Fatal(err)
	}
	var plan protocol.ToolPlan
	if err = json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	dest := ""
	for _, target := range plan.Targets {
		if target.Root == ".agents/skills" && target.Action == "copy" {
			dest = target.Destination
			break
		}
	}
	if dest == "" {
		t.Fatalf("no destination: %+v", plan.Targets)
	}
	if err = os.MkdirAll(dest, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = cli.call("tools_apply", protocol.ToolPlanID{ID: plan.ID}); err == nil || !strings.Contains(err.Error(), "plan out of date") {
		t.Fatalf("expected stale plan: %v", err)
	}
}

func TestToolDestinationFingerprintDetectsSameMetadataContentChange(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude/skills/demo")
	if err := os.MkdirAll(dest, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dest, "SKILL.md")
	if err := os.WriteFile(file, []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{InventoryHome: home}}
	req := protocol.ToolPlanProbe{Name: "demo", Harness: "claude"}
	a := d.probeToolDestination(req)
	if err = os.WriteFile(file, []byte("two"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(file, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	b := d.probeToolDestination(req)
	if a.Fingerprint == b.Fingerprint {
		t.Fatal("content change with restored metadata was missed")
	}
}

func TestToolPlanReportsMissingCheckout(t *testing.T) {
	home := t.TempDir()
	d := &Daemon{cfg: Config{InventoryHome: home}}
	missing := filepath.Join(home, "missing-checkout")
	r := d.probeToolDestination(protocol.ToolPlanProbe{Name: "demo", Harness: "codex", Checkout: missing})
	if r.Conflict == "" {
		t.Fatalf("missing checkout not reported: %+v", r)
	}
}

// The machine refuses a destination that is the home folder, is not a Git
// working tree, or is a worktree of another project (decision 0048).
func TestToolPlanRefusesNonProjectFolders(t *testing.T) {
	home := t.TempDir()
	plain := t.TempDir()
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", "main")
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, repo, "worktree", "add", "-qb", "other", other)
	d := &Daemon{cfg: Config{InventoryHome: home}}
	probe := func(checkout string, worktrees []string) protocol.ToolPlanProbeResult {
		return d.probeToolDestination(protocol.ToolPlanProbe{Name: "demo", Harness: "claude", Checkout: checkout, Worktrees: worktrees})
	}
	if got := probe(home, []string{}); got.Conflict != "the home folder is never a project" {
		t.Fatalf("home: %+v", got)
	}
	if got := probe(plain, []string{}); got.Conflict != "not a Git repository" {
		t.Fatalf("plain folder: %+v", got)
	}
	if got := probe(other, []string{}); got.Conflict != "worktree of another project" {
		t.Fatalf("foreign worktree: %+v", got)
	}
	if got := probe(other, []string{other}); got.Conflict != "" {
		t.Fatalf("own worktree: %+v", got)
	}
	if got := probe(repo, []string{other}); got.Conflict != "" {
		t.Fatalf("main checkout: %+v", got)
	}
}

func TestToolApplyRejectsUnscannedShadow(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/skills/demo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	var item protocol.ToolItem
	eventually(t, "tool inventory", func() bool {
		raw, err := cli.call("tools_items", map[string]any{})
		if err != nil {
			return false
		}
		var p protocol.ToolsPage
		if json.Unmarshal(raw, &p) != nil {
			return false
		}
		for _, v := range p.Items {
			if v.Name == "demo" {
				item = v
				return true
			}
		}
		return false
	})
	raw, err := cli.call("tools_plan", protocol.ToolPlanRequest{Item: item.ID, Target: protocol.ToolAssignment{Scope: "uninstalled"}})
	if err != nil {
		t.Fatal(err)
	}
	var plan protocol.ToolPlan
	if err = json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	shadow := filepath.Join(home, ".agents/skills/demo")
	if err = os.MkdirAll(shadow, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(shadow, "SKILL.md"), []byte("new shadow"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = cli.call("tools_apply", protocol.ToolPlanID{ID: plan.ID}); err == nil || !strings.Contains(err.Error(), "plan out of date") {
		t.Fatalf("unscanned shadow accepted: %v", err)
	}
}

func TestToolProjectFolderTargetsAndShadow(t *testing.T) {
	checkout := t.TempDir()
	targets := []protocol.ToolTarget{}
	for _, h := range []string{"claude", "codex"} {
		targets = append(targets, protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "skill:demo", Assignment: protocol.ToolAssignment{Scope: "projects"}}, Target: protocol.ToolPlanTarget{Harness: h, Checkout: checkout, Action: "copy"}})
	}
	if got := projectTargets(targets); len(got) != 0 {
		t.Fatalf("planned folders conflict: %v", got)
	}
	if err := os.MkdirAll(filepath.Join(checkout, ".opencode/skills/demo"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := projectTargets(targets); len(got) == 0 {
		t.Fatal("foreign OpenCode source was not detected")
	}
}

func TestToolProbeBatchLogs(t *testing.T) {
	var logs bytes.Buffer
	d := &Daemon{cfg: Config{InventoryHome: t.TempDir()}, log: log.New(&logs, "", 0)}
	results := d.probeToolDestinations([]protocol.ToolPlanProbe{{Name: "demo", Harness: "claude"}, {Name: "bad/name", Harness: "claude"}})
	if len(results) != 2 || results[0].Conflict != "" || results[1].Conflict == "" {
		t.Fatalf("results: %+v", results)
	}
	for _, want := range []string{"tool probes: count=2", "duration=", "tool probe failed:", "bad/name"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("missing %q: %s", want, logs.String())
		}
	}
}

package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

// The fixture: two machines (research and Nicolas-MBP), about forty skills and
// one MCP server. It has a repo skill that hides a managed one, a version drift
// (code-review), two different copies on one machine (design-taste-frontend),
// a duplicate under another name, a pending target and a harness limit.

const (
	toolNodeR = "n_research"
	toolNodeM = "n_mbp"
)

func toolLoc(node, machine, path, scope, project, harness string) protocol.ToolLocation {
	return protocol.ToolLocation{NodeID: node, Machine: machine, GraphNode: project, InventoryItem: protocol.InventoryItem{
		Harness: harness, Kind: "skill", Scope: scope, Project: project, Location: path, Source: protocol.InventoryNative, State: "ok"}}
}

func toolSkill(name, hash string, locs ...protocol.ToolLocation) protocol.ToolItem {
	for i := range locs {
		locs[i].Name, locs[i].Hash = name, hash
	}
	return protocol.ToolItem{ID: "skill:" + name + ":" + hash, Kind: "skill", Name: name, Hash: hash, Locations: locs, State: "ok"}
}

func toolManaged(it protocol.ToolItem, scope string, projects ...string) protocol.ToolItem {
	it.Desired = &protocol.ToolRevision{Item: it.ID, Revision: 1, Version: it.Hash, Assignment: protocol.ToolAssignment{Scope: scope, Projects: projects}, State: "pending"}
	return it
}

func userBoth(name, hash string) protocol.ToolItem {
	return toolSkill(name, hash,
		toolLoc(toolNodeR, "research", "/home/n/.claude/skills/"+name, "user", "", "claude"),
		toolLoc(toolNodeM, "Nicolas-MBP", "/Users/n/.claude/skills/"+name, "user", "", "claude"))
}

func toolFixtureNodes() []protocol.NodeView {
	return []protocol.NodeView{
		{NodeID: toolNodeR, Hostname: "research", Online: true},
		{NodeID: toolNodeM, Hostname: "Nicolas-MBP", Online: true},
	}
}

func toolFixtureItems() []protocol.ToolItem {
	var items []protocol.ToolItem
	// managed, on both machines
	items = append(items, toolManaged(userBoth("using-git-worktrees", "sha256:3333333aaaa"), "global"))
	items = append(items, toolManaged(userBoth("research", "sha256:1111111aaaa"), "global"))
	// drift: the Mac has another version
	review := toolManaged(toolSkill("code-review", "sha256:3333333bbbb", toolLoc(toolNodeR, "research", "/home/n/.claude/skills/code-review", "user", "", "claude")), "global")
	oldReview := toolSkill("code-review", "sha256:1111111cccc", toolLoc(toolNodeM, "Nicolas-MBP", "/Users/n/.claude/skills/code-review", "user", "", "claude"))
	items = append(items, review, oldReview)
	// a harness limit
	diag := userBoth("diagnosing-bugs", "sha256:1111111dddd")
	diag.Locations[1].Unsupported = []string{"the scripts folder is not used there: it gets the text only"}
	items = append(items, toolManaged(diag, "global"))
	// project assignments, one pending, one hidden by a repo copy
	react := toolManaged(toolSkill("react-best-practices", "sha256:1111111eeee"), "projects", "wbg-housing", "wbg-worldview", "homelab-portal")
	react.Results = []protocol.ToolResult{{Project: "wbg-housing", NodeID: toolNodeR, State: "applied"}, {Project: "wbg-worldview", NodeID: toolNodeR, State: "applied"}, {Project: "homelab-portal", NodeID: toolNodeM, State: "pending"}}
	items = append(items, react)
	web := toolManaged(toolSkill("web-design-guidelines", "sha256:1111111ffff"), "projects", "wbg-housing", "sovl-clone")
	web.Results = []protocol.ToolResult{{Project: "wbg-housing", NodeID: toolNodeR, State: "applied"}, {Project: "sovl-clone", NodeID: toolNodeR, State: "applied"}}
	items = append(items, web)
	items = append(items, toolSkill("web-design-guidelines", "sha256:2222222ffff", toolLoc(toolNodeR, "research", "/src/sovl-clone/.claude/skills/web-design-guidelines", "project", "sovl-clone", "claude")))
	pg := toolManaged(toolSkill("mastering-postgresql", "sha256:2222222aaaa"), "projects", "cogforge", "tenebrae")
	pg.Results = []protocol.ToolResult{{Project: "cogforge", NodeID: toolNodeR, State: "applied"}, {Project: "tenebrae", NodeID: toolNodeM, State: "pending"}}
	items = append(items, pg)
	// two different copies on research
	items = append(items,
		toolSkill("design-taste-frontend", "sha256:aaaaaaa1111", toolLoc(toolNodeR, "research", "/home/n/.claude/skills/design-taste-frontend", "user", "", "claude")),
		toolSkill("design-taste-frontend", "sha256:bbbbbbb2222", toolLoc(toolNodeR, "research", "/home/n/.agents/skills/design-taste-frontend", "user", "", "pi")))
	// a duplicate under another name
	items = append(items, toolManaged(userBoth("clean-architecture-python", "sha256:5555555aaaa"), "global"))
	items = append(items, toolSkill("imnplementing-clean-architecture", "sha256:5555555aaaa", toolLoc(toolNodeR, "research", "/home/n/.claude/skills/imnplementing-clean-architecture", "user", "", "claude")))
	// repo skills
	items = append(items,
		toolSkill("deploy-test-server", "sha256:6666666aaaa", toolLoc(toolNodeR, "research", "/src/sovl-clone/.claude/skills/deploy-test-server", "project", "sovl-clone", "claude")),
		toolSkill("estate-runbook", "sha256:7777777aaaa", toolLoc(toolNodeR, "research", "/src/homelab/.claude/skills/estate-runbook", "project", "homelab", "claude")))
	// unmanaged skills: 4 on both machines, the rest only on research
	for i := 0; i < 26; i++ {
		name := fmt.Sprintf("found-skill-%02d", i)
		h := fmt.Sprintf("sha256:9%06d", i)
		if i < 4 {
			items = append(items, userBoth(name, h))
			continue
		}
		items = append(items, toolSkill(name, h, toolLoc(toolNodeR, "research", "/home/n/.claude/skills/"+name, "user", "", "claude")))
	}
	// the MCP server on the gateway
	items = append(items, protocol.ToolItem{ID: "mcp:tenebrae-library", Kind: "mcp", Name: "tenebrae-library", Hash: "federated-1", Federated: true, Host: "research", HostOnline: true, Scope: "global", State: "ok"})
	return items
}

// fakeTools answers the Tools tab from memory and records what it was asked.
type fakeTools struct {
	*fakeSource
	mu       sync.Mutex
	items    []protocol.ToolItem
	accepted []string
	needs    []protocol.ToolNeed
	itemsErr error

	plans      []protocol.ToolPlanRequest
	applied    []string
	imports    []protocol.ToolImport
	acceptCall []protocol.ToolAccept
	acceptErr  error
	rescans    int
	sent       []string // "to: body"
	mcpPlans   []string
	mcpApplied []string
}

func newToolsModel(w, h int) (Model, *fakeTools) {
	base := &fakeSource{snap: fakeSnapshot(), events: fakeEvents()}
	base.snap.Nodes = toolFixtureNodes()
	base.snap.Projects = nil
	for _, p := range []string{"a2a-ocs", "cogforge", "dotfiles", "flood-wargame", "homelab", "homelab-portal", "infra-game", "sovl-clone", "strategies", "tenebrae", "wbg-arch", "wbg-housing", "wbg-worldview"} {
		base.snap.Projects = append(base.snap.Projects, protocol.ProjectView{Name: p, NodeID: toolNodeR, Path: "/src/" + p, Source: protocol.SourceRegistered})
	}
	base.snap.Agents = append(base.snap.Agents, protocol.AgentView{SessionID: "s_sovl", NodeID: toolNodeR, Hostname: "research", Harness: "claude", Online: true, Status: "idle",
		Profile: protocol.Profile{Name: "sovl-agent"}, Repo: protocol.RepoInfo{Project: "sovl-clone", Root: "/src/sovl-clone"}})
	src := &fakeTools{fakeSource: base, items: toolFixtureItems(), needs: []protocol.ToolNeed{{Node: "homelab", Text: "a deploy runbook skill", State: "unverified"}}}
	m := New(src)
	m.loc = time.UTC
	m.now = func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	m = refresh(m, src)
	return m, src
}

// openTools presses 5 and lets the load finish.
func openTools(m Model) Model { return pressKey(m, "5") }

func (f *fakeTools) ToolsItems(context.Context) ([]protocol.ToolItem, error) {
	return f.items, f.itemsErr
}
func (f *fakeTools) ToolsAccepted(context.Context) ([]string, error) { return f.accepted, nil }
func (f *fakeTools) ToolsAccept(_ context.Context, key string, on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acceptCall = append(f.acceptCall, protocol.ToolAccept{Key: key, Accepted: on})
	return f.acceptErr
}
func (f *fakeTools) ToolsNeeds(context.Context) ([]protocol.ToolNeed, error) { return f.needs, nil }
func (f *fakeTools) ToolsPlan(_ context.Context, r protocol.ToolPlanRequest) (protocol.ToolPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.plans = append(f.plans, r)
	plan := protocol.ToolPlan{ID: fmt.Sprintf("plan_%d", len(f.plans)), Item: r.Item, Assignment: r.Target, Operation: r.Operation, RepoTargets: r.RepoTargets, Conflicts: []string{}}
	if r.Operation == "repo_remove" {
		for _, target := range r.RepoTargets {
			plan.Targets = append(plan.Targets, protocol.ToolPlanTarget{NodeID: target.NodeID, Machine: "research", Project: r.Project, Checkout: target.Checkout, Action: "repo_remove", State: "pending", Branch: "agent-net/remove-deploy-test-server", DefaultBranch: "main", BaseCommit: "base", Files: []string{".agents/skills/deploy-test-server/SKILL.md", ".claude/skills/deploy-test-server/SKILL.md"}, Dirty: true})
		}
		return plan, nil
	}
	for _, n := range []struct{ id, host, dir string }{{toolNodeR, "research", "/home/n"}, {toolNodeM, "Nicolas-MBP", "/Users/n"}} {
		for _, h := range []string{".claude", ".pi/agent", ".codex"} {
			plan.Targets = append(plan.Targets, protocol.ToolPlanTarget{NodeID: n.id, Machine: n.host, Harness: "h", Action: "copy", State: "pending", Destination: n.dir + "/" + h + "/skills/x"})
		}
	}
	return plan, nil
}
func (f *fakeTools) ToolsApply(_ context.Context, id string) (protocol.ToolApplyResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, id)
	if len(f.plans) > 0 && f.plans[len(f.plans)-1].Operation == "repo_remove" {
		return protocol.ToolApplyResult{Targets: []protocol.ToolTargetOutcome{{NodeID: toolNodeR, Machine: "research", Project: "sovl-clone", Action: "repo_remove", State: "ok", Branch: "agent-net/remove-deploy-test-server", Commit: strings.Repeat("a", 40)}}}, nil
	}
	return protocol.ToolApplyResult{Targets: []protocol.ToolTargetOutcome{{NodeID: toolNodeR, Machine: "research", State: "ok"}, {NodeID: toolNodeM, Machine: "Nicolas-MBP", State: "pending", Reason: "machine offline"}}}, nil
}
func (f *fakeTools) ToolsImport(_ context.Context, imp protocol.ToolImport) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.imports = append(f.imports, imp)
	return nil
}
func (f *fakeTools) ToolsRescan(context.Context) error { f.rescans++; return nil }
func (f *fakeTools) MCPCandidates(context.Context) ([]protocol.MCPImportCandidate, error) {
	return nil, errors.New("no candidates in this fixture")
}
func (f *fakeTools) MCPImportPlan(_ context.Context, c string) (protocol.MCPImportPlan, error) {
	f.mcpPlans = append(f.mcpPlans, c)
	return protocol.MCPImportPlan{ID: "mplan"}, nil
}
func (f *fakeTools) MCPImportApply(_ context.Context, id string) (protocol.MCPImportResult, error) {
	f.mcpApplied = append(f.mcpApplied, id)
	return protocol.MCPImportResult{State: protocol.MCPImportOK}, nil
}
func (f *fakeTools) MCPNativeRemovePlan(context.Context, string) (protocol.MCPNativeRemovePlan, error) {
	return protocol.MCPNativeRemovePlan{ID: "rplan"}, nil
}
func (f *fakeTools) MCPNativeRemoveApply(context.Context, string) (protocol.MCPNativeRemoveResult, error) {
	return protocol.MCPNativeRemoveResult{State: protocol.MCPNativeRemoved}, nil
}
func (f *fakeTools) SendAsOwner(_ context.Context, to, body string) (protocol.MessageSent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, to+": "+body)
	return protocol.MessageSent{Message: &protocol.Message{ID: "m_x", To: to}}, nil
}

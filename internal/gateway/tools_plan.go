package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func (g *Gateway) toolTargets(ctx context.Context, item string, a protocol.ToolAssignment, failed map[string]bool) ([]protocol.ToolPlanTarget, error) {
	var kind, name, hash string
	if err := g.store.db.QueryRow(`SELECT kind,name,hash FROM tool_identities WHERE id=?`, item).Scan(&kind, &name, &hash); err != nil {
		return nil, errors.New("tool item not found")
	}
	if kind == protocol.InventoryMCP && isFederatedHash(hash) {
		if len(a.Machines) > 0 {
			return nil, errors.New("a gateway server runs on its host: it cannot be assigned to chosen machines")
		}
		return g.federatedTargets(name)
	}
	allNodes, err := g.store.toolPlanNodes()
	if err != nil {
		return nil, err
	}
	// A machine the assignment does not name gets no target at all.
	nodes := allNodes[:0:0]
	for _, n := range allNodes {
		if a.OnMachine(n.NodeID) {
			nodes = append(nodes, n)
		}
	}
	projects, err := g.store.Projects("")
	if err != nil {
		return nil, err
	}
	reports, err := g.store.Inventories()
	if err != nil {
		return nil, err
	}
	targets := []protocol.ToolPlanTarget{}
	for _, node := range nodes {
		if failed[node.NodeID] {
			targets = append(targets, protocol.ToolPlanTarget{NodeID: node.NodeID, Machine: node.Hostname, Action: "inspect", State: "conflict", Conflict: "fresh inventory scan unavailable"})
		}
	}
	source := ""
	if kind == protocol.InventorySkill {
		var commit string
		err = g.store.db.QueryRow(`SELECT commit_id FROM skill_versions WHERE item=? AND hash=?`, item, hash).Scan(&commit)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			source = filepath.Join(g.opts.DataDir, "skills.git") + "@" + commit + ":" + skillPath(item)
		}
	}
	if kind == protocol.InventorySkill && source == "" {
		targets = append(targets, protocol.ToolPlanTarget{Action: "import", State: "conflict", Conflict: "skill version has not been imported"})
	} else if kind == protocol.InventorySkill {
		repo, e := g.skillRepository()
		if e != nil {
			return nil, errors.New("skill repository unavailable")
		}
		if _, e = repo.read(item, hash); e != nil {
			targets = append(targets, protocol.ToolPlanTarget{Source: source, Action: "verify", State: "conflict", Conflict: "stored skill version failed verification"})
		}
	}
	if kind != protocol.InventorySkill {
		targets = append(targets, protocol.ToolPlanTarget{Action: "manual", State: "conflict", Conflict: "MCP assignment requires federation support"})
	}
	selected := func(project string) bool {
		for _, v := range a.Projects {
			if v == project {
				return true
			}
		}
		return false
	}
	for _, node := range nodes {
		host := g.conn(node.NodeID)
		before := len(targets)
		if a.Scope == "uninstalled" {
			t := protocol.ToolPlanTarget{NodeID: node.NodeID, Machine: node.Hostname, Action: "verify", State: "pending"}
			if host == nil {
				t.State = "offline"
				t.Conflict = "machine offline; absence cannot be verified"
			}
			targets = append(targets, t)
		}
		if a.Scope == "global" || a.Scope == "projects" {
			checkouts := []protocol.ProjectView{}
			if a.Scope == "global" {
				checkouts = append(checkouts, protocol.ProjectView{})
			} else {
				for _, p := range projects {
					if p.NodeID == node.NodeID && (p.Source == protocol.SourceRegistered || p.Source == protocol.SourceBoth) && selected(p.Name) {
						checkouts = append(checkouts, p)
						for _, wt := range p.Worktrees {
							v := p
							v.Path = wt
							checkouts = append(checkouts, v)
						}
					}
				}
			}
			for _, checkout := range checkouts {
				if len(node.Harnesses) == 0 {
					targets = append(targets, protocol.ToolPlanTarget{NodeID: node.NodeID, Machine: node.Hostname, Project: checkout.Name, Checkout: checkout.Path, Action: "copy", State: "conflict", Conflict: "installed harnesses are unknown"})
					continue
				}
				harnesses := make([]string, 0, len(node.Harnesses))
				for _, h := range node.Harnesses {
					harnesses = append(harnesses, h.Name)
				}
				for _, h := range node.Harnesses {
					probeHarness := h.Name
					projectConflict := false
					if checkout.Path != "" {
						root, conflict := inventory.ProjectSkillRoot(h.Name, harnesses)
						projectConflict = conflict
						switch root {
						case ".agents/skills":
							probeHarness = "codex"
						case ".claude/skills":
							probeHarness = "claude"
						case ".opencode/skills":
							probeHarness = "opencode"
						case ".pi/skills":
							probeHarness = "pi"
						}
					}
					t := protocol.ToolPlanTarget{NodeID: node.NodeID, Machine: node.Hostname, Harness: h.Name, Project: checkout.Name, Checkout: checkout.Path, Source: source, Action: "copy", State: "pending"}
					if projectConflict {
						t.State = "conflict"
						t.Conflict = "installed harnesses have overlapping skill roots"
					} else if kind != protocol.InventorySkill {
						t.Action = "manual"
						t.State = "conflict"
						t.Conflict = "MCP projection is not supported"
					} else if host == nil {
						t.State = "offline"
						t.Conflict = "machine offline; destination cannot be inspected"
					} else {
						raw, e := host.request(ctx, "node.tool_plan_probe", protocol.ToolPlanProbe{Name: name, Harness: probeHarness, Checkout: checkout.Path}, 10*time.Second)
						if e != nil {
							t.State = "conflict"
							t.Conflict = "destination inspection unavailable"
						} else {
							var r protocol.ToolPlanProbeResult
							if json.Unmarshal(raw, &r) != nil {
								t.State = "conflict"
								t.Conflict = "invalid destination inspection"
							} else {
								t.Destination = r.Destination
								t.Fingerprint = r.Fingerprint
								t.Conflict = r.Conflict
								if r.Conflict != "" {
									t.State = "conflict"
								}
							}
						}
					}
					targets = append(targets, t)
				}
			}
		}
		if len(targets) == before {
			t := protocol.ToolPlanTarget{NodeID: node.NodeID, Machine: node.Hostname, Action: "verify", State: "pending"}
			if host == nil {
				t.State = "offline"
				t.Conflict = "machine offline; absence cannot be verified"
			}
			targets = append(targets, t)
		}
	}
	for _, report := range reports {
		if !a.OnMachine(report.NodeID) {
			continue
		}
		if report.State != protocol.InventoryOK {
			targets = append(targets, protocol.ToolPlanTarget{NodeID: report.NodeID, Machine: report.Machine, Action: "inspect", State: "conflict", Conflict: "inventory scan incomplete"})
		}
		for _, obs := range report.Items {
			if obs.Kind == kind && len(obs.Unsupported) > 0 && (obs.Source == protocol.InventoryPlugin || obs.Source == protocol.InventoryPackage || obs.Source == protocol.InventorySystem) {
				targets = append(targets, protocol.ToolPlanTarget{NodeID: report.NodeID, Machine: report.Machine, Harness: obs.Harness, Project: obs.Project, Checkout: obs.ProjectRoot, Source: obs.Location, Action: "inspect", State: "conflict", Conflict: "opaque source may shadow this item"})
			}

			if obs.Kind != kind || obs.Name != name {
				continue
			}
			if obs.Hash != hash {
				targets = append(targets, protocol.ToolPlanTarget{NodeID: report.NodeID, Machine: report.Machine, Harness: obs.Harness, Project: obs.Project, Checkout: obs.ProjectRoot, Source: obs.Location, Action: "inspect", State: "conflict", Conflict: "same name has another version that may shadow this item"})
				continue
			}
			remove := a.Scope == "uninstalled" || (a.Scope == "projects" && (obs.Scope != protocol.InventoryProject || !selected(obs.Project))) || (a.Scope == "global" && obs.Scope == protocol.InventoryProject)
			if !remove {
				continue
			}
			t := protocol.ToolPlanTarget{NodeID: report.NodeID, Machine: report.Machine, Harness: obs.Harness, Project: obs.Project, Checkout: obs.ProjectRoot, Source: obs.Location, Action: "remove", State: "conflict", Conflict: "unmanaged source requires guarded removal"}
			if kind != protocol.InventorySkill {
				t.Action = "manual"
				t.Conflict = "native MCP removal is not supported"
			} else if host := g.conn(report.NodeID); host == nil {
				t.State = "offline"
				t.Conflict = "machine offline; source cannot be inspected"
			} else {
				raw, e := host.request(ctx, "node.tool_plan_probe", protocol.ToolPlanProbe{Name: name, Harness: obs.Harness, Source: obs.Location, Hash: hash}, 10*time.Second)
				if e != nil {
					t.Conflict = "source inspection unavailable"
				} else {
					var r protocol.ToolPlanProbeResult
					if json.Unmarshal(raw, &r) != nil {
						t.Conflict = "invalid source inspection"
					} else if r.Conflict != "" {
						t.Conflict = r.Conflict
					}
				}
			}
			targets = append(targets, t)
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		a, b := targets[i], targets[j]
		return a.NodeID+"\x00"+a.Harness+"\x00"+a.Checkout+"\x00"+a.Source+"\x00"+a.Action < b.NodeID+"\x00"+b.Harness+"\x00"+b.Checkout+"\x00"+b.Source+"\x00"+b.Action
	})
	return targets, nil
}
func (g *Gateway) planTool(ctx context.Context, p protocol.ToolPlanRequest, actor string) (out protocol.ToolPlan, err error) {
	defer func() {
		if err != nil && (actor == "owner" || actor == "cto") {
			if e := g.store.recordRefusedToolPlan(p.Item, actor); e != nil {
				err = e
			}
		}
	}()
	a, err := normalizeAssignment(p.Target)
	if err != nil {
		return out, err
	}
	if a.Machines, err = g.store.resolveMachines(a.Machines); err != nil {
		return out, err
	}
	a, err = normalizeAssignment(a)
	if err != nil {
		return out, err
	}
	p.Target = a
	failed, err := g.refreshToolObservations(ctx)
	if err != nil {
		return out, err
	}
	snap, err := g.store.ToolSnapshot()
	if err != nil {
		return out, err
	}
	var targets []protocol.ToolPlanTarget
	switch p.Operation {
	case "":
		targets, err = g.toolTargets(ctx, p.Item, a, failed)
	case "repo_remove":
		targets, err = g.repoRemovalTargets(ctx, p)
	default:
		return out, errors.New("unsupported tools operation")
	}
	if err != nil {
		return out, err
	}
	return g.store.saveToolPlan(p.Item, a, targets, actor, snap, p.Operation, p.RepoTargets)
}

func (g *Gateway) repoRemovalTargets(ctx context.Context, p protocol.ToolPlanRequest) ([]protocol.ToolPlanTarget, error) {
	var kind, name string
	if err := g.store.db.QueryRow(`SELECT kind,name FROM tool_identities WHERE id=?`, p.Item).Scan(&kind, &name); err != nil {
		return nil, errors.New("tool item not found")
	}
	if kind != protocol.InventorySkill {
		return nil, errors.New("repo removal requires a skill")
	}
	if p.Project == "" || len(p.RepoTargets) == 0 || len(p.RepoTargets) > 200 || p.Target.Scope != "projects" || len(p.Target.Projects) != 1 || p.Target.Projects[0] != p.Project {
		return nil, errors.New("repo removal needs one project checkout")
	}
	projects, err := g.store.Projects(p.Project)
	if err != nil {
		return nil, err
	}
	registered := map[string]protocol.ProjectView{}
	for _, project := range projects {
		if project.Name == p.Project && (project.Source == protocol.SourceRegistered || project.Source == protocol.SourceBoth) {
			registered[project.NodeID+"\x00"+filepath.Clean(project.Path)] = project
		}
	}
	items, err := g.store.ToolsItems(protocol.ToolsFilter{Kind: protocol.InventorySkill}, func(string) bool { return true })
	if err != nil {
		return nil, err
	}
	tracked := map[string]bool{}
	for _, item := range items {
		if item.ID != p.Item {
			continue
		}
		for _, location := range item.Locations {
			if location.Scope == protocol.InventoryProject && location.Tracked && location.Project == p.Project && location.ProjectRoot != "" {
				tracked[location.NodeID+"\x00"+filepath.Clean(location.ProjectRoot)] = true
			}
		}
	}
	branch := "agent-net/remove-" + name
	seen := map[string]bool{}
	targets := make([]protocol.ToolPlanTarget, 0, len(p.RepoTargets))
	for _, repo := range p.RepoTargets {
		key := repo.NodeID + "\x00" + filepath.Clean(repo.Checkout)
		if repo.NodeID == "" || repo.Checkout == "" || seen[key] {
			return nil, errors.New("invalid repo checkout list")
		}
		seen[key] = true
		project, ok := registered[key]
		if !ok {
			return nil, errors.New("project checkout is no longer registered")
		}
		if !tracked[key] {
			return nil, errors.New("repo removal requires a tracked project copy")
		}
		t := protocol.ToolPlanTarget{NodeID: project.NodeID, Machine: project.Hostname, Project: project.Name, Checkout: project.Path, Branch: branch,
			DefaultBranch: project.DefaultBranch, Action: "repo_remove", State: "pending"}
		host := g.conn(project.NodeID)
		if host == nil {
			t.State, t.Conflict = "offline", "machine offline; project cannot be inspected"
			targets = append(targets, t)
			continue
		}
		raw, probeErr := host.request(ctx, "node.tool_plan_probe", protocol.ToolPlanProbe{Name: name, Operation: "repo_remove", Project: project.Name, Checkout: project.Path, DefaultBranch: project.DefaultBranch, Branch: branch}, 20*time.Second)
		if probeErr != nil {
			t.State, t.Conflict = "conflict", "project inspection unavailable"
		} else {
			var result protocol.ToolPlanProbeResult
			if json.Unmarshal(raw, &result) != nil {
				t.State, t.Conflict = "conflict", "invalid project inspection"
			} else {
				t.BaseCommit, t.Files, t.Dirty, t.Conflict = result.BaseCommit, result.Files, result.Dirty, result.Conflict
				if t.Conflict != "" {
					t.State = "conflict"
				}
			}
		}
		targets = append(targets, t)
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].NodeID+"\x00"+targets[i].Checkout < targets[j].NodeID+"\x00"+targets[j].Checkout
	})
	return targets, nil
}

func firstRepoProject(targets []protocol.ToolPlanTarget) string {
	if len(targets) == 0 {
		return ""
	}
	return targets[0].Project
}

func (g *Gateway) applyTool(ctx context.Context, id, actor string) (out protocol.ToolApplyResult, err error) {
	var r protocol.ToolRevision
	checked := false
	defer func() {
		if err != nil && !checked && (actor == "owner" || actor == "cto") {
			if e := g.store.recordRefusedToolApply(id, actor); e != nil {
				err = e
			}
		}
	}()
	var raw, snapshot string
	if err = g.store.db.QueryRow(`SELECT plan,snapshot FROM tool_plans WHERE id=?`, id).Scan(&raw, &snapshot); err != nil {
		return out, errors.New("plan not found")
	}
	failed, e := g.refreshToolObservations(ctx)
	if e != nil {
		return out, e
	}
	current, e := g.store.ToolSnapshot()
	if e != nil {
		return out, e
	}
	if snapshot != current {
		return out, errors.New("plan out of date")
	}
	var p protocol.ToolPlan
	if json.Unmarshal([]byte(raw), &p) != nil {
		return out, errors.New("invalid plan")
	}
	var targets []protocol.ToolPlanTarget
	if p.Operation == "repo_remove" {
		targets, e = g.repoRemovalTargets(ctx, protocol.ToolPlanRequest{Item: p.Item, Target: p.Assignment, Operation: p.Operation, Project: firstRepoProject(p.Targets), RepoTargets: p.RepoTargets})
	} else {
		targets, e = g.toolTargets(ctx, p.Item, p.Assignment, failed)
	}
	if e != nil {
		if e.Error() == "tool item not found" {
			return out, errors.New("plan out of date")
		}
		return out, e
	}
	if p.Operation == "repo_remove" {
		for _, target := range targets {
			if target.Conflict == "branch exists" {
				return out, errors.New("branch exists")
			}
		}
	}
	if !reflect.DeepEqual(targets, p.Targets) {
		return out, errors.New("plan out of date")
	}
	checked = true
	r, err = g.store.ApplyToolPlan(id, actor)
	if err == nil {
		g.notifyToolNodes(ctx, p.Targets)
		g.mcpScopeChanged(ctx, p.Item)
		out = g.toolOutcome(ctx, r, p.Targets)
	}
	return out, err
}

// toolOutcome builds the per-target answer. It waits briefly for connected
// machines to report, so a fast local apply shows its real result. Machines
// that are offline or slow stay pending.
func (g *Gateway) toolOutcome(ctx context.Context, r protocol.ToolRevision, targets []protocol.ToolPlanTarget) protocol.ToolApplyResult {
	deadline := time.Now().Add(toolOutcomeWait)
	for {
		out, waiting := g.store.toolOutcomes(r, targets, func(node string) bool { return g.conn(node) != nil })
		if !waiting || time.Now().After(deadline) || ctx.Err() != nil {
			return out
		}
		select {
		case <-ctx.Done():
			return out
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// toolOutcomeWait bounds how long apply and undo wait for machine results.
const toolOutcomeWait = 3 * time.Second

// notifyToolNodes wakes every connected node that owns a target.
func (g *Gateway) notifyToolNodes(ctx context.Context, targets []protocol.ToolPlanTarget) {
	frame, e := protocol.EncodeRequest("node.tools_changed", protocol.NewID("d"), struct{}{})
	if e != nil {
		return
	}
	seen := map[string]bool{}
	for _, t := range targets {
		if !seen[t.NodeID] {
			seen[t.NodeID] = true
			if c := g.conn(t.NodeID); c != nil {
				_ = c.write(ctx, frame)
			}
		}
	}
}

// undoTool reverses the item's newest revision as a new audited revision.
func (g *Gateway) undoTool(ctx context.Context, item, actor string) (protocol.ToolApplyResult, error) {
	r, p, err := g.store.UndoTool(item, actor)
	if err != nil {
		return protocol.ToolApplyResult{}, err
	}
	g.notifyToolNodes(ctx, p.Targets)
	g.mcpScopeChanged(ctx, item)
	return g.toolOutcome(ctx, r, p.Targets), nil
}

func (g *Gateway) validToolOwner(nc *nodeConn, id string) bool {
	if id == "" {
		return false
	}
	a, ok, err := g.store.Agent(id)
	return err == nil && ok && a.NodeID == nc.id && a.Harness == protocol.OwnerHarness && a.Online
}

// resolveMachines turns machine names or node IDs into node IDs. A name must
// match one enrolled machine; an ID is used as it is.
func (s *Store) resolveMachines(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	nodes, err := s.toolPlanNodes()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(names))
	for _, want := range names {
		var found []string
		for _, n := range nodes {
			if n.NodeID == want {
				found = []string{n.NodeID}
				break
			}
			if n.Hostname == want || strings.EqualFold(n.Hostname, want) {
				found = append(found, n.NodeID)
			}
		}
		switch len(found) {
		case 1:
			out = append(out, found[0])
		case 0:
			return nil, errors.New("machine " + want + " is not enrolled")
		default:
			return nil, errors.New("machine name " + want + " matches more than one machine: use its ID")
		}
	}
	return out, nil
}

// toolPlanNodes also includes enrolled nodes that have never reported a hello.
func (s *Store) toolPlanNodes() ([]protocol.NodeView, error) {
	rows, err := s.db.Query(`SELECT id,hostname,harnesses FROM nodes ORDER BY hostname,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := []protocol.NodeView{}
	for rows.Next() {
		var n protocol.NodeView
		var harnesses string
		if err = rows.Scan(&n.NodeID, &n.Hostname, &harnesses); err != nil {
			return nil, err
		}
		n.Harnesses = decodeHarnesses(harnesses)
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

func (g *Gateway) refreshToolObservations(ctx context.Context) (map[string]bool, error) {
	nodes, err := g.store.toolPlanNodes()
	if err != nil {
		return nil, err
	}
	failed := map[string]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for _, node := range nodes {
		host := g.conn(node.NodeID)
		if host == nil {
			continue
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				failed[id] = true
				mu.Unlock()
				return
			}
			defer func() { <-sem }()
			if _, err := host.request(ctx, "node.tool_plan_scan", struct{}{}, 30*time.Second); err != nil {
				mu.Lock()
				failed[id] = true
				mu.Unlock()
			}
		}(node.NodeID)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return failed, nil
}

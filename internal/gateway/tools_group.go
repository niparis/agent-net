package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

type skillGroupKey struct{}

// Prefer this checkout's copy, then this machine's user copy, then the
// newest library version. The owner chooses projects, never machines.
func (g *Gateway) skillCopyIdentity(id, node, project string) bool {
	items, err := g.store.ToolsItems(protocol.ToolsFilter{Kind: protocol.InventorySkill}, func(string) bool { return true })
	if err != nil {
		return false
	}
	name := ""
	for _, it := range items {
		if it.ID == id {
			name = it.Name
		}
	}
	best := ""
	score := -1
	version := -1
	for _, it := range items {
		if it.Name != name {
			continue
		}
		rank := 0
		for _, l := range it.Locations {
			if l.NodeID == node {
				rank = max(rank, 1)
				if l.Scope == protocol.InventoryUser {
					rank = max(rank, 2)
				}
				if l.Scope == protocol.InventoryProject && l.Project == project {
					rank = 3
				}
			}
		}
		if rank > score || rank == score && (it.VersionNo > version || it.VersionNo == version && it.ID > best) {
			best = it.ID
			score = rank
			version = it.VersionNo
		}
	}
	return best == id
}

func (g *Gateway) toggleSkillGroup(ctx context.Context, p protocol.ToolToggleRequest, actor string) (protocol.ToolPlan, bool, error) {
	items, err := g.store.ToolsItems(protocol.ToolsFilter{Kind: protocol.InventorySkill}, func(string) bool { return true })
	if err != nil {
		return protocol.ToolPlan{}, true, err
	}
	name := ""
	for _, it := range items {
		if it.ID == p.Item {
			name = it.Name
		}
	}
	var copies []protocol.ToolItem
	for _, it := range items {
		if it.Name == name {
			copies = append(copies, it)
		}
	}
	if len(copies) < 2 {
		return protocol.ToolPlan{}, false, nil
	}
	ctx = context.WithValue(ctx, skillGroupKey{}, true)
	group := protocol.ToolPlan{ToggleProject: p.Project, ToggleOn: p.On, ID: protocol.NewID("plan"), Item: p.Item, Operation: "skill_group", Actor: actor, State: "pending", At: g.store.now().UnixNano()}
	projects, err := g.store.Projects("")
	if err != nil {
		return group, true, err
	}
	// Import all versions before taking any snapshots or choosing fallbacks.
	for _, it := range copies {
		if err = g.ensureSkillImported(ctx, it.ID); err != nil {
			return group, true, err
		}
	}
	// Differing project versions cannot share one global copy on that node.
	mixedProjects := false
	for _, it := range copies {
		for _, l := range it.Locations {
			if l.Scope == protocol.InventoryProject {
				mixedProjects = true
			}
		}
	}
	states, err := g.store.toolProjectStates(validatingProposal(ctx))
	if err != nil {
		return group, true, err
	}
	// Every identity retains its own assignment, snapshot and daemon revision.
	for _, it := range copies {
		a, e := g.store.toolToggleAssignment(protocol.ToolToggleRequest{Item: it.ID, Project: p.Project, On: p.On}, validatingProposal(ctx))
		if e != nil {
			return group, true, e
		}
		if mixedProjects && a.Scope == "global" {
			a.Scope = "projects"
			for _, row := range states {
				if row.Item == it.ID {
					for name := range row.Projects {
						a.Projects = append(a.Projects, name)
					}
				}
			}
			if len(a.Projects) == 0 {
				a.Scope = "uninstalled"
			}
		}
		machines := map[string]bool{}
		for _, l := range it.Locations {
			machines[l.NodeID] = true
		}
		for _, checkout := range projects {
			if p.On && checkout.Name == p.Project && g.skillCopyIdentity(it.ID, checkout.NodeID, p.Project) {
				machines[checkout.NodeID] = true
			}
		}
		a.Machines = nil
		for n := range machines {
			a.Machines = append(a.Machines, n)
		}
		// An identity without any destination still needs a revision removing its copies.
		if len(machines) == 0 {
			continue
		}
		member, e := g.planSkillAssignment(ctx, it.ID, a, actor)
		if e != nil {
			return group, true, e
		}
		previous := observedToolAssignment(it)
		member.Previous = &previous
		if !validatingProposal(ctx) {
			raw, _ := json.Marshal(member)
			if _, e = g.store.db.Exec(`UPDATE tool_plans SET plan=? WHERE id=?`, string(raw), member.ID); e != nil {
				return group, true, e
			}
		}
		group.Members = append(group.Members, member)
		group.Targets = append(group.Targets, member.Targets...)
		group.Skipped = append(group.Skipped, member.Skipped...)
		group.Conflicts = append(group.Conflicts, member.Conflicts...)
	}
	if !validatingProposal(ctx) {
		raw, e := json.Marshal(group)
		if e != nil {
			return group, true, e
		}
		if len(raw) > 1<<20 {
			return group, true, errors.New("plan exceeds limit")
		}
		_, err = g.store.db.Exec(`INSERT INTO tool_plans(id,item,snapshot,plan) VALUES(?,?,?,?)`, group.ID, group.Item, "", string(raw))
	}
	return group, true, err
}

func (g *Gateway) skillGroup(id string) (protocol.ToolPlan, bool, error) {
	var raw string
	err := g.store.db.QueryRow(`SELECT plan FROM tool_plans WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.ToolPlan{}, false, nil
	}
	if err != nil {
		return protocol.ToolPlan{}, false, err
	}
	var p protocol.ToolPlan
	err = json.Unmarshal([]byte(raw), &p)
	return p, p.Operation == "skill_group", err
}

func (g *Gateway) applySkillGroup(ctx context.Context, id, actor string) (protocol.ToolApplyResult, bool, error) {
	p, ok, err := g.skillGroup(id)
	if !ok || err != nil {
		return protocol.ToolApplyResult{}, ok, err
	}
	if !validChangeActor(actor) {
		return protocol.ToolApplyResult{}, true, errors.New("owner authority required")
	}
	if failed, e := g.refreshToolObservations(ctx); e != nil {
		return protocol.ToolApplyResult{}, true, e
	} else if len(failed) > 0 {
		return protocol.ToolApplyResult{}, true, errors.New("fresh inventory scan unavailable")
	}
	// A newly discovered content identity also belongs to this name. Never
	// apply an older group that omitted it.
	items, e := g.store.ToolsItems(protocol.ToolsFilter{Kind: protocol.InventorySkill}, func(string) bool { return true })
	if e != nil {
		return protocol.ToolApplyResult{}, true, e
	}
	members := map[string]bool{}
	name := ""
	for _, m := range p.Members {
		members[m.Item] = true
	}
	for _, it := range items {
		if it.ID == p.Item {
			name = it.Name
		}
	}
	for _, it := range items {
		if it.Name == name && !members[it.ID] && len(it.Locations) > 0 {
			return protocol.ToolApplyResult{}, true, errors.New("plan out of date")
		}
	}
	// Check every member before recording any revision. No daemon is woken until
	// all revisions are saved, so one member cannot invalidate another's snapshot.
	preview := context.WithValue(ctx, proposalValidationKey{}, true)
	for _, m := range p.Members {
		if _, err = g.applyTool(preview, m.ID, actor); err != nil {
			return protocol.ToolApplyResult{}, true, err
		}
	}
	if validatingProposal(ctx) {
		return protocol.ToolApplyResult{}, true, nil
	}
	var out protocol.ToolApplyResult
	var revisions []protocol.ToolRevision
	tx, err := g.store.db.Begin()
	if err != nil {
		return out, true, err
	}
	defer tx.Rollback()
	for _, m := range p.Members {
		r, e := g.store.applyToolPlanTx(tx, m.ID, actor)
		if e != nil {
			return out, true, e
		}
		revisions = append(revisions, r)
	}
	_, err = tx.Exec(`UPDATE tool_plans SET applied=1 WHERE id=?`, p.ID)
	if err != nil {
		return out, true, err
	}
	if err = tx.Commit(); err != nil {
		return out, true, err
	}
	g.notifyToolNodes(ctx, p.Targets)
	for i, m := range p.Members {
		r := g.toolOutcome(ctx, revisions[i], m.Targets)
		out.ToolRevision = r.ToolRevision
		out.Targets = append(out.Targets, r.Targets...)
	}
	return out, true, nil
}

func (g *Gateway) undoSkillGroup(ctx context.Context, id, actor string) (protocol.ToolApplyResult, bool, error) {
	p, ok, err := g.skillGroup(id)
	if err != nil {
		return protocol.ToolApplyResult{}, ok, err
	}
	if !ok {
		var raw string
		e := g.store.db.QueryRow(`SELECT plan FROM tool_plans WHERE item=? AND applied=1 AND json_extract(plan,'$.operation')='skill_group' ORDER BY rowid DESC LIMIT 1`, id).Scan(&raw)
		if errors.Is(e, sql.ErrNoRows) {
			return protocol.ToolApplyResult{}, false, nil
		}
		if e != nil {
			return protocol.ToolApplyResult{}, true, e
		}
		if e = json.Unmarshal([]byte(raw), &p); e != nil {
			return protocol.ToolApplyResult{}, true, e
		}
	}
	if !validChangeActor(actor) {
		return protocol.ToolApplyResult{}, true, errors.New("owner authority required")
	}
	for _, m := range p.Members {
		var source string
		err = g.store.db.QueryRow(`SELECT source FROM tool_revisions WHERE item=? ORDER BY revision DESC LIMIT 1`, m.Item).Scan(&source)
		if err != nil || source != "plan:"+m.ID {
			return protocol.ToolApplyResult{}, true, errors.New("group is no longer the newest change")
		}
		if _, _, err = g.store.UndoTool(m.Item, actor, true); err != nil {
			return protocol.ToolApplyResult{}, true, err
		}
	}
	if validatingProposal(ctx) {
		return protocol.ToolApplyResult{}, true, nil
	}
	var out protocol.ToolApplyResult
	for _, m := range p.Members {
		r, e := g.undoSingleTool(ctx, m.Item, actor)
		if e != nil {
			return out, true, e
		}
		out.ToolRevision = r.ToolRevision
		out.Targets = append(out.Targets, r.Targets...)
	}
	return out, true, nil
}

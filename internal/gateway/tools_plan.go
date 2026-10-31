package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func (g *Gateway) toolTargets(ctx context.Context, item string, a protocol.ToolAssignment) ([]protocol.ToolPlanTarget, error) {
	var kind, name, hash string
	if err := g.store.db.QueryRow(`SELECT kind,name,hash FROM tool_identities WHERE id=?`, item).Scan(&kind, &name, &hash); err != nil {
		return nil, errors.New("tool item not found")
	}
	nodes, err := g.store.Nodes()
	if err != nil {
		return nil, err
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
	var imported int
	if kind == protocol.InventorySkill {
		if err = g.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM skill_versions WHERE item=? AND hash=?)`, item, hash).Scan(&imported); err != nil {
			return nil, err
		}
	}
	if kind == protocol.InventorySkill && imported == 0 {
		targets = append(targets, protocol.ToolPlanTarget{Action: "import", State: "conflict", Conflict: "skill version has not been imported"})
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
				for _, h := range node.Harnesses {
					t := protocol.ToolPlanTarget{NodeID: node.NodeID, Machine: node.Hostname, Harness: h.Name, Project: checkout.Name, Checkout: checkout.Path, Source: "gateway version " + hash, Action: "copy", State: "pending"}
					if kind != protocol.InventorySkill {
						t.Action = "manual"
						t.State = "conflict"
						t.Conflict = "MCP projection is not supported"
					} else if host == nil {
						t.State = "offline"
						t.Conflict = "machine offline; destination cannot be inspected"
					} else {
						raw, e := host.request(ctx, "node.tool_plan_probe", protocol.ToolPlanProbe{Name: name, Harness: h.Name, Checkout: checkout.Path}, 10*time.Second)
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
	}
	for _, report := range reports {
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
	snap, err := g.store.ToolSnapshot()
	if err != nil {
		return out, err
	}
	targets, err := g.toolTargets(ctx, p.Item, a)
	if err != nil {
		return out, err
	}
	return g.store.saveToolPlan(p.Item, a, targets, actor, snap)
}

func (g *Gateway) applyTool(ctx context.Context, id, actor string) (r protocol.ToolRevision, err error) {
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
		return r, errors.New("plan not found")
	}
	current, e := g.store.ToolSnapshot()
	if e != nil {
		return r, e
	}
	if snapshot != current {
		return r, errors.New("plan out of date")
	}
	var p protocol.ToolPlan
	if json.Unmarshal([]byte(raw), &p) != nil {
		return r, errors.New("invalid plan")
	}
	targets, e := g.toolTargets(ctx, p.Item, p.Assignment)
	if e != nil {
		if e.Error() == "tool item not found" {
			return r, errors.New("plan out of date")
		}
		return r, e
	}
	if !reflect.DeepEqual(targets, p.Targets) {
		return r, errors.New("plan out of date")
	}
	checked = true
	return g.store.ApplyToolPlan(id, actor)
}

func (g *Gateway) validToolOwner(nc *nodeConn, id string) bool {
	if id == "" {
		return false
	}
	a, ok, err := g.store.Agent(id)
	return err == nil && ok && a.NodeID == nc.id && a.Harness == protocol.OwnerHarness && a.Online
}

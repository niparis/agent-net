package gateway

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// skillUpdatePlan lists every copy of a skill name and says what an update to
// the stored version `item` would do with it. It writes nothing. A copy that
// was edited after it was deployed ("changed here") is always kept: it must be
// saved as a version first.
func (g *Gateway) skillUpdatePlan(item string) (protocol.ToolUpdatePlan, error) {
	plan := protocol.ToolUpdatePlan{Item: item, Copies: []protocol.ToolUpdateCopy{}}
	var kind string
	if err := g.store.db.QueryRow(`SELECT kind,name,hash FROM tool_identities WHERE id=?`, item).Scan(&kind, &plan.Name, &plan.Hash); err != nil || kind != protocol.InventorySkill {
		return plan, reject("update needs a stored skill version")
	}
	if err := g.store.db.QueryRow(`SELECT number FROM skill_versions WHERE item=? AND hash=?`, item, plan.Hash).Scan(&plan.No); err != nil || plan.No == 0 {
		return plan, reject("that skill version is not in the store: save it first")
	}
	items, err := g.store.ToolsItems(protocol.ToolsFilter{Kind: protocol.InventorySkill}, func(string) bool { return true })
	if err != nil {
		return plan, err
	}
	// One folder can be read by several harnesses and so appear twice.
	listed := map[string]bool{}
	for _, it := range items {
		if it.Name != plan.Name || it.ID == item {
			continue
		}
		for _, l := range it.Locations {
			key := l.NodeID + "\x00" + l.Location
			if listed[key] {
				continue
			}
			listed[key] = true
			if l.State != protocol.InventoryOK || l.Source != protocol.InventoryNative || l.Tracked {
				continue
			}
			c := protocol.ToolUpdateCopy{NodeID: l.NodeID, Machine: l.Machine, Location: l.Location, Project: l.Project, Harness: l.Harness, FromNo: it.VersionNo, FromItem: it.ID, FromHash: it.Hash}
			switch {
			case l.ChangedHere:
				c.State, c.Reason = protocol.UpdateKept, "changed here: save it as a version first"
			case it.VersionNo == 0:
				c.State, c.Reason = protocol.UpdateKept, "content is not a stored version"
			case it.VersionNo > plan.No:
				continue
			case g.conn(l.NodeID) == nil:
				c.State, c.Reason = protocol.UpdateKept, "machine offline"
			default:
				c.State = protocol.UpdateWillReplace
			}
			plan.Copies = append(plan.Copies, c)
		}
	}
	sort.Slice(plan.Copies, func(i, j int) bool {
		a, b := plan.Copies[i], plan.Copies[j]
		return a.Machine+"\x00"+a.Location < b.Machine+"\x00"+b.Location
	})
	return plan, nil
}

// updateSkillCopies replaces exactly the copies the owner confirmed. It plans
// again first and refuses the whole request when a confirmed copy is no longer
// a plain update, so nothing changes under a stale plan.
func (g *Gateway) updateSkillCopies(ctx context.Context, p protocol.ToolUpdateRequest) (any, error) {
	if len(p.Copies) == 0 || len(p.Copies) > 200 {
		return nil, reject("name between 1 and 200 copies to update")
	}
	plan, err := g.skillUpdatePlan(p.Item)
	if err != nil {
		return nil, err
	}
	chosen := []protocol.ToolUpdateCopy{}
	seen := map[string]bool{}
	for _, want := range p.Copies {
		key := want.NodeID + "\x00" + want.Location
		if seen[key] {
			continue
		}
		seen[key] = true
		found := false
		for _, c := range plan.Copies {
			if c.NodeID == want.NodeID && c.Location == want.Location {
				if c.State != protocol.UpdateWillReplace {
					return nil, reject("%s on %s is not updated: %s", c.Location, c.Machine, c.Reason)
				}
				chosen = append(chosen, c)
				found = true
			}
		}
		if !found {
			return nil, reject("%s is not an older copy of this skill any more; plan again", want.Location)
		}
	}
	bundle, err := g.exportSkill(p.Item, plan.Hash)
	if err != nil {
		return nil, err
	}
	out := plan
	out.Copies = nil
	for _, c := range chosen {
		host := g.conn(c.NodeID)
		if host == nil {
			c.State, c.Reason = protocol.UpdateFailed, "machine went offline"
			out.Copies = append(out.Copies, c)
			continue
		}
		raw, err := host.request(ctx, "node.skill_update_copy", protocol.ToolUpdateCopyRequest{Name: plan.Name, Location: c.Location, FromItem: c.FromItem, FromHash: c.FromHash, ToItem: p.Item, ToHash: plan.Hash, Data: bundle.Data}, 90*time.Second)
		var result protocol.ToolResult
		switch {
		case err != nil:
			c.State, c.Reason = protocol.UpdateFailed, err.Error()
		case json.Unmarshal(raw, &result) != nil:
			c.State, c.Reason = protocol.UpdateFailed, "invalid answer from the machine"
		case result.State == "applied":
			c.State, c.Reason = protocol.UpdateApplied, ""
		default:
			c.State, c.Reason = protocol.UpdateFailed, result.Reason
		}
		out.Copies = append(out.Copies, c)
	}
	return out, nil
}

// saveChangedCopy saves a copy that was edited after deployment as the skill's
// next stored version. It is the same import as tools_import, with the same
// size limit, hash check and provenance, but it refuses a copy that is not
// marked changed here.
func (g *Gateway) saveChangedCopy(ctx context.Context, p protocol.ToolSaveChanged) (any, error) {
	var obs string
	if err := g.store.db.QueryRow(`SELECT l.observation FROM tool_locations l WHERE l.identity_id=? AND l.node_id=? AND json_extract(l.observation,'$.location')=? LIMIT 1`, p.Item, p.Location.NodeID, p.Location.Location).Scan(&obs); err != nil {
		return nil, reject("skill location is not a current observation")
	}
	var seen protocol.InventoryItem
	if json.Unmarshal([]byte(obs), &seen) != nil || !seen.ChangedHere {
		return nil, reject("that copy is not changed here: nothing to save")
	}
	return g.importSkill(ctx, p.ToolImport)
}

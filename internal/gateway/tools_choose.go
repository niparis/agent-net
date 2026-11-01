package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func (g *Gateway) keepSkillCopy(ctx context.Context, p protocol.ToolCopyChoice) (any, error) {
	if len(p.Copies) == 0 || len(p.Copies) > 200 {
		return nil, reject("choose between 1 and 200 observed copies")
	}
	var name string
	if err := g.store.db.QueryRow(`SELECT name FROM tool_identities WHERE id=? AND kind='skill'`, p.Chosen.Item).Scan(&name); err != nil {
		return nil, reject("chosen skill is unknown")
	}
	// Validate every named source before importing or removing anything. The
	// host repeats the hash and ownership checks at the moment of removal.
	for _, c := range p.Copies {
		if c.Name != name {
			return nil, reject("copies must have the same name")
		}
		var observation string
		if err := g.store.db.QueryRow(`SELECT l.observation FROM tool_locations l JOIN tool_identities i ON i.id=l.identity_id WHERE i.kind='skill' AND i.name=? AND i.hash=? AND l.node_id=? AND json_extract(l.observation,'$.location')=? LIMIT 1`, c.Name, c.Hash, c.NodeID, c.Location).Scan(&observation); err != nil {
			return nil, reject("copy is no longer observed")
		}
		var obs protocol.InventoryItem
		if json.Unmarshal([]byte(observation), &obs) != nil || obs.State != protocol.InventoryOK || obs.Tracked || obs.Source != protocol.InventoryNative || obs.Scope != protocol.InventoryUser {
			return nil, reject("only native, untracked user copies can be replaced")
		}
		if g.conn(c.NodeID) == nil {
			return nil, reject("copy machine is offline")
		}
	}
	version, err := g.importSkill(ctx, p.Chosen)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, c := range p.Copies {
		key := c.NodeID + "\x00" + c.Location
		if seen[key] {
			continue
		}
		seen[key] = true
		if !p.RemoveAll && c.NodeID == p.Chosen.Location.NodeID && c.Location == p.Chosen.Location.Location {
			continue
		}
		host := g.conn(c.NodeID)
		if host == nil {
			return nil, reject("copy machine went offline")
		}
		raw, err := host.request(ctx, "node.skill_remove_copy", c, 60*time.Second)
		if err != nil {
			return nil, err
		}
		var result protocol.ToolResult
		if json.Unmarshal(raw, &result) != nil || result.State != "applied" {
			return nil, errors.New("copy removal refused: " + result.Reason)
		}
	}
	return version, nil
}

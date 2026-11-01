package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// toolSkillDetails asks one connected machine to read the file manifest of one
// observed skill and stores it in the inventory, so the next tools_items page
// carries the details.
func (g *Gateway) toolSkillDetails(ctx context.Context, p protocol.ToolSkillScan) (protocol.ToolSkillDetails, error) {
	if p.NodeID == "" || p.Name == "" || p.Location == "" {
		return protocol.ToolSkillDetails{}, errors.New("skill scan needs a machine, name and location")
	}
	host := g.conn(p.NodeID)
	if host == nil {
		name := g.store.hostname(p.NodeID)
		if name == "" {
			name = p.NodeID
		}
		return protocol.ToolSkillDetails{}, errors.New(name + " is offline")
	}
	raw, err := host.request(ctx, "node.tool_skill_scan", p, 15*time.Second)
	if err != nil {
		return protocol.ToolSkillDetails{}, err
	}
	var details protocol.ToolSkillDetails
	if err = json.Unmarshal(raw, &details); err != nil {
		return protocol.ToolSkillDetails{}, errors.New("invalid skill details")
	}
	if details.Manifest == nil {
		return details, nil
	}
	if err = g.store.SetInventoryItemManifest(p.NodeID, p.Location, details.Manifest, details.ScannedAt); err != nil {
		return protocol.ToolSkillDetails{}, err
	}
	return details, nil
}

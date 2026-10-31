package gateway

import (
	"encoding/json"

	"github.com/niparis/agent-net/internal/protocol"
)

// ToolTargets returns only the newest applied plan's targets for this node.
func (s *Store) ToolTargets(node string) ([]protocol.ToolTarget, error) {
	rows, err := s.db.Query(`SELECT r.item,r.revision,r.assignment,r.version,r.actor,r.source,r.at,i.name,p.plan FROM tool_revisions r JOIN tool_identities i ON i.id=r.item JOIN tool_plans p ON r.source='plan:' || p.id AND p.applied=1 WHERE r.revision=(SELECT MAX(x.revision) FROM tool_revisions x WHERE x.item=r.item) ORDER BY r.item`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.ToolTarget{}
	for rows.Next() {
		var r protocol.ToolRevision
		var name, assignment, plan string
		if err = rows.Scan(&r.Item, &r.Revision, &assignment, &r.Version, &r.Actor, &r.Source, &r.At, &name, &plan); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(assignment), &r.Assignment); err != nil {
			return nil, err
		}
		r.State = "pending"
		var p protocol.ToolPlan
		if err = json.Unmarshal([]byte(plan), &p); err != nil {
			return nil, err
		}
		for i, t := range p.Targets {
			if t.NodeID == node {
				out = append(out, protocol.ToolTarget{Revision: r, Name: name, Index: i, Target: t})
			}
		}
	}
	return out, rows.Err()
}

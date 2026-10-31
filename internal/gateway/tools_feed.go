package gateway

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// ToolTargets returns only the newest applied plan's targets for this node.
func (s *Store) ToolTargets(node string) ([]protocol.ToolTarget, error) {
	rows, err := s.db.Query(`SELECT r.item,r.revision,r.assignment,r.version,r.actor,r.source,r.at,i.name,p.plan,COALESCE((SELECT group_concat(target_index,',') FROM tool_results WHERE item=r.item AND revision=r.revision AND state='applied'),'') FROM tool_revisions r JOIN tool_identities i ON i.id=r.item JOIN tool_plans p ON r.source='plan:' || p.id AND p.applied=1 WHERE r.revision=(SELECT MAX(x.revision) FROM tool_revisions x WHERE x.item=r.item) ORDER BY r.item`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.ToolTarget{}
	for rows.Next() {
		var r protocol.ToolRevision
		var name, assignment, plan, applied string
		if err = rows.Scan(&r.Item, &r.Revision, &assignment, &r.Version, &r.Actor, &r.Source, &r.At, &name, &plan, &applied); err != nil {
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
		appliedIndices := map[int]bool{}
		for _, v := range strings.Split(applied, ",") {
			if i, e := strconv.Atoi(v); e == nil {
				appliedIndices[i] = true
			}
		}
		ready := true
		for i, t := range p.Targets {
			if t.Action == "copy" && !appliedIndices[i] {
				ready = false
			}
		}
		for i, t := range p.Targets {
			if t.NodeID == node {
				out = append(out, protocol.ToolTarget{Revision: r, Name: name, Index: i, Target: t, CopiesReady: ready})
			}
		}
	}
	return out, rows.Err()
}

func (s *Store) ReportToolResult(node string, r protocol.ToolResult) error {
	switch r.State {
	case "applied", "pending", "conflict", "manual", "failed":
	default:
		return errors.New("invalid tool result state")
	}
	if len(r.Reason) > 512 || r.Index < 0 {
		return errors.New("invalid tool result")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	err = tx.QueryRow(`SELECT p.plan FROM tool_revisions r JOIN tool_plans p ON r.source='plan:' || p.id AND p.applied=1 WHERE r.item=? AND r.revision=? AND r.revision=(SELECT MAX(x.revision) FROM tool_revisions x WHERE x.item=r.item)`, r.Item, r.Revision).Scan(&raw)
	if err != nil {
		return errors.New("tool revision unavailable")
	}
	var p protocol.ToolPlan
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return err
	}
	if r.Index >= len(p.Targets) || p.Targets[r.Index].NodeID != node || node == "" {
		return errors.New("target does not belong to node")
	}
	_, err = tx.Exec(`INSERT INTO tool_results(item,revision,target_index,node_id,state,reason) VALUES(?,?,?,?,?,?) ON CONFLICT(item,revision,target_index) DO UPDATE SET state=excluded.state,reason=excluded.reason`, r.Item, r.Revision, r.Index, node, r.State, r.Reason)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// toolOutcomes maps each plan target to ok, pending, conflict or failed.
// waiting is true while a target on a connected machine has not reported yet.
func (s *Store) toolOutcomes(r protocol.ToolRevision, targets []protocol.ToolPlanTarget, online func(string) bool) (out protocol.ToolApplyResult, waiting bool) {
	out.ToolRevision = r
	out.Targets = []protocol.ToolTargetOutcome{}
	reported := map[int]protocol.ToolResult{}
	if rows, err := s.db.Query(`SELECT target_index,state,reason FROM tool_results WHERE item=? AND revision=?`, r.Item, r.Revision); err == nil {
		for rows.Next() {
			var v protocol.ToolResult
			if rows.Scan(&v.Index, &v.State, &v.Reason) == nil {
				reported[v.Index] = v
			}
		}
		rows.Close()
	}
	for i, t := range targets {
		o := protocol.ToolTargetOutcome{NodeID: t.NodeID, Machine: t.Machine, Harness: t.Harness, Project: t.Project, Checkout: t.Checkout, Source: t.Source, Destination: t.Destination, Action: t.Action}
		if v, ok := reported[i]; ok {
			o.Reason = v.Reason
			switch v.State {
			case "applied":
				o.State = "ok"
			case "pending":
				o.State = "pending"
			case "failed":
				o.State = "failed"
			default:
				o.State = "conflict"
			}
		} else if t.Action == "inspect" || t.Action == "import" || t.Action == "manual" {
			o.State = "conflict"
			o.Reason = t.Conflict
		} else {
			o.State = "pending"
			if online(t.NodeID) {
				waiting = true
				o.Reason = "waiting for the machine to report"
			} else {
				o.Reason = "machine offline; applies when it reconnects"
			}
		}
		out.Targets = append(out.Targets, o)
	}
	return out, waiting
}

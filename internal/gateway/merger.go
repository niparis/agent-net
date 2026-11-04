package gateway

import (
	"database/sql"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// SetMerger changes an assignment only through the owner gate or an approved proposal.
func (s *Store) SetMerger(q protocol.MergerSetRequest, actor string) error {
	if !actorAllowed(actor, "rule") {
		return reject("only the owner may register a merger")
	}
	if q.Project == "" || q.NodeID == "" {
		return reject("merger needs project and node_id")
	}
	needsDefault, err := s.MergerNeedsDefault(q.Project)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if q.SessionID == "" {
		_, err = tx.Exec(`DELETE FROM project_mergers WHERE project=? AND node_id=?`, q.Project, q.NodeID)
	} else {
		var valid int
		err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=? AND s.node_id=? AND COALESCE(NULLIF(s.repo_project,''),p.project,'')=? AND s.worker='' AND s.harness='claude')`, q.SessionID, q.NodeID, q.Project).Scan(&valid)
		if err != nil {
			return err
		}
		if valid == 0 {
			return reject("merger must be a Claude session of this project on this machine")
		}
		_, err = tx.Exec(`INSERT INTO project_mergers(project,node_id,session_id) VALUES(?,?,?) ON CONFLICT(project,node_id) DO UPDATE SET session_id=excluded.session_id`, q.Project, q.NodeID, q.SessionID)
	}
	if err != nil {
		return err
	}
	if q.Configure {
		if q.SessionID != "" {
			_, err = tx.Exec(`INSERT INTO profiles(session_id,name,function,project,capabilities,task,updated_at,expertise) VALUES(?,?,'','','[]','',?,'') ON CONFLICT(session_id) DO UPDATE SET name=excluded.name`, q.SessionID, q.Project+" merger", s.now().UnixNano())
			if err != nil {
				return err
			}
			if needsDefault {
				snap, err := settingsSnapshotTx(tx)
				if err != nil {
					return err
				}
				requires := append(snap.Strings("delivery.pipeline.merge.requires", settings.Where{Repo: q.Project}), "test:suite")
				value, _ := json.Marshal(requires)
				var previous string
				err = tx.QueryRow(`SELECT value FROM setting_values WHERE key='delivery.pipeline.merge.requires' AND level='repo' AND scope=? AND item=''`, q.Project).Scan(&previous)
				if err != nil && err != sql.ErrNoRows {
					return err
				}
				change := SettingsChange{Key: "delivery.pipeline.merge.requires", Level: "repo", Scope: q.Project, Value: value}
				if previous != "" {
					change.Expect = json.RawMessage(previous)
				}
				if _, err = s.writeSettingsTx(tx, []SettingsChange{change}, actor, "enable merger pipeline", "write", nil); err != nil {
					return err
				}
			}

		}
		owner := "/delivery/merger"
		if q.SessionID == "" {
			owner = "/delivery/caller"
		}
		for _, kind := range []string{"accepts", "tests", "merges"} {
			source := owner
			if kind == "accepts" {
				if q.SessionID == "" {
					continue
				}
				source = "/delivery/caller"
			}
			rows, err := tx.Query(`SELECT source,mode,rule FROM graph_edges WHERE target=? AND kind=?`, q.Project, kind)
			if err != nil {
				return err
			}
			var previous []protocol.Edge
			for rows.Next() {
				e := protocol.Edge{To: q.Project, Kind: kind}
				if err := rows.Scan(&e.From, &e.Mode, &e.Rule); err != nil {
					rows.Close()
					return err
				}
				previous = append(previous, e)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			if _, err = tx.Exec(`DELETE FROM graph_edges WHERE target=? AND kind=?`, q.Project, kind); err != nil {
				return err
			}
			for _, e := range previous {
				raw, _ := json.Marshal(e)
				if err := graphRecord(tx, "edge", e.From, kind, string(raw), "", actor, s.now().UnixNano()); err != nil {
					return err
				}
			}
			if err := setEdgeTx(tx, protocol.Edge{From: source, To: q.Project, Kind: kind}, actor, s.now().UnixNano()); err != nil {
				return err
			}
		}
		// A stopped or replaced merger must not retain claims or queued notices.
		if err := resetMergerTasks(tx, q.Project, q.NodeID); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.refreshDeliveryTasks()
	s.settingsNotify()
	return nil
}

// MergerNeedsDefault detects projects where testing was optional. All projects
// already have a default pipeline; merger setup makes its test a merge gate.
func (s *Store) MergerNeedsDefault(project string) (bool, error) {
	snap, err := s.SettingsSnapshot()
	if err != nil {
		return false, err
	}
	p, err := protocol.PipelineFromSettings(snap, project)
	if err != nil {
		return false, err
	}
	merge, have := p.Find(protocol.StepRef{Step: protocol.StepMerge})
	if !have {
		return true, nil
	}
	for _, req := range merge.Requires {
		if req.Step == protocol.StepTest && !req.AfterMerge {
			return false, nil
		}
	}
	return true, nil
}

func resetMergerTasks(tx *sql.Tx, project, node string) error {
	_, err := tx.Exec(`UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched') AND id IN (SELECT m.message_id FROM delivery_step_messages m JOIN delivery_changes c ON c.id=m.change_id WHERE c.project=? AND c.machine=? AND m.step IN ('test','merge') AND m.after_merge=0)`, project, node)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM delivery_step_messages WHERE change_id IN (SELECT id FROM delivery_changes WHERE project=? AND machine=?) AND step IN ('test','merge') AND after_merge=0`, project, node)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM delivery_step_tasks WHERE change_id IN (SELECT id FROM delivery_changes WHERE project=? AND machine=?) AND step IN ('test','merge') AND after_merge=0 AND completed=0`, project, node)
	return err
}

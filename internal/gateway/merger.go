package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"

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
	if q.Configure && q.SessionID != "" {
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
		owner := "/delivery/merger"
		for _, kind := range []string{"accepts", "tests", "merges"} {
			source := owner
			if kind == "accepts" {
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
		if err := resetConfiguredMergerTasks(tx, q.Project, q.NodeID); err != nil {
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

// Register cleanup on the record itself so session deletion and direct owner
// replacements have the same atomic behavior as merger_stop.
func mergerLifecycleMigration() string {
	reset := func(scope, owner string) string {
		return fmt.Sprintf(`
 UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched','failed') AND id IN (
 SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t
 USING(change_id,revision,step,for_step,after_merge)
 JOIN delivery_changes c ON c.id=t.change_id
 WHERE t.owner_node='/merger' AND t.completed=0 AND %s);
 DELETE FROM delivery_step_messages WHERE message_id IN (
 SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t
 USING(change_id,revision,step,for_step,after_merge)
 JOIN delivery_changes c ON c.id=t.change_id
 WHERE t.owner_node='/merger' AND t.completed=0 AND %s);
 UPDATE delivery_step_tasks SET owner_node='%s',claimed_by=''
 WHERE owner_node='/merger' AND completed=0 AND change_id IN
 (SELECT c.id FROM delivery_changes c WHERE %s);
 `, scope, scope, owner, scope)
	}
	return `
 DROP TRIGGER IF EXISTS merger_removed;
 DROP TRIGGER IF EXISTS merger_replaced;
 CREATE TRIGGER merger_removed AFTER DELETE ON project_mergers BEGIN
 ` + reset("c.project=OLD.project", callerTaskOwner) + `
 INSERT OR IGNORE INTO graph_edges(source,target,kind,mode,rule)
 SELECT '/delivery/caller',target,kind,mode,rule FROM graph_edges
 WHERE source='/delivery/merger' AND target=OLD.project AND kind<>'accepts';
 DELETE FROM graph_edges WHERE source='/delivery/merger' AND target=OLD.project AND kind<>'accepts';
 UPDATE setting_revision SET revision=revision+1 WHERE id=1;
 END;
 CREATE TRIGGER merger_replaced AFTER UPDATE OF session_id ON project_mergers
 WHEN OLD.session_id<>NEW.session_id BEGIN
 ` + reset("c.project=OLD.project AND c.machine=OLD.node_id", mergerTaskOwner) + `
 END;
 `
}

// Starting routing also moves ready pre-merge test and merge tasks that were
// captured under their previous owner. Completed evidence stays untouched.
func resetConfiguredMergerTasks(tx *sql.Tx, project, node string) error {
	for _, query := range []string{
		`UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched','failed') AND id IN
 (SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t USING(change_id,revision,step,for_step,after_merge)
 JOIN delivery_changes c ON c.id=t.change_id WHERE c.project=? AND c.machine=? AND t.completed=0 AND t.step IN ('test','merge') AND t.after_merge=0)`,
		`DELETE FROM delivery_step_messages WHERE message_id IN
 (SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t USING(change_id,revision,step,for_step,after_merge)
 JOIN delivery_changes c ON c.id=t.change_id WHERE c.project=? AND c.machine=? AND t.completed=0 AND t.step IN ('test','merge') AND t.after_merge=0)`,
		`UPDATE delivery_step_tasks SET owner_node='/merger',claimed_by='' WHERE completed=0 AND step IN ('test','merge') AND after_merge=0
 AND change_id IN (SELECT id FROM delivery_changes WHERE project=? AND machine=?)`,
	} {
		if _, err := tx.Exec(query, project, node); err != nil {
			return err
		}
	}
	return nil
}

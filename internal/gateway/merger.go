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
		err = s.stopMergerTx(tx, q.Project, actor)
	} else {
		var valid int
		err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=? AND s.node_id=? AND `+sessionRepoSQL+`=? AND s.worker='')`, q.SessionID, q.NodeID, q.Project).Scan(&valid)
		if err != nil {
			return err
		}
		if valid == 0 {
			return reject("merger must be a session of this project on this machine")
		}
		_, err = tx.Exec(`INSERT INTO project_mergers(project,node_id,session_id) VALUES(?,?,?) ON CONFLICT(project,node_id) DO UPDATE SET session_id=excluded.session_id`, q.Project, q.NodeID, q.SessionID)
		if err == nil {
			err = bindMergerAgentTx(tx, q.Project, q.SessionID, actor, s.now().UnixNano())
		}
		// Configure is merger_start: agent-net started this session.
		if err == nil && q.Configure {
			_, err = tx.Exec(`UPDATE sessions SET started_by=? WHERE id=?`, protocol.StartedByAgentNet, q.SessionID)
		}
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

// Register cleanup on the record itself so a session deletion ends the
// project's routing the same way an explicit stop does. A configured stop
// does its work in Go; these triggers cover rows deleted by any SQL path.
//
// The text below is frozen once shipped: migrations are append-only, so a
// later change needs a new migration. Keep it literal SQL, with every owner
// and edge source written out.
func mergerLifecycleMigration() string {
	return `
 DROP TRIGGER IF EXISTS merger_removed;
 DROP TRIGGER IF EXISTS merger_replaced;
 CREATE TRIGGER merger_removed AFTER DELETE ON project_mergers BEGIN
 UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched','failed') AND id IN (
 SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t
 USING(change_id,revision,step,for_step,after_merge)
 JOIN delivery_changes c ON c.id=t.change_id
 WHERE t.owner_node='/merger' AND t.completed=0 AND c.project=OLD.project);
 DELETE FROM delivery_step_messages WHERE message_id IN (
 SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t
 USING(change_id,revision,step,for_step,after_merge)
 JOIN delivery_changes c ON c.id=t.change_id
 WHERE t.owner_node='/merger' AND t.completed=0 AND c.project=OLD.project);
 UPDATE delivery_step_tasks SET owner_node='/caller',claimed_by=''
 WHERE owner_node='/merger' AND completed=0 AND change_id IN
 (SELECT c.id FROM delivery_changes c WHERE c.project=OLD.project);
 INSERT OR IGNORE INTO graph_edges(source,target,kind,mode,rule)
 SELECT '/delivery/caller',target,kind,mode,rule FROM graph_edges
 WHERE source='/delivery/merger' AND target=OLD.project AND kind<>'accepts';
 DELETE FROM graph_edges WHERE source='/delivery/merger' AND target=OLD.project AND kind<>'accepts';
 DELETE FROM project_mergers WHERE project=OLD.project;
 UPDATE setting_revision SET revision=revision+1 WHERE id=1;
 END;
 CREATE TRIGGER merger_replaced AFTER UPDATE OF session_id ON project_mergers
 WHEN OLD.session_id<>NEW.session_id BEGIN
 UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched','failed') AND id IN (
 SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t
 USING(change_id,revision,step,for_step,after_merge)
 JOIN delivery_changes c ON c.id=t.change_id
 WHERE t.owner_node='/merger' AND t.completed=0 AND c.project=OLD.project AND c.machine=OLD.node_id);
 DELETE FROM delivery_step_messages WHERE message_id IN (
 SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t
 USING(change_id,revision,step,for_step,after_merge)
 JOIN delivery_changes c ON c.id=t.change_id
 WHERE t.owner_node='/merger' AND t.completed=0 AND c.project=OLD.project AND c.machine=OLD.node_id);
 UPDATE delivery_step_tasks SET owner_node='/merger',claimed_by=''
 WHERE owner_node='/merger' AND completed=0 AND change_id IN
 (SELECT c.id FROM delivery_changes c WHERE c.project=OLD.project AND c.machine=OLD.node_id);
 END;
 `
}

// stopMergerTx ends routing for a project. Every @merger edge except accept
// goes back to the caller through the normal graph path, so graph history
// records the change and graph undo can restore it. Open tasks and queued
// notices follow, and every machine's registration is removed: a registration
// without routing would leave its session a merger that receives nothing.
func (s *Store) stopMergerTx(tx *sql.Tx, project, actor string) error {
	rows, err := tx.Query(`SELECT source,kind,mode,rule FROM graph_edges
 WHERE target=? AND source='/delivery/merger' AND kind<>'accepts'`, project)
	if err != nil {
		return err
	}
	var previous []protocol.Edge
	for rows.Next() {
		e := protocol.Edge{To: project}
		if err := rows.Scan(&e.From, &e.Kind, &e.Mode, &e.Rule); err != nil {
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
	for _, e := range previous {
		if _, err := tx.Exec(`DELETE FROM graph_edges WHERE source=? AND target=? AND kind=?`, e.From, e.To, e.Kind); err != nil {
			return err
		}
		old, _ := json.Marshal(e)
		if err := graphRecord(tx, "edge", e.From, e.Kind, string(old), "", actor, s.now().UnixNano()); err != nil {
			return err
		}
		if err := setEdgeTx(tx, protocol.Edge{From: "/delivery/caller", To: project, Kind: e.Kind}, actor, s.now().UnixNano()); err != nil {
			return err
		}
	}
	if err := resetStoppedMergerTasks(tx, project); err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM project_mergers WHERE project=?`, project)
	return err
}

// resetStoppedMergerTasks expires queued @merger notices and returns every open
// @merger task of the project to the caller on every machine.
func resetStoppedMergerTasks(tx *sql.Tx, project string) error {
	if _, err := tx.Exec(`UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched','failed') AND id IN
 (SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t USING(change_id,revision,step,for_step,after_merge)
 JOIN delivery_changes c ON c.id=t.change_id WHERE c.project=? AND t.owner_node='/merger' AND t.completed=0)`, project); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM delivery_step_messages WHERE message_id IN
 (SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t USING(change_id,revision,step,for_step,after_merge)
 JOIN delivery_changes c ON c.id=t.change_id WHERE c.project=? AND t.owner_node='/merger' AND t.completed=0)`, project); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE delivery_step_tasks SET owner_node=?,claimed_by='',urgent=1 WHERE owner_node='/merger' AND completed=0
 AND change_id IN (SELECT id FROM delivery_changes WHERE project=?)`, callerTaskOwner, project)
	return err
}

// Starting routing moves unfinished tasks whose configured pipeline owner is merger.
// Existing pre-merge claims are released as before; after-merge claims stay put.
func resetConfiguredMergerTasks(tx *sql.Tx, project, node string) error {
	snap, err := settingsSnapshotTx(tx)
	if err != nil {
		return err
	}
	p, err := protocol.PipelineFromSettings(snap, project)
	if err != nil {
		return err
	}
	for _, step := range p.Steps {
		if step.Owner.Kind != protocol.OwnerMerger {
			continue
		}
		args := []any{project, node, step.Step, step.For, boolInt(step.AfterMerge)}
		for _, query := range []string{
			`UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched','failed') AND id IN
    (SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t USING(change_id,revision,step,for_step,after_merge)
    JOIN delivery_changes c ON c.id=t.change_id WHERE c.project=? AND c.machine=? AND t.step=? AND t.for_step=? AND t.after_merge=? AND t.completed=0 AND (t.after_merge=0 OR t.claimed_by=''))`,
			`DELETE FROM delivery_step_messages WHERE message_id IN
    (SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t USING(change_id,revision,step,for_step,after_merge)
    JOIN delivery_changes c ON c.id=t.change_id WHERE c.project=? AND c.machine=? AND t.step=? AND t.for_step=? AND t.after_merge=? AND t.completed=0 AND (t.after_merge=0 OR t.claimed_by=''))`,
			`UPDATE delivery_step_tasks SET owner_node='/merger',claimed_by='',urgent=1 WHERE change_id IN
    (SELECT id FROM delivery_changes WHERE project=? AND machine=?) AND step=? AND for_step=? AND after_merge=? AND completed=0 AND (after_merge=0 OR claimed_by='')`,
		} {
			if _, err := tx.Exec(query, args...); err != nil {
				return err
			}
		}
	}
	return nil
}

package gateway

import "github.com/niparis/agent-net/internal/protocol"

// RestoreMergerRegistrations restores only registrations. Routing edges and
// pipeline settings have their own guarded undo records; restoring a
// registration must not overwrite the state those records compare against.
func (s *Store) RestoreMergerRegistrations(project string, saved []protocol.MergerSetRequest) error {
	if project == "" {
		return reject("project required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range saved {
		if q.Project != project || q.NodeID == "" || q.SessionID == "" {
			return reject("invalid merger snapshot")
		}
		var valid bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=? AND s.node_id=? AND `+sessionRepoSQL+`=? AND s.worker='' AND s.harness='claude')`, q.SessionID, q.NodeID, project).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return reject("saved merger is not a Claude session of this project")
		}
	}
	// The shipped deletion trigger resets merger routing. Preserve those
	// edges inside this transaction so their own guarded undo can run next.
	type edge struct{ source, target, kind, mode, rule, sourceKind, targetKind string }
	rows, err := tx.Query(`SELECT source,target,kind,mode,rule,source_kind,target_kind FROM graph_edges WHERE target=? AND source IN ('/delivery/merger','/delivery/caller') AND kind<>'accepts'`, project)
	if err != nil {
		return err
	}
	var edges []edge
	for rows.Next() {
		var e edge
		if err := rows.Scan(&e.source, &e.target, &e.kind, &e.mode, &e.rule, &e.sourceKind, &e.targetKind); err != nil {
			rows.Close()
			return err
		}
		edges = append(edges, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM project_mergers WHERE project=?`, project); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM graph_edges WHERE target=? AND source IN ('/delivery/merger','/delivery/caller') AND kind<>'accepts'`, project); err != nil {
		return err
	}
	for _, e := range edges {
		if _, err := tx.Exec(`INSERT INTO graph_edges(source,target,kind,mode,rule,source_kind,target_kind) VALUES(?,?,?,?,?,?,?)`, e.source, e.target, e.kind, e.mode, e.rule, e.sourceKind, e.targetKind); err != nil {
			return err
		}
	}
	for _, q := range saved {
		if _, err := tx.Exec(`INSERT INTO project_mergers(project,node_id,session_id) VALUES(?,?,?)`, project, q.NodeID, q.SessionID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.refreshDeliveryTasks()
	return nil
}

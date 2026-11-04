package gateway

import "github.com/niparis/agent-net/internal/protocol"

// SetMerger changes an assignment only through the owner gate or an approved proposal.
func (s *Store) SetMerger(q protocol.MergerSetRequest, actor string) error {
	if !actorAllowed(actor, "rule") {
		return reject("only the owner may register a merger")
	}
	if q.Project == "" || q.NodeID == "" {
		return reject("merger needs project and node_id")
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
	if err = tx.Commit(); err != nil {
		return err
	}
	s.refreshDeliveryTasks()
	return nil
}

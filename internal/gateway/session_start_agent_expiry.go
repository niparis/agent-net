package gateway

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A lost launcher response must not hold new sessions or later selected starts
// indefinitely. Two minutes bounds recovery without making availability a setting.
const sessionAgentStartTimeout = 2 * time.Minute

type agentStartExpiry struct{ node, harness, native, ref, owner, project string }

func (s *Store) ExpireSessionAgentStarts() ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT node_id,harness,native_id,ref,owner_session,project FROM session_agent_starts WHERE expires_at>0 AND expires_at<=?`, s.now().UnixNano())
	if err != nil {
		return nil, err
	}
	var starts []agentStartExpiry
	for rows.Next() {
		var a agentStartExpiry
		if err = rows.Scan(&a.node, &a.harness, &a.native, &a.ref, &a.owner, &a.project); err != nil {
			rows.Close()
			return nil, err
		}
		starts = append(starts, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var nodes []string
	for _, a := range starts {
		if err = s.failAgentStart(tx, a, "Agent choice expired after two minutes"); err != nil {
			return nil, err
		}
		nodes = append(nodes, a.node)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return nodes, nil
}

func (s *Store) failAgentStart(tx *sql.Tx, a agentStartExpiry, reason string) error {
	if _, err := tx.Exec(`DELETE FROM session_agent_starts WHERE node_id=? AND harness=? AND native_id=? AND ref=?`, a.node, a.harness, a.native, a.ref); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE sessions SET agent_id=(SELECT owner_agent_id FROM repos WHERE id=sessions.repo_id) WHERE node_id=? AND harness=? AND worker='' AND agent_id IS NULL AND NOT (`+sessionAgentHoldSQL("sessions")+`)`, a.node, a.harness); err != nil {
		return err
	}
	text := s.agentChoiceFailureText(tx, a.ref, a.harness, a.project, "")
	if a.native != "" {
		var id string
		_ = tx.QueryRow(`SELECT id FROM sessions WHERE node_id=? AND harness=? AND native_id=?`, a.node, a.harness, a.native).Scan(&id)
		if id != "" {
			text = s.agentChoiceFailureText(tx, a.ref, a.harness, a.project, id)
		}
	}
	item, err := s.createInboxItem(tx, protocol.InboxItem{Kind: "check", SessionID: a.owner, Node: a.project, Text: text, Recommendation: reason})
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO session_agent_start_failures(node_id,ref,harness,native_id,owner_session,project,item_id,reason) VALUES(?,?,?,?,?,?,?,?)`, a.node, a.ref, a.harness, a.native, a.owner, a.project, item.ID, reason)
	return err
}

// When the native ID was lost, name the new harness session without guessing
// which concurrent session was selected. A later exact ID updates this same item.
func (s *Store) agentChoiceFailureText(q queryer, ref, harness, project, id string) string {
	label := "new " + harness + " session"
	agent := "Repo-owner Agent"
	if id != "" {
		var attached string
		if err := q.QueryRow(`SELECT s.name,COALESCE(s.repo_id,''),COALESCE(a.display_name,s.agent_id,'') FROM sessions s LEFT JOIN agents a ON a.id=s.agent_id WHERE s.id=?`, id).Scan(&label, &project, &attached); err == nil && attached != "" {
			return fmt.Sprintf("Agent choice for %s failed; %s uses the default %s", ref, label, attached)
		}
	}
	var name string
	if err := q.QueryRow(`SELECT COALESCE(a.display_name,r.owner_agent_id,'unassigned Agent') FROM repos r LEFT JOIN agents a ON a.id=r.owner_agent_id WHERE r.id=?`, project).Scan(&name); err == nil && name != "" {
		agent = name
	}
	return fmt.Sprintf("Agent choice for %s failed; %s uses the default %s", ref, label, agent)
}

func (s *Store) noteLateAgentNative(tx *sql.Tx, node string, p protocol.SessionAgentReservation) error {
	if p.NativeID == "" {
		return nil
	}
	if _, err := tx.Exec(`UPDATE session_agent_start_failures SET native_id=? WHERE node_id=? AND ref=? AND (native_id='' OR native_id=?)`, p.NativeID, node, p.Ref, p.NativeID); err != nil {
		return err
	}
	var id string
	err := tx.QueryRow(`SELECT id FROM sessions WHERE node_id=? AND harness=? AND native_id=?`, node, p.Harness, p.NativeID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE sessions SET agent_id=(SELECT owner_agent_id FROM repos WHERE id=sessions.repo_id) WHERE id=? AND worker='' AND agent_id IS NULL AND NOT (`+sessionAgentHoldSQL("sessions")+`)`, id); err != nil {
		return err
	}
	return s.updateFailedAgentSession(tx, node, p.NativeID, id)
}

func (s *Store) updateFailedAgentSession(q interface {
	execer
	Query(string, ...any) (*sql.Rows, error)
	queryer
}, node, native, id string) error {
	rows, err := q.Query(`SELECT ref,harness,project,item_id FROM session_agent_start_failures WHERE node_id=? AND native_id=? AND harness=(SELECT harness FROM sessions WHERE id=?)`, node, native, id)
	if err != nil {
		return err
	}
	type notice struct{ ref, harness, project, item string }
	var notices []notice
	for rows.Next() {
		var n notice
		if err = rows.Scan(&n.ref, &n.harness, &n.project, &n.item); err != nil {
			rows.Close()
			return err
		}
		notices = append(notices, n)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, n := range notices {
		if _, err = q.Exec(`UPDATE inbox_items SET text=? WHERE id=?`, s.agentChoiceFailureText(q, n.ref, n.harness, n.project, id), n.item); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) cancelSessionAgentStart(tx *sql.Tx, node string, p protocol.SessionAgentReservation) error {
	if p.Ref == "" {
		return reject("failed start needs its reference")
	}
	var a agentStartExpiry
	err := tx.QueryRow(`SELECT node_id,harness,native_id,ref,owner_session,project FROM session_agent_starts WHERE node_id=? AND ref=?`, node, p.Ref).Scan(&a.node, &a.harness, &a.native, &a.ref, &a.owner, &a.project)
	if errors.Is(err, sql.ErrNoRows) {
		if err = s.noteLateAgentNative(tx, node, p); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if p.NativeID != "" {
		a.native = p.NativeID
		// Delete by reference too when a response failed before the native binding.
		if _, err = tx.Exec(`DELETE FROM session_agent_starts WHERE node_id=? AND ref=?`, node, p.Ref); err != nil {
			return err
		}
	}
	if err = s.failAgentStart(tx, a, "The daemon reported that the start failed"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SessionAgentStartStatus(node, owner, ref string) (protocol.StartedSession, error) {
	if _, err := s.ExpireSessionAgentStarts(); err != nil {
		return protocol.StartedSession{}, err
	}
	var allowed bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=? AND node_id=? AND harness='owner' AND worker='')`, owner, node).Scan(&allowed); err != nil {
		return protocol.StartedSession{}, err
	}
	if !allowed {
		return protocol.StartedSession{}, reject("owner authority required")
	}
	r := protocol.StartedSession{ID: ref}
	err := s.db.QueryRow(`SELECT reason FROM session_agent_start_failures WHERE node_id=? AND ref=?`, node, ref).Scan(&r.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	r.Status = "failed"
	return r, nil
}

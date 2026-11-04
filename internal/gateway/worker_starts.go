package gateway

import (
	"database/sql"
	"errors"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// workerStart is a start of a worker that the gateway handed to a machine and
// tracks until the machine says it never became a worker. The reference is the
// queue ID of a queued start, or the session ID of a start that runs in the
// background. A machine can only report what it was given: a report names one
// of its own tracked starts, and the owner, project and harness of the notice
// come from this record, never from the report.
type workerStart struct {
	Ref, NodeID, Owner, Project, Harness, Permissions, SessionID, State string
}

// trackWorkerStart records a start that the gateway handed to a machine.
func (s *Store) trackWorkerStart(ref, nodeID, owner, project, harness, permissions, sessionID string) error {
	if ref == "" || nodeID == "" || owner == "" {
		return nil
	}
	// A start that no one reported for a month is not going to be reported.
	if _, err := s.db.Exec(`DELETE FROM worker_starts WHERE created_at < ?`, s.now().Add(-30*24*time.Hour).UnixNano()); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO worker_starts(ref,node_id,owner,project,harness,permissions,session_id,created_at)
		VALUES(?,?,?,?,?,?,?,?)`, ref, nodeID, owner, project, harness, permissions, sessionID, s.now().UnixNano())
	return err
}

// bindStartSession tells which session a tracked start became, so a later
// relay of its task can be checked against the start.
func (s *Store) bindStartSession(nodeID, ref, sessionID string) error {
	if ref == "" || sessionID == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE worker_starts SET session_id=? WHERE ref=? AND node_id=? AND session_id=''`, sessionID, ref, nodeID)
	return err
}

// declareNotStarted ends a tracked start of this machine, once. It returns the
// record and true when this call ended it. An unknown reference, a start of
// another machine, or a start already declared returns false.
func (s *Store) declareNotStarted(nodeID, ref string) (workerStart, bool, error) {
	var w workerStart
	tx, err := s.db.Begin()
	if err != nil {
		return w, false, err
	}
	defer tx.Rollback()
	err = tx.QueryRow(`SELECT ref,node_id,owner,project,harness,permissions,session_id,state FROM worker_starts WHERE ref=? AND node_id=?`, ref, nodeID).
		Scan(&w.Ref, &w.NodeID, &w.Owner, &w.Project, &w.Harness, &w.Permissions, &w.SessionID, &w.State)
	if errors.Is(err, sql.ErrNoRows) {
		return workerStart{}, false, nil
	}
	if err != nil {
		return workerStart{}, false, err
	}
	if w.State != "open" {
		return w, false, nil
	}
	if _, err := tx.Exec(`UPDATE worker_starts SET state='not_started' WHERE ref=?`, ref); err != nil {
		return workerStart{}, false, err
	}
	return w, true, tx.Commit()
}

// startDeclaredNotStarted tells whether the start that made a session was
// declared not started.
func (s *Store) startDeclaredNotStarted(sessionID string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_starts WHERE state='not_started' AND (session_id=? OR ref=?)`, sessionID, sessionID).Scan(&n)
	return n > 0, err
}

// taskSent tells whether the owner already sent a request to the worker. The
// first request a worker gets from its owner is its task.
func (s *Store) taskSent(owner, session string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE sender=? AND recipient=? AND type=?`, owner, session, protocol.TypeRequest).Scan(&n)
	return n > 0, err
}

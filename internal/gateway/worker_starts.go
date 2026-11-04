package gateway

import (
	"database/sql"
	"errors"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// workerStart is a start of a worker that the gateway handed to a machine and
// tracks. The reference is the StartRef the gateway chose, which a queued start
// uses as its queue ID. A machine can only report what it was given: a report
// names one of its own tracked starts, and the owner, project and harness of
// the notice come from this record, never from the report.
//
// A start is open until one of two things ends it, once:
//   - closed: the worker is running and its task was sent. The machine can no
//     longer say it never started.
//   - not_started: the machine said it never became a worker.
type workerStart struct {
	Ref, NodeID, Owner, Project, Harness, Permissions, State string
}

// closedStartKeep is how long a closed start stays, counted from the day it
// closed. An open start is never removed: it may still be reported.
const closedStartKeep = 30 * 24 * time.Hour

// trackWorkerStart records a start that the gateway handed to a machine.
func (s *Store) trackWorkerStart(ref, nodeID, owner, project, harness, permissions string) error {
	if ref == "" || nodeID == "" || owner == "" {
		return nil
	}
	if _, err := s.db.Exec(`DELETE FROM worker_starts WHERE state <> 'open' AND closed_at > 0 AND closed_at < ?`, s.now().Add(-closedStartKeep).UnixNano()); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO worker_starts(ref,node_id,owner,project,harness,permissions,created_at)
		VALUES(?,?,?,?,?,?,?)`, ref, nodeID, owner, project, harness, permissions, s.now().UnixNano())
	return err
}

// closeStart closes an open start of this machine: its worker is running and
// got its task. A start that is not open, or unknown, is left as it is.
func (s *Store) closeStart(nodeID, ref string) error {
	if ref == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE worker_starts SET state='closed', closed_at=? WHERE ref=? AND node_id=? AND state='open'`, s.now().UnixNano(), ref, nodeID)
	return err
}

// declareNotStarted ends an open start of this machine, once, and stores the
// notice for its owner in the same transaction: a crash or a storage error
// leaves the start open, so the report can be made again. It returns the
// record, the notice when one was stored, and whether this call ended the start.
//
// An unknown reference, or a start of another machine, comes back with an empty
// rec.Ref and the caller refuses it. A start that is closed cannot be declared
// not started: the worker is running. A start already declared returns false.
func (s *Store) declareNotStarted(nodeID, ref, host, reason string) (rec workerStart, notice protocol.Message, noticed, first bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	err = tx.QueryRow(`SELECT ref,node_id,owner,project,harness,permissions,state FROM worker_starts WHERE ref=? AND node_id=?`, ref, nodeID).
		Scan(&rec.Ref, &rec.NodeID, &rec.Owner, &rec.Project, &rec.Harness, &rec.Permissions, &rec.State)
	if errors.Is(err, sql.ErrNoRows) {
		return workerStart{}, notice, false, false, nil
	}
	if err != nil {
		return workerStart{}, notice, false, false, err
	}
	switch rec.State {
	case "not_started":
		return rec, notice, false, false, nil
	case "closed":
		return rec, notice, false, false, reject("the worker of start %s is running, so it did start", ref)
	}
	if _, err = tx.Exec(`UPDATE worker_starts SET state='not_started', closed_at=? WHERE ref=? AND state='open'`, s.now().UnixNano(), ref); err != nil {
		return
	}
	notice, noticed, err = s.notStartedNotice(tx, rec, host, reason)
	if err != nil {
		return
	}
	if err = tx.Commit(); err != nil {
		return rec, protocol.Message{}, false, false, err
	}
	return rec, notice, noticed, true, nil
}

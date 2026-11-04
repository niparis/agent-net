package gateway

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// workerStart is a start of a worker that the gateway handed to a machine and
// tracks. The reference is the StartRef the gateway chose, which a queued start
// uses as its queue ID. A machine can only report what it was given: a report
// names one of its own tracked starts, and the owner, project and harness of
// the notice come from this record, never from the report.
//
// A start is open until one of two things closes it, and only once:
//   - delivered: the gateway stored its initial task. The machine can no longer
//     say it never started.
//   - not_started: the machine said it never became a worker. The task is no
//     longer accepted.
type workerStart struct {
	Ref, NodeID, Owner, Project, Harness, Permissions, SessionID, State string
}

// taskBriefPrefix starts the initial task a worker gets from its owner.
const taskBriefPrefix = "[agent-net worker brief]"

// closedStartKeep is how long a closed start stays. An open start is never
// removed: it may still be reported.
const closedStartKeep = 30 * 24 * time.Hour

// trackWorkerStart records a start that the gateway handed to a machine.
func (s *Store) trackWorkerStart(ref, nodeID, owner, project, harness, permissions, sessionID string) error {
	if ref == "" || nodeID == "" || owner == "" {
		return nil
	}
	if _, err := s.db.Exec(`DELETE FROM worker_starts WHERE state <> 'open' AND created_at < ?`, s.now().Add(-closedStartKeep).UnixNano()); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO worker_starts(ref,node_id,owner,project,harness,permissions,session_id,created_at)
		VALUES(?,?,?,?,?,?,?,?)`, ref, nodeID, owner, project, harness, permissions, sessionID, s.now().UnixNano())
	return err
}

// bindStartSession tells which session a tracked start became, so its task can
// be checked against the start.
func (s *Store) bindStartSession(nodeID, ref, sessionID string) error {
	if ref == "" || sessionID == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE worker_starts SET session_id=? WHERE ref=? AND node_id=? AND session_id=''`, sessionID, ref, nodeID)
	return err
}

// declareNotStarted ends an open start of this machine, once, and stores the
// notice for its owner in the same transaction: a crash or a storage error
// leaves the start open, so the report can be made again. It returns the
// record, the notice when one was stored, and whether this call ended the start.
//
// An unknown reference, or a start of another machine, is refused by the caller
// (rec.Ref is empty). A start that was already delivered cannot be declared
// not started: the worker is running. A start already declared returns false.
func (s *Store) declareNotStarted(nodeID, ref, host, reason string) (rec workerStart, notice protocol.Message, noticed, first bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	err = tx.QueryRow(`SELECT ref,node_id,owner,project,harness,permissions,session_id,state FROM worker_starts WHERE ref=? AND node_id=?`, ref, nodeID).
		Scan(&rec.Ref, &rec.NodeID, &rec.Owner, &rec.Project, &rec.Harness, &rec.Permissions, &rec.SessionID, &rec.State)
	if errors.Is(err, sql.ErrNoRows) {
		return workerStart{}, notice, false, false, nil
	}
	if err != nil {
		return workerStart{}, notice, false, false, err
	}
	switch rec.State {
	case "not_started":
		return rec, notice, false, false, nil
	case "delivered":
		return rec, notice, false, false, reject("the worker of start %s received its task, so it did start", ref)
	}
	if _, err = tx.Exec(`UPDATE worker_starts SET state='not_started' WHERE ref=? AND state='open'`, ref); err != nil {
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

// startDeclaredNotStarted tells whether the start that made a session was
// declared not started.
func (s *Store) startDeclaredNotStarted(sessionID string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_starts WHERE state='not_started' AND (session_id=? OR ref=?)`, sessionID, sessionID).Scan(&n)
	return n > 0, err
}

// taskSent tells whether the owner already sent the initial task to the worker.
// Only the worker brief counts: another request from the owner is not the task.
func (s *Store) taskSent(owner, session string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE sender=? AND recipient=? AND type=? AND substr(body,1,?)=?`,
		owner, session, protocol.TypeRequest, len(taskBriefPrefix), taskBriefPrefix).Scan(&n)
	return n > 0, err
}

// taskStart finds the tracked start that a message would be the initial task
// of: its sender is the owner the gateway recorded, it goes to that start's
// worker, and it is a request that opens with the worker brief. Any other
// message is not a task (found is false), also another request of the owner.
func taskStart(q queryer, p protocol.MsgSend) (ref, state string, online, found bool, err error) {
	if (p.Type != "" && p.Type != protocol.TypeRequest) || !strings.HasPrefix(p.Body, taskBriefPrefix) {
		return "", "", false, false, nil
	}
	err = q.QueryRow(`SELECT w.ref, w.state, s.online FROM worker_starts w JOIN sessions s ON s.id=w.session_id
		WHERE w.session_id=? AND w.owner=?`, p.Recipient, p.Sender).Scan(&ref, &state, &online)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, false, nil
	}
	return ref, state, online, err == nil, err
}

// checkInitialTask is the gate before a send is handled. It refuses the task
// of a start declared not started and the task for a worker that is offline,
// and it says (delivered) when the task was stored already, so an ended worker
// is not started again by a late repeat of its own task.
func (s *Store) checkInitialTask(p protocol.MsgSend) (delivered bool, err error) {
	_, state, online, found, err := taskStart(s.db, p)
	if err != nil || !found {
		return false, err
	}
	switch state {
	case "not_started":
		return false, reject("the start of worker %s was declared not started, so it gets no task", p.Recipient)
	case "delivered":
		return true, nil
	}
	if !online {
		return false, reject("worker %s has ended, so it gets no task", p.Recipient)
	}
	return false, nil
}

// claimInitialTask applies the same rules in the transaction that stores the
// message.
//
// The gateway, not the sender, decides the key of the task: it is derived from
// the start, so the same task sent twice (a relay that was repeated, a slow
// first send that lands late, an owner machine of any version) is stored once.
// It returns the reference to mark delivered in the same transaction, or "".
func (s *Store) claimInitialTask(tx *sql.Tx, p *protocol.MsgSend) (string, error) {
	ref, state, online, found, err := taskStart(tx, *p)
	if err != nil || !found {
		return "", err
	}
	switch state {
	case "not_started":
		return "", reject("the start of worker %s was declared not started, so it gets no task", p.Recipient)
	case "delivered":
		p.IdempotencyKey = "task:" + ref
		return "", nil
	}
	if !online {
		return "", reject("worker %s has ended, so it gets no task", p.Recipient)
	}
	p.IdempotencyKey = "task:" + ref
	return ref, nil
}

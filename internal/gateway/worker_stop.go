package gateway

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A stop is final. The gateway marks the worker's session stopped, so no
// later report, resume or message brings it back. When the worker's machine
// is not connected, the stop is kept as pending and sent when the machine
// connects again, before the messages waiting for it are dispatched.

// workerStopTimeout bounds the machine's answer to a stop.
const workerStopTimeout = 20 * time.Second

// noWorkerOnMachine tells whether a machine answered that it does not run the
// worker. Older daemons answer a stop for an ended worker that way.
func noWorkerOnMachine(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no worker with session")
}

// machineUnreachable tells whether a call failed because the machine did not
// answer, rather than because it refused.
func machineUnreachable(err error) bool {
	return errors.Is(err, errNodeGone) || errors.Is(err, context.DeadlineExceeded)
}

func (g *Gateway) workerStop(ctx context.Context, nc *nodeConn, p protocol.WorkerStopFrame, validate ...bool) (map[string]any, error) {
	req, ok, err := g.store.Agent(p.Requester)
	if err != nil {
		return nil, err
	}
	if !ok || req.NodeID != nc.id {
		return nil, reject("requester session %q does not belong to this node", p.Requester)
	}
	if protocol.IsQueueID(p.Session) {
		return g.cancelQueuedStart(ctx, req, p, validate...)
	}
	w, ok, err := g.store.Agent(p.Session)
	if err != nil {
		return nil, err
	}
	if !ok || w.Worker == nil {
		return nil, reject("session %q is not a worker", p.Session)
	}
	// The owner at a keyboard may stop any worker. An agent may stop its own.
	if !protocol.IsOwner(req.Harness) && req.SessionID != w.Worker.Owner {
		return nil, reject("only the worker's owner or the person at the command line can stop it")
	}
	kill := protocol.WorkerKill{Session: p.Session, Requester: p.Requester, AsOwner: protocol.IsOwner(req.Harness)}
	target := g.conn(w.NodeID)
	if len(validate) > 0 && validate[0] {
		// A stop to a machine that is away is stored, so only a refusal by a
		// connected machine makes it invalid.
		if target == nil {
			return nil, nil
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := target.call(cctx, "worker.validate_stop", kill)
		cancel()
		if err != nil && !noWorkerOnMachine(err) && !machineUnreachable(err) {
			return nil, reject("machine %s would not stop the worker: %v", w.Hostname, err)
		}
		return nil, nil
	}
	err = errNodeGone
	if target != nil {
		cctx, cancel := context.WithTimeout(ctx, workerStopTimeout)
		_, err = target.call(cctx, "worker.kill", kill)
		cancel()
	}
	switch {
	case err == nil, noWorkerOnMachine(err):
		// Stopped, or the machine does not run it: either way it has ended.
		if err := g.recordWorkerStop(ctx, w, false); err != nil {
			return nil, err
		}
		return map[string]any{"stopped": true}, nil
	case machineUnreachable(err):
		if err := g.recordWorkerStop(ctx, w, true); err != nil {
			return nil, err
		}
		return map[string]any{"stopped": false, "pending": true,
			"note": fmt.Sprintf("machine %s did not answer, so the stop is pending: the worker is marked stopped now, and its machine ends it when it connects again, before any message or start for it runs", w.Hostname)}, nil
	default:
		return nil, reject("machine %s did not stop the worker: %v", w.Hostname, err)
	}
}

// recordWorkerStop marks the worker stopped and ended, and gives each message
// still waiting for it back to its sender with a note.
func (g *Gateway) recordWorkerStop(ctx context.Context, w protocol.AgentView, pending bool) error {
	if err := g.store.RecordWorkerStop(w.NodeID, w.SessionID, pending); err != nil {
		return err
	}
	g.store.Event("worker.stop_recorded", w.NodeID, w.SessionID, "", fmt.Sprintf("pending=%t", pending))
	g.logLine("worker.stop_recorded", "node", w.NodeID, "host", w.Hostname, "session", w.SessionID, "pending", pending)
	waiting, err := g.store.withdrawForStoppedWorker(w.SessionID)
	if err != nil {
		g.logLine("error", "where", "withdraw messages of a stopped worker", "session", w.SessionID, "err", err)
		return nil
	}
	for _, m := range waiting {
		g.noteUndelivered(ctx, w.SessionID, m.From, m.ID, m.Body, stoppedWorkerReason(w.SessionID))
	}
	return nil
}

// stoppedWorkerReason is the one text that says why a stopped worker takes
// no message.
func stoppedWorkerReason(session string) error {
	return reject("worker %s was stopped, so it takes no messages and does not start again; continue it with agent_worker_continue to run its task again", session)
}

// applyPendingStops sends the stops kept while a machine was away. It runs
// when the machine has synced its sessions, before messages are dispatched.
func (g *Gateway) applyPendingStops(ctx context.Context, nc *nodeConn) {
	stops, err := g.store.pendingWorkerStops(nc.id)
	if err != nil {
		g.logLine("error", "where", "pending worker stops", "err", err)
		return
	}
	for _, s := range stops {
		cctx, cancel := context.WithTimeout(ctx, workerStopTimeout)
		_, err := nc.call(cctx, "worker.kill", protocol.WorkerKill{Session: s.session, Requester: s.owner})
		cancel()
		if machineUnreachable(err) {
			return // still away: the next connect tries again
		}
		if err != nil && !noWorkerOnMachine(err) {
			g.logLine("worker.stop_refused", "node", nc.id, "session", s.session, "reason", err)
		}
		if err := g.store.clearWorkerStopPending(nc.id, s.session); err != nil {
			g.logLine("error", "where", "clear pending worker stop", "err", err)
		}
		g.logLine("worker.stop_applied", "node", nc.id, "session", s.session)
	}
}

// noteUndelivered tells a sender that its message did not reach a worker, and
// keeps the message text in that note.
func (g *Gateway) noteUndelivered(ctx context.Context, worker, sender, key, text string, reason error) {
	if sender == "" || sender == worker {
		return
	}
	body := fmt.Sprintf("[agent-net] Your message could not be delivered because %v. Retry the message or start a new worker.\n\nMessage: ", reason)
	remaining := protocol.MaxBodyBytes - len(body)
	if remaining < 0 {
		return
	}
	if len(text) <= remaining {
		body += text
	} else {
		body += strings.ToValidUTF8(text[:remaining], "")
	}
	idem := fmt.Sprintf("resume-refusal-%x", sha256.Sum256([]byte(sender+"\x00"+key)))
	m, created, err := g.store.StoreMessage(protocol.MsgSend{Sender: worker, Recipient: sender, Type: protocol.TypeNotification, Body: body, IdempotencyKey: idem, WorkerNotice: true})
	if err != nil {
		g.logLine("worker.resume_notice_failed", "session", worker, "reason", err)
		return
	}
	if created {
		g.dispatch(ctx, m)
	}
}

// RecordWorkerStop marks a worker's session stopped and ended. pending says
// that its machine has not been told yet. The first stop time is kept.
func (s *Store) RecordWorkerStop(nodeID, sessionID string, pending bool) error {
	now := s.now().UnixNano()
	_, err := s.db.Exec(`UPDATE sessions SET online=0, status='ended', worker_run_active=0, last_seen=?,
		worker_stopped_at=CASE WHEN worker_stopped_at=0 THEN ? ELSE worker_stopped_at END, worker_stop_pending=?
		WHERE id=? AND node_id=? AND worker<>''`, now, now, boolInt(pending), sessionID, nodeID)
	return err
}

// WorkerStopped tells whether a worker's session was stopped.
func (s *Store) WorkerStopped(sessionID string) (bool, error) {
	var at int64
	err := s.db.QueryRow(`SELECT worker_stopped_at FROM sessions WHERE id=?`, sessionID).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return at > 0, err
}

type pendingStop struct{ session, owner string }

// pendingWorkerStops lists the stops a machine has not been told yet, oldest
// first, with the owner of each worker.
func (s *Store) pendingWorkerStops(nodeID string) ([]pendingStop, error) {
	rows, err := s.db.Query(`SELECT id, CASE WHEN json_valid(worker) THEN COALESCE(json_extract(worker,'$.owner'),'') ELSE '' END
		FROM sessions WHERE node_id=? AND worker_stop_pending=1 ORDER BY worker_stopped_at, id`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pendingStop
	for rows.Next() {
		var p pendingStop
		if err := rows.Scan(&p.session, &p.owner); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) clearWorkerStopPending(nodeID, sessionID string) error {
	_, err := s.db.Exec(`UPDATE sessions SET worker_stop_pending=0 WHERE id=? AND node_id=?`, sessionID, nodeID)
	return err
}

// withdrawForStoppedWorker takes back the messages still waiting for a
// stopped worker, so none of them starts it again.
func (s *Store) withdrawForStoppedWorker(sessionID string) ([]protocol.Message, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id FROM messages WHERE recipient=? AND state IN `+withdrawableStates+` ORDER BY created_at, rowid`, sessionID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	now := s.now().UnixNano()
	var out []protocol.Message
	for _, id := range ids {
		m, ok, err := s.message(tx, "m.id=?", id)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if err := s.withdrawOne(tx, m, now); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for _, m := range out {
		s.Event("msg.withdrawn", "", m.To, m.ID, "worker stopped")
	}
	return out, nil
}

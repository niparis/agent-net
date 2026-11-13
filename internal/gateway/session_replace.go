package gateway

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// RebindResumedSession keeps a session's ID when a restart resumed the same
// conversation under a new native ID, as a Claude Code background copy can.
// The record stays; only the harness handle moves. Only the machine that holds
// the session may ask, and not while it is working: a stopped session can
// still be reported online with an ended status until its harness drops it.
func (s *Store) RebindResumedSession(nodeID, sessionID, nativeID string) (bool, error) {
	if !validSessionID(sessionID) || strings.TrimSpace(nativeID) == "" {
		return false, reject("a resumed session needs its ID and the new native ID")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var node, harness, current, replaced, status string
	var online int
	err = tx.QueryRow(`SELECT node_id,harness,native_id,online,status,replaced_by FROM sessions WHERE id=?`, sessionID).
		Scan(&node, &harness, &current, &online, &status, &replaced)
	if errors.Is(err, sql.ErrNoRows) {
		return false, reject("unknown session %s", sessionID)
	}
	if err != nil {
		return false, err
	}
	if node != nodeID {
		return false, reject("only the machine that holds a session can resume it")
	}
	if replaced != "" {
		return false, reject("session %s was replaced by %s", sessionID, replaced)
	}
	// A stopped session can still be reported as online with an ended status
	// until its harness drops it. A working session is not a restart.
	if online != 0 && status == "busy" {
		return false, reject("session %s is still working", sessionID)
	}
	if harness != "claude" && harness != "codex" && harness != "opencode" {
		return false, reject("%s cannot resume a conversation under a new native ID", harness)
	}
	if current == nativeID {
		return false, nil
	}
	var used int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE node_id=? AND harness=? AND native_id=?`,
		nodeID, harness, nativeID).Scan(&used); err != nil {
		return false, err
	}
	if used > 0 {
		return false, reject("the resumed conversation %s is already a session", nativeID)
	}
	res, err := tx.Exec(`UPDATE sessions SET native_id=? WHERE id=? AND node_id=? AND native_id=? AND online=0`,
		nativeID, sessionID, nodeID, current)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, reject("session %s changed before the resume could keep its ID", sessionID)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ReplaceSession moves a restarted session's work to the session its new
// conversation registered, in one transaction, and marks the old record
// replaced. It is the fallback for a harness that cannot keep the session ID:
// a new conversation is a new session, but it is still the same caller.
//
// It moves the caller role of running and queued workers, the caller ownership
// of open and merged delivery changes and their steps, undelivered messages and open
// requests, the agent and repo binding, and the name. The old record keeps its
// name and says which session replaced it, so nothing is renamed to "name 2".
func (s *Store) ReplaceSession(nodeID, sessionID, newSessionID string) (bool, error) {
	if !validSessionID(sessionID) || !validSessionID(newSessionID) || sessionID == newSessionID {
		return false, reject("a replaced session needs its ID and a different new session")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var node, name, replaced, status string
	var online int
	err = tx.QueryRow(`SELECT node_id,name,replaced_by,online,status FROM sessions WHERE id=?`, sessionID).
		Scan(&node, &name, &replaced, &online, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, reject("unknown session %s", sessionID)
	}
	if err != nil {
		return false, err
	}
	if node != nodeID {
		return false, reject("only the machine that holds a session can replace it")
	}
	// The restart may be reported twice (a poll and a retry), and the owner may
	// run the repair again for a pair that an earlier version moved only in
	// part. The first move wins; a repeat only repairs change ownership that
	// was missed, without renaming or moving the inbox twice. A session that
	// still works is not the one a restart replaced.
	if replaced == newSessionID {
		// Repair ownership missed by an earlier version without moving names,
		// inbox entries or bindings again. A change still has work only while
		// it is open or merged; done and stopped changes keep their history
		// under the old caller. The move is reported when it repairs a row, so
		// the owner's retry is not mistaken for a no-op. A session that already
		// holds both a stale and a current association keeps the current one.
		repaired, err := moveStepNotices(tx, sessionID, newSessionID)
		if err != nil {
			return false, err
		}
		for _, query := range []string{
			"UPDATE delivery_changes SET caller=? WHERE caller=? AND state IN ('open','merged')",
			"UPDATE delivery_step_tasks SET claimed_by=? WHERE claimed_by=? AND change_id IN (SELECT id FROM delivery_changes WHERE state IN ('open','merged'))",
		} {
			res, err := tx.Exec(query, newSessionID, sessionID)
			if err != nil {
				return false, err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return false, err
			}
			repaired += n
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return repaired > 0, nil
	}
	if replaced != "" || (online != 0 && status == "busy") {
		return false, nil
	}
	var newNode, newName, newWorker string
	err = tx.QueryRow(`SELECT node_id,name,worker FROM sessions WHERE id=?`, newSessionID).Scan(&newNode, &newName, &newWorker)
	if errors.Is(err, sql.ErrNoRows) {
		return false, reject("unknown session %s", newSessionID)
	}
	if err != nil {
		return false, err
	}
	if newNode != nodeID {
		return false, reject("the replacing session is on another machine")
	}
	if newWorker != "" {
		return false, reject("a worker does not take over a restarted session")
	}
	name = replacedSessionName(name, newName)
	now := s.now().UnixNano()

	// The caller role of running and queued workers and the ratings that were
	// waiting for this caller.
	steps := []struct {
		query string
		args  []any
	}{
		{`UPDATE sessions SET worker=json_set(worker,'$.owner',?) WHERE worker<>'' AND json_valid(worker) AND json_extract(worker,'$.owner')=?`, []any{newSessionID, sessionID}},
		{`UPDATE worker_starts SET owner=?, caller_agent_id=(SELECT agent_id FROM sessions WHERE id=?) WHERE owner=? AND state IN ('open','starting')`, []any{newSessionID, newSessionID, sessionID}},
		{`UPDATE worker_runs SET caller_session_id=? WHERE caller_session_id=?`, []any{newSessionID, sessionID}},
		{`UPDATE worker_rate_offers SET caller=? WHERE caller=?`, []any{newSessionID, sessionID}},
		// The caller's open changes and the steps it has claimed.
		{`UPDATE delivery_changes SET caller=?, updated_at=? WHERE caller=? AND state IN ('open','merged')`, []any{newSessionID, now, sessionID}},
		{`UPDATE delivery_step_tasks SET claimed_by=? WHERE claimed_by=? AND change_id IN (SELECT id FROM delivery_changes WHERE state IN ('open','merged'))`, []any{newSessionID, sessionID}},
		// The open requests follow the new record instead of a dead ID.
		{`UPDATE inbox_items SET session_id=? WHERE session_id=?`, []any{newSessionID, sessionID}},
		// The lasting binding: the agent and the repo, and the lead places
		// the session held.
		{`UPDATE sessions SET agent_id=COALESCE(NULLIF((SELECT agent_id FROM sessions WHERE id=?),''),agent_id), repo_id=COALESCE((SELECT repo_id FROM sessions WHERE id=?),repo_id) WHERE id=?`, []any{sessionID, sessionID, newSessionID}},
		{`UPDATE agents SET lead_session=? WHERE lead_session=?`, []any{newSessionID, sessionID}},
		{`UPDATE repo_leads SET lead_session=? WHERE lead_session=?`, []any{newSessionID, sessionID}},
		{`UPDATE teams SET lead_session=? WHERE lead_session=?`, []any{newSessionID, sessionID}},
		// A merger place follows the conversation, unless the new record is
		// already the merger of another project.
		{`UPDATE project_mergers SET session_id=? WHERE session_id=? AND NOT EXISTS (SELECT 1 FROM project_mergers WHERE session_id=?)`, []any{newSessionID, sessionID, newSessionID}},
	}
	for _, step := range steps {
		if _, err := tx.Exec(step.query, step.args...); err != nil {
			return false, err
		}
	}
	// The step notices move before the inbox, so an old duplicate notice is
	// expired here and not handed to the new session as well.
	if _, err := moveStepNotices(tx, sessionID, newSessionID); err != nil {
		return false, err
	}
	// The inbox: anything the session has not been given yet follows the new
	// record.
	if _, err := tx.Exec(`UPDATE messages SET recipient=? WHERE recipient=? AND state IN `+undeliveredStates, newSessionID, sessionID); err != nil {
		return false, err
	}
	// The name belongs to the conversation, so both records carry it: the live
	// one shows it, and the replaced one says it was replaced by it.
	if _, err := tx.Exec(`INSERT INTO profiles(session_id,name,function,project,capabilities,task,expertise,updated_at)
		SELECT ?,name,function,project,capabilities,task,expertise,? FROM profiles WHERE session_id=?
		ON CONFLICT(session_id) DO NOTHING`, newSessionID, now, sessionID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE sessions SET name=? WHERE id=?`, name, newSessionID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE sessions SET name=?, replaced_by=?, online=0, status='replaced', waiting='' WHERE id=?`,
		name, newSessionID, sessionID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// undeliveredStates are the message states a session has not been given yet.
const undeliveredStates = `('held','stored','dispatched','failed')`

// moveStepNotices moves the old session's notices of steps that still have
// work to the new session, and reports how many rows it changed. Decision 0148
// sends a caller step to any live session of the agent, so both sessions can
// hold a notice for the same step. The new session's notice stays. The old
// one is expired and dropped, so it neither wakes the old session nor reaches
// the new one twice.
func moveStepNotices(tx *sql.Tx, oldID, newID string) (int64, error) {
	const duplicate = `session_id=? AND change_id IN (SELECT id FROM delivery_changes WHERE state IN ('open','merged'))
		AND EXISTS (SELECT 1 FROM delivery_step_messages n WHERE n.session_id=? AND n.change_id=delivery_step_messages.change_id
		AND n.revision=delivery_step_messages.revision AND n.step=delivery_step_messages.step
		AND n.for_step=delivery_step_messages.for_step AND n.after_merge=delivery_step_messages.after_merge)`
	if _, err := tx.Exec(`UPDATE messages SET state='expired' WHERE state IN `+undeliveredStates+`
		AND id IN (SELECT message_id FROM delivery_step_messages WHERE `+duplicate+`)`, oldID, newID); err != nil {
		return 0, err
	}
	var changed int64
	for _, step := range []struct {
		query string
		args  []any
	}{
		{`DELETE FROM delivery_step_messages WHERE ` + duplicate, []any{oldID, newID}},
		{`UPDATE delivery_step_messages SET session_id=? WHERE session_id=? AND change_id IN (SELECT id FROM delivery_changes WHERE state IN ('open','merged'))`, []any{newID, oldID}},
	} {
		res, err := tx.Exec(step.query, step.args...)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		changed += n
	}
	return changed, nil
}

// ReplaceSessionForOwner is the owner's repair for a restart that already
// orphaned a session. It uses the same move as the automatic restart path, and
// the old session's own node, so the owner or the CTO may ask from any
// machine. It writes one Desk line saying who moved the work and where.
func (s *Store) ReplaceSessionForOwner(oldSessionID, newSessionID, actor string) (protocol.SessionReplaceResult, error) {
	res := protocol.SessionReplaceResult{OldSession: oldSessionID, NewSession: newSessionID}
	old, ok, err := s.Agent(oldSessionID)
	if err != nil {
		return res, err
	}
	if !ok {
		return res, reject("unknown session %s", oldSessionID)
	}
	moved, err := s.ReplaceSession(old.NodeID, oldSessionID, newSessionID)
	if err != nil {
		return res, err
	}
	res.Moved, res.OldName = moved, old.Profile.Name
	if !moved {
		return res, nil
	}
	if fresh, ok, err := s.Agent(newSessionID); err == nil && ok {
		res.NewName = fresh.Profile.Name
	}
	source, who := "owner", "The owner"
	if actor == "cto" {
		source, who = "cto", "The CTO"
	}
	text := fmt.Sprintf("%s moved the work of %s (%s) to %s (%s)", who,
		oldSessionID, res.OldName, newSessionID, res.NewName)
	if err := s.deskLog(s.db, source, "session", oldSessionID, text, "", nil); err != nil {
		return res, err
	}
	return res, nil
}

// replacedSessionName picks the name a restarted conversation keeps. An
// automatic name with the lower number wins, so a record renamed to
// "<name> 3" while the live one took "<name> 2" keeps the name the
// conversation had. A name the owner chose beats an automatic one.
func replacedSessionName(old, fresh string) string {
	if old == "" || old == fresh {
		return fresh
	}
	if fresh == "" {
		return old
	}
	oldBase, oldN := automaticNameParts(old)
	newBase, newN := automaticNameParts(fresh)
	if oldBase != newBase {
		return old
	}
	if oldN <= newN {
		return old
	}
	return fresh
}

// automaticNameParts splits "shop" and "shop 3" into the base and the number.
// The number of a name without one is 0, so it sorts before "shop 2".
func automaticNameParts(name string) (base string, n int) {
	if i := strings.LastIndexByte(name, ' '); i > 0 {
		if v, err := strconv.Atoi(name[i+1:]); err == nil && v >= 2 && strconv.Itoa(v) == name[i+1:] {
			return name[:i], v
		}
	}
	return name, 0
}

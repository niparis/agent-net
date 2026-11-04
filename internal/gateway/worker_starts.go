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

// workerStartMinVersion is the first release that tracks worker starts.
const workerStartMinVersion = "0.4.127"

// daemonTracksStarts tells whether a daemon version tracks start references.
func daemonTracksStarts(version string) bool {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if version == "" {
		return false
	}
	if version == "dev" {
		return true
	}
	have, ok := parseReleaseVersion(version)
	if !ok {
		return false
	}
	want, _ := parseReleaseVersion(workerStartMinVersion)
	for i := range want {
		if have[i] != want[i] {
			return have[i] > want[i]
		}
	}
	return true
}

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

// startSession records the session a start became, when the machine's launch
// answer names it.
func (s *Store) startSession(nodeID, ref, sessionID string) error {
	if ref == "" || sessionID == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE worker_starts SET session_id=? WHERE (ref=? OR (legacy_ref=? AND legacy_ref<>'')) AND node_id=? AND session_id=''`, sessionID, ref, ref, nodeID)
	return err
}

// recordLegacyQueueID maps the gateway's StartRef to the legacy queue ID an older daemon returned.
func (s *Store) recordLegacyQueueID(nodeID, ref, legacyRef string) error {
	if ref == "" || legacyRef == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE worker_starts SET legacy_ref=? WHERE ref=? AND node_id=?`, legacyRef, ref, nodeID)
	return err
}

// markStartUnknown marks an open start as unknown when its launch call timed out
// or disconnected. It stays open until a started or not-started report arrives.
func (s *Store) markStartUnknown(nodeID, ref string) error {
	if ref == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE worker_starts SET launch_state='unknown' WHERE (ref=? OR (legacy_ref=? AND legacy_ref<>'')) AND node_id=? AND state='open'`, ref, ref, nodeID)
	return err
}

// verifyStart refuses a reference that belongs to another machine, another
// owner or another worker than the one the call names. A reference from an
// older daemon (before start tracking) falls back to untracked if not found.
// A node whose daemon tracks starts must provide a valid tracked reference
// that is bound to this worker session.
func (s *Store) verifyStart(nodeID, ref, owner, sessionID string) error {
	v, _ := s.NodeVersion(nodeID)
	tracks := daemonTracksStarts(v)
	if ref == "" {
		if tracks {
			return reject("start reference required")
		}
		return nil
	}
	var rn, ro, rs string
	err := s.db.QueryRow(`SELECT node_id, owner, session_id FROM worker_starts WHERE ref=? OR (legacy_ref=? AND legacy_ref<>'')`, ref, ref).Scan(&rn, &ro, &rs)
	if errors.Is(err, sql.ErrNoRows) {
		if tracks {
			return reject("start %s is not tracked", ref)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if rn != nodeID || ro != owner || rs == "" || rs != sessionID {
		return reject("start %s does not belong to that machine, owner and worker", ref)
	}
	return nil
}

// closeStart closes an open start of this machine: its worker is running and
// got its task. A start that is not open, or unknown, is left as it is.
func (s *Store) closeStart(nodeID, ref, sessionID string) error {
	if ref == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE worker_starts SET state='closed', closed_at=?, session_id=CASE WHEN session_id='' THEN ? ELSE session_id END
		WHERE (ref=? OR (legacy_ref=? AND legacy_ref<>'')) AND node_id=? AND state='open'`, s.now().UnixNano(), sessionID, ref, ref, nodeID)
	return err
}

// closeLegacyStart reconciles and closes the oldest open start for a legacy daemon
// that sent worker.queued_start without a Ref.
func (s *Store) closeLegacyStart(nodeID, owner, project, sessionID string) (string, error) {
	var ref string
	err := s.db.QueryRow(`SELECT ref FROM worker_starts WHERE node_id=? AND owner=? AND project=? AND state='open' ORDER BY created_at ASC LIMIT 1`,
		nodeID, owner, project).Scan(&ref)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return ref, s.closeStart(nodeID, ref, sessionID)
}

// abandonStart ends an open start that will never be a worker and needs no
// notice, because the caller already knows: the machine refused the launch, or
// the owner cancelled the queued start.
func (s *Store) abandonStart(nodeID, ref string) error {
	if ref == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE worker_starts SET state='not_started', closed_at=? WHERE (ref=? OR (legacy_ref=? AND legacy_ref<>'')) AND node_id=? AND state='open'`, s.now().UnixNano(), ref, ref, nodeID)
	return err
}

// abandonLegacyStart reconciles and abandons the oldest open start for a legacy daemon
// report that arrived without a Ref.
func (s *Store) abandonLegacyStart(nodeID, owner, project string) error {
	var ref string
	err := s.db.QueryRow(`SELECT ref FROM worker_starts WHERE node_id=? AND owner=? AND project=? AND state='open' ORDER BY created_at ASC LIMIT 1`,
		nodeID, owner, project).Scan(&ref)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.abandonStart(nodeID, ref)
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
	err = tx.QueryRow(`SELECT ref,node_id,owner,project,harness,permissions,state FROM worker_starts WHERE (ref=? OR (legacy_ref=? AND legacy_ref<>'')) AND node_id=?`, ref, ref, nodeID).
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
	if _, err = tx.Exec(`UPDATE worker_starts SET state='not_started', closed_at=? WHERE (ref=? OR (legacy_ref=? AND legacy_ref<>'')) AND state='open'`, s.now().UnixNano(), ref, ref); err != nil {
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

// OpenStarts counts the starts that are still open: the gateway has not heard
// that they became workers or that they did not. A start whose end the gateway
// should have been told of must not stay here.
func (s *Store) OpenStarts() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_starts WHERE state='open'`).Scan(&n)
	return n, err
}

const (
	defaultUnknownStartTimeout = 10 * time.Minute
	defaultLegacyStartTimeout  = 24 * time.Hour
)

// ExpireStarts closes open starts that have timed out:
// 1. Unknown launches (timed out or disconnected) are closed with one not-started notice.
// 2. Unmatched legacy starts are closed without a duplicate notice.
func (s *Store) ExpireStarts(unknownTimeout, legacyTimeout time.Duration) ([]protocol.Message, error) {
	if unknownTimeout <= 0 {
		unknownTimeout = defaultUnknownStartTimeout
	}
	if legacyTimeout <= 0 {
		legacyTimeout = defaultLegacyStartTimeout
	}
	now := s.now()
	var notices []protocol.Message

	// 1. Unknown launches
	unknownCutoff := now.Add(-unknownTimeout).UnixNano()
	rows, err := s.db.Query(`SELECT ref, node_id FROM worker_starts WHERE state='open' AND launch_state='unknown' AND created_at < ?`, unknownCutoff)
	if err != nil {
		return nil, err
	}
	type target struct{ ref, nodeID string }
	var unknowns []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.ref, &t.nodeID); err == nil {
			unknowns = append(unknowns, t)
		}
	}
	rows.Close()

	for _, u := range unknowns {
		host := s.hostname(u.nodeID)
		_, n, noticed, first, err := s.declareNotStarted(u.nodeID, u.ref, host, "the machine did not confirm starting the worker")
		if err == nil && first && noticed {
			notices = append(notices, n)
		}
	}

	// 2. Unmatched legacy starts
	legacyCutoff := now.Add(-legacyTimeout).UnixNano()
	rows, err = s.db.Query(`SELECT ws.ref, ws.node_id, ws.legacy_ref, COALESCE(n.version, '') FROM worker_starts ws LEFT JOIN nodes n ON n.id = ws.node_id WHERE ws.state='open' AND ws.created_at < ?`, legacyCutoff)
	if err != nil {
		return notices, err
	}
	type legacyTarget struct{ ref, nodeID, legacyRef, version string }
	var candidates []legacyTarget
	for rows.Next() {
		var lt legacyTarget
		if err := rows.Scan(&lt.ref, &lt.nodeID, &lt.legacyRef, &lt.version); err == nil {
			candidates = append(candidates, lt)
		}
	}
	rows.Close()

	for _, c := range candidates {
		if c.legacyRef != "" || !daemonTracksStarts(c.version) {
			_ = s.abandonStart(c.nodeID, c.ref)
		}
	}

	return notices, nil
}

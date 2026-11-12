package gateway

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// sessionKeepalive is how long a report that repeats the last one may be
// skipped. A daemon sends a report on every activity change, and most repeat
// what the gateway stores. After this time the gateway writes the row again,
// so last_seen stays fresh and a column some other path changed is restored.
//
// Liveness does not depend on this: a session goes offline when its daemon
// removes it, when a sync leaves it out and when its node disconnects.
const sessionKeepalive = 2 * time.Minute

// sessionSeen is the last report written for one session.
type sessionSeen struct {
	hash     [sha256.Size]byte
	id       string
	lastSeen int64  // the last_seen value that write left in the row
	columns  string // sessionDispatchKey of the row after that write
}

func sessionSeenKey(nodeID string, in protocol.SessionInfo) string {
	return nodeID + "\x00" + in.Harness + "\x00" + in.NativeID
}

// skipSessionReport says whether a report repeats the one already written and
// the row still looks as that write left it. A row that went offline, was
// archived or lost its harness, or whose last_seen moved, is written again.
func (s *Store) skipSessionReport(key string, hash [sha256.Size]byte) (string, bool) {
	v, ok := s.upsertSeen.Load(key)
	if !ok {
		return "", false
	}
	seen := v.(sessionSeen)
	if seen.hash != hash {
		return "", false
	}
	if s.now().UnixNano()-seen.lastSeen >= int64(sessionKeepalive) {
		return "", false
	}
	// The row must still be what that write left, in every column the report
	// sets: a column changed by another path (a name repair, an offline mark)
	// is put right by writing again.
	var lastSeen int64
	if err := s.db.QueryRow(`SELECT last_seen FROM sessions WHERE id=?`, seen.id).Scan(&lastSeen); err != nil || lastSeen != seen.lastSeen {
		return "", false
	}
	if cols, err := sessionColumnsByID(s.db, seen.id); err != nil || cols != seen.columns {
		return "", false
	}
	return seen.id, true
}

// sessionDispatchKey reads the columns of a session that the delivery sweeps
// depend on, as one string. Time stamps that advance with activity and a
// worker's token counters are left out. A session that is not stored yet gives "".
func sessionDispatchKey(q interface {
	QueryRow(string, ...any) *sql.Row
}, nodeID, harness, native string) (string, error) {
	return sessionColumns(q, `node_id=? AND harness=? AND native_id=?`, nodeID, harness, native)
}

func sessionColumnsByID(q interface {
	QueryRow(string, ...any) *sql.Row
}, id string) (string, error) {
	return sessionColumns(q, `id=?`, id)
}

func sessionColumns(q interface {
	QueryRow(string, ...any) *sql.Row
}, where string, args ...any) (string, error) {
	var key string
	err := q.QueryRow(`SELECT json_array(id,online,status,waiting,name,title,cwd,pid,delivery,mode,model,CASE WHEN json_valid(worker) THEN json_remove(worker,'$.context_tokens','$.context_limit','$.tokens') ELSE worker END,worker_title,worker_run_active,
		repo_root,repo_project,repo_remote,repo_branch,repo_worktree,repo_description,repo_id,agent_id,no_project,graph_excluded,
		limited_until,limited_reason,idle_compacted_at,tools_version,tools_old,archived_at,harness_lost)
		FROM sessions WHERE `+where, args...).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return key, err
}

// UpsertSessionReport is UpsertSession that also says whether the report
// changed what the delivery sweeps read (a new session, one that came online,
// a new status, wait, project, folder or limit). A report that only moves the
// activity time does not, and the caller may skip those sweeps.
func (s *Store) UpsertSessionReport(nodeID string, in protocol.SessionInfo) (id string, relevant bool, err error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return "", false, err
	}
	key, hash := sessionSeenKey(nodeID, in), sha256.Sum256(raw)
	if id, ok := s.skipSessionReport(key, hash); ok {
		return id, false, nil
	}
	if _, err := s.ExpireSessionAgentStarts(); err != nil {
		return "", false, err
	}
	started := s.now().UnixNano()
	tx, err := s.db.Begin()
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	before, err := sessionDispatchKey(tx, nodeID, in.Harness, in.NativeID)
	if err != nil {
		return "", false, err
	}
	id, created, err := s.upsertSession(tx, nodeID, in)
	if err != nil {
		return "", false, err
	}
	after, err := sessionDispatchKey(tx, nodeID, in.Harness, in.NativeID)
	if err != nil {
		return "", false, err
	}
	var lastSeen int64
	if err = tx.QueryRow(`SELECT last_seen FROM sessions WHERE id=?`, id).Scan(&lastSeen); err != nil {
		return "", false, err
	}
	if err = tx.Commit(); err != nil {
		return "", false, err
	}
	if created {
		s.logNewSessions([]newSession{{id, nodeID, in}})
	}
	if lastSeen >= started {
		s.upsertSeen.Store(key, sessionSeen{hash: hash, id: id, lastSeen: lastSeen, columns: after})
	} else {
		s.upsertSeen.Delete(key)
	}
	return id, created || before != after, nil
}

package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/dbfile"
	"github.com/niparis/agent-net/internal/protocol"
	_ "modernc.org/sqlite"
)

// Rejection is an error caused by the caller breaking a rule, not by a fault.
type Rejection string

func (r Rejection) Error() string { return string(r) }

func reject(format string, args ...any) error { return Rejection(fmt.Sprintf(format, args...)) }

// Store is the gateway's SQLite database.
type Store struct {
	db                *sql.DB
	graphJunkRemoved  int64
	graphJunkMigrated bool
	now               func() time.Time
	// logf writes a structured log line; the gateway sets it. Never pass a
	// message body to it.
	logf func(event string, kv ...any)

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// migrations are applied in order. Never edit one that has shipped.
var migrations = []string{`
CREATE TABLE nodes (
	id TEXT PRIMARY KEY, hostname TEXT NOT NULL, os TEXT NOT NULL,
	token_hash TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL);
CREATE TABLE enroll_codes (
	code_hash TEXT PRIMARY KEY, expires_at INTEGER NOT NULL, used_at INTEGER);
CREATE TABLE sessions (
	id TEXT PRIMARY KEY, node_id TEXT NOT NULL, harness TEXT NOT NULL, native_id TEXT NOT NULL,
	name TEXT NOT NULL DEFAULT '', cwd TEXT NOT NULL DEFAULT '', pid INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'unknown', delivery TEXT NOT NULL DEFAULT '',
	last_seen INTEGER NOT NULL, online INTEGER NOT NULL DEFAULT 0,
	UNIQUE (node_id, harness, native_id));
CREATE TABLE profiles (
	session_id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', function TEXT NOT NULL DEFAULT '',
	project TEXT NOT NULL DEFAULT '', capabilities TEXT NOT NULL DEFAULT '[]',
	task TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL);
CREATE TABLE messages (
	id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, parent_id TEXT NOT NULL DEFAULT '',
	sender TEXT NOT NULL, recipient TEXT NOT NULL, type TEXT NOT NULL, body TEXT NOT NULL,
	hop INTEGER NOT NULL, idem_key TEXT NOT NULL, created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL, state TEXT NOT NULL,
	UNIQUE (sender, idem_key));
CREATE INDEX messages_conv ON messages (conversation_id);
CREATE INDEX messages_state ON messages (state);
CREATE TABLE deliveries (
	id INTEGER PRIMARY KEY AUTOINCREMENT, message_id TEXT NOT NULL, state TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL);
CREATE TABLE events (
	id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, kind TEXT NOT NULL,
	node_id TEXT NOT NULL DEFAULT '', session_id TEXT NOT NULL DEFAULT '',
	message_id TEXT NOT NULL DEFAULT '', detail TEXT NOT NULL DEFAULT '');
`, `
ALTER TABLE nodes ADD COLUMN version TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN seen_at INTEGER NOT NULL DEFAULT 0;
`, `
CREATE TABLE mcp_servers (
	name TEXT PRIMARY KEY, node_id TEXT NOT NULL, spec TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	added_by_node TEXT NOT NULL DEFAULT '', added_by_session TEXT NOT NULL DEFAULT '',
	added_by_harness TEXT NOT NULL DEFAULT '', added_at INTEGER NOT NULL,
	health TEXT NOT NULL DEFAULT 'unknown', health_detail TEXT NOT NULL DEFAULT '',
	health_at INTEGER NOT NULL DEFAULT 0, tools TEXT NOT NULL DEFAULT '[]');
`, `
ALTER TABLE sessions ADD COLUMN repo_root TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN repo_project TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN repo_remote TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN repo_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN repo_worktree TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN harnesses TEXT NOT NULL DEFAULT 'null';
ALTER TABLE nodes ADD COLUMN harnesses_at INTEGER NOT NULL DEFAULT 0;
CREATE INDEX events_at ON events (at);
CREATE INDEX events_kind ON events (kind);
CREATE INDEX messages_created ON messages (created_at);
CREATE INDEX deliveries_at ON deliveries (at);
CREATE INDEX sessions_seen ON sessions (online, last_seen);
`, `
CREATE TABLE projects (
	node_id TEXT NOT NULL, path TEXT NOT NULL, name TEXT NOT NULL,
	remote TEXT NOT NULL DEFAULT '', default_branch TEXT NOT NULL DEFAULT '',
	added_by_session TEXT NOT NULL DEFAULT '', added_by_harness TEXT NOT NULL DEFAULT '',
	added_at INTEGER NOT NULL, checked_at INTEGER NOT NULL,
	PRIMARY KEY (node_id, path));
`, `
ALTER TABLE sessions ADD COLUMN mode TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN model TEXT NOT NULL DEFAULT '';
`, `
ALTER TABLE sessions ADD COLUMN worker TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN launch INTEGER NOT NULL DEFAULT -1;
`, `
ALTER TABLE sessions ADD COLUMN repo_description TEXT NOT NULL DEFAULT '';
`, `
ALTER TABLE sessions ADD COLUMN peer_trust INTEGER NOT NULL DEFAULT 0;
ALTER TABLE profiles ADD COLUMN expertise TEXT NOT NULL DEFAULT '';
`, `
ALTER TABLE sessions ADD COLUMN delegation TEXT NOT NULL DEFAULT '';
CREATE TABLE project_policies (
	node_id TEXT NOT NULL, project TEXT NOT NULL, policy TEXT NOT NULL, updated_at INTEGER NOT NULL,
	PRIMARY KEY (node_id, project));
`, `
CREATE TABLE model_failures (
	node_id TEXT NOT NULL, harness TEXT NOT NULL, model TEXT NOT NULL, at INTEGER NOT NULL,
	PRIMARY KEY (node_id, harness, model));
`, `
ALTER TABLE nodes ADD COLUMN reported_hostname TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
UPDATE nodes SET reported_hostname = hostname;
`, `
ALTER TABLE sessions ADD COLUMN worker_started INTEGER NOT NULL DEFAULT 0;
`, `
ALTER TABLE sessions ADD COLUMN tools_version TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN tools_old INTEGER NOT NULL DEFAULT 0;
`, `
ALTER TABLE sessions ADD COLUMN created_at INTEGER NOT NULL DEFAULT 0;
UPDATE sessions SET created_at = last_seen;
`, `
ALTER TABLE sessions ADD COLUMN waiting TEXT NOT NULL DEFAULT '';
`, `
CREATE TABLE owner_settings (
	key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL);
`, `
CREATE TABLE graph_nodes (name TEXT PRIMARY KEY, kind TEXT NOT NULL, created_at INTEGER NOT NULL);
CREATE TABLE graph_charter (node TEXT NOT NULL, attr TEXT NOT NULL, value TEXT NOT NULL, updated_at INTEGER NOT NULL, updated_by TEXT NOT NULL, PRIMARY KEY(node, attr));
CREATE TABLE graph_edges (source TEXT NOT NULL, target TEXT NOT NULL, kind TEXT NOT NULL, mode TEXT NOT NULL, rule TEXT NOT NULL, PRIMARY KEY(source, target, kind));
CREATE TABLE graph_history (id INTEGER PRIMARY KEY AUTOINCREMENT, type TEXT NOT NULL, node TEXT NOT NULL, attr TEXT NOT NULL, old_value TEXT NOT NULL, new_value TEXT NOT NULL, at INTEGER NOT NULL, actor TEXT NOT NULL);
CREATE INDEX graph_history_node ON graph_history(node, id);
`, `
ALTER TABLE messages ADD COLUMN sender_cto INTEGER NOT NULL DEFAULT 0;
`, `
ALTER TABLE messages ADD COLUMN owner_approved INTEGER NOT NULL DEFAULT 0;
`, `
INSERT OR IGNORE INTO graph_edges(source,target,kind,mode,rule)
SELECT DISTINCT '*',COALESCE(NULLIF(s.repo_project,''),p.project,''),'depends','','auto'
FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id
WHERE s.peer_trust=1 AND COALESCE(NULLIF(s.repo_project,''),p.project,'')<>'';
`, `
CREATE TABLE graph_node_settings (node TEXT NOT NULL, setting TEXT NOT NULL, value TEXT NOT NULL, updated_at INTEGER NOT NULL, updated_by TEXT NOT NULL, PRIMARY KEY(node, setting));
`, `
CREATE TABLE graph_outcomes (source TEXT NOT NULL, target TEXT NOT NULL, message_id TEXT NOT NULL, reply_id TEXT NOT NULL PRIMARY KEY, outcome TEXT NOT NULL, reason TEXT NOT NULL, redirect_to TEXT NOT NULL, recommendation TEXT NOT NULL, at INTEGER NOT NULL);
CREATE INDEX graph_outcomes_edge ON graph_outcomes(source, target, at);
`, `
ALTER TABLE graph_outcomes ADD COLUMN resolution TEXT NOT NULL DEFAULT '';
`, `
CREATE TABLE outcome_overrules (message_id TEXT PRIMARY KEY, reply_id TEXT NOT NULL);
`, `
CREATE TABLE inbox_items (
 id TEXT PRIMARY KEY, kind TEXT NOT NULL, session_id TEXT NOT NULL, node TEXT NOT NULL,
 text TEXT NOT NULL, recommendation TEXT NOT NULL, message_id TEXT NOT NULL,
 conversation_id TEXT NOT NULL, created_at INTEGER NOT NULL, status TEXT NOT NULL,
 answer TEXT NOT NULL DEFAULT '', answered_at INTEGER NOT NULL DEFAULT 0);
CREATE INDEX inbox_items_order ON inbox_items(created_at, id);
`, `
ALTER TABLE messages ADD COLUMN approval_note TEXT NOT NULL DEFAULT '';
`, `
INSERT INTO inbox_items(id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,answer)
SELECT 'i_' || o.reply_id, CASE o.outcome WHEN 'reject' THEN 'rejection' ELSE 'escalation' END,
       request.recipient, o.target, 'Request: ' || substr(request.body,1,500) || char(10) || 'Reason: ' || o.reason, o.recommendation, o.reply_id, request.conversation_id, o.at,
       CASE WHEN o.resolution='' THEN 'open' ELSE 'answered' END, o.resolution
FROM graph_outcomes o JOIN messages request ON request.id=o.message_id
JOIN sessions sender ON sender.id=request.sender
WHERE o.outcome IN ('reject','escalate') AND sender.harness<>'owner';
`, `
CREATE TABLE model_successes (
	node_id TEXT NOT NULL, harness TEXT NOT NULL, model TEXT NOT NULL, at INTEGER NOT NULL,
	PRIMARY KEY (node_id, harness, model));
`, `
CREATE TABLE IF NOT EXISTS worker_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT, node_id TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '',
  harness TEXT NOT NULL, model TEXT NOT NULL DEFAULT '', outcome TEXT NOT NULL,
  started_at INTEGER NOT NULL DEFAULT 0, ended_at INTEGER NOT NULL, run_seconds INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS worker_runs_node ON worker_runs (node_id, harness, model, ended_at);
CREATE INDEX IF NOT EXISTS worker_runs_at ON worker_runs (ended_at);
CREATE TABLE IF NOT EXISTS playbook_notes (
  model TEXT NOT NULL, harness TEXT NOT NULL DEFAULT '', note TEXT NOT NULL,
  updated_at INTEGER NOT NULL, PRIMARY KEY (model, harness));
`, `
CREATE TABLE IF NOT EXISTS findings (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  subject_kind TEXT NOT NULL, subject TEXT NOT NULL, finding TEXT NOT NULL,
  evidence TEXT NOT NULL DEFAULT '[]', reporter TEXT NOT NULL, node_id TEXT NOT NULL DEFAULT '',
  at INTEGER NOT NULL, status TEXT NOT NULL DEFAULT 'new');
CREATE INDEX IF NOT EXISTS findings_subject ON findings (subject_kind, subject, at);
CREATE INDEX IF NOT EXISTS findings_reporter ON findings (reporter, at);
CREATE INDEX IF NOT EXISTS findings_status ON findings (status, at);
CREATE TABLE IF NOT EXISTS playbook (
  subject_kind TEXT NOT NULL, subject TEXT NOT NULL, harness TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL, source TEXT NOT NULL DEFAULT 'owner', support INTEGER NOT NULL DEFAULT 0,
  track_line TEXT NOT NULL DEFAULT '', pinned INTEGER NOT NULL DEFAULT 0,
  stale INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  PRIMARY KEY (subject_kind, subject, harness));
INSERT OR IGNORE INTO playbook (subject_kind, subject, harness, note, source, support, track_line, pinned, stale, created_at, updated_at)
  SELECT 'model', model, harness, note, 'owner', 0, '', 0, 0, updated_at, updated_at FROM playbook_notes;
`, `
ALTER TABLE sessions ADD COLUMN title TEXT NOT NULL DEFAULT '';
UPDATE sessions SET title = name;
`, `
DELETE FROM graph_nodes
WHERE kind='component'
  AND NOT EXISTS (SELECT 1 FROM graph_charter c WHERE c.node=graph_nodes.name AND c.value<>'')
  AND NOT EXISTS (SELECT 1 FROM graph_edges e WHERE e.source=graph_nodes.name OR e.target=graph_nodes.name)
  AND NOT EXISTS (SELECT 1 FROM graph_history h WHERE h.node=graph_nodes.name)
  AND ( (name GLOB 'tmp*' AND length(name)>=9 AND substr(name,4) NOT GLOB '*[^a-z0-9_]*')
        OR (EXISTS (SELECT 1 FROM sessions s WHERE s.repo_project=graph_nodes.name AND s.repo_root='')
            AND NOT EXISTS (SELECT 1 FROM sessions s WHERE s.repo_project=graph_nodes.name AND s.repo_root<>'')
            AND NOT EXISTS (SELECT 1 FROM projects p WHERE p.name=graph_nodes.name))
        OR NOT EXISTS (SELECT 1 FROM sessions s WHERE s.repo_project=graph_nodes.name AND (s.online=1 OR s.last_seen >= (CAST(strftime('%s','now') AS INTEGER)-604800)*1000000000)) );
`, `
ALTER TABLE sessions ADD COLUMN graph_excluded INTEGER NOT NULL DEFAULT 0;
ALTER TABLE projects ADD COLUMN graph_excluded INTEGER NOT NULL DEFAULT 0;
`, `
CREATE TABLE inventory_reports (node_id TEXT PRIMARY KEY, report TEXT NOT NULL);
`, `
CREATE TABLE tool_identities (id TEXT PRIMARY KEY, kind TEXT NOT NULL, name TEXT NOT NULL, hash TEXT NOT NULL, UNIQUE(kind,name,hash));
CREATE TABLE tool_locations (node_id TEXT NOT NULL, ordinal INTEGER NOT NULL, identity_id TEXT NOT NULL REFERENCES tool_identities(id), observation TEXT NOT NULL, PRIMARY KEY(node_id,ordinal));
CREATE INDEX tool_locations_identity ON tool_locations(identity_id);
INSERT OR IGNORE INTO tool_identities(id,kind,name,hash)
SELECT json_array(COALESCE(json_extract(j.value,'$.kind'),''),COALESCE(json_extract(j.value,'$.name'),''),COALESCE(json_extract(j.value,'$.hash'),'')), COALESCE(json_extract(j.value,'$.kind'),''), COALESCE(json_extract(j.value,'$.name'),''), COALESCE(json_extract(j.value,'$.hash'),'') FROM inventory_reports r, json_each(r.report,'$.items') j;
INSERT INTO tool_locations(node_id,ordinal,identity_id,observation)
SELECT r.node_id, CAST(j.key AS INTEGER), json_array(COALESCE(json_extract(j.value,'$.kind'),''),COALESCE(json_extract(j.value,'$.name'),''),COALESCE(json_extract(j.value,'$.hash'),'')), j.value FROM inventory_reports r, json_each(r.report,'$.items') j;
`, `
CREATE TABLE skill_versions (item TEXT NOT NULL, hash TEXT NOT NULL, commit_id TEXT NOT NULL, node_id TEXT NOT NULL, location TEXT NOT NULL, imported_at INTEGER NOT NULL, PRIMARY KEY(item,hash));
`, `
CREATE TABLE tool_revisions (item TEXT NOT NULL, revision INTEGER NOT NULL, assignment TEXT NOT NULL, version TEXT NOT NULL, actor TEXT NOT NULL, source TEXT NOT NULL, at INTEGER NOT NULL, PRIMARY KEY(item, revision));
`, `
CREATE TABLE tool_plans (id TEXT PRIMARY KEY, item TEXT NOT NULL, snapshot TEXT NOT NULL, plan TEXT NOT NULL, applied INTEGER NOT NULL DEFAULT 0);
`, `
CREATE TABLE tool_audit (id INTEGER PRIMARY KEY AUTOINCREMENT, item TEXT NOT NULL, plan_id TEXT NOT NULL, action TEXT NOT NULL, actor TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 0, targets TEXT NOT NULL, result TEXT NOT NULL, at INTEGER NOT NULL);
CREATE INDEX tool_audit_item ON tool_audit(item,id);
`, `
CREATE TABLE tool_results (item TEXT NOT NULL, revision INTEGER NOT NULL, target_index INTEGER NOT NULL, node_id TEXT NOT NULL, state TEXT NOT NULL, reason TEXT NOT NULL, PRIMARY KEY(item,revision,target_index));
`, `
INSERT OR IGNORE INTO tool_identities(id,kind,name,hash)
SELECT json_array('mcp',name,'federated-'||added_at),'mcp',name,'federated-'||added_at FROM mcp_servers;
`, `
CREATE TABLE IF NOT EXISTS mcp_import_plans (id TEXT PRIMARY KEY, snapshot TEXT NOT NULL, plan TEXT NOT NULL, applied INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS mcp_imports (name TEXT PRIMARY KEY, record TEXT NOT NULL);
`}

// OpenStore opens (or creates) the database at path and migrates it.
func OpenStore(path string) (*Store, error) {
	// The file is private from the start; SQLite gives -wal and -shm its mode.
	if err := dbfile.Create(path); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection keeps writes serialized and avoids lock errors.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, now: time.Now, logf: func(string, ...any) {}, buckets: map[string]*bucket{}}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	// Fix files that an older version made with the default mode.
	if err := dbfile.Secure(path); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var cur int
	err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&cur)
	if err != nil {
		return err
	}
	for i := cur; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		result, err := tx.Exec(migrations[i])
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if strings.Contains(migrations[i], "DELETE FROM graph_nodes") {
			s.graphJunkRemoved, _ = result.RowsAffected()
			s.graphJunkMigrated = true
		}
	}
	return nil
}

// SchemaVersion returns the highest applied migration.
func (s *Store) SchemaVersion() (int, error) {
	var v int
	err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&v)
	return v, err
}

func hashHex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func (s *Store) event(q execer, kind, nodeID, sessionID, messageID, detail string) {
	q.Exec(`INSERT INTO events (at, kind, node_id, session_id, message_id, detail) VALUES (?,?,?,?,?,?)`,
		s.now().UnixNano(), kind, nodeID, sessionID, messageID, detail)
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// Event appends an audit record.
func (s *Store) Event(kind, nodeID, sessionID, messageID, detail string) {
	s.event(s.db, kind, nodeID, sessionID, messageID, detail)
}

// Events returns audit records of one kind, oldest first.
func (s *Store) Events(kind string) ([]string, error) {
	rows, err := s.db.Query(`SELECT detail FROM events WHERE kind = ? ORDER BY id`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ---- enrollment ----

const codeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

// CreateEnrollCode makes a one-time code, stored hashed.
func (s *Store) CreateEnrollCode(ttl time.Duration) (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	code := make([]byte, 8)
	for i := range b {
		code[i] = codeAlphabet[int(b[i])%32]
	}
	_, err := s.db.Exec(`INSERT INTO enroll_codes (code_hash, expires_at) VALUES (?,?)`,
		hashHex(string(code)), s.now().Add(ttl).UnixNano())
	return string(code), err
}

// EnrollCodeValid reports whether a code is unexpired and unused, without
// consuming it.
func (s *Store) EnrollCodeValid(code string) (bool, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM enroll_codes WHERE code_hash = ? AND used_at IS NULL AND expires_at > ?`,
		hashHex(code), s.now().UnixNano()).Scan(&n)
	return n == 1, err
}

// ConsumeEnrollCode returns true once for a valid, unexpired, unused code.
func (s *Store) ConsumeEnrollCode(code string) (bool, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	now := s.now().UnixNano()
	res, err := s.db.Exec(`UPDATE enroll_codes SET used_at = ? WHERE code_hash = ? AND used_at IS NULL AND expires_at > ?`,
		now, hashHex(code), now)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) CreateNode(hostname, osName string) (nodeID, token string, err error) {
	nodeID = protocol.NewID("n")
	token = protocol.NewToken()
	_, err = s.db.Exec(`INSERT INTO nodes (id, hostname, reported_hostname, os, token_hash, created_at) VALUES (?,?,?,?,?,?)`,
		nodeID, hostname, hostname, osName, hashHex(token), s.now().UnixNano())
	if err == nil {
		s.Event("node.enrolled", nodeID, "", "", hostname)
	}
	return
}

func (s *Store) NodeByToken(token string) (nodeID string, ok bool, err error) {
	err = s.db.QueryRow(`SELECT id FROM nodes WHERE token_hash = ?`, hashHex(token)).Scan(&nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return nodeID, err == nil, err
}

// SetNodeHostname stores the host name a daemon reported. The column hostname
// is the name every listing uses: it stays the owner's display name when one
// is set (decision 0015), and follows the reported name otherwise.
func (s *Store) SetNodeHostname(nodeID, hostname string) error {
	if hostname == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE nodes SET reported_hostname = ?,
		hostname = CASE WHEN display_name != '' THEN display_name ELSE ? END WHERE id = ?`, hostname, hostname, nodeID)
	return err
}

// ---- sessions ----

// newSession is a session the gateway saw for the first time.
type newSession struct {
	id   string
	node string
	in   protocol.SessionInfo
}

// upsertSession stores one session. created is true for a session the gateway
// did not know.
func (s *Store) upsertSession(q interface {
	execer
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}, nodeID string, in protocol.SessionInfo) (id string, created bool, err error) {
	if in.Harness == "" || in.NativeID == "" {
		return "", false, reject("session needs harness and native_id")
	}
	if in.Status == "" {
		in.Status = "unknown"
	}
	var knownID, knownName string
	err = q.QueryRow(`SELECT id, name FROM sessions WHERE node_id=? AND harness=? AND native_id=?`,
		nodeID, in.Harness, in.NativeID).Scan(&knownID, &knownName)
	known := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	now := s.now().UnixNano()
	// Mode and model come from a daemon. Keep only values that make sense.
	if !protocol.ValidMode(in.Mode) {
		in.Mode = ""
	}
	in.Model = protocol.CleanModel(in.Model)
	// The owner pseudo-session is the person at a machine. A daemon makes one
	// per machine, with this fixed native ID, and it is never a worker.
	if protocol.IsOwner(in.Harness) {
		if in.NativeID != protocol.OwnerNativeID {
			return "", false, reject("an owner session must have native_id %q", protocol.OwnerNativeID)
		}
		in.Worker = nil
	}
	worker := ""
	var workerStarted int64
	if in.Worker != nil {
		raw, err := json.Marshal(in.Worker)
		if err != nil {
			return "", false, err
		}
		worker = string(raw)
		workerStarted = in.Worker.StartedAt * int64(time.Second)
	}
	in.Repo.Description = protocol.CleanDescription(in.Repo.Description)
	// The name is the project's (decision 0024); the harness name is only a
	// title for the TASK column. The owner pseudo-session keeps its own name.
	name := ""
	if protocol.IsOwner(in.Harness) {
		name = protocol.CleanModel(in.Name)
	} else if name, err = chooseSessionName(q, knownID, knownName, in); err != nil {
		return "", false, err
	}
	title := protocol.CleanModel(in.Name)
	if protocol.IsOwner(in.Harness) {
		title = ""
	}
	// A new record gets the ID the daemon asks for when that ID looks right
	// and nobody uses it: a worker whose record was lost keeps its ID.
	// Cleanup may delete a known row between the lookup and the insert.
	// Reuse its ID even if the insert no longer hits the uniqueness conflict.
	newID := knownID
	if !known {
		newID = protocol.NewID("s")
	}
	if !known && validSessionID(in.SessionID) {
		var used int
		if err := q.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = ?`, in.SessionID).Scan(&used); err != nil {
			return "", false, err
		}
		if used == 0 {
			newID = in.SessionID
		}
	}
	waiting := ""
	if w := protocol.CleanWaiting(in.Waiting); w != nil {
		if w.Since == 0 {
			w.Since = s.now().Unix()
		}
		if b, err := json.Marshal(w); err == nil {
			waiting = string(b)
		}
	}
	_, err = q.Exec(`INSERT INTO sessions (id, node_id, harness, native_id, name, title, cwd, pid, status, delivery, last_seen, online,
		repo_root, repo_project, repo_remote, repo_branch, repo_worktree, mode, model, worker, repo_description, worker_started,
		tools_version, tools_old, created_at, waiting, graph_excluded)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,1,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (node_id, harness, native_id) DO UPDATE SET
		name=excluded.name, title=excluded.title, cwd=excluded.cwd, pid=excluded.pid, status=excluded.status,
		delivery=excluded.delivery, last_seen=excluded.last_seen, online=1,
		repo_root=excluded.repo_root, repo_project=excluded.repo_project,
		repo_remote=excluded.repo_remote, repo_branch=excluded.repo_branch,
		repo_worktree=excluded.repo_worktree, mode=excluded.mode, model=excluded.model, worker=excluded.worker,
		repo_description=excluded.repo_description, worker_started=excluded.worker_started,
		tools_version=excluded.tools_version, tools_old=excluded.tools_old, waiting=excluded.waiting, graph_excluded=excluded.graph_excluded`,
		newID, nodeID, in.Harness, in.NativeID, name, title, in.Cwd, in.PID, in.Status, in.Delivery, now,
		in.Repo.Root, in.Repo.Project, in.Repo.Remote, in.Repo.Branch, in.Repo.Worktree, in.Mode, in.Model, worker,
		in.Repo.Description, workerStarted, protocol.CleanModel(in.ToolsVersion), boolInt(in.ToolsOld), now, waiting, graphExclusion(in.GraphExcluded, in.GraphChecked))
	if err != nil {
		return "", false, err
	}
	err = q.QueryRow(`SELECT id FROM sessions WHERE node_id=? AND harness=? AND native_id=?`,
		nodeID, in.Harness, in.NativeID).Scan(&id)
	if err != nil {
		return "", false, err
	}
	// The owner session is the person, not a new agent: no session.new for it.
	fresh := !known && !protocol.IsOwner(in.Harness)
	if fresh {
		s.event(q, "session.new", nodeID, id, "", sessionDetail(in, name))
	}
	return id, fresh, nil
}

// sessionBaseName is the base of a session's name: the project the daemon
// found (the main working tree, decision 0003), or the folder it runs in. A
// very old daemon that sends neither keeps the name it reported.
func sessionBaseName(in protocol.SessionInfo) string {
	name := strings.TrimSpace(in.Repo.Project)
	if name == "" {
		if cwd := strings.TrimSpace(in.Cwd); cwd != "" {
			name = filepath.Base(filepath.Clean(cwd))
		}
	}
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = strings.TrimSpace(in.Name)
	}
	if name = protocol.CleanModel(name); name == "" {
		name = in.Harness
	}
	if name == "" {
		name = "session"
	}
	return name
}

// automaticName is the candidate name for a session: the base, then " 2",
// " 3"; a worker is "<base> worker 1", "worker 2".
func automaticName(base string, worker bool, n int) string {
	if worker {
		return base + " worker " + strconv.Itoa(n)
	}
	if n <= 1 {
		return base
	}
	return base + " " + strconv.Itoa(n)
}

// isAutomaticName says whether name is one of the automatic names of this
// base, so a reporting session keeps it and a renamed project does not.
func isAutomaticName(name, base string, worker bool) bool {
	if worker {
		rest, ok := strings.CutPrefix(name, base+" worker ")
		if !ok {
			return false
		}
		n, err := strconv.Atoi(rest)
		return err == nil && n >= 1 && strconv.Itoa(n) == rest
	}
	if name == base {
		return true
	}
	rest, ok := strings.CutPrefix(name, base+" ")
	if !ok {
		return false
	}
	n, err := strconv.Atoi(rest)
	return err == nil && n >= 2 && strconv.Itoa(n) == rest
}

// chooseSessionName gives a session its name. A live session keeps the name it
// has while it fits its project. A new session, or one whose name another live
// session took while it was away, gets the lowest free number. Only live
// sessions hold a name (decision 0024).
func chooseSessionName(q interface {
	Query(string, ...any) (*sql.Rows, error)
}, sessionID, current string, in protocol.SessionInfo) (string, error) {
	base, worker := sessionBaseName(in), in.Worker != nil
	used := map[string]bool{}
	rows, err := q.Query(`SELECT COALESCE(NULLIF(p.name,''), s.name) FROM sessions s
		LEFT JOIN profiles p ON p.session_id = s.id WHERE s.online = 1 AND s.id <> ?`, sessionID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return "", err
		}
		if name != "" {
			used[name] = true
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if current != "" && !used[current] && isAutomaticName(current, base, worker) {
		return current, nil
	}
	first := 1
	if !worker {
		first = 0 // the base itself is the first name, then " 2"
	}
	for n := first; ; n++ {
		name := automaticName(base, worker, n)
		if !used[name] {
			return name, nil
		}
	}
}

// validSessionID says whether id has the shape of a session ID.
func validSessionID(id string) bool {
	if !strings.HasPrefix(id, "s_") || len(id) < 4 || len(id) > 40 {
		return false
	}
	for _, c := range id[2:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

func sessionDetail(in protocol.SessionInfo, name string) string {
	return strings.TrimPrefix(pairs("harness", in.Harness, "name", name, "title", in.Name, "project", in.Repo.Project,
		"branch", in.Repo.Branch, "cwd", in.Cwd), " ")
}

// logNewSessions writes the session.new log lines, after the transaction that
// stored them has committed.
func (s *Store) logNewSessions(list []newSession) {
	for _, n := range list {
		host := s.hostname(n.node)
		var name string
		s.db.QueryRow(`SELECT name FROM sessions WHERE id = ?`, n.id).Scan(&name)
		s.logf("session.new", "session", n.id, "node", n.node, "host", host, "harness", n.in.Harness,
			"name", name, "title", n.in.Name, "pid", n.in.PID, "cwd", n.in.Cwd, "project", n.in.Repo.Project,
			"git_root", n.in.Repo.Root, "worktree", n.in.Repo.Worktree, "remote", n.in.Repo.Remote, "branch", n.in.Repo.Branch)
	}
}

func (s *Store) hostname(nodeID string) string {
	var h string
	s.db.QueryRow(`SELECT hostname FROM nodes WHERE id = ?`, nodeID).Scan(&h)
	return h
}

func (s *Store) UpsertSession(nodeID string, in protocol.SessionInfo) (string, error) {
	id, created, err := s.upsertSession(s.db, nodeID, in)
	if err == nil && created {
		s.logNewSessions([]newSession{{id, nodeID, in}})
	}
	return id, err
}

// SyncSessions upserts the full set and marks the node's other sessions offline.
func (s *Store) SyncSessions(nodeID string, in []protocol.SessionInfo) ([]protocol.SessionInfo, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := make([]protocol.SessionInfo, 0, len(in))
	keep := map[string]bool{}
	var fresh []newSession
	for _, si := range in {
		id, created, err := s.upsertSession(tx, nodeID, si)
		if err != nil {
			return nil, err
		}
		if created {
			fresh = append(fresh, newSession{id, nodeID, si})
		}
		keep[id] = true
		si.SessionID = id
		out = append(out, si)
	}
	rows, err := tx.Query(`SELECT id FROM sessions WHERE node_id = ? AND online = 1`, nodeID)
	if err != nil {
		return nil, err
	}
	var stale []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		if !keep[id] {
			stale = append(stale, id)
		}
	}
	rows.Close()
	for _, id := range stale {
		if _, err := tx.Exec(`UPDATE sessions SET online = 0, last_seen = ? WHERE id = ?`, s.now().UnixNano(), id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.logNewSessions(fresh)
	return out, nil
}

// SetPeerTrust turns peer trust on or off for a session (decision 0012). The
// owner pseudo-session cannot be trusted: it is a person, not an agent. It
// returns false when the session does not exist.
func (s *Store) SetPeerTrust(sessionID string, on bool) (bool, error) {
	a, ok, err := s.Agent(sessionID)
	if err != nil || !ok || a.Harness == protocol.OwnerHarness {
		return false, err
	}
	project := projectOfSession(a)
	if project != "" {
		rule := "normal"
		if on {
			rule = "auto"
		}
		if err := s.SetEdge(protocol.Edge{From: "*", To: project, Kind: "depends", Rule: rule}, "owner"); err != nil {
			return false, err
		}
	}
	_, err = s.db.Exec(`UPDATE sessions SET peer_trust=? WHERE id=?`, boolInt(on), sessionID)
	return err == nil, err
}

func (s *Store) SessionNode(sessionID string) (string, error) {
	var n string
	err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id = ?`, sessionID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return n, err
}

func (s *Store) SetSessionOffline(nodeID, sessionID string) error {
	_, err := s.db.Exec(`UPDATE sessions SET online = 0, last_seen = ? WHERE id = ? AND node_id = ?`, s.now().UnixNano(), sessionID, nodeID)
	return err
}

// CleanWorkers deletes the worker records of a node that are offline and
// whose worker started longer ago than age. A record with no start time was
// kept by a gateway from before the column existed, so it counts as old. A
// record the machine lists in running is never deleted: its machine still
// runs that worker, even if the worker is offline for a moment (a daemon
// restart). It returns how many records it deleted.
func (s *Store) CleanWorkers(nodeID string, age time.Duration, running []string) (int, error) {
	if age <= 0 {
		return 0, nil
	}
	cut := s.now().Add(-age).UnixNano()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id FROM sessions WHERE node_id = ? AND online = 0 AND worker != ''
		AND (worker_started = 0 OR worker_started < ?)
        AND NOT EXISTS (SELECT 1 FROM graph_outcomes o JOIN messages req ON req.id=o.message_id JOIN sessions src ON src.id=req.sender WHERE o.resolution='' AND o.outcome IN ('reject','escalate') AND src.harness<>'owner' AND (sessions.id=req.sender OR sessions.id=req.recipient))`, nodeID, cut)
	if err != nil {
		return 0, err
	}
	keep := map[string]bool{}
	for _, id := range running {
		keep[id] = true
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		if !keep[id] {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err := tx.Exec(`DELETE FROM profiles WHERE session_id = ?`, id); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM sessions WHERE id = ?`, id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if len(ids) > 0 {
		s.logf("worker.cleaned", "node", nodeID, "workers", len(ids))
	}
	return len(ids), nil
}

// CleanWorktreeSessions deletes the offline sessions of one node whose working
// folder is inside dir (the folder where that node's daemon creates worker
// worktrees) and that are not worker records. They are ghosts: a user's own
// service once registered a worker's session as an ordinary one. Rows of
// other nodes, worker records and sessions elsewhere are never touched.
func (s *Store) CleanWorktreeSessions(nodeID, dir string) (int, error) {
	dir = strings.TrimRight(dir, "/")
	if !strings.HasPrefix(dir, "/") || dir == "" {
		return 0, nil
	}
	prefix := dir + "/"
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id FROM sessions WHERE node_id = ? AND online = 0 AND worker = ''
		AND substr(cwd, 1, ?) = ?
        AND NOT EXISTS (SELECT 1 FROM graph_outcomes o JOIN messages req ON req.id=o.message_id JOIN sessions src ON src.id=req.sender WHERE o.resolution='' AND o.outcome IN ('reject','escalate') AND src.harness<>'owner' AND (sessions.id=req.sender OR sessions.id=req.recipient))`, nodeID, len(prefix), prefix)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err := tx.Exec(`DELETE FROM profiles WHERE session_id = ?`, id); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM sessions WHERE id = ?`, id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func (s *Store) SetNodeOffline(nodeID string) error {
	_, err := s.db.Exec(`UPDATE sessions SET online = 0, last_seen = ? WHERE node_id = ? AND online = 1`, s.now().UnixNano(), nodeID)
	return err
}

// Announce stores what an agent says about itself: its current task, and
// optionally a name, a project and capability tags. Empty fields keep what is
// stored, so announcing a new task does not erase the rest. The function is
// not taken from the agent: it comes from the project's AGENTS.md (see
// RepoInfo.Description), so a "function" in the request is ignored.
func (s *Store) Announce(nodeID string, p protocol.ProfileAnnounce) (protocol.Profile, error) {
	owner, err := s.SessionNode(p.SessionID)
	if err != nil {
		return protocol.Profile{}, err
	}
	if owner != nodeID {
		return protocol.Profile{}, reject("unknown session %q", p.SessionID)
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Task = strings.TrimSpace(p.Task)
	p.Expertise = protocol.CleanExpertise(p.Expertise)
	caps := "[]"
	if p.Capabilities != nil {
		b, _ := json.Marshal(p.Capabilities)
		caps = string(b)
	}
	_, err = s.db.Exec(`INSERT INTO profiles (session_id, name, function, project, capabilities, task, updated_at, expertise)
		VALUES (?,?,'',?,?,?,?,?)
		ON CONFLICT (session_id) DO UPDATE SET
		name=CASE WHEN excluded.name='' THEN profiles.name ELSE excluded.name END,
		project=CASE WHEN excluded.project='' THEN profiles.project ELSE excluded.project END,
		capabilities=CASE WHEN ? THEN profiles.capabilities ELSE excluded.capabilities END,
		task=CASE WHEN excluded.task='' THEN profiles.task ELSE excluded.task END,
		expertise=CASE WHEN excluded.expertise='' THEN profiles.expertise ELSE excluded.expertise END,
		updated_at=excluded.updated_at`,
		p.SessionID, p.Name, p.Project, caps, p.Task, s.now().UnixNano(), p.Expertise, p.Capabilities == nil)
	if err != nil {
		return protocol.Profile{}, err
	}
	a, ok, err := s.Agent(p.SessionID)
	if err != nil || !ok {
		return protocol.Profile{}, err
	}
	return a.Profile, nil
}

const agentSelect = `SELECT s.id, s.node_id, n.hostname, s.harness, s.cwd, s.status, s.online, s.last_seen,
	COALESCE(NULLIF(p.name,''), s.name), s.repo_description, COALESCE(p.project,''),
	COALESCE(p.capabilities,'[]'), COALESCE(p.task,''),
	s.repo_root, s.repo_project, s.repo_remote, s.repo_branch, s.repo_worktree, s.mode, s.model, s.pid, s.worker, s.repo_description,
	s.peer_trust, COALESCE(p.expertise,''), s.delegation, s.title, s.created_at, s.tools_version, s.tools_old, s.waiting
	FROM sessions s JOIN nodes n ON n.id = s.node_id LEFT JOIN profiles p ON p.session_id = s.id`

func scanAgent(sc interface{ Scan(...any) error }) (protocol.AgentView, error) {
	var a protocol.AgentView
	var online int
	var lastSeen, created int64
	var caps, worker string
	var trust int
	var deleg, waiting string
	var toolsOld int
	err := sc.Scan(&a.SessionID, &a.NodeID, &a.Hostname, &a.Harness, &a.Cwd, &a.Status, &online, &lastSeen,
		&a.Profile.Name, &a.Profile.Function, &a.Profile.Project, &caps, &a.Profile.Task,
		&a.Repo.Root, &a.Repo.Project, &a.Repo.Remote, &a.Repo.Branch, &a.Repo.Worktree, &a.Mode, &a.Model, &a.PID, &worker, &a.Repo.Description,
		&trust, &a.Profile.Expertise, &deleg, &a.Title, &created, &a.ToolsVersion, &toolsOld, &waiting)
	if err != nil {
		return a, err
	}
	a.ToolsOld = toolsOld != 0
	if waiting != "" {
		var w protocol.Waiting
		if json.Unmarshal([]byte(waiting), &w) == nil {
			a.Waiting = protocol.CleanWaiting(&w)
		}
	}
	if deleg != "" {
		var p protocol.DelegationPolicy
		if json.Unmarshal([]byte(deleg), &p) == nil && !p.Empty() {
			a.Delegation = &protocol.DelegationView{DelegationPolicy: p, Scope: protocol.ScopeSession}
		}
	}
	a.PeerTrust = trust != 0
	if worker != "" {
		var w protocol.WorkerInfo
		if json.Unmarshal([]byte(worker), &w) == nil {
			a.Worker = &w
		}
	}
	a.Online = online == 1
	a.LastSeen = lastSeen / int64(time.Second)
	a.FirstSeen = created / int64(time.Second)
	if json.Unmarshal([]byte(caps), &a.Profile.Capabilities) != nil || a.Profile.Capabilities == nil {
		a.Profile.Capabilities = []string{}
	}
	return a, nil
}

func (s *Store) Agent(sessionID string) (protocol.AgentView, bool, error) {
	a, err := scanAgent(s.db.QueryRow(agentSelect+` WHERE s.id = ?`, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return a, false, nil
	}
	if err != nil {
		return a, false, err
	}
	one := []protocol.AgentView{a}
	if err := s.applyDelegation(one); err != nil {
		return a, false, err
	}
	if err := s.applyAgentCharters(one, false); err != nil {
		return a, false, err
	}
	return one[0], true, nil
}

func (s *Store) Agents(f protocol.QueryAgents) ([]protocol.AgentView, error) {
	rows, err := s.db.Query(agentSelect + ` ORDER BY s.last_seen DESC, s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.AgentView{}
	text := strings.ToLower(f.Text)
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		if f.OnlineOnly && !a.Online {
			continue
		}
		if protocol.IsOwner(a.Harness) && !f.IncludeOwners && f.Harness != protocol.OwnerHarness {
			continue
		}
		if f.Harness != "" && a.Harness != f.Harness {
			continue
		}
		if f.Project != "" && !strings.EqualFold(a.Profile.Project, f.Project) && !strings.EqualFold(a.Repo.Project, f.Project) {
			continue
		}
		if f.Capability != "" {
			found := false
			for _, c := range a.Profile.Capabilities {
				if strings.EqualFold(c, f.Capability) {
					found = true
				}
			}
			if !found {
				continue
			}
		}
		if text != "" {
			hay := strings.ToLower(strings.Join(append([]string{a.Profile.Name, a.Profile.Function,
				a.Profile.Project, a.Profile.Task, a.Profile.Expertise, a.Cwd, a.Hostname, a.Repo.Project, a.Repo.Branch, a.Repo.Remote}, a.Profile.Capabilities...), " "))
			if !strings.Contains(hay, text) {
				continue
			}
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close() // one connection: free it before the next query
	if err := s.applyDelegation(out); err != nil {
		return nil, err
	}
	if err := s.applyAgentCharters(out, true); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- messages ----

const msgSelect = `SELECT m.id, m.conversation_id, m.parent_id, m.sender, m.recipient, m.type, m.body, m.hop,
	m.idem_key, m.created_at, m.expires_at, m.state,
	COALESCE(NULLIF(p.name,''), s.name, ''), COALESCE(s.harness, ''), COALESCE(s.worker, '') != '', m.sender_cto, m.owner_approved, EXISTS(SELECT 1 FROM outcome_overrules oo WHERE oo.message_id=m.id), m.approval_note
	FROM messages m LEFT JOIN sessions s ON s.id = m.sender LEFT JOIN profiles p ON p.session_id = m.sender`

func scanMessage(sc interface{ Scan(...any) error }) (protocol.Message, error) {
	var m protocol.Message
	var created, expires int64
	err := sc.Scan(&m.ID, &m.ConversationID, &m.ParentID, &m.From, &m.To, &m.Type, &m.Body, &m.Hop,
		&m.IdempotencyKey, &created, &expires, &m.State, &m.FromName, &m.FromHarness, &m.FromWorker, &m.FromCTO, &m.OwnerApproved, &m.Overruled, &m.ApprovalNote)
	if m.FromCTO {
		m.FromHarness = "cto"
	}
	m.CreatedAt = time.Unix(0, created).UTC()
	m.ExpiresAt = time.Unix(0, expires).UTC()
	return m, err
}

type queryer interface {
	QueryRow(string, ...any) *sql.Row
}

func (s *Store) message(q queryer, where string, args ...any) (protocol.Message, bool, error) {
	m, err := scanMessage(q.QueryRow(msgSelect+" WHERE "+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return m, false, nil
	}
	return m, err == nil, err
}

func (s *Store) Message(id string) (protocol.Message, bool, error) {
	return s.message(s.db, "m.id = ?", id)
}

func (s *Store) allow(sender string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	b := s.buckets[sender]
	if b == nil {
		b = &bucket{tokens: protocol.RatePerMinute, last: now}
		s.buckets[sender] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * protocol.RatePerMinute / 60
	if b.tokens > protocol.RatePerMinute {
		b.tokens = protocol.RatePerMinute
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// StoreMessage applies every rule and commits the message. created is false
// when the idempotency key was seen before and the first message is returned.
func (s *Store) StoreMessage(p protocol.MsgSend) (msg protocol.Message, created bool, err error) {
	p.ReplyOutcome, err = protocol.CleanOutcome(p.ReplyOutcome)
	if err != nil {
		return msg, false, reject("%v", err)
	}
	if p.Outcome != "" && (p.ParentID == "" || (p.Type != "" && p.Type != protocol.TypeReply)) {
		return msg, false, reject("outcome requires a reply")
	}
	if len(p.Body) > protocol.MaxBodyBytes {
		return msg, false, reject("body is %d bytes, the limit is %d", len(p.Body), protocol.MaxBodyBytes)
	}
	if strings.TrimSpace(p.Body) == "" {
		return msg, false, reject("body is empty")
	}
	if p.Sender == p.Recipient {
		return msg, false, reject("cannot send a message to yourself")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return msg, false, err
	}
	defer tx.Rollback()

	if p.IdempotencyKey == "" {
		p.IdempotencyKey = protocol.NewID("k")
	}
	if first, ok, err := s.message(tx, "m.sender = ? AND m.idem_key = ?", p.Sender, p.IdempotencyKey); err != nil {
		return msg, false, err
	} else if ok {
		return first, false, nil
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = ?`, p.Sender).Scan(&n); err != nil || n == 0 {
		return msg, false, reject("unknown sender session %q", p.Sender)
	}
	if p.FromCTO {
		var harness string
		if err := tx.QueryRow(`SELECT harness FROM sessions WHERE id = ?`, p.Sender).Scan(&harness); err != nil || harness != protocol.OwnerHarness {
			return msg, false, reject("CTO sender requires an owner session")
		}
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = ?`, p.Recipient).Scan(&n); err != nil || n == 0 {
		return msg, false, reject("unknown recipient session %q", p.Recipient)
	}
	if !s.allow(p.Sender) {
		return msg, false, reject("rate limit: at most %d messages per minute per session", protocol.RatePerMinute)
	}

	now := s.now()
	m := protocol.Message{
		ID: protocol.NewID("m"), From: p.Sender, To: p.Recipient, Type: p.Type, Body: p.Body,
		IdempotencyKey: p.IdempotencyKey, CreatedAt: now.UTC(), ExpiresAt: now.Add(protocol.DefaultTTL).UTC(),
		State: protocol.StateStored, FromCTO: p.FromCTO,
	}
	if p.ParentID != "" {
		parent, ok, err := s.message(tx, "m.id = ?", p.ParentID)
		if err != nil {
			return msg, false, err
		}
		if !ok {
			return msg, false, reject("unknown parent message %q", p.ParentID)
		}
		if parent.To != p.Sender || parent.From != p.Recipient {
			return msg, false, reject("a reply must come from the parent's recipient and go to its sender")
		}
		m.ParentID = parent.ID
		m.ConversationID = parent.ConversationID
		m.Hop = parent.Hop + 1
		if m.Type == "" {
			m.Type = protocol.TypeReply
		}
	} else {
		m.ConversationID = protocol.NewID("c")
		if m.Type == "" {
			m.Type = protocol.TypeRequest
		}
	}
	switch m.Type {
	case protocol.TypeRequest, protocol.TypeReply, protocol.TypeNotification:
	default:
		return msg, false, reject("unknown message_type %q", m.Type)
	}
	if m.Type == protocol.TypeRequest && !m.FromCTO {
		var rule string
		err := tx.QueryRow(`SELECT e.rule FROM graph_edges e
			JOIN sessions src ON src.id=? JOIN sessions dst ON dst.id=?
			LEFT JOIN profiles sp ON sp.session_id=src.id LEFT JOIN profiles dp ON dp.session_id=dst.id
			WHERE e.target=COALESCE(NULLIF(dst.repo_project,''),dp.project,'')
			AND e.target<>'' AND (e.source=COALESCE(NULLIF(src.repo_project,''),sp.project,'') OR e.source='*')
			ORDER BY CASE WHEN e.source='*' THEN 1 ELSE 0 END, e.kind LIMIT 1`, m.From, m.To).Scan(&rule)
		if err != nil && err != sql.ErrNoRows {
			return msg, false, err
		}
		if rule == "ask" {
			m.State = protocol.StateHeld
		} else if rule == "never" {
			return msg, false, reject("refused by your user's rule")
		}
	}
	if m.Hop > protocol.MaxHop {
		return msg, false, reject("hop limit exceeded (%d)", protocol.MaxHop)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id = ?`, m.ConversationID).Scan(&n); err != nil {
		return msg, false, err
	}
	if n >= protocol.MaxConvMessages {
		return msg, false, reject("conversation budget exceeded (%d messages)", protocol.MaxConvMessages)
	}
	_, err = tx.Exec(`INSERT INTO messages (id, conversation_id, parent_id, sender, recipient, type, body, hop,
		idem_key, created_at, expires_at, state, sender_cto) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body, m.Hop,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State, m.FromCTO)
	if err != nil {
		return msg, false, err
	}
	if p.Outcome != "" {
		var from, to string
		if err := tx.QueryRow(`SELECT a.repo_project,b.repo_project FROM messages q JOIN sessions a ON a.id=q.sender JOIN sessions b ON b.id=q.recipient WHERE q.id=? AND q.type=?`, p.ParentID, protocol.TypeRequest).Scan(&from, &to); err != nil {
			return msg, false, reject("outcome requires a request parent")
		}
		if _, err := tx.Exec(`INSERT INTO graph_outcomes(source,target,message_id,reply_id,outcome,reason,redirect_to,recommendation,at) VALUES(?,?,?,?,?,?,?,?,?)`, from, to, p.ParentID, m.ID, p.Outcome, p.Reason, p.RedirectTo, p.Recommendation, now.UnixNano()); err != nil {
			return msg, false, err
		}
		if p.Outcome == "reject" || p.Outcome == "escalate" {
			var harness string
			if err := tx.QueryRow(`SELECT harness FROM sessions WHERE id=?`, m.To).Scan(&harness); err != nil {
				return msg, false, err
			}
			if !protocol.IsOwner(harness) {
				var requestBody string
				if err := tx.QueryRow(`SELECT body FROM messages WHERE id=?`, p.ParentID).Scan(&requestBody); err != nil {
					return msg, false, err
				}
				text := "Request: " + protocol.CleanWaitingText(requestBody) + "\nReason: " + p.Reason
				if _, err := s.inboxFromOutcome(tx, p.Outcome, m.From, to, text, p.Recommendation, m.ID, m.ConversationID); err != nil {
					return msg, false, err
				}
			}
		}
	}
	if m.ParentID != "" {
		if _, err := tx.Exec(`UPDATE messages SET state = ? WHERE id = ? AND state IN (?,?,?)`,
			protocol.StateReplied, m.ParentID, protocol.StateStored, protocol.StateDispatched, protocol.StateDelivered); err != nil {
			return msg, false, err
		}
	}
	detail := strings.TrimPrefix(pairs("type", m.Type, "to", m.To, "bytes", len(m.Body)), " ")
	if m.FromCTO {
		detail += " by=cto"
	}
	s.event(tx, "msg.stored", "", m.From, m.ID, detail)
	if err := tx.Commit(); err != nil {
		return msg, false, err
	}
	s.logMessage(m)
	out, _, err := s.Message(m.ID)
	return out, true, err
}

// party describes one end of a message for the log: id, name, harness, host.
func (s *Store) party(prefix, sessionID string) []any {
	a, ok, err := s.Agent(sessionID)
	if err != nil || !ok {
		return []any{prefix, sessionID}
	}
	return []any{prefix, sessionID, prefix + "_name", a.Profile.Name, prefix + "_harness", a.Harness, prefix + "_host", a.Hostname}
}

// logMessage writes the one line that describes a stored message. It has ids,
// type, both ends and the size of the body, and never the body.
func (s *Store) logMessage(m protocol.Message) {
	kv := []any{"msg", m.ID, "conv", m.ConversationID, "type", m.Type}
	kv = append(kv, s.party("from", m.From)...)
	kv = append(kv, s.party("to", m.To)...)
	kv = append(kv, "bytes", len(m.Body))
	s.logf("msg.stored", kv...)
}

// recipientOf returns the recipient session of a message, or "".
func (s *Store) recipientOf(id string) string {
	var to string
	s.db.QueryRow(`SELECT recipient FROM messages WHERE id = ?`, id).Scan(&to)
	return to
}

// logStep writes the line for a later delivery step of a message.
func (s *Store) logStep(event, id string, extra ...any) {
	var conv, to, typ string
	s.db.QueryRow(`SELECT conversation_id, recipient, type FROM messages WHERE id = ?`, id).Scan(&conv, &to, &typ)
	kv := []any{"msg", id, "conv", conv, "type", typ, "to", to}
	if to != "" {
		var host string
		s.db.QueryRow(`SELECT n.hostname FROM sessions s JOIN nodes n ON n.id = s.node_id WHERE s.id = ?`, to).Scan(&host)
		kv = append(kv, "to_host", host)
	}
	s.logf(event, append(kv, extra...)...)
}

// Pending returns undelivered messages for the node's online sessions, oldest first.
func (s *Store) Pending(nodeID string, includeDispatched bool) ([]protocol.Message, error) {
	states := `m.state = 'stored'`
	if includeDispatched {
		states = `m.state IN ('stored','dispatched')`
	}
	rows, err := s.db.Query(msgSelect+` JOIN sessions r ON r.id = m.recipient
		WHERE r.node_id = ? AND r.online = 1 AND `+states+` AND m.expires_at > ?
		ORDER BY m.created_at, m.rowid`, nodeID, s.now().UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) MarkDispatched(id string) error {
	res, err := s.db.Exec(`UPDATE messages SET state = 'dispatched' WHERE id = ? AND state = 'stored'`, id)
	if err == nil {
		if n, _ := res.RowsAffected(); n == 1 {
			s.Event("msg.dispatched", "", s.recipientOf(id), id, "")
			s.logStep("msg.dispatched", id)
		}
	}
	return err
}

// UnmarkDispatched puts a message back to stored when the write to the node
// failed, so the next dispatch sends it again.
func (s *Store) UnmarkDispatched(id string) {
	s.db.Exec(`UPDATE messages SET state = 'stored' WHERE id = ? AND state = 'dispatched'`, id)
}

// ApplyDelivery records a daemon's report. A message that is already replied,
// expired or failed keeps its state.
func (s *Store) ApplyDelivery(id, state, detail string) error {
	if state != protocol.StateDelivered && state != protocol.StateFailed {
		return reject("delivery state must be delivered or failed")
	}
	now := s.now().UnixNano()
	if _, err := s.db.Exec(`INSERT INTO deliveries (message_id, state, detail, at) VALUES (?,?,?,?)`, id, state, detail, now); err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE messages SET state = ? WHERE id = ? AND state IN ('stored','dispatched')`, state, id)
	s.Event("msg."+state, "", s.recipientOf(id), id, detail)
	note := ""
	if err == nil {
		if n, _ := res.RowsAffected(); n == 0 {
			note = "state_kept" // already replied, expired or failed
		}
	}
	s.logStep("msg."+state, id, "detail", detail, "note", note)
	return err
}

// Expire marks overdue undelivered messages expired and returns how many.
func (s *Store) Expire() (int, error) {
	now := s.now().UnixNano()
	rows, err := s.db.Query(`SELECT id FROM messages WHERE state IN ('stored','dispatched','held') AND expires_at <= ?`, now)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		expired, err := s.expireMessage(id, now)
		if err != nil {
			return n, err
		}
		if expired {
			n++
			s.Event("msg.expired", "", s.recipientOf(id), id, "")
			s.logStep("msg.expired", id)
		}
	}

	return n, nil
}

// CanSee tells whether a message involves a session of the node.
func (s *Store) CanSee(nodeID string, m protocol.Message) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE node_id = ? AND id IN (?,?)`, nodeID, m.From, m.To).Scan(&n)
	return n > 0, err
}

// Messages lists recent messages (newest first) that involve a session, or any
// session of the node when sessionID is empty.
func (s *Store) Messages(nodeID string, q protocol.QueryMessages) ([]protocol.Message, error) {
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}
	where := `(m.sender IN (SELECT id FROM sessions WHERE node_id = ?) OR m.recipient IN (SELECT id FROM sessions WHERE node_id = ?))`
	args := []any{nodeID, nodeID}
	if q.SessionID != "" {
		owner, err := s.SessionNode(q.SessionID)
		if err != nil {
			return nil, err
		}
		if owner != nodeID {
			return nil, reject("unknown session %q", q.SessionID)
		}
		where += ` AND (m.sender = ? OR m.recipient = ?)`
		args = append(args, q.SessionID, q.SessionID)
	}
	if q.ConversationID != "" {
		where += ` AND m.conversation_id = ?`
		args = append(args, q.ConversationID)
	}
	args = append(args, q.Limit)
	rows, err := s.db.QueryContext(context.Background(), msgSelect+` WHERE `+where+` ORDER BY m.created_at DESC, m.rowid DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetAllOffline is run at start: no daemon is connected to a new process.
func (s *Store) SetAllOffline() error {
	_, err := s.db.Exec(`UPDATE sessions SET online = 0`)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) expireMessage(id string, now int64) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	m, ok, err := s.message(tx, "m.id=?", id)
	if err != nil || !ok {
		return false, err
	}
	res, err := tx.Exec(`UPDATE messages SET state='expired' WHERE id=? AND state IN ('stored','dispatched','held') AND expires_at<=?`, id, now)
	if err != nil {
		return false, err
	}
	count, err := res.RowsAffected()
	if err != nil || count == 0 {
		return false, err
	}
	if m.State == protocol.StateHeld {
		if err = s.heldReply(tx, m, "your request expired while awaiting your user", "expired"); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// graphExclusion distinguishes older daemons from an eligible checked root.
func graphExclusion(excluded, checked bool) int {
	if excluded {
		return 1
	}
	if checked {
		return 2
	}
	return 0
}

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
	db  *sql.DB
	now func() time.Time
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
		if _, err := tx.Exec(migrations[i]); err != nil {
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
	_, err = s.db.Exec(`INSERT INTO nodes (id, hostname, os, token_hash, created_at) VALUES (?,?,?,?,?)`,
		nodeID, hostname, osName, hashHex(token), s.now().UnixNano())
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

func (s *Store) SetNodeHostname(nodeID, hostname string) error {
	if hostname == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE nodes SET hostname = ? WHERE id = ?`, hostname, nodeID)
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
	QueryRow(string, ...any) *sql.Row
}, nodeID string, in protocol.SessionInfo) (id string, created bool, err error) {
	if in.Harness == "" || in.NativeID == "" {
		return "", false, reject("session needs harness and native_id")
	}
	if in.Status == "" {
		in.Status = "unknown"
	}
	var known int
	if err := q.QueryRow(`SELECT COUNT(*) FROM sessions WHERE node_id=? AND harness=? AND native_id=?`,
		nodeID, in.Harness, in.NativeID).Scan(&known); err != nil {
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
	if in.Worker != nil {
		raw, err := json.Marshal(in.Worker)
		if err != nil {
			return "", false, err
		}
		worker = string(raw)
	}
	in.Repo.Description = protocol.CleanDescription(in.Repo.Description)
	_, err = q.Exec(`INSERT INTO sessions (id, node_id, harness, native_id, name, cwd, pid, status, delivery, last_seen, online,
		repo_root, repo_project, repo_remote, repo_branch, repo_worktree, mode, model, worker, repo_description)
		VALUES (?,?,?,?,?,?,?,?,?,?,1,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (node_id, harness, native_id) DO UPDATE SET
		name=excluded.name, cwd=excluded.cwd, pid=excluded.pid, status=excluded.status,
		delivery=excluded.delivery, last_seen=excluded.last_seen, online=1,
		repo_root=excluded.repo_root, repo_project=excluded.repo_project,
		repo_remote=excluded.repo_remote, repo_branch=excluded.repo_branch,
		repo_worktree=excluded.repo_worktree, mode=excluded.mode, model=excluded.model, worker=excluded.worker,
		repo_description=excluded.repo_description`,
		protocol.NewID("s"), nodeID, in.Harness, in.NativeID, in.Name, in.Cwd, in.PID, in.Status, in.Delivery, now,
		in.Repo.Root, in.Repo.Project, in.Repo.Remote, in.Repo.Branch, in.Repo.Worktree, in.Mode, in.Model, worker, in.Repo.Description)
	if err != nil {
		return "", false, err
	}
	err = q.QueryRow(`SELECT id FROM sessions WHERE node_id=? AND harness=? AND native_id=?`,
		nodeID, in.Harness, in.NativeID).Scan(&id)
	if err != nil {
		return "", false, err
	}
	// The owner session is the person, not a new agent: no session.new for it.
	fresh := known == 0 && !protocol.IsOwner(in.Harness)
	if fresh {
		s.event(q, "session.new", nodeID, id, "", sessionDetail(in))
	}
	return id, fresh, nil
}

func sessionDetail(in protocol.SessionInfo) string {
	return strings.TrimPrefix(pairs("harness", in.Harness, "name", in.Name, "project", in.Repo.Project,
		"branch", in.Repo.Branch, "cwd", in.Cwd), " ")
}

// logNewSessions writes the session.new log lines, after the transaction that
// stored them has committed.
func (s *Store) logNewSessions(list []newSession) {
	for _, n := range list {
		host := s.hostname(n.node)
		s.logf("session.new", "session", n.id, "node", n.node, "host", host, "harness", n.in.Harness,
			"name", n.in.Name, "pid", n.in.PID, "cwd", n.in.Cwd, "project", n.in.Repo.Project,
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
	v := 0
	if on {
		v = 1
	}
	res, err := s.db.Exec(`UPDATE sessions SET peer_trust = ? WHERE id = ? AND harness != ?`, v, sessionID, protocol.OwnerHarness)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
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
	s.peer_trust, COALESCE(p.expertise,''), s.delegation
	FROM sessions s JOIN nodes n ON n.id = s.node_id LEFT JOIN profiles p ON p.session_id = s.id`

func scanAgent(sc interface{ Scan(...any) error }) (protocol.AgentView, error) {
	var a protocol.AgentView
	var online int
	var lastSeen int64
	var caps, worker string
	var trust int
	var deleg string
	err := sc.Scan(&a.SessionID, &a.NodeID, &a.Hostname, &a.Harness, &a.Cwd, &a.Status, &online, &lastSeen,
		&a.Profile.Name, &a.Profile.Function, &a.Profile.Project, &caps, &a.Profile.Task,
		&a.Repo.Root, &a.Repo.Project, &a.Repo.Remote, &a.Repo.Branch, &a.Repo.Worktree, &a.Mode, &a.Model, &a.PID, &worker, &a.Repo.Description,
		&trust, &a.Profile.Expertise, &deleg)
	if err != nil {
		return a, err
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
	return out, nil
}

// ---- messages ----

const msgSelect = `SELECT m.id, m.conversation_id, m.parent_id, m.sender, m.recipient, m.type, m.body, m.hop,
	m.idem_key, m.created_at, m.expires_at, m.state,
	COALESCE(NULLIF(p.name,''), s.name, ''), COALESCE(s.harness, ''), COALESCE(s.worker, '') != ''
	FROM messages m LEFT JOIN sessions s ON s.id = m.sender LEFT JOIN profiles p ON p.session_id = m.sender`

func scanMessage(sc interface{ Scan(...any) error }) (protocol.Message, error) {
	var m protocol.Message
	var created, expires int64
	err := sc.Scan(&m.ID, &m.ConversationID, &m.ParentID, &m.From, &m.To, &m.Type, &m.Body, &m.Hop,
		&m.IdempotencyKey, &created, &expires, &m.State, &m.FromName, &m.FromHarness, &m.FromWorker)
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
		State: protocol.StateStored,
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
		idem_key, created_at, expires_at, state) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body, m.Hop,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State)
	if err != nil {
		return msg, false, err
	}
	if m.ParentID != "" {
		if _, err := tx.Exec(`UPDATE messages SET state = ? WHERE id = ? AND state IN (?,?,?)`,
			protocol.StateReplied, m.ParentID, protocol.StateStored, protocol.StateDispatched, protocol.StateDelivered); err != nil {
			return msg, false, err
		}
	}
	s.event(tx, "msg.stored", "", m.From, m.ID, strings.TrimPrefix(pairs("type", m.Type, "to", m.To, "bytes", len(m.Body)), " "))
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
	rows, err := s.db.Query(`SELECT id FROM messages WHERE state IN ('stored','dispatched') AND expires_at <= ?`, now)
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
		res, err := s.db.Exec(`UPDATE messages SET state = 'expired' WHERE id = ? AND state IN ('stored','dispatched')`, id)
		if err != nil {
			return n, err
		}
		if c, _ := res.RowsAffected(); c == 1 {
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

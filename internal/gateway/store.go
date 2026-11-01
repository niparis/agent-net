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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/dbfile"
	"github.com/niparis/agent-net/internal/phrasing"
	"github.com/niparis/agent-net/internal/protocol"
	_ "modernc.org/sqlite"
)

// Rejection is an error caused by the caller breaking a rule, not by a fault.
type Rejection string

func (r Rejection) Error() string { return string(r) }

func reject(format string, args ...any) error { return Rejection(fmt.Sprintf(format, args...)) }

// Store is the gateway's SQLite database.
type Store struct {
	qualityMu         sync.Mutex
	quality           protocol.QualityAnalysis
	qualityFit        qualityFit
	qualityStrong     map[string]int
	reminderMu        sync.Mutex
	reminderOffline   map[string]bool
	checkMu           sync.Mutex
	pendingMu         sync.Mutex
	pendingEpisodes   map[string]pendingEpisode
	pendingStalls     int
	db                *sql.DB
	graphJunkRemoved  int64
	graphJunkMigrated bool
	identityMerges    [][2]string
	identityConflicts [][2]string
	now               func() time.Time
	// logf writes a structured log line; the gateway sets it. Never pass a
	// message body to it.
	logf func(event string, kv ...any)

	mu            sync.Mutex
	buckets       map[string]*bucket
	learnMu       sync.Mutex
	phraseCleaner *phrasing.Cleaner
	// hiddenMu serializes reads that prune expired session entries with writes.
	hiddenMu sync.Mutex
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
`, `
CREATE TABLE IF NOT EXISTS mcp_native_plans (id TEXT PRIMARY KEY, snapshot TEXT NOT NULL, plan TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'pending');
`, `
CREATE TABLE IF NOT EXISTS tool_accepted (key TEXT PRIMARY KEY, at INTEGER NOT NULL);
`, `
ALTER TABLE skill_versions ADD COLUMN number INTEGER NOT NULL DEFAULT 0;
UPDATE skill_versions SET number=(SELECT COUNT(*) FROM skill_versions o JOIN tool_identities oi ON oi.id=o.item, tool_identities ti WHERE ti.id=skill_versions.item AND oi.kind=ti.kind AND oi.name=ti.name AND (o.imported_at<skill_versions.imported_at OR (o.imported_at=skill_versions.imported_at AND o.item<=skill_versions.item)));
`, `
ALTER TABLE tool_audit ADD COLUMN assignment TEXT NOT NULL DEFAULT '';
`, `
ALTER TABLE tool_results ADD COLUMN branch TEXT NOT NULL DEFAULT '';
ALTER TABLE tool_results ADD COLUMN commit_hash TEXT NOT NULL DEFAULT '';
`, `
CREATE TABLE IF NOT EXISTS signal_prompts (session_id TEXT PRIMARY KEY, prompt TEXT NOT NULL, at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS charter_signals (message_id TEXT PRIMARY KEY, source TEXT NOT NULL, target TEXT NOT NULL, prompt TEXT NOT NULL, at INTEGER NOT NULL);
`, `
CREATE TABLE IF NOT EXISTS learned_suggestions (
 id INTEGER PRIMARY KEY AUTOINCREMENT, node TEXT NOT NULL, field TEXT NOT NULL,
 edge_kind TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL, phrase TEXT NOT NULL,
 evidence TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending', severity TEXT NOT NULL,
 created_at INTEGER NOT NULL, ready_at INTEGER NOT NULL, decided_at INTEGER NOT NULL DEFAULT 0,
 CHECK(status IN ('pending','applied','rejected','undone')),
 CHECK(kind IN ('add','remove')), CHECK(severity IN ('minor','major'))
);
CREATE INDEX IF NOT EXISTS learned_pending ON learned_suggestions(node,field,phrase,status);
CREATE TABLE IF NOT EXISTS learned_message_flow (message_id TEXT PRIMARY KEY, source TEXT NOT NULL, target TEXT NOT NULL, excerpt TEXT NOT NULL, at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS learned_flow_pair ON learned_message_flow(source,target,at);
`, `
CREATE TABLE IF NOT EXISTS learned_suggestion_details (suggestion_id INTEGER PRIMARY KEY, origin TEXT NOT NULL DEFAULT '', history_id INTEGER NOT NULL DEFAULT 0);
INSERT OR IGNORE INTO learned_suggestion_details(suggestion_id,origin)
 SELECT id,'owner' FROM learned_suggestions WHERE field='accepts';
`, `
CREATE TABLE IF NOT EXISTS learned_phrase_state (suggestion_id INTEGER PRIMARY KEY, raw_phrase TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS learned_phrase_runs (id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL);
`, `
CREATE TABLE IF NOT EXISTS learned_views (node TEXT PRIMARY KEY, last_seen_id INTEGER NOT NULL);
`, `SELECT 1 /* project identity migration */;`, `CREATE TABLE IF NOT EXISTS project_display_names (project TEXT PRIMARY KEY, name TEXT NOT NULL);`, `
-- Names set by announce are cleared: only the owner renames a session now
-- (decision 0033). A session shows its automatic name at its next report.
UPDATE profiles SET name = '';
`, `
ALTER TABLE sessions ADD COLUMN last_active INTEGER NOT NULL DEFAULT 0;
`, `
-- Reserve CTO for the network session, including names saved before part 6.
UPDATE profiles SET name = '' WHERE name = 'CTO';
UPDATE sessions SET name = 'CTO ' || substr(id, 3) WHERE name = 'CTO' AND harness <> 'cto';
`, `SELECT 1 /* CTO legacy route upgrade */;`, `ALTER TABLE messages ADD COLUMN reminded INTEGER NOT NULL DEFAULT 0;`, `
-- The two rework counters of a worker run (difficulty calibration, part 1).
SELECT 1 /* worker rework counters */;
`, `
-- The caller and the level it asked for on every run (difficulty
-- calibration, part 2). Kept on the run because a worker session is cleaned
-- a few hours after it starts.
SELECT 1 /* worker calibration columns */;
`, `
-- The idle check keeps only the high-water mark of the last activity it
-- covered, the small stopping counters, and the daily cap. Task content is
-- never stored here.
CREATE TABLE IF NOT EXISTS session_checks (
 session_id TEXT PRIMARY KEY,
 check_active INTEGER NOT NULL DEFAULT 0,
 check_waiting INTEGER NOT NULL DEFAULT 0,
 check_more INTEGER NOT NULL DEFAULT 0,
 check_open INTEGER NOT NULL DEFAULT 0,
 check_msgs INTEGER NOT NULL DEFAULT 0,
 check_day TEXT NOT NULL DEFAULT '',
 check_day_count INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS check_answer_counts (
 day TEXT NOT NULL, kind TEXT NOT NULL, count INTEGER NOT NULL, PRIMARY KEY(day, kind));
`, `
-- The autonomy guard rails. Only the daily count, the no-progress streak and
-- the progress markers of the last wake are kept. Task content is never stored.
CREATE TABLE IF NOT EXISTS session_wakes (
 session_id TEXT PRIMARY KEY,
 wake_day TEXT NOT NULL DEFAULT '',
 wake_day_count INTEGER NOT NULL DEFAULT 0,
 wake_streak INTEGER NOT NULL DEFAULT 0,
 wake_open INTEGER NOT NULL DEFAULT 0,
 wake_msgs INTEGER NOT NULL DEFAULT 0);
`, `
-- The satisfaction sample (difficulty calibration, part 4): one row per
-- rating, and one row per finished worker the gateway offered to the caller.
CREATE TABLE IF NOT EXISTS worker_ratings (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id INTEGER NOT NULL DEFAULT 0,
  session_id TEXT NOT NULL UNIQUE,
  caller TEXT NOT NULL DEFAULT '',
  rater_model TEXT NOT NULL DEFAULT '',
  harness TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  effort TEXT NOT NULL DEFAULT '',
  difficulty TEXT NOT NULL DEFAULT '',
  score INTEGER NOT NULL CHECK(score BETWEEN 1 AND 5),
  note TEXT NOT NULL DEFAULT '',
  at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS worker_ratings_model ON worker_ratings (model, effort, at);
CREATE TABLE IF NOT EXISTS worker_rate_offers (
  session_id TEXT PRIMARY KEY,
  caller TEXT NOT NULL DEFAULT '',
  harness TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  effort TEXT NOT NULL DEFAULT '',
  difficulty TEXT NOT NULL DEFAULT '',
  asked INTEGER NOT NULL DEFAULT 0,
  at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS worker_rate_offers_caller ON worker_rate_offers (caller, at);
`, `
-- Whether the rework counters were live for a run (difficulty calibration,
-- parts 1 and 2). Runs recorded before the counters shipped read 0, which is
-- not the same as no rework, so they stay out of the medians.
SELECT 1 /* worker run counters known */;
`, `
-- Indexes for the per-worker rows of the stored conversation list.
CREATE INDEX IF NOT EXISTS messages_sender ON messages (sender, created_at);
CREATE INDEX IF NOT EXISTS messages_recipient ON messages (recipient, created_at);
`, `
-- Defects reported after a worker's hand-back (difficulty calibration). One
-- row per report, so a worker session may have several. Each row counts as one
-- fix on that task in the medians. The note is the caller's one line, never
-- the task text.
CREATE TABLE IF NOT EXISTS worker_defects (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id INTEGER NOT NULL DEFAULT 0,
  session_id TEXT NOT NULL,
  caller TEXT NOT NULL DEFAULT '',
  rater_model TEXT NOT NULL DEFAULT '',
  harness TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  effort TEXT NOT NULL DEFAULT '',
  difficulty TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '',
  at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS worker_defects_session ON worker_defects (session_id, at);
CREATE INDEX IF NOT EXISTS worker_defects_model ON worker_defects (model, at);
`, `
-- The severity of a late defect report, 1 to 4 (difficulty calibration). It
-- adds that many points of rework to the worker's task. Rows made before this
-- column existed count as severity 1.
SELECT 1 /* worker defect severity */;
`, `SELECT 1 /* worker blast radius columns */;`, `
-- Automatic continues the daemon sent for a worker run. They cost the caller
-- no rework, so they are kept apart from continue_count and never enter the
-- difficulty calibration.
SELECT 1 /* worker auto continue */;
`, `
-- Continued sessions belong to one task. Mid-run requests are kept apart
-- from the existing rework counters until the owner assigns a weight.
SELECT 1 /* worker task rework columns */;
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
	if err := s.backfillRework(); err != nil {
		db.Close()
		return nil, fmt.Errorf("rework backfill: %w", err)
	}
	if err := s.backfillTaskRework(); err != nil {
		db.Close()
		return nil, fmt.Errorf("task rework backfill: %w", err)
	}
	// Fix files that an older version made with the default mode.
	if err := dbfile.Secure(path); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.refreshQuality(); err != nil {
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
		// SQLite has no ADD COLUMN IF NOT EXISTS, and a test may replay a
		// migration on a database that already has these columns.
		if strings.Contains(migrations[i], "worker rework counters") {
			for _, col := range []struct{ name, spec string }{
				{"continue_count", `continue_count INTEGER NOT NULL DEFAULT 0`},
				{"fix_count", `fix_count INTEGER NOT NULL DEFAULT 0`},
			} {
				var has int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_runs') WHERE name = ?`, col.name).Scan(&has); err != nil {
					tx.Rollback()
					return fmt.Errorf("migration %d: %w", i+1, err)
				}
				if has == 0 {
					if _, err := tx.Exec(`ALTER TABLE worker_runs ADD COLUMN ` + col.spec); err != nil {
						tx.Rollback()
						return fmt.Errorf("migration %d: %w", i+1, err)
					}
				}
			}
		}
		if strings.Contains(migrations[i], "worker calibration columns") {
			for _, col := range []struct{ name, spec string }{
				{"caller", `caller TEXT NOT NULL DEFAULT ''`},
				{"difficulty", `difficulty TEXT NOT NULL DEFAULT ''`},
			} {
				var has int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_runs') WHERE name = ?`, col.name).Scan(&has); err != nil {
					tx.Rollback()
					return fmt.Errorf("migration %d: %w", i+1, err)
				}
				if has == 0 {
					if _, err := tx.Exec(`ALTER TABLE worker_runs ADD COLUMN ` + col.spec); err != nil {
						tx.Rollback()
						return fmt.Errorf("migration %d: %w", i+1, err)
					}
				}
			}
			if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS worker_runs_caller ON worker_runs (caller, difficulty, ended_at)`); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			// Runs made before these columns existed can still be attributed
			// while their worker session is kept (a few hours after a start).
			rows, err := tx.Query(`SELECT id, worker FROM sessions WHERE worker <> ''`)
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			type runFacts struct{ caller, asked, used string }
			facts := map[string]runFacts{}
			for rows.Next() {
				var id, raw string
				if err := rows.Scan(&id, &raw); err != nil {
					rows.Close()
					tx.Rollback()
					return fmt.Errorf("migration %d: %w", i+1, err)
				}
				var w protocol.WorkerInfo
				if json.Unmarshal([]byte(raw), &w) == nil {
					facts[id] = runFacts{caller: w.Owner, asked: w.DifficultyAsked, used: w.DifficultyUsed}
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			rows.Close()
			for id, f := range facts {
				if f.caller == "" {
					continue
				}
				difficulty := f.asked
				if difficulty == "" {
					difficulty = f.used
				}
				if difficulty == "" {
					difficulty = "normal"
				}
				if _, err := tx.Exec(`UPDATE worker_runs SET caller = ?, difficulty = ? WHERE session_id = ? AND caller = ''`, f.caller, difficulty, id); err != nil {
					tx.Rollback()
					return fmt.Errorf("migration %d: %w", i+1, err)
				}
			}
		}
		if strings.Contains(migrations[i], "worker blast radius columns") {
			for _, col := range []string{"touches", "difficulty_used"} {
				var has int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_runs') WHERE name = ?`, col).Scan(&has); err != nil {
					tx.Rollback()
					return err
				}
				if has == 0 {
					if _, err := tx.Exec(`ALTER TABLE worker_runs ADD COLUMN ` + col + ` TEXT NOT NULL DEFAULT ''`); err != nil {
						tx.Rollback()
						return err
					}
				}
			}
		}
		if strings.Contains(migrations[i], "worker task rework columns") {
			for _, col := range []struct{ name, spec string }{
				{"task_id", `task_id TEXT NOT NULL DEFAULT ''`},
				{"midrun_count", `midrun_count INTEGER NOT NULL DEFAULT 0`},
			} {
				var has int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_runs') WHERE name=?`, col.name).Scan(&has); err != nil {
					tx.Rollback()
					return fmt.Errorf("migration %d: %w", i+1, err)
				}
				if has == 0 {
					if _, err := tx.Exec(`ALTER TABLE worker_runs ADD COLUMN ` + col.spec); err != nil {
						tx.Rollback()
						return fmt.Errorf("migration %d: %w", i+1, err)
					}
				}
			}
		}
		if strings.Contains(migrations[i], "worker run counters known") {
			var has int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_runs') WHERE name = 'counters_known'`).Scan(&has); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			if has == 0 {
				// Rows that already exist were recorded before the counters
				// shipped, so they keep the default 0: unknown.
				if _, err := tx.Exec(`ALTER TABLE worker_runs ADD COLUMN counters_known INTEGER NOT NULL DEFAULT 0`); err != nil {
					tx.Rollback()
					return fmt.Errorf("migration %d: %w", i+1, err)
				}
			}
		}
		if strings.Contains(migrations[i], "worker defect severity") {
			var has int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_defects') WHERE name = 'severity'`).Scan(&has); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			if has == 0 {
				// An older report had no severity; one point is the least a
				// defect can cost.
				if _, err := tx.Exec(`ALTER TABLE worker_defects ADD COLUMN severity INTEGER NOT NULL DEFAULT 1`); err != nil {
					tx.Rollback()
					return fmt.Errorf("migration %d: %w", i+1, err)
				}
			}
		}
		if strings.Contains(migrations[i], "worker auto continue") {
			var has int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_runs') WHERE name = 'auto_continue_count'`).Scan(&has); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			if has == 0 {
				if _, err := tx.Exec(`ALTER TABLE worker_runs ADD COLUMN auto_continue_count INTEGER NOT NULL DEFAULT 0`); err != nil {
					tx.Rollback()
					return fmt.Errorf("migration %d: %w", i+1, err)
				}
			}
		}
		if strings.Contains(migrations[i], "CTO legacy route upgrade") {
			if _, err := tx.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('cto_legacy_route_start','',?) ON CONFLICT(key) DO NOTHING`, s.now().UnixNano()); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
		}
		var merges, conflicts [][2]string
		if strings.Contains(migrations[i], "project identity migration") {
			merges, conflicts, err = migrateProjectIdentities(tx)
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		s.identityMerges = append(s.identityMerges, merges...)
		s.identityConflicts = append(s.identityConflicts, conflicts...)
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
}, nodeID string, in protocol.SessionInfo, cto ...bool) (id string, created bool, err error) {
	if in.Harness == "cto" && (len(cto) == 0 || !cto[0]) {
		return "", false, reject("CTO session requires owner registration")
	}
	if in.Harness == "" || in.NativeID == "" {
		return "", false, reject("session needs harness and native_id")
	}
	if in.Status == "" {
		in.Status = "unknown"
	}
	// A resumed worker may get a new native ID. Only its own node may
	// rebind its offline record, and the worker ID must still match.
	if in.Worker != nil && validSessionID(in.SessionID) {
		_, err := q.Exec(`UPDATE sessions SET native_id=? WHERE id=? AND node_id=? AND harness=? AND online=0 AND CASE WHEN json_valid(worker) THEN json_extract(worker, '$.id') END=? AND native_id<>? AND NOT EXISTS (SELECT 1 FROM sessions WHERE node_id=? AND harness=? AND native_id=?)`,
			in.NativeID, in.SessionID, nodeID, in.Harness, in.Worker.ID, in.NativeID, nodeID, in.Harness, in.NativeID)
		if err != nil {
			return "", false, err
		}
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
	if in.Harness == "cto" {
		name = "CTO"
	} else if protocol.IsOwner(in.Harness) {
		name = protocol.CleanModel(in.Name)
	} else if name, err = chooseSessionName(q, nodeID, knownID, knownName, in); err != nil {
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
	lastActive := in.LastActive * int64(time.Second)
	_, err = q.Exec(`INSERT INTO sessions (id, node_id, harness, native_id, name, title, cwd, pid, status, delivery, last_seen, last_active, online,
		repo_root, repo_project, repo_remote, repo_branch, repo_worktree, mode, model, worker, repo_description, worker_started,
		tools_version, tools_old, created_at, waiting, graph_excluded)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,1,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (node_id, harness, native_id) DO UPDATE SET
		name=excluded.name, title=excluded.title, cwd=excluded.cwd, pid=excluded.pid, status=excluded.status,
		delivery=excluded.delivery, last_seen=excluded.last_seen, online=1,
		last_active=MAX(sessions.last_active, excluded.last_active),
		repo_root=excluded.repo_root, repo_project=excluded.repo_project,
		repo_remote=excluded.repo_remote, repo_branch=excluded.repo_branch,
		repo_worktree=excluded.repo_worktree, mode=excluded.mode, model=excluded.model, worker=excluded.worker,
		repo_description=excluded.repo_description, worker_started=excluded.worker_started,
		tools_version=excluded.tools_version, tools_old=excluded.tools_old, waiting=excluded.waiting, graph_excluded=excluded.graph_excluded`,
		newID, nodeID, in.Harness, in.NativeID, name, title, in.Cwd, in.PID, in.Status, in.Delivery, now, lastActive,
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
}, nodeID, sessionID, current string, in protocol.SessionInfo) (string, error) {
	base, worker := sessionBaseName(in), in.Worker != nil
	used := map[string]bool{"CTO": true}
	rows, err := q.Query(`SELECT COALESCE(NULLIF(p.name,''), s.name), s.online,
		s.node_id, s.harness, s.mode, s.pid, s.worker FROM sessions s
		LEFT JOIN profiles p ON p.session_id = s.id WHERE s.id <> ? AND
		((s.online = 1 AND NOT (`+piChildSQL+`)) OR
		(? = 'pi' AND ? = 'foreground' AND ? > 0 AND s.node_id = ?
		 AND s.harness = 'pi' AND s.mode = 'background' AND s.pid = ? AND s.worker = ''))`,
		sessionID, in.Harness, in.Mode, in.PID, nodeID, in.PID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var name, node, harness, mode, workerJSON string
		var online, pid int
		if err := rows.Scan(&name, &online, &node, &harness, &mode, &pid, &workerJSON); err != nil {
			return "", err
		}
		// Before the first TUI report there may be no foreground row to join.
		// Ignore its SDK helpers, and repair a suffix they previously caused.
		if in.Harness == "pi" && in.Mode == protocol.ModeForeground && in.PID > 0 &&
			node == nodeID && harness == "pi" && mode == protocol.ModeBackground && pid == in.PID && workerJSON == "" {
			if name == base {
				current = ""
			}
			continue
		}
		if online == 1 && name != "" {
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
	rows, err := tx.Query(`SELECT id FROM sessions WHERE node_id = ? AND online = 1 AND harness <> 'cto'`, nodeID)
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
func (s *Store) SetPeerTrust(sessionID string, on bool, validate ...bool) (bool, error) {
	return s.setPeerTrust(sessionID, on, "owner", validate...)
}

func (s *Store) setPeerTrust(sessionID string, on bool, actor string, validate ...bool) (bool, error) {
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
		if err := s.SetEdge(protocol.Edge{From: "*", To: project, Kind: "depends", Rule: rule}, actor, validate...); err != nil {
			return false, err
		}
	}
	if len(validate) > 0 && validate[0] {
		return true, nil
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

// SetSessionWorkerRework keeps the rework counters, the automatic continues and
// the difficulties on a worker's session record. The end report is the reliable
// last word, so the gateway does not depend on the session sync that came before
// it. A session that is not a worker is left alone.
func (s *Store) SetSessionWorkerRework(nodeID, sessionID string, cont, fix int, asked, used string, auto ...int) error {
	var raw string
	err := s.db.QueryRow(`SELECT worker FROM sessions WHERE id = ? AND node_id = ?`, sessionID, nodeID).Scan(&raw)
	if err == sql.ErrNoRows || raw == "" {
		return nil
	}
	if err != nil {
		return err
	}
	var w protocol.WorkerInfo
	if json.Unmarshal([]byte(raw), &w) != nil {
		return nil
	}
	w.Continue, w.Fix = cont, fix
	if len(auto) > 0 {
		w.AutoContinue = auto[0]
	}
	if asked != "" {
		w.DifficultyAsked = asked
	}
	if used != "" {
		w.DifficultyUsed = used
	}
	b, err := json.Marshal(w)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE sessions SET worker = ? WHERE id = ? AND node_id = ?`, string(b), sessionID, nodeID)
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
// optionally a project and capability tags. Empty fields keep what is stored,
// so announcing a new task does not erase the rest. The name is not announce's
// to set: only the owner renames a session (decision 0033). The function is
// not taken from the agent either: it comes from the project's AGENTS.md (see
// RepoInfo.Description), so a "function" in the request is ignored.
func (s *Store) Announce(nodeID string, p protocol.ProfileAnnounce) (protocol.Profile, error) {
	owner, err := s.SessionNode(p.SessionID)
	if err != nil {
		return protocol.Profile{}, err
	}
	if owner != nodeID {
		return protocol.Profile{}, reject("unknown session %q", p.SessionID)
	}
	p.Task = strings.TrimSpace(p.Task)
	p.Expertise = protocol.CleanExpertise(p.Expertise)
	caps := "[]"
	if p.Capabilities != nil {
		b, _ := json.Marshal(p.Capabilities)
		caps = string(b)
	}
	_, err = s.db.Exec(`INSERT INTO profiles (session_id, name, function, project, capabilities, task, updated_at, expertise)
		VALUES (?,'','',?,?,?,?,?)
		ON CONFLICT (session_id) DO UPDATE SET
		project=CASE WHEN excluded.project='' THEN profiles.project ELSE excluded.project END,
		capabilities=CASE WHEN ? THEN profiles.capabilities ELSE excluded.capabilities END,
		task=CASE WHEN excluded.task='' THEN profiles.task ELSE excluded.task END,
		expertise=CASE WHEN excluded.expertise='' THEN profiles.expertise ELSE excluded.expertise END,
		updated_at=excluded.updated_at`,
		p.SessionID, p.Project, caps, p.Task, s.now().UnixNano(), p.Expertise, p.Capabilities == nil)
	if err != nil {
		return protocol.Profile{}, err
	}
	if err := s.answerCheck(p.SessionID, p.Task); err != nil {
		return protocol.Profile{}, err
	}
	a, ok, err := s.Agent(p.SessionID)
	if err != nil || !ok {
		return protocol.Profile{}, err
	}
	return a.Profile, nil
}

const agentSelect = `SELECT s.id, s.node_id, n.hostname, s.harness, s.cwd, s.native_id, s.status, s.online, s.last_seen, s.last_active,
	COALESCE(NULLIF(p.name,''), s.name), s.repo_description, COALESCE(p.project,''),
	COALESCE(p.capabilities,'[]'), COALESCE(p.task,''),
	s.repo_root, s.repo_project, s.repo_remote, s.repo_branch, s.repo_worktree, s.mode, s.model, s.pid, s.worker, s.repo_description,
	s.peer_trust, COALESCE(p.expertise,''), s.delegation, s.title, s.created_at, s.tools_version, s.tools_old, s.waiting, COALESCE((SELECT name FROM project_display_names WHERE project=COALESCE(NULLIF(s.repo_project,''),p.project)),'')
	FROM sessions s JOIN nodes n ON n.id = s.node_id LEFT JOIN profiles p ON p.session_id = s.id`

func scanAgent(sc interface{ Scan(...any) error }) (protocol.AgentView, error) {
	var a protocol.AgentView
	var online int
	var lastSeen, lastActive, created int64
	var caps, worker string
	var trust int
	var deleg, waiting string
	var toolsOld int
	err := sc.Scan(&a.SessionID, &a.NodeID, &a.Hostname, &a.Harness, &a.Cwd, &a.NativeID, &a.Status, &online, &lastSeen, &lastActive,
		&a.Profile.Name, &a.Profile.Function, &a.Profile.Project, &caps, &a.Profile.Task,
		&a.Repo.Root, &a.Repo.Project, &a.Repo.Remote, &a.Repo.Branch, &a.Repo.Worktree, &a.Mode, &a.Model, &a.PID, &worker, &a.Repo.Description,
		&trust, &a.Profile.Expertise, &deleg, &a.Title, &created, &a.ToolsVersion, &toolsOld, &waiting, &a.ProjectDisplayName)
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
	a.LastActive = lastActive / int64(time.Second)
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

// Older Pi extensions registered SDK helpers as independent sessions. Match
// only background non-workers with a foreground session in the same process.
// Overlapping lifetimes keep a later reuse of the PID from hiding old sessions.
const piChildSQL = `s.harness = 'pi' AND s.mode = 'background' AND s.pid > 0 AND s.worker = ''
	AND EXISTS (SELECT 1 FROM sessions parent WHERE parent.node_id = s.node_id
		AND parent.harness = 'pi' AND parent.mode = 'foreground' AND parent.pid = s.pid
		AND (parent.online = 1 OR parent.last_seen >= s.created_at)
		AND (s.online = 1 OR s.last_seen >= parent.created_at))`

func (s *Store) Agents(f protocol.QueryAgents) ([]protocol.AgentView, error) {
	rows, err := s.db.Query(agentSelect + ` WHERE NOT (` + piChildSQL + `) ORDER BY s.last_seen DESC, s.id`)
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
	if err := s.applyPending(out); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- messages ----

const msgSelect = `SELECT m.id, m.conversation_id, m.parent_id, m.sender, m.recipient, m.type, m.body, m.hop,
	m.idem_key, m.created_at, m.expires_at, m.state,
	COALESCE(NULLIF(p.name,''), s.name, ''), COALESCE(s.harness, ''), COALESCE(s.worker, '') != '', m.sender_cto, m.owner_approved, EXISTS(SELECT 1 FROM outcome_overrules oo WHERE oo.message_id=m.id), m.approval_note, COALESCE((SELECT substr(cs.prompt,1,200) FROM charter_signals cs WHERE cs.message_id=m.id),'')
	FROM messages m LEFT JOIN sessions s ON s.id = m.sender LEFT JOIN profiles p ON p.session_id = m.sender`

func scanMessage(sc interface{ Scan(...any) error }) (protocol.Message, error) {
	var m protocol.Message
	var created, expires int64
	err := sc.Scan(&m.ID, &m.ConversationID, &m.ParentID, &m.From, &m.To, &m.Type, &m.Body, &m.Hop,
		&m.IdempotencyKey, &created, &expires, &m.State, &m.FromName, &m.FromHarness, &m.FromWorker, &m.FromCTO, &m.OwnerApproved, &m.Overruled, &m.ApprovalNote, &m.OwnerInstruction)
	if m.FromCTO {
		m.FromHarness = "cto"
	}
	if m.From == "agent-net" {
		m.FromName, m.FromHarness = "agent-net", "agent-net"
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
	if p.Sender == "agent-net" {
		return msg, false, reject("agent-net sender is reserved")
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
		var registered string
		if err := tx.QueryRow(`SELECT value FROM owner_settings WHERE key = 'cto_session'`).Scan(&registered); err != nil || registered != p.Sender {
			return msg, false, reject("CTO sender requires the registered CTO session")
		}
	} else if p.OwnerApproved {
		return msg, false, reject("owner approval requires the CTO session")
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
		State: protocol.StateStored, FromCTO: p.FromCTO, OwnerApproved: p.OwnerApproved && p.FromCTO,
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
		// Before the network CTO existed, its messages came from the owner.
		// Route replies to those roots to the registered identity for 30 days
		// from the gateway upgrade. The parent check above still applies.
		var legacyCTO string
		err = tx.QueryRow(`SELECT c.value FROM owner_settings c JOIN owner_settings start ON start.key='cto_legacy_route_start'
			JOIN messages root ON root.conversation_id=? AND root.parent_id=''
			JOIN sessions sender ON sender.id=root.sender
			WHERE c.key='cto_session' AND root.sender_cto=1 AND sender.harness=? AND root.sender=?
			AND root.created_at<=start.updated_at AND ?>=start.updated_at AND ?<start.updated_at+?`,
			parent.ConversationID, protocol.OwnerHarness, m.To, now.UnixNano(), now.UnixNano(), int64(30*24*time.Hour)).Scan(&legacyCTO)
		if err != nil && err != sql.ErrNoRows {
			return msg, false, err
		}
		if legacyCTO != "" && m.To == parent.From {
			m.To = legacyCTO
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
			WHERE e.target=CASE WHEN dst.harness='cto' THEN ? ELSE COALESCE(NULLIF(dst.repo_project,''),dp.project,'') END
			AND e.target<>'' AND (e.source=CASE WHEN src.harness='cto' THEN ? ELSE COALESCE(NULLIF(src.repo_project,''),sp.project,'') END OR e.source='*')
			ORDER BY CASE WHEN e.source='*' THEN 1 ELSE 0 END, e.kind LIMIT 1`, m.From, m.To, protocol.CTONodeName, protocol.CTONodeName).Scan(&rule)
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
		idem_key, created_at, expires_at, state, sender_cto, owner_approved) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body, m.Hop,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State, m.FromCTO, m.OwnerApproved)
	if err != nil {
		return msg, false, err
	}
	if m.ParentID == "" && !m.FromCTO {
		if err := s.signalRequest(tx, m); err != nil {
			return msg, false, err
		}
	}
	if p.Outcome != "" {
		var from, to string
		if err := tx.QueryRow(`SELECT COALESCE(NULLIF(a.repo_project,''),ap.project,''),COALESCE(NULLIF(b.repo_project,''),bp.project,'') FROM messages q JOIN sessions a ON a.id=q.sender JOIN sessions b ON b.id=q.recipient LEFT JOIN profiles ap ON ap.session_id=a.id LEFT JOIN profiles bp ON bp.session_id=b.id WHERE q.id=? AND q.type=?`, p.ParentID, protocol.TypeRequest).Scan(&from, &to); err != nil {
			return msg, false, reject("outcome requires a request parent")
		}
		if _, err := tx.Exec(`INSERT INTO graph_outcomes(source,target,message_id,reply_id,outcome,reason,redirect_to,recommendation,at) VALUES(?,?,?,?,?,?,?,?,?)`, from, to, p.ParentID, m.ID, p.Outcome, p.Reason, p.RedirectTo, p.Recommendation, now.UnixNano()); err != nil {
			return msg, false, err
		}
		if err := s.signalOutcome(tx, m, p); err != nil {
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	filtered := out[:0]
	for _, m := range out {
		if m.From == "agent-net" && !isWakeMessage(m) {
			eligible, err := s.reminderEligible(m.ParentID)
			if err != nil {
				return nil, err
			}
			if !eligible {
				continue
			}
		}
		filtered = append(filtered, m)
	}
	return filtered, nil
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
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO deliveries (message_id, state, detail, at) VALUES (?,?,?,?)`, id, state, detail, now); err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE messages SET state = ? WHERE id = ? AND state IN ('stored','dispatched')`, state, id)
	if err != nil {
		return err
	}
	if state == protocol.StateDelivered {
		if err = s.learnDelivery(tx, id, now); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
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
	messages, _, err := s.MessagesPage(nodeID, q)
	return messages, err
}

func (s *Store) MessagesPage(nodeID string, q protocol.QueryMessages) ([]protocol.Message, int, error) {
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}
	where := `(m.sender IN (SELECT id FROM sessions WHERE node_id = ?) OR m.recipient IN (SELECT id FROM sessions WHERE node_id = ?))`
	args := []any{nodeID, nodeID}
	if q.SessionID != "" {
		owner, err := s.SessionNode(q.SessionID)
		if err != nil {
			return nil, 0, err
		}
		if owner != nodeID {
			return nil, 0, reject("unknown session %q", q.SessionID)
		}
		where += ` AND (m.sender = ? OR m.recipient = ?)`
		args = append(args, q.SessionID, q.SessionID)
	}
	if q.ConversationID != "" {
		where += ` AND m.conversation_id = ?`
		args = append(args, q.ConversationID)
	}
	var total int
	if err := s.db.QueryRowContext(context.Background(), `SELECT count(*) FROM messages m WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	args = append(args, q.Limit, q.Offset)
	rows, err := s.db.QueryContext(context.Background(), msgSelect+` WHERE `+where+` ORDER BY m.created_at DESC, m.rowid DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []protocol.Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}

// workerNoticeLifecycleSQL is true for a worker notice that carries no content
// of its own: a start that is queued or has begun, or a start that never
// became a worker. The list skips these messages as rows and as activity, but
// the worker's conversation still shows them.
func workerNoticeLifecycleSQL(alias string) string {
	return `(` + alias + `.body LIKE '[agent-net worker notice]%' AND (
	instr(` + alias + `.body, char(10)||'status: started'||char(10)) > 0 OR
	instr(` + alias + `.body, char(10)||'status: queued'||char(10)) > 0 OR
	instr(` + alias + `.body, char(10)||'status: not started'||char(10)) > 0))`
}

// workerSessionIDs finds the worker sessions from data that outlives the
// session record: the live worker records, and the notices and task briefs
// that only a worker or its caller sends.
func (s *Store) workerSessionIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE worker <> ''
		UNION
		SELECT m.sender FROM messages m WHERE m.body LIKE '[agent-net worker notice]%'
			AND instr(m.body, char(10)||'status: not started'||char(10)) = 0
			AND NOT EXISTS (SELECT 1 FROM sessions s WHERE s.id = m.sender)
		UNION
		SELECT m.recipient FROM messages m WHERE m.body LIKE '[agent-net worker brief]%'
			AND NOT EXISTS (SELECT 1 FROM sessions s WHERE s.id = m.recipient)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// workerValuesCTE binds the worker session IDs once, for the queries that use
// them. It returns the CTE prefix and its arguments, or empty when there are
// no workers.
func workerValuesCTE(ids []string) (string, []any) {
	if len(ids) == 0 {
		return "", nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return `WITH wid(id) AS MATERIALIZED (VALUES ` + strings.TrimSuffix(strings.Repeat("(?),", len(ids)), ",") + `)`, args
}

// ConversationsPage lists every stored conversation on the gateway, newest
// activity first, with a keyset cursor. The caller is the owner, so it is not
// limited to one machine's sessions (docs/security.md). Normal conversations
// keep the indexed conversation_id query. Workers are grouped under
// "worker:<session>" and read through the sender and recipient indexes, so a
// worker with a started, a task and a hand-back notice has one row, not three.
// The gateway does the grouping and filtering, never the screen.
func (s *Store) ConversationsPage(q protocol.QueryConversations) (protocol.ConversationPage, error) {
	page := protocol.ConversationPage{Conversations: []protocol.ConversationView{}}
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 50
	}
	workers, err := s.workerSessionIDs()
	if err != nil {
		return page, err
	}
	wid, widArgs := workerValuesCTE(workers)

	normalWhere := `m.conversation_id <> '' AND NOT ` + workerNoticeLifecycleSQL("m")
	if len(workers) > 0 {
		normalWhere += ` AND m.conversation_id NOT IN (
			SELECT m2.conversation_id FROM messages m2
			WHERE m2.sender IN (SELECT id FROM wid) OR m2.recipient IN (SELECT id FROM wid))`
	}
	countSQL := wid + ` SELECT COUNT(*) FROM (
		SELECT m.conversation_id FROM messages m WHERE ` + normalWhere + ` GROUP BY m.conversation_id)`
	if err := s.db.QueryRow(countSQL, widArgs...).Scan(&page.Total); err != nil {
		return page, err
	}

	normalPrefix := "WITH "
	if wid != "" {
		normalPrefix = wid + ", "
	}
	sqlText := normalPrefix + `page AS (
		SELECT m.conversation_id AS gid, MAX(m.created_at) AS last_at
		FROM messages m WHERE ` + normalWhere + `
		GROUP BY m.conversation_id`
	args := append([]any{}, widArgs...)
	if at, id, ok := parseConversationCursor(q.Cursor); ok {
		sqlText += ` HAVING last_at < ? OR (last_at = ? AND gid < ?)`
		args = append(args, at, at, id)
	}
	sqlText += ` ORDER BY last_at DESC, gid DESC LIMIT ?)
	SELECT p.gid, p.last_at, m.id, m.sender, m.recipient, m.body, m.state, m.sender_cto, m.type,
		(SELECT COUNT(*) FROM messages c WHERE c.conversation_id = p.gid
			AND NOT ` + workerNoticeLifecycleSQL("c") + `),
		COALESCE(NULLIF(fp.name,''), fs.name, ''),
		COALESCE(NULLIF(rp.name,''), rs.name, ''),
		EXISTS(SELECT 1 FROM charter_signals cs WHERE cs.message_id = m.id)
	FROM page p
	JOIN messages m ON m.conversation_id = p.gid AND m.created_at = p.last_at
		AND m.rowid = (SELECT MAX(m2.rowid) FROM messages m2
			WHERE m2.conversation_id = p.gid AND m2.created_at = p.last_at)
	LEFT JOIN sessions fs ON fs.id = m.sender
	LEFT JOIN profiles fp ON fp.session_id = m.sender
	LEFT JOIN sessions rs ON rs.id = m.recipient
	LEFT JOIN profiles rp ON rp.session_id = m.recipient
	ORDER BY p.last_at DESC, p.gid DESC`
	args = append(args, q.Limit+1)
	normal, err := s.scanConversations(sqlText, args...)
	if err != nil {
		return page, err
	}
	rows := normal

	if len(workers) > 0 {
		workerBase := wid + `, wm AS MATERIALIZED (
			SELECT m.rowid AS rid, m.id, m.conversation_id, m.created_at, m.sender, m.recipient,
				m.type, m.body, m.state, m.sender_cto,
				CASE WHEN m.sender IN (SELECT id FROM wid) THEN 'worker:' || m.sender
					ELSE 'worker:' || m.recipient END AS gid,
				` + workerNoticeLifecycleSQL("m") + ` AS lifecycle
			FROM messages m
			WHERE m.conversation_id <> ''
				AND (m.sender IN (SELECT id FROM wid) OR m.recipient IN (SELECT id FROM wid))
		)`
		var n int
		if err := s.db.QueryRow(workerBase+` SELECT COUNT(*) FROM (
			SELECT gid FROM wm WHERE lifecycle = 0 AND gid <> '' GROUP BY gid)`, widArgs...).Scan(&n); err != nil {
			return page, err
		}
		page.Total += n

		workerSQL := workerBase + `, live AS MATERIALIZED (SELECT * FROM wm WHERE lifecycle = 0), latest AS MATERIALIZED (
			SELECT gid, rid, id, sender, recipient, body, state, sender_cto, type, created_at,
				COUNT(*) OVER (PARTITION BY gid) AS n,
				ROW_NUMBER() OVER (PARTITION BY gid ORDER BY created_at DESC, rid DESC) AS rn
			FROM live WHERE gid <> ''
		), page AS (
			SELECT gid, created_at AS last_at, id, sender, recipient, body, state, sender_cto, type, n
			FROM latest WHERE rn = 1`
		wargs := append([]any{}, widArgs...)
		if at, id, ok := parseConversationCursor(q.Cursor); ok {
			workerSQL += ` AND (created_at < ? OR (created_at = ? AND gid < ?))`
			wargs = append(wargs, at, at, id)
		}
		workerSQL += ` ORDER BY created_at DESC, gid DESC LIMIT ?)
		SELECT p.gid, p.last_at, p.id, p.sender, p.recipient, p.body, p.state, p.sender_cto, p.type, p.n,
			COALESCE(NULLIF(fp.name,''), fs.name, ''),
			COALESCE(NULLIF(rp.name,''), rs.name, ''),
			EXISTS(SELECT 1 FROM charter_signals cs WHERE cs.message_id = p.id)
		FROM page p
		LEFT JOIN sessions fs ON fs.id = p.sender
		LEFT JOIN profiles fp ON fp.session_id = p.sender
		LEFT JOIN sessions rs ON rs.id = p.recipient
		LEFT JOIN profiles rp ON rp.session_id = p.recipient
		ORDER BY p.last_at DESC, p.gid DESC`
		wargs = append(wargs, q.Limit+1)
		workerRows, err := s.scanConversations(workerSQL, wargs...)
		if err != nil {
			return page, err
		}
		// The worker facts are reads of their own, and the store may have one
		// connection.
		for i := range workerRows {
			if session, ok := strings.CutPrefix(workerRows[i].ID, "worker:"); ok {
				s.workerRow(&workerRows[i], session)
			}
		}
		rows = append(rows, workerRows...)
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].At.Equal(rows[j].At) {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].At.After(rows[j].At)
	})
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
		last := rows[len(rows)-1]
		page.Next = fmt.Sprintf("%d|%s", last.At.UnixNano(), last.ID)
	}
	page.Conversations = rows
	return page, nil
}

// scanConversations reads the rows both conversation queries select.
func (s *Store) scanConversations(sqlText string, args ...any) ([]protocol.ConversationView, error) {
	rows, err := s.db.QueryContext(context.Background(), sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.ConversationView{}
	for rows.Next() {
		var v protocol.ConversationView
		var at int64
		var senderCTO, ownerInstruction int
		var typ string
		if err := rows.Scan(&v.ID, &at, &v.LastMessageID, &v.From, &v.To, &v.LastLine, &v.State,
			&senderCTO, &typ, &v.Count, &v.FromName, &v.ToName, &ownerInstruction); err != nil {
			return nil, err
		}
		v.Type = typ
		v.At = time.Unix(0, at).UTC()
		v.LastLine = conversationLine(v.LastLine)
		v.FromCTO = senderCTO != 0
		v.OwnerInstruction = ownerInstruction != 0
		out = append(out, v)
	}
	return out, rows.Err()
}

// workerRow fills the caller and task of a worker row from messages that
// survive the worker's session record: the first message sent to the worker,
// or its stored worker info when no task message is left. The row is named
// from these facts, never from a missing session.
func (s *Store) workerRow(v *protocol.ConversationView, session string) {
	v.WorkerSession = session
	var caller, body string
	if err := s.db.QueryRow(`SELECT m.sender, m.body FROM messages m
		WHERE m.recipient = ? AND m.conversation_id <> '' AND NOT `+workerNoticeLifecycleSQL("m")+`
		ORDER BY m.created_at ASC, m.rowid ASC LIMIT 1`, session).Scan(&caller, &body); err != nil {
		caller, body = "", ""
	}
	if body != "" {
		v.Task = conversationLine(protocol.WorkerTaskSummary(body))
	}
	if caller != "" {
		v.WorkerOwner = caller
		v.From = caller
		v.To = session
		if name := s.displayName(caller); name != "" {
			v.FromName = name
		}
	}
	if v.Task == "" || v.WorkerOwner == "" {
		var owner, task, name string
		err := s.db.QueryRow(`SELECT COALESCE(json_extract(s.worker,'$.owner'),''),
			COALESCE(json_extract(s.worker,'$.task'),''), COALESCE(NULLIF(p.name,''), s.name, '')
			FROM sessions s LEFT JOIN profiles p ON p.session_id = s.id WHERE s.id = ?`, session).Scan(&owner, &task, &name)
		if err == nil {
			if v.Task == "" {
				v.Task = conversationLine(task)
			}
			if v.WorkerOwner == "" && owner != "" {
				v.WorkerOwner = owner
				v.From = owner
				v.To = session
				if ownerName := s.displayName(owner); ownerName != "" {
					v.FromName = ownerName
				}
			}
		}
	}
}

// displayName is the name the screen would draw for a session, or empty when
// its record is gone.
func (s *Store) displayName(id string) string {
	var name string
	if err := s.db.QueryRow(`SELECT COALESCE(NULLIF(p.name,''), s.name, '')
		FROM sessions s LEFT JOIN profiles p ON p.session_id = s.id WHERE s.id = ?`, id).Scan(&name); err != nil {
		return ""
	}
	return name
}

// ConversationMessages returns every stored message of one conversation,
// newest first. A "worker:<session>" ID returns every message that involves
// that worker, including the lifecycle notices the list hides. The caller is
// the owner, so it is not limited to one machine's sessions. Agents keep the
// node-scoped MessagesPage read.
func (s *Store) ConversationMessages(conversationID string, limit int) ([]protocol.Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	sqlText := msgSelect + `
		WHERE m.conversation_id = ? ORDER BY m.created_at DESC, m.rowid DESC LIMIT ?`
	args := []any{conversationID, limit}
	if session, ok := strings.CutPrefix(conversationID, "worker:"); ok && session != "" {
		sqlText = msgSelect + `
			WHERE m.sender = ? OR m.recipient = ? ORDER BY m.created_at DESC, m.rowid DESC LIMIT ?`
		args = []any{session, session, limit}
	}
	rows, err := s.db.QueryContext(context.Background(), sqlText, args...)
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

// parseConversationCursor reads the Next value of a page.
func parseConversationCursor(cursor string) (int64, string, bool) {
	at, id, ok := strings.Cut(cursor, "|")
	if !ok || id == "" {
		return 0, "", false
	}
	n, err := strconv.ParseInt(at, 10, 64)
	if err != nil {
		return 0, "", false
	}
	return n, id, true
}

// conversationLine keeps the first line of a body for a list row: no control
// characters, no line break, and at most 200 characters.
func conversationLine(body string) string {
	line, _, _ := strings.Cut(body, "\n")
	line = strings.Map(func(r rune) rune {
		switch {
		case r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, strings.TrimSpace(line))
	if r := []rune(line); len(r) > 200 {
		line = string(r[:199]) + "…"
	}
	return line
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

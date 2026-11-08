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
	"sync/atomic"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/dbfile"
	"github.com/niparis/agent-net/internal/phrasing"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/sqlitecache"
	"github.com/niparis/agent-net/internal/temppath"
	_ "modernc.org/sqlite"
)

// Rejection is an error caused by the caller breaking a rule, not by a fault.
type Rejection string

func (r Rejection) Error() string { return string(r) }

func reject(format string, args ...any) error { return Rejection(fmt.Sprintf(format, args...)) }

// Store is the gateway's SQLite database.
type Store struct {
	graphRefsOn     atomic.Bool
	settingsHookMu  sync.RWMutex
	settingsHook    func()
	qualityMu       sync.Mutex
	runsMu          sync.Mutex
	runs            *runsSnapshot
	quality         protocol.QualityAnalysis
	qualityFit      qualityFit
	reworkFit       qualityFit
	qualityStrong   map[string]int
	reminderMu      sync.Mutex
	reminderOffline map[string]bool
	checkMu         sync.Mutex
	pendingMu       sync.Mutex
	pendingEpisodes map[string]pendingEpisode
	pendingStalls   int
	db              *sql.DB
	// connector opens the database connection. Its write count says when
	// data read from the database may be out of date.
	connector *sqlitecache.Connector
	// rc keeps reads that stay current until the next write. See readThrough.
	rc                readCache
	graphJunkRemoved  int64
	graphJunkMigrated bool
	identityMerges    [][2]string
	identityConflicts [][2]string
	// tempFolders lists folders whose sessions belong to no project. Nil
	// uses the system temporary folders; an empty list turns the rule off.
	tempFolders []string
	now         func() time.Time
	// logf writes a structured log line; the gateway sets it. Never pass a
	// message body to it.
	logf func(event string, kv ...any)

	// path is the gateway.db file, for the size before and after a vacuum.
	path string
	// vacuumMu guards the weekly schedule of the vacuum.
	vacuumMu   sync.Mutex
	lastVacuum time.Time

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

// tempRoots is the list of folders that hold no project. Nil means the system
// temporary folders; an empty list turns the rule off (tests).
func (s *Store) tempRoots() []string {
	if s.tempFolders != nil {
		return s.tempFolders
	}
	return temppath.Default()
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
`, `
-- A session whose folder is temporary belongs to no project, so temp test
-- runs do not fill the project list. The session itself stays.
SELECT 1 /* session no project column */;
`, `
-- The recorded start of the announced task (tool redesign, task 2a). It moves
-- only when the task changes, never on last activity.
SELECT 1 /* profile task started column */;
`, `
CREATE TABLE IF NOT EXISTS apps (
 id TEXT PRIMARY KEY, name TEXT NOT NULL,
 kind TEXT NOT NULL CHECK (kind IN ('owner_app', 'bot')),
 client_id TEXT NOT NULL, client_name TEXT NOT NULL,
 redirect_uris TEXT NOT NULL DEFAULT '[]',
 created_at INTEGER NOT NULL, last_used_at INTEGER, revoked_at INTEGER,
 grants TEXT NOT NULL DEFAULT '[]');
CREATE UNIQUE INDEX IF NOT EXISTS apps_active_name ON apps (name) WHERE revoked_at IS NULL;
CREATE TABLE IF NOT EXISTS app_tokens (
 hash TEXT PRIMARY KEY, app_id TEXT NOT NULL REFERENCES apps(id),
 type TEXT NOT NULL CHECK (type IN ('access', 'refresh')),
 expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS app_tokens_app ON app_tokens (app_id);
CREATE INDEX IF NOT EXISTS app_tokens_expiry ON app_tokens (expires_at);
`, `
-- OAuth clients and approvals. Repeating the app DDL also makes this
-- migration safe to replay when recovering an incomplete app migration.
CREATE TABLE IF NOT EXISTS apps (
 id TEXT PRIMARY KEY, name TEXT NOT NULL,
 kind TEXT NOT NULL CHECK (kind IN ('owner_app', 'bot')),
 client_id TEXT NOT NULL, client_name TEXT NOT NULL,
 redirect_uris TEXT NOT NULL DEFAULT '[]',
 created_at INTEGER NOT NULL, last_used_at INTEGER, revoked_at INTEGER,
 grants TEXT NOT NULL DEFAULT '[]');
CREATE UNIQUE INDEX IF NOT EXISTS apps_active_name ON apps (name) WHERE revoked_at IS NULL;
CREATE TABLE IF NOT EXISTS app_tokens (
 hash TEXT PRIMARY KEY, app_id TEXT NOT NULL REFERENCES apps(id),
 type TEXT NOT NULL CHECK (type IN ('access', 'refresh')),
 expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS app_tokens_app ON app_tokens (app_id);
CREATE INDEX IF NOT EXISTS app_tokens_expiry ON app_tokens (expires_at);
CREATE TABLE IF NOT EXISTS oauth_clients (
 client_id TEXT PRIMARY KEY, client_name TEXT NOT NULL, redirect_uris TEXT NOT NULL,
 grant_types TEXT NOT NULL, response_types TEXT NOT NULL,
 token_endpoint_auth_method TEXT NOT NULL, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS oauth_pending (
 id TEXT PRIMARY KEY, user_code TEXT NOT NULL, client_id TEXT NOT NULL,
 redirect_uri TEXT NOT NULL, code_challenge TEXT NOT NULL, state TEXT NOT NULL,
 resource TEXT NOT NULL, created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL,
 status TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'refused')),
 app_id TEXT NOT NULL DEFAULT '', approved_at INTEGER);
CREATE UNIQUE INDEX IF NOT EXISTS oauth_pending_user_code ON oauth_pending (user_code) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS oauth_pending_reuse ON oauth_pending (client_id, redirect_uri, approved_at);
CREATE TABLE IF NOT EXISTS oauth_codes (
 hash TEXT PRIMARY KEY, pending_id TEXT NOT NULL, app_id TEXT NOT NULL,
 client_id TEXT NOT NULL, redirect_uri TEXT NOT NULL, code_challenge TEXT NOT NULL,
 expires_at INTEGER NOT NULL, used_at INTEGER);
CREATE INDEX IF NOT EXISTS oauth_codes_pending ON oauth_codes (pending_id);
CREATE TABLE IF NOT EXISTS oauth_code_misses (source TEXT NOT NULL, attempted_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS oauth_code_misses_source ON oauth_code_misses (source, attempted_at);
`, `
-- OAuth pending browser hash. Repeat app DDL for migration replay recovery.
CREATE TABLE IF NOT EXISTS apps (
 id TEXT PRIMARY KEY, name TEXT NOT NULL,
 kind TEXT NOT NULL CHECK (kind IN ('owner_app', 'bot')),
 client_id TEXT NOT NULL, client_name TEXT NOT NULL,
 redirect_uris TEXT NOT NULL DEFAULT '[]',
 created_at INTEGER NOT NULL, last_used_at INTEGER, revoked_at INTEGER,
 grants TEXT NOT NULL DEFAULT '[]');
CREATE UNIQUE INDEX IF NOT EXISTS apps_active_name ON apps (name) WHERE revoked_at IS NULL;
CREATE TABLE IF NOT EXISTS app_tokens (
 hash TEXT PRIMARY KEY, app_id TEXT NOT NULL REFERENCES apps(id),
 type TEXT NOT NULL CHECK (type IN ('access', 'refresh')),
 expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS app_tokens_app ON app_tokens (app_id);
CREATE INDEX IF NOT EXISTS app_tokens_expiry ON app_tokens (expires_at);
`, `
-- Token families and source limits for the public app listener.
CREATE TABLE IF NOT EXISTS oauth_registration_attempts (source TEXT NOT NULL, created_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS oauth_registration_attempts_source ON oauth_registration_attempts (source,created_at);
`, `
-- App token purpose, OAuth retry code and refresh rotation budget.
CREATE TABLE IF NOT EXISTS app_refresh_budget (
 family_id TEXT PRIMARY KEY, window_started_at INTEGER NOT NULL, rotations INTEGER NOT NULL);
`, `
-- Bound consumed refresh history already present on upgrade.
DELETE FROM app_tokens WHERE rowid IN (
 SELECT rowid FROM (SELECT rowid, row_number() OVER
  (PARTITION BY app_id ORDER BY used_at DESC, rowid DESC) AS rank
  FROM app_tokens WHERE type='refresh' AND used_at IS NOT NULL) WHERE rank>2000);
`, `
-- The worker's short title, beside the worker record. Records made before
-- this column existed read an empty title, and a view derives one from the
-- task. The column is added by the migration code so a replay is safe.
SELECT 1 /* worker title column */;
`, `
CREATE TABLE IF NOT EXISTS worker_assessments (
 run_id TEXT PRIMARY KEY, session_id TEXT NOT NULL, started_at INTEGER NOT NULL,
 assessed_at INTEGER NOT NULL, reasoning INTEGER NOT NULL, scope INTEGER NOT NULL,
 coupling INTEGER NOT NULL, uncertainty INTEGER NOT NULL, verification INTEGER NOT NULL,
 risk INTEGER NOT NULL, d REAL NOT NULL, label TEXT NOT NULL, bottleneck TEXT NOT NULL,
 confidence TEXT NOT NULL, rules_version TEXT NOT NULL, judge_harness TEXT NOT NULL,
 judge_model TEXT NOT NULL, reasons TEXT NOT NULL, picker_label TEXT NOT NULL, error TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS worker_assessments_session ON worker_assessments(session_id, started_at DESC);
`, `
CREATE TABLE IF NOT EXISTS tool_project_settings (item TEXT PRIMARY KEY, new_projects INTEGER NOT NULL CHECK(new_projects IN (0,1)));
CREATE TABLE IF NOT EXISTS tool_project_seen (item TEXT NOT NULL, project TEXT NOT NULL, PRIMARY KEY(item,project));
`, `
CREATE INDEX IF NOT EXISTS deliveries_message_state ON deliveries(message_id, state, id);
`, `
SELECT 1 /* machine token revocation columns */;
CREATE TRIGGER IF NOT EXISTS removed_node_session_insert BEFORE INSERT ON sessions
 WHEN EXISTS(SELECT 1 FROM nodes WHERE id=NEW.node_id AND removed=1)
 BEGIN SELECT RAISE(ABORT,'machine was removed from the network'); END;
CREATE TRIGGER IF NOT EXISTS removed_node_session_online BEFORE UPDATE OF online ON sessions
 WHEN NEW.online=1 AND EXISTS(SELECT 1 FROM nodes WHERE id=NEW.node_id AND removed=1)
 BEGIN SELECT RAISE(ABORT,'machine was removed from the network'); END;
`, `
SELECT 1 /* model auth failure column */;
`, `
-- Uploads in parts (decision 0054). One row per upload and one per stored
-- part; the bytes live under <gateway data>/transfers/<upload id>/ and are
-- deleted when the receiver confirms or after seven days.
CREATE TABLE IF NOT EXISTS transfers (
 id TEXT PRIMARY KEY,
 sender TEXT NOT NULL,
 receiver TEXT NOT NULL,
 name TEXT NOT NULL DEFAULT '',
 size INTEGER NOT NULL,
 sha256 TEXT NOT NULL,
 part_size INTEGER NOT NULL,
 parts INTEGER NOT NULL,
 created_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL,
 finished_at INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS transfers_sender ON transfers(sender);
CREATE INDEX IF NOT EXISTS transfers_receiver ON transfers(receiver);
CREATE INDEX IF NOT EXISTS transfers_expires ON transfers(expires_at);
CREATE TABLE IF NOT EXISTS transfer_parts (
 id TEXT NOT NULL,
 idx INTEGER NOT NULL,
 size INTEGER NOT NULL,
 sha256 TEXT NOT NULL,
 at INTEGER NOT NULL,
 PRIMARY KEY(id, idx));
`, `
-- The picker's full explanation for a chosen start, for the owner's worker
-- view. Runs recorded before this column existed read an empty note, and the
-- view shows a dash. The column is added by the migration code so a replay is
-- safe.
SELECT 1 /* worker picker note */;
`, `
-- The session that started the worker, kept on the run so the owner view can
-- show the caller after the worker session is cleaned. Old rows read empty and
-- fall back to the worker record of their session while it still exists.
SELECT 1 /* worker caller column */;
`, `
CREATE TABLE IF NOT EXISTS delivery_retries (message_id TEXT PRIMARY KEY, attempts INTEGER NOT NULL, next_at INTEGER NOT NULL);
`, `
-- The IANA time zone each machine reported, for the owner's display time
-- zone. A separate table so the migration can run again (append-only lock).
CREATE TABLE IF NOT EXISTS node_time_zones (node_id TEXT PRIMARY KEY, zone TEXT NOT NULL, updated_at INTEGER NOT NULL);
`, `
CREATE TABLE IF NOT EXISTS setting_revision (id INTEGER PRIMARY KEY CHECK (id = 1), revision INTEGER NOT NULL CHECK (revision >= 0));
INSERT OR IGNORE INTO setting_revision VALUES (1, 0);
CREATE TABLE IF NOT EXISTS setting_values (
 key TEXT NOT NULL, level TEXT NOT NULL CHECK (level IN ('network', 'team', 'repo', 'agent')),
 scope TEXT NOT NULL, item TEXT NOT NULL DEFAULT '', value TEXT NOT NULL CHECK (json_valid(value)),
 revision INTEGER NOT NULL, PRIMARY KEY (key, level, scope, item),
 CHECK (level <> 'network' OR scope = 'network'));
CREATE TABLE IF NOT EXISTS setting_history (
 revision INTEGER NOT NULL, seq INTEGER NOT NULL, key TEXT NOT NULL, level TEXT NOT NULL,
 scope TEXT NOT NULL, item TEXT NOT NULL DEFAULT '', old_value TEXT, new_value TEXT,
 actor TEXT NOT NULL, reason TEXT NOT NULL,
 cause TEXT NOT NULL CHECK (cause IN ('write', 'parent', 'undo', 'release', 'cutover')),
 undo_of INTEGER, at INTEGER NOT NULL, PRIMARY KEY (revision, seq),
 CHECK (old_value IS NOT NULL OR new_value IS NOT NULL));
CREATE TRIGGER IF NOT EXISTS setting_history_no_update BEFORE UPDATE ON setting_history
BEGIN SELECT RAISE(ABORT, 'setting history is append-only'); END;
CREATE TRIGGER IF NOT EXISTS setting_history_no_delete BEFORE DELETE ON setting_history
BEGIN SELECT RAISE(ABORT, 'setting history is append-only'); END;
`, `
SELECT 1 /* delivery settings cutover */;
`, `
-- A provider account that a worker run reported out of quota, per machine,
-- until the reset the provider named. The gateway adds it to the machine's
-- quota reading, so starts on that provider are refused until then.
CREATE TABLE IF NOT EXISTS quota_blocks (
 node_id TEXT NOT NULL, provider TEXT NOT NULL, resets_at INTEGER NOT NULL,
 detail TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL,
 PRIMARY KEY (node_id, provider));
`, `
SELECT 1 /* worker sandbox cutover */;
`, `
-- The manifest of the files that come with a message (decision 0054), and the
-- Tailscale address each machine reported, so a sender can push the bytes
-- over Taildrop. Separate tables so the migration can run again.
CREATE TABLE IF NOT EXISTS message_files (message_id TEXT PRIMARY KEY, files TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS node_tailscale (node_id TEXT PRIMARY KEY, ip TEXT NOT NULL, updated_at INTEGER NOT NULL);
`, `
-- The delivery pipeline: delivery changes, revisions and evidence (decision
-- 0057, build step 1). seq orders changes newest first. At most one open
-- change per project and branch. A revision is the head at one complete
-- hand-back or submit; evidence is append-only, like setting_history.
CREATE TABLE IF NOT EXISTS delivery_changes (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 project TEXT NOT NULL, machine TEXT NOT NULL DEFAULT '', branch TEXT NOT NULL,
 target_branch TEXT NOT NULL DEFAULT '', base_commit TEXT NOT NULL DEFAULT '',
 head_commit TEXT NOT NULL, uncommitted INTEGER NOT NULL DEFAULT 0,
 revision INTEGER NOT NULL CHECK (revision >= 1),
 origin TEXT NOT NULL CHECK (json_valid(origin)), caller TEXT NOT NULL DEFAULT '',
 title TEXT NOT NULL DEFAULT '', difficulty TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL CHECK (state IN ('open', 'merged', 'done', 'stopped')),
 merged_commit TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL DEFAULT 0);
CREATE UNIQUE INDEX IF NOT EXISTS delivery_changes_open_branch ON delivery_changes(project, branch) WHERE state = 'open';
CREATE INDEX IF NOT EXISTS delivery_changes_project ON delivery_changes(project, seq);
CREATE TABLE IF NOT EXISTS delivery_revisions (
 change_id TEXT NOT NULL, number INTEGER NOT NULL CHECK (number >= 1),
 branch TEXT NOT NULL, commit_hash TEXT NOT NULL, uncommitted INTEGER NOT NULL DEFAULT 0,
 at INTEGER NOT NULL, PRIMARY KEY (change_id, number));
CREATE TABLE IF NOT EXISTS delivery_evidence (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, change_id TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK (revision >= 1), step TEXT NOT NULL,
 for_step TEXT NOT NULL DEFAULT '', after_merge INTEGER NOT NULL DEFAULT 0,
 result TEXT NOT NULL CHECK (result IN ('pass', 'fail', 'needs_owner', 'close')),
 commit_hash TEXT NOT NULL, fields TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(fields)),
 by_session TEXT NOT NULL, reported_by TEXT NOT NULL DEFAULT '',
 by_model TEXT NOT NULL DEFAULT '', by_model_family TEXT NOT NULL DEFAULT '',
 note TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS delivery_evidence_change ON delivery_evidence(change_id, seq);
CREATE TRIGGER IF NOT EXISTS delivery_evidence_no_update BEFORE UPDATE ON delivery_evidence
BEGIN SELECT RAISE(ABORT, 'delivery evidence is append-only'); END;
CREATE TRIGGER IF NOT EXISTS delivery_evidence_no_delete BEFORE DELETE ON delivery_evidence
BEGIN SELECT RAISE(ABORT, 'delivery evidence is append-only'); END;
`, `
-- worker starts the gateway tracks: a not-started report must name one
CREATE TABLE IF NOT EXISTS worker_starts (
 ref TEXT PRIMARY KEY, node_id TEXT NOT NULL, owner TEXT NOT NULL,
 project TEXT NOT NULL DEFAULT '', harness TEXT NOT NULL DEFAULT '',
 permissions TEXT NOT NULL DEFAULT '', session_id TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed', 'not_started')),
 created_at INTEGER NOT NULL, closed_at INTEGER NOT NULL DEFAULT 0);
`, `
-- A node-owned step survives the owner's sessions going offline. The owner
-- and the revision are captured when the step becomes ready.
CREATE TABLE IF NOT EXISTS delivery_step_tasks (
 change_id TEXT NOT NULL, revision INTEGER NOT NULL, step TEXT NOT NULL,
 for_step TEXT NOT NULL DEFAULT '', after_merge INTEGER NOT NULL DEFAULT 0,
 owner_node TEXT NOT NULL, claimed_by TEXT NOT NULL DEFAULT '',
 completed INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL,
 PRIMARY KEY(change_id, revision, step, for_step, after_merge));
CREATE TABLE IF NOT EXISTS delivery_step_messages (
 change_id TEXT NOT NULL, revision INTEGER NOT NULL, step TEXT NOT NULL,
 for_step TEXT NOT NULL, after_merge INTEGER NOT NULL, session_id TEXT NOT NULL,
 message_id TEXT NOT NULL,
 PRIMARY KEY(change_id, revision, step, for_step, after_merge, session_id));
`, `
-- A diff stat belongs to the revision named by its head commit.
CREATE TABLE IF NOT EXISTS delivery_revision_stats (
 change_id TEXT NOT NULL, revision INTEGER NOT NULL, diff_stat TEXT NOT NULL,
 PRIMARY KEY(change_id, revision));
`, `
-- Inbox approvals refer to an exact change revision and commit.
CREATE TABLE IF NOT EXISTS delivery_step_inbox (
 change_id TEXT NOT NULL, revision INTEGER NOT NULL, step TEXT NOT NULL,
 for_step TEXT NOT NULL DEFAULT '', after_merge INTEGER NOT NULL DEFAULT 0,
 commit_hash TEXT NOT NULL, item_id TEXT NOT NULL UNIQUE,
 PRIMARY KEY(change_id, revision, step, for_step, after_merge));
`, `
-- Delegates are resolved when an approval step starts.
CREATE TABLE IF NOT EXISTS delivery_step_delegates (
 change_id TEXT NOT NULL, revision INTEGER NOT NULL, step TEXT NOT NULL,
 for_step TEXT NOT NULL DEFAULT '', after_merge INTEGER NOT NULL DEFAULT 0,
 node TEXT NOT NULL,
 PRIMARY KEY(change_id, revision, step, for_step, after_merge, node));
`, `
-- delivery pipeline settings cutover: retain old values for downgrade.
CREATE TABLE IF NOT EXISTS delivery_settings_legacy (
 key TEXT NOT NULL, level TEXT NOT NULL, scope TEXT NOT NULL,
 item TEXT NOT NULL DEFAULT '', value TEXT NOT NULL, revision INTEGER NOT NULL,
 PRIMARY KEY(key, level, scope, item));
`, `
SELECT 1 /* worker task marker and run state columns */;
`, `
SELECT 1 /* delivery review risk columns */;

`, `
SELECT 1 /* worker start tracking: legacy daemon references and launch timeout tracking */;
CREATE INDEX IF NOT EXISTS worker_starts_legacy_ref ON worker_starts(legacy_ref) WHERE legacy_ref <> '';
`, `
SELECT 1 /* worker start state: starting */;
CREATE TABLE IF NOT EXISTS worker_starts_new (
 ref TEXT PRIMARY KEY, node_id TEXT NOT NULL, owner TEXT NOT NULL,
 project TEXT NOT NULL DEFAULT '', harness TEXT NOT NULL DEFAULT '',
 permissions TEXT NOT NULL DEFAULT '', session_id TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'starting', 'closed', 'not_started')),
 created_at INTEGER NOT NULL, closed_at INTEGER NOT NULL DEFAULT 0,
 legacy_ref TEXT NOT NULL DEFAULT '', launch_state TEXT NOT NULL DEFAULT ''
);
INSERT INTO worker_starts_new(ref, node_id, owner, project, harness, permissions, session_id, state, created_at, closed_at, legacy_ref, launch_state)
 SELECT ref, node_id, owner, project, harness, permissions, session_id, state, created_at, closed_at, legacy_ref, launch_state FROM worker_starts;
DROP TABLE worker_starts;
ALTER TABLE worker_starts_new RENAME TO worker_starts;
CREATE INDEX IF NOT EXISTS worker_starts_legacy_ref ON worker_starts(legacy_ref) WHERE legacy_ref <> '';
`, `
CREATE TABLE IF NOT EXISTS project_mergers (
 project TEXT NOT NULL, node_id TEXT NOT NULL, session_id TEXT NOT NULL UNIQUE,
 PRIMARY KEY(project,node_id)
);
CREATE TRIGGER IF NOT EXISTS remove_project_merger AFTER DELETE ON sessions BEGIN
 DELETE FROM project_mergers WHERE session_id=OLD.id;
END;
`, mergerLifecycleMigration(), `
-- Auto-wake throttling survives gateway restarts.
CREATE TABLE IF NOT EXISTS retired_session_wakes (session_id TEXT PRIMARY KEY, attempted_at INTEGER NOT NULL);
`, `
-- Repo and agent records, preserving legacy graph keys.
CREATE TABLE IF NOT EXISTS agents (id TEXT PRIMARY KEY, display_name TEXT NOT NULL, role TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, tombstone INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS repos (id TEXT PRIMARY KEY, display_name TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, owner_agent_id TEXT REFERENCES agents(id) ON DELETE SET NULL);
CREATE TEMP TABLE split_components (name TEXT PRIMARY KEY);
INSERT OR IGNORE INTO split_components SELECT name FROM graph_nodes WHERE kind='component';
INSERT OR IGNORE INTO split_components SELECT name FROM projects WHERE name<>'' AND name NOT IN (SELECT name FROM graph_nodes WHERE kind<>'component');
INSERT OR IGNORE INTO split_components SELECT repo_project FROM sessions WHERE repo_project<>'' AND repo_project NOT IN (SELECT name FROM graph_nodes WHERE kind<>'component');
INSERT OR IGNORE INTO split_components SELECT source FROM graph_edges WHERE source<>'' AND source<>'*' AND source NOT LIKE '/delivery/%' AND source NOT IN (SELECT name FROM graph_nodes WHERE kind<>'component');
INSERT OR IGNORE INTO split_components SELECT target FROM graph_edges WHERE target<>'' AND target NOT IN (SELECT name FROM graph_nodes WHERE kind<>'component');
INSERT OR IGNORE INTO agents SELECT name,name,'',0,0,0 FROM split_components;
INSERT OR IGNORE INTO agents SELECT name,name,'architect',created_at,created_at,0 FROM graph_nodes WHERE kind='architect';
INSERT OR IGNORE INTO repos SELECT name,name,0,0,name FROM split_components;
DROP TABLE split_components;
CREATE TRIGGER IF NOT EXISTS repo_agent_project_insert AFTER INSERT ON projects WHEN NEW.name<>'' AND NOT EXISTS (SELECT 1 FROM graph_nodes WHERE name=NEW.name AND kind<>'component') BEGIN
 INSERT INTO agents VALUES(NEW.name,NEW.name,'',NEW.added_at,NEW.added_at,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos VALUES(NEW.name,NEW.name,NEW.added_at,NEW.added_at,NEW.name) ON CONFLICT(id) DO NOTHING;
END;
CREATE TRIGGER IF NOT EXISTS repo_agent_project_update AFTER UPDATE OF name ON projects WHEN NEW.name<>'' AND NOT EXISTS (SELECT 1 FROM graph_nodes WHERE name=NEW.name AND kind<>'component') BEGIN
 INSERT INTO agents VALUES(NEW.name,NEW.name,'',NEW.checked_at,NEW.checked_at,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos VALUES(NEW.name,NEW.name,NEW.checked_at,NEW.checked_at,NEW.name) ON CONFLICT(id) DO NOTHING;
END;
CREATE TRIGGER IF NOT EXISTS repo_agent_session_insert AFTER INSERT ON sessions WHEN NEW.repo_project<>'' AND NOT EXISTS (SELECT 1 FROM graph_nodes WHERE name=NEW.repo_project AND kind<>'component') BEGIN
 INSERT INTO agents VALUES(NEW.repo_project,NEW.repo_project,'',NEW.created_at,NEW.created_at,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos VALUES(NEW.repo_project,NEW.repo_project,NEW.created_at,NEW.created_at,NEW.repo_project) ON CONFLICT(id) DO NOTHING;
END;
CREATE TRIGGER IF NOT EXISTS repo_agent_session_update AFTER UPDATE OF repo_project ON sessions WHEN NEW.repo_project<>'' AND NOT EXISTS (SELECT 1 FROM graph_nodes WHERE name=NEW.repo_project AND kind<>'component') BEGIN
 INSERT INTO agents VALUES(NEW.repo_project,NEW.repo_project,'',NEW.last_seen,NEW.last_seen,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos VALUES(NEW.repo_project,NEW.repo_project,NEW.last_seen,NEW.last_seen,NEW.repo_project) ON CONFLICT(id) DO NOTHING;
END;
CREATE TRIGGER IF NOT EXISTS repo_agent_architect_insert AFTER INSERT ON graph_nodes WHEN NEW.kind='architect' BEGIN
 INSERT INTO agents VALUES(NEW.name,NEW.name,'architect',NEW.created_at,NEW.created_at,0)
 ON CONFLICT(id) DO UPDATE SET role='architect', tombstone=0, updated_at=NEW.created_at;
END;
CREATE TRIGGER IF NOT EXISTS repo_agent_architect_delete AFTER DELETE ON graph_nodes WHEN OLD.kind='architect' BEGIN
 UPDATE repos SET owner_agent_id=NULL WHERE owner_agent_id=OLD.name;
 UPDATE agents SET tombstone=1, updated_at=CAST(strftime('%s','now') AS INTEGER)*1000000000 WHERE id=OLD.name;
END;
`, `
-- Agent charters are separate from the existing repo charter.
CREATE TABLE IF NOT EXISTS agent_charter (agent_id TEXT NOT NULL REFERENCES agents(id), attr TEXT NOT NULL, value TEXT NOT NULL, updated_at INTEGER NOT NULL, updated_by TEXT NOT NULL, PRIMARY KEY(agent_id, attr));
CREATE TABLE IF NOT EXISTS agent_charter_history (id INTEGER PRIMARY KEY AUTOINCREMENT, agent_id TEXT NOT NULL REFERENCES agents(id), attr TEXT NOT NULL, old_value TEXT NOT NULL, new_value TEXT NOT NULL, at INTEGER NOT NULL, actor TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS agent_charter_history_agent ON agent_charter_history(agent_id, id);
`, edgeKindsMigration(), `
ALTER TABLE inbox_items ADD COLUMN options TEXT NOT NULL DEFAULT '[]';
ALTER TABLE inbox_items ADD COLUMN asker_name TEXT NOT NULL DEFAULT '';
ALTER TABLE inbox_items ADD COLUMN change_title TEXT NOT NULL DEFAULT '';
`, `
-- Explicit session and run identity, retaining project strings as adapters.
ALTER TABLE sessions ADD COLUMN agent_id TEXT REFERENCES agents(id);
ALTER TABLE sessions ADD COLUMN repo_id TEXT REFERENCES repos(id);
ALTER TABLE worker_runs ADD COLUMN caller_agent_id TEXT REFERENCES agents(id);
ALTER TABLE worker_starts ADD COLUMN caller_agent_id TEXT REFERENCES agents(id);
INSERT OR IGNORE INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES('cto','CTO','cto',0,0,0);
UPDATE sessions SET repo_id=(SELECT id FROM repos WHERE id=COALESCE(NULLIF(sessions.repo_project,''),(SELECT project FROM profiles WHERE session_id=sessions.id))) WHERE harness NOT IN ('cto','owner');
UPDATE sessions SET agent_id=CASE WHEN harness='cto' THEN 'cto' WHEN harness='owner' OR worker<>'' THEN NULL ELSE (SELECT owner_agent_id FROM repos WHERE id=sessions.repo_id) END;
UPDATE worker_runs SET caller_agent_id=(SELECT agent_id FROM sessions WHERE id=worker_runs.caller_session_id);
UPDATE worker_starts SET caller_agent_id=(SELECT agent_id FROM sessions WHERE id=worker_starts.owner);
CREATE TRIGGER IF NOT EXISTS session_identity_repo_update AFTER UPDATE OF repo_project ON sessions BEGIN
 UPDATE sessions SET repo_id=(SELECT id FROM repos WHERE id=NEW.repo_project) WHERE id=NEW.id AND NEW.harness NOT IN ('cto','owner');
END;
`, `
-- Owner changes to a registered session's agent.
CREATE TABLE IF NOT EXISTS session_agent_history (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 session_id TEXT NOT NULL,
 old_agent_id TEXT NOT NULL,
 new_agent_id TEXT NOT NULL,
 actor TEXT NOT NULL,
 at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS session_agent_history_session ON session_agent_history(session_id,id);
`, `
SELECT 1 /* delivery step tasks urgent column */;
DROP TRIGGER IF EXISTS merger_removed;
DROP TRIGGER IF EXISTS merger_replaced;
CREATE TRIGGER merger_removed AFTER DELETE ON project_mergers BEGIN
UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched','failed') AND id IN (
SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t
USING(change_id,revision,step,for_step,after_merge)
JOIN delivery_changes c ON c.id=t.change_id
WHERE t.owner_node='/merger' AND t.completed=0 AND c.project=OLD.project);
DELETE FROM delivery_step_messages WHERE message_id IN (
SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t
USING(change_id,revision,step,for_step,after_merge)
JOIN delivery_changes c ON c.id=t.change_id
WHERE t.owner_node='/merger' AND t.completed=0 AND c.project=OLD.project);
UPDATE delivery_step_tasks SET owner_node='/caller',claimed_by='',urgent=1
WHERE owner_node='/merger' AND completed=0 AND change_id IN
(SELECT c.id FROM delivery_changes c WHERE c.project=OLD.project);
INSERT OR IGNORE INTO graph_edges(source,target,kind,mode,rule,source_kind,target_kind)
SELECT '/delivery/caller',target,kind,mode,rule,'special','repo' FROM graph_edges
WHERE source='/delivery/merger' AND target=OLD.project AND kind<>'accepts';
DELETE FROM graph_edges WHERE source='/delivery/merger' AND target=OLD.project AND kind<>'accepts';
DELETE FROM project_mergers WHERE project=OLD.project;
UPDATE setting_revision SET revision=revision+1 WHERE id=1;
END;
CREATE TRIGGER merger_replaced AFTER UPDATE OF session_id ON project_mergers
WHEN OLD.session_id<>NEW.session_id BEGIN
UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched','failed') AND id IN (
SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t
USING(change_id,revision,step,for_step,after_merge)
JOIN delivery_changes c ON c.id=t.change_id
WHERE t.owner_node='/merger' AND t.completed=0 AND c.project=OLD.project AND c.machine=OLD.node_id);
DELETE FROM delivery_step_messages WHERE message_id IN (
SELECT d.message_id FROM delivery_step_messages d JOIN delivery_step_tasks t
USING(change_id,revision,step,for_step,after_merge)
JOIN delivery_changes c ON c.id=t.change_id
WHERE t.owner_node='/merger' AND t.completed=0 AND c.project=OLD.project AND c.machine=OLD.node_id);
UPDATE delivery_step_tasks SET owner_node='/merger',claimed_by='',urgent=1
WHERE owner_node='/merger' AND completed=0 AND change_id IN
(SELECT c.id FROM delivery_changes c WHERE c.project=OLD.project AND c.machine=OLD.node_id);
END;
`, `
-- One-time repair: older versions left a finished worker's session offline
-- with the busy status of its last turn. worker_run_active is 0 once a run's
-- end was recorded, and a resumed worker sets it back to 1. This migration
-- marks those finished worker sessions ended.
UPDATE sessions SET status = 'ended'
WHERE online = 0 AND worker <> '' AND worker_run_active = 0 AND status <> 'ended';
`, `
CREATE TABLE IF NOT EXISTS node_health (node_id TEXT PRIMARY KEY, report TEXT NOT NULL, reported_at INTEGER NOT NULL);
`, `
-- Typed Graph capability reports and explicit backup-gated cutover.
CREATE TABLE IF NOT EXISTS node_graph_refs (node_id TEXT PRIMARY KEY REFERENCES nodes(id), version TEXT NOT NULL, supported INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS graph_ref_gate (id INTEGER PRIMARY KEY CHECK(id=1), enabled INTEGER NOT NULL DEFAULT 0, backup TEXT NOT NULL DEFAULT '');
INSERT OR IGNORE INTO graph_ref_gate(id) VALUES(1);
INSERT INTO graph_history(type,node,attr,old_value,new_value,at,actor,target_kind)
 SELECT 'agent_charter',a.agent_id,a.attr,a.old_value,a.new_value,a.at,a.actor,'agent' FROM agent_charter_history a
 WHERE NOT EXISTS (SELECT 1 FROM graph_history h WHERE h.type='agent_charter' AND h.node=a.agent_id AND h.attr=a.attr AND h.old_value=a.old_value AND h.new_value=a.new_value AND h.at=a.at AND h.actor=a.actor) ORDER BY a.id;

`, `
-- Typed lifecycle and prospective display-name uniqueness. Existing collisions remain visible.
CREATE TABLE IF NOT EXISTS archived_repos (repo_id TEXT PRIMARY KEY REFERENCES repos(id));
-- Automatic discovery may encounter existing display names. Only explicit
-- lifecycle writes enforce uniqueness; remove any old trigger on replay.
DROP TRIGGER IF EXISTS repos_unique_name_insert;
DROP TRIGGER IF EXISTS repos_unique_name_update;
DROP TRIGGER IF EXISTS agents_unique_name_insert;
DROP TRIGGER IF EXISTS agents_unique_name_update;
-- Discovery must not assign a tombstoned Agent, including on an ignored
-- insert of an existing Repo. Keep that Repo unowned until the owner chooses.
DROP TRIGGER IF EXISTS repo_agent_project_insert;
DROP TRIGGER IF EXISTS repo_agent_project_update;
DROP TRIGGER IF EXISTS repo_agent_session_insert;
DROP TRIGGER IF EXISTS repo_agent_session_update;
CREATE TRIGGER repo_agent_project_insert AFTER INSERT ON projects WHEN NEW.name<>'' AND NOT EXISTS (SELECT 1 FROM graph_nodes WHERE name=NEW.name AND kind<>'component') BEGIN
 INSERT INTO agents VALUES(NEW.name,NEW.name,'',NEW.added_at,NEW.added_at,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos VALUES(NEW.name,NEW.name,NEW.added_at,NEW.added_at,CASE WHEN EXISTS (SELECT 1 FROM agents WHERE id=NEW.name AND tombstone=0) THEN NEW.name ELSE NULL END) ON CONFLICT(id) DO NOTHING;
END;
CREATE TRIGGER repo_agent_project_update AFTER UPDATE OF name ON projects WHEN NEW.name<>'' AND NOT EXISTS (SELECT 1 FROM graph_nodes WHERE name=NEW.name AND kind<>'component') BEGIN
 INSERT INTO agents VALUES(NEW.name,NEW.name,'',NEW.checked_at,NEW.checked_at,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos VALUES(NEW.name,NEW.name,NEW.checked_at,NEW.checked_at,CASE WHEN EXISTS (SELECT 1 FROM agents WHERE id=NEW.name AND tombstone=0) THEN NEW.name ELSE NULL END) ON CONFLICT(id) DO NOTHING;
END;
CREATE TRIGGER repo_agent_session_insert AFTER INSERT ON sessions WHEN NEW.repo_project<>'' AND NOT EXISTS (SELECT 1 FROM graph_nodes WHERE name=NEW.repo_project AND kind<>'component') BEGIN
 INSERT INTO agents VALUES(NEW.repo_project,NEW.repo_project,'',NEW.created_at,NEW.created_at,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos VALUES(NEW.repo_project,NEW.repo_project,NEW.created_at,NEW.created_at,CASE WHEN EXISTS (SELECT 1 FROM agents WHERE id=NEW.repo_project AND tombstone=0) THEN NEW.repo_project ELSE NULL END) ON CONFLICT(id) DO NOTHING;
END;
CREATE TRIGGER repo_agent_session_update AFTER UPDATE OF repo_project ON sessions WHEN NEW.repo_project<>'' AND NOT EXISTS (SELECT 1 FROM graph_nodes WHERE name=NEW.repo_project AND kind<>'component') BEGIN
 INSERT INTO agents VALUES(NEW.repo_project,NEW.repo_project,'',NEW.last_seen,NEW.last_seen,0) ON CONFLICT(id) DO NOTHING;
 INSERT INTO repos VALUES(NEW.repo_project,NEW.repo_project,NEW.last_seen,NEW.last_seen,CASE WHEN EXISTS (SELECT 1 FROM agents WHERE id=NEW.repo_project AND tombstone=0) THEN NEW.repo_project ELSE NULL END) ON CONFLICT(id) DO NOTHING;
END;
CREATE TRIGGER IF NOT EXISTS repo_live_owner_insert BEFORE INSERT ON repos
 WHEN NEW.owner_agent_id IS NOT NULL AND EXISTS (SELECT 1 FROM agents WHERE id=NEW.owner_agent_id AND tombstone<>0)
 BEGIN SELECT RAISE(ABORT, 'tombstoned agent cannot own a repo'); END;
CREATE TRIGGER IF NOT EXISTS repo_live_owner_update BEFORE UPDATE OF owner_agent_id ON repos
 WHEN NEW.owner_agent_id IS NOT NULL AND EXISTS (SELECT 1 FROM agents WHERE id=NEW.owner_agent_id AND tombstone<>0)
 BEGIN SELECT RAISE(ABORT, 'tombstoned agent cannot own a repo'); END;
CREATE TRIGGER IF NOT EXISTS agent_owned_tombstone BEFORE UPDATE OF tombstone ON agents
 WHEN NEW.tombstone<>0 AND EXISTS (SELECT 1 FROM repos WHERE owner_agent_id=NEW.id)
 BEGIN SELECT RAISE(ABORT, 'agent owns a repo'); END;
CREATE TRIGGER IF NOT EXISTS session_lifecycle_insert BEFORE INSERT ON sessions
 WHEN NEW.online=1 AND (SELECT enabled FROM graph_ref_gate WHERE id=1)=1 AND
 (EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=NEW.repo_id) OR EXISTS(SELECT 1 FROM agents WHERE id=NEW.agent_id AND tombstone<>0))
 BEGIN SELECT RAISE(ABORT, 'session identity is inactive'); END;
CREATE TRIGGER IF NOT EXISTS session_lifecycle_update BEFORE UPDATE OF online,repo_id,agent_id ON sessions
 WHEN NEW.online=1 AND (SELECT enabled FROM graph_ref_gate WHERE id=1)=1 AND
 (EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=NEW.repo_id) OR EXISTS(SELECT 1 FROM agents WHERE id=NEW.agent_id AND tombstone<>0))
 BEGIN SELECT RAISE(ABORT, 'session identity is inactive'); END;
`, `
-- Validate active session identities in their write transactions. Removing
-- these triggers also permits old-schema fixtures to drop identity columns.
DROP TRIGGER IF EXISTS session_lifecycle_insert;
DROP TRIGGER IF EXISTS session_lifecycle_update;
`, `
-- 123: owner-authorized Agent selection before first session registration.
CREATE TABLE IF NOT EXISTS session_agent_starts (
 node_id TEXT NOT NULL REFERENCES nodes(id),
 harness TEXT NOT NULL,
 native_id TEXT NOT NULL,
 agent_id TEXT NOT NULL REFERENCES agents(id),
 ref TEXT NOT NULL DEFAULT '',
 cwd TEXT NOT NULL DEFAULT '',
 existing TEXT NOT NULL DEFAULT '[]',
 expires_at INTEGER NOT NULL DEFAULT 0,
 owner_session TEXT NOT NULL DEFAULT '',
 project TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(node_id,harness,native_id)
);
CREATE TABLE IF NOT EXISTS session_agent_start_failures (
 node_id TEXT NOT NULL, ref TEXT NOT NULL, harness TEXT NOT NULL,
 native_id TEXT NOT NULL, owner_session TEXT NOT NULL, project TEXT NOT NULL,
 item_id TEXT NOT NULL, reason TEXT NOT NULL,
 PRIMARY KEY(node_id,ref)
);
`, `
-- 124: Immutable settings target identities, keyed by typed kind and storage ID.
-- Seeds existing network, repo and agent records; a rerun keeps current rows.
CREATE TABLE IF NOT EXISTS settings_target_identity (
 kind TEXT NOT NULL CHECK(kind IN ('network','repo','agent')),
 id TEXT NOT NULL,
 token TEXT NOT NULL,
 PRIMARY KEY(kind,id));
INSERT OR IGNORE INTO settings_target_identity(kind,id,token) VALUES('network','network',lower(hex(randomblob(16))));
INSERT OR IGNORE INTO settings_target_identity(kind,id,token) SELECT 'repo',id,lower(hex(randomblob(16))) FROM repos;
INSERT OR IGNORE INTO settings_target_identity(kind,id,token) SELECT 'agent',id,lower(hex(randomblob(16))) FROM agents;
CREATE TRIGGER IF NOT EXISTS settings_target_identity_repo_delete AFTER DELETE ON repos BEGIN
 DELETE FROM settings_target_identity WHERE kind='repo' AND id=OLD.id;
END;
CREATE TRIGGER IF NOT EXISTS settings_target_identity_agent_delete AFTER DELETE ON agents BEGIN
 DELETE FROM settings_target_identity WHERE kind='agent' AND id=OLD.id;
END;
CREATE TRIGGER IF NOT EXISTS settings_target_identity_agent_tombstone AFTER UPDATE OF tombstone ON agents WHEN NEW.tombstone<>0 BEGIN
 DELETE FROM settings_target_identity WHERE kind='agent' AND id=NEW.id;
END;
`, `
-- 125: Worker run generations fence delayed end reports after a resume.
ALTER TABLE sessions ADD COLUMN worker_run_generation INTEGER NOT NULL DEFAULT 0;
`, `
-- Team records and typed membership. No inference or settings inheritance.
CREATE TABLE IF NOT EXISTS teams (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 purpose TEXT NOT NULL DEFAULT '',
 owner_agent_id TEXT REFERENCES agents(id),
 active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
-- Separate typed keys avoid the legacy graph_edges primary key, which cannot
-- distinguish an Agent and Repo with the same ID joining the same Team.
CREATE TABLE IF NOT EXISTS team_members (
 member_kind TEXT NOT NULL CHECK(member_kind IN ('agent','repo')),
 member_id TEXT NOT NULL,
 team_id TEXT NOT NULL REFERENCES teams(id),
 PRIMARY KEY(member_kind,member_id)
);
CREATE INDEX IF NOT EXISTS team_members_team ON team_members(team_id);
CREATE TABLE IF NOT EXISTS team_history (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 action TEXT NOT NULL,
 team_id TEXT NOT NULL,
 old_team_id TEXT NOT NULL DEFAULT '',
 member_kind TEXT NOT NULL DEFAULT '',
 member_id TEXT NOT NULL DEFAULT '',
 old_value TEXT NOT NULL,
 new_value TEXT NOT NULL,
 at INTEGER NOT NULL,
 actor TEXT NOT NULL
);
CREATE TRIGGER IF NOT EXISTS team_owner_delete BEFORE DELETE ON agents
 WHEN EXISTS(SELECT 1 FROM teams WHERE owner_agent_id=OLD.id)
 BEGIN SELECT RAISE(ABORT,'clear or transfer Team ownership first'); END;
CREATE TRIGGER IF NOT EXISTS team_owner_tombstone BEFORE UPDATE OF tombstone ON agents
 WHEN NEW.tombstone<>0 AND EXISTS(SELECT 1 FROM teams WHERE owner_agent_id=OLD.id)
 BEGIN SELECT RAISE(ABORT,'clear or transfer Team ownership first'); END;

`, `
-- worker early quit evidence (columns added replay-safely below).
SELECT 1;
`, `
-- Session harness activity and unexpected loss.
ALTER TABLE sessions ADD COLUMN harness_activity INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN harness_lost INTEGER NOT NULL DEFAULT 0;
UPDATE sessions SET harness_activity=last_seen WHERE status='busy';

`, `
SELECT 1 /* session archive column */;
`, `SELECT 1 /* session owner use columns */;`, `
-- Automatic typed Graph cutover: remember when this gateway version first started.
CREATE TABLE IF NOT EXISTS graph_cutover_state (
 id INTEGER PRIMARY KEY CHECK(id=1),
 gateway_version TEXT NOT NULL DEFAULT '',
 upgraded_at INTEGER NOT NULL DEFAULT 0);
INSERT OR IGNORE INTO graph_cutover_state(id) VALUES(1);
`, `
-- Repo lead override. A missing row uses the repo owner.
CREATE TABLE IF NOT EXISTS repo_leads (repo_id TEXT PRIMARY KEY REFERENCES repos(id), agent_id TEXT REFERENCES agents(id));
CREATE TRIGGER IF NOT EXISTS repo_lead_delete BEFORE DELETE ON agents
 WHEN EXISTS(SELECT 1 FROM repo_leads WHERE agent_id=OLD.id)
 BEGIN SELECT RAISE(ABORT,'clear or transfer Repo lead first'); END;
CREATE TRIGGER IF NOT EXISTS repo_lead_tombstone BEFORE UPDATE OF tombstone ON agents
 WHEN NEW.tombstone<>0 AND EXISTS(SELECT 1 FROM repo_leads WHERE agent_id=OLD.id)
 BEGIN SELECT RAISE(ABORT,'clear or transfer Repo lead first'); END;
`, `
-- Durable places for bounded Repo session starts.
CREATE TABLE IF NOT EXISTS repo_start_reservations (node_id TEXT NOT NULL,ref TEXT NOT NULL,repo_id TEXT NOT NULL,harness TEXT NOT NULL,native_id TEXT NOT NULL DEFAULT '',expires_at INTEGER NOT NULL,PRIMARY KEY(node_id,ref));
`, `SELECT 1 /* session close column */;`, `SELECT 1 /* session idle compaction column */;`, `
ALTER TABLE repo_leads ADD COLUMN lead_session TEXT;
ALTER TABLE teams ADD COLUMN lead_session TEXT;
`, `SELECT 1 /* idle check task hash column */;`, `SELECT 1 /* session provider limit columns */;`, `
-- A version number per table that readers keep a parsed copy of. SQLite bumps
-- it on every write to the table, whoever makes it, so a reader reloads its
-- copy exactly when the table changed and never on a timer.
CREATE TABLE IF NOT EXISTS table_versions (name TEXT PRIMARY KEY, n INTEGER NOT NULL DEFAULT 0);
INSERT OR IGNORE INTO table_versions(name, n) VALUES('worker_runs', 0);
CREATE TRIGGER IF NOT EXISTS worker_runs_version_insert AFTER INSERT ON worker_runs
 BEGIN UPDATE table_versions SET n = n + 1 WHERE name = 'worker_runs'; END;
CREATE TRIGGER IF NOT EXISTS worker_runs_version_update AFTER UPDATE ON worker_runs
 BEGIN UPDATE table_versions SET n = n + 1 WHERE name = 'worker_runs'; END;
CREATE TRIGGER IF NOT EXISTS worker_runs_version_delete AFTER DELETE ON worker_runs
 BEGIN UPDATE table_versions SET n = n + 1 WHERE name = 'worker_runs'; END;
`, `SELECT 1 /* session worker retained column */;`, `
-- Charter levels and enum cutover. All statements are safe to replay.
CREATE TABLE IF NOT EXISTS charter_values (
 level TEXT NOT NULL CHECK(level IN ('global','team')),
 scope TEXT NOT NULL, attr TEXT NOT NULL, value TEXT NOT NULL,
 updated_at INTEGER NOT NULL, updated_by TEXT NOT NULL,
 PRIMARY KEY(level,scope,attr), CHECK(level<>'global' OR scope=''));

INSERT INTO graph_charter(node,attr,value,updated_at,updated_by)
 SELECT node,'maturity_note',substr(value,instr(value,' - ')+3),updated_at,updated_by
 FROM graph_charter WHERE attr='maturity' AND instr(value,' - ')>0
 ON CONFLICT(node,attr) DO NOTHING;
UPDATE graph_charter SET value=CASE lower(substr(trim(value),1,instr(trim(value)||' ',' ')-1))
 WHEN 'experimental' THEN 'experimental' WHEN 'early' THEN 'early'
 WHEN 'growing' THEN 'growing' WHEN 'mature' THEN 'mature' ELSE 'early' END
 WHERE attr='maturity' AND value<>'';
INSERT INTO graph_charter(node,attr,value,updated_at,updated_by)
 SELECT node,'rules',group_concat(label || char(10) || value,char(10)||char(10)),max(updated_at),'migration'
 FROM (SELECT *,CASE attr WHEN 'consumers' THEN 'Consumers:' WHEN 'needs' THEN 'Needs:' ELSE 'Notes:' END AS label
 FROM graph_charter WHERE attr IN ('consumers','needs','notes') AND value<>'' ORDER BY node,attr)
 GROUP BY node
 ON CONFLICT(node,attr) DO UPDATE SET value=CASE WHEN graph_charter.value='' THEN excluded.value ELSE graph_charter.value||char(10)||char(10)||excluded.value END;
DELETE FROM graph_charter WHERE attr IN ('consumers','needs','notes');

INSERT INTO agent_charter(agent_id,attr,value,updated_at,updated_by)
 SELECT agent_id,'maturity_note',substr(value,instr(value,' - ')+3),updated_at,updated_by
 FROM agent_charter WHERE attr='maturity' AND instr(value,' - ')>0
 ON CONFLICT(agent_id,attr) DO NOTHING;
UPDATE agent_charter SET value=CASE lower(substr(trim(value),1,instr(trim(value)||' ',' ')-1))
 WHEN 'experimental' THEN 'experimental' WHEN 'early' THEN 'early'
 WHEN 'growing' THEN 'growing' WHEN 'mature' THEN 'mature' ELSE 'early' END
 WHERE attr='maturity' AND value<>'';
INSERT INTO agent_charter(agent_id,attr,value,updated_at,updated_by)
 SELECT agent_id,'rules',group_concat(label || char(10) || value,char(10)||char(10)),max(updated_at),'migration'
 FROM (SELECT *,CASE attr WHEN 'consumers' THEN 'Consumers:' WHEN 'needs' THEN 'Needs:' ELSE 'Notes:' END AS label
 FROM agent_charter WHERE attr IN ('consumers','needs','notes') AND value<>'' ORDER BY agent_id,attr)
 GROUP BY agent_id
 ON CONFLICT(agent_id,attr) DO UPDATE SET value=CASE WHEN agent_charter.value='' THEN excluded.value ELSE agent_charter.value||char(10)||char(10)||excluded.value END;
DELETE FROM agent_charter WHERE attr IN ('consumers','needs','notes');
`, `
-- Message hand-off: the lead who passed this message on. Empty for others.
ALTER TABLE messages ADD COLUMN handed_off_by TEXT NOT NULL DEFAULT '';
`, `
-- Charter drafts (decision 0088). A session proposes a charter value read from
-- AGENTS.md; it stays pending until the owner or CTO decides. One ask row per
-- repo, agent or team keeps the request to once per version of AGENTS.md. All
-- statements are safe to replay.
CREATE TABLE IF NOT EXISTS charter_drafts (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 level TEXT NOT NULL CHECK(level IN ('repo','agent','team')),
 scope TEXT NOT NULL, attr TEXT NOT NULL, value TEXT NOT NULL,
 source TEXT NOT NULL, source_hash TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','accepted','rejected','superseded')),
 drafted_by TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 decided_at INTEGER NOT NULL DEFAULT 0, decided_by TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS charter_drafts_key ON charter_drafts(level,scope,attr,status);
CREATE TABLE IF NOT EXISTS charter_draft_asks (
 level TEXT NOT NULL, scope TEXT NOT NULL, source_hash TEXT NOT NULL,
 asked_at INTEGER NOT NULL, session_id TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(level,scope));
`, `
-- Team authority mode and held back (decision 0089). A repo has no column:
-- the repos table has positional inserts in live triggers, so its held back
-- text lives in its own table. is_order records the sender's checked order flag.
ALTER TABLE teams ADD COLUMN authority_mode TEXT NOT NULL DEFAULT 'charter_first';
ALTER TABLE teams ADD COLUMN held_back TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN is_order INTEGER NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS repo_held_back (repo_id TEXT PRIMARY KEY REFERENCES repos(id), held_back TEXT NOT NULL DEFAULT '');
`, `
-- Remove governs (decision 0089). The owner had no governs edge in use. Drop any
-- row left, and the setting that chose which governs modes could approve a merge.
DELETE FROM graph_edges WHERE kind='governs';
DELETE FROM setting_values WHERE key='delivery.delegated_approver_modes';
`, `
-- Asks and the Desk (decision 0090). inbox_items is the one store of asks:
-- everything that waits on a person. These columns give an ask its source key,
-- what it is about, who must act now, and a line on why it is the owner's. The
-- state of an ask is read from status: open, answered or closed with the
-- reason kept in answer. desk_log is the one event log; it holds what used to
-- be notices (CTO applied or sent, app approved) and what automation did.
ALTER TABLE inbox_items ADD COLUMN from_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE inbox_items ADD COLUMN source_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE inbox_items ADD COLUMN about_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE inbox_items ADD COLUMN about_id TEXT NOT NULL DEFAULT '';
ALTER TABLE inbox_items ADD COLUMN next_who TEXT NOT NULL DEFAULT 'owner';
ALTER TABLE inbox_items ADD COLUMN why_you TEXT NOT NULL DEFAULT '';
ALTER TABLE inbox_items ADD COLUMN detail TEXT NOT NULL DEFAULT '';
`, `
-- Standing orders: the owner's durable instructions to the CTO. They live at
-- the gateway so they survive CTO restarts and machine changes.
CREATE TABLE IF NOT EXISTS standing_orders (
 id TEXT PRIMARY KEY, text TEXT NOT NULL, saved_at INTEGER NOT NULL,
 source TEXT NOT NULL DEFAULT 'owner');
`, botAgentMigration, `
-- Notices leave the queue for good (decision 0090, part A2). What the CTO
-- applied or sent and a new app are lines of desk_log, and the Desk shows them
-- there. The old Inbox rows of these notices are closed, not deleted. The
-- statement is safe to replay.
UPDATE inbox_items SET status='closed', answer='moved_to_log', answered_at=created_at
 WHERE kind='check' AND status='open' AND from_kind IN ('cto','apps');
`, `
-- Standing loops (decision 0094): one row per node that has a loop. The clock,
-- the day's counts, the no-progress streak and the progress markers of the
-- last tick. Task text is never stored; last_result is one cleaned announce
-- line.
CREATE TABLE IF NOT EXISTS agent_loops (
 node TEXT PRIMARY KEY,
 started_at INTEGER NOT NULL DEFAULT 0,
 last_at INTEGER NOT NULL DEFAULT 0,
 sent_at INTEGER NOT NULL DEFAULT 0,
 session_id TEXT NOT NULL DEFAULT '',
 day TEXT NOT NULL DEFAULT '',
 day_ticks INTEGER NOT NULL DEFAULT 0,
 day_progress INTEGER NOT NULL DEFAULT 0,
 streak INTEGER NOT NULL DEFAULT 0,
 last_open INTEGER NOT NULL DEFAULT 0,
 last_msgs INTEGER NOT NULL DEFAULT 0,
 announced INTEGER NOT NULL DEFAULT 0,
 awaiting INTEGER NOT NULL DEFAULT 0,
 counted INTEGER NOT NULL DEFAULT 0,
 paused INTEGER NOT NULL DEFAULT 0,
 last_result TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS agent_loops_session ON agent_loops(session_id);
`, `
-- Quota blocks per window. A block names the window of a provider that a
-- worker run reported out of quota (window_id), or every model of the provider
-- when window_id is empty. The old table is dropped once: its rows name no
-- window, so they would block every agy model until their reset.
DROP TABLE IF EXISTS quota_blocks;
CREATE TABLE IF NOT EXISTS quota_blocks (
 node_id TEXT NOT NULL, provider TEXT NOT NULL, window_id TEXT NOT NULL DEFAULT '',
 window_label TEXT NOT NULL DEFAULT '', resets_at INTEGER NOT NULL,
 detail TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL,
 PRIMARY KEY (node_id, provider, window_id));
`, `
-- A wake that the machine refused for a reason. A gateway restart does not
-- retry it; only a new message asks again (see wake_failure.go).
CREATE TABLE IF NOT EXISTS session_wake_failures (
 session_id TEXT PRIMARY KEY, failed_at INTEGER NOT NULL, reason TEXT NOT NULL);
`}

// asksMigrationRest is the part of the asks migration that is safe to replay
// after its columns exist: the log table, the indexes and the backfill of old
// open items. It does not delete any data.
const asksMigrationRest = `
CREATE TABLE IF NOT EXISTS desk_log (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 at INTEGER NOT NULL,
 source TEXT NOT NULL,
 about_kind TEXT NOT NULL DEFAULT '',
 about_id TEXT NOT NULL DEFAULT '',
 text TEXT NOT NULL,
 detail TEXT NOT NULL DEFAULT '',
 options TEXT NOT NULL DEFAULT '[]');
CREATE INDEX IF NOT EXISTS inbox_items_status ON inbox_items(status, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS inbox_items_open_source ON inbox_items(source_ref) WHERE source_ref<>'' AND status='open';
UPDATE inbox_items SET from_kind='cto' WHERE from_kind='' AND node='cto' AND kind='check';
UPDATE inbox_items SET from_kind='apps' WHERE from_kind='' AND node='apps' AND kind='check';
UPDATE inbox_items SET from_kind='disk' WHERE from_kind='' AND kind='check' AND text LIKE 'Disk space low on %';
UPDATE inbox_items SET about_kind='change', about_id=(SELECT d.change_id FROM delivery_step_inbox d WHERE d.item_id=inbox_items.id LIMIT 1)
 WHERE about_kind='' AND id IN (SELECT item_id FROM delivery_step_inbox);
UPDATE inbox_items SET about_kind='project', about_id=node
 WHERE about_kind='' AND node<>'' AND kind IN ('question','approval','feature_idea','session_stall');
INSERT INTO desk_log(at,source,about_kind,about_id,text,detail,options)
 SELECT i.created_at, i.from_kind, '', '', i.text, i.recommendation, i.options
 FROM inbox_items i WHERE i.kind='check' AND i.status='open' AND i.from_kind IN ('cto','apps')
 AND NOT EXISTS (SELECT 1 FROM desk_log l WHERE l.at=i.created_at AND l.source=i.from_kind AND l.text=i.text);
`

// stmtCacheSize is how many prepared statements the connection keeps. The
// gateway runs a few hundred different statements.
const stmtCacheSize = 1024

// OpenStore opens (or creates) the database at path and migrates it.
func OpenStore(path string) (*Store, error) {
	// The file is private from the start; SQLite gives -wal and -shm its mode.
	if err := dbfile.Create(path); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	// The connection keeps the prepared statements of its recent SQL texts, so
	// the gateway does not parse the same statements again on every call.
	connector, err := sqlitecache.New(dsn, stmtCacheSize)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	// One connection keeps writes serialized and avoids lock errors.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, connector: connector, path: path, now: time.Now, logf: func(string, ...any) {}, buckets: map[string]*bucket{}}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	// agentSelect looks up the stall item of a session for every session row.
	// Without this index each row scans all inbox items. It is created here
	// and not as a versioned migration, so an older release can still open
	// the file.
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS inbox_items_session ON inbox_items (session_id, message_id, created_at)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("inbox session index: %w", err)
	}
	// applyPending asks for replies from a recipient to a sender. Without this
	// index each request scans every message to that recipient. Created here
	// and not as a versioned migration, like the index above.
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS messages_pair ON messages (sender, recipient, type)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("messages pair index: %w", err)
	}
	if err := s.loadGraphRefs(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.resetStartingStarts(); err != nil {
		db.Close()
		return nil, fmt.Errorf("reset starting starts: %w", err)
	}
	if err := s.backfillRework(); err != nil {
		db.Close()
		return nil, fmt.Errorf("rework backfill: %w", err)
	}
	if err := s.backfillTaskRework(); err != nil {
		db.Close()
		return nil, fmt.Errorf("task rework backfill: %w", err)
	}
	if err := s.repairExternalDone(); err != nil {
		db.Close()
		return nil, fmt.Errorf("external merge repair: %w", err)
	}
	if err := s.backfillDeliveryTasks(); err != nil {
		db.Close()
		return nil, fmt.Errorf("delivery task backfill: %w", err)
	}
	if err := s.retireObsoleteNotices(); err != nil {
		db.Close()
		return nil, fmt.Errorf("retire obsolete notices: %w", err)
	}
	if err := s.reconcileAsks(); err != nil {
		db.Close()
		return nil, fmt.Errorf("reconcile asks: %w", err)
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
	// A database from a newer release may hold tables, columns and triggers
	// this binary does not know. Opening it would silently drop the rules the
	// newer code enforced, so refuse and let the owner restore its backup.
	if cur > len(migrations) {
		return fmt.Errorf("gateway.db schema version %d is newer than this agent-net (knows %d); upgrade agent-net or restore the backup taken before the upgrade", cur, len(migrations))
	}
	for i := cur; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if strings.Contains(migrations[i], "session close column") {
			var has int
			err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='closed_at'`).Scan(&has)
			if err == nil && has == 0 {
				_, err = tx.Exec(`ALTER TABLE sessions ADD COLUMN closed_at INTEGER NOT NULL DEFAULT 0`)
			}
			if err != nil {
				tx.Rollback()
				return err
			}
		}
		if strings.Contains(migrations[i], "Typed edge metadata") {
			for _, table := range []string{"graph_edges", "graph_history"} {
				for _, name := range []string{"source_kind", "target_kind"} {
					var has int
					if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, name).Scan(&has); err != nil {
						tx.Rollback()
						return err
					}
					if has == 0 {
						if _, err := tx.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + name + ` TEXT NOT NULL DEFAULT ''`); err != nil {
							tx.Rollback()
							return err
						}
					}
				}
			}
		}
		if strings.Contains(migrations[i], "worker task marker and run state columns") {
			for _, col := range []struct{ table, name, spec string }{
				{"messages", "worker_task", "worker_task INTEGER"},
				{"sessions", "worker_run_active", "worker_run_active INTEGER NOT NULL DEFAULT 1"},
			} {
				var has int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, col.table, col.name).Scan(&has); err != nil {
					tx.Rollback()
					return err
				}
				if has == 0 {
					if _, err := tx.Exec(`ALTER TABLE ` + col.table + ` ADD COLUMN ` + col.spec); err != nil {
						tx.Rollback()
						return err
					}
				}
			}
		}
		if strings.Contains(migrations[i], "model auth failure column") {
			var has int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('model_failures') WHERE name='auth_at'`).Scan(&has); err != nil {
				tx.Rollback()
				return err
			}
			if has == 0 {
				if _, err := tx.Exec(`ALTER TABLE model_failures ADD COLUMN auth_at INTEGER NOT NULL DEFAULT 0`); err != nil {
					tx.Rollback()
					return err
				}
			}
		}
		// Add the public app columns only when absent. Old migration tests replay
		// later migrations on an already migrated database.
		if strings.Contains(migrations[i], "Token families and source limits") {
			for _, col := range []struct{ table, name, spec string }{
				{"app_tokens", "family_id", `family_id TEXT NOT NULL DEFAULT ''`},
				{"app_tokens", "used_at", `used_at INTEGER`},
				{"oauth_clients", "source", `source TEXT NOT NULL DEFAULT ''`},
				{"oauth_pending", "source", `source TEXT NOT NULL DEFAULT ''`},
			} {
				var has int
				if err := tx.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name=?`, col.table, col.name).Scan(&has); err != nil {
					tx.Rollback()
					return err
				}
				if has == 0 {
					if _, err := tx.Exec(`ALTER TABLE ` + col.table + ` ADD COLUMN ` + col.spec); err != nil {
						tx.Rollback()
						return err
					}
				}
			}
			if _, err := tx.Exec(`UPDATE app_tokens SET family_id=app_id WHERE family_id=''; CREATE INDEX IF NOT EXISTS app_tokens_family ON app_tokens(family_id); CREATE INDEX IF NOT EXISTS oauth_clients_source ON oauth_clients(source,created_at); CREATE INDEX IF NOT EXISTS oauth_pending_source ON oauth_pending(source,status,created_at);`); err != nil {
				tx.Rollback()
				return err
			}
		}
		if strings.Contains(migrations[i], "App token purpose, OAuth retry code") {
			for _, col := range []struct{ table, name, spec string }{
				{"app_tokens", "purpose", `purpose TEXT NOT NULL DEFAULT 'oauth' CHECK (purpose IN ('oauth','phone'))`},
				{"oauth_codes", "retry_code", `retry_code TEXT NOT NULL DEFAULT ''`},
			} {
				var has int
				if err := tx.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name=?`, col.table, col.name).Scan(&has); err != nil {
					tx.Rollback()
					return err
				}
				if has == 0 {
					if _, err := tx.Exec(`ALTER TABLE ` + col.table + ` ADD COLUMN ` + col.spec); err != nil {
						tx.Rollback()
						return err
					}
				}
			}
		}
		if strings.Contains(migrations[i], "machine token revocation columns") {
			for _, col := range []struct{ name, spec string }{
				{"removed", `INTEGER NOT NULL DEFAULT 0`},
				{"pending_token_hash", `TEXT NOT NULL DEFAULT ''`},
			} {
				var has int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('nodes') WHERE name=?`, col.name).Scan(&has); err != nil {
					tx.Rollback()
					return err
				}
				if has == 0 {
					if _, err := tx.Exec(`ALTER TABLE nodes ADD COLUMN ` + col.name + ` ` + col.spec); err != nil {
						tx.Rollback()
						return err
					}
				}
			}
		}
		if strings.Contains(migrations[i], "worker start tracking") {
			for _, col := range []struct{ name, spec string }{
				{"legacy_ref", `TEXT NOT NULL DEFAULT ''`},
				{"launch_state", `TEXT NOT NULL DEFAULT ''`},
			} {
				var has int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_starts') WHERE name=?`, col.name).Scan(&has); err != nil {
					tx.Rollback()
					return err
				}
				if has == 0 {
					if _, err := tx.Exec(`ALTER TABLE worker_starts ADD COLUMN ` + col.name + ` ` + col.spec); err != nil {
						tx.Rollback()
						return err
					}
				}
			}
		}
		if strings.Contains(migrations[i], "idle check task hash column") {
			var has int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('session_checks') WHERE name='check_task_hash'`).Scan(&has); err != nil {
				tx.Rollback()
				return err
			}
			if has == 0 {
				if _, err := tx.Exec(`ALTER TABLE session_checks ADD COLUMN check_task_hash TEXT NOT NULL DEFAULT ''`); err != nil {
					tx.Rollback()
					return err
				}
			}
		}
		if strings.Contains(migrations[i], "session provider limit columns") {
			for _, col := range []struct{ name, def string }{{"limited_until", "INTEGER NOT NULL DEFAULT 0"}, {"limited_reason", "TEXT NOT NULL DEFAULT ''"}} {
				var has int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name=?`, col.name).Scan(&has); err != nil {
					tx.Rollback()
					return err
				}
				if has == 0 {
					if _, err := tx.Exec(`ALTER TABLE sessions ADD COLUMN ` + col.name + ` ` + col.def); err != nil {
						tx.Rollback()
						return err
					}
				}
			}
		}
		if strings.Contains(migrations[i], "session worker retained column") {
			var has int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='worker_retained'`).Scan(&has); err != nil {
				tx.Rollback()
				return err
			}
			if has == 0 {
				if _, err := tx.Exec(`ALTER TABLE sessions ADD COLUMN worker_retained INTEGER NOT NULL DEFAULT 0`); err != nil {
					tx.Rollback()
					return err
				}
			}
		}
		if strings.Contains(migrations[i], "session idle compaction column") {
			var has int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='idle_compacted_at'`).Scan(&has); err != nil {
				tx.Rollback()
				return err
			}
			if has == 0 {
				if _, err := tx.Exec(`ALTER TABLE sessions ADD COLUMN idle_compacted_at INTEGER NOT NULL DEFAULT 0`); err != nil {
					tx.Rollback()
					return err
				}
			}
		}
		var result sql.Result
		if strings.Contains(migrations[i], "ALTER TABLE repo_leads ADD COLUMN lead_session") {
			for _, table := range []string{"repo_leads", "teams"} {
				var has int
				if err = tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name='lead_session'`, table).Scan(&has); err != nil {
					break
				}
				if has == 0 {
					if _, err = tx.Exec(`ALTER TABLE ` + table + ` ADD COLUMN lead_session TEXT`); err != nil {
						break
					}
				}
			}
			if err == nil {
				result, err = tx.Exec(`SELECT 1`)
			}
		} else if strings.Contains(migrations[i], "ALTER TABLE inbox_items ADD COLUMN options") {
			for _, col := range []struct{ name, spec string }{
				{"options", `TEXT NOT NULL DEFAULT '[]'`}, {"asker_name", `TEXT NOT NULL DEFAULT ''`}, {"change_title", `TEXT NOT NULL DEFAULT ''`},
			} {
				var has int
				if err = tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('inbox_items') WHERE name=?`, col.name).Scan(&has); err != nil {
					break
				}
				if has == 0 {
					if _, err = tx.Exec(`ALTER TABLE inbox_items ADD COLUMN ` + col.name + ` ` + col.spec); err != nil {
						break
					}
				}
			}
			if err == nil {
				result, err = tx.Exec(`SELECT 1`)
			}
		} else if strings.Contains(migrations[i], "Session harness activity and unexpected loss") {
			// Rewound databases may retain either column. Preserve existing
			// observations and initialize activity only when adding its column.
			addedActivity := false
			for _, col := range []struct{ name, spec string }{
				{"harness_activity", "INTEGER NOT NULL DEFAULT 0"},
				{"harness_lost", "INTEGER NOT NULL DEFAULT 0"},
			} {
				var has int
				if err = tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name=?`, col.name).Scan(&has); err != nil {
					break
				}
				if has == 0 {
					if _, err = tx.Exec(`ALTER TABLE sessions ADD COLUMN ` + col.name + ` ` + col.spec); err != nil {
						break
					}
					if col.name == "harness_activity" {
						addedActivity = true
					}
				}
			}
			if err == nil && addedActivity {
				_, err = tx.Exec(`UPDATE sessions SET harness_activity=last_seen WHERE status='busy'`)
			}
			if err == nil {
				result, err = tx.Exec(`SELECT 1`)
			}
		} else if strings.Contains(migrations[i], "Team authority mode and held back (decision 0089)") {
			for _, col := range []struct{ table, name, spec string }{
				{"teams", "authority_mode", `TEXT NOT NULL DEFAULT 'charter_first'`},
				{"teams", "held_back", `TEXT NOT NULL DEFAULT ''`},
				{"messages", "is_order", `INTEGER NOT NULL DEFAULT 0`},
			} {
				var has int
				if err = tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, col.table, col.name).Scan(&has); err != nil {
					break
				}
				if has == 0 {
					if _, err = tx.Exec(`ALTER TABLE ` + col.table + ` ADD COLUMN ` + col.name + ` ` + col.spec); err != nil {
						break
					}
				}
			}
			if err == nil {
				result, err = tx.Exec(`CREATE TABLE IF NOT EXISTS repo_held_back (repo_id TEXT PRIMARY KEY REFERENCES repos(id), held_back TEXT NOT NULL DEFAULT '')`)
			}
		} else if strings.Contains(migrations[i], "Asks and the Desk (decision 0090)") {
			for _, col := range []struct{ name, spec string }{
				{"from_kind", `TEXT NOT NULL DEFAULT ''`}, {"source_ref", `TEXT NOT NULL DEFAULT ''`},
				{"about_kind", `TEXT NOT NULL DEFAULT ''`}, {"about_id", `TEXT NOT NULL DEFAULT ''`},
				{"next_who", `TEXT NOT NULL DEFAULT 'owner'`}, {"why_you", `TEXT NOT NULL DEFAULT ''`},
				{"detail", `TEXT NOT NULL DEFAULT ''`},
			} {
				var has int
				if err = tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('inbox_items') WHERE name=?`, col.name).Scan(&has); err != nil {
					break
				}
				if has == 0 {
					if _, err = tx.Exec(`ALTER TABLE inbox_items ADD COLUMN ` + col.name + ` ` + col.spec); err != nil {
						break
					}
				}
			}
			if err == nil {
				result, err = tx.Exec(asksMigrationRest)
			}
		} else if strings.Contains(migrations[i], "Message hand-off: the lead who passed") {
			var has int
			err = tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name='handed_off_by'`).Scan(&has)
			if err == nil {
				if has == 0 {
					result, err = tx.Exec(migrations[i])
				} else {
					result, err = tx.Exec(`SELECT 1`)
				}
			}
		} else if strings.Contains(migrations[i], "Worker run generations fence") {
			var has int
			err = tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='worker_run_generation'`).Scan(&has)
			if err == nil {
				if has == 0 {
					result, err = tx.Exec(migrations[i])
				} else {
					result, err = tx.Exec(`SELECT 1`)
				}
			}
		} else {
			result, err = tx.Exec(migrations[i])
		}
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		// SQLite has no ADD COLUMN IF NOT EXISTS, and a test may replay a
		// migration on a database that already has these columns.
		if strings.Contains(migrations[i], "OAuth pending browser hash") {
			for _, col := range []struct{ name, spec string }{
				{"browser_hash", `browser_hash TEXT NOT NULL DEFAULT ''`},
				{"browser_expires_at", `browser_expires_at INTEGER NOT NULL DEFAULT 0`},
			} {
				var has int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('oauth_pending') WHERE name = ?`, col.name).Scan(&has); err != nil {
					tx.Rollback()
					return fmt.Errorf("migration %d: %w", i+1, err)
				}
				if has == 0 {
					if _, err := tx.Exec(`ALTER TABLE oauth_pending ADD COLUMN ` + col.spec); err != nil {
						tx.Rollback()
						return fmt.Errorf("migration %d: %w", i+1, err)
					}
				}
			}
		}
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
		if strings.Contains(migrations[i], "worker title column") {
			var has int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='worker_title'`).Scan(&has); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			if has == 0 {
				if _, err := tx.Exec(`ALTER TABLE sessions ADD COLUMN worker_title TEXT NOT NULL DEFAULT ''`); err != nil {
					tx.Rollback()
					return fmt.Errorf("migration %d: %w", i+1, err)
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

		if strings.Contains(migrations[i], "worker early quit evidence") {
			for _, col := range []struct{ name, spec string }{
				{"files_changed", "INTEGER"}, {"lines_changed", "INTEGER"},
				{"worker_complete", "INTEGER"}, {"early_quit", "INTEGER NOT NULL DEFAULT 0"},
			} {
				var has int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_runs') WHERE name=?`, col.name).Scan(&has); err != nil {
					tx.Rollback()
					return err
				}
				if has == 0 {
					if _, err := tx.Exec(`ALTER TABLE worker_runs ADD COLUMN ` + col.name + ` ` + col.spec); err != nil {
						tx.Rollback()
						return err
					}
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
		if strings.Contains(migrations[i], "session no project column") {
			var has int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'no_project'`).Scan(&has); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			if has == 0 {
				if _, err := tx.Exec(`ALTER TABLE sessions ADD COLUMN no_project INTEGER NOT NULL DEFAULT 0`); err != nil {
					tx.Rollback()
					return fmt.Errorf("migration %d: %w", i+1, err)
				}
			}
		}
		if strings.Contains(migrations[i], "session archive column") {
			var has bool
			if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('sessions') WHERE name='archived_at')`).Scan(&has); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			if !has {
				if _, err := tx.Exec(`ALTER TABLE sessions ADD COLUMN archived_at INTEGER NOT NULL DEFAULT 0`); err != nil {
					tx.Rollback()
					return fmt.Errorf("migration %d: %w", i+1, err)
				}
			}
		}
		if strings.Contains(migrations[i], "session owner use columns") {
			for _, name := range []string{"owner_used_at", "owner_uses", "pinned_at"} {
				var has bool
				if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('sessions') WHERE name=?)`, name).Scan(&has); err != nil {
					tx.Rollback()
					return fmt.Errorf("migration %d: %w", i+1, err)
				}
				if !has {
					if _, err := tx.Exec(`ALTER TABLE sessions ADD COLUMN ` + name + ` INTEGER NOT NULL DEFAULT 0`); err != nil {
						tx.Rollback()
						return fmt.Errorf("migration %d: %w", i+1, err)
					}
				}
			}
		}
		if strings.Contains(migrations[i], "profile task started column") {
			var has int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('profiles') WHERE name = 'task_started'`).Scan(&has); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			if has == 0 {
				if _, err := tx.Exec(`ALTER TABLE profiles ADD COLUMN task_started INTEGER NOT NULL DEFAULT 0`); err != nil {
					tx.Rollback()
					return fmt.Errorf("migration %d: %w", i+1, err)
				}
			}
		}
		if strings.Contains(migrations[i], "delivery review risk columns") {
			if err := migrateDeliveryReviewRisk(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
		}
		if strings.Contains(migrations[i], "worker picker note") {
			var has int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_assessments') WHERE name = 'picker_note'`).Scan(&has); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			if has == 0 {
				if _, err := tx.Exec(`ALTER TABLE worker_assessments ADD COLUMN picker_note TEXT NOT NULL DEFAULT ''`); err != nil {
					tx.Rollback()
					return fmt.Errorf("migration %d: %w", i+1, err)
				}
			}
		}
		if strings.Contains(migrations[i], "worker caller column") {
			var has int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_runs') WHERE name = 'caller_session_id'`).Scan(&has); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			if has == 0 {
				if _, err := tx.Exec(`ALTER TABLE worker_runs ADD COLUMN caller_session_id TEXT NOT NULL DEFAULT ''`); err != nil {
					tx.Rollback()
					return fmt.Errorf("migration %d: %w", i+1, err)
				}
			}
		}
		if strings.Contains(migrations[i], "delivery step tasks urgent column") {
			var has int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('delivery_step_tasks') WHERE name = 'urgent'`).Scan(&has); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			if has == 0 {
				if _, err := tx.Exec(`ALTER TABLE delivery_step_tasks ADD COLUMN urgent INTEGER NOT NULL DEFAULT 0`); err != nil {
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
		if strings.Contains(migrations[i], "worker sandbox cutover") {
			if err := s.cutoverWorkerSandboxTx(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
		}
		if strings.Contains(migrations[i], "delivery settings cutover") {
			if err := s.cutoverDeliveryTx(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
		}
		if strings.Contains(migrations[i], "delivery pipeline settings cutover") {
			if err := s.cutoverDeliveryPipelineTx(tx); err != nil {
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
	// A reconnect proves that the daemon saved the staged token, even if its
	// acknowledgement was lost. Promotion and lookup share one transaction.
	tx, err := s.db.Begin()
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	hash := hashHex(token)
	var pending string
	err = tx.QueryRow(`SELECT id,pending_token_hash FROM nodes WHERE removed=0 AND (token_hash=? OR pending_token_hash=?)`, hash, hash).Scan(&nodeID, &pending)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if pending == hash {
		if _, err = tx.Exec(`UPDATE nodes SET token_hash=pending_token_hash,pending_token_hash='' WHERE id=?`, nodeID); err != nil {
			return "", false, err
		}
	}
	err = tx.Commit()
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
	if in.Harness == "bot" {
		return "", false, reject("bot sessions are reserved for connected apps")
	}
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
	var knownID, knownName, knownWaiting string
	err = q.QueryRow(`SELECT id, name, waiting FROM sessions WHERE node_id=? AND harness=? AND native_id=?`,
		nodeID, in.Harness, in.NativeID).Scan(&knownID, &knownName, &knownWaiting)
	known := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	if known && in.Harness == "opencode" && in.Worker == nil {
		var closed int64
		if err := q.QueryRow(`SELECT closed_at FROM sessions WHERE id=?`, knownID).Scan(&closed); err != nil {
			return "", false, err
		}
		if closed > 0 {
			if in.OpenCodeActivity <= closed {
				return knownID, false, nil
			}
			if _, err := q.Exec(`UPDATE sessions SET closed_at=0 WHERE id=?`, knownID); err != nil {
				return "", false, err
			}
		}
	}
	if known && in.Worker != nil && in.Worker.RunGeneration > 0 {
		var current int
		if err := q.QueryRow(`SELECT worker_run_generation FROM sessions WHERE id=?`, knownID).Scan(&current); err != nil {
			return "", false, err
		}
		if in.Worker.RunGeneration < current {
			return knownID, false, nil
		}
		if in.Worker.RunGeneration > current {
			if _, err := q.Exec(`UPDATE sessions SET worker_run_generation=?, worker_run_active=1 WHERE id=?`, in.Worker.RunGeneration, knownID); err != nil {
				return "", false, err
			}
		}
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
	workerTitle := ""
	if in.Worker != nil {
		workerTitle = in.Worker.Title
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
		keepWaitSince(knownWaiting, w)
		if w.Since == 0 {
			w.Since = s.now().Unix()
		}
		if b, err := json.Marshal(w); err == nil {
			waiting = string(b)
		}
	}
	lastActive := in.LastActive * int64(time.Second)
	_, err = q.Exec(`INSERT INTO sessions (id, node_id, harness, native_id, name, title, cwd, pid, status, delivery, last_seen, last_active, online,
		repo_root, repo_project, repo_remote, repo_branch, repo_worktree, mode, model, worker, worker_title, repo_description, worker_started,
		tools_version, tools_old, created_at, waiting, graph_excluded, no_project, harness_activity, idle_compacted_at, limited_until, limited_reason)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,1,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (node_id, harness, native_id) DO UPDATE SET
		name=excluded.name, title=excluded.title, cwd=excluded.cwd, pid=excluded.pid,
		-- A worker whose run is over stays ended. A late session sync from its
		-- machine still lists the harness session for a moment; registering
		-- that as online and busy would undo the end report. Only an explicit
		-- resume (SetWorkerRunActive) lets the registration bring it back.
		status=CASE WHEN sessions.worker<>'' AND sessions.worker_run_active=0 AND sessions.status='ended'
			THEN 'ended' ELSE excluded.status END,
		delivery=excluded.delivery, harness_lost=0,
		last_seen=CASE WHEN sessions.worker<>'' AND sessions.worker_run_active=0 AND sessions.status='ended'
			THEN sessions.last_seen ELSE excluded.last_seen END,
		online=CASE WHEN sessions.worker<>'' AND sessions.worker_run_active=0 AND sessions.status='ended'
			THEN 0 ELSE 1 END, archived_at=0,
		last_active=MAX(sessions.last_active, excluded.last_active),
		harness_activity=MAX(sessions.harness_activity, ?,
			CASE WHEN excluded.status='busy' AND sessions.status<>'busy' THEN excluded.last_seen ELSE 0 END),
		repo_root=excluded.repo_root, repo_project=excluded.repo_project,
		repo_remote=excluded.repo_remote, repo_branch=excluded.repo_branch,
		repo_worktree=excluded.repo_worktree, mode=excluded.mode, model=excluded.model, worker=excluded.worker,
		worker_title=excluded.worker_title,
		repo_description=excluded.repo_description, worker_started=excluded.worker_started,
		tools_version=excluded.tools_version, tools_old=excluded.tools_old, waiting=excluded.waiting, graph_excluded=excluded.graph_excluded,
		no_project=excluded.no_project, idle_compacted_at=excluded.idle_compacted_at,
		limited_until=excluded.limited_until, limited_reason=excluded.limited_reason,
		worker_run_active=CASE WHEN sessions.worker<>'' AND sessions.worker_run_active=0 AND sessions.status='ended'
			THEN 0 WHEN sessions.online=0 THEN 1 ELSE sessions.worker_run_active END`,
		newID, nodeID, in.Harness, in.NativeID, name, title, in.Cwd, in.PID, in.Status, in.Delivery, now, lastActive,
		in.Repo.Root, in.Repo.Project, in.Repo.Remote, in.Repo.Branch, in.Repo.Worktree, in.Mode, in.Model, worker, workerTitle,
		in.Repo.Description, workerStarted, protocol.CleanModel(in.ToolsVersion), boolInt(in.ToolsOld), now, waiting,
		graphExclusion(in.GraphExcluded, in.GraphChecked), boolInt(in.NoProject),
		max(in.HarnessActivity*int64(time.Second), busyInitialActivity(in.Status, now)),
		in.IdleCompactedAt, in.LimitedUntil, protocol.CleanModel(in.LimitedReason), in.HarnessActivity*int64(time.Second))
	if err != nil {
		return "", false, err
	}
	err = q.QueryRow(`SELECT id FROM sessions WHERE node_id=? AND harness=? AND native_id=?`,
		nodeID, in.Harness, in.NativeID).Scan(&id)
	if err != nil {
		return "", false, err
	}
	if waiting != knownWaiting {
		if err := s.syncWaitingAsk(q, id, waiting); err != nil {
			return "", false, err
		}
	}
	if in.Worker != nil && in.Worker.RunGeneration > 0 {
		if _, err := q.Exec(`UPDATE sessions SET worker_run_generation=MAX(worker_run_generation,?) WHERE id=?`, in.Worker.RunGeneration, id); err != nil {
			return "", false, err
		}
	}
	// A new progress report clears the current warning without waiting for
	// the minute sweep. Keep a pending delivery warning until its ack.
	_, err = q.Exec(`UPDATE inbox_items SET status='closed',answer='activity_or_end',answered_at=?
		WHERE session_id=? AND message_id='session-stall' AND status IN ('open','dismissed') AND EXISTS
		(SELECT 1 FROM sessions s WHERE s.id=? AND (s.worker<>'' OR s.waiting<>'' OR s.harness_activity / 1000000000 > CAST(inbox_items.conversation_id AS INTEGER)
		OR (s.status<>'busy' AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.recipient=s.id
		AND (m.state IN ('stored','dispatched') OR (m.state='failed' AND EXISTS (SELECT 1 FROM delivery_retries r WHERE r.message_id=m.id)))))))`, now, id, id)
	if err != nil {
		return "", false, err
	}
	// Triggers from the repo migration have now created any new repo records.
	// A sync may update the repo context but must not replace a selected agent.
	_, err = q.Exec(`UPDATE sessions SET
		repo_id=CASE WHEN harness IN ('cto','owner') THEN NULL ELSE (SELECT id FROM repos WHERE id=COALESCE(NULLIF(repo_project,''),(SELECT project FROM profiles WHERE session_id=sessions.id))) END,
		agent_id=CASE WHEN harness='cto' THEN 'cto' WHEN harness='owner' OR worker<>'' THEN NULL ELSE COALESCE(agent_id,(SELECT owner_agent_id FROM repos WHERE id=COALESCE(NULLIF(repo_project,''),(SELECT project FROM profiles WHERE session_id=sessions.id)))) END
		WHERE id=?`, id)
	if err != nil {
		return "", false, err
	}
	removed, err := q.Exec(`DELETE FROM repo_start_reservations WHERE node_id=? AND harness=? AND native_id=? AND native_id<>''`, nodeID, in.Harness, in.NativeID)
	if err != nil {
		return "", false, err
	}
	n, err := removed.RowsAffected()
	if err != nil {
		return "", false, err
	}
	if n == 0 && !known && in.Worker == nil {
		// Codex can hand a new terminal to the owner before it has a native
		// ID. Count the new live session in place of one matching launch.
		if _, err = q.Exec(`DELETE FROM repo_start_reservations WHERE rowid IN (SELECT r.rowid FROM repo_start_reservations r JOIN sessions s ON s.id=? WHERE r.node_id=? AND r.harness=? AND r.native_id='' AND r.repo_id=s.repo_id ORDER BY r.expires_at,r.ref LIMIT 1)`, id, nodeID, in.Harness); err != nil {
			return "", false, err
		}
	}
	if err := s.applyStartedAgent(q, nodeID, in, id, known); err != nil {
		return "", false, err
	}
	if err := s.updateFailedAgentSession(q, nodeID, in.NativeID, id); err != nil {
		return "", false, err
	}
	if err = checkActiveSessionIdentity(q, id); err != nil {
		return "", false, err
	}
	// The owner session is the person, not a new agent: no session.new for it.
	fresh := !known && !protocol.IsOwner(in.Harness)
	if fresh {
		s.event(q, "session.new", nodeID, id, "", sessionDetail(in, name))
	}
	if in.Worker != nil && in.Worker.StartRef != "" {
		_, _ = q.Exec(`UPDATE worker_starts SET session_id=? WHERE (ref=? OR (legacy_ref=? AND legacy_ref<>'')) AND node_id=? AND owner=? AND session_id=''`,
			id, in.Worker.StartRef, in.Worker.StartRef, nodeID, in.Worker.Owner)
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
	if _, err := s.ExpireSessionAgentStarts(); err != nil {
		return "", err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	id, created, err := s.upsertSession(tx, nodeID, in)
	if err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	if created {
		s.logNewSessions([]newSession{{id, nodeID, in}})
	}
	return id, nil
}

// SyncSessions upserts the full set and marks the node's other sessions offline.
func (s *Store) SyncSessions(nodeID string, in []protocol.SessionInfo) ([]protocol.SessionInfo, error) {
	if _, err := s.ExpireSessionAgentStarts(); err != nil {
		return nil, err
	}
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
		var merger int
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM project_mergers WHERE session_id=?)`, id).Scan(&merger); err != nil {
			return nil, err
		}
		explicit := ""
		if merger != 0 {
			explicit = "merger"
		}
		si.Type = protocol.SessionType(si.Harness, si.Worker, explicit)
		out = append(out, si)
	}
	// The registered CTO belongs to this node even when its harness is asleep.
	// A node disconnect marks it offline; its host sync makes it reachable again
	// without allowing a different node to move the identity.
	if _, err := tx.Exec(`UPDATE sessions SET online=1, archived_at=0, status=CASE WHEN online=0 THEN 'dormant' ELSE status END, last_seen=? WHERE node_id=? AND harness='cto' AND id=(SELECT value FROM owner_settings WHERE key='cto_session')`, s.now().UnixNano(), nodeID); err != nil {
		return nil, err
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
		if _, err := tx.Exec(`UPDATE sessions SET online = 0,
			status = CASE WHEN worker<>'' AND worker_run_active=0 THEN 'ended' ELSE status END,
			last_seen = ? WHERE id = ?`, s.now().UnixNano(), id); err != nil {
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

// SetSessionOffline takes a session offline. A worker whose run is over is
// also ended: its last status, often busy, is not the state of an ended
// worker. A worker that only lost its connection keeps its status, because it
// may still be busy.
func (s *Store) SetSessionOffline(nodeID, sessionID string) error {
	_, err := s.db.Exec(`UPDATE sessions SET online = 0, harness_lost=0,
		status = CASE WHEN worker<>'' AND worker_run_active=0 THEN 'ended' ELSE status END,
		last_seen = ? WHERE id = ? AND node_id = ?`, s.now().UnixNano(), sessionID, nodeID)
	return err
}

// SetSessionLost records an unexpected disappearance while a turn was busy.
// It does not decide that the session ended.
func (s *Store) SetSessionLost(nodeID, sessionID string) error {
	_, err := s.db.Exec(`UPDATE sessions SET online=0,harness_lost=CASE WHEN status='busy' AND worker='' THEN 1 ELSE 0 END,last_seen=? WHERE id=? AND node_id=?`, s.now().UnixNano(), sessionID, nodeID)
	return err
}

// SetWorkerSessionEnded records a worker's final state. Merely losing a
// connection uses SetSessionOffline: a disconnected worker may still be busy.
// A later registration after an explicit resume supplies its new live status.
func (s *Store) SetWorkerSessionEnded(nodeID, sessionID string, generation ...int) error {
	gen := 0
	if len(generation) > 0 {
		gen = generation[0]
	}
	result, err := s.db.Exec(`UPDATE sessions SET online = 0, status = 'ended', worker_run_active = 0,
		last_seen = ? WHERE id = ? AND node_id = ? AND (?=0 OR worker_run_generation=?)`, s.now().UnixNano(), sessionID, nodeID, gen, gen)
	if err == nil && gen > 0 && s.logf != nil {
		if n, _ := result.RowsAffected(); n == 0 {
			s.logf("worker.stale_end", "node", nodeID, "session", sessionID, "generation", gen)
		}
	}
	return err
}

// SetWorkerRunActive marks a worker's run as started again. The gateway calls
// it for legacy resumes; new daemons announce the next generation before
// registering the harness in either resume path. A stale generation is ignored.
func (s *Store) SetWorkerRunActive(nodeID, sessionID string, generation ...int) error {
	gen := 0
	if len(generation) > 0 {
		gen = generation[0]
	}
	_, err := s.db.Exec(`UPDATE sessions SET worker_run_active = 1, worker_run_generation=MAX(worker_run_generation,?) WHERE id = ? AND node_id = ? AND worker<>'' AND (?=0 OR worker_run_generation<=?)`,
		gen, sessionID, nodeID, gen, gen)
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
	// Replace this node's retained set, including releases of deleted branches.
	if _, err := tx.Exec(`UPDATE sessions SET worker_retained=0 WHERE node_id=? AND worker_retained<>0`, nodeID); err != nil {
		return 0, err
	}
	for _, id := range running {
		if _, err := tx.Exec(`UPDATE sessions SET worker_retained=1 WHERE node_id=? AND id=? AND worker<>''`, nodeID, id); err != nil {
			return 0, err
		}
	}
	rows, err := tx.Query(`SELECT id FROM sessions WHERE archived_at=0 AND pinned_at=0 AND node_id = ? AND online = 0 AND worker != ''
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
	rows, err := tx.Query(`SELECT id FROM sessions WHERE archived_at=0 AND pinned_at=0 AND owner_used_at < ? AND node_id = ? AND online = 0 AND worker = ''
		AND substr(cwd, 1, ?) = ?
        AND NOT EXISTS (SELECT 1 FROM graph_outcomes o JOIN messages req ON req.id=o.message_id JOIN sessions src ON src.id=req.sender WHERE o.resolution='' AND o.outcome IN ('reject','escalate') AND src.harness<>'owner' AND (sessions.id=req.sender OR sessions.id=req.recipient))`, s.now().Add(-7*24*time.Hour).UnixNano(), nodeID, len(prefix), prefix)
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
	_, err := s.db.Exec(`UPDATE sessions SET online = 0,
		status = CASE WHEN worker<>'' AND worker_run_active=0 THEN 'ended' ELSE status END,
		last_seen = ? WHERE node_id = ? AND online = 1`, s.now().UnixNano(), nodeID)
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
	now := s.now().UnixNano()
	taskStarted := int64(0)
	if p.Task != "" {
		taskStarted = now
	}
	// The time of the session's earlier announce, for the asks it left open.
	var previousAnnounce int64
	if err := s.db.QueryRow(`SELECT updated_at FROM profiles WHERE session_id=?`, p.SessionID).Scan(&previousAnnounce); err != nil && err != sql.ErrNoRows {
		return protocol.Profile{}, err
	}
	_, err = s.db.Exec(`INSERT INTO profiles (session_id, name, function, project, capabilities, task, updated_at, expertise, task_started)
		VALUES (?,'','',?,?,?,?,?,?)
		ON CONFLICT (session_id) DO UPDATE SET
		project=CASE WHEN excluded.project='' THEN profiles.project ELSE excluded.project END,
		capabilities=CASE WHEN ? THEN profiles.capabilities ELSE excluded.capabilities END,
		task=CASE WHEN excluded.task='' THEN profiles.task ELSE excluded.task END,
		expertise=CASE WHEN excluded.expertise='' THEN profiles.expertise ELSE excluded.expertise END,
		task_started=CASE
			WHEN excluded.task='' THEN profiles.task_started
			WHEN profiles.task=excluded.task THEN profiles.task_started
			ELSE excluded.task_started END,
		updated_at=excluded.updated_at`,
		p.SessionID, p.Project, caps, p.Task, now, p.Expertise, taskStarted, p.Capabilities == nil)
	if err != nil {
		return protocol.Profile{}, err
	}
	// An announce without a wait says the session went on. A question or an
	// approval it raised before its earlier announce is overtaken. One raised
	// since (the ask and the task line of the same turn) stays.
	if previousAnnounce > 0 && protocol.CleanWaiting(p.Waiting) == nil {
		if _, err := s.closeSessionOwnerAsks(s.db, p.SessionID, previousAnnounce, protocol.AskReasonMovedOn, protocol.AskKindQuestion, protocol.AskKindApproval); err != nil {
			return protocol.Profile{}, err
		}
	}
	if err := s.answerCheck(p.SessionID, p.Task); err != nil {
		return protocol.Profile{}, err
	}
	if err := s.loopAnnounce(p.SessionID, p.Task); err != nil {
		return protocol.Profile{}, err
	}
	a, ok, err := s.Agent(p.SessionID)
	if err != nil || !ok {
		return protocol.Profile{}, err
	}
	return a.Profile, nil
}

// agentSelectTemplate is the select of an agent row. Three of its values count
// or look up rows of other tables by the session's repo or id: the Repo
// max_agents setting, the number of live sessions in the Repo and the stall
// item. They are written two ways, and the values are the same:
//   - agentSelect has a subquery for each, which runs for that row. It suits
//     one session or a few.
//   - agentListSelect reads each from a join on a table that is worked out
//     once for the whole query. It suits the list of every session, where the
//     subqueries ran once per row.
const agentSelectTemplate = `SELECT s.id, s.node_id, COALESCE(n.hostname, '-'), s.harness, s.cwd, s.native_id, s.status,
	CASE WHEN s.harness='bot' THEN EXISTS(SELECT 1 FROM apps a WHERE a.id=s.node_id AND a.kind='bot' AND a.revoked_at IS NULL) ELSE s.online END, s.last_seen, s.last_active,
	COALESCE(NULLIF(p.name,''), s.name), s.repo_description, COALESCE(p.project,''),
	COALESCE(p.capabilities,'[]'), COALESCE(p.task,''),
	s.repo_root, s.repo_project, s.repo_remote, s.repo_branch, s.repo_worktree, s.mode, s.model, s.pid, s.worker, s.repo_description,
	s.peer_trust, COALESCE(p.expertise,''), s.delegation, s.title, s.created_at, s.tools_version, s.tools_old, s.waiting, COALESCE((SELECT name FROM project_display_names WHERE project=COALESCE(NULLIF(s.repo_project,''),p.project)),''),
	CASE WHEN EXISTS(SELECT 1 FROM project_mergers pm WHERE pm.session_id=s.id) THEN 'merger' ELSE '' END,
 COALESCE(p.task_started,0),
 COALESCE((SELECT json_object('run_id',wa.run_id,'session_id',wa.session_id,'started_at',wa.started_at,
 'assessed_at',wa.assessed_at,'reasoning',wa.reasoning,'scope',wa.scope,'coupling',wa.coupling,
 'uncertainty',wa.uncertainty,'verification',wa.verification,'risk',wa.risk,'d',wa.d,
 'label',wa.label,'bottleneck',wa.bottleneck,'confidence',wa.confidence,'rules_version',wa.rules_version,
 'judge_harness',wa.judge_harness,'judge_model',wa.judge_model,'reasons',json(wa.reasons),
 'picker_label',wa.picker_label,'error',wa.error) FROM worker_assessments wa
 WHERE wa.session_id=s.id ORDER BY wa.started_at DESC, wa.rowid DESC LIMIT 1),''),
 COALESCE(s.repo_id,''), COALESCE(s.agent_id,''), COALESCE((SELECT display_name FROM agents WHERE id=s.agent_id),''),
 COALESCE((COALESCE((SELECT lead_session FROM repo_leads WHERE repo_id=s.repo_id),s.id)=s.id) AND s.agent_id=(SELECT CASE WHEN EXISTS(SELECT 1 FROM repo_leads WHERE repo_id=r.id) THEN (SELECT agent_id FROM repo_leads WHERE repo_id=r.id) ELSE r.owner_agent_id END FROM repos r WHERE r.id=s.repo_id),0),
	EXISTS(SELECT 1 FROM repo_leads rl WHERE rl.repo_id=s.repo_id AND rl.agent_id=s.agent_id AND (rl.lead_session IS NULL OR rl.lead_session=s.id)),
 EXISTS(SELECT 1 FROM teams t JOIN team_members tm ON tm.team_id=t.id WHERE t.active=1 AND t.owner_agent_id=s.agent_id AND (t.lead_session IS NULL OR t.lead_session=s.id) AND ((tm.member_kind='repo' AND tm.member_id=s.repo_id) OR (tm.member_kind='agent' AND tm.member_id=s.agent_id))),
 @MAX@
 @COUNT@
 s.owner_used_at, s.owner_uses, s.pinned_at, s.idle_compacted_at,
 @STALL@
 s.limited_until, s.limited_reason
	FROM sessions s LEFT JOIN nodes n ON n.id = s.node_id LEFT JOIN profiles p ON p.session_id = s.id@JOINS@`

const (
	// agentRepoCountWhere is what makes a session count toward its Repo's
	// agent limit. x is the session.
	agentRepoCountWhere = `x.online=1 AND x.worker='' AND x.harness NOT IN ('owner','cto','bot') AND x.id<>COALESCE((SELECT value FROM owner_settings WHERE key='cto_session'),'') AND NOT EXISTS(SELECT 1 FROM project_mergers pm WHERE pm.session_id=x.id)`

	// A Repo has one max_agents row, as the key is unique. The stall item is
	// the newest of the session, and the later row when two are as new, which
	// is what the subquery's scan of the index gives.
	agentListJoins = `
	LEFT JOIN setting_values mx ON mx.key='max_agents' AND mx.level='repo' AND mx.scope=s.repo_id AND mx.item=''
	LEFT JOIN (SELECT x.repo_id AS repo_id, COUNT(*) AS n FROM sessions x WHERE ` + agentRepoCountWhere + ` GROUP BY x.repo_id) rc ON rc.repo_id=s.repo_id
	LEFT JOIN (SELECT session_id, conversation_id, ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY created_at DESC, rowid DESC) AS rn FROM inbox_items WHERE message_id='session-stall' AND status IN ('open','dismissed')) st ON st.session_id=s.id AND st.rn=1`
)

var (
	agentSelect = strings.NewReplacer(
		"@MAX@", `COALESCE((SELECT CAST(value AS INTEGER) FROM setting_values WHERE key='max_agents' AND level='repo' AND scope=s.repo_id AND item=''),0),`,
		"@COUNT@", `(SELECT COUNT(*) FROM sessions x WHERE x.repo_id=s.repo_id AND `+agentRepoCountWhere+`),`,
		"@STALL@", `COALESCE((SELECT CAST(i.conversation_id AS INTEGER) FROM inbox_items i WHERE i.session_id=s.id AND i.message_id='session-stall' AND i.status IN ('open','dismissed') ORDER BY i.created_at DESC LIMIT 1),0),`,
		"@JOINS@", "").Replace(agentSelectTemplate)
	agentListSelect = strings.NewReplacer(
		"@MAX@", `COALESCE(CAST(mx.value AS INTEGER),0),`,
		"@COUNT@", `COALESCE(rc.n,0),`,
		"@STALL@", `COALESCE(CAST(st.conversation_id AS INTEGER),0),`,
		"@JOINS@", agentListJoins).Replace(agentSelectTemplate)
)

func scanAgent(sc interface{ Scan(...any) error }) (protocol.AgentView, error) {
	var a protocol.AgentView
	var online int
	var lastSeen, lastActive, created int64
	var caps, worker, assessment string
	var trust int
	var deleg, waiting string
	var toolsOld int
	err := sc.Scan(&a.SessionID, &a.NodeID, &a.Hostname, &a.Harness, &a.Cwd, &a.NativeID, &a.Status, &online, &lastSeen, &lastActive,
		&a.Profile.Name, &a.Profile.Function, &a.Profile.Project, &caps, &a.Profile.Task,
		&a.Repo.Root, &a.Repo.Project, &a.Repo.Remote, &a.Repo.Branch, &a.Repo.Worktree, &a.Mode, &a.Model, &a.PID, &worker, &a.Repo.Description,
		&trust, &a.Profile.Expertise, &deleg, &a.Title, &created, &a.ToolsVersion, &toolsOld, &waiting, &a.ProjectDisplayName,
		&a.Type, &a.TaskStarted, &assessment, &a.RepoContext, &a.AgentID, &a.AgentName, &a.RepoLead, &a.RepoLeadExplicit, &a.TeamLead, &a.RepoMaxAgents, &a.RepoAgentCount, &a.OwnerUsedAt, &a.OwnerUses, &a.PinnedAt, &a.IdleCompactedAt, &a.StalledSince, &a.LimitedUntil, &a.LimitedReason)
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
			if assessment != "" {
				var v protocol.WorkerAssessment
				if json.Unmarshal([]byte(assessment), &v) == nil {
					a.Worker.Assessment = &v
				}
			}
		}
	}
	a.RepoOverMax = a.RepoMaxAgents > 0 && a.RepoAgentCount > a.RepoMaxAgents
	a.Type = protocol.SessionType(a.Harness, a.Worker, a.Type)
	a.Online = online == 1
	a.LastSeen = lastSeen / int64(time.Second)
	a.LastActive = lastActive / int64(time.Second)
	a.OwnerUsedAt /= int64(time.Second)
	a.PinnedAt /= int64(time.Second)
	a.TaskStarted = a.TaskStarted / int64(time.Second)
	a.FirstSeen = created / int64(time.Second)
	if json.Unmarshal([]byte(caps), &a.Profile.Capabilities) != nil || a.Profile.Capabilities == nil {
		a.Profile.Capabilities = []string{}
	}
	return a, nil
}

func (s *Store) Agent(sessionID string) (protocol.AgentView, bool, error) {
	a, ok, err := s.agentBase(sessionID)
	if err != nil || !ok {
		return a, false, err
	}
	one := []protocol.AgentView{a}
	in, err := s.agentViewInputs()
	if err != nil {
		return a, false, err
	}
	if err := s.applyDelegationWith(one, in); err != nil {
		return a, false, err
	}
	if err := s.applyAgentChartersWith(one, false, in); err != nil {
		return a, false, err
	}
	if err := s.applyLoops(one); err != nil {
		return a, false, err
	}
	return one[0], true, nil
}

// agentBase reads one agent row without delegation, charter or rules. Callers
// that need only the stored fields (name, title, node) use it so they do not
// build the graph and the settings snapshot.
func (s *Store) agentBase(sessionID string) (protocol.AgentView, bool, error) {
	a, err := scanAgent(s.db.QueryRow(agentSelect+` WHERE s.archived_at=0 AND s.id = ? AND (n.id IS NOT NULL OR s.harness='bot')`, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return a, false, nil
	}
	if err != nil {
		return a, false, err
	}
	s.botOnline(&a)
	a.OwnerUses = decayOwnerUses(a.OwnerUses, a.OwnerUsedAt, s.now().Unix())
	return a, true, nil
}

// workerForContinue looks up workers even when their sessions are archived.
func (s *Store) workerForContinue(ref, owner string) (protocol.AgentView, bool, error) {
	a, err := scanAgent(s.db.QueryRow(agentSelect+` WHERE s.worker<>'' AND (s.id=? OR (json_extract(s.worker, '$.id')=? AND json_extract(s.worker, '$.owner')=?)) AND n.id IS NOT NULL ORDER BY (s.id=?) DESC LIMIT 1`, ref, ref, owner, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return a, false, nil
	}
	if err != nil {
		return a, false, err
	}
	return a, true, nil
}

// Older Pi extensions registered SDK helpers as independent sessions. Match
// only background non-workers with a foreground session in the same process.
// Overlapping lifetimes keep a later reuse of the PID from hiding old sessions.
const piChildSQL = `s.harness = 'pi' AND s.mode = 'background' AND s.pid > 0 AND s.worker = ''
	AND EXISTS (SELECT 1 FROM sessions parent WHERE parent.node_id = s.node_id
		AND parent.harness = 'pi' AND parent.mode = 'foreground' AND parent.pid = s.pid
		AND (parent.online = 1 OR parent.last_seen >= s.created_at)
		AND (s.online = 1 OR s.last_seen >= parent.created_at))`

// Agents lists the agent views that match f.
//
// The read is kept until the next write to the database, but never past the
// second it was read in: the view holds windows and decays that depend on the
// time. Each caller gets a copy.
func (s *Store) Agents(f protocol.QueryAgents) ([]protocol.AgentView, error) {
	key, err := json.Marshal(f)
	if err != nil {
		return s.agentsRead(f)
	}
	v, err := s.readThrough("agents|"+string(key), true, func() (any, error) { return s.agentsRead(f) })
	if err != nil {
		return nil, err
	}
	return copyOf(v.([]protocol.AgentView)), nil
}

func (s *Store) agentsRead(f protocol.QueryAgents) ([]protocol.AgentView, error) {
	out, err := s.agentsBase(f)
	if err != nil {
		return nil, err
	}
	if err := s.applyDelegation(out); err != nil {
		return nil, err
	}
	if err := s.applyAgentCharters(out, true); err != nil {
		return nil, err
	}
	if err := s.applyPending(out); err != nil {
		return nil, err
	}
	if err := s.applyLoops(out); err != nil {
		return nil, err
	}
	return out, nil
}

// agentsBase is Agents without delegation, charters and pending requests. A
// caller that reads only the stored fields (id, node, harness, model, worker)
// uses it, so it does not build the graph and the settings snapshot.
func (s *Store) agentsBase(f protocol.QueryAgents) ([]protocol.AgentView, error) {
	return s.agentsBaseWith(agentListSelect, f)
}

// agentsBaseWith is agentsBase with the select it reads the rows with.
func (s *Store) agentsBaseWith(selectSQL string, f protocol.QueryAgents) ([]protocol.AgentView, error) {
	// The harness filter is applied here too, so the select list (a dozen
	// subqueries per row) is only worked out for the sessions that stay.
	where, args := ``, []any{}
	if f.Harness != "" {
		where, args = ` AND s.harness=?`, append(args, f.Harness)
	}
	rows, err := s.db.Query(selectSQL+` WHERE s.archived_at=0 AND (n.id IS NOT NULL OR s.harness='bot') AND NOT (`+piChildSQL+`)`+where+` ORDER BY s.last_seen DESC, s.id`, args...)
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
		s.botOnline(&a)
		a.OwnerUses = decayOwnerUses(a.OwnerUses, a.OwnerUsedAt, s.now().Unix())
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
	return out, nil
}

// ---- messages ----

const msgSelect = `SELECT m.id, m.conversation_id, m.parent_id, m.sender, m.recipient, m.type, m.body, m.hop,
	m.idem_key, m.created_at, m.expires_at, m.state, COALESCE((SELECT d.detail FROM deliveries d WHERE d.message_id=m.id AND d.state=m.state ORDER BY d.rowid DESC LIMIT 1),'') || COALESCE((SELECT ' (retry ' || attempts || ' scheduled)' FROM delivery_retries WHERE message_id=m.id AND m.state='failed'),''),
	COALESCE(NULLIF(p.name,''), s.name, ''), COALESCE(s.harness, ''), COALESCE(s.worker, '') != '', m.sender_cto, m.owner_approved, EXISTS(SELECT 1 FROM outcome_overrules oo WHERE oo.message_id=m.id), m.approval_note, COALESCE((SELECT substr(cs.prompt,1,200) FROM charter_signals cs WHERE cs.message_id=m.id),''),
	COALESCE((SELECT mf.files FROM message_files mf WHERE mf.message_id=m.id),''), m.worker_task, m.is_order
	FROM messages m LEFT JOIN sessions s ON s.id = m.sender LEFT JOIN profiles p ON p.session_id = m.sender`

func scanMessage(sc interface{ Scan(...any) error }) (protocol.Message, error) {
	var m protocol.Message
	var created, expires int64
	var files string
	var workerTask sql.NullBool
	err := sc.Scan(&m.ID, &m.ConversationID, &m.ParentID, &m.From, &m.To, &m.Type, &m.Body, &m.Hop,
		&m.IdempotencyKey, &created, &expires, &m.State, &m.DeliveryDetail, &m.FromName, &m.FromHarness, &m.FromWorker, &m.FromCTO, &m.OwnerApproved, &m.Overruled, &m.ApprovalNote, &m.OwnerInstruction, &files, &workerTask, &m.IsOrder)
	if workerTask.Valid {
		m.WorkerTask = &workerTask.Bool
	}
	if err == nil && files != "" {
		if jerr := json.Unmarshal([]byte(files), &m.Files); jerr != nil {
			err = fmt.Errorf("message %s: stored files: %w", m.ID, jerr)
		}
	}
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
func (s *Store) StoreMessage(p protocol.MsgSend) (protocol.Message, bool, error) {
	return s.storeMessage(p, nil)
}

// StoreLeadMessage applies lead routing only to user-originated message sends.
func (s *Store) StoreLeadMessage(p protocol.MsgSend) (protocol.Message, bool, string, error) {
	var route string
	m, created, err := s.storeMessage(p, &route)
	return m, created, route, err
}

func (s *Store) storeMessage(p protocol.MsgSend, route *string) (msg protocol.Message, created bool, err error) {
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
	if err := protocol.ValidateFiles(p.Files); err != nil {
		return msg, false, reject("files: %v", err)
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
		if route != nil && first.To != p.Recipient && strings.HasPrefix(first.Body, "Redirected to ") {
			*route, _, _ = strings.Cut(first.Body, "\n")
		}
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
	// Initial worker briefs must not revive or reach a worker that has ended.
	// Check in the message transaction: worker.stopped can record the run
	// before its separate session-offline update reaches the database.
	workerTask, err := workerTaskForSend(tx, p)
	if err != nil {
		return msg, false, err
	}
	if workerTask {
		var stopped int
		err := tx.QueryRow(`SELECT COUNT(*) FROM sessions s WHERE s.id=? AND s.worker<>''
			AND (s.online=0 OR s.worker_run_active=0)`, p.Recipient).Scan(&stopped)
		if err != nil {
			return msg, false, err
		}
		if stopped != 0 {
			return msg, false, reject("worker %s is offline or has ended; its task cannot be sent", p.Recipient)
		}
	}
	if !s.allow(p.Sender) {
		return msg, false, reject("rate limit: at most %d messages per minute per session", protocol.RatePerMinute)
	}

	now := s.now()
	m := protocol.Message{
		ID: protocol.NewID("m"), From: p.Sender, To: p.Recipient, Type: p.Type, Body: p.Body,
		IdempotencyKey: p.IdempotencyKey, CreatedAt: now.UTC(), ExpiresAt: now.Add(protocol.DefaultTTL).UTC(),
		State: protocol.StateStored, FromCTO: p.FromCTO, OwnerApproved: p.OwnerApproved && p.FromCTO, WorkerTask: &workerTask,
	}
	threaded := false
	var parent protocol.Message
	if p.ParentID != "" {
		var ok bool
		parent, ok, err = s.message(tx, "m.id = ?", p.ParentID)
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
	} else if p.ConversationID != "" {
		// Threaded into a conversation without a parent (W4.4): only by a
		// session that the recipient wrote to in that conversation.
		var hop sql.NullInt64
		if err := tx.QueryRow(`SELECT MAX(hop) FROM messages WHERE conversation_id=? AND sender=? AND recipient=?`,
			p.ConversationID, p.Recipient, p.Sender).Scan(&hop); err != nil {
			return msg, false, err
		}
		if !hop.Valid {
			return msg, false, reject("conversation %q has no message from the recipient to the sender", p.ConversationID)
		}
		m.ConversationID = p.ConversationID
		m.Hop = int(hop.Int64) + 1
		threaded = true
		if m.Type == "" {
			m.Type = protocol.TypeReply
		}
	} else {
		m.ConversationID = protocol.NewID("c")
		if m.Type == "" {
			m.Type = protocol.TypeRequest
		}
	}
	if p.WorkerIdleNotice {
		if m.Type != protocol.TypeReply || m.ParentID == "" {
			return msg, false, reject("an idle worker notice must reply to its task")
		}
		var workerOwner string
		if err := tx.QueryRow(`SELECT COALESCE(CASE WHEN json_valid(worker) THEN json_extract(worker, '$.owner') END, '') FROM sessions WHERE id=?`, p.Sender).Scan(&workerOwner); err != nil {
			return msg, false, err
		}
		if workerOwner == "" || workerOwner != p.Recipient {
			return msg, false, reject("an idle worker notice must be sent to the worker's owner")
		}
		var latestTask string
		err := tx.QueryRow(`SELECT id FROM messages WHERE sender=? AND recipient=? AND type=? ORDER BY rowid DESC LIMIT 1`,
			workerOwner, p.Sender, protocol.TypeRequest).Scan(&latestTask)
		if err != nil {
			if err == sql.ErrNoRows {
				return msg, false, reject("an idle worker notice has no current task")
			}
			return msg, false, err
		}
		if m.ParentID != latestTask {
			return msg, false, reject("idle worker notice parent %q is stale; latest task is %q", m.ParentID, latestTask)
		}
	}
	switch m.Type {
	case protocol.TypeRequest, protocol.TypeReply, protocol.TypeNotification:
	default:
		return msg, false, reject("unknown message_type %q", m.Type)
	}
	if route != nil && p.LeadAddress != "" {
		if err = validateLeadAddress(tx, p.LeadAddress, m.To, s.now()); err != nil {
			return msg, false, err
		}
	}
	if route != nil && p.LeadAddress == "" {
		*route, err = routeLead(tx, &m, s.now())
		if err != nil {
			return msg, false, err
		}
	}
	if route != nil {
		note, err := limitedNote(tx, m.To, s.now())
		if err != nil {
			return msg, false, err
		}
		if note != "" {
			if *route != "" {
				*route += "\n"
			}
			*route += note
		}
	}
	// Apply this after lead and legacy routing, against the actual recipient.
	// A reply remains permitted throughout a CTO-started conversation.
	var ctoID string
	err = tx.QueryRow(`SELECT value FROM owner_settings WHERE key='cto_session'`).Scan(&ctoID)
	if err != nil && err != sql.ErrNoRows {
		return msg, false, err
	}
	if m.To == ctoID && ctoID != "" {
		var harness string
		if err = tx.QueryRow(`SELECT harness FROM sessions WHERE id=?`, m.From).Scan(&harness); err != nil {
			return msg, false, err
		}
		allowed := protocol.IsOwner(harness)
		if !allowed && p.WorkerNotice {
			var workerOwner string
			if err = tx.QueryRow(`SELECT COALESCE(CASE WHEN json_valid(worker) THEN json_extract(worker, '$.owner') END, '') FROM sessions WHERE id=?`, m.From).Scan(&workerOwner); err != nil {
				return msg, false, err
			}
			allowed = workerOwner == ctoID
		}
		if !allowed && m.Type == protocol.TypeReply && (p.ParentID != "" || p.ConversationID != "") {
			var root string
			var rootCTO bool
			err = tx.QueryRow(`SELECT sender,sender_cto FROM messages WHERE conversation_id=? AND parent_id='' ORDER BY rowid LIMIT 1`, m.ConversationID).Scan(&root, &rootCTO)
			if err != nil && err != sql.ErrNoRows {
				return msg, false, err
			}
			allowed = root == ctoID || rootCTO
		}
		if !allowed {
			return msg, false, reject("The CTO only takes replies. Ask your owner, or wait for the CTO to ask you.")
		}
	}
	if m.Type == protocol.TypeRequest && !m.FromCTO && m.To != ctoID {
		rule, err := requestRuleTx(tx, m.From, m.To)
		if err != nil {
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
	// The order flag counts only when the sender is above the recipient in the
	// tree. Otherwise the message goes as a request and the sender is told.
	if p.Order && route != nil {
		if note, err := s.checkOrderFlag(tx, &m); err != nil {
			return msg, false, err
		} else if note != "" {
			if *route != "" {
				*route += "\n"
			}
			*route += note
		}
	}
	_, err = tx.Exec(`INSERT INTO messages (id, conversation_id, parent_id, sender, recipient, type, body, hop,
		idem_key, created_at, expires_at, state, sender_cto, owner_approved, worker_task, is_order) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body, m.Hop,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State, m.FromCTO, m.OwnerApproved, workerTask, m.IsOrder)
	if err != nil {
		return msg, false, err
	}
	if m.State == protocol.StateHeld {
		if err := s.openHeldAsk(tx, m.ID, m.From, m.To, m.Body); err != nil {
			return msg, false, err
		}
	}

	if len(p.Files) > 0 {
		raw, err := json.Marshal(p.Files)
		if err != nil {
			return msg, false, err
		}
		if _, err := tx.Exec(`INSERT INTO message_files (message_id, files) VALUES (?,?)`, m.ID, string(raw)); err != nil {
			return msg, false, err
		}
		m.Files = p.Files
	}
	if m.ParentID == "" && !threaded && !m.FromCTO {
		if err := s.signalRequest(tx, m); err != nil {
			return msg, false, err
		}
	}
	if p.Outcome != "" {
		if parent.Type != protocol.TypeRequest {
			desc := parent.Type
			if parent.Type == protocol.TypeNotification && (parent.ApprovalNote != "" || parent.OwnerApproved) {
				desc = "notification (an owner approval notice)"
			}
			return msg, false, reject("%s is a %s, not a request: omit outcome to reply to it.", parent.ID, desc)
		}
		var fromID, toID string
		if err := tx.QueryRow(`SELECT sender,recipient FROM messages WHERE id=? AND type=?`, p.ParentID, protocol.TypeRequest).Scan(&fromID, &toID); err != nil {
			return msg, false, reject("%s is a %s, not a request: omit outcome to reply to it.", parent.ID, parent.Type)
		}
		from, _, _, _, err := sessionIdentityQuery(tx, fromID)
		if err != nil {
			return msg, false, err
		}
		to, _, _, _, err := sessionIdentityQuery(tx, toID)
		if err != nil {
			return msg, false, err
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
		if err := s.storeHandoffCopy(tx, m.ParentID, m); err != nil {
			return msg, false, err
		}
		if _, err := tx.Exec(`UPDATE messages SET state = ? WHERE id = ? AND state IN (?,?,?)`,
			protocol.StateReplied, m.ParentID, protocol.StateStored, protocol.StateDispatched, protocol.StateDelivered); err != nil {
			return msg, false, err
		}
		if _, err := tx.Exec(`DELETE FROM delivery_retries WHERE message_id=? AND EXISTS (SELECT 1 FROM messages WHERE id=? AND state='replied')`, m.ParentID, m.ParentID); err != nil {
			return msg, false, err
		}
	}
	if threaded && m.Type == protocol.TypeReply {
		// A reply to a handed-off request also goes to the lead as a copy.
		var handed string
		err := tx.QueryRow(`SELECT id FROM messages WHERE conversation_id=? AND sender=? AND recipient=? AND type=? AND handed_off_by<>'' ORDER BY rowid DESC LIMIT 1`,
			m.ConversationID, m.To, m.From, protocol.TypeRequest).Scan(&handed)
		if err != nil && err != sql.ErrNoRows {
			return msg, false, err
		}
		if handed != "" {
			if err := s.storeHandoffCopy(tx, handed, m); err != nil {
				return msg, false, err
			}
		}
		// A reply to the conversation answers every open request there from
		// its recipient, as the reminder check already counts it.
		if _, err := tx.Exec(`UPDATE messages SET state = ? WHERE conversation_id = ? AND sender = ? AND recipient = ? AND type = ? AND state IN (?,?,?)`,
			protocol.StateReplied, m.ConversationID, m.To, m.From, protocol.TypeRequest,
			protocol.StateStored, protocol.StateDispatched, protocol.StateDelivered); err != nil {
			return msg, false, err
		}
		if _, err := tx.Exec(`DELETE FROM delivery_retries WHERE message_id IN (SELECT id FROM messages WHERE conversation_id = ? AND sender = ? AND recipient = ? AND type = ? AND state = ?)`,
			m.ConversationID, m.To, m.From, protocol.TypeRequest, protocol.StateReplied); err != nil {
			return msg, false, err
		}
	}
	if _, err := tx.Exec(`UPDATE sessions SET owner_uses=MIN(1000000, (owner_uses >> MIN(30, MAX(0, (? - owner_used_at) / ?))) + 1), owner_used_at=? WHERE id=? AND EXISTS (SELECT 1 FROM sessions WHERE id=? AND harness=?)`, now.UnixNano(), int64(24*time.Hour), now.UnixNano(), m.To, m.From, protocol.OwnerHarness); err != nil {
		return msg, false, err
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
	if err := s.retireObsoleteNotices(); err != nil {
		return nil, err
	}
	if _, err := s.ExpireSessionAgentStarts(); err != nil {
		return nil, err
	}
	states := `m.state = 'stored'`
	if includeDispatched {
		states = `m.state IN ('stored','dispatched')`
	}
	rows, err := s.db.Query(msgSelect+` JOIN sessions r ON r.id = m.recipient
		WHERE NOT ( `+sessionAgentHoldSQL("r")+` ) AND r.node_id = ? AND r.harness <> 'bot' AND r.online = 1 AND `+states+` AND m.expires_at > ?
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
		if needsReminderCheck(m) {
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
	_, _, err := s.ApplyDeliveryReport(id, state, detail)
	return err
}

// ApplyDeliveryReport is ApplyDelivery that also returns the notice to the
// sender, stored in the same transaction, when the report leaves the message
// failed for good (W4.3), or delivers a request to a session that waits for
// its user (decision 0057). The caller dispatches it.
func (s *Store) ApplyDeliveryReport(id, state, detail string) (notice protocol.Message, noticed bool, err error) {
	if state != protocol.StateDelivered && state != protocol.StateFailed && state != protocol.StateDispatched {
		return notice, false, reject("delivery state must be dispatched, delivered or failed")
	}
	now := s.now().UnixNano()
	tx, err := s.db.Begin()
	if err != nil {
		return notice, false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO deliveries (message_id, state, detail, at) VALUES (?,?,?,?)`, id, state, detail, now); err != nil {
		return notice, false, err
	}
	res, err := tx.Exec(`UPDATE messages SET state = ? WHERE id = ? AND state IN ('stored','dispatched')`, state, id)
	if err != nil {
		return notice, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return notice, false, err
	}
	if n == 1 {
		if state == protocol.StateFailed && retryableFailure(detail) {
			var attempts int
			err = tx.QueryRow(`SELECT attempts FROM delivery_retries WHERE message_id=?`, id).Scan(&attempts)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return notice, false, err
			}
			attempts++
			_, err = tx.Exec(`INSERT INTO delivery_retries(message_id,attempts,next_at) VALUES(?,?,?) ON CONFLICT(message_id) DO UPDATE SET attempts=excluded.attempts,next_at=excluded.next_at`, id, attempts, now+int64(retryDelay(attempts)))
		} else if state != protocol.StateDispatched {
			_, err = tx.Exec(`DELETE FROM delivery_retries WHERE message_id=?`, id)
		}
		if err != nil {
			return notice, false, err
		}
		// A failure that is not retried is final: tell the sender once.
		if state == protocol.StateFailed && !retryableFailure(detail) {
			if notice, noticed, err = s.failureNotice(tx, id, detail); err != nil {
				return protocol.Message{}, false, err
			}
		}
		// A request delivered to a session that waits for its user: tell the
		// sender once per wait (decision 0057).
		if state == protocol.StateDelivered {
			if notice, noticed, err = s.waitingNotice(tx, id); err != nil {
				return protocol.Message{}, false, err
			}
		}
	}
	if state == protocol.StateDelivered {
		if err = s.learnDelivery(tx, id, now); err != nil {
			return notice, false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return protocol.Message{}, false, err
	}
	s.Event("msg."+state, "", s.recipientOf(id), id, detail)
	note := ""
	if err == nil {
		if n, _ := res.RowsAffected(); n == 0 {
			note = "state_kept" // already replied, expired or failed
		}
	}
	s.logStep("msg."+state, id, "detail", detail, "note", note)
	if noticed {
		s.logMessage(notice)
	}
	return notice, noticed, err
}

// Expire marks overdue undelivered messages expired and returns how many.
func (s *Store) Expire() (int, error) {
	now := s.now().UnixNano()
	rows, err := s.db.Query(`SELECT id FROM messages WHERE (state IN ('stored','dispatched','held') OR (state='failed' AND EXISTS (SELECT 1 FROM delivery_retries WHERE message_id=messages.id))) AND expires_at <= ? AND NOT EXISTS (SELECT 1 FROM sessions r WHERE r.id=messages.recipient AND r.harness='bot' AND messages.state='stored')`, now)
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
// became a worker, or a difficulty assessment. The list skips these messages
// as rows and as activity, but the worker's conversation still shows them.
func workerNoticeLifecycleSQL(alias string) string {
	return `(` + alias + `.body LIKE '[agent-net worker notice]%' AND (
	instr(` + alias + `.body, char(10)||'status: started'||char(10)) > 0 OR
	instr(` + alias + `.body, char(10)||'status: difficulty'||char(10)) > 0 OR
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
		SELECT m.recipient FROM messages m WHERE COALESCE(m.worker_task, m.body LIKE '[agent-net worker brief]%')
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
	res, err := tx.Exec(`UPDATE messages SET state='expired' WHERE id=? AND (state IN ('stored','dispatched','held') OR (state='failed' AND EXISTS (SELECT 1 FROM delivery_retries WHERE message_id=messages.id))) AND expires_at<=?`, id, now)
	if err != nil {
		return false, err
	}
	count, err := res.RowsAffected()
	if err != nil || count == 0 {
		return false, err
	}
	if _, err = tx.Exec(`DELETE FROM delivery_retries WHERE message_id=?`, id); err != nil {
		return false, err
	}
	var notice protocol.Message
	noticed := false
	if m.State == protocol.StateHeld {
		// The held path answers the sender with its own reply.
		if err = s.heldReply(tx, m, "your request expired while awaiting your user", "expired"); err != nil {
			return false, err
		}
		if err = s.closeHeldAsk(tx, m.ID, protocol.AskReasonExpired); err != nil {
			return false, err
		}
	} else if notice, noticed, err = s.expiryNotice(tx, m); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	if noticed {
		s.logMessage(notice)
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

// workerTaskForSend classifies the message once using the sending daemon's
// version. Development builds support the field; unknown versions are old.
func workerTaskForSend(q queryer, p protocol.MsgSend) (bool, error) {
	if (p.Type != "" && p.Type != protocol.TypeRequest) || p.ParentID != "" {
		return false, nil
	}
	if p.WorkerTask {
		return true, nil
	}
	var version string
	err := q.QueryRow(`SELECT n.version FROM sessions s JOIN nodes n ON n.id=s.node_id WHERE s.id=?`, p.Sender).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if version == "dev" {
		return false, nil
	}
	have, ok := parseReleaseVersion(version)
	want, _ := parseReleaseVersion("0.4.130")
	if ok {
		for i := range want {
			if have[i] > want[i] {
				return false, nil
			}
			if have[i] < want[i] {
				break
			}
			if i == len(want)-1 {
				return false, nil
			}
		}
	}
	return strings.HasPrefix(p.Body, "[agent-net worker brief]"), nil
}

// SetSessionClosed keeps an owner-closed conversation offline until new activity or resume.
func (s *Store) SetSessionClosed(nodeID, sessionID string, closed bool) error {
	if !closed {
		_, err := s.db.Exec(`UPDATE sessions SET closed_at=0 WHERE id=? AND node_id=? AND harness='opencode' AND worker=''`, sessionID, nodeID)
		return err
	}
	_, err := s.db.Exec(`UPDATE sessions SET closed_at=?, online=0, status='ended', harness_lost=0 WHERE id=? AND node_id=? AND harness='opencode' AND worker=''`, s.now().UnixMilli(), sessionID, nodeID)
	return err
}

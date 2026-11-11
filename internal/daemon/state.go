package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/dbfile"
	"github.com/niparis/agent-net/internal/protocol"
	_ "modernc.org/sqlite"
)

// state is the daemon's small local database: delivered message IDs (to
// remove duplicates) and the outbox of sends made while the gateway was away.
type state struct {
	db                 *sql.DB
	proposalDeliveryMu sync.Mutex
	assessmentMu       sync.Mutex
}

func openState(dir string) (*state, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "daemon.db")
	// Private from the start; SQLite gives -wal and -shm the mode of this file.
	if err := dbfile.Create(path); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS notification_sent (session_id TEXT PRIMARY KEY, at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS claude_mouse_notice (key TEXT PRIMARY KEY, at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS settings_snapshot (id INTEGER PRIMARY KEY CHECK(id=1), revision INTEGER NOT NULL, payload TEXT NOT NULL CHECK(json_valid(payload)), received_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS owner_preferences (key TEXT PRIMARY KEY, value INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS cto (id INTEGER PRIMARY KEY CHECK(id=1), session_id TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS cto_network (id INTEGER PRIMARY KEY CHECK(id=1), session_id TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS cto_actions (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, text TEXT NOT NULL, at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS cto_history (id INTEGER PRIMARY KEY AUTOINCREMENT, harness TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL, at INTEGER NOT NULL, payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS cto_proposals (id INTEGER PRIMARY KEY AUTOINCREMENT, tool TEXT NOT NULL, action TEXT NOT NULL, args TEXT NOT NULL, reason TEXT NOT NULL, created_at INTEGER NOT NULL, status TEXT NOT NULL, note TEXT NOT NULL DEFAULT '', result TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS cto_placements (purpose TEXT PRIMARY KEY, placement TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS cto_deliveries (id INTEGER PRIMARY KEY AUTOINCREMENT, sender TEXT NOT NULL, at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS cto_proposal_messages (proposal_id INTEGER NOT NULL, sender TEXT NOT NULL, at INTEGER NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS cto_proposals_open_call ON cto_proposals(tool,action,args) WHERE status = 'open';
CREATE INDEX IF NOT EXISTS cto_proposal_messages_proposal ON cto_proposal_messages(proposal_id);
CREATE INDEX IF NOT EXISTS cto_history_harness ON cto_history(harness,kind,id);
CREATE TABLE IF NOT EXISTS delivered (message_id TEXT PRIMARY KEY, at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS outbox (seq INTEGER PRIMARY KEY AUTOINCREMENT, payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS worker_delivery_risk (worker_id TEXT PRIMARY KEY, risk INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS worker_assessment_outbox (run_id TEXT PRIMARY KEY, payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS worker_idle_notice (worker_id TEXT PRIMARY KEY, since INTEGER NOT NULL, notice_key TEXT NOT NULL, report TEXT NOT NULL DEFAULT '', sent INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS inventory_scan (id INTEGER PRIMARY KEY CHECK(id=1), version TEXT NOT NULL, at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS session_activity (harness TEXT NOT NULL, native_id TEXT NOT NULL, at INTEGER NOT NULL, PRIMARY KEY (harness, native_id));
CREATE TABLE IF NOT EXISTS cleanup_worktrees (
 worker_id TEXT PRIMARY KEY, project TEXT NOT NULL, path TEXT NOT NULL,
 branch TEXT NOT NULL, ended_at INTEGER NOT NULL, harness TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS workers (
	id TEXT PRIMARY KEY, harness TEXT NOT NULL, project TEXT NOT NULL, path TEXT NOT NULL,
	model TEXT NOT NULL DEFAULT '', owner TEXT NOT NULL, owner_name TEXT NOT NULL DEFAULT '', native_id TEXT NOT NULL DEFAULT '',
	session_id TEXT NOT NULL DEFAULT '', pid INTEGER NOT NULL, pgid INTEGER NOT NULL,
	start_token TEXT NOT NULL, started_at INTEGER NOT NULL, last_active INTEGER NOT NULL,
	ended_reason TEXT NOT NULL DEFAULT '',
	main_path TEXT NOT NULL DEFAULT '', worktree TEXT NOT NULL DEFAULT '', branch TEXT NOT NULL DEFAULT '',
	base TEXT NOT NULL DEFAULT '', task_msg TEXT NOT NULL DEFAULT '', done_status TEXT NOT NULL DEFAULT '',
	task_text TEXT NOT NULL DEFAULT '')`)
	if err == nil {
		_, err = db.Exec(queueSchema)
	}
	if err == nil {
		_, err = db.Exec(claudeDeliveriesSchema)
	}
	if err == nil {
		var has int
		err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('cto_proposals') WHERE name = 'note_delivered'`).Scan(&has)
		if err == nil && has == 0 {
			_, err = db.Exec(`ALTER TABLE cto_proposals ADD COLUMN note_delivered INTEGER NOT NULL DEFAULT 0`)
		}
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	var hasCleanupHarness int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('cleanup_worktrees') WHERE name='harness'`).Scan(&hasCleanupHarness); err != nil {
		db.Close()
		return nil, err
	}
	if hasCleanupHarness == 0 {
		if _, err := db.Exec(`ALTER TABLE cleanup_worktrees ADD COLUMN harness TEXT NOT NULL DEFAULT ''`); err != nil {
			db.Close()
			return nil, err
		}
	}
	for _, col := range []string{"retain_until", "dirty_logged_at", "retain_head"} {
		var has int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('cleanup_worktrees') WHERE name=?`, col).Scan(&has); err != nil {
			db.Close()
			return nil, err
		}
		if has == 0 {
			spec := ` INTEGER NOT NULL DEFAULT 0`
			if col == "retain_head" {
				spec = ` TEXT NOT NULL DEFAULT ''`
			}
			if _, err := db.Exec(`ALTER TABLE cleanup_worktrees ADD COLUMN ` + col + spec); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	// harness_ref is what a harness needs to find its worker again: the
	// background session ID of a Claude worker, the address of a Codex one.
	// A table from an older version does not have it.
	var hasRef int
	db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('workers') WHERE name = 'harness_ref'`).Scan(&hasRef)
	if hasRef == 0 {
		if _, err := db.Exec(`ALTER TABLE workers ADD COLUMN harness_ref TEXT NOT NULL DEFAULT ''`); err != nil {
			db.Close()
			return nil, err
		}
	}
	// state is the delivery record of a message (decision 0053). A table
	// from an older version has only delivered messages in it, so the
	// default keeps those rows delivered.
	var hasDeliveryState int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('delivered') WHERE name = 'state'`).Scan(&hasDeliveryState); err != nil {
		db.Close()
		return nil, err
	}
	if hasDeliveryState == 0 {
		if _, err := db.Exec(`ALTER TABLE delivered ADD COLUMN state TEXT NOT NULL DEFAULT 'delivered'`); err != nil {
			db.Close()
			return nil, err
		}
	}
	// task_text is the short task line shown for a worker. resumable is 1 for
	// a worker that ended after its hand-back and whose harness session is
	// kept, so a message from its owner can start it again. reported says the
	// gateway has been told that it ended. A table from an older version does
	// not have these columns.
	for _, col := range []struct{ name, spec string }{
		{"run_generation", `run_generation INTEGER NOT NULL DEFAULT 0`},
		{"task_text", `task_text TEXT NOT NULL DEFAULT ''`},
		{"failure", `failure TEXT NOT NULL DEFAULT ''`},
		{"title", `title TEXT NOT NULL DEFAULT ''`},
		{"resumable", `resumable INTEGER NOT NULL DEFAULT 0`},
		{"reported", `reported INTEGER NOT NULL DEFAULT 0`},
		{"task_full", `task_full TEXT NOT NULL DEFAULT ''`},
		{"permissions", `permissions TEXT NOT NULL DEFAULT ''`},
		{"done_partial", `done_partial INTEGER NOT NULL DEFAULT 0`},
		{"continue_count", `continue_count INTEGER NOT NULL DEFAULT 0`},
		{"fix_count", `fix_count INTEGER NOT NULL DEFAULT 0`},
		{"continue_run", `continue_run INTEGER NOT NULL DEFAULT 0`},
		{"fix_run", `fix_run INTEGER NOT NULL DEFAULT 0`},
		{"pending_handback", `pending_handback INTEGER NOT NULL DEFAULT 0`},
		{"pending_partial", `pending_partial INTEGER NOT NULL DEFAULT 0`},
		{"difficulty_asked", `difficulty_asked TEXT NOT NULL DEFAULT ''`},
		{"difficulty_used", `difficulty_used TEXT NOT NULL DEFAULT ''`},
		{"touches", `touches TEXT NOT NULL DEFAULT ''`},
		{"auto_continue", `auto_continue INTEGER NOT NULL DEFAULT 1`},
		{"auto_continue_count", `auto_continue_count INTEGER NOT NULL DEFAULT 0`},
		{"auto_continue_run", `auto_continue_run INTEGER NOT NULL DEFAULT 0`},
		{"task_id", `task_id TEXT NOT NULL DEFAULT ''`},
		{"midrun_count", `midrun_count INTEGER NOT NULL DEFAULT 0`},
		{"midrun_run", `midrun_run INTEGER NOT NULL DEFAULT 0`},
		{"notice_since_request", `notice_since_request INTEGER NOT NULL DEFAULT 0`},
		{"change_id", `change_id TEXT NOT NULL DEFAULT ''`},
		{"fixes", `fixes TEXT NOT NULL DEFAULT ''`},
		{"run_evidence", `run_evidence TEXT NOT NULL DEFAULT 'null'`},
	} {
		var has int
		db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('workers') WHERE name = ?`, col.name).Scan(&has)
		if has == 0 {
			if _, err := db.Exec(`ALTER TABLE workers ADD COLUMN ` + col.spec); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	var hasMidrunSeen int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('workers') WHERE name='midrun_seen'`).Scan(&hasMidrunSeen); err != nil {
		db.Close()
		return nil, err
	}
	if hasMidrunSeen == 0 {
		if _, err := db.Exec(`ALTER TABLE workers ADD COLUMN midrun_seen INTEGER NOT NULL DEFAULT 0`); err != nil {
			db.Close()
			return nil, err
		}
		if _, err := db.Exec(`UPDATE workers SET midrun_seen=1 WHERE task_msg<>''`); err != nil {
			db.Close()
			return nil, err
		}
	}
	var hasWorkerOwnerName int
	db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('workers') WHERE name = 'owner_name'`).Scan(&hasWorkerOwnerName)
	if hasWorkerOwnerName == 0 {
		if _, err := db.Exec(`ALTER TABLE workers ADD COLUMN owner_name TEXT NOT NULL DEFAULT ''`); err != nil {
			db.Close()
			return nil, err
		}
	}
	// base and record_task keep a continued or based start in the queue. A
	// queue table from an older version does not have them.
	for _, col := range []string{"base", "record_task", "title", "permissions", "difficulty_asked", "difficulty_used", "touches", "task_id", "assessment", "change_id", "mcp_servers", "fixes", "resume_session"} {
		var has int
		db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_queue') WHERE name = ?`, col).Scan(&has)
		if has == 0 {
			if _, err := db.Exec(`ALTER TABLE worker_queue ADD COLUMN ` + col + ` TEXT NOT NULL DEFAULT ''`); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	var hasContinuationPartial int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_queue') WHERE name='continuation_partial'`).Scan(&hasContinuationPartial); err != nil {
		db.Close()
		return nil, err
	}
	if hasContinuationPartial == 0 {
		if _, err := db.Exec(`ALTER TABLE worker_queue ADD COLUMN continuation_partial INTEGER NOT NULL DEFAULT 0`); err != nil {
			db.Close()
			return nil, err
		}
	}
	var hasCTO int
	db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_queue') WHERE name = 'sender_cto'`).Scan(&hasCTO)
	if hasCTO == 0 {
		if _, err := db.Exec(`ALTER TABLE worker_queue ADD COLUMN sender_cto INTEGER NOT NULL DEFAULT 0`); err != nil {
			db.Close()
			return nil, err
		}
	}
	var hasAutoContinue int
	db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_queue') WHERE name = 'auto_continue'`).Scan(&hasAutoContinue)
	if hasAutoContinue == 0 {
		if _, err := db.Exec(`ALTER TABLE worker_queue ADD COLUMN auto_continue INTEGER NOT NULL DEFAULT 1`); err != nil {
			db.Close()
			return nil, err
		}
	}
	var hasOwnerName int
	db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_queue') WHERE name = 'owner_name'`).Scan(&hasOwnerName)
	if hasOwnerName == 0 {
		if _, err := db.Exec(`ALTER TABLE worker_queue ADD COLUMN owner_name TEXT NOT NULL DEFAULT ''`); err != nil {
			db.Close()
			return nil, err
		}
	}
	var hasCTOGraphAfter int
	db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('cto') WHERE name = 'graph_after'`).Scan(&hasCTOGraphAfter)
	if hasCTOGraphAfter == 0 {
		if _, err := db.Exec(`ALTER TABLE cto ADD COLUMN graph_after INTEGER NOT NULL DEFAULT 0`); err != nil {
			db.Close()
			return nil, err
		}
	}
	for _, col := range []struct{ table, name, spec string }{
		{"cto", "claude_session_id", "claude_session_id TEXT NOT NULL DEFAULT ''"},
		{"cto", "claude_graph_after", "claude_graph_after INTEGER NOT NULL DEFAULT 0"},
		{"cto", "active_harness", "active_harness TEXT NOT NULL DEFAULT 'opencode'"},
		{"cto", "opencode_model", "opencode_model TEXT NOT NULL DEFAULT ''"},
		{"cto", "claude_model", "claude_model TEXT NOT NULL DEFAULT ''"},
		{"cto", "models_migrated", "models_migrated INTEGER NOT NULL DEFAULT 0"},
		{"cto_actions", "harness", "harness TEXT NOT NULL DEFAULT 'opencode'"},
	} {
		var has int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, col.table, col.name).Scan(&has); err != nil {
			db.Close()
			return nil, err
		}
		if has == 0 {
			if _, err := db.Exec(`ALTER TABLE ` + col.table + ` ADD COLUMN ` + col.spec); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	var hasOpenCodeLastModel int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('cto') WHERE name = 'opencode_last_model'`).Scan(&hasOpenCodeLastModel); err != nil {
		db.Close()
		return nil, err
	}
	if hasOpenCodeLastModel == 0 {
		if _, err := db.Exec(`ALTER TABLE cto ADD COLUMN opencode_last_model TEXT NOT NULL DEFAULT ''`); err != nil {
			db.Close()
			return nil, err
		}
		// Before per-harness model settings, opencode_model remembered the
		// model last applied to the live OpenCode conversation.
		if _, err := db.Exec(`UPDATE cto SET opencode_last_model=opencode_model`); err != nil {
			db.Close()
			return nil, err
		}
	}
	// Fix files that an older version made with the default mode.
	if err := dbfile.Secure(path); err != nil {
		db.Close()
		return nil, err
	}
	return &state{db: db}, nil
}

func (s *state) close() error { return s.db.Close() }

// mouseNoticeAt is when the Claude mouse problem key was last reported.
func (s *state) mouseNoticeAt(key string) (time.Time, bool, error) {
	var at int64
	err := s.db.QueryRow(`SELECT at FROM claude_mouse_notice WHERE key=?`, key).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return time.Unix(at, 0), true, nil
}

// setMouseNotice records the report across daemon restarts.
func (s *state) setMouseNotice(key string, at time.Time) error {
	_, err := s.db.Exec(`INSERT INTO claude_mouse_notice(key,at) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET at=excluded.at`, key, at.Unix())
	return err
}

// inventoryScanVersion is the agent-net version that produced the last
// inventory scan on this machine. An upgrade changes it, which forces a fresh
// scan so new observation fields (such as file manifests) appear.
func (s *state) inventoryScanVersion() string {
	var version string
	if err := s.db.QueryRow(`SELECT version FROM inventory_scan WHERE id=1`).Scan(&version); err != nil {
		return ""
	}
	return version
}

func (s *state) setInventoryScanVersion(version string) error {
	_, err := s.db.Exec(`INSERT INTO inventory_scan(id,version,at) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET version=excluded.version, at=excluded.at`, version, time.Now().Unix())
	return err
}

// sessionActivity loads when each known session last did something, keyed by
// harness and native ID. It survives a daemon restart: a restart must not make
// an idle session look active.
func (s *state) sessionActivity() (map[string]time.Time, error) {
	rows, err := s.db.Query(`SELECT harness, native_id, at FROM session_activity`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var harness, native string
		var at int64
		if err := rows.Scan(&harness, &native, &at); err != nil {
			return nil, err
		}
		out[key(harness, native)] = time.Unix(0, at)
	}
	return out, rows.Err()
}

// saveSessionActivity keeps the newer of the stored and given time.
func (s *state) saveSessionActivity(harness, native string, at time.Time) error {
	_, err := s.db.Exec(`INSERT INTO session_activity (harness, native_id, at) VALUES (?,?,?)
		ON CONFLICT (harness, native_id) DO UPDATE SET at=MAX(session_activity.at, excluded.at)`,
		harness, native, at.UnixNano())
	return err
}

// Delivery record states, one row per message ID in the delivered table
// (decision 0053). A record moves from injecting to one of the other three;
// delivered is final.
const (
	// deliveryInjecting is written before the write to the harness. A record
	// still in this state after a restart means the daemon stopped during
	// the write: the message may have arrived.
	deliveryInjecting = "injecting"
	// deliveryDelivered: the harness confirmed the message.
	deliveryDelivered = "delivered"
	// deliveryUnconfirmed: the write started but failed or was not
	// confirmed. The message may have arrived, so it is not written again.
	deliveryUnconfirmed = "unconfirmed"
	// deliveryFailed: a known failure before anything reached the harness.
	// A later push may try again.
	deliveryFailed = "failed"
)

// The details sent to the gateway when a push finds a record that may have
// reached the harness already. The gateway shows them with state failed.
const (
	detailStoppedWhileDelivering = "may have arrived: the daemon stopped while delivering"
	detailEarlierUnconfirmed     = "may have arrived: an earlier write failed after connecting"
)

func (s *state) isDelivered(id string) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM delivered WHERE message_id = ? AND state = ?`, id, deliveryDelivered).Scan(&n)
	return n > 0
}

// deliveryState returns the delivery record of a message, if there is one.
func (s *state) deliveryState(id string) (string, bool) {
	st, ok, _ := s.readDeliveryState(id)
	return st, ok
}

func (s *state) readDeliveryState(id string) (string, bool, error) {
	return s.readDeliveryStateCtx(context.Background(), id)
}

func (s *state) readDeliveryStateCtx(ctx context.Context, id string) (string, bool, error) {
	var st string
	err := s.db.QueryRowContext(ctx, `SELECT state FROM delivered WHERE message_id = ?`, id).Scan(&st)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return st, err == nil, err
}

// setDeliveryState writes the record of a message. A delivered record is
// never changed.
func (s *state) setDeliveryState(ctx context.Context, id, st string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO delivered (message_id, at, state) VALUES (?,?,?)
		ON CONFLICT (message_id) DO UPDATE SET state=excluded.state, at=excluded.at WHERE delivered.state != ?`,
		id, time.Now().Unix(), st, deliveryDelivered)
	return err
}

// beginDelivery records that the daemon is about to write the message to
// its harness. It must be called, and succeed, before that write.
func (s *state) beginDelivery(id string) error {
	return s.setDeliveryState(context.Background(), id, deliveryInjecting)
}

func (s *state) markDelivered(id string) error {
	return s.markDeliveredCtx(context.Background(), id)
}

func (s *state) markDeliveredCtx(ctx context.Context, id string) error {
	return s.setDeliveryState(ctx, id, deliveryDelivered)
}

func (s *state) markDeliveryFailed(id string) error {
	return s.setDeliveryState(context.Background(), id, deliveryFailed)
}

func (s *state) markDeliveryUnconfirmed(id string) error {
	return s.setDeliveryState(context.Background(), id, deliveryUnconfirmed)
}

// clearDelivery removes an injecting record when the adapter only queued the
// message in memory: nothing reached the harness, and a push after a restart
// must deliver it.
func (s *state) clearDelivery(id string) error {
	_, err := s.db.Exec(`DELETE FROM delivered WHERE message_id = ? AND state = ?`, id, deliveryInjecting)
	return err
}

// deliveredMargin is how long a delivery record outlives its message. The
// record is a duplicate check, but keeping it past the message expiry means a
// message that is still alive can never be accepted twice.
const deliveredMargin = 7 * 24 * time.Hour

// pruneDelivered deletes delivery records, in any state, older than the
// message expiry plus deliveredMargin. It returns how many rows went.
func (s *state) pruneDelivered(now time.Time) (int64, error) {
	cutoff := now.Add(-(protocol.DefaultTTL + deliveredMargin)).Unix()
	res, err := s.db.Exec(`DELETE FROM delivered WHERE at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *state) enqueue(payload []byte) error {
	_, err := s.db.Exec(`INSERT INTO outbox (payload) VALUES (?)`, string(payload))
	return err
}

func (s *state) outboxLen() int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM outbox`).Scan(&n)
	return n
}

type queued struct {
	seq int64
	msg protocol.MsgSend
}

func (s *state) outbox() ([]queued, error) {
	rows, err := s.db.Query(`SELECT seq, payload FROM outbox ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []queued
	for rows.Next() {
		var q queued
		var p string
		if err := rows.Scan(&q.seq, &p); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(p), &q.msg) == nil {
			out = append(out, q)
		}
	}
	return out, rows.Err()
}

func (s *state) dequeue(seq int64) error {
	_, err := s.db.Exec(`DELETE FROM outbox WHERE seq = ?`, seq)
	return err
}

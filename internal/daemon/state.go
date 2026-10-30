package daemon

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/niparis/agent-net/internal/dbfile"
	"github.com/niparis/agent-net/internal/protocol"
	_ "modernc.org/sqlite"
)

// state is the daemon's small local database: delivered message IDs (to
// remove duplicates) and the outbox of sends made while the gateway was away.
type state struct{ db *sql.DB }

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
CREATE TABLE IF NOT EXISTS cto (id INTEGER PRIMARY KEY CHECK(id=1), session_id TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS cto_actions (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, text TEXT NOT NULL, at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS delivered (message_id TEXT PRIMARY KEY, at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS outbox (seq INTEGER PRIMARY KEY AUTOINCREMENT, payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS workers (
	id TEXT PRIMARY KEY, harness TEXT NOT NULL, project TEXT NOT NULL, path TEXT NOT NULL,
	model TEXT NOT NULL DEFAULT '', owner TEXT NOT NULL, native_id TEXT NOT NULL DEFAULT '',
	session_id TEXT NOT NULL DEFAULT '', pid INTEGER NOT NULL, pgid INTEGER NOT NULL,
	start_token TEXT NOT NULL, started_at INTEGER NOT NULL, last_active INTEGER NOT NULL,
	ended_reason TEXT NOT NULL DEFAULT '',
	main_path TEXT NOT NULL DEFAULT '', worktree TEXT NOT NULL DEFAULT '', branch TEXT NOT NULL DEFAULT '',
	base TEXT NOT NULL DEFAULT '', task_msg TEXT NOT NULL DEFAULT '', done_status TEXT NOT NULL DEFAULT '',
	task_text TEXT NOT NULL DEFAULT '')`)
	if err == nil {
		_, err = db.Exec(queueSchema)
	}
	if err != nil {
		db.Close()
		return nil, err
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
	// task_text is the short task line shown for a worker. resumable is 1 for
	// a worker that ended after its hand-back and whose harness session is
	// kept, so a message from its owner can start it again. reported says the
	// gateway has been told that it ended. A table from an older version does
	// not have these columns.
	for _, col := range []struct{ name, spec string }{
		{"task_text", `task_text TEXT NOT NULL DEFAULT ''`},
		{"resumable", `resumable INTEGER NOT NULL DEFAULT 0`},
		{"reported", `reported INTEGER NOT NULL DEFAULT 0`},
		{"task_full", `task_full TEXT NOT NULL DEFAULT ''`},
		{"permissions", `permissions TEXT NOT NULL DEFAULT ''`},
		{"done_partial", `done_partial INTEGER NOT NULL DEFAULT 0`},
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
	// base and record_task keep a continued or based start in the queue. A
	// queue table from an older version does not have them.
	for _, col := range []string{"base", "record_task", "permissions"} {
		var has int
		db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_queue') WHERE name = ?`, col).Scan(&has)
		if has == 0 {
			if _, err := db.Exec(`ALTER TABLE worker_queue ADD COLUMN ` + col + ` TEXT NOT NULL DEFAULT ''`); err != nil {
				db.Close()
				return nil, err
			}
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
	var hasCTOGraphAfter int
	db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('cto') WHERE name = 'graph_after'`).Scan(&hasCTOGraphAfter)
	if hasCTOGraphAfter == 0 {
		if _, err := db.Exec(`ALTER TABLE cto ADD COLUMN graph_after INTEGER NOT NULL DEFAULT 0`); err != nil {
			db.Close()
			return nil, err
		}
	}
	// Fix files that an older version made with the default mode.
	if err := dbfile.Secure(path); err != nil {
		db.Close()
		return nil, err
	}
	return &state{db}, nil
}

func (s *state) close() error { return s.db.Close() }

func (s *state) isDelivered(id string) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM delivered WHERE message_id = ?`, id).Scan(&n)
	return n > 0
}

func (s *state) markDelivered(id string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO delivered (message_id, at) VALUES (?,?)`, id, time.Now().Unix())
	return err
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

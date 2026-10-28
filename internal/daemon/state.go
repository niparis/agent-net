package daemon

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/niparis/a2a-ocs/internal/protocol"
	_ "modernc.org/sqlite"
)

// state is the daemon's small local database: delivered message IDs (to
// remove duplicates) and the outbox of sends made while the gateway was away.
type state struct{ db *sql.DB }

func openState(dir string) (*state, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.Join(dir, "daemon.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS delivered (message_id TEXT PRIMARY KEY, at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS outbox (seq INTEGER PRIMARY KEY AUTOINCREMENT, payload TEXT NOT NULL)`)
	if err != nil {
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

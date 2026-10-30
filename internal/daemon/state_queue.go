package daemon

import (
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The queue of worker starts that wait for room, and the costs of ended
// workers. Both live in daemon.db, so a restart keeps them (decision 0018).

const queueSchema = `
CREATE TABLE IF NOT EXISTS worker_queue (
	seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, owner TEXT NOT NULL,
	project TEXT NOT NULL, path TEXT NOT NULL, harness TEXT NOT NULL, model TEXT NOT NULL DEFAULT '',
	task TEXT NOT NULL, isolation TEXT NOT NULL DEFAULT '', queued_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS worker_costs (
	id INTEGER PRIMARY KEY AUTOINCREMENT, harness TEXT NOT NULL, model TEXT NOT NULL DEFAULT '',
	ended_at INTEGER NOT NULL, avg_cpu REAL NOT NULL, peak_cpu REAL NOT NULL, avg_rss REAL NOT NULL, peak_rss REAL NOT NULL)`

// queuedStart is a worker start that waits for room.
type queuedStart struct {
	ID, Owner, Project, Path, Harness, Model, Task, Isolation string
	// Base is the branch to start the worktree from; RecordTask is what the
	// worker record keeps as the first task (both empty for a plain start).
	Base, RecordTask string
	QueuedAt         time.Time
}

func (s *state) addQueued(q queuedStart) error {
	_, err := s.db.Exec(`INSERT INTO worker_queue (id, owner, project, path, harness, model, task, isolation, queued_at, base, record_task)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`, q.ID, q.Owner, q.Project, q.Path, q.Harness, q.Model, q.Task, q.Isolation, q.QueuedAt.UnixNano(), q.Base, q.RecordTask)
	return err
}

// queue lists the waiting starts, the oldest first.
func (s *state) queue() ([]queuedStart, error) {
	rows, err := s.db.Query(`SELECT id, owner, project, path, harness, model, task, isolation, queued_at, base, record_task FROM worker_queue ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []queuedStart
	for rows.Next() {
		var q queuedStart
		var at int64
		if err := rows.Scan(&q.ID, &q.Owner, &q.Project, &q.Path, &q.Harness, &q.Model, &q.Task, &q.Isolation, &at, &q.Base, &q.RecordTask); err != nil {
			return nil, err
		}
		q.QueuedAt = time.Unix(0, at)
		out = append(out, q)
	}
	return out, rows.Err()
}

func (s *state) queueLen() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_queue`).Scan(&n)
	return n, err
}

// removeQueued deletes a start from the queue and says whether it was there.
func (s *state) removeQueued(id string) bool {
	res, err := s.db.Exec(`DELETE FROM worker_queue WHERE id = ?`, id)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// workerCost is the measured cost of one ended worker.
type workerCost struct {
	Harness, Model  string
	AvgCPU, PeakCPU float64 // percent of one core
	AvgRSS, PeakRSS float64 // bytes
	EndedAt         time.Time
}

// addCost stores the cost of an ended worker and keeps the newest
// protocol.WorkerCostKeep of its harness and model.
func (s *state) addCost(c workerCost) error {
	if _, err := s.db.Exec(`INSERT INTO worker_costs (harness, model, ended_at, avg_cpu, peak_cpu, avg_rss, peak_rss)
		VALUES (?,?,?,?,?,?,?)`, c.Harness, c.Model, c.EndedAt.UnixNano(), c.AvgCPU, c.PeakCPU, c.AvgRSS, c.PeakRSS); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM worker_costs WHERE harness = ? AND model = ? AND id NOT IN
		(SELECT id FROM worker_costs WHERE harness = ? AND model = ? ORDER BY id DESC LIMIT ?)`,
		c.Harness, c.Model, c.Harness, c.Model, protocol.WorkerCostKeep)
	return err
}

// costs lists the stored costs of a harness and model, or of all when both are
// empty.
func (s *state) costs(harness, model string) ([]workerCost, error) {
	q := `SELECT harness, model, ended_at, avg_cpu, peak_cpu, avg_rss, peak_rss FROM worker_costs`
	var args []any
	if harness != "" {
		q += ` WHERE harness = ? AND model = ?`
		args = append(args, harness, model)
	}
	rows, err := s.db.Query(q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []workerCost
	for rows.Next() {
		var c workerCost
		var at int64
		if err := rows.Scan(&c.Harness, &c.Model, &at, &c.AvgCPU, &c.PeakCPU, &c.AvgRSS, &c.PeakRSS); err != nil {
			return nil, err
		}
		c.EndedAt = time.Unix(0, at)
		out = append(out, c)
	}
	return out, rows.Err()
}

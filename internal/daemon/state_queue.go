package daemon

import (
	"encoding/json"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The queue of worker starts that wait for room, and the costs of ended
// workers. Both live in daemon.db, so a restart keeps them (decision 0018).

const queueSchema = `
CREATE TABLE IF NOT EXISTS worker_queue (
	seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, owner TEXT NOT NULL,
	owner_name TEXT NOT NULL DEFAULT '',
	project TEXT NOT NULL, path TEXT NOT NULL, harness TEXT NOT NULL, model TEXT NOT NULL DEFAULT '',
	task TEXT NOT NULL, isolation TEXT NOT NULL DEFAULT '', queued_at INTEGER NOT NULL, assessment TEXT NOT NULL DEFAULT '', resume_session TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS worker_costs (
	id INTEGER PRIMARY KEY AUTOINCREMENT, harness TEXT NOT NULL, model TEXT NOT NULL DEFAULT '',
	ended_at INTEGER NOT NULL, avg_cpu REAL NOT NULL, peak_cpu REAL NOT NULL, avg_rss REAL NOT NULL, peak_rss REAL NOT NULL)`

// queuedStart is a worker start that waits for room.
type queuedStart struct {
	ID, Owner, Project, Path, Harness, Model, Task, Title, Isolation string
	// OwnerName is the caller's display name, for the commit trailers.
	OwnerName string
	// Base is the branch to start the worktree from; RecordTask is what the
	// worker record keeps as the first task (both empty for a plain start).
	Base, RecordTask string
	// Permissions is the worker permission level of the project at queue time.
	Permissions string
	// DifficultyAsked and DifficultyUsed are the levels of the start.
	DifficultyAsked, DifficultyUsed string
	Touches                         string
	TaskID                          string
	ContinuationPartial             bool
	FromCTO                         bool
	// ChangeID is the delivery change a queued continue carries.
	ChangeID string
	// Fixes is the shipped change the start says it fixes.
	Fixes string
	// MCPServers is the start's "approve" or "disable" for a Claude worker.
	MCPServers string
	// Assessment is the judge result made when the start was requested. It
	// keeps the level through the queue.
	Assessment *protocol.WorkerAssessment
	// AutoContinue is the start's automatic-continue setting.
	AutoContinue bool
	QueuedAt     time.Time
	// ResumeSession is set when this queued entry restarts an ended worker session.
	ResumeSession string
}

func (s *state) addQueued(q queuedStart) error {
	assessment := ""
	if q.Assessment != nil {
		if raw, err := json.Marshal(q.Assessment); err == nil {
			assessment = string(raw)
		}
	}
	_, err := s.db.Exec(`INSERT INTO worker_queue (id, owner, owner_name, project, path, harness, model, task, title, isolation, queued_at, base, record_task, permissions, sender_cto, difficulty_asked, difficulty_used, touches, auto_continue, task_id, continuation_partial, assessment, change_id, mcp_servers, fixes, resume_session)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, q.ID, q.Owner, q.OwnerName, q.Project, q.Path, q.Harness, q.Model, q.Task, q.Title, q.Isolation, q.QueuedAt.UnixNano(), q.Base, q.RecordTask, q.Permissions, q.FromCTO, q.DifficultyAsked, q.DifficultyUsed, q.Touches, boolInt(q.AutoContinue), q.TaskID, boolInt(q.ContinuationPartial), assessment, q.ChangeID, q.MCPServers, q.Fixes, q.ResumeSession)
	return err
}

// queue lists the waiting starts, the oldest first.
func (s *state) queue() ([]queuedStart, error) {
	rows, err := s.db.Query(`SELECT id, owner, owner_name, project, path, harness, model, task, title, isolation, queued_at, base, record_task, permissions, sender_cto, difficulty_asked, difficulty_used, touches, auto_continue, task_id, continuation_partial, assessment, change_id, mcp_servers, fixes, resume_session FROM worker_queue ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []queuedStart
	for rows.Next() {
		var q queuedStart
		var at int64
		var autoContinue int
		var assessment string
		if err := rows.Scan(&q.ID, &q.Owner, &q.OwnerName, &q.Project, &q.Path, &q.Harness, &q.Model, &q.Task, &q.Title, &q.Isolation, &at, &q.Base, &q.RecordTask, &q.Permissions, &q.FromCTO, &q.DifficultyAsked, &q.DifficultyUsed, &q.Touches, &autoContinue, &q.TaskID, &q.ContinuationPartial, &assessment, &q.ChangeID, &q.MCPServers, &q.Fixes, &q.ResumeSession); err != nil {
			return nil, err
		}
		q.QueuedAt = time.Unix(0, at)
		q.AutoContinue = autoContinue == 1
		if assessment != "" {
			var a protocol.WorkerAssessment
			if err := json.Unmarshal([]byte(assessment), &a); err != nil {
				return nil, err
			}
			q.Assessment = &a
		}
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

// removeQueuedForSession deletes any queued start restarting the given worker session.
func (s *state) removeQueuedForSession(sessionID string) bool {
	res, err := s.db.Exec(`DELETE FROM worker_queue WHERE resume_session = ?`, sessionID)
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
func (s *state) addCost(c workerCost, limits ...int) error {
	keep := protocol.WorkerCostKeep
	if len(limits) > 0 && limits[0] > 0 {
		keep = limits[0]
	}
	if _, err := s.db.Exec(`INSERT INTO worker_costs (harness, model, ended_at, avg_cpu, peak_cpu, avg_rss, peak_rss)
		VALUES (?,?,?,?,?,?,?)`, c.Harness, c.Model, c.EndedAt.UnixNano(), c.AvgCPU, c.PeakCPU, c.AvgRSS, c.PeakRSS); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM worker_costs WHERE harness = ? AND model = ? AND id NOT IN
		(SELECT id FROM worker_costs WHERE harness = ? AND model = ? ORDER BY id DESC LIMIT ?)`,
		c.Harness, c.Model, c.Harness, c.Model, keep)
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

package main

import (
	"database/sql"
	"strings"
)

// briefPrefix starts every brief message the gateway sends to a worker.
const briefPrefix = "[agent-net worker brief]"

// taskMarker ends the fixed preamble. The caller's task text follows it, as
// written. That text is what protocol.CheckWorkerBrief saw at start time.
const taskMarker = "\n\nTask:\n"

// brief is one past brief, sent to one worker session.
type brief struct {
	ID      string
	Session string
	Sender  string
	SentAt  int64 // unix nanoseconds, as stored in messages.created_at
	Task    string
}

// runOutcome is what the runs of one worker session did, summed over its runs.
type runOutcome struct {
	Runs       int
	FixRounds  int
	Continues  int
	EarlyQuit  bool
	Final      string // outcome of the last run
	Difficulty string // difficulty_used of the last run that has one
	Complete   sql.NullInt64
	Struggle   int
}

// outcome is what happened to one brief's worker, joined by session id.
type outcome struct {
	Launched    bool
	Runs        int
	FixRounds   int
	Continues   int
	EarlyQuit   bool
	Final       string
	Difficulty  string
	Complete    sql.NullInt64
	Struggle    int
	Rated       bool
	Score       int
	Defects     int
	MaxSeverity int
}

// NotComplete is true for a launched brief whose last run did not finish.
func (o outcome) NotComplete() bool { return o.Launched && o.Final != "complete" }

// LowRating is true for a rated brief with a score of 2 or less.
func (o outcome) LowRating() bool { return o.Rated && o.Score <= 2 }

// Bad is true when any sign of a bad outcome is present: the last run did not
// complete, an early quit, a fix round, a low rating, or a defect found later.
func (o outcome) Bad() bool {
	return o.Launched && (o.NotComplete() || o.EarlyQuit || o.FixRounds > 0 || o.LowRating() || o.Defects > 0)
}

// briefTask returns the caller's task text from a brief message. It reports
// false when the message has no task marker.
func briefTask(body string) (string, bool) {
	i := strings.Index(body, taskMarker)
	if i < 0 {
		return "", false
	}
	return body[i+len(taskMarker):], true
}

// loadBriefs reads every brief message in send order. A message without the
// task marker is counted and left out, so it cannot be replayed.
func loadBriefs(db *sql.DB) (briefs []brief, noMarker int, err error) {
	rows, err := db.Query(`SELECT id, sender, recipient, created_at, body FROM messages
		WHERE body LIKE ? ORDER BY created_at, id`, briefPrefix+"%")
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var b brief
		var body string
		if err := rows.Scan(&b.ID, &b.Sender, &b.Session, &b.SentAt, &body); err != nil {
			return nil, 0, err
		}
		task, ok := briefTask(body)
		if !ok {
			noMarker++
			continue
		}
		b.Task = task
		briefs = append(briefs, b)
	}
	return briefs, noMarker, rows.Err()
}

// loadRuns sums the runs of each worker session.
func loadRuns(db *sql.DB) (map[string]*runOutcome, error) {
	rows, err := db.Query(`SELECT session_id, outcome, early_quit, fix_count, continue_count,
		struggle_failed + struggle_repeats + struggle_retries + struggle_exploration + struggle_env,
		worker_complete, difficulty_used
		FROM worker_runs WHERE session_id <> '' ORDER BY session_id, ended_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*runOutcome{}
	for rows.Next() {
		var session, result, difficulty string
		var early, fix, cont, struggle int
		var complete sql.NullInt64
		if err := rows.Scan(&session, &result, &early, &fix, &cont, &struggle, &complete, &difficulty); err != nil {
			return nil, err
		}
		r := out[session]
		if r == nil {
			r = &runOutcome{}
			out[session] = r
		}
		r.Runs++
		r.FixRounds += fix
		r.Continues += cont
		r.Struggle += struggle
		r.EarlyQuit = r.EarlyQuit || early != 0
		r.Final = result
		if difficulty != "" {
			r.Difficulty = difficulty
		}
		if complete.Valid {
			r.Complete = complete
		}
	}
	return out, rows.Err()
}

// loadRatings reads the score of each rated worker session.
func loadRatings(db *sql.DB) (map[string]int, error) {
	rows, err := db.Query(`SELECT session_id, score FROM worker_ratings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var session string
		var score int
		if err := rows.Scan(&session, &score); err != nil {
			return nil, err
		}
		out[session] = score
	}
	return out, rows.Err()
}

// defectCount is the number of defects found later for one worker session.
type defectCount struct {
	Count       int
	MaxSeverity int
}

// loadDefects counts the defects recorded against each worker session.
func loadDefects(db *sql.DB) (map[string]defectCount, error) {
	rows, err := db.Query(`SELECT session_id, COUNT(*), MAX(severity) FROM worker_defects
		WHERE session_id <> '' GROUP BY session_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]defectCount{}
	for rows.Next() {
		var session string
		var d defectCount
		if err := rows.Scan(&session, &d.Count, &d.MaxSeverity); err != nil {
			return nil, err
		}
		out[session] = d
	}
	return out, rows.Err()
}

// joinOutcome builds the outcome of one brief from its session's runs, rating
// and defects. A session with no runs was never launched.
func joinOutcome(session string, runs map[string]*runOutcome, ratings map[string]int, defects map[string]defectCount) outcome {
	var o outcome
	if r := runs[session]; r != nil {
		o.Launched = true
		o.Runs = r.Runs
		o.FixRounds = r.FixRounds
		o.Continues = r.Continues
		o.EarlyQuit = r.EarlyQuit
		o.Final = r.Final
		o.Difficulty = r.Difficulty
		o.Complete = r.Complete
		o.Struggle = r.Struggle
	}
	if score, ok := ratings[session]; ok {
		o.Rated = true
		o.Score = score
	}
	if d, ok := defects[session]; ok {
		o.Defects = d.Count
		o.MaxSeverity = d.MaxSeverity
	}
	return o
}

package main

import (
	"database/sql"
	"strings"
)

// scored is one brief with the rules it fails and its outcome.
type scored struct {
	brief
	Outcome  outcome
	Strict   []string // rules the live check fails
	Loose    []string // rules still failed under the candidate looser matchers
	Unmapped []string // live check failure lines that match no rule
}

// rejectionStats summarises the refusals already in worker_brief_rejections.
// That table keeps the failure lines but not the brief text, so these refusals
// cannot be replayed. They are a cross-check on the rules.
type rejectionStats struct {
	Rows     int
	ByRule   map[string]int
	Unmapped int
}

// replay is the whole result of one run over a database copy.
type replay struct {
	Briefs     []scored
	NoMarker   int
	Runs       int // worker sessions with at least one run
	Rated      int // worker sessions with a rating
	Defected   int // worker sessions with at least one defect
	Rejections rejectionStats
}

// runReplay reads the briefs and their outcomes and runs the live brief check
// on each brief. It never writes to the database.
func runReplay(db *sql.DB) (*replay, error) {
	briefs, noMarker, err := loadBriefs(db)
	if err != nil {
		return nil, err
	}
	runs, err := loadRuns(db)
	if err != nil {
		return nil, err
	}
	ratings, err := loadRatings(db)
	if err != nil {
		return nil, err
	}
	defects, err := loadDefects(db)
	if err != nil {
		return nil, err
	}
	rej, err := loadRejections(db)
	if err != nil {
		return nil, err
	}
	out := &replay{NoMarker: noMarker, Runs: len(runs), Rated: len(ratings), Defected: len(defects), Rejections: rej}
	for _, b := range briefs {
		strict, unmapped := strictRules(b.Task)
		out.Briefs = append(out.Briefs, scored{
			brief:    b,
			Outcome:  joinOutcome(b.Session, runs, ratings, defects),
			Strict:   strict,
			Loose:    looseRules(b.Task, strict),
			Unmapped: unmapped,
		})
	}
	return out, nil
}

// loadRejections counts the failure lines of the refusals already stored.
func loadRejections(db *sql.DB) (rejectionStats, error) {
	st := rejectionStats{ByRule: map[string]int{}}
	rows, err := db.Query(`SELECT failures FROM worker_brief_rejections ORDER BY id`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var failures string
		if err := rows.Scan(&failures); err != nil {
			return st, err
		}
		st.Rows++
		failed, unmapped := failedRules(strings.Split(failures, "\n"))
		for _, r := range failed {
			st.ByRule[r]++
		}
		st.Unmapped += len(unmapped)
	}
	return st, rows.Err()
}

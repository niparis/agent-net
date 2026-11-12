package gateway

import (
	"fmt"
	"net/http"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// ScoresWeekly returns the last 8 ISO weeks of worker scores and release markers.
func (s *Store) ScoresWeekly() ([]protocol.ScoresWeeklyRow, error) {
	return s.ScoresWeeklyAt(s.now())
}

func (g *Gateway) handleScoresWeekly(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.nodeAuthed(r); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	out, err := g.store.ScoresWeekly()
	writeJSON(w, out, err)
}

// isoWeekKey is the ISO week of a Unix second in UTC, in the form the table
// shows. It is computed in Go: SQLite's %G and %V need SQLite 3.46 or newer, so
// a read that passes the week through SQL depends on the driver's version.
func isoWeekKey(unix int64) string {
	year, week := time.Unix(unix, 0).UTC().ISOWeek()
	return fmt.Sprintf("%04d-W%02d", year, week)
}

// weekBuckets is one row's running sums: runs, early quits, fix rounds, tokens
// and the mean task score.
type weekBuckets struct {
	runs, early, fixes    int
	briefDefects, defects int
	tokens                int64
	scoreSum              float64
	tasks                 int
	causes                protocol.CauseCounts
}

// ScoresWeeklyAt computes the last 8 ISO weeks relative to the given reference time.
// The task scores come from the picker's own reader (pickerTaskRecords) and
// PickerTaskScore, so there is one score rule and one set of task facts. The
// other columns are simple scans of indexed time ranges, bucketed by ISO week
// in Go.
func (s *Store) ScoresWeeklyAt(now time.Time) ([]protocol.ScoresWeeklyRow, error) {
	now = now.UTC()
	weekday := (int(now.Weekday()) + 6) % 7 // Monday = 0
	mon := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -weekday)
	start := mon.AddDate(0, 0, -7*7) // 7 weeks before current week Monday = 8 weeks total
	end := mon.AddDate(0, 0, 7)      // Next Monday 00:00:00 UTC
	startUnix, endUnix := start.Unix(), end.Unix()

	buckets := make([]weekBuckets, 8)
	index := make(map[string]int, 8)
	out := make([]protocol.ScoresWeeklyRow, 8)
	for i := range out {
		week := isoWeekKey(start.AddDate(0, 0, 7*i).Unix())
		index[week] = i
		out[i].Week = week
	}

	// Runs, early quits, fix rounds, tokens and causes per week.
	rows, err := s.db.Query(`
		SELECT r.ended_at, r.early_quit, r.fix_count, COALESCE(r.cause,''),
			COALESCE(CAST(json_extract(CASE WHEN json_valid(s.worker) THEN s.worker ELSE '{}' END, '$.tokens') AS INTEGER), 0)
		FROM worker_runs r LEFT JOIN sessions s ON s.id = r.session_id
		WHERE r.ended_at >= ? AND r.ended_at < ?`, startUnix, endUnix)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ended int64
		var early, fix int
		var cause string
		var tokens int64
		if err := rows.Scan(&ended, &early, &fix, &cause, &tokens); err != nil {
			rows.Close()
			return nil, err
		}
		i, ok := index[isoWeekKey(ended)]
		if !ok {
			continue
		}
		buckets[i].runs++
		buckets[i].early += early
		buckets[i].fixes += fix
		buckets[i].tokens += tokens
		switch cause {
		case protocol.CauseQuotaAuth, protocol.CauseSandbox, protocol.CauseHarness:
			buckets[i].causes.Machine++
		case protocol.CauseTestFailure, protocol.CauseAcceptFinding:
			buckets[i].causes.Work++
		case protocol.CauseBriefDefect:
			buckets[i].causes.Brief++
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	// The task score is the mean of PickerTaskScore over the tasks whose last
	// counted run ended in the week.
	tasks, err := s.pickerTaskRecords()
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		// A task is in the week of its last counted run: the same run that
		// gives the final outcome.
		if task.end < startUnix || task.end >= endUnix {
			continue
		}
		i, ok := index[isoWeekKey(task.end)]
		if !ok {
			continue
		}
		buckets[i].scoreSum += protocol.PickerTaskScore(task.facts)
		buckets[i].tasks++
	}

	// Brief defects are charged to the caller, not to the worker, and are
	// counted in the week the caller filed them (worker_brief_defects.at is
	// Unix seconds, like every other at column).
	if err := s.countWeeklyAt(`SELECT at FROM worker_brief_defects WHERE at >= ? AND at < ?`, startUnix, endUnix, index, buckets, func(b *weekBuckets) { b.briefDefects++ }); err != nil {
		return nil, err
	}
	if err := s.countWeeklyAt(`SELECT at FROM worker_defects WHERE moved_at = 0 AND at >= ? AND at < ?`, startUnix, endUnix, index, buckets, func(b *weekBuckets) { b.defects++ }); err != nil {
		return nil, err
	}
	// A defect found later is caught before release or escaped (decision 0141).
	rows, err = s.db.Query(`SELECT at, COALESCE(escape_phase,'') FROM worker_defects WHERE moved_at = 0 AND at >= ? AND at < ?`, startUnix, endUnix)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var at int64
		var phase string
		if err := rows.Scan(&at, &phase); err != nil {
			rows.Close()
			return nil, err
		}
		i, ok := index[isoWeekKey(at)]
		if !ok {
			continue
		}
		switch phase {
		case protocol.DefectCaught:
			buckets[i].causes.Caught++
		case protocol.DefectEscaped:
			buckets[i].causes.Escaped++
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	// One tag per release: every change of a release has its own evidence row,
	// so the tag is grouped and placed by its first row.
	releases := make([][]string, 8)
	rows, err = s.db.Query(`
		SELECT json_extract(fields, '$.tag') AS tag, MIN(at)
		FROM delivery_evidence
		WHERE step = 'release' AND json_extract(fields, '$.tag') IS NOT NULL AND json_extract(fields, '$.tag') <> ''
			AND at >= ? AND at < ?
		GROUP BY tag
		ORDER BY MIN(at) ASC`, startUnix, endUnix)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var tag string
		var at int64
		if err := rows.Scan(&tag, &at); err != nil {
			rows.Close()
			return nil, err
		}
		if i, ok := index[isoWeekKey(at)]; ok {
			releases[i] = append(releases[i], tag)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	for i := range out {
		b := buckets[i]
		if b.runs > 0 {
			out[i].Runs = b.runs
			out[i].EarlyQuitRate = float64(b.early) / float64(b.runs)
			out[i].MeanFixRounds = float64(b.fixes) / float64(b.runs)
			// Rounded like the old read, so a weekly mean token count keeps
			// the value the table showed before.
			out[i].MeanTokens = (b.tokens + int64(b.runs)/2) / int64(b.runs)
		}
		if b.tasks > 0 {
			out[i].MeanScore = b.scoreSum / float64(b.tasks)
		}
		out[i].BriefDefects = b.briefDefects
		out[i].Defects = b.defects
		out[i].Causes = b.causes
		out[i].Releases = releases[i]
	}
	return out, nil
}

// countWeeklyAt reads a list of Unix-second timestamps in the window and adds
// one to the matching week's row.
func (s *Store) countWeeklyAt(query string, start, end int64, index map[string]int, buckets []weekBuckets, add func(*weekBuckets)) error {
	rows, err := s.db.Query(query, start, end)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var at int64
		if err := rows.Scan(&at); err != nil {
			return err
		}
		if i, ok := index[isoWeekKey(at)]; ok {
			add(&buckets[i])
		}
	}
	return rows.Err()
}

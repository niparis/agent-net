package gateway

import (
	"net/http"
	"strings"
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

// ScoresWeeklyAt computes the last 8 ISO weeks relative to the given reference time.
func (s *Store) ScoresWeeklyAt(now time.Time) ([]protocol.ScoresWeeklyRow, error) {
	now = now.UTC()
	weekday := (int(now.Weekday()) + 6) % 7 // Monday = 0
	mon := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -weekday)
	start := mon.AddDate(0, 0, -7*7) // 7 weeks before current week Monday = 8 weeks total
	end := mon.AddDate(0, 0, 7)      // Next Monday 00:00:00 UTC

	startUnix, endUnix := start.Unix(), end.Unix()

	const query = `
	WITH RECURSIVE week_series(n, wstart) AS (
		SELECT 0, ?
		UNION ALL
		SELECT n + 1, wstart + 7 * 86400 FROM week_series WHERE n < 7
	),
	runs_per_task AS (
		SELECT
			CASE WHEN r.task_id <> '' THEN r.task_id ELSE r.session_id END AS root,
			strftime('%G-W%V', MAX(r.ended_at), 'unixepoch') AS week,
			(SELECT outcome FROM worker_runs r2
				WHERE (CASE WHEN r2.task_id <> '' THEN r2.task_id ELSE r2.session_id END) = (CASE WHEN r.task_id <> '' THEN r.task_id ELSE r.session_id END)
				  AND r2.outcome NOT IN ('stopped', 'infra')
				ORDER BY r2.ended_at DESC, r2.id DESC LIMIT 1) AS final_outcome,
			SUM(r.fix_count) AS task_fixes,
			(SELECT COUNT(*) FROM delivery_evidence e JOIN delivery_changes c ON c.id = e.change_id
				WHERE e.result = 'fail' AND e.step IN ('accept', 'review', 'test')
				AND json_extract(c.origin, '$.session_id') IN (SELECT session_id FROM worker_runs r3 WHERE (CASE WHEN r3.task_id <> '' THEN r3.task_id ELSE r3.session_id END) = (CASE WHEN r.task_id <> '' THEN r.task_id ELSE r.session_id END))) AS failed_steps,
			(SELECT COUNT(*) FROM worker_defects d
				WHERE d.moved_at = 0
				AND d.session_id IN (SELECT session_id FROM worker_runs r4 WHERE (CASE WHEN r4.task_id <> '' THEN r4.task_id ELSE r4.session_id END) = (CASE WHEN r.task_id <> '' THEN r.task_id ELSE r.session_id END))) AS defect_count,
			(SELECT COALESCE(SUM(d.severity), 0) FROM worker_defects d
				WHERE d.moved_at = 0
				AND d.session_id IN (SELECT session_id FROM worker_runs r5 WHERE (CASE WHEN r5.task_id <> '' THEN r5.task_id ELSE r5.session_id END) = (CASE WHEN r.task_id <> '' THEN r.task_id ELSE r.session_id END))) AS defect_sev
		FROM worker_runs r
		WHERE r.outcome NOT IN ('stopped', 'infra') AND r.ended_at >= ? AND r.ended_at < ?
		GROUP BY root
	),
	task_scores AS (
		SELECT
			week,
			AVG(CASE
				WHEN final_outcome <> 'complete' THEN 0.0
				ELSE MAX(0.0, 1.0 - 0.25 * (task_fixes + failed_steps + defect_count + defect_sev))
			END) AS mean_score
		FROM runs_per_task
		GROUP BY week
	),
	runs_summary AS (
		SELECT
			strftime('%G-W%V', r.ended_at, 'unixepoch') AS week,
			COUNT(*) AS runs,
			CAST(SUM(CASE WHEN r.early_quit = 1 THEN 1 ELSE 0 END) AS REAL) / COUNT(*) AS early_quit_rate,
			CAST(SUM(r.fix_count) AS REAL) / COUNT(*) AS mean_fix_rounds,
			CAST(SUM(COALESCE(CAST(json_extract(CASE WHEN json_valid(s.worker) THEN s.worker ELSE '{}' END, '$.tokens') AS INTEGER), 0)) AS REAL) / COUNT(*) AS mean_tokens
		FROM worker_runs r
		LEFT JOIN sessions s ON s.id = r.session_id
		WHERE r.ended_at >= ? AND r.ended_at < ?
		GROUP BY week
	),
	brief_defects_summary AS (
		SELECT
			strftime('%G-W%V', at, 'unixepoch') AS week,
			COUNT(*) AS brief_defects
		FROM worker_brief_defects
		WHERE at >= ? AND at < ?
		GROUP BY week
	),
	defects_summary AS (
		SELECT
			strftime('%G-W%V', at, 'unixepoch') AS week,
			COUNT(*) AS defects
		FROM worker_defects
		WHERE moved_at = 0 AND at >= ? AND at < ?
		GROUP BY week
	),
	releases_summary AS (
		SELECT
			week,
			GROUP_CONCAT(tag, ', ') AS releases
		FROM (
			SELECT DISTINCT strftime('%G-W%V', at, 'unixepoch') AS week, json_extract(fields, '$.tag') AS tag, at
			FROM delivery_evidence
			WHERE step = 'release' AND json_extract(fields, '$.tag') IS NOT NULL AND json_extract(fields, '$.tag') <> '' AND at >= ? AND at < ?
			ORDER BY at ASC
		)
		GROUP BY week
	)
	SELECT
		strftime('%G-W%V', w.wstart, 'unixepoch') AS week,
		COALESCE(rs.runs, 0) AS runs,
		COALESCE(ts.mean_score, 0.0) AS mean_score,
		COALESCE(rs.early_quit_rate, 0.0) AS early_quit_rate,
		COALESCE(rs.mean_fix_rounds, 0.0) AS mean_fix_rounds,
		COALESCE(bds.brief_defects, 0) AS brief_defects,
		COALESCE(ds.defects, 0) AS defects,
		ROUND(COALESCE(rs.mean_tokens, 0)) AS mean_tokens,
		COALESCE(rel.releases, '') AS releases
	FROM week_series w
	LEFT JOIN runs_summary rs ON rs.week = strftime('%G-W%V', w.wstart, 'unixepoch')
	LEFT JOIN task_scores ts ON ts.week = strftime('%G-W%V', w.wstart, 'unixepoch')
	LEFT JOIN brief_defects_summary bds ON bds.week = strftime('%G-W%V', w.wstart, 'unixepoch')
	LEFT JOIN defects_summary ds ON ds.week = strftime('%G-W%V', w.wstart, 'unixepoch')
	LEFT JOIN releases_summary rel ON rel.week = strftime('%G-W%V', w.wstart, 'unixepoch')
	ORDER BY w.wstart ASC
	`

	rows, err := s.db.Query(query,
		startUnix,
		startUnix, endUnix,
		startUnix, endUnix,
		startUnix, endUnix,
		startUnix, endUnix,
		startUnix, endUnix,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []protocol.ScoresWeeklyRow
	for rows.Next() {
		var row protocol.ScoresWeeklyRow
		var relStr string
		var meanTokens float64
		if err := rows.Scan(
			&row.Week,
			&row.Runs,
			&row.MeanScore,
			&row.EarlyQuitRate,
			&row.MeanFixRounds,
			&row.BriefDefects,
			&row.Defects,
			&meanTokens,
			&relStr,
		); err != nil {
			return nil, err
		}
		row.MeanTokens = int64(meanTokens)
		if relStr != "" {
			row.Releases = strings.Split(relStr, ", ")
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

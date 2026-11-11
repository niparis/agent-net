package gateway

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// briefRetryWindow is how long a refused brief is remembered for its task.
// After it, a caller gets two fresh rejections.
const briefRetryWindow = 24 * time.Hour

// checkWorkerBrief runs the brief checks that need the store, on top of the
// text checks in protocol.CheckWorkerBrief. Overlap compares the paths named
// in this brief with the briefs of running workers in the same project and the
// changed files of open changes there. A brief that says "overlap ok: reason"
// passes the overlap check.
func (s *Store) checkWorkerBrief(p protocol.WorkerStartFrame, project, level string) ([]string, error) {
	failures := protocol.CheckWorkerBrief(p.Task, level, p.Model, p.NotFamily)
	if _, ok := protocol.BriefOverlapAllowed(p.Task); ok {
		return failures, nil
	}
	mine := protocol.BriefPaths(p.Task)
	if len(mine) == 0 {
		return failures, nil
	}
	running, err := s.runningWorkerBriefs(project)
	if err != nil {
		return nil, err
	}
	changes, err := s.openChangeBriefs(project)
	if err != nil {
		return nil, err
	}
	var overlaps []string
	seen := map[string]bool{}
	for _, other := range append(running, changes...) {
		for _, a := range mine {
			for _, b := range other.paths {
				if !protocol.BriefPathsShare(a, b) {
					continue
				}
				shared := a
				if len(b) > len(shared) {
					shared = b
				}
				msg := fmt.Sprintf("overlaps %s %s on %s", other.kind, other.id, shared)
				if !seen[msg] {
					seen[msg] = true
					overlaps = append(overlaps, msg)
				}
			}
		}
	}
	sort.Strings(overlaps)
	return append(failures, overlaps...), nil
}

type briefOverlap struct {
	kind  string
	id    string
	paths []string
}

// runningWorkerBriefs reads the full task text of the running workers in one
// project. The task message holds the brief as the caller wrote it; the
// worker record only keeps its first line.
func (s *Store) runningWorkerBriefs(project string) ([]briefOverlap, error) {
	agents, err := s.Agents(protocol.QueryAgents{OnlineOnly: true})
	if err != nil {
		return nil, err
	}
	var out []briefOverlap
	for _, a := range agents {
		if a.Worker == nil || !strings.EqualFold(a.Worker.Project, project) {
			continue
		}
		task := a.Worker.Task
		if full, terr := s.workerBriefTask(a.SessionID); terr != nil {
			return nil, terr
		} else if full != "" {
			task = full
		}
		paths := protocol.BriefPaths(task)
		if len(paths) == 0 {
			continue
		}
		out = append(out, briefOverlap{kind: "running worker", id: a.SessionID, paths: paths})
	}
	return out, nil
}

// workerBriefTask returns the task text of the first task message sent to a
// worker, without the fixed preamble.
func (s *Store) workerBriefTask(session string) (string, error) {
	var body string
	err := s.db.QueryRow(`SELECT body FROM messages WHERE recipient=? AND COALESCE(worker_task,0)=1
		ORDER BY created_at DESC, rowid DESC LIMIT 1`, session).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	const marker = "\n\nTask:\n"
	if i := strings.Index(body, marker); i >= 0 {
		return body[i+len(marker):], nil
	}
	return body, nil
}

// openChangeBriefs reads the changed files of the open changes in one project,
// from the diff stat stored for the current revision.
func (s *Store) openChangeBriefs(project string) ([]briefOverlap, error) {
	rows, err := s.db.Query(`SELECT c.id, COALESCE(st.diff_stat,'')
		FROM delivery_changes c
		LEFT JOIN delivery_revision_stats st ON st.change_id=c.id AND st.revision=c.revision
		WHERE c.project=? AND c.state='open' ORDER BY c.id`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []briefOverlap
	for rows.Next() {
		var id, stat string
		if err := rows.Scan(&id, &stat); err != nil {
			return nil, err
		}
		paths := protocol.BriefPaths(stat)
		if len(paths) == 0 {
			continue
		}
		out = append(out, briefOverlap{kind: "open change", id: id, paths: paths})
	}
	return out, rows.Err()
}

// recordBriefRejection stores one refused start for the per-caller count.
func (s *Store) recordBriefRejection(caller, taskKey string, failures []string) error {
	_, err := s.db.Exec(`INSERT INTO worker_brief_rejections(caller,task_key,failures,at) VALUES(?,?,?,?)`,
		caller, taskKey, strings.Join(failures, "\n"), s.now().Unix())
	return err
}

// briefRejectionCount counts the refused starts of one task since cut.
func (s *Store) briefRejectionCount(caller, taskKey string, cut int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_brief_rejections WHERE caller=? AND task_key=? AND at>=?`,
		caller, taskKey, cut).Scan(&n)
	return n, err
}

// BriefRejectionCountsByCaller counts the refused briefs of each caller since
// cut. The weekly table adds it beside BriefDefectCountsByCaller.
func (s *Store) BriefRejectionCountsByCaller(cut int64) (map[string]int, error) {
	rows, err := s.db.Query(`SELECT caller, COUNT(*) FROM worker_brief_rejections WHERE at>=? GROUP BY caller`, cut)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var caller string
		var n int
		if err := rows.Scan(&caller, &n); err != nil {
			return nil, err
		}
		out[caller] = n
	}
	return out, rows.Err()
}

// recordBriefDefectFromBrief charges one brief defect to the caller when the
// third attempt starts with items still missing.
func (s *Store) recordBriefDefectFromBrief(caller string, failures []string) error {
	note := "brief started after 2 rejections: " + strings.Join(failures, "; ")
	_, err := s.db.Exec(`INSERT INTO worker_brief_defects
		(run_id, worker_session_id, caller, rater, rater_model, severity, note, at)
		VALUES (0,'',?,?,'',2,?,?)`, caller, caller, note, s.now().Unix())
	return err
}

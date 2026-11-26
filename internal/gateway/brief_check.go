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

// checkWorkerBrief runs the brief check that needs the store: overlap. The
// six format rules were retired (decision 0145); a brief is refused only for
// a file it shares with a running worker or an open change in the same
// project. Overlap compares the paths named in this brief with the briefs of
// running workers in the same project and the changed files of open changes
// there. A brief that says "overlap ok: reason" passes. A file that a clause
// tells the worker to leave alone ("do not touch X") does not count, in the
// new brief or in a running worker's brief. A change or worker
// this start itself targets is not an overlap: the change's id or the worker's
// id named in the brief, an open change whose branch is the start's base, or
// the continue or fixes target. A fix round starts from its own change's
// branch and names its files.
func (s *Store) checkWorkerBrief(p protocol.WorkerStartFrame, project string, repo protocol.WorkerBriefRepo) ([]string, []string, error) {
	text := p.BriefText()
	var failures []string
	if _, ok := protocol.BriefOverlapAllowed(text); ok {
		return failures, nil, nil
	}
	mine := repo.Paths(protocol.BriefWithoutAvoided(text))
	if len(mine) == 0 {
		return failures, nil, nil
	}
	mineSet := map[string]bool{}
	for _, file := range mine {
		mineSet[file] = true
	}
	targets := protocol.BriefTargetIDs(text)
	for _, id := range []string{p.Continue, p.Fixes} {
		if id = strings.TrimSpace(id); id != "" {
			targets[strings.ToLower(id)] = true
		}
	}
	base := strings.TrimSpace(p.Base)
	running, err := s.runningWorkerBriefs(project)
	if err != nil {
		return nil, nil, err
	}
	changes, err := s.openChangeBriefs(project)
	if err != nil {
		return nil, nil, err
	}
	var overlaps, warnings []string
	seen := map[string]bool{}
	for _, other := range append(running, changes...) {
		if targets[strings.ToLower(other.id)] || (base != "" && other.kind == "open change" && other.branch == base) {
			continue
		}
		otherFiles := repo.Paths(strings.Join(other.paths, "\n"))
		if other.kind == "running worker" {
			// A running worker started from its own base tree, which can be
			// older than the files the new brief's base holds now. A
			// directory in its brief names an area, not every file that
			// reached that area since: main's newer files are not its work.
			otherFiles = repo.FilePaths(strings.Join(other.paths, "\n"))
		}
		for _, shared := range otherFiles {
			if !mineSet[shared] {
				continue
			}
			msg := fmt.Sprintf("overlaps %s %s on %s", other.kind, other.id, shared)
			if !seen[msg] {
				seen[msg] = true
				if protocol.BriefSharedIndex(shared) {
					warnings = append(warnings, "warning: "+msg)
				} else {
					overlaps = append(overlaps, msg)
				}
			}
		}
	}
	sort.Strings(overlaps)
	sort.Strings(warnings)
	return append(failures, overlaps...), warnings, nil
}

type briefOverlap struct {
	kind   string
	id     string
	branch string
	paths  []string
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
		paths := protocol.BriefPaths(protocol.BriefWithoutAvoided(task))
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

// openChangeBriefs reads the branch and changed files of the open changes in
// one project, from the diff stat stored for the current revision.
func (s *Store) openChangeBriefs(project string) ([]briefOverlap, error) {
	rows, err := s.db.Query(`SELECT c.id, c.branch, COALESCE(st.diff_stat,'')
		FROM delivery_changes c
		LEFT JOIN delivery_revisions st ON st.change_id=c.id AND st.number=c.revision
		WHERE c.project=? AND c.state='open' ORDER BY c.id`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []briefOverlap
	for rows.Next() {
		var id, branch, stat string
		if err := rows.Scan(&id, &branch, &stat); err != nil {
			return nil, err
		}
		paths := protocol.BriefPaths(stat)
		// Diff stats name exact repository paths. A root file is not a
		// caller's ambiguous bare filename, so keep it root-relative.
		for i, file := range paths {
			paths[i] = "/" + strings.TrimPrefix(file, "/")
		}
		if len(paths) == 0 {
			continue
		}
		out = append(out, briefOverlap{kind: "open change", id: id, branch: branch, paths: paths})
	}
	return out, rows.Err()
}

// recordBriefRejection stores one refused start as a closed worker_starts row in
// state 'refused'. The row's age is the 24 hour window the retry rule counts in.
func (s *Store) recordBriefRejection(caller, taskKey string, failures []string) error {
	at := s.now().UnixNano()
	_, err := s.db.Exec(`INSERT INTO worker_starts(ref,node_id,owner,state,created_at,closed_at,task_key,failures) VALUES(?,'',?,'refused',?,?,?,?)`,
		protocol.NewID("refused"), caller, at, at, taskKey, strings.Join(failures, "\n"))
	return err
}

// briefRejectionCount counts the refused starts of one task since cut.
func (s *Store) briefRejectionCount(caller, taskKey string, cut int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_starts WHERE state='refused' AND owner=? AND task_key=? AND created_at>=?`,
		caller, taskKey, cut*int64(time.Second)).Scan(&n)
	return n, err
}

// recordBriefDefectFromBrief charges one brief defect to the caller when the
// third attempt starts with overlaps still there.
func (s *Store) recordBriefDefectFromBrief(caller string, failures []string) error {
	note := "brief started after 2 rejections: " + strings.Join(failures, "; ")
	_, err := s.db.Exec(`INSERT INTO worker_defects(session_id,caller,severity,note,at,blame) VALUES('',?,2,?,?,'brief')`, caller, note, s.now().Unix())
	return err
}

// osSandboxOf names the OS sandbox a machine's daemon runs its workers in, by
// the machine's OS (internal/daemon/ossandbox_*.go). It is "" for an OS with
// none, so the brief lint has no sandbox fact there.
func osSandboxOf(goos string) string {
	switch goos {
	case "linux":
		return "bubblewrap"
	case "darwin":
		return "sandbox-exec"
	}
	return ""
}

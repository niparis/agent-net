package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Hints that connect a fix to the earlier work it fixes (defect accounting).
// They are text only: nothing here blocks a start, a hand-back or a report.

// recentChangeWindow is how far back the hints look for merged changes.
const recentChangeWindow = 7 * 24 * time.Hour

// maxHintChanges is the most recent changes any hint lists.
const maxHintChanges = 5

// looksLikeFixTask tells whether a title or task says the work is a fix. It
// looks at whole words, so "prefix" and "document" do not count.
func looksLikeFixTask(text string) bool {
	for _, field := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		switch field {
		case "fix", "fixes", "fixed", "fixing", "bug", "bugs", "buggy",
			"defect", "defects", "regression", "regressions", "broken":
			return true
		}
	}
	return false
}

// WorkerStartHint is the one line a start result carries when the task sounds
// like a fix and the caller named no fixes. It names up to three recent
// changes in the project so the caller can pass the right ones.
func (s *Store) WorkerStartHint(project, title, task, fixes string) string {
	project = strings.TrimSpace(project)
	if project == "" || strings.TrimSpace(fixes) != "" || !looksLikeFixTask(title+" "+task) {
		return ""
	}
	changes, err := s.RecentWorkerChanges(project, "", "", 3)
	if err != nil || len(changes) == 0 {
		return ""
	}
	var recent []string
	for _, c := range changes {
		worker := c.WorkerSession
		if name := s.displayName(worker); name != "" {
			worker = name
		}
		recent = append(recent, fmt.Sprintf("%s %q by %s merged %s", c.ChangeID, c.Title, worker,
			time.Unix(c.MergedAt, 0).UTC().Format("2006-01-02")))
	}
	return "If this fixes earlier work, pass fixes=<change id> so agent-net counts the defect. Recent changes in " +
		project + ": " + strings.Join(recent, "; ")
}

// RecentWorkerChanges returns merged changes in project in the last seven
// days, newest first, made by a worker other than excludeSession and other
// than excludeChange. It is the data behind both hint lines. A change with no
// merged commit is left out: the hand-back hint cannot check its files.
func (s *Store) RecentWorkerChanges(project, excludeSession, excludeChange string, limit int) ([]protocol.RecentWorkerChange, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, nil
	}
	if limit <= 0 || limit > maxHintChanges {
		limit = maxHintChanges
	}
	cut := s.now().Add(-recentChangeWindow).Unix()
	rows, err := s.db.Query(`SELECT id, title, origin, updated_at, merged_commit FROM delivery_changes
		WHERE project=? AND state=? AND updated_at>=? ORDER BY updated_at DESC`, project, protocol.ChangeMerged, cut)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.RecentWorkerChange{}
	for rows.Next() {
		var id, title, rawOrigin, commit string
		var mergedAt int64
		if err := rows.Scan(&id, &title, &rawOrigin, &mergedAt, &commit); err != nil {
			return nil, err
		}
		if id == excludeChange || commit == "" {
			continue
		}
		var origin protocol.ChangeOrigin
		if json.Unmarshal([]byte(rawOrigin), &origin) != nil || origin.Kind != protocol.OriginWorker {
			continue
		}
		if origin.SessionID == "" || origin.SessionID == excludeSession {
			continue
		}
		out = append(out, protocol.RecentWorkerChange{ChangeID: id, Title: strings.TrimSpace(title),
			WorkerID: origin.WorkerID, WorkerSession: origin.SessionID, MergedAt: mergedAt, MergedCommit: commit})
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}

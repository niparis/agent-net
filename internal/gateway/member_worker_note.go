package gateway

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A member can leave "T2b worker running" in its task for an hour after the
// worker ended, and nothing shows the lead that the line is stale (decision
// 0111). When a view reads a task line that names a worker session which has
// ended, the gateway adds the note "(worker <id> ended <time>)" to the
// session's Profile.TaskNote. It is worked out at read time, with a fixed
// date and time so a cached read stays right. The member's own
// announce is never edited, and the idle check and the searches read the
// announce, not the note.

// workerIDInTask finds a session id in the text of a task.
var workerIDInTask = regexp.MustCompile(`\bs_[0-9a-f]{16}\b`)

// maxTaskNotes is how many worker ids of one task line are looked at.
const maxTaskNotes = 3

// applyEndedWorkerNotes sets the note on each session whose task names an
// ended worker. It costs one small lookup for a task that names a session id,
// and nothing for the rest.
func (s *Store) applyEndedWorkerNotes(agents []protocol.AgentView) error {
	type endedAt struct {
		at    int64
		ended bool
	}
	var looked map[string]endedAt
	for i := range agents {
		task := agents[i].Profile.Task
		if !strings.Contains(task, "s_") {
			continue
		}
		var notes []string
		seen := map[string]bool{}
		for _, id := range workerIDInTask.FindAllString(task, -1) {
			if seen[id] || id == agents[i].SessionID {
				continue
			}
			seen[id] = true
			if len(seen) > maxTaskNotes {
				break
			}
			if looked == nil {
				looked = map[string]endedAt{}
			}
			e, known := looked[id]
			if !known {
				var err error
				if e.at, e.ended, err = s.endedWorkerAt(id); err != nil {
					return err
				}
				looked[id] = e
			}
			if e.ended {
				notes = append(notes, fmt.Sprintf("(worker %s ended %s)", id, time.Unix(0, e.at).UTC().Format("Jan 2 15:04")+" UTC"))
			}
		}
		agents[i].Profile.TaskNote = strings.Join(notes, " ")
	}
	return nil
}

// endedWorkerAt is when a worker session ended, in Unix nanoseconds. ended is
// false when the id is not a worker session, or its worker has not ended.
func (s *Store) endedWorkerAt(id string) (at int64, ended bool, err error) {
	err = s.db.QueryRow(`SELECT last_seen FROM sessions WHERE id=? AND worker<>'' AND status='ended'`, id).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return at, err == nil, err
}

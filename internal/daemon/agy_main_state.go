package daemon

import (
	"encoding/json"
	"slices"
	"time"
)

// agyMainsSchema keeps each registered agy main session across a daemon
// restart. agy sends SessionStart only on the first prompt of a process, so a
// running agy never reports itself again after the daemon forgets it.
const agyMainsSchema = `CREATE TABLE IF NOT EXISTS agy_main_sessions (
	conversation_id TEXT PRIMARY KEY, pid INTEGER NOT NULL, process_start TEXT NOT NULL,
	workspace_paths TEXT NOT NULL CHECK(json_valid(workspace_paths)), start_ref TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL)`

// agyMainRow is one registered agy conversation and the process that runs it.
type agyMainRow struct {
	ConversationID string
	PID            int
	ProcessStart   string
	WorkspacePaths []string
	StartRef       string
	At             time.Time
}

func (r agyMainRow) same(o agyMainRow) bool {
	return r.ConversationID == o.ConversationID && r.PID == o.PID && r.ProcessStart == o.ProcessStart &&
		r.StartRef == o.StartRef && slices.Equal(r.WorkspacePaths, o.WorkspacePaths)
}

func (s *state) agyMains() ([]agyMainRow, error) {
	rows, err := s.db.Query(`SELECT conversation_id,pid,process_start,workspace_paths,start_ref,at FROM agy_main_sessions ORDER BY conversation_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []agyMainRow
	for rows.Next() {
		var r agyMainRow
		var paths string
		var at int64
		if err := rows.Scan(&r.ConversationID, &r.PID, &r.ProcessStart, &paths, &r.StartRef, &at); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(paths), &r.WorkspacePaths); err != nil {
			return nil, err
		}
		r.At = time.Unix(at, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *state) saveAgyMain(r agyMainRow) error {
	paths, err := json.Marshal(append([]string{}, r.WorkspacePaths...))
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO agy_main_sessions(conversation_id,pid,process_start,workspace_paths,start_ref,at) VALUES(?,?,?,?,?,?)
ON CONFLICT(conversation_id) DO UPDATE SET pid=excluded.pid, process_start=excluded.process_start, workspace_paths=excluded.workspace_paths, start_ref=excluded.start_ref, at=excluded.at`,
		r.ConversationID, r.PID, r.ProcessStart, string(paths), r.StartRef, r.At.Unix())
	return err
}

// dropAgyMain removes the row only while it still names that process, so a
// newer agy process of the same conversation keeps its row.
func (s *state) dropAgyMain(r agyMainRow) error {
	_, err := s.db.Exec(`DELETE FROM agy_main_sessions WHERE conversation_id=? AND pid=? AND process_start=?`, r.ConversationID, r.PID, r.ProcessStart)
	return err
}

// agyMainLive reports whether pid is still the agy process that was saved.
func agyMainLive(pid int, processStart string) bool {
	return pid > 1 && processStart != "" && agyProcess(pid) && agyProcessStart(pid) == processStart
}

// loadAgyMains brings back the agy main sessions an earlier daemon registered
// whose process still runs, and forgets the others. New calls it.
func (d *Daemon) loadAgyMains() error {
	if d.st == nil {
		return nil
	}
	rows, err := d.st.agyMains()
	if err != nil {
		return err
	}
	for _, r := range rows {
		if !agyMainLive(r.PID, r.ProcessStart) {
			if err := d.st.dropAgyMain(r); err != nil {
				return err
			}
			continue
		}
		d.mu.Lock()
		if d.agyEvents == nil {
			d.agyEvents = make(map[string]antigravityEvent)
		}
		d.agyEvents[r.ConversationID] = antigravityEvent{ConversationID: r.ConversationID, WorkspacePaths: r.WorkspacePaths, Time: r.At,
			StartRef: r.StartRef, Started: true, PID: r.PID, ProcessStart: r.ProcessStart}
		d.mu.Unlock()
	}
	return nil
}

// saveAgyMains makes the saved rows match the registered sessions. Only
// changes are written. It runs in refresh, outside the daemon locks.
func (a *agyAdapter) saveAgyMains(events map[string]antigravityEvent, registered map[string]bool) {
	st := a.d.st
	if st == nil {
		return
	}
	want := map[string]agyMainRow{}
	for id := range registered {
		e := events[id]
		want[id] = agyMainRow{ConversationID: id, PID: e.PID, ProcessStart: e.ProcessStart, WorkspacePaths: e.WorkspacePaths, StartRef: e.StartRef, At: e.Time}
	}
	for id, old := range a.saved {
		if _, ok := want[id]; ok {
			continue
		}
		if err := st.dropAgyMain(old); err != nil {
			a.d.Logf("agy: cannot forget saved session %s: %v", id, err)
			continue
		}
		delete(a.saved, id)
	}
	for id, r := range want {
		if old, ok := a.saved[id]; ok && old.same(r) {
			continue
		}
		if err := st.saveAgyMain(r); err != nil {
			a.d.Logf("agy: cannot save session %s: %v", id, err)
			continue
		}
		a.saved[id] = r
	}
}

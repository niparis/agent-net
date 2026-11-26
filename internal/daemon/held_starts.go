package daemon

import (
	"encoding/json"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A start that waits for the owner's answer on the Desk is kept in daemon.db,
// next to the worker queue, so the answer still starts it after a daemon
// restart. The request kept here never holds owner_words or the caller's
// reason: checkOverride clears both before a start is held.

const heldStartsSchema = `CREATE TABLE IF NOT EXISTS held_starts (
	id TEXT PRIMARY KEY, payload TEXT NOT NULL CHECK(json_valid(payload)), at INTEGER NOT NULL)`

// heldStartRow is what the answer needs to start the worker.
type heldStartRow struct {
	Caller        string                      `json:"caller"`
	Request       protocol.WorkerStartRequest `json:"request"`
	CallerProject bool                        `json:"caller_project,omitempty"`
	CallerPath    string                      `json:"caller_path,omitempty"`
	CTO           bool                        `json:"cto,omitempty"`
}

func (s *state) addHeldStart(h *heldStart, at time.Time) error {
	raw, err := json.Marshal(heldStartRow{Caller: h.caller, Request: h.req, CallerProject: h.callerProject, CallerPath: h.callerPath, CTO: h.cto})
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR REPLACE INTO held_starts(id,payload,at) VALUES(?,?,?)`, h.id, string(raw), at.UnixNano())
	return err
}

func (s *state) forgetHeldStart(id string) error {
	_, err := s.db.Exec(`DELETE FROM held_starts WHERE id=?`, id)
	return err
}

func (s *state) heldStarts() (map[string]*heldStart, error) {
	rows, err := s.db.Query(`SELECT id, payload FROM held_starts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*heldStart{}
	for rows.Next() {
		var id, payload string
		if err := rows.Scan(&id, &payload); err != nil {
			return nil, err
		}
		var r heldStartRow
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		out[id] = &heldStart{id: id, caller: r.Caller, req: r.Request, callerProject: r.CallerProject, callerPath: r.CallerPath, cto: r.CTO}
	}
	return out, rows.Err()
}

// loadHeldStarts reads the starts an earlier daemon held. New calls it.
func (d *Daemon) loadHeldStarts() error {
	if d.st == nil {
		return nil
	}
	held, err := d.st.heldStarts()
	if err != nil {
		return err
	}
	d.heldMu.Lock()
	d.heldStarts = held
	d.heldMu.Unlock()
	return nil
}

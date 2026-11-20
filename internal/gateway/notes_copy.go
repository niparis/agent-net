package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

type noteCopyDB interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
	Exec(string, ...any) (sql.Result, error)
}

// copyAgentNotesQ copies the agent-scope notes of agent from to agent to. It
// adds rows and deletes none: from keeps its notes, and a name that to already
// has is kept as it is, so a copy never overwrites. Notes of one agent are
// shared by all its sessions, which is why a note is copied and not moved.
func copyAgentNotesQ(q noteCopyDB, from, to string, now time.Time) (copied, kept []string, err error) {
	copied, kept = []string{}, []string{}
	if from == "" || from == to {
		return copied, kept, nil
	}
	rows, err := q.Query(`SELECT `+noteColumns+` FROM standing_orders WHERE name<>'' AND scope=? AND scope_ref=? ORDER BY saved_at,id`, protocol.NoteScopeAgent, from)
	if err != nil {
		return nil, nil, err
	}
	var notes []protocol.StandingOrder
	for rows.Next() {
		o, e := scanNote(rows)
		if e != nil {
			rows.Close()
			return nil, nil, e
		}
		notes = append(notes, o)
	}
	if err = rows.Close(); err != nil {
		return nil, nil, err
	}
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	for _, o := range notes {
		var id string
		switch err = q.QueryRow(`SELECT id FROM standing_orders WHERE scope=? AND scope_ref=? AND name=?`, protocol.NoteScopeAgent, to, o.Name).Scan(&id); {
		case err == nil:
			kept = append(kept, o.Name)
			continue
		case !errors.Is(err, sql.ErrNoRows):
			return nil, nil, err
		}
		id = protocol.NewID("so")
		tags, _ := json.Marshal(o.Tags)
		// The copy keeps the text, kind, project and source of the original.
		// Its use count starts again: nobody has read this copy yet.
		if _, err = q.Exec(`INSERT INTO standing_orders(id,text,saved_at,source,tags,pinned,updated_at,kind,area,ref,scope,scope_ref,name,description,used_at,uses)
 VALUES(?,?,?,?,?,0,0,?,?,'',?,?,?,?,?,0)`, id, o.Text, o.SavedAt, o.Source, string(tags), o.Kind, o.Area, protocol.NoteScopeAgent, to, o.Name, o.Description, now.Unix()); err != nil {
			return nil, nil, err
		}
		copied = append(copied, id)
	}
	return copied, kept, nil
}

// CopyAgentNotes is the owner's copy of one agent's notes to another agent.
func (s *Store) CopyAgentNotes(node string, p protocol.NotesCopyRequest) (protocol.NotesCopyResult, error) {
	p.FromAgent, p.ToAgent = strings.TrimSpace(p.FromAgent), strings.TrimSpace(p.ToAgent)
	if p.FromAgent == "" || p.ToAgent == "" {
		return protocol.NotesCopyResult{}, reject("from_agent and to_agent are required")
	}
	if p.FromAgent == p.ToAgent {
		return protocol.NotesCopyResult{}, reject("from_agent and to_agent are the same agent")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.NotesCopyResult{}, err
	}
	defer tx.Rollback()
	if err := sessionAgentOwner(tx, node, p.Owner); err != nil {
		return protocol.NotesCopyResult{}, err
	}
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents WHERE id=? AND tombstone=0 AND role NOT IN ('cto','bot'))`, p.ToAgent).Scan(&exists); err != nil {
		return protocol.NotesCopyResult{}, err
	}
	if !exists {
		return protocol.NotesCopyResult{}, reject("unknown or unavailable agent %q", p.ToAgent)
	}
	copied, kept, err := copyAgentNotesQ(tx, p.FromAgent, p.ToAgent, s.now())
	if err != nil {
		return protocol.NotesCopyResult{}, err
	}
	return protocol.NotesCopyResult{CopiedNotes: copied, KeptNotes: kept}, tx.Commit()
}

// UncopyNotes deletes the agent-scope notes with the given ids: the undo of a
// copy. It touches no other note, so a note the agent wrote itself stays.
func (s *Store) UncopyNotes(node string, p protocol.NotesUncopyRequest) error {
	if len(p.IDs) == 0 {
		return reject("ids are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := sessionAgentOwner(tx, node, p.Owner); err != nil {
		return err
	}
	for _, id := range p.IDs {
		if _, err := tx.Exec(`DELETE FROM standing_orders WHERE id=? AND scope=? AND name<>''`, id, protocol.NoteScopeAgent); err != nil {
			return err
		}
	}
	return tx.Commit()
}

package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoNotes runs the two note tools for the CTO. It has no bound agent session,
// so the notes are those of its network session: the agent "cto", no project,
// and the owner-wide ones. The call is the same gateway frame an agent session
// uses (operator.go), so there is one write path.
func (d *Daemon) ctoNotes(ctx context.Context, tool string, raw json.RawMessage) (any, error) {
	sid := d.ctoSessionID()
	if sid == "" {
		return nil, errors.New("CTO network session is not registered; retry after the gateway reconnects")
	}
	var frame string
	var arg, out any
	if tool == "agent_note_write" {
		var p protocol.NoteWriteRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad agent_note_write params")
		}
		frame, arg, out = "note.write", protocol.NoteWriteFrame{NoteWriteRequest: p, Session: sid}, &protocol.NoteWriteResult{}
	} else {
		var p protocol.NotesFindRequest
		if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad agent_notes_find params")
		}
		frame, arg, out = "notes.find", protocol.NotesFindFrame{NotesFindRequest: p, Session: sid}, &protocol.NotesFindResult{}
	}
	data, err := d.call(ctx, frame, arg)
	if err != nil {
		return nil, oldGateway(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return nil, err
	}
	return out, nil
}

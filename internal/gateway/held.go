package gateway

import (
	"database/sql"
	"encoding/json"

	"github.com/niparis/agent-net/internal/protocol"
)

// HeldRequests returns unexpired requests awaiting an owner decision.
func (s *Store) HeldRequests() ([]protocol.Message, error) {
	rows, err := s.db.Query(msgSelect+` WHERE m.state='held' AND m.expires_at>? ORDER BY m.created_at, m.rowid`, s.now().UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AnswerHeld applies one owner's decision once, atomically with a rule change.
func (s *Store) AnswerHeld(id, action string) (protocol.Message, error) {
	var empty protocol.Message
	if action != "a" && action != "A" && action != "r" && action != "R" {
		return empty, reject("invalid held action")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	m, ok, err := s.message(tx, "m.id=?", id)
	if err != nil {
		return empty, err
	}
	if !ok || m.State != protocol.StateHeld || !m.ExpiresAt.After(s.now()) {
		return empty, reject("request is not held")
	}
	if action == "A" || action == "R" {
		var from, to string
		err = tx.QueryRow(`SELECT COALESCE(NULLIF(s.repo_project,''),p.project,'') FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=?`, m.From).Scan(&from)
		if err != nil {
			return empty, err
		}
		err = tx.QueryRow(`SELECT COALESCE(NULLIF(s.repo_project,''),p.project,'') FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=?`, m.To).Scan(&to)
		if err != nil {
			return empty, err
		}
		if from == "" || to == "" || from == to {
			return empty, reject("request has no distinct graph nodes")
		}
		var old protocol.Edge
		old.From, old.To = from, to
		err = tx.QueryRow(`SELECT kind,mode,rule FROM graph_edges WHERE source=? AND target=? ORDER BY kind LIMIT 1`, from, to).Scan(&old.Kind, &old.Mode, &old.Rule)
		previous := ""
		e := old
		if err == nil {
			b, _ := json.Marshal(old)
			previous = string(b)
		} else if err == sql.ErrNoRows {
			e.Kind = "depends"
		} else {
			return empty, err
		}
		e.Rule = "auto"
		if action == "R" {
			e.Rule = "never"
		}
		b, _ := json.Marshal(e)
		if _, err = tx.Exec(`INSERT INTO graph_edges VALUES(?,?,?,?,?) ON CONFLICT(source,target,kind) DO UPDATE SET rule=excluded.rule`, e.From, e.To, e.Kind, e.Mode, e.Rule); err != nil {
			return empty, err
		}
		if err = graphRecord(tx, "edge", from, e.Kind, previous, string(b), "owner", s.now().UnixNano()); err != nil {
			return empty, err
		}
	}
	state := protocol.StateFailed
	approved := false
	if action == "a" || action == "A" {
		state = protocol.StateStored
		approved = true
	}
	res, err := tx.Exec(`UPDATE messages SET state=?,owner_approved=? WHERE id=? AND state='held' AND expires_at>?`, state, approved, id, s.now().UnixNano())
	if err != nil {
		return empty, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return empty, err
	}
	if n != 1 {
		return empty, reject("request is not held")
	}
	if !approved {
		if err = s.heldReply(tx, m, "your user declined this request", "decline"); err != nil {
			return empty, err
		}
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	m.State = state
	m.OwnerApproved = approved
	return m, nil
}

// heldReply writes a gateway-generated response in the same transaction as the decision.
func (s *Store) heldReply(tx *sql.Tx, m protocol.Message, body, reason string) error {
	now := s.now()
	id := protocol.NewID("m")
	_, err := tx.Exec(`INSERT INTO messages (id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state,sender_cto) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,0)`,
		id, m.ConversationID, m.ID, m.To, m.From, protocol.TypeReply, body, m.Hop+1, "held-"+reason+"-"+m.ID, now.UnixNano(), now.Add(protocol.DefaultTTL).UnixNano(), protocol.StateStored)
	if err != nil {
		return err
	}
	s.event(tx, "msg.stored", "", m.To, id, "type=reply")
	return nil
}

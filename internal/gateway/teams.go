package gateway

import (
	"database/sql"
	"encoding/json"
	"strings"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

func scanTeam(row interface{ Scan(...any) error }) (protocol.Team, error) {
	var t protocol.Team
	var owner *string
	err := row.Scan(&t.ID, &t.Name, &t.Purpose, &owner, &t.Active, &t.CreatedAt, &t.UpdatedAt)
	if owner != nil {
		t.Owner = &protocol.GraphRef{Kind: "agent", ID: *owner}
	}
	return t, err
}

// Teams includes archived identities so their history and owner remain visible.
func (s *Store) Teams() ([]protocol.Team, error) {
	rows, err := s.db.Query(`SELECT id,name,purpose,owner_agent_id,active,created_at,updated_at FROM teams ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.Team{}
	for rows.Next() {
		t, err := scanTeam(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// WriteTeam validates authority, endpoints and expected values in the write
// transaction. This step changes no settings, routing or delivery revisions.
func (s *Store) WriteTeam(q protocol.TeamWrite, actor string) error {
	if err := s.requireGraphRefs(); err != nil {
		return err
	}
	if actor != "owner" {
		return reject("only the owner may change Teams")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var on bool
	if err = tx.QueryRow(`SELECT enabled FROM graph_ref_gate WHERE id=1`).Scan(&on); err != nil {
		return err
	}
	if !on {
		return reject("typed Graph cutover is waiting for all machines to upgrade")
	}
	now := s.now().UnixNano()
	oldValue, newValue, oldTeam := "", "", ""
	if q.Action == "membership" {
		oldTeam, err = writeTeamMember(tx, q)
		oldValue, newValue = oldTeam, q.ID
	} else {
		if !validGraphName(q.ID) {
			return reject("invalid Team ID")
		}
		switch q.Action {
		case "create":
			if !validGraphName(q.Name) {
				return reject("invalid Team name")
			}
			if len(q.Purpose) > protocol.CharterValueMaxBytes || strings.IndexFunc(q.Purpose, func(r rune) bool { return unicode.IsControl(r) && r != '\n' }) >= 0 {
				return reject("invalid Team purpose")
			}
			var owner any
			if q.Owner != nil {
				if q.Owner.Kind != "agent" {
					return reject("Team owner must be an Agent")
				}
				if err = liveTeamMember(tx, *q.Owner); err != nil {
					return err
				}
				owner = q.Owner.ID
			}
			_, err = tx.Exec(`INSERT INTO teams(id,name,purpose,owner_agent_id,created_at,updated_at) VALUES(?,?,?,?,?,?)`, q.ID, q.Name, q.Purpose, owner, now, now)
		case "rename", "archive":
			var t protocol.Team
			t, err = scanTeam(tx.QueryRow(`SELECT id,name,purpose,owner_agent_id,active,created_at,updated_at FROM teams WHERE id=?`, q.ID))
			if err == sql.ErrNoRows {
				return reject("unknown Team")
			}
			if err != nil {
				return err
			}
			if !t.Active {
				return reject("Team is archived")
			}
			b, _ := json.Marshal(t)
			oldValue = string(b)
			if q.Action == "rename" {
				if t.Name != q.ExpectName {
					return reject("Team name changed; reload Graph")
				}
				if !validGraphName(q.Name) {
					return reject("invalid Team name")
				}
				if t.Name == q.Name {
					return nil
				}
				_, err = tx.Exec(`UPDATE teams SET name=?,updated_at=? WHERE id=?`, q.Name, now, q.ID)
			} else {
				var members int
				if err = tx.QueryRow(`SELECT COUNT(*) FROM team_members WHERE team_id=?`, q.ID).Scan(&members); err != nil {
					return err
				}
				if members != 0 {
					return reject("remove or move Team members before archiving")
				}
				_, err = tx.Exec(`UPDATE teams SET active=0,updated_at=? WHERE id=?`, now, q.ID)
			}
		default:
			return reject("unknown Team action")
		}
		if err == nil {
			var t protocol.Team
			t, err = scanTeam(tx.QueryRow(`SELECT id,name,purpose,owner_agent_id,active,created_at,updated_at FROM teams WHERE id=?`, q.ID))
			b, _ := json.Marshal(t)
			newValue = string(b)
		}
	}
	if err != nil {
		return err
	}
	if oldValue == newValue {
		return nil
	}
	kind, id := "", ""
	if q.Member != nil && q.Action == "membership" {
		kind, id = q.Member.Kind, q.Member.ID
	}
	if _, err = tx.Exec(`INSERT INTO team_history(action,team_id,old_team_id,member_kind,member_id,old_value,new_value,at,actor) VALUES(?,?,?,?,?,?,?,?,?)`, q.Action, q.ID, oldTeam, kind, id, oldValue, newValue, now, actor); err != nil {
		return err
	}
	return tx.Commit()
}

func liveTeamMember(tx *sql.Tx, ref protocol.GraphRef) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	query := ""
	switch ref.Kind {
	case "agent":
		query = `SELECT EXISTS(SELECT 1 FROM agents WHERE id=? AND tombstone=0)`
	case "repo":
		query = `SELECT EXISTS(SELECT 1 FROM repos WHERE id=?)`
	default:
		return reject("Team member must be an Agent or Repo")
	}
	var exists bool
	if err := tx.QueryRow(query, ref.ID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return reject("unknown or inactive %s endpoint", ref.Kind)
	}
	return nil
}

func writeTeamMember(tx *sql.Tx, q protocol.TeamWrite) (string, error) {
	if q.Member == nil {
		return "", reject("typed member required")
	}
	if err := liveTeamMember(tx, *q.Member); err != nil {
		return "", err
	}
	var old string
	err := tx.QueryRow(`SELECT team_id FROM team_members WHERE member_kind=? AND member_id=?`, q.Member.Kind, q.Member.ID).Scan(&old)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if old != q.ExpectTeam {
		return "", reject("membership changed; reload Graph")
	}
	if q.ID != "" {
		var active bool
		err = tx.QueryRow(`SELECT active FROM teams WHERE id=?`, q.ID).Scan(&active)
		if err == sql.ErrNoRows {
			return "", reject("unknown Team")
		}
		if err != nil {
			return "", err
		}
		if !active {
			return "", reject("Team is archived")
		}
	}
	if old == q.ID {
		return old, nil
	}
	if q.ID == "" {
		_, err = tx.Exec(`DELETE FROM team_members WHERE member_kind=? AND member_id=?`, q.Member.Kind, q.Member.ID)
	} else {
		_, err = tx.Exec(`INSERT INTO team_members(member_kind,member_id,team_id) VALUES(?,?,?) ON CONFLICT(member_kind,member_id) DO UPDATE SET team_id=excluded.team_id`, q.Member.Kind, q.Member.ID, q.ID)
	}
	return old, err
}

// TeamHistory is owner-only through the operator relay. Undo and proposals are
// deferred; retained snapshots cannot bypass current validation.
func (s *Store) TeamHistory(id string, limit int) ([]protocol.TeamChange, error) {
	if err := s.requireGraphRefs(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT id,action,team_id,old_team_id,member_kind,member_id,old_value,new_value,at,actor FROM team_history WHERE team_id=? OR old_team_id=? ORDER BY id DESC LIMIT ?`, id, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.TeamChange{}
	for rows.Next() {
		var h protocol.TeamChange
		var ref protocol.GraphRef
		if err = rows.Scan(&h.ID, &h.Action, &h.TeamID, &h.OldTeamID, &ref.Kind, &ref.ID, &h.OldValue, &h.NewValue, &h.At, &h.By); err != nil {
			return nil, err
		}
		if ref.Kind != "" {
			h.Member = &ref
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) teamGraph() (protocol.Graph, error) {
	g := protocol.Graph{Nodes: []protocol.Node{}, Edges: []protocol.Edge{}}
	teams, err := s.Teams()
	if err != nil {
		return g, err
	}
	for _, t := range teams {
		active := t.Active
		g.Nodes = append(g.Nodes, protocol.Node{Ref: &protocol.GraphRef{Kind: "team", ID: t.ID}, Owner: t.Owner, Name: t.Name, Kind: "team", Active: &active, Charter: protocol.Charter{Purpose: t.Purpose}, CreatedAt: t.CreatedAt})
	}
	rows, err := s.db.Query(`SELECT member_kind,member_id,team_id FROM team_members ORDER BY member_kind,member_id`)
	if err != nil {
		return g, err
	}
	defer rows.Close()
	for rows.Next() {
		var ref protocol.GraphRef
		var id string
		if err = rows.Scan(&ref.Kind, &ref.ID, &id); err != nil {
			return g, err
		}
		g.Edges = append(g.Edges, protocol.Edge{From: ref.ID, To: id, Kind: "member_of", FromRef: &ref, ToRef: &protocol.GraphRef{Kind: "team", ID: id}})
	}
	return g, rows.Err()
}

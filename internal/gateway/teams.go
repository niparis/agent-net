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
	err := row.Scan(&t.ID, &t.Name, &t.Purpose, &owner, &t.Active, &t.AuthorityMode, &t.HeldBack, &t.CreatedAt, &t.UpdatedAt)
	if owner != nil {
		t.Owner = &protocol.GraphRef{Kind: "agent", ID: *owner}
	}
	return t, err
}

// Teams includes archived identities so their history and owner remain visible.
func (s *Store) Teams() ([]protocol.Team, error) {
	rows, err := s.db.Query(`SELECT id,name,purpose,owner_agent_id,active,authority_mode,held_back,created_at,updated_at FROM teams ORDER BY id`)
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
// transaction. Membership affects lead routing but not settings or delivery revisions.
func (s *Store) WriteTeam(q protocol.TeamWrite, actor string) error {
	return s.writeTeam(q, actor, false)
}

// ValidateTeam runs all checks and validations of WriteTeam inside a transaction
// that is rolled back without committing changes.
func (s *Store) ValidateTeam(q protocol.TeamWrite, actor string) error {
	return s.writeTeam(q, actor, true)
}

func (s *Store) writeTeam(q protocol.TeamWrite, actor string, dryRun bool) error {
	if q.Action == "charter" {
		return s.SetLevelCharter(protocol.CharterSetRequest{Level: "team", Node: q.ID, Attr: q.Attr, Value: q.Value}, actor, dryRun)
	}
	if err := s.requireGraphRefs(); err != nil {
		return err
	}
	if !actorAllowed(actor, "rule") {
		return reject("only the owner or CTO may change Teams")
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
	} else if q.Action == "repo_held_back" {
		// A repo is not a Team: its change has no Team history row.
		if _, _, err = writeRepoHeldBack(tx, q, actor, now); err != nil || dryRun {
			return err
		}
		return tx.Commit()
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
				if err = refuseBotLead(tx, q.Owner.ID); err != nil {
					return err
				}
				owner = q.Owner.ID
			}
			_, err = tx.Exec(`INSERT INTO teams(id,name,purpose,owner_agent_id,created_at,updated_at) VALUES(?,?,?,?,?,?)`, q.ID, q.Name, q.Purpose, owner, now, now)
		case "rename", "purpose", "archive", "mode", "held_back":
			var t protocol.Team
			t, err = scanTeam(tx.QueryRow(`SELECT id,name,purpose,owner_agent_id,active,authority_mode,held_back,created_at,updated_at FROM teams WHERE id=?`, q.ID))
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
			switch q.Action {
			case "rename":
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
			case "mode":
				if !protocol.ValidAuthorityMode(q.Mode) {
					return reject("authority mode is absolute, charter_first or negotiate")
				}
				if t.AuthorityMode == q.Mode {
					return nil
				}
				_, err = tx.Exec(`UPDATE teams SET authority_mode=?,updated_at=? WHERE id=?`, q.Mode, now, q.ID)
			case "held_back":
				if !validHeldBack(q.HeldBack) {
					return reject("invalid held back text")
				}
				held := strings.TrimSpace(q.HeldBack)
				if t.HeldBack == held {
					return nil
				}
				_, err = tx.Exec(`UPDATE teams SET held_back=?,updated_at=? WHERE id=?`, held, now, q.ID)
			case "purpose":
				if len(q.Purpose) > protocol.CharterValueMaxBytes || strings.IndexFunc(q.Purpose, func(r rune) bool { return unicode.IsControl(r) && r != '\n' }) >= 0 {
					return reject("invalid Team purpose")
				}
				if t.Purpose == q.Purpose {
					return nil
				}
				_, err = tx.Exec(`UPDATE teams SET purpose=?,updated_at=? WHERE id=?`, q.Purpose, now, q.ID)
			default:
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
			t, err = scanTeam(tx.QueryRow(`SELECT id,name,purpose,owner_agent_id,active,authority_mode,held_back,created_at,updated_at FROM teams WHERE id=?`, q.ID))
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
	if dryRun {
		return nil
	}
	if _, err = tx.Exec(`INSERT INTO team_history(action,team_id,old_team_id,member_kind,member_id,old_value,new_value,at,actor) VALUES(?,?,?,?,?,?,?,?,?)`, q.Action, q.ID, oldTeam, kind, id, oldValue, newValue, now, actor); err != nil {
		return err
	}
	if actor == "cto" {
		attr := q.Action
		if q.Member != nil && q.Action == "membership" {
			attr += ":" + q.Member.Kind + ":" + q.Member.ID
		}
		if err = graphRecord(tx, "team", q.ID, attr, oldValue, newValue, actor, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Team looks up an active team by id or name.
func (s *Store) Team(selector string) (id, name string, err error) {
	selector = strings.TrimSpace(selector)
	err = s.db.QueryRow(`SELECT id, name FROM teams WHERE (id=? OR name=?) AND active=1`, selector, selector).Scan(&id, &name)
	if err == sql.ErrNoRows {
		return "", "", reject("unknown Team")
	}
	return id, name, err
}

// MemberTeam returns the current team ID of a member, or "" if none.
func (s *Store) MemberTeam(ref protocol.GraphRef) (teamID string, err error) {
	err = s.db.QueryRow(`SELECT team_id FROM team_members WHERE member_kind=? AND member_id=?`, ref.Kind, ref.ID).Scan(&teamID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return teamID, err
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

// TeamHistory is owner-only through the operator relay. Retained snapshots
// cannot bypass current validation.
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
		lead := t.Owner
		var session sql.NullString
		if err := s.db.QueryRow(`SELECT lead_session FROM teams WHERE id=?`, t.ID).Scan(&session); err != nil {
			return g, err
		}
		if session.Valid {
			lead = &protocol.GraphRef{Kind: "session", ID: session.String}
		}
		g.Nodes = append(g.Nodes, protocol.Node{Ref: &protocol.GraphRef{Kind: "team", ID: t.ID}, Owner: t.Owner, Lead: lead, Name: t.Name, Kind: "team", Active: &active, Charter: protocol.Charter{Purpose: t.Purpose}, AuthorityMode: t.AuthorityMode, HeldBack: t.HeldBack, CreatedAt: t.CreatedAt})
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

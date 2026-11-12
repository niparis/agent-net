package gateway

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Charter drafts (decision 0088). agent-net never writes a charter from a
// model call of its own. When a repo, agent or team has no value for a field,
// the live session that knows it is asked once per version of AGENTS.md. The
// session answers with agent_charter_draft. A draft is a suggestion until the
// owner or the CTO accepts it; accept writes it with the normal charter write.

var draftRepoFields = []string{"purpose", "maturity", "refuses"}
var draftAgentFields = []string{"accepts", "refuses", "escalate", "feature_requests", "rules"}
var draftTeamFields = []string{"purpose", "refuses"}

type draftQuerier interface {
	QueryRow(string, ...any) *sql.Row
}

// draftOwnValue is the value stored at exactly one level. It ignores
// inheritance: a draft fills a gap at its own level.
func draftOwnValue(q draftQuerier, level, scope, attr string) (string, error) {
	var value string
	var err error
	switch {
	case level == "repo":
		err = q.QueryRow(`SELECT value FROM graph_charter WHERE node=? AND attr=?`, scope, attr).Scan(&value)
	case level == "agent":
		err = q.QueryRow(`SELECT value FROM agent_charter WHERE agent_id=? AND attr=?`, scope, attr).Scan(&value)
	case level == "team" && attr == "purpose":
		err = q.QueryRow(`SELECT purpose FROM teams WHERE id=? AND active=1`, scope).Scan(&value)
	case level == "team":
		err = q.QueryRow(`SELECT value FROM charter_values WHERE level='team' AND scope=? AND attr=?`, scope, attr).Scan(&value)
	default:
		return "", reject("invalid charter level")
	}
	if err == sql.ErrNoRows {
		return "", nil
	}
	return strings.TrimSpace(value), err
}

func draftMissing(q draftQuerier, level, scope string, fields []string) ([]string, error) {
	var out []string
	for _, attr := range fields {
		v, err := draftOwnValue(q, level, scope, attr)
		if err != nil {
			return nil, err
		}
		if v == "" {
			out = append(out, attr)
		}
	}
	return out, nil
}

func draftAsked(q draftQuerier, level, scope, hash string) (bool, error) {
	var have string
	err := q.QueryRow(`SELECT source_hash FROM charter_draft_asks WHERE level=? AND scope=?`, level, scope).Scan(&have)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return have == hash, err
}

func draftFieldLines(fields []string) string {
	var b strings.Builder
	for _, attr := range fields {
		guide, _ := protocol.CharterGuideFor(attr)
		fmt.Fprintf(&b, "  - %s: %s (%s)\n", attr, strings.TrimSuffix(guide.Meaning, "."), guide.Size)
	}
	return b.String()
}

// draftSessionKind tells whether a session may be asked: a lasting coding
// session, never a worker, the owner, the CTO or a bot.
func (s *Store) draftSessionAllowed(session string) (bool, error) {
	_, _, harness, worker, err := s.sessionIdentity(session)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if worker || harness == "" || harness == "cto" || harness == "bot" || protocol.IsOwner(harness) {
		return false, nil
	}
	return true, nil
}

// CharterDraftAsk returns the one-time text that asks a live session to draft
// the charter fields that are still empty, or "" when nothing is due. hash
// identifies the session's AGENTS.md. The repo and the agent are asked once per
// hash; a team lead is asked once per set of member charters.
func (s *Store) CharterDraftAsk(session, hash string) (string, error) {
	c, err := s.resolveSessionContext(session)
	if err != nil {
		if _, ok := err.(Rejection); ok {
			return "", nil
		}
		return "", err
	}
	return s.charterDraftAskFor(session, c, hash)
}

func (s *Store) charterDraftAskFor(session string, c resolvedContext, hash string) (string, error) {
	ok, err := s.draftSessionAllowed(session)
	if err != nil || !ok {
		return "", err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	now := s.now().UnixNano()
	record := func(level, scope, h string) error {
		_, err := tx.Exec(`INSERT INTO charter_draft_asks(level,scope,source_hash,asked_at,session_id) VALUES(?,?,?,?,?) ON CONFLICT(level,scope) DO UPDATE SET source_hash=excluded.source_hash,asked_at=excluded.asked_at,session_id=excluded.session_id`, level, scope, h, now, session)
		return err
	}
	var text strings.Builder
	if hash != "" {
		var sections []string
		if c.Repo != "" {
			asked, err := draftAsked(tx, "repo", c.Repo, hash)
			if err != nil {
				return "", err
			}
			if !asked {
				missing, err := draftMissing(tx, "repo", c.Repo, draftRepoFields)
				if err != nil {
					return "", err
				}
				if len(missing) > 0 {
					sections = append(sections, fmt.Sprintf("- level repo (%s):\n%s", c.Repo, draftFieldLines(missing)))
					if err = record("repo", c.Repo, hash); err != nil {
						return "", err
					}
				}
			}
		}
		if c.Agent != "" {
			asked, err := draftAsked(tx, "agent", c.Agent, hash)
			if err != nil {
				return "", err
			}
			if !asked {
				missing, err := draftMissing(tx, "agent", c.Agent, draftAgentFields)
				if err != nil {
					return "", err
				}
				if len(missing) > 0 {
					sections = append(sections, "- level agent (your own work):\n"+draftFieldLines(missing))
					if err = record("agent", c.Agent, hash); err != nil {
						return "", err
					}
				}
			}
		}
		if len(sections) > 0 {
			text.WriteString("[agent-net] Charter draft request. Some charter fields for this project are empty.\n" +
				"Read AGENTS.md (and README.md if you need it). Then draft these fields with agent_charter_draft, one call per field (level, attr, value):\n")
			for _, section := range sections {
				text.WriteString(section)
			}
			text.WriteString("Use only what AGENTS.md says; do not guess. Keep each value short. " +
				"A draft is a suggestion: nothing changes until the owner accepts it, and a draft never grants or removes authority. " +
				"agent-net asks once, and again only if AGENTS.md changes.\n")
		}
	}
	barText, err := s.barAskText(tx, session, c, hash, record)
	if err != nil {
		return "", err
	}
	if barText != "" {
		if text.Len() > 0 {
			text.WriteString("\n")
		}
		text.WriteString(barText + "\n")
	}
	if c.Agent != "" {
		teamText, err := s.draftTeamAsk(tx, session, c.Agent, record)
		if err != nil {
			return "", err
		}
		if teamText != "" {
			if text.Len() > 0 {
				text.WriteString("\n")
			}
			text.WriteString(teamText)
		}
	}
	if text.Len() == 0 {
		return "", nil
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return strings.TrimRight(text.String(), "\n"), nil
}

type draftTeam struct{ id, name string }

// draftLedTeams lists the active teams this session leads.
func draftLedTeams(q interface {
	Query(string, ...any) (*sql.Rows, error)
}, session, agent string) ([]draftTeam, error) {
	rows, err := q.Query(`SELECT id,name FROM teams WHERE active=1 AND owner_agent_id=? AND (lead_session IS NULL OR lead_session=?) ORDER BY id`, agent, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []draftTeam
	for rows.Next() {
		var t draftTeam
		if err := rows.Scan(&t.id, &t.name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// draftTeamMembers returns the member names and a hash input that changes when
// a member's purpose or refuses change.
func draftTeamMembers(tx *sql.Tx, team string) (names []string, sig string, err error) {
	rows, err := tx.Query(`SELECT member_kind,member_id FROM team_members WHERE team_id=? ORDER BY member_kind,member_id`, team)
	if err != nil {
		return nil, "", err
	}
	type member struct{ kind, id string }
	var members []member
	for rows.Next() {
		var m member
		if err = rows.Scan(&m.kind, &m.id); err != nil {
			rows.Close()
			return nil, "", err
		}
		members = append(members, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, "", err
	}
	var b strings.Builder
	filled := false
	for _, m := range members {
		level := m.kind
		purpose, err := draftOwnValue(tx, level, m.id, "purpose")
		if err != nil {
			return nil, "", err
		}
		refuses, err := draftOwnValue(tx, level, m.id, "refuses")
		if err != nil {
			return nil, "", err
		}
		if purpose != "" || refuses != "" {
			filled = true
		}
		fmt.Fprintf(&b, "%s:%s:%s:%s\n", m.kind, m.id, purpose, refuses)
		names = append(names, m.id)
	}
	if !filled {
		return names, "", nil
	}
	return names, b.String(), nil
}

func (s *Store) draftTeamAsk(tx *sql.Tx, session, agent string, record func(level, scope, hash string) error) (string, error) {
	teams, err := draftLedTeams(tx, session, agent)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for _, t := range teams {
		missing, err := draftMissing(tx, "team", t.id, draftTeamFields)
		if err != nil {
			return "", err
		}
		if len(missing) == 0 {
			continue
		}
		names, sig, err := draftTeamMembers(tx, t.id)
		if err != nil {
			return "", err
		}
		if sig == "" {
			continue // the members have nothing to draft from yet
		}
		hash := hashText(sig)
		asked, err := draftAsked(tx, "team", t.id, hash)
		if err != nil || asked {
			if err != nil {
				return "", err
			}
			continue
		}
		if err = record("team", t.id, hash); err != nil {
			return "", err
		}
		fmt.Fprintf(&out, "[agent-net] Charter draft request. You lead team %s (%s). These team fields are empty:\n%s", t.name, t.id, draftFieldLines(missing))
		fmt.Fprintf(&out, "Members: %s. Read their charters with agent_graph_read, then draft each field with agent_charter_draft (level team, attr, value). "+
			"Team refuses is a safety limit that every member inherits, so list only what the members' charters already refuse. "+
			"Use only what the member charters say; do not guess. "+
			"A draft is a suggestion: nothing changes until the owner accepts it, and a draft never grants or removes authority. "+
			"agent-net asks once, and again only if the members' charters change.\n", strings.Join(names, ", "))
	}
	return out.String(), nil
}

// SubmitCharterDraft stores one drafted value as a pending suggestion. The
// scope is the caller's own repo, agent or led team, never a parameter.
func (s *Store) SubmitCharterDraft(session string, q protocol.CharterDraftRequest, hash string) (protocol.CharterDraftResult, error) {
	var res protocol.CharterDraftResult
	if !protocol.CharterDraftAllowed(q.Level, q.Attr) {
		return res, reject("a %s draft may not set %s", q.Level, q.Attr)
	}
	ok, err := s.draftSessionAllowed(session)
	if err != nil {
		return res, err
	}
	if !ok {
		return res, reject("only a lasting agent session may draft a charter")
	}
	value, err := protocol.CleanCharterValue(q.Attr, q.Value)
	if err != nil {
		return res, reject("%v", err)
	}
	if value = strings.TrimSpace(value); value == "" {
		return res, reject("draft value is empty")
	}
	c, err := s.resolveSessionContext(session)
	if err != nil {
		return res, err
	}
	scope, source := "", protocol.CharterDraftSource
	switch q.Level {
	case "repo":
		scope = c.Repo
	case "agent":
		scope = c.Agent
	case "team":
		source = protocol.CharterDraftTeamSource
		teams, err := draftLedTeams(s.db, session, c.Agent)
		if err != nil {
			return res, err
		}
		for _, t := range teams {
			if q.Team == "" || q.Team == t.id {
				if scope != "" {
					return res, reject("you lead several teams: name one with team")
				}
				scope = t.id
			}
		}
		if scope == "" {
			return res, reject("only a team lead may draft for its team")
		}
	}
	if scope == "" {
		return res, reject("this session has no %s to draft for", q.Level)
	}
	if own, err := draftOwnValue(s.db, q.Level, scope, q.Attr); err != nil {
		return res, err
	} else if own != "" {
		return res, reject("%s %s already has a value; the owner changes it", q.Level, q.Attr)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRow(`SELECT id FROM charter_drafts WHERE level=? AND scope=? AND attr=? AND value=? AND status='rejected' ORDER BY id DESC LIMIT 1`, q.Level, scope, q.Attr, value).Scan(&id)
	if err == nil {
		return protocol.CharterDraftResult{ID: id, Status: "rejected"}, nil
	}
	if err != sql.ErrNoRows {
		return res, err
	}
	now := s.now().UnixNano()
	err = tx.QueryRow(`SELECT id FROM charter_drafts WHERE level=? AND scope=? AND attr=? AND status='pending' ORDER BY id DESC LIMIT 1`, q.Level, scope, q.Attr).Scan(&id)
	if err == nil {
		_, err = tx.Exec(`UPDATE charter_drafts SET value=?,source=?,source_hash=?,drafted_by=?,created_at=? WHERE id=?`, value, source, hash, session, now, id)
	} else if err == sql.ErrNoRows {
		var r sql.Result
		r, err = tx.Exec(`INSERT INTO charter_drafts(level,scope,attr,value,source,source_hash,drafted_by,created_at) VALUES(?,?,?,?,?,?,?,?)`, q.Level, scope, q.Attr, value, source, hash, session, now)
		if err == nil {
			id, err = r.LastInsertId()
		}
	}
	if err != nil {
		return res, err
	}
	if err = tx.Commit(); err != nil {
		return res, err
	}
	if err = s.reconcileLearnedAsks(); err != nil {
		return res, err
	}
	return protocol.CharterDraftResult{ID: id, Status: "pending"}, nil
}

func (s *Store) draftName(level, scope string) string {
	var name string
	switch level {
	case "agent":
		_ = s.db.QueryRow(`SELECT display_name FROM agents WHERE id=?`, scope).Scan(&name)
	case "team":
		_ = s.db.QueryRow(`SELECT name FROM teams WHERE id=?`, scope).Scan(&name)
	}
	if name == "" {
		name = scope
	}
	return name
}

// PendingCharterDrafts lists the drafts that still wait for a decision. A
// draft whose field got a value some other way is marked superseded and left
// out.
func (s *Store) PendingCharterDrafts() ([]protocol.CharterDraft, error) {
	rows, err := s.db.Query(`SELECT id,level,scope,attr,value,source,status,drafted_by,created_at FROM charter_drafts WHERE status='pending' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var all []protocol.CharterDraft
	for rows.Next() {
		var d protocol.CharterDraft
		if err = rows.Scan(&d.ID, &d.Level, &d.Scope, &d.Attr, &d.Value, &d.Source, &d.Status, &d.DraftedBy, &d.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []protocol.CharterDraft{}
	for _, d := range all {
		own, err := draftOwnValue(s.db, d.Level, d.Scope, d.Attr)
		if err != nil {
			return nil, err
		}
		if own != "" {
			if _, err = s.db.Exec(`UPDATE charter_drafts SET status='superseded',decided_at=? WHERE id=? AND status='pending'`, s.now().UnixNano(), d.ID); err != nil {
				return nil, err
			}
			continue
		}
		d.Name = s.draftName(d.Level, d.Scope)
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// DecideCharterDraft accepts or rejects a pending draft. Accept writes the
// value with the normal charter write at the draft's level, as the actor (the
// owner or the CTO). The draft row keeps its source.
func (s *Store) DecideCharterDraft(id int64, action, actor string) error {
	if action != "accept" && action != "reject" {
		return reject("action must be accept or reject")
	}
	if actor != "owner" && actor != "cto" {
		return reject("only the owner or the CTO decides a charter draft")
	}
	var d protocol.CharterDraft
	err := s.db.QueryRow(`SELECT level,scope,attr,value FROM charter_drafts WHERE id=? AND status='pending'`, id).Scan(&d.Level, &d.Scope, &d.Attr, &d.Value)
	if err == sql.ErrNoRows {
		return reject("charter draft is not pending")
	}
	if err != nil {
		return err
	}
	status := "rejected"
	if action == "accept" {
		if err = s.SetLevelCharter(protocol.CharterSetRequest{Level: d.Level, Node: d.Scope, Attr: d.Attr, Value: d.Value}, actor, false); err != nil {
			return err
		}
		status = "accepted"
	}
	_, err = s.db.Exec(`UPDATE charter_drafts SET status=?,decided_at=?,decided_by=? WHERE id=?`, status, s.now().UnixNano(), actor, id)
	return err
}

// ownerDrafts shows pending drafts in the owner's learned list. They carry the
// negative of their draft ID, so the existing answer path can route them.
func (s *Store) ownerDrafts() ([]protocol.LearnedSuggestion, error) {
	drafts, err := s.PendingCharterDrafts()
	if err != nil {
		return nil, err
	}
	out := make([]protocol.LearnedSuggestion, 0, len(drafts))
	for _, d := range drafts {
		out = append(out, protocol.LearnedSuggestion{
			ID: -d.ID, Node: d.Name, Field: d.Attr, Kind: "add", Phrase: d.Value,
			Evidence: []protocol.LearnedEvidence{{ID: "draft", Excerpt: "Drafted from " + d.Source}},
			Status:   "pending", Severity: "major", Origin: "draft",
			CreatedAt: d.CreatedAt, ReadyAt: d.CreatedAt, NewValue: d.Value,
			By: d.Source, Level: d.Level, Scope: d.Scope,
		})
	}
	return out, nil
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}

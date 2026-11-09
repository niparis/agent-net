package gateway

import (
	"database/sql"
	"strings"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

// A wait that names the session's own lead (decision 0126). A member that
// announces "waiting: <lead> confirming X" is in an ordinary wait inside its
// team. The owner has nothing to do with it, so it makes no owner ask. The lead
// hears of it with the notice of decision 0111, whatever the team's asks_to.

// waitLead is the lead a wait names.
type waitLead struct {
	scope  string // "Team forge" or "Repo demo", as the notice says it
	agent  string // the lead Agent
	pinned string // the pinned lead session, or ""
}

// wordIn tells whether word stands in text as a whole word, ignoring case.
// Letters and digits join a word; a hyphen or a dot does not.
func wordIn(text, word string) bool {
	text, word = strings.ToLower(text), strings.ToLower(strings.TrimSpace(word))
	if word == "" {
		return false
	}
	for from := 0; from < len(text); {
		i := strings.Index(text[from:], word)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(word)
		from = i + 1
		if i > 0 {
			if r := []rune(text[:i]); isWordRune(r[len(r)-1]) {
				continue
			}
		}
		if end < len(text) {
			if r := []rune(text[end:])[0]; isWordRune(r) {
				continue
			}
		}
		return true
	}
	return false
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// leadsOfSession lists the leads a session has: its team lead and its repo
// lead, as lead routing finds them. The session's own agent is never its lead.
func leadsOfSession(q leadDB, session string) ([]waitLead, error) {
	var out []waitLead
	t, ok, err := memberTeamIn(q, session)
	if err != nil {
		return nil, err
	}
	if ok {
		out = append(out, waitLead{scope: "Team " + t.name, agent: t.leadAgent, pinned: t.pinned})
	}
	var agent, repo, harness, worker string
	err = q.QueryRow(`SELECT COALESCE(agent_id,''),COALESCE(repo_id,''),harness,worker FROM sessions WHERE id=?`, session).Scan(&agent, &repo, &harness, &worker)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if agent == "" || repo == "" || worker != "" || protocol.IsOwner(harness) || harness == "cto" {
		return out, nil
	}
	var repoID, repoName string
	var lead, pin sql.NullString
	err = q.QueryRow(repoLeadQuery(`id=?`), repo).Scan(&repoID, &repoName, &lead, &pin)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if lead.Valid && lead.String != "" && !isLeadSession(lead, pin, agent, session) {
		out = append(out, waitLead{scope: "Repo " + repoName, agent: lead.String, pinned: pin.String})
	}
	return out, nil
}

// leadNames are the names that point at a lead: its Agent id and display name,
// the ids of its sessions, and the pinned session.
func leadNames(q leadDB, l waitLead) ([]string, error) {
	names := []string{l.agent, l.pinned}
	var display string
	if err := q.QueryRow(`SELECT display_name FROM agents WHERE id=?`, l.agent).Scan(&display); err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	names = append(names, display)
	rows, err := q.Query(`SELECT id FROM sessions WHERE agent_id=? AND worker='' AND archived_at=0`, l.agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		names = append(names, id)
	}
	return names, rows.Err()
}

// waitNamesLead finds the lead that a wait text names, for a session. ok is
// false when it names none, or when it also names the owner: a wait that needs
// the owner keeps the owner's path. Only a text that stands as a whole word
// counts, so a lead called "demo" is not found in "demonstrate".
func (s *Store) waitNamesLead(q leadDB, session, text string) (lead waitLead, ok bool, err error) {
	text = strings.TrimSpace(text)
	if text == "" || wordIn(text, "owner") {
		return lead, false, nil
	}
	leads, err := leadsOfSession(q, session)
	if err != nil {
		return lead, false, err
	}
	for _, l := range leads {
		names, err := leadNames(q, l)
		if err != nil {
			return lead, false, err
		}
		for _, n := range names {
			if wordIn(text, n) {
				return l, true, nil
			}
		}
	}
	return lead, false, nil
}

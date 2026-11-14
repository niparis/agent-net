package gateway

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

// Authority (decision 0089). The tree is owner, team lead, repo lead, agent.
// The sender of a message may mark it as an order only when it is above the
// recipient in that tree. agent-net checks the tree on every send and writes
// the label on every delivery; a sender cannot write a label in the body.

type sessionPlace struct {
	id, agent, repo, harness, worker string
}

func loadPlace(q leadDB, id string) (sessionPlace, error) {
	p := sessionPlace{id: id}
	err := q.QueryRow(`SELECT COALESCE(agent_id,''),COALESCE(repo_id,''),harness,worker FROM sessions WHERE id=?`, id).Scan(&p.agent, &p.repo, &p.harness, &p.worker)
	if err == sql.ErrNoRows {
		return p, nil
	}
	return p, err
}

// inTree is true for a session that sits in the tree: an agent session that is
// not the owner, the CTO or a worker.
func (p sessionPlace) inTree() bool {
	return p.agent != "" && p.worker == "" && !protocol.IsOwner(p.harness) && p.harness != "cto"
}

// joinHeldBack joins the held back texts of the levels that apply, top first.
func joinHeldBack(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.Join(strings.Fields(p), " "); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "; ")
}

// repoTeam is the active team a repo belongs to, if any.
func repoTeam(q leadDB, repo string) (id, name, mode, held string, owner sql.NullString, err error) {
	if repo == "" {
		return "", "", "", "", owner, sql.ErrNoRows
	}
	err = q.QueryRow(`SELECT t.id,t.name,t.authority_mode,t.held_back,t.owner_agent_id FROM teams t JOIN team_members tm ON tm.team_id=t.id
		WHERE t.active=1 AND tm.member_kind='repo' AND tm.member_id=? ORDER BY t.id LIMIT 1`, repo).Scan(&id, &name, &mode, &held, &owner)
	return
}

func repoHeldBack(q leadDB, repo string) (string, error) {
	var held string
	err := q.QueryRow(`SELECT held_back FROM repo_held_back WHERE repo_id=?`, repo).Scan(&held)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return held, err
}

// RepoHeldBack is what the owner holds back from a repo.
func (s *Store) RepoHeldBack(repo string) (string, error) { return repoHeldBack(s.db, repo) }

// orderAbove says whether the sender is above the recipient in the tree and,
// if so, fills in what the order note needs. A team lead is above every repo,
// repo lead and agent session of its team. A repo lead is above the other
// agents that work in its repo.
func orderAbove(q leadDB, sender, recipient string, now time.Time) (protocol.OrderInfo, bool, error) {
	var none protocol.OrderInfo
	from, err := loadPlace(q, sender)
	if err != nil {
		return none, false, err
	}
	to, err := loadPlace(q, recipient)
	if err != nil {
		return none, false, err
	}
	if !from.inTree() || !to.inTree() || from.agent == to.agent {
		return none, false, nil
	}
	name := sessionLabel(q, sender)
	var id, teamName, mode, held string
	err = q.QueryRow(`SELECT t.id,t.name,t.authority_mode,t.held_back FROM teams t JOIN team_members tm ON tm.team_id=t.id
		LEFT JOIN agents a ON a.id=t.owner_agent_id
		WHERE t.active=1 AND t.owner_agent_id=? AND a.lead_session=?
		AND ((tm.member_kind='repo' AND tm.member_id=? AND tm.member_id<>'') OR (tm.member_kind='agent' AND tm.member_id=?))
		ORDER BY t.id LIMIT 1`, from.agent, sender, to.repo, to.agent).Scan(&id, &teamName, &mode, &held)
	if err == nil {
		repoHeld := ""
		if to.repo != "" {
			if repoHeld, err = repoHeldBack(q, to.repo); err != nil {
				return none, false, err
			}
		}
		return protocol.OrderInfo{Lead: name, Scope: "team", Name: teamName, Mode: mode, HeldBack: joinHeldBack(held, repoHeld), Above: "the owner"}, true, nil
	}
	if err != sql.ErrNoRows {
		return none, false, err
	}
	if to.repo == "" {
		return none, false, nil
	}
	lead, err := repoTopAgentQ(q, to.repo)
	if err != nil {
		return none, false, err
	}
	if lead == "" {
		return none, false, nil
	}
	same, err := isLeadSessionQ(q, lead, from.agent, sender)
	if err != nil {
		return none, false, err
	}
	if !same {
		return none, false, nil
	}
	repoName, err := repoNameQ(q, to.repo)
	if err != nil {
		return none, false, err
	}
	info := protocol.OrderInfo{Lead: name, Scope: "repo", Name: repoName, Mode: protocol.DefaultAuthorityMode, Above: "the owner"}
	repoHeld, err := repoHeldBack(q, to.repo)
	if err != nil {
		return none, false, err
	}
	teamHeld := ""
	_, tname, tmode, theld, towner, err := repoTeam(q, to.repo)
	if err != nil && err != sql.ErrNoRows {
		return none, false, err
	}
	if err == nil {
		info.Mode, teamHeld = tmode, theld
		if towner.Valid && towner.String != "" {
			lead, err := leadNameQ(q, towner.String, now)
			if err != nil {
				return none, false, err
			}
			info.Above = "your team lead " + lead + " (team " + tname + ")"
		}
	}
	info.HeldBack = joinHeldBack(teamHeld, repoHeld)
	return info, true, nil
}

// checkOrderFlag runs in the send transaction. It keeps the order flag when
// the sender is above the recipient, and otherwise says why the message goes
// as a request. A reply is always advice, so the flag does nothing on one.
func (s *Store) checkOrderFlag(tx *sql.Tx, m *protocol.Message) (string, error) {
	if m.Type == protocol.TypeReply {
		return "The order flag was ignored: a reply is advice, not an order.", nil
	}
	_, ok, err := orderAbove(tx, m.From, m.To, s.now())
	if err != nil {
		return "", err
	}
	if !ok {
		return fmt.Sprintf("The order flag was ignored: you are not above %s in the tree, so this went as a request.", sessionLabel(tx, m.To)), nil
	}
	m.IsOrder = true
	return "", nil
}

// authorityNote is agent-net's label for a delivered message: the order note
// for a checked order, the advice note for a reply from above, or nothing. It
// reads the live tree, so a change of mode or of lead shows on the next
// delivery, and an order whose sender has since left the tree loses its label.
func (s *Store) authorityNote(m protocol.Message) (string, error) {
	return authorityNoteFor(s.db, m, s.now())
}

func authorityNoteFor(q leadDB, m protocol.Message, now time.Time) (string, error) {
	if m.Type == protocol.TypeReply {
		_, ok, err := orderAbove(q, m.From, m.To, now)
		if err != nil || !ok {
			return "", err
		}
		return protocol.AdviceNote, nil
	}
	if !m.IsOrder {
		return "", nil
	}
	info, ok, err := orderAbove(q, m.From, m.To, now)
	if err != nil || !ok {
		return "", err
	}
	return protocol.OrderNote(info), nil
}

// validHeldBack checks the free text the owner writes. Newlines are allowed.
func validHeldBack(text string) bool {
	return len(text) <= protocol.CharterValueMaxBytes && strings.IndexFunc(text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' }) < 0
}

// writeRepoHeldBack sets the held back text of a repo.
func writeRepoHeldBack(tx *sql.Tx, q protocol.TeamWrite, actor string, now int64) (oldValue, newValue string, err error) {
	var live bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM repos WHERE id=? AND NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id))`, q.ID).Scan(&live); err != nil {
		return
	}
	if !live {
		return "", "", reject("unknown Repo")
	}
	if !validHeldBack(q.HeldBack) {
		return "", "", reject("invalid held back text")
	}
	oldValue, err = repoHeldBack(tx, q.ID)
	if err != nil {
		return
	}
	newValue = strings.TrimSpace(q.HeldBack)
	if oldValue == newValue {
		return
	}
	if newValue == "" {
		_, err = tx.Exec(`DELETE FROM repo_held_back WHERE repo_id=?`, q.ID)
	} else {
		_, err = tx.Exec(`INSERT INTO repo_held_back(repo_id,held_back) VALUES(?,?) ON CONFLICT(repo_id) DO UPDATE SET held_back=excluded.held_back`, q.ID, newValue)
	}
	if err == nil && actor == "cto" {
		err = graphRecord(tx, "repo_held_back", q.ID, "held_back", oldValue, newValue, actor, now)
	}
	return
}

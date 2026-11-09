package gateway

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Your place. Every session of a team or repo lead is told that it leads, who
// its members are, and what to do with a request that came to it from outside.
// A session in a team is told who leads it. The text is built from the live
// team data on every call, so it never goes stale.

const placeHeader = "[agent-net] Your place, from agent-net:"

// handoffInstruction is the sentence a lead gets for requests from outside. A
// Team lead does not get requests to a member repo that has its own lead: those
// go to that repo lead.
func handoffInstruction(scope string, teamLead bool) string {
	line := fmt.Sprintf("Requests from outside %s to a member come to you", scope)
	if teamLead {
		line += ", except requests to a member repo that has its own lead, which go to that lead"
	}
	return line + ". Answer each one, or hand it off with agent_message_handoff so the member answers the original sender directly."
}

// reportLine says who a lead reports to and what the owner holds back.
func reportLine(above, held, scope string) string {
	line := "You report to " + above + "."
	if h := joinHeldBack(held); h != "" {
		line += " Held back from " + scope + ": " + protocol.NeutralizeMarkers(h) + "."
	}
	return line
}

// memberOrderLine tells a member what an order from its lead means.
func memberOrderLine(mode string) string {
	switch protocol.NormalAuthorityMode(mode) {
	case protocol.AuthorityAbsolute:
		return "An order from your lead is carried out like the owner's instruction; your charter does not override it."
	case protocol.AuthorityNegotiate:
		return "You may push back on an order from your lead with a reason and a counter-proposal; the lead accepts, changes or overrules it."
	}
	return "An order from your lead binds you unless your charter refuses it; then say so, quote the charter line, and it goes up."
}

// placePurposeMax caps the team purpose in the text.
const placePurposeMax = 300

// placePurpose collapses white space and trims the purpose for the text.
func placePurpose(p string) string {
	p = strings.Join(strings.Fields(p), " ")
	if r := []rune(p); len(r) > placePurposeMax {
		p = strings.TrimSpace(string(r[:placePurposeMax])) + "..."
	}
	return p
}

func namesLine(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// placeText is the "your place" block for one session. It is empty for a
// session that leads nothing and belongs to no team, and for the owner, the
// CTO and workers.
func (s *Store) placeText(session string) (string, error) {
	var agent, repo, harness, worker string
	err := s.db.QueryRow(`SELECT COALESCE(agent_id,''),COALESCE(repo_id,''),harness,worker FROM sessions WHERE id=?`, session).Scan(&agent, &repo, &harness, &worker)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if agent == "" || worker != "" || protocol.IsOwner(harness) || harness == "cto" {
		return "", nil
	}
	var cto string
	if err = s.db.QueryRow(`SELECT COALESCE((SELECT value FROM owner_settings WHERE key='cto_session'),'')`).Scan(&cto); err != nil {
		return "", err
	}
	if cto == session {
		return "", nil
	}
	var parts []string

	// Teams this session leads.
	type teamRow struct{ id, name, purpose, mode, held string }
	var led []teamRow
	rows, err := s.db.Query(`SELECT id,name,purpose,authority_mode,held_back FROM teams WHERE active=1 AND owner_agent_id=? AND (lead_session IS NULL OR lead_session=?) ORDER BY name COLLATE NOCASE,id`, agent, session)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var t teamRow
		if err = rows.Scan(&t.id, &t.name, &t.purpose, &t.mode, &t.held); err != nil {
			rows.Close()
			return "", err
		}
		led = append(led, t)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return "", err
	}
	rows.Close()
	for _, t := range led {
		names, err := s.teamMemberNames(t.id, repo, agent)
		if err != nil {
			return "", err
		}
		lines := []string{
			fmt.Sprintf("You lead team %s (mode: %s). Members: %s.", t.name, protocol.AuthorityModeLabel(t.mode), namesLine(names)),
			reportLine("the owner", t.held, "your team"),
			handoffInstruction(t.name, true),
		}
		if purpose := placePurpose(t.purpose); purpose != "" {
			lines = append(lines, "Team purpose: "+purpose)
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}

	// Repos this session leads, when other agents work in them.
	repoRows, err := repoLeadRows(s.db, `NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id)`)
	if err != nil {
		return "", err
	}
	type repoRow struct{ id, name string }
	var ledRepos []repoRow
	for repoRows.Next() {
		var id, name string
		var lead, pinned sql.NullString
		if err = repoRows.Scan(&id, &name, &lead, &pinned); err != nil {
			repoRows.Close()
			return "", err
		}
		if lead.Valid && lead.String == agent && (!pinned.Valid || pinned.String == "" || pinned.String == session) {
			ledRepos = append(ledRepos, repoRow{id, name})
		}
	}
	if err = repoRows.Err(); err != nil {
		repoRows.Close()
		return "", err
	}
	repoRows.Close()
	for _, r := range ledRepos {
		names, err := s.repoMemberNames(r.id, agent)
		if err != nil {
			return "", err
		}
		if len(names) == 0 {
			continue
		}
		held, err := repoHeldBack(s.db, r.id)
		if err != nil {
			return "", err
		}
		above, mode := "the owner", protocol.DefaultAuthorityMode
		if _, tname, tmode, theld, towner, tpinned, terr := repoTeam(s.db, r.id); terr == nil {
			mode, held = tmode, joinHeldBack(theld, held)
			if towner.Valid && towner.String != "" {
				lead, err := s.leadName(towner.String, tpinned.String)
				if err != nil {
					return "", err
				}
				above = fmt.Sprintf("%s, who leads team %s", lead, tname)
			}
		} else if terr != sql.ErrNoRows {
			return "", terr
		}
		parts = append(parts, strings.Join([]string{
			fmt.Sprintf("You lead repo %s (mode: %s). Members: %s.", r.name, protocol.AuthorityModeLabel(mode), namesLine(names)),
			reportLine(above, held, "your repo"),
			handoffInstruction(r.name, false),
		}, "\n"))
	}

	// The team this session belongs to, when it does not lead it.
	var teamID, teamName, owner, mode, held, asksTo, leadMerges string
	var pinned sql.NullString
	err = s.db.QueryRow(`SELECT t.id,t.name,COALESCE(t.owner_agent_id,''),t.lead_session,t.authority_mode,t.held_back,t.asks_to,t.lead_merges FROM teams t JOIN team_members tm ON tm.team_id=t.id
		WHERE t.active=1 AND ((tm.member_kind='repo' AND tm.member_id=? AND tm.member_id<>'') OR (tm.member_kind='agent' AND tm.member_id=?))
		ORDER BY CASE tm.member_kind WHEN 'repo' THEN 0 ELSE 1 END,t.id LIMIT 1`, repo, agent).Scan(&teamID, &teamName, &owner, &pinned, &mode, &held, &asksTo, &leadMerges)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	leadsIt := owner == agent && (!pinned.Valid || pinned.String == "" || pinned.String == session)
	if err == nil && owner != "" && !leadsIt {
		lead, err := s.leadName(owner, pinned.String)
		if err != nil {
			return "", err
		}
		lines := []string{
			fmt.Sprintf("You are in team %s, led by %s (mode: %s).", teamName, lead, protocol.AuthorityModeLabel(mode)),
			memberOrderLine(mode),
		}
		if repoHeld, err := repoHeldBack(s.db, repo); err != nil {
			return "", err
		} else if h := joinHeldBack(held, repoHeld); h != "" {
			lines = append(lines, "Held back from your team: "+protocol.NeutralizeMarkers(h)+".")
		}
		lines = append(lines, "Escalate to your lead first, not to the owner.")
		if asksTo == protocol.AsksToLead {
			lines = append(lines, "Questions for the owner go to your lead, "+lead+".")
		}
		// The owner's grant is stated as a fact. It is not a gate: nothing is
		// blocked by it, it tells the member where a merge it cannot do goes.
		if protocol.NormalLeadMerges(leadMerges) == protocol.LeadMergesOn {
			lines = append(lines, "Merges: your lead "+lead+" may merge this team's work on the owner's grant; hand a merge you cannot do to the lead.")
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	if len(parts) == 0 {
		return "", nil
	}
	return placeHeader + "\n" + strings.Join(parts, "\n"), nil
}

// teamMemberNames lists the repos and agents of a team by display name, except
// the leader's own.
func (s *Store) teamMemberNames(team, ownRepo, ownAgent string) ([]string, error) {
	rows, err := s.db.Query(`SELECT r.display_name FROM team_members tm JOIN repos r ON r.id=tm.member_id
		WHERE tm.team_id=? AND tm.member_kind='repo' AND tm.member_id<>? AND NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=r.id)
		UNION SELECT a.display_name FROM team_members tm JOIN agents a ON a.id=tm.member_id
		WHERE tm.team_id=? AND tm.member_kind='agent' AND tm.member_id<>?`, team, ownRepo, team, ownAgent)
	if err != nil {
		return nil, err
	}
	return sortedNames(rows)
}

// repoMemberNames lists the other agents that have sessions in a repo.
func (s *Store) repoMemberNames(repo, ownAgent string) ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT a.display_name FROM sessions x JOIN agents a ON a.id=x.agent_id
		WHERE x.repo_id=? AND x.agent_id<>? AND x.worker='' AND x.archived_at=0 AND x.harness NOT IN ('owner','cto')`, repo, ownAgent)
	if err != nil {
		return nil, err
	}
	return sortedNames(rows)
}

func sortedNames(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		if n != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, rows.Err()
}

// leadName is the name of a team's live lead session, or the lead agent's
// name when no session of it is online.
func (s *Store) leadName(agent, pinned string) (string, error) {
	return leadNameQ(s.db, agent, pinned, s.now())
}

func leadNameQ(q leadDB, agent, pinned string, now time.Time) (string, error) {
	var id string
	err := liveLeadSession(q, agent, "", pinned, false, now).Scan(&id)
	if err == nil {
		return sessionLabel(q, id), nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	var name string
	err = q.QueryRow(`SELECT display_name FROM agents WHERE id=?`, agent).Scan(&name)
	if err == sql.ErrNoRows {
		return agent, nil
	}
	return name, err
}

// contextText is what context.get sends: the node charter, then the place.
func (s *Store) contextText(session string) (string, error) {
	c, err := s.resolveSessionContext(session)
	if err != nil {
		return "", err
	}
	graph, err := s.GraphFor(c.Project)
	if err != nil {
		return "", err
	}
	text := protocol.NodeContextWithSettings(graph, c.Project, c.Settings)
	// A node's effective charter already joins the global rules and refuses. A
	// session no node covers gets the global level alone.
	if text, err = s.withGlobalLevel(text); err != nil {
		return "", err
	}
	place, err := s.placeText(session)
	if err != nil {
		return "", err
	}
	if text != "" && place != "" {
		return text + "\n" + place, nil
	}
	return text + place, nil
}

// handoffNotice is the first line of a handed-off message.
func handoffNotice(lead, leadID string) string {
	return fmt.Sprintf("[agent-net] Handed off to you by %s (session %s): this request was first sent to the lead. Your reply goes to the asker directly, and the lead gets a copy.", lead, leadID)
}

// stripRoutingNote drops the lines lead routing put before a message body.
func stripRoutingNote(body string) string {
	if !strings.HasPrefix(body, "Redirected to ") {
		return body
	}
	_, rest, _ := strings.Cut(body, "\n")
	if strings.HasPrefix(rest, "You lead ") {
		_, rest, _ = strings.Cut(rest, "\n")
	}
	return rest
}

// sessionLabel is the name a session goes by in text, or its ID.
func sessionLabel(q interface{ QueryRow(string, ...any) *sql.Row }, id string) string {
	var name string
	if q.QueryRow(`SELECT COALESCE(NULLIF(p.name,''),NULLIF(s.name,''),s.id) FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=?`, id).Scan(&name) != nil || name == "" {
		return id
	}
	return name
}

// handoffCopyKey is the idempotency key of the lead's copy of a reply, so the
// gateway can find the copy it stored for a reply.
func handoffCopyKey(replyID string) string { return "handoff_copy_" + replyID }

// StoreHandoff passes a message its recipient received to another session. The
// new message keeps the original sender and conversation, so the new
// recipient's reply goes straight to the asker. The lead's own copy counts as
// answered.
func (s *Store) StoreHandoff(p protocol.MsgHandoff) (protocol.Message, error) {
	var none protocol.Message
	if p.MessageID == "" || p.Recipient == "" {
		return none, reject("hand-off needs a message and a session")
	}
	if p.Sender == p.Recipient {
		return none, reject("cannot hand a message off to yourself")
	}
	if len(p.Note) > protocol.MaxBodyBytes {
		return none, reject("note is %d bytes, the limit is %d", len(p.Note), protocol.MaxBodyBytes)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return none, err
	}
	defer tx.Rollback()
	key := "handoff_" + p.Sender + "_" + p.MessageID + "_" + p.Recipient
	if p.IdempotencyKey != "" {
		key += "_" + p.IdempotencyKey
	}
	orig, ok, err := s.message(tx, "m.id = ?", p.MessageID)
	if err != nil {
		return none, err
	}
	if !ok {
		return none, reject("unknown message %q", p.MessageID)
	}
	if first, ok, err := s.message(tx, "m.sender = ? AND m.idem_key = ?", orig.From, key); err != nil {
		return none, err
	} else if ok {
		return first, nil
	}
	if orig.To != p.Sender {
		return none, reject("message %s was not sent to you", p.MessageID)
	}
	if orig.Type == protocol.TypeReply {
		return none, reject("%s is a reply; hand off a request or a notification", p.MessageID)
	}
	if orig.From == p.Recipient {
		return none, reject("%s came from that session; reply to it instead", p.MessageID)
	}
	var n int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = ?`, p.Recipient).Scan(&n); err != nil || n == 0 {
		return none, reject("unknown session %q", p.Recipient)
	}
	if !s.allow(p.Sender) {
		return none, reject("rate limit: at most %d messages per minute per session", protocol.RatePerMinute)
	}
	lead := sessionLabel(tx, p.Sender)
	body := handoffNotice(lead, p.Sender)
	if note := strings.TrimSpace(p.Note); note != "" {
		body += "\nNote from the lead: " + note
	}
	body += "\n\n" + stripRoutingNote(orig.Body)
	now := s.now()
	m := protocol.Message{
		ID: protocol.NewID("m"), ConversationID: orig.ConversationID, From: orig.From, To: p.Recipient, Type: orig.Type,
		Body: body, Hop: orig.Hop + 1, IdempotencyKey: key, CreatedAt: now.UTC(), ExpiresAt: now.Add(protocol.DefaultTTL).UTC(),
		State: protocol.StateStored,
	}
	// The request rule from the asker to the member applies as it does to a
	// request sent straight to the member.
	if m.Type == protocol.TypeRequest {
		rule, err := requestRuleTx(tx, m.From, m.To)
		if err != nil {
			return none, err
		}
		if rule == "never" {
			return none, reject("refused by your user's rule")
		}
		if rule == "ask" {
			m.State = protocol.StateHeld
		}
	}
	workerTask := false
	if _, err = tx.Exec(`INSERT INTO messages (id, conversation_id, parent_id, sender, recipient, type, body, hop,
		idem_key, created_at, expires_at, state, sender_cto, owner_approved, worker_task, handed_off_by) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.ConversationID, "", m.From, m.To, m.Type, m.Body, m.Hop,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State, false, false, workerTask, p.Sender); err != nil {
		return none, err
	}
	if m.State == protocol.StateHeld {
		if err := s.openHeldAsk(tx, m.ID, m.From, m.To, m.Body); err != nil {
			return none, err
		}
	}
	if _, err = tx.Exec(`UPDATE messages SET state = ? WHERE id = ? AND state IN (?,?,?)`,
		protocol.StateReplied, orig.ID, protocol.StateStored, protocol.StateDispatched, protocol.StateDelivered); err != nil {
		return none, err
	}
	if _, err = tx.Exec(`DELETE FROM delivery_retries WHERE message_id=? AND EXISTS (SELECT 1 FROM messages WHERE id=? AND state='replied')`, orig.ID, orig.ID); err != nil {
		return none, err
	}
	s.event(tx, "msg.handoff", "", p.Sender, m.ID, strings.TrimPrefix(pairs("from", orig.ID, "to", m.To, "bytes", len(m.Body)), " "))
	if err = tx.Commit(); err != nil {
		return none, err
	}
	s.logMessage(m)
	out, _, err := s.Message(m.ID)
	return out, err
}

// storeHandoffCopy stores the lead's copy of a reply to a handed-off message,
// in the transaction that stores the reply. It does nothing for other replies.
func (s *Store) storeHandoffCopy(tx *sql.Tx, parentID string, reply protocol.Message) error {
	var lead string
	if err := tx.QueryRow(`SELECT handed_off_by FROM messages WHERE id=?`, parentID).Scan(&lead); err != nil || lead == "" {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	body := fmt.Sprintf("[agent-net] Copy of %s's reply to %s, on the request you handed off (message %s). This is for your information; the asker already has it.\n\n%s",
		sessionLabel(tx, reply.From), sessionLabel(tx, reply.To), parentID, reply.Body)
	now := s.now()
	_, err := tx.Exec(`INSERT INTO messages (id, conversation_id, parent_id, sender, recipient, type, body, hop,
		idem_key, created_at, expires_at, state, sender_cto, owner_approved, worker_task, handed_off_by) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		protocol.NewID("m"), reply.ConversationID, "", reply.From, lead, protocol.TypeNotification, body, reply.Hop+1,
		handoffCopyKey(reply.ID), now.UnixNano(), now.Add(protocol.DefaultTTL).UnixNano(), protocol.StateStored, false, false, false, "")
	return err
}

// HandoffCopy returns the lead's copy of a reply, if the gateway stored one.
func (s *Store) HandoffCopy(replyID string) (protocol.Message, bool, error) {
	var sender string
	if err := s.db.QueryRow(`SELECT sender FROM messages WHERE id=?`, replyID).Scan(&sender); err != nil {
		if err == sql.ErrNoRows {
			return protocol.Message{}, false, nil
		}
		return protocol.Message{}, false, err
	}
	return s.message(s.db, "m.sender = ? AND m.idem_key = ?", sender, handoffCopyKey(replyID))
}

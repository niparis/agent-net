package gateway

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// A team can send the questions of its members to its lead instead of the
// owner (decision 0094). RedirectOwnerAsk is the one place that does it. Both
// ways a session asks the owner, the inbox request and the explicit wait of a
// plugin, call it first.

// leadAskRefPrefix starts the source key of an ask that went to a team lead.
// The key ends with the id of the request message that carries the question.
const leadAskRefPrefix = "lead_ask:"

// askerTeam finds the active team that sends the questions of a session to its
// lead. The session's repo team comes first, then the team of its agent, as
// lead routing does. ok is false when the session is in no such team, or is
// the lead itself.
func (s *Store) askerTeam(session string) (teamName, leadAgent, pinned string, ok bool, err error) {
	var agent, repo, harness, worker string
	err = s.db.QueryRow(`SELECT COALESCE(agent_id,''),COALESCE(repo_id,''),harness,worker FROM sessions WHERE id=?`, session).Scan(&agent, &repo, &harness, &worker)
	if err == sql.ErrNoRows {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	// Only agents in the tree have a lead. The owner, the CTO and workers
	// (which their caller answers) do not.
	if agent == "" || worker != "" || protocol.IsOwner(harness) || harness == "cto" {
		return "", "", "", false, nil
	}
	var teamID, asksTo string
	var lead, pin sql.NullString
	err = s.db.QueryRow(`SELECT t.id,t.name,t.owner_agent_id,t.lead_session,t.asks_to FROM teams t JOIN team_members tm ON tm.team_id=t.id
		WHERE t.active=1 AND ((tm.member_kind='repo' AND tm.member_id=? AND tm.member_id<>'') OR (tm.member_kind='agent' AND tm.member_id=?))
		ORDER BY CASE tm.member_kind WHEN 'repo' THEN 0 ELSE 1 END,t.id LIMIT 1`, repo, agent).Scan(&teamID, &teamName, &lead, &pin, &asksTo)
	if err == sql.ErrNoRows {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	if asksTo != protocol.AsksToLead || !lead.Valid || lead.String == "" || isLeadSession(lead, pin, agent, session) {
		return "", "", "", false, nil
	}
	return teamName, lead.String, pin.String, true, nil
}

// leadAskSession picks the session of the lead that gets the question: a live
// one, otherwise the latest one the lead agent had. The message waits for it.
func (s *Store) leadAskSession(leadAgent, pinned string) (id, name string, err error) {
	err = liveLeadSession(s.db, leadAgent, "", pinned, true, s.now()).Scan(&id, &name)
	if err != sql.ErrNoRows {
		return id, name, err
	}
	err = s.db.QueryRow(`SELECT s.id,a.display_name FROM sessions s JOIN agents a ON a.id=s.agent_id
		WHERE s.agent_id=? AND s.worker='' AND s.archived_at=0 AND s.harness NOT IN ('owner','cto') ORDER BY s.last_seen DESC,s.id LIMIT 1`, leadAgent).Scan(&id, &name)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	return id, name, err
}

// RedirectOwnerAsk sends the ask of a session to its team lead when the
// session's team has asks_to=lead. It returns nil, and does nothing, when the
// ask belongs to the owner: the team sends questions to the owner, the session
// is outside a team or is the lead, or the lead has no session. The caller
// stores the returned message as sent and dispatches it.
//
// The ask becomes a request message from the asker to the lead, and a Desk ask
// that waits on the lead. The lead answers with a normal reply; that reply
// reaches the asker like any answer and closes the ask.
func (s *Store) RedirectOwnerAsk(item protocol.InboxItem) (*protocol.AskRedirect, protocol.Message, error) {
	var none protocol.Message
	// An empty text only withdraws an earlier ask; that is the owner path.
	if strings.TrimSpace(item.Text) == "" {
		return nil, none, nil
	}
	teamName, leadAgent, pinned, ok, err := s.askerTeam(item.SessionID)
	if err != nil || !ok {
		return nil, none, err
	}
	leadSession, leadName, err := s.leadAskSession(leadAgent, pinned)
	if err != nil || leadSession == "" || leadSession == item.SessionID {
		return nil, none, err
	}
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: item.SessionID, Recipient: leadSession, Type: protocol.TypeRequest, Body: leadAskBody(s, item, teamName)})
	if err != nil {
		return nil, none, err
	}
	// The ask waits on the lead and is not in front of the owner.
	item.NextWho = leadSession
	item.MessageID, item.ConversationID = m.ID, m.ConversationID
	item.SourceRef = leadAskRefPrefix + m.ID
	if item.AboutKind == "" && item.Node != "" {
		item.AboutKind, item.AboutID = protocol.AskAboutProject, item.Node
	}
	created, err := s.CreateInboxItem(item)
	if err != nil {
		return nil, m, err
	}
	return &protocol.AskRedirect{
		Team: teamName, Lead: leadName, LeadSession: leadSession, MessageID: m.ID, ItemID: created.ID,
		Note: fmt.Sprintf("Your team (%s) sends questions to its lead, %s. Sent there; carry on with work that does not need the answer.", teamName, leadName),
	}, m, nil
}

// leadAskBody is the request the lead reads. It carries the question as the
// asker wrote it, and says what stays with the owner.
func leadAskBody(s *Store, item protocol.InboxItem, team string) string {
	var b strings.Builder
	asker := item.AskerName
	if asker == "" || strings.HasPrefix(asker, "s_") {
		asker = sessionLabel(s.db, item.SessionID)
	}
	what := "A question"
	switch item.Kind {
	case "approval":
		what = "An approval request"
	case "feature_idea":
		what = "An idea"
	}
	fmt.Fprintf(&b, "%s for the owner from %s. Team %s sends questions to its lead, so you answer first.\n\n", what, asker, team)
	b.WriteString(strings.TrimSpace(item.Text))
	if r := strings.TrimSpace(item.Recommendation); r != "" {
		b.WriteString("\n\nTheir recommendation: " + r)
	}
	if len(item.Options) > 0 {
		b.WriteString("\n\nOptions: " + strings.Join(item.Options, " | "))
	}
	b.WriteString("\n\nAnswer with agent_message_reply to this message. The owner always holds back " + protocol.AlwaysHeldBack +
		": if this needs the owner, ask them yourself with agent_owner_ask.")
	return b.String()
}

// closeLeadAsk closes the Desk ask of a question that a team lead answered.
// reply is the stored reply message. It runs in the transaction that stores
// the reply, so the ask never stays open once the lead has answered.
func (s *Store) closeLeadAsk(q askDB, reply protocol.Message) error {
	_, err := q.Exec(`UPDATE inbox_items SET status='answered',answer=?,answered_at=?
		WHERE status='open' AND source_ref LIKE ? AND conversation_id=? AND session_id=?`,
		reply.Body, s.now().UnixNano(), leadAskRefPrefix+"%", reply.ConversationID, reply.To)
	return err
}

// askingSession checks that a session may ask the owner from this node, and
// returns it with the project and the name its ask carries.
func (s *Store) askingSession(node, session string) (agent protocol.AgentView, project, name string, err error) {
	agent, ok, err := s.Agent(session)
	if err != nil {
		return agent, "", "", err
	}
	if !ok || agent.NodeID != node || agent.Harness == protocol.OwnerHarness {
		return agent, "", "", reject("session does not belong to this node")
	}
	identity, err := s.resolveSessionContext(agent.SessionID)
	if err != nil {
		return agent, "", "", err
	}
	// Preserve the old Inbox node for sessions an old daemon reported
	// without verified repository metadata.
	project = identity.Project
	if agent.Repo.Project == "" {
		project = ""
	}
	name = agent.Profile.Name
	if name == "" {
		name = agent.Title
	}
	if name == "" || strings.HasPrefix(name, "s_") {
		name = "an agent"
	}
	return agent, project, name, nil
}

package gateway

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/niparis/agent-net/internal/protocol"
)

// CreateInboxItem stores an item for the owner. Callers must derive the session
// and node from authenticated state rather than trusting agent-supplied values.
func (s *Store) CreateInboxItem(item protocol.InboxItem) (protocol.InboxItem, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return item, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if item.Replaces != "" {
		var old protocol.InboxItem
		var created, answered int64
		var options string
		err := tx.QueryRow(`SELECT `+inboxColumns+` FROM inbox_items WHERE id=?`, item.Replaces).
			Scan(&old.ID, &old.Kind, &old.SessionID, &old.Node, &old.Text, &old.Recommendation, &old.MessageID, &old.ConversationID, &created, &old.Status, &old.Answer, &answered, &options, &old.AskerName, &old.ChangeTitle)
		if err == sql.ErrNoRows {
			return item, reject("unknown inbox item %q", item.Replaces)
		}
		if err != nil {
			return item, err
		}
		if old.Status != "open" {
			return item, reject("inbox item is not pending")
		}
		if old.SessionID != item.SessionID {
			return item, reject("inbox item %q does not belong to this session", item.Replaces)
		}
		if _, err := tx.Exec(`UPDATE inbox_items SET status='replaced',answered_at=? WHERE id=?`, now.UnixNano(), item.Replaces); err != nil {
			return item, err
		}
		if strings.TrimSpace(item.Text) == "" {
			old.Status = "replaced"
			old.AnsweredAt = now
			if err := tx.Commit(); err != nil {
				return item, err
			}
			return old, nil
		}
	}
	item, err = s.createInboxItem(tx, item)
	if err != nil {
		return item, err
	}
	if err = tx.Commit(); err != nil {
		return item, err
	}
	return item, nil
}

// OtherOpenAsks lists the open owner asks of a session other than one item, by
// ID with the first line of each. A new ask does not retire the earlier ones;
// the asking agent sees this list and names one with replaces to retire it.
func (s *Store) OtherOpenAsks(session, except string) ([]protocol.OpenAskRef, error) {
	rows, err := s.db.Query(`SELECT id,kind,text FROM inbox_items WHERE session_id=? AND status='open' AND kind IN ('question','approval','feature_idea') AND id<>? ORDER BY created_at,rowid`, session, except)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.OpenAskRef{}
	for rows.Next() {
		var r protocol.OpenAskRef
		if err := rows.Scan(&r.ID, &r.Kind, &r.Text); err != nil {
			return nil, err
		}
		r.Text = askFirstLine(r.Text, 100)
		out = append(out, r)
	}
	return out, rows.Err()
}

// RequestWrittenApproval adds the exact action to the owner's inbox.
func (s *Store) RequestWrittenApproval(session, node string, req protocol.ApprovalRequest) (protocol.InboxItem, error) {
	return s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: session, Node: node, Text: req.Action, Recommendation: req.Recommendation})
}

func (s *Store) createInboxItem(db askDB, item protocol.InboxItem) (protocol.InboxItem, error) {
	switch item.Kind {
	case "approval", "feature_idea", "question", "rejection", "escalation", "check", "session_stall",
		protocol.AskKindHeld, protocol.AskKindLearned, protocol.AskKindDraft, protocol.AskKindProposal, protocol.AskKindPermission, protocol.AskKindStep, protocol.AskKindWaiting,
		protocol.AskKindBrief, protocol.AskKindSignIn, protocol.AskKindMachine:
	default:
		return item, reject("invalid inbox kind")
	}
	if strings.TrimSpace(item.SessionID) == "" || strings.TrimSpace(item.Text) == "" {
		return item, reject("inbox session and text are required")
	}
	if (item.Kind == "question" || item.Kind == "approval" || item.Kind == "feature_idea") && utf8.RuneCountInString(item.Text) > protocol.MaxOwnerAskChars {
		return item, reject("inbox text too long")
	}
	if len(item.Text) > protocol.MaxBodyBytes || len(item.Recommendation) > protocol.MaxBodyBytes || len(item.Detail) > protocol.MaxBodyBytes {
		return item, reject("inbox text too long")
	}
	// An ask goes to the chief of staff when one is live (decision 0114). A
	// caller that names the next who has routed the ask on purpose, so chief
	// adoption leaves it there.
	routeFixed := 0
	if item.NextWho != "" {
		routeFixed = 1
	}
	nextWho, skipped, err := s.chiefRoute(db, item)
	if err != nil {
		return item, err
	}
	if skipped {
		// The chief is registered but cannot work: the ask was sent past it.
		routeFixed = 1
	}
	if len(item.Options) > 4 {
		return item, reject("too many options")
	}
	options, err := json.Marshal(item.Options)
	if err != nil {
		return item, err
	}
	// One open ask per source key: a repeat refreshes the text it shows.
	if item.SourceRef != "" {
		var id string
		err := db.QueryRow(`SELECT id FROM inbox_items WHERE source_ref=? AND status='open'`, item.SourceRef).Scan(&id)
		if err == nil {
			_, err = db.Exec(`UPDATE inbox_items SET text=?,recommendation=?,options=?,detail=?,why_you=? WHERE id=?`,
				item.Text, item.Recommendation, string(options), item.Detail, item.WhyYou, id)
			item.ID = id
			return item, err
		}
		if err != sql.ErrNoRows {
			return item, err
		}
	}
	item.ID = protocol.NewID("i")
	item.CreatedAt = s.now().UTC()
	item.Status = "open"
	item.Answer = ""
	item.AnsweredAt = time.Time{}
	_, err = db.Exec(`INSERT INTO inbox_items (id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,options,asker_name,change_title,
		from_kind,source_ref,about_kind,about_id,next_who,why_you,detail,route_fixed) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.Kind, item.SessionID, item.Node, item.Text, item.Recommendation, item.MessageID, item.ConversationID, item.CreatedAt.UnixNano(), item.Status, string(options), item.AskerName, item.ChangeTitle,
		item.FromKind, item.SourceRef, item.AboutKind, item.AboutID, nextWho, item.WhyYou, item.Detail, routeFixed)
	return item, err
}

// sourceRefID reads the numeric ID from a source key such as draft:12.
func sourceRefID(ref, prefix string) (int64, error) {
	if !strings.HasPrefix(ref, prefix) {
		return 0, reject("unknown source key")
	}
	return strconv.ParseInt(strings.TrimPrefix(ref, prefix), 10, 64)
}

type inboxScanner interface{ Scan(...any) error }

func scanInbox(row inboxScanner) (protocol.InboxItem, error) {
	var item protocol.InboxItem
	var created, answered int64
	var options string
	err := row.Scan(&item.ID, &item.Kind, &item.SessionID, &item.Node, &item.Text, &item.Recommendation, &item.MessageID, &item.ConversationID, &created, &item.Status, &item.Answer, &answered, &options, &item.AskerName, &item.ChangeTitle)
	if err != nil {
		return item, err
	}
	if err := json.Unmarshal([]byte(options), &item.Options); err != nil {
		return item, err
	}
	item.CreatedAt = time.Unix(0, created).UTC()
	if answered != 0 {
		item.AnsweredAt = time.Unix(0, answered).UTC()
	}
	return item, nil
}

const inboxColumns = `id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,answer,answered_at,options,asker_name,change_title`

// inboxTables are the tables inboxRead reads: the items, the delivery steps and
// their changes, the names of the askers (agentNames: sessions, nodes, profiles)
// and the pipeline of a delivery step, which reads the graph and the settings.
// Keep it equal to those queries (see readThrough).
var inboxTables = tableUnion([]string{"inbox_items", "delivery_step_inbox", "delivery_changes", "sessions", "nodes", "profiles"}, graphTables)

// ListInbox lists the open and recent inbox items. The read is kept until one
// of inboxTables is written, and never past the second it was read in: the
// "connected" rule compares with the clock. Each caller gets a copy.
func (s *Store) ListInbox() ([]protocol.InboxItem, error) {
	v, err := s.readThrough("inbox", inboxTables, true, func() (any, error) { return s.inboxRead() })
	if err != nil {
		return nil, err
	}
	return copyOf(v.([]protocol.InboxItem)), nil
}

func (s *Store) inboxRead() ([]protocol.InboxItem, error) {
	// An open ask that waits on someone other than the owner (a team lead) is
	// not the owner's Inbox item.
	rows, err := s.db.Query(`SELECT ` + inboxColumns + ` FROM inbox_items WHERE NOT (status='open' AND next_who NOT IN ('owner','')) ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	items := []protocol.InboxItem{}
	for rows.Next() {
		item, err := scanInbox(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if item.Kind == "check" && item.Node == "apps" && strings.HasPrefix(item.Text, "Connected: ") && !item.CreatedAt.After(s.now().Add(-24*time.Hour)) {
			continue
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var askers map[string]string
	for i := range items {
		if items[i].AskerName != "" {
			continue
		}
		if askers == nil {
			if askers, err = s.agentNames(); err != nil {
				askers = map[string]string{}
			}
		}
		items[i].AskerName = askers[items[i].SessionID]
		if items[i].AskerName == "" || strings.HasPrefix(items[i].AskerName, "s_") {
			items[i].AskerName = "an agent"
		}
	}
	byID := map[string]int{}
	for i := range items {
		byID[items[i].ID] = i
	}
	rows, err = s.db.Query(`SELECT d.item_id,d.change_id,d.step,d.for_step,d.after_merge,d.commit_hash,c.project,c.title
		FROM delivery_step_inbox d JOIN delivery_changes c ON c.id=d.change_id`)
	if err != nil {
		return nil, err
	}
	type deliveryItem struct {
		id, project, title string
		delivery           protocol.InboxDelivery
	}
	var deliveries []deliveryItem
	for rows.Next() {
		var d deliveryItem
		var after int
		if err := rows.Scan(&d.id, &d.delivery.ChangeID, &d.delivery.Step, &d.delivery.For, &after, &d.delivery.Commit, &d.project, &d.title); err != nil {
			rows.Close()
			return nil, err
		}
		d.delivery.AfterMerge = after != 0
		deliveries = append(deliveries, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	pipelines := map[string]protocol.Pipeline{}
	for _, d := range deliveries {
		i, ok := byID[d.id]
		if !ok {
			continue
		}
		p, ok := pipelines[d.project]
		if !ok {
			p, err = s.deliveryPipeline(d.project)
			if err != nil {
				return nil, err
			}
			pipelines[d.project] = p
		}
		d.delivery.ReportFields = p.ReportFields(d.delivery.StepRef)
		items[i].Delivery = &d.delivery
		if items[i].ChangeTitle == "" {
			items[i].ChangeTitle = d.title
		}
	}
	return items, nil
}

// AnswerInbox records the owner's answer.
func (s *Store) AnswerInbox(id, answer string, validate ...bool) (protocol.InboxItem, error) {
	return s.AnswerInboxAs(id, answer, "owner", validate...)
}

// AnswerInboxAs records an answer from the owner or the CTO and queues its
// delivery in one transaction.
func (s *Store) AnswerInboxAs(id, answer, actor string, validate ...bool) (protocol.InboxItem, error) {
	if actor == "" {
		actor = "owner"
	}
	if actor != "owner" && actor != "cto" {
		return protocol.InboxItem{}, reject("unknown answer actor")
	}
	if item, handled, err := s.answerDeliveryApproval(id, answer, validate...); handled || err != nil {
		return item, err
	}
	if item, handled, err := s.answerLoopAsk(id, answer, validate...); handled || err != nil {
		return item, err
	}
	if item, handled, err := s.answerStartAsk(id, answer, actor, validate...); handled || err != nil {
		return item, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.InboxItem{}, err
	}
	defer tx.Rollback()
	item, err := scanInbox(tx.QueryRow(`SELECT `+inboxColumns+` FROM inbox_items WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return item, reject("inbox item not found")
	}
	if err != nil {
		return item, err
	}
	if item.Status != "open" {
		return item, reject("inbox item already answered")
	}
	if strings.TrimSpace(answer) == "" || len(answer) > protocol.MaxBodyBytes {
		return item, reject("invalid answer")
	}
	if item.Kind == protocol.AskKindBrief && answer != "dismiss" && answer != "seen" {
		return item, reject("a brief is informational: dismiss it only")
	}
	// Dismissing closes the item without answering it. It needs no recipient:
	// an old approval can outlive the session that asked for it, and the owner
	// must still be able to clear it from the Inbox. "seen" is the older
	// notice answer and means the same thing.
	if answer == "dismiss" || answer == "seen" {
		item.Status = "dismissed"
		item.Answer = ""
		item.AnsweredAt = s.now().UTC()
		if _, err = tx.Exec(`UPDATE inbox_items SET status=?,answer=?,answered_at=? WHERE id=?`, item.Status, item.Answer, item.AnsweredAt.UnixNano(), id); err != nil {
			return item, err
		}
		if err = s.recordOwnerAnswer(tx, item, answer); err != nil {
			return item, err
		}
		if item.Kind == protocol.AskKindPermission {
			if err = s.rejectPermissionTx(tx, id); err != nil {
				return item, err
			}
		}
		if len(validate) > 0 && validate[0] {
			return item, nil
		}
		if err = tx.Commit(); err != nil {
			return item, err
		}
		return item, nil
	}
	// A Claude Code permission proposal is an ask for the gateway, not for a
	// live session. Only the owner answers it. The gateway applies it after
	// this call returns, and only for an approved ask.
	if item.Kind == protocol.AskKindPermission {
		return s.answerPermissionAsk(tx, item, answer, actor, validate...)
	}
	// A charter draft is an ask for the gateway, not for a live session. Its
	// answer applies the drafted value exactly as the owner's TUI accept does,
	// then closes the Inbox row. Nothing is delivered to a session.
	if item.Kind == protocol.AskKindDraft {
		var ref string
		if err = tx.QueryRow(`SELECT source_ref FROM inbox_items WHERE id=?`, id).Scan(&ref); err != nil {
			return item, err
		}
		// The ask of a node holds all its fields; the answer may name one.
		if strings.HasPrefix(ref, draftGroupRefPrefix) {
			_ = tx.Rollback()
			return s.answerDraftGroup(item, ref, answer, actor, len(validate) > 0 && validate[0])
		}
		if answer != "accept" && answer != "reject" {
			return item, reject("a charter draft answer must be accept or reject")
		}
		draftID, parseErr := sourceRefID(ref, "draft:")
		if parseErr != nil {
			return item, reject("charter draft ask has no draft")
		}
		item.Status = "answered"
		item.Answer = answer
		item.AnsweredAt = s.now().UTC()
		if len(validate) > 0 && validate[0] {
			var status string
			if err = tx.QueryRow(`SELECT status FROM charter_drafts WHERE id=?`, draftID).Scan(&status); err != nil {
				if err == sql.ErrNoRows {
					return item, reject("charter draft is not pending")
				}
				return item, err
			}
			if status != "pending" {
				return item, reject("charter draft is not pending")
			}
			return item, nil
		}
		// DecideCharterDraft opens its own transaction, so release this one
		// first: the store has a single connection.
		_ = tx.Rollback()
		if err = s.DecideCharterDraft(draftID, answer, actor); err != nil {
			return item, err
		}
		return item, s.closeAnsweredItem(item, answer)
	}
	switch answer {
	case "approve":
		item.Status = "approved"
	case "refuse":
		item.Status = "refused"
	default:
		item.Status = "answered"
	}
	item.Answer = answer
	item.AnsweredAt = s.now().UTC()
	if _, err = tx.Exec(`UPDATE inbox_items SET status=?,answer=?,answered_at=? WHERE id=?`, item.Status, item.Answer, item.AnsweredAt.UnixNano(), id); err != nil {
		return item, err
	}
	// The CTO answers for the owner, on his instructions: it is his decision.
	if err = s.recordOwnerAnswer(tx, item, answer); err != nil {
		return item, err
	}
	var sender string
	err = tx.QueryRow(`SELECT o.id FROM sessions o JOIN sessions r ON r.node_id=o.node_id WHERE r.id=? AND o.harness=? LIMIT 1`, item.SessionID, protocol.OwnerHarness).Scan(&sender)
	if err == sql.ErrNoRows {
		// The owner session may be on another machine, or gone, while the
		// asking session is offline. The answer still waits for it: fall back
		// to any owner session, then to the reserved gateway sender.
		err = tx.QueryRow(`SELECT id FROM sessions WHERE harness=? LIMIT 1`, protocol.OwnerHarness).Scan(&sender)
		if err == sql.ErrNoRows {
			sender, err = gatewayAsker, nil
		}
	}
	if err != nil {
		return item, err
	}
	note := ""
	if item.Kind == "approval" && answer == "approve" {
		note = protocol.WrittenApprovalNote("the owner", protocol.NeutralizeMarkers(item.Text), item.AnsweredAt)
	}
	_, err = tx.Exec(`INSERT INTO messages (id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state,approval_note) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		protocol.NewID("m"), protocol.NewID("c"), "", sender, item.SessionID, protocol.TypeNotification, answer, 0, protocol.NewID("k"), item.AnsweredAt.UnixNano(), item.AnsweredAt.Add(protocol.DefaultTTL).UnixNano(), protocol.StateStored, note)
	if err != nil {
		return item, err
	}
	if item.Kind == "rejection" || item.Kind == "escalation" {
		if _, err = tx.Exec(`UPDATE graph_outcomes SET resolution='answered' WHERE reply_id=? AND resolution=''`, item.MessageID); err != nil {
			return item, err
		}
	}
	if len(validate) > 0 && validate[0] {
		return item, nil
	}
	if err = tx.Commit(); err != nil {
		return item, err
	}
	return item, nil
}

// CloseArchivedSessionInbox closes only requests belonging to a session on the authenticated node.
func (s *Store) CloseArchivedSessionInbox(node, session string) error {
	_, err := s.db.Exec(`UPDATE inbox_items SET status='closed',answer='session_archived',answered_at=? WHERE session_id=? AND kind IN ('question','approval','feature_idea','waiting','session_stall','check') AND from_kind IN ('','session') AND status='open' AND EXISTS (SELECT 1 FROM sessions WHERE id=? AND node_id=?)`, s.now().UnixNano(), session, session, node)
	return err
}

// agentNames reads the display name of every agent Store.Agent would find, in
// one query: the profile name, else the session name, else the title. It does
// not build the graph or the settings snapshot.
func (s *Store) agentNames() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT s.id, COALESCE(NULLIF(p.name,''), s.name), s.title
		FROM sessions s LEFT JOIN nodes n ON n.id = s.node_id LEFT JOIN profiles p ON p.session_id = s.id
		WHERE s.archived_at=0 AND (n.id IS NOT NULL OR s.harness='bot')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[string]string{}
	for rows.Next() {
		var id, name, title string
		if err := rows.Scan(&id, &name, &title); err != nil {
			return nil, err
		}
		if name == "" {
			name = title
		}
		names[id] = name
	}
	return names, rows.Err()
}

func (s *Store) askArea(tx askDB, item protocol.InboxItem) string {
	var aboutKind, aboutID string
	_ = tx.QueryRow(`SELECT about_kind, about_id FROM inbox_items WHERE id=?`, item.ID).Scan(&aboutKind, &aboutID)

	var project string
	if aboutKind == "change" && aboutID != "" {
		_ = tx.QueryRow(`SELECT project FROM delivery_changes WHERE id=?`, aboutID).Scan(&project)
		if project != "" {
			return project
		}
	}
	_ = tx.QueryRow(`SELECT c.project FROM delivery_step_inbox d JOIN delivery_changes c ON c.id=d.change_id WHERE d.item_id=?`, item.ID).Scan(&project)
	if project != "" {
		return project
	}

	if (aboutKind == "project" || aboutKind == "repo" || aboutKind == "team") && aboutID != "" {
		return aboutID
	}

	if item.SessionID != "" {
		var repoProject, profProject, repoID string
		_ = tx.QueryRow(`SELECT COALESCE(s.repo_project,''), COALESCE(p.project,''), COALESCE(s.repo_id,'')
			FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=?`, item.SessionID).Scan(&repoProject, &profProject, &repoID)
		if repoProject != "" {
			return repoProject
		}
		if profProject != "" {
			return profProject
		}
		if repoID != "" {
			return repoID
		}

		var teamName string
		_ = tx.QueryRow(`SELECT t.name FROM teams t JOIN team_members tm ON tm.team_id=t.id JOIN sessions s ON s.id=?
			WHERE t.active=1 AND ((tm.member_kind='repo' AND tm.member_id=s.repo_id AND s.repo_id<>'') OR (tm.member_kind='agent' AND tm.member_id=s.agent_id AND s.agent_id<>'')) LIMIT 1`,
			item.SessionID).Scan(&teamName)
		if teamName != "" {
			return teamName
		}
	}

	if item.Node != "" && !strings.HasPrefix(item.Node, "n_") {
		return item.Node
	}

	return ""
}

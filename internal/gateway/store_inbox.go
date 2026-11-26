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

// CreateInboxItem makes an item: an item for the owner goes to the chief of
// staff or the log (decision 0196), any other is stored. Callers must derive
// the session and node from authenticated state rather than trusting
// agent-supplied values.
func (s *Store) CreateInboxItem(item protocol.InboxItem) (protocol.InboxItem, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return item, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if item.Replaces != "" {
		old, err := scanInbox(tx.QueryRow(`SELECT `+inboxColumns+` FROM inbox_items WHERE id=?`, item.Replaces))
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
	// One decision per item (decision 0150): an ask with several questions
	// becomes one item per question. The first one is returned. An ask that
	// goes to the chief stays one request.
	parts := []string{item.Text}
	toChief := ownerBound(item)
	if item.Kind == protocol.AskKindQuestion && item.SourceRef == "" && !toChief {
		if split := protocol.SplitQuestions(item.Text); split != nil {
			parts = split
		}
	}
	first := protocol.InboxItem{}
	for i, text := range parts {
		part := item
		part.Text = text
		if part, err = s.createInboxItem(tx, part); err != nil {
			return part, err
		}
		if i == 0 {
			first = part
		}
	}
	item = first
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

// createInboxItem is the one way an item is made. An item for the owner is not
// stored (decision 0196): it becomes a request to the chief of staff, or a log
// line when agent-net raised it itself (owner_items.go). Other items are
// stored as before.
func (s *Store) createInboxItem(db askDB, item protocol.InboxItem) (protocol.InboxItem, error) {
	if ownerBound(item) {
		return s.ownerItemToChief(db, item)
	}
	switch item.Kind {
	case "approval", "feature_idea", "question", "rejection", "escalation", "check", "session_stall",
		protocol.AskKindHeld, protocol.AskKindLearned, protocol.AskKindDraft, protocol.AskKindProposal, protocol.AskKindPermission, protocol.AskKindStep, protocol.AskKindWaiting,
		protocol.AskKindBrief, protocol.AskKindSignIn, protocol.AskKindMachine, protocol.AskKindCTOAlert, protocol.AskKindQuotaStuck, protocol.AskKindCTOIntake:
	default:
		return item, reject("invalid inbox kind")
	}
	// The route of decision 0152. A kind the table does not list keeps
	// today's default, the chief; TestRouteTableCoversEveryKind is what
	// makes every kind get a row.
	rt, ok := routeOf(routeAsk, askRouteKey(item))
	if !ok {
		rt = route{Class: routeAsk, Kind: item.Kind, To: routeChief}
		if s.logf != nil {
			s.logf("route.missing", "kind", item.Kind)
		}
	}
	// An automatic item routed to the owner (sign-in and OAuth) skips the
	// chief: it is a one-click ask only the owner can do.
	if rt.Auto && rt.To == routeOwner && item.NextWho == "" {
		item.NextWho = protocol.AskNextOwner
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
	// A role node that holds the asks scope over the asker's repo gets an ask the
	// route table sends to the chief before the chief does (decision 0166).
	holder := ""
	if item.NextWho == "" && rt.To == routeChief && (item.FromKind == "" || item.FromKind == protocol.AskFromSession) {
		if holder = s.asksHolderSession(db, item.SessionID); holder != "" {
			nextWho, skipped = holder, false
		}
	}
	if skipped {
		// The chief is registered but cannot work: the ask was sent past it.
		routeFixed = 1
	}
	if len(item.Options) > 4 {
		return item, reject("too many options")
	}
	fromSession := item.FromKind == "" || item.FromKind == protocol.AskFromSession
	item.Category = itemCategory(item)
	options, err := json.Marshal(item.Options)
	if err != nil {
		return item, err
	}
	now := s.now().UTC()
	// One open ask per source key (and so per source and kind: a key belongs
	// to one kind). A repeat from the same asker refreshes the text it shows;
	// another asker joins the open item and gets its answer too.
	if item.SourceRef != "" {
		var id, session string
		err := db.QueryRow(`SELECT id,session_id FROM inbox_items WHERE source_ref=? AND status='open'`, item.SourceRef).Scan(&id, &session)
		if err == nil {
			if session != item.SessionID && fromSession {
				err := noteRepeat(db, repeatAsk{id: id, session: session, status: "open"}, item)
				item.ID = id
				return item, err
			}
			_, err = db.Exec(`UPDATE inbox_items SET text=?,recommendation=?,options=?,detail=?,why_you=? WHERE id=?`,
				item.Text, item.Recommendation, string(options), item.Detail, item.WhyYou, id)
			item.ID = id
			return item, err
		}
		if err != sql.ErrNoRows {
			return item, err
		}
	} else if item.MessageID != "" {
		// A message is the item's source: its outcome is resolved by that
		// message, so it never joins another item by text.
	} else if item.FromKind == protocol.AskFromSession {
		// The repeat rule is for asks an agent raised (agent_owner_ask and a
		// turn-end question, relayed ones included).
		found, err := repeatAskWithin(db, item, now)
		if err != nil {
			return item, err
		}
		if found.id != "" {
			if err := noteRepeat(db, found, item); err != nil {
				return item, err
			}
			return scanInbox(db.QueryRow(`SELECT `+inboxColumns+` FROM inbox_items WHERE id=?`, found.id))
		}
	} else if id, err := sameAskWithin(db, item, now); err != nil {
		return item, err
	} else if id != "" {
		// An event or generated ask keeps the behaviour it had: an open ask of
		// the same asker with the same text catches it, and a closed one does
		// not. A stall at a new baseline is a new event, not a repeat.
		return scanInbox(db.QueryRow(`SELECT `+inboxColumns+` FROM inbox_items WHERE id=?`, id))
	}
	item.ID = protocol.NewID("i")
	item.CreatedAt = now
	item.Status = "open"
	item.Answer = ""
	item.AnsweredAt = time.Time{}
	_, err = db.Exec(`INSERT INTO inbox_items (id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,options,asker_name,change_title,
		from_kind,source_ref,about_kind,about_id,next_who,why_you,detail,route_fixed,category) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.Kind, item.SessionID, item.Node, item.Text, item.Recommendation, item.MessageID, item.ConversationID, item.CreatedAt.UnixNano(), item.Status, string(options), item.AskerName, item.ChangeTitle,
		item.FromKind, item.SourceRef, item.AboutKind, item.AboutID, nextWho, item.WhyYou, item.Detail, routeFixed, item.Category)
	if err == nil && holder != "" {
		_, err = s.storeAgentNetMessage(db, holder, s.holderAskBody(db, item), item.ID, now)
	}
	return item, err
}

// itemCategory is the category of a new item: the one its kind fixes, else
// the one the asker gave, else normal.
func itemCategory(item protocol.InboxItem) string {
	if c := protocol.KindCategory(item.Kind); c != "" {
		return c
	}
	if protocol.ValidItemCategory(item.Category) {
		return item.Category
	}
	return protocol.CategoryNormal
}

// sameAskWindow is how long an open ask catches the same question from the
// same asker or its relay (decision 0150).
const sameAskWindow = 24 * time.Hour

// repeatClosedWindow is how long a closed ask still catches a repeat. A repeat
// this soon after the answer is the asker missing it, not a new question.
const repeatClosedWindow = 10 * time.Minute

// sameAskWithin is the older rule, kept for asks agent-net raises itself: an
// open ask of the same asker, made in the last 24 hours, whose text is the
// same once case, white space and ids are folded.
func sameAskWithin(db askDB, item protocol.InboxItem, now time.Time) (string, error) {
	if item.SessionID == "" {
		return "", nil
	}
	rows, err := db.Query(`SELECT id,text FROM inbox_items WHERE session_id=? AND status='open' AND created_at>? ORDER BY created_at,rowid`,
		item.SessionID, now.Add(-sameAskWindow).UnixNano())
	if err != nil {
		return "", err
	}
	defer rows.Close()
	key := protocol.NormalizeAskText(item.Text)
	for rows.Next() {
		var id, text string
		if err := rows.Scan(&id, &text); err != nil {
			return "", err
		}
		if protocol.NormalizeAskText(text) == key {
			return id, nil
		}
	}
	return "", rows.Err()
}

// repeatAsk is the ask a new ask repeats.
type repeatAsk struct {
	id, session, status string
}

// repeatAskWithin finds a related ask whose text asks the same thing. A
// related ask is one of the same asker, one about the same thing (the marker
// the chief reads as "related open asks"), or one from a session in the same
// project and kind. An open ask catches a repeat for 24 hours; a closed one
// catches it for 10 minutes. The ids in the text must match too, so a new
// commit head is a new ask and not a repeat.
func repeatAskWithin(db askDB, item protocol.InboxItem, now time.Time) (repeatAsk, error) {
	if item.SessionID == "" {
		return repeatAsk{}, nil
	}
	rows, err := db.Query(`SELECT id,session_id,status,text FROM inbox_items
		WHERE ((status='open' AND created_at>?) OR (status='closed' AND answered_at>=?))
		  AND (session_id=? OR (?<>'' AND about_id=?) OR (?<>'' AND node=? AND kind=?))
		ORDER BY created_at DESC,rowid DESC`,
		now.Add(-sameAskWindow).UnixNano(), now.Add(-repeatClosedWindow).UnixNano(),
		item.SessionID, item.AboutID, item.AboutID, item.Node, item.Node, item.Kind)
	if err != nil {
		return repeatAsk{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var found repeatAsk
		var text string
		if err := rows.Scan(&found.id, &found.session, &found.status, &text); err != nil {
			return repeatAsk{}, err
		}
		if protocol.SameAskText(text, item.Text) {
			return found, nil
		}
	}
	return repeatAsk{}, rows.Err()
}

// noteRepeat keeps the repeat on the ask the chief reads, with who repeated it.
func noteRepeat(db askDB, found repeatAsk, item protocol.InboxItem) error {
	who := strings.TrimSpace(item.AskerName)
	switch {
	case who == "":
		who = item.SessionID
	case item.SessionID != "":
		who += " (" + item.SessionID + ")"
	}
	if who == "" {
		who = "another asker"
	}
	note := "Repeated by " + who + ": " + askFirstLine(item.Text, 200)
	_, err := db.Exec(`UPDATE inbox_items SET detail=CASE WHEN detail='' THEN ? ELSE detail||char(10)||? END WHERE id=?`, note, note, found.id)
	return err
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
	err := row.Scan(&item.ID, &item.Kind, &item.SessionID, &item.Node, &item.Text, &item.Recommendation, &item.MessageID, &item.ConversationID, &created, &item.Status, &item.Answer, &answered, &options, &item.AskerName, &item.ChangeTitle, &item.Category)
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

const inboxColumns = `id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,answer,answered_at,options,asker_name,change_title,category`

// inboxTables are the tables inboxRead reads: the items, the delivery steps and
// their changes, the names of the askers (agentNames: sessions, nodes, profiles)
// and the pipeline of a delivery step, which reads the graph and the settings.
// Keep it equal to those queries (see readThrough).
var inboxTables = tableUnion([]string{"inbox_items", "delivery_changes", "sessions", "nodes.id", "nodes.hostname"}, graphTables)

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
	rows, err := s.db.Query(`SELECT i.id,i.kind,i.session_id,i.node,i.text,i.recommendation,i.message_id,i.conversation_id,i.created_at,i.status,i.answer,i.answered_at,i.options,i.asker_name,i.change_title,i.category,
		i.about_kind,i.about_id,COALESCE(c.project,''),COALESCE(c.title,'')
		FROM inbox_items i LEFT JOIN delivery_changes c ON i.about_kind='step' AND c.id=substr(i.about_id,1,instr(i.about_id,':')-1)
		WHERE i.kind<>'cto_intake' AND NOT (i.status='open' AND i.next_who NOT IN ('owner','')) ORDER BY i.created_at,i.id`)
	if err != nil {
		return nil, err
	}
	var stepLinks []deliveryLink
	items := []protocol.InboxItem{}
	for rows.Next() {
		var item protocol.InboxItem
		var created, answered int64
		var options, aboutKind, aboutID, project, title string
		if err := rows.Scan(&item.ID, &item.Kind, &item.SessionID, &item.Node, &item.Text, &item.Recommendation, &item.MessageID, &item.ConversationID, &created, &item.Status, &item.Answer, &answered, &options, &item.AskerName, &item.ChangeTitle, &item.Category, &aboutKind, &aboutID, &project, &title); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(options), &item.Options); err != nil {
			rows.Close()
			return nil, err
		}
		item.CreatedAt = time.Unix(0, created).UTC()
		if answered != 0 {
			item.AnsweredAt = time.Unix(0, answered).UTC()
		}
		if item.Kind == "check" && item.Node == "apps" && strings.HasPrefix(item.Text, "Connected: ") && !item.CreatedAt.After(s.now().Add(-24*time.Hour)) {
			continue
		}
		if aboutKind == protocol.AskAboutStep {
			if changeID, _, ref, commit, ok := parseStepAboutID(aboutID); ok && project != "" {
				item.Delivery = &protocol.InboxDelivery{ChangeID: changeID, StepRef: ref, Commit: commit}
				if item.ChangeTitle == "" {
					item.ChangeTitle = title
				}
				stepLinks = append(stepLinks, deliveryLink{item.Delivery, project})
			}
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
	return items, s.fillDeliveryReportFields(stepLinks)
}

type deliveryLink struct {
	delivery *protocol.InboxDelivery
	project  string
}

func (s *Store) fillDeliveryReportFields(links []deliveryLink) error {
	pipelines := map[string]protocol.Pipeline{}
	for _, sl := range links {
		p, ok := pipelines[sl.project]
		if !ok {
			var err error
			if p, err = s.deliveryPipeline(sl.project); err != nil {
				return err
			}
			pipelines[sl.project] = p
		}
		sl.delivery.ReportFields = p.ReportFields(sl.delivery.StepRef)
	}
	return nil
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
	// A learned suggestion is an ask for the gateway too. Its answer decides
	// the suggestion, so the next sweep does not ask for it again.
	if item.Kind == protocol.AskKindLearned {
		var ref string
		if err = tx.QueryRow(`SELECT source_ref FROM inbox_items WHERE id=?`, id).Scan(&ref); err != nil {
			return item, err
		}
		if learnedID, parseErr := sourceRefID(ref, "learned:"); parseErr == nil {
			return s.answerLearnedAsk(tx, item, learnedID, answer, actor, len(validate) > 0 && validate[0])
		}
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
	// Every asker that joined the item gets the answer (decision 0150). Read
	// them before the update settles their rows.
	joined, err := joinedAskers(tx, id)
	if err != nil {
		return item, err
	}
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
	for _, to := range append([]string{item.SessionID}, joined...) {
		_, err = tx.Exec(`INSERT INTO messages (id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state,approval_note) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			protocol.NewID("m"), protocol.NewID("c"), "", sender, to, protocol.TypeNotification, answer, 0, protocol.NewID("k"), item.AnsweredAt.UnixNano(), item.AnsweredAt.Add(protocol.DefaultTTL).UnixNano(), protocol.StateStored, note)
		if err != nil {
			return item, err
		}
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

// joinedAskers lists the other sessions recorded as repeats in an item's detail.
func joinedAskers(q askDB, id string) ([]string, error) {
	var ownerSession, detail string
	if err := q.QueryRow(`SELECT session_id,detail FROM inbox_items WHERE id=?`, id).Scan(&ownerSession, &detail); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	seen := map[string]bool{ownerSession: true, "": true, "another asker": true}
	var out []string
	for _, line := range strings.Split(detail, "\n") {
		rest, ok := strings.CutPrefix(line, "Repeated by ")
		if !ok {
			continue
		}
		who, _, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}
		sid := strings.TrimSpace(who)
		if i := strings.LastIndex(sid, " ("); i >= 0 && strings.HasSuffix(sid, ")") {
			sid = sid[i+2 : len(sid)-1]
		}
		if !seen[sid] {
			seen[sid] = true
			out = append(out, sid)
		}
	}
	return out, nil
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
	rows, err := s.db.Query(`SELECT s.id, s.name, s.title
		FROM sessions s LEFT JOIN nodes n ON n.id = s.node_id
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
	if repo, team, err := s.askRepoTeam(tx, item.ID); err == nil {
		if repo != "" {
			return repo
		}
		if team != "" {
			return team
		}
	}
	if item.Node != "" && !strings.HasPrefix(item.Node, "n_") {
		return item.Node
	}
	return ""
}

// answerLearnedAsk applies an answer to the ask of a learned suggestion.
// "accept" and "reject" decide it as the owner's keys do. Any other text is the
// answerer saying what it did, so it counts as carried out, or as a refusal if
// it starts with one. A suggestion that is no longer pending only closes the
// ask. The transaction is released first: the store has a single connection.
func (s *Store) answerLearnedAsk(tx *sql.Tx, item protocol.InboxItem, learnedID int64, answer, actor string, validateOnly bool) (protocol.InboxItem, error) {
	var status string
	err := tx.QueryRow(`SELECT status FROM learned_suggestions WHERE id=?`, learnedID).Scan(&status)
	if err != nil && err != sql.ErrNoRows {
		return item, err
	}
	item.Status = "answered"
	item.Answer = answer
	item.AnsweredAt = s.now().UTC()
	if validateOnly {
		return item, nil
	}
	_ = tx.Rollback()
	if status == "pending" {
		switch answer {
		case "accept", "reject":
			err = s.decideLearned(learnedID, answer, actor)
		default:
			err = s.carryOutLearned(learnedID, answer)
		}
		if err != nil {
			return item, err
		}
	}
	if err = s.closeAnsweredItem(item, answer); err != nil {
		return item, err
	}
	return item, s.reconcileLearnedAsks()
}

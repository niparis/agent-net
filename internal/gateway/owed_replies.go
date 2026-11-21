package gateway

import (
	"fmt"
	"strconv"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

const owedRepliesLiveSinceKey = "owed_replies_live_since"

func (s *Store) initOwedRepliesLiveSince() error {
	if _, set, err := stateGet(s.db, owedRepliesLiveSinceKey); err != nil || set {
		return err
	}
	now := s.now().UnixNano()
	return stateSet(s.db, owedRepliesLiveSinceKey, strconv.FormatInt(now, 10), now)
}

const dstWaitsForOthersSQL = `(EXISTS(SELECT 1 FROM sessions w WHERE w.online=1 AND w.worker<>'' AND json_extract(w.worker,'$.owner')=dst.id)
 OR EXISTS(SELECT 1 FROM delivery_step_tasks t JOIN delivery_changes c ON c.id=t.change_id AND c.revision=t.revision
 WHERE c.caller=dst.id AND c.state='open' AND t.completed=0 AND t.claimed_by<>'' AND t.claimed_by<>dst.id))`

type owedCandidate struct {
	reqID          string
	convID         string
	sender         string
	recipient      string
	body           string
	reqCreatedAt   int64
	dstStatus      string
	dstHarnessAct  int64
	dstLastActive  int64
	dstCreatedAt   int64
	dstWaiting     string
	dstRepo        string
	dstTeam        string
	srcRepo        string
	senderName     string
	recipientName  string
	waitsForOthers bool
	openAsksCount  int
	nudgeAt        int64
	progressAt     int64
	progressUntil  int64
	askRaised      bool
	limitedUntil   int64
}

// holdNanos is the time the owed-reply chase waits until. It is the time the
// latest progress reply gave, or that reply's time plus the grace when it gave
// none. Zero means no hold.
func (c owedCandidate) holdNanos(grace time.Duration) int64 {
	switch {
	case c.progressAt == 0:
		return 0
	case c.progressUntil > 0:
		return c.progressUntil
	default:
		return c.progressAt + int64(grace)
	}
}

// sessionSenderLead finds the lead of the sender session, if any.
func (s *Store) sessionSenderLead(senderID string) string {
	t, ok, _ := s.memberTeamOf(senderID)
	if ok && t.leadAgent != "" {
		_, name, _ := s.leadAskSession(t.leadAgent)
		if name != "" {
			return name
		}
		return t.leadAgent
	}
	var repo string
	_ = s.db.QueryRow(`SELECT COALESCE(NULLIF(s.repo_project,''),p.project,'') FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=?`, senderID).Scan(&repo)
	if repo != "" {
		repoLead, _ := repoTopAgentQ(s.db, repo)
		if repoLead != "" {
			return repoLead
		}
	}
	return ""
}

// checkOwedReplies checks open requests and orders with no final reply.
// An idle recipient gets one nudge after messages.owed_reply_nudge_after.
// If still unanswered messages.owed_reply_ask_after later, an ask is routed to the chief.
// A progress reply holds both until the time it gives, or for
// messages.owed_reply_progress_grace after it when it gives none.
// A recipient with a usage limit gets neither until the limit ends.
func (s *Store) checkOwedReplies() ([]protocol.Message, error) {
	snap, err := s.SettingsSnapshot()
	if err != nil {
		return nil, err
	}
	now := s.now()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var liveSinceNanos int64
	rawLiveSince, set, err := stateGet(tx, owedRepliesLiveSinceKey)
	if err != nil {
		return nil, err
	}
	if set {
		liveSinceNanos, _ = strconv.ParseInt(rawLiveSince, 10, 64)
	} else {
		liveSinceNanos = now.UnixNano()
		if err := stateSet(tx, owedRepliesLiveSinceKey, strconv.FormatInt(liveSinceNanos, 10), liveSinceNanos); err != nil {
			return nil, err
		}
	}

	// Close any open owed-ask where the recipient has since replied in the conversation.
	if _, err := tx.Exec(`UPDATE inbox_items SET status='closed', answer='replied', answered_at=?
		WHERE status='open' AND source_ref LIKE 'owed-ask:%'
		  AND EXISTS (
			SELECT 1 FROM messages q
			WHERE 'owed-ask:'||q.id = inbox_items.source_ref
			  AND (q.state='replied' OR EXISTS (
				SELECT 1 FROM messages rep
				WHERE rep.conversation_id = q.conversation_id
				  AND rep.sender = q.recipient
				  AND rep.recipient = q.sender
				  AND rep.created_at > q.created_at
				  AND rep.parent_id <> q.id
			  ))
		  )`, now.UnixNano()); err != nil {
		return nil, err
	}

	rows, err := tx.Query(`SELECT q.id, q.conversation_id, q.sender, q.recipient, q.body, q.created_at,
		dst.status, dst.harness_activity, dst.last_active, dst.created_at, dst.waiting,
		COALESCE(NULLIF(dst.repo_project,''), dp.project, ''),
		COALESCE((SELECT tm.team_id FROM team_members tm JOIN teams t ON t.id=tm.team_id AND t.active=1
			WHERE (tm.member_kind='agent' AND tm.member_id=dst.agent_id) OR (tm.member_kind='repo' AND tm.member_id=dst.repo_id)
			ORDER BY tm.team_id LIMIT 1), ''),
		COALESCE(NULLIF(src.repo_project,''), sp.project, ''),
		COALESCE(NULLIF(sp.name,''), NULLIF(src.name,''), q.sender),
		COALESCE(NULLIF(dp.name,''), NULLIF(dst.name,''), q.recipient),
		`+dstWaitsForOthersSQL+`,
		(SELECT COUNT(*) FROM inbox_items ii WHERE ii.session_id=dst.id AND ii.status='open'),
		COALESCE((SELECT m.created_at FROM messages m WHERE m.parent_id=q.id AND m.sender='agent-net' AND m.idem_key='owed-nudge:'||q.id LIMIT 1), 0),
		COALESCE((SELECT p.created_at FROM messages p WHERE p.conversation_id=q.conversation_id AND p.parent_id=q.id
			AND p.sender=q.recipient AND p.recipient=q.sender AND p.progress=1 ORDER BY p.created_at DESC LIMIT 1), 0),
		COALESCE((SELECT p.progress_until FROM messages p WHERE p.conversation_id=q.conversation_id AND p.parent_id=q.id
			AND p.sender=q.recipient AND p.recipient=q.sender AND p.progress=1 ORDER BY p.created_at DESC LIMIT 1), 0),
		EXISTS(SELECT 1 FROM inbox_items ii WHERE ii.source_ref='owed-ask:'||q.id),
		dst.limited_until
	FROM messages q
	JOIN sessions src ON src.id=q.sender
	JOIN sessions dst ON dst.id=q.recipient
	LEFT JOIN profiles sp ON sp.session_id=src.id
	LEFT JOIN profiles dp ON dp.session_id=dst.id
	WHERE (q.type='request' OR q.is_order=1)
		AND q.state IN ('stored','dispatched','delivered')
		AND q.expires_at > ?
		AND q.created_at >= ?
		AND dst.harness NOT IN ('owner','cto')
		AND dst.online = 1
		AND (dst.worker = '' OR dst.worker IS NULL)
		AND NOT EXISTS (
			SELECT 1 FROM messages rep
			WHERE rep.conversation_id = q.conversation_id
			  AND rep.sender = q.recipient
			  AND rep.recipient = q.sender
			  AND rep.created_at > q.created_at
			  AND rep.parent_id <> q.id
		)`, now.UnixNano(), liveSinceNanos)
	if err != nil {
		return nil, err
	}

	var candidates []owedCandidate
	for rows.Next() {
		var c owedCandidate
		if err := rows.Scan(
			&c.reqID, &c.convID, &c.sender, &c.recipient, &c.body, &c.reqCreatedAt,
			&c.dstStatus, &c.dstHarnessAct, &c.dstLastActive, &c.dstCreatedAt, &c.dstWaiting,
			&c.dstRepo, &c.dstTeam, &c.srcRepo,
			&c.senderName, &c.recipientName,
			&c.waitsForOthers, &c.openAsksCount,
			&c.nudgeAt, &c.progressAt, &c.progressUntil, &c.askRaised, &c.limitedUntil,
		); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	var nudges []protocol.Message
	for _, c := range candidates {
		where := settings.Where{Repo: c.dstRepo, Team: c.dstTeam}
		nudgeAfter := snap.Duration("messages.owed_reply_nudge_after", where)
		if nudgeAfter <= 0 {
			nudgeAfter = 15 * time.Minute
		}
		askAfter := snap.Duration("messages.owed_reply_ask_after", where)
		if askAfter <= 0 {
			askAfter = 10 * time.Minute
		}

		// A progress reply holds the chase until the time it gives, or the grace after it.
		grace := snap.Duration("messages.owed_reply_progress_grace", where)
		hold := time.Unix(0, c.holdNanos(grace))

		// A recipient on a usage limit cannot reply. The chase sends it nothing
		// until the limit ends, and then counts its waits from that end.
		limitEnd := time.Unix(c.limitedUntil, 0)
		if now.Before(limitEnd) {
			continue
		}

		// Nudge phase
		if c.nudgeAt == 0 {
			if now.Before(hold) {
				continue
			}
			// Skip busy recipient, waiting on owner, or waiting on others.
			if c.dstStatus == "busy" || c.dstWaiting != "" || c.openAsksCount > 0 || c.waitsForOthers {
				continue
			}
			baseline := c.dstLastActive
			if c.dstHarnessAct > baseline {
				baseline = c.dstHarnessAct
			}
			if baseline <= 0 || baseline < c.reqCreatedAt {
				baseline = c.reqCreatedAt
			}
			if limitNanos := limitEnd.UnixNano(); limitNanos > baseline {
				baseline = limitNanos
			}
			idleSince := time.Unix(0, baseline)
			if now.Sub(idleSince) < nudgeAfter {
				continue
			}

			if paused, err := s.nudgePaused(tx, "owed_reply", c.recipient); err != nil {
				return nil, err
			} else if paused {
				continue
			}

			n := protocol.Message{
				ID:             protocol.NewID("m"),
				ConversationID: c.convID,
				ParentID:       c.reqID,
				From:           "agent-net",
				FromName:       "agent-net",
				FromHarness:    "agent-net",
				To:             c.recipient,
				Type:           protocol.TypeNotification,
				Body: fmt.Sprintf("You still owe %s a final reply to %s (%s). Reply with the result, or hand back.",
					c.senderName, c.reqID, askFirstLine(c.body, 120)),
				IdempotencyKey: "owed-nudge:" + c.reqID,
				CreatedAt:      now.UTC(),
				ExpiresAt:      now.Add(protocol.DefaultTTL).UTC(),
				State:          protocol.StateStored,
			}

			if _, err := tx.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
				VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`,
				n.ID, n.ConversationID, n.ParentID, n.From, n.To, n.Type, n.Body,
				n.IdempotencyKey, n.CreatedAt.UnixNano(), n.ExpiresAt.UnixNano(), n.State,
			); err != nil {
				return nil, err
			}

			if err := recordNudgeFire(tx, "owed_reply", n.To, n.ID, n.CreatedAt); err != nil {
				return nil, err
			}
			s.event(tx, "msg.stored", "", n.From, n.ID, "type=notification by=agent-net owed_reply="+c.reqID)
			nudges = append(nudges, n)
			continue
		}

		// Ask phase
		if !c.askRaised {
			nudgeTime := time.Unix(0, c.nudgeAt)
			// The ask waits askAfter from the nudge, or from the end of a progress
			// hold or a usage limit when that is later.
			askFrom := nudgeTime
			if hold.After(askFrom) {
				askFrom = hold
			}
			if limitEnd.After(askFrom) {
				askFrom = limitEnd
			}
			if now.Sub(askFrom) < askAfter {
				continue
			}

			senderLead := s.sessionSenderLead(c.sender)
			text := fmt.Sprintf("%s (%s) waits on %s (%s) since %s for reply to %s (%s). Nudged at %s.",
				c.senderName, c.sender, c.recipientName, c.recipient,
				time.Unix(0, c.reqCreatedAt).UTC().Format(time.RFC3339),
				c.reqID, askFirstLine(c.body, 120),
				nudgeTime.UTC().Format(time.RFC3339),
			)
			if senderLead != "" {
				text += fmt.Sprintf(" Sender's lead: %s.", senderLead)
			}

			node := c.srcRepo
			if node == "" {
				node = c.dstRepo
			}

			item := protocol.InboxItem{
				Kind:           protocol.AskKindQuestion,
				SessionID:      c.sender,
				Node:           node,
				NextWho:        protocol.AskNextChief,
				SourceRef:      "owed-ask:" + c.reqID,
				MessageID:      c.reqID,
				ConversationID: c.convID,
				Text:           text,
				Detail:         c.body,
				Options:        []string{"wait", "close"},
			}

			if _, err := s.createInboxItem(tx, item); err != nil {
				return nil, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return nudges, nil
}

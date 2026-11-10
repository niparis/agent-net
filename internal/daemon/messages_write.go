package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The message write path behind agent_message_send and agent_message_reply.
// The old agent_message_send and agent_message_reply use the same methods and keep their old
// result shapes. A caller that asks for receipt: true gets the receipt shape
// the tool catalog describes.

type messageSet struct {
	Team       string   `json:"team"`
	Machine    string   `json:"machine"`
	Project    string   `json:"project"`
	Harness    string   `json:"harness"`
	Kind       []string `json:"kind"`
	OnlineOnly *bool    `json:"online_only"`
}

type messageSessionRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type messageReceipt struct {
	MessageID string            `json:"message_id"`
	To        messageSessionRef `json:"to"`
	State     string            `json:"state"`
	Note      string            `json:"note,omitempty"`
}

type messageReceipts struct {
	Sent   []messageReceipt   `json:"sent"`
	Missed []missedTeamMember `json:"missed,omitempty"`
}

func (d *Daemon) localSend(ctx context.Context, lc *localConn, raw json.RawMessage) (any, error) {
	sid, err := lc.boundID()
	if err != nil {
		return nil, err
	}
	var p struct {
		To             string      `json:"to"`
		ToSet          *messageSet `json:"to_set"`
		Body           string      `json:"body"`
		MessageType    string      `json:"message_type"`
		Type           string      `json:"type"`
		AsOwner        bool        `json:"as_owner"`
		Order          bool        `json:"order"`
		Urgent         bool        `json:"urgent"`
		IdempotencyKey string      `json:"idempotency_key"`
		Receipt        bool        `json:"receipt"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return nil, errors.New("bad send params")
	}
	kind := p.MessageType
	if kind == "" {
		kind = p.Type
	}
	if kind != "" && kind != protocol.TypeRequest && kind != protocol.TypeNotification && kind != protocol.TypeReply {
		return nil, errors.New("type is request or notification")
	}
	if p.To != "" && p.ToSet != nil {
		return nil, errors.New("choose exactly one of to and to_set")
	}
	if p.To == "" && p.ToSet == nil {
		return nil, errors.New(`choose exactly one of to and to_set; example: {"to":"s01","body":"Please review."}`)
	}
	if p.AsOwner {
		if sid != d.ctoSessionID() {
			return nil, errors.New("as_owner is for the CTO session only; send a normal message instead")
		}
		if p.ToSet != nil {
			return d.sendSet(ctx, sid, p.ToSet, p.Body, kind, p.IdempotencyKey, true)
		}
	}
	if p.ToSet != nil {
		return d.sendSet(ctx, sid, p.ToSet, p.Body, kind, p.IdempotencyKey, false)
	}

	to, err := d.resolveTo(ctx, p.To)
	if err != nil {
		return nil, err
	}
	address := ""
	if strings.HasPrefix(p.To, "repo:") || strings.HasPrefix(p.To, "team:") {
		address = p.To
	}
	data, err := d.send(ctx, protocol.MsgSend{Sender: sid, Recipient: to, Type: kind, LeadAddress: address,
		Body: p.Body, IdempotencyKey: p.IdempotencyKey, FromCTO: p.AsOwner, OwnerApproved: p.AsOwner, Order: p.Order, Urgent: p.Urgent})
	if err == nil && sid == d.ctoSessionID() {
		err = d.recordCTOSent(ctx, to, p.AsOwner)
	}
	if err != nil || !p.Receipt {
		return data, err
	}
	receipt := d.receiptForSend(ctx, data, to)
	return messageReceipts{Sent: []messageReceipt{receipt}}, nil
}

func (d *Daemon) localReply(ctx context.Context, lc *localConn, raw json.RawMessage) (any, error) {
	sid, err := lc.boundID()
	if err != nil {
		return nil, err
	}
	var p struct {
		protocol.ReplyOutcome
		MessageID      string `json:"message_id"`
		Body           string `json:"body"`
		Ack            bool   `json:"ack"`
		AsOwner        bool   `json:"as_owner"`
		IdempotencyKey string `json:"idempotency_key"`
		Receipt        bool   `json:"receipt"`
	}
	if json.Unmarshal(raw, &p) != nil || p.MessageID == "" {
		return nil, errors.New("reply needs message_id")
	}
	if p.AsOwner {
		if sid != d.ctoSessionID() {
			return nil, errors.New("as_owner is for the CTO session only; reply normally instead")
		}
	}
	p.ReplyOutcome, err = protocol.CleanOutcome(p.ReplyOutcome)
	if err != nil {
		return nil, err
	}
	if p.Ack && p.Outcome != "" {
		return nil, errors.New("ack cannot be combined with outcome")
	}
	data, err := d.call(ctx, "query.message", protocol.QueryMessage{MessageID: p.MessageID})
	if err != nil {
		return nil, err
	}
	var parent struct {
		Message protocol.Message `json:"message"`
	}
	if err := json.Unmarshal(data, &parent); err != nil {
		return nil, err
	}
	kind := protocol.TypeReply
	if p.Ack {
		kind = protocol.TypeAck
	}
	data, err = d.send(ctx, protocol.MsgSend{Sender: sid, Recipient: parent.Message.From,
		Type: kind, Body: p.Body, ParentID: p.MessageID, IdempotencyKey: p.IdempotencyKey, ReplyOutcome: p.ReplyOutcome, Ack: p.Ack, FromCTO: p.AsOwner, OwnerApproved: p.AsOwner})
	if err == nil && sid == d.ctoSessionID() {
		err = d.recordCTOSent(ctx, parent.Message.From, p.AsOwner)
	}
	if err != nil || !p.Receipt {
		return data, err
	}
	return d.receiptForSend(ctx, data, parent.Message.From), nil
}

func (d *Daemon) recordCTOSent(ctx context.Context, to string, asOwner bool) error {
	voice := ""
	if asOwner {
		voice = " as owner"
	}
	_, err := d.call(ctx, "inbox.create", map[string]any{"session_id": d.ctoSessionID(), "kind": "check", "text": "CTO sent" + voice + " to " + d.sessionName(ctx, to)})
	return err
}

// sendSet sends one message to every online session that matches the set.
// Only the CTO session may call it. The receipt list always
// names every attempted recipient, so a partial failure can be repaired
// without sending twice.
func (d *Daemon) sendSet(ctx context.Context, sid string, set *messageSet, body, kind, key string, asOwner bool) (any, error) {
	if !d.maySendGroup(ctx, sid) {
		return nil, errors.New("group messages are for the CTO only; send to one session with to")
	}
	if strings.TrimSpace(set.Team) == "" && strings.TrimSpace(set.Machine) == "" && strings.TrimSpace(set.Project) == "" && strings.TrimSpace(set.Harness) == "" {
		return nil, errors.New(`to_set needs a nonempty team, machine, project or harness filter; example: {"to_set":{"harness":"claude"},"body":"Please review."}`)
	}
	kinds, err := messageKinds(set.Kind)
	if err != nil {
		return nil, err
	}
	online := true
	if set.OnlineOnly != nil {
		online = *set.OnlineOnly
	}
	if set.Team != "" {
		online = true
	}
	raw, err := d.call(ctx, "query.agents", protocol.QueryAgents{OnlineOnly: online})
	if err != nil {
		return nil, err
	}
	var page struct {
		Agents []protocol.AgentView `json:"agents"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, err
	}
	out := messageReceipts{Sent: []messageReceipt{}}
	var teamIDs map[string]bool
	if strings.TrimSpace(set.Team) != "" {
		raw, err := d.call(ctx, "graph.read", protocol.GraphRead{Typed: true})
		if err != nil {
			return nil, err
		}
		var graph protocol.GraphGot
		if err := json.Unmarshal(raw, &graph); err != nil {
			return nil, err
		}
		teamIDs, out.Missed, err = teamRecipients(graph.Graph, set.Team, page.Agents)
		if err != nil {
			return nil, err
		}
	}
	if key == "" {
		key = protocol.NewID("k")
	}
	seen := map[string]bool{}
	for _, a := range page.Agents {
		if a.SessionID == "" || a.SessionID == sid || seen[a.SessionID] || (teamIDs != nil && !teamIDs[a.SessionID]) || !matchesMessageSet(a, set, kinds) {
			continue
		}
		seen[a.SessionID] = true
		receipt := messageReceipt{To: messageSessionRef{ID: a.SessionID, Name: messageSessionName(a)}}

		data, sendErr := d.send(ctx, protocol.MsgSend{Sender: sid, Recipient: a.SessionID, Type: kind,
			Body: body, IdempotencyKey: key + ":" + a.SessionID, FromCTO: asOwner, OwnerApproved: asOwner})
		if sendErr != nil {
			receipt.State = "failed"
			out.Sent = append(out.Sent, receipt)
			continue
		}
		if sid == d.ctoSessionID() {
			if err := d.recordCTOSent(ctx, a.SessionID, asOwner); err != nil {
				return out, err
			}
		}
		receipt = d.receiptForSend(ctx, data, a.SessionID)
		out.Sent = append(out.Sent, receipt)
	}
	return out, nil
}

// maySendGroup is the daemon-side authority check. Only the registered CTO
// network session may send to a group (decision 0089).
func (d *Daemon) maySendGroup(_ context.Context, sid string) bool {
	return sid != "" && sid == d.ctoSessionID()
}

func messageKinds(kinds []string) (map[string]bool, error) {
	if len(kinds) == 0 {
		return map[string]bool{"agent": true}, nil
	}
	out := map[string]bool{}
	for _, kind := range kinds {
		if kind != "agent" && kind != "worker" {
			return nil, fmt.Errorf("to_set kind is agent or worker, not %q", kind)
		}
		out[kind] = true
	}
	return out, nil
}

func matchesMessageSet(a protocol.AgentView, set *messageSet, kinds map[string]bool) bool {
	worker := a.Worker != nil
	if worker && !kinds["worker"] || !worker && !kinds["agent"] {
		return false
	}
	if harness := strings.TrimSpace(set.Harness); harness != "" && !strings.EqualFold(a.Harness, harness) {
		return false
	}
	if machine := strings.TrimSpace(set.Machine); machine != "" &&
		!strings.EqualFold(a.Hostname, machine) && !strings.EqualFold(a.NodeID, machine) {
		return false
	}
	if project := strings.TrimSpace(set.Project); project != "" &&
		!strings.EqualFold(a.Repo.Project, project) && !strings.EqualFold(a.Profile.Project, project) {
		return false
	}
	return true
}

func messageSessionName(a protocol.AgentView) string {
	if a.Profile.Name != "" {
		return a.Profile.Name
	}
	if a.Title != "" {
		return a.Title
	}
	return a.SessionID
}

func (d *Daemon) sessionName(ctx context.Context, id string) string {
	view, err := d.agentView(ctx, id)
	if err != nil {
		return id
	}
	return messageSessionName(view)
}

func (d *Daemon) receiptForSend(ctx context.Context, data json.RawMessage, to string) messageReceipt {
	receipt := messageReceipt{To: messageSessionRef{ID: to, Name: d.sessionName(ctx, to)}}
	receipt.MessageID, receipt.State = messageStateOf(data)
	var result struct {
		Message *protocol.Message `json:"message"`
		Routing string            `json:"routing"`
		Queue   string            `json:"queue"`
	}
	if json.Unmarshal(data, &result) == nil {
		receipt.Note = result.Routing
		if result.Queue != "" {
			if receipt.Note != "" {
				receipt.Note += "; "
			}
			receipt.Note += result.Queue
		}
		if result.Message != nil && result.Message.To != "" && result.Message.To != to {
			receipt.To = messageSessionRef{ID: result.Message.To, Name: d.sessionName(ctx, result.Message.To)}
		}
	}
	return receipt
}

// messageStateOf reads the message id and state from a send result. A queued
// outbox send has no message yet, so it keeps the idempotency key and the
// queued state.
func messageStateOf(data json.RawMessage) (id, state string) {
	var r struct {
		Message *protocol.Message `json:"message"`
		Queued  bool              `json:"queued"`
		Key     string            `json:"idempotency_key"`
	}
	if json.Unmarshal(data, &r) != nil {
		return "", "unknown"
	}
	if r.Message != nil {
		return r.Message.ID, r.Message.State
	}
	if r.Queued {
		return r.Key, "queued"
	}
	return "", "unknown"
}

// localWithdraw takes back an undelivered message. The gateway decides
// whether the actor is the sender, the owner or the CTO (decision 0130).
func (d *Daemon) localWithdraw(ctx context.Context, lc *localConn, raw json.RawMessage) (any, error) {
	sid, err := lc.boundID()
	if err != nil {
		return nil, err
	}
	var p struct {
		MessageID string `json:"message_id"`
		OlderThan bool   `json:"older_than"`
		AsOwner   bool   `json:"as_owner"`
	}
	if json.Unmarshal(raw, &p) != nil || p.MessageID == "" {
		return nil, errors.New("withdraw needs message_id")
	}
	if p.AsOwner && sid != d.ctoSessionID() {
		return nil, errors.New("as_owner is for the CTO session only; withdraw your own message instead")
	}
	return d.call(ctx, "msg.withdraw", protocol.MsgWithdraw{MessageID: p.MessageID, OlderThan: p.OlderThan,
		Sender: sid, AsOwner: p.AsOwner})
}

// localHandoff passes a message this session received to another session. The
// gateway keeps the original sender on the new message, so the member's reply
// goes straight to the asker, and stores a copy of that reply for this session.
func (d *Daemon) localHandoff(ctx context.Context, lc *localConn, raw json.RawMessage) (any, error) {
	sid, err := lc.boundID()
	if err != nil {
		return nil, err
	}
	var p struct {
		MessageID      string `json:"message_id"`
		ToSession      string `json:"to_session"`
		Note           string `json:"note"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if json.Unmarshal(raw, &p) != nil || strings.TrimSpace(p.MessageID) == "" || strings.TrimSpace(p.ToSession) == "" {
		return nil, errors.New(`handoff needs message_id and to_session; example: {"message_id":"m_1","to_session":"ingest-forge","note":"yours"}`)
	}
	to, err := d.resolveTo(ctx, p.ToSession)
	if err != nil {
		return nil, err
	}
	if p.IdempotencyKey == "" {
		p.IdempotencyKey = protocol.NewID("k")
	}
	d.touchWorker(sid, to)
	data, err := d.call(withCallWait(ctx, 90*time.Second), "msg.handoff", protocol.MsgHandoff{
		Sender: sid, MessageID: p.MessageID, Recipient: to, Note: p.Note, IdempotencyKey: p.IdempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	return d.receiptForSend(ctx, data, to), nil
}

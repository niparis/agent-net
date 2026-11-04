package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// The message write path behind agent_message_send and agent_message_reply.
// The old agent_message_send and agent_message_reply use the same methods and keep their old
// result shapes. A caller that asks for receipt: true gets the receipt shape
// the tool catalog describes.

type messageSet struct {
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
}

type messageReceipts struct {
	Sent []messageReceipt `json:"sent"`
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
			return nil, errors.New("as_owner cannot be used with to_set")
		}
		return d.sendAsOwner(ctx, p.To, p.Body, kind)
	}
	if p.ToSet != nil {
		return d.sendSet(ctx, sid, p.ToSet, p.Body, kind, p.IdempotencyKey)
	}

	to, err := d.resolveTo(ctx, p.To)
	if err != nil {
		return nil, err
	}
	data, err := d.send(ctx, protocol.MsgSend{Sender: sid, Recipient: to, Type: kind,
		Body: p.Body, IdempotencyKey: p.IdempotencyKey})
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
		AsOwner        bool   `json:"as_owner"`
		IdempotencyKey string `json:"idempotency_key"`
		Receipt        bool   `json:"receipt"`
	}
	if json.Unmarshal(raw, &p) != nil || p.MessageID == "" {
		return nil, errors.New("reply needs message_id and body")
	}
	if p.AsOwner {
		if sid != d.ctoSessionID() {
			return nil, errors.New("as_owner is for the CTO session only; reply normally instead")
		}
		if p.ReplyOutcome != (protocol.ReplyOutcome{}) {
			return nil, errors.New("as_owner proposes a plain reply; leave out outcome, reason, redirect_to and recommendation")
		}
		return d.replyAsOwner(ctx, p.MessageID, p.Body)
	}
	p.ReplyOutcome, err = protocol.CleanOutcome(p.ReplyOutcome)
	if err != nil {
		return nil, err
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
	data, err = d.send(ctx, protocol.MsgSend{Sender: sid, Recipient: parent.Message.From,
		Type: protocol.TypeReply, Body: p.Body, ParentID: p.MessageID, IdempotencyKey: p.IdempotencyKey, ReplyOutcome: p.ReplyOutcome})
	if err != nil || !p.Receipt {
		return data, err
	}
	return d.receiptForSend(ctx, data, parent.Message.From), nil
}

// replyAsOwner stores an owner-reply proposal for the owner's decision. The
// approved reply goes from the CTO session with the owner's approval.
func (d *Daemon) replyAsOwner(ctx context.Context, messageID, body string) (any, error) {
	data, err := d.call(ctx, "query.message", protocol.QueryMessage{MessageID: messageID})
	if err != nil {
		return nil, err
	}
	var parent struct {
		Message protocol.Message `json:"message"`
	}
	if err := json.Unmarshal(data, &parent); err != nil {
		return nil, err
	}
	args, err := json.Marshal(map[string]any{
		"action":     "reply",
		"message_id": messageID,
		"body":       body,
		"as_owner":   true,
	})
	if err != nil {
		return nil, err
	}
	id, err := d.Propose(ctx, "message", "reply", args, "propose a reply from the owner")
	if err != nil {
		return nil, err
	}
	return messageReceipt{
		MessageID: id,
		To:        messageSessionRef{ID: parent.Message.From, Name: d.sessionName(ctx, parent.Message.From)},
		State:     "proposed",
	}, nil
}

// sendAsOwner stores an owner-send proposal for the owner's decision.
func (d *Daemon) sendAsOwner(ctx context.Context, to, body, kind string) (any, error) {
	if strings.TrimSpace(to) == "" {
		return nil, errors.New("as_owner needs to and body")
	}
	if kind != "" && kind != protocol.TypeRequest {
		return nil, errors.New("as_owner proposes a request, not a notification")
	}
	target, err := d.resolveTo(ctx, to)
	if err != nil {
		return nil, err
	}
	// The CTO message proposal carries only the fields its concrete schema
	// defines; a message type is always a request on that path.
	args, err := json.Marshal(map[string]any{
		"action":   "send",
		"to":       target,
		"body":     body,
		"as_owner": true,
	})
	if err != nil {
		return nil, err
	}
	id, err := d.Propose(ctx, "message", "send", args, "propose a message from the owner")
	if err != nil {
		return nil, err
	}
	return messageReceipts{Sent: []messageReceipt{{
		MessageID: id,
		To:        messageSessionRef{ID: target, Name: d.sessionName(ctx, target)},
		State:     "proposed",
	}}}, nil
}

// sendSet sends one message to every online session that matches the set.
// Only the CTO session and advisor nodes may call it. The receipt list always
// names every attempted recipient, so a partial failure can be repaired
// without sending twice.
func (d *Daemon) sendSet(ctx context.Context, sid string, set *messageSet, body, kind, key string) (any, error) {
	if !d.maySendGroup(ctx, sid) {
		return nil, errors.New("group messages are for the CTO and advisor nodes only; send to one session with to")
	}
	if strings.TrimSpace(set.Machine) == "" && strings.TrimSpace(set.Project) == "" && strings.TrimSpace(set.Harness) == "" {
		return nil, errors.New(`to_set needs a nonempty machine, project or harness filter; example: {"to_set":{"harness":"claude"},"body":"Please review."}`)
	}
	kinds, err := messageKinds(set.Kind)
	if err != nil {
		return nil, err
	}
	online := true
	if set.OnlineOnly != nil {
		online = *set.OnlineOnly
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
	if key == "" {
		key = protocol.NewID("k")
	}
	out := messageReceipts{Sent: []messageReceipt{}}
	seen := map[string]bool{}
	for _, a := range page.Agents {
		if a.SessionID == "" || a.SessionID == sid || seen[a.SessionID] || !matchesMessageSet(a, set, kinds) {
			continue
		}
		seen[a.SessionID] = true
		receipt := messageReceipt{To: messageSessionRef{ID: a.SessionID, Name: messageSessionName(a)}}
		data, sendErr := d.send(ctx, protocol.MsgSend{Sender: sid, Recipient: a.SessionID, Type: kind,
			Body: body, IdempotencyKey: key + ":" + a.SessionID})
		if sendErr != nil {
			receipt.State = "failed"
			out.Sent = append(out.Sent, receipt)
			continue
		}
		receipt.MessageID, receipt.State = messageStateOf(data)
		out.Sent = append(out.Sent, receipt)
	}
	return out, nil
}

// maySendGroup is the daemon-side authority check. A session is a CTO when it
// is the registered CTO network session. A node is an advisor when the graph
// has a governs edge in mode advisor whose source is the node the session
// verifiably works in (the repository folder, never an announced project).
func (d *Daemon) maySendGroup(ctx context.Context, sid string) bool {
	if sid != "" && sid == d.ctoSessionID() {
		return true
	}
	view, err := d.agentView(ctx, sid)
	if err != nil || view.Repo.Project == "" {
		return false
	}
	raw, err := d.call(ctx, "graph.read", protocol.GraphRead{})
	if err != nil {
		return false
	}
	var got protocol.GraphGot
	if json.Unmarshal(raw, &got) != nil {
		return false
	}
	for _, e := range got.Graph.Edges {
		if e.From == view.Repo.Project && e.Kind == "governs" && e.Mode == "advisor" {
			return true
		}
	}
	return false
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

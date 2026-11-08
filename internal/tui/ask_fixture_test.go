package tui

import (
	"time"

	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Fixtures turn the older shapes the tests were written in into the asks the
// Desk reads (decision 0090).

func askFromItem(it protocol.InboxItem) protocol.Ask {
	a := protocol.Ask{
		ID: it.ID, Kind: it.Kind, State: protocol.AskStateOpen, FromKind: protocol.AskFromSession, From: it.AskerName, FromSession: it.SessionID,
		Node: it.Node, Question: it.Text, Options: it.Options, Recommendation: it.Recommendation, Next: protocol.AskNextOwner,
		MessageID: it.MessageID, ConversationID: it.ConversationID, CreatedAt: it.CreatedAt, Delivery: it.Delivery,
	}
	if it.Delivery != nil {
		a.AboutKind, a.AboutID = protocol.AskAboutChange, it.Delivery.ChangeID
	}
	if it.ChangeTitle != "" {
		a.AboutTitle = it.ChangeTitle
	}
	return a
}

func askItems(items []protocol.InboxItem) []protocol.Ask {
	var out []protocol.Ask
	for _, it := range items {
		if it.Status != "" && it.Status != "open" {
			continue
		}
		out = append(out, askFromItem(it))
	}
	return out
}

func askFromHeld(msg protocol.Message) protocol.Ask {
	return protocol.Ask{ID: "i_held_" + msg.ID, Kind: protocol.AskKindHeld, State: protocol.AskStateOpen, FromKind: protocol.AskFromSession,
		From: msg.FromName, FromSession: msg.From, AboutKind: protocol.AskAboutSession, AboutID: msg.To, Question: msg.Body, Detail: msg.Body,
		Next: protocol.AskNextOwner, Ref: "held:" + msg.ID, MessageID: msg.ID, CreatedAt: msg.CreatedAt}
}

func askFromOutcome(e protocol.OutcomeInboxEntry) protocol.Ask {
	kind := protocol.AskKindRejection
	if e.Outcome == "escalate" {
		kind = protocol.AskKindEscalation
	}
	q := "Request: " + e.Request + "\nReason: " + e.Reason
	return protocol.Ask{ID: "i_out_" + e.ReplyID, Kind: kind, State: protocol.AskStateOpen, FromKind: protocol.AskFromSession, From: e.From, FromSession: e.Sender,
		Node: e.To, Question: q, Recommendation: e.Recommendation, Next: protocol.AskNextOwner, MessageID: e.ReplyID}
}

func askFromLearned(v protocol.LearnedSuggestion) protocol.Ask {
	kind, ref := protocol.AskKindLearned, "learned:"+itoa(int(v.ID))
	question, detail := `add "`+v.Phrase+`" to `+plainField(v.Field), ""
	if v.ID < 0 {
		kind, ref = protocol.AskKindDraft, "draft:"+itoa(int(-v.ID))
		question, detail = `set `+plainField(v.Field)+` to "`+v.NewValue+`" (drafted from `+v.By+`)`, "Drafted from "+v.By
	}
	return protocol.Ask{ID: "i_l" + itoa(int(v.ID)), Kind: kind, State: protocol.AskStateOpen, FromKind: protocol.AskFromGateway, From: "learned", Node: v.Node,
		AboutKind: protocol.AskAboutProject, AboutID: v.Node, Question: question, Detail: detail, Options: []string{"accept", "reject"}, Next: protocol.AskNextOwner, Ref: ref}
}

func plainField(field string) string {
	switch field {
	case "refuses":
		return "what it refuses"
	case "accepts":
		return "what it accepts"
	}
	return "its " + field
}

func askFromProposal(p protocol.CTOProposal) protocol.Ask {
	return protocol.Ask{ID: "i_p_" + p.ID, Kind: protocol.AskKindProposal, State: protocol.AskStateOpen, FromKind: protocol.AskFromCTO, From: "CTO",
		Question: strings.TrimSpace(p.Tool + " " + p.Action), Detail: p.Reason, Options: []string{"approve", "decline"}, Next: protocol.AskNextOwner,
		Ref: "proposal:" + p.NodeID + ":" + p.ID, CreatedAt: p.CreatedAt}
}

func askFromWaiting(session string, w *protocol.Waiting) protocol.Ask {
	return protocol.Ask{ID: "i_w_" + session, Kind: protocol.AskKindWaiting, State: protocol.AskStateOpen, FromKind: protocol.AskFromSession, FromSession: session,
		AboutKind: protocol.AskAboutSession, AboutID: session, Question: w.Text, Detail: w.Kind, Next: protocol.AskNextOwner, Ref: "waiting:" + session}
}

// withWaitingAsks gives every session of the snapshot that waits for its user
// the ask the gateway keeps for it.
func withWaitingAsks(m Model) Model {
	for _, a := range m.snap.Agents {
		if a.Waiting == nil || a.Worker != nil || protocol.IsOwner(a.Harness) {
			continue
		}
		ask := askFromWaiting(a.SessionID, a.Waiting)
		ask.From = a.Profile.Name
		ask.CreatedAt = time.Unix(a.Waiting.Since, 0)
		m.asks = append(m.asks, ask)
	}
	return m
}

// openProposalAsks is the ask list the gateway holds for the open proposals.
func openProposalAsks(list []protocol.CTOProposal) []protocol.Ask {
	var out []protocol.Ask
	for _, p := range list {
		if p.Status == "open" {
			out = append(out, askFromProposal(p))
		}
	}
	return out
}

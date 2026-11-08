package daemon

// The CTO's one read of everything that waits for the owner: charter drafts,
// its own open proposals, held requests and open Inbox asks. Each row names the
// exact call that resolves it, so the CTO can act without the owner's screen.
// cto_proposals_find carries it as a kind, so the CTO tool list does not grow.

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

var ctoOwnerItemKinds = map[string]bool{
	protocol.AskKindDraft: true, protocol.AskKindHeld: true,
	protocol.AskKindApproval: true, protocol.AskKindQuestion: true,
	protocol.AskKindEscalation: true, protocol.AskKindRejection: true,
}

func ctoOwnerInboxKind(kind string) bool {
	switch kind {
	case protocol.AskKindApproval, protocol.AskKindQuestion, protocol.AskKindEscalation, protocol.AskKindRejection:
		return true
	}
	return false
}

// ctoOwnerKindFilter accepts the same values as the catalog schema.
func ctoOwnerKindFilter(kind string) (string, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return protocol.AskKindProposal, nil
	}
	switch kind {
	case "all", "inbox", protocol.AskKindDraft, protocol.AskKindProposal, protocol.AskKindHeld,
		protocol.AskKindApproval, protocol.AskKindQuestion, protocol.AskKindEscalation, protocol.AskKindRejection:
		return kind, nil
	}
	return "", errors.New(`kind must be all, draft, proposal, held, inbox, approval, question, escalation or rejection. Example: {"kind":"draft"}`)
}

// ctoOwnerItemRows reads the gateway's open asks and the daemon's own open
// proposals as the flat rows of cto_proposals_find.
func (d *Daemon) ctoOwnerItemRows(ctx context.Context, kind string) ([]map[string]any, error) {
	rows := make([]map[string]any, 0)
	if kind != protocol.AskKindProposal {
		value, err := d.call(ctx, "asks.list", struct{}{})
		if err != nil {
			return nil, err
		}
		var result protocol.AsksResult
		if err := ctoDecode(value, &result); err != nil {
			return nil, err
		}
		for _, ask := range result.Asks {
			if !ctoOwnerItemKinds[ask.Kind] {
				continue
			}
			if ask.Next != "" && ask.Next != protocol.AskNextOwner {
				continue
			}
			if kind != "all" && kind != "inbox" && ask.Kind != kind {
				continue
			}
			if kind == "inbox" && !ctoOwnerInboxKind(ask.Kind) {
				continue
			}
			rows = append(rows, ctoOwnerAskRow(ask))
		}
	}
	if kind == "all" || kind == protocol.AskKindProposal {
		items, err := d.proposalRunner().list()
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.Status != "open" {
				continue
			}
			rows = append(rows, ctoOwnerProposalRow(item))
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, _ := rows[i]["created"].(string)
		b, _ := rows[j]["created"].(string)
		if a != b {
			return a > b
		}
		return rows[i]["id"].(string) < rows[j]["id"].(string)
	})
	return rows, nil
}

func ctoOwnerSummary(text string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	first = strings.TrimSpace(protocol.CleanWaitingText(first))
	if r := []rune(first); len(r) > 200 {
		first = string(r[:200])
	}
	return first
}

func ctoOwnerFirst(options []string, fallback string) string {
	for _, option := range options {
		if strings.TrimSpace(option) != "" {
			return option
		}
	}
	return fallback
}

// ctoOwnerCall is the exact tool call that resolves one row.
func ctoOwnerCall(tool string, arguments map[string]any) string {
	raw, _ := json.Marshal(arguments)
	return tool + " " + string(raw)
}

func ctoOwnerAskRow(ask protocol.Ask) map[string]any {
	options := ask.Options
	if options == nil {
		options = []string{}
	}
	from := ask.From
	if from == "" {
		from = "gateway"
	}
	summary := ctoOwnerSummary(ask.Question)
	row := map[string]any{
		"id": ask.ID, "tool": "answer", "status": "open", "reason": summary,
		"created": ctoCatalogTime(ask.CreatedAt), "kind": ask.Kind, "from": from,
		"summary": summary, "options": options,
	}
	switch ask.Kind {
	case protocol.AskKindDraft:
		if len(options) == 0 {
			options = []string{"accept", "reject"}
			row["options"] = options
		}
		answer := ctoOwnerFirst(options, "accept")
		row["action"] = answer
		row["answer"] = ctoOwnerCall("answer", map[string]any{"kind": "inbox", "id": ask.ID, "answer": answer})
	case protocol.AskKindHeld:
		if len(options) == 0 {
			options = []string{"approve", "refuse"}
			row["options"] = options
		}
		action := "a"
		if strings.EqualFold(ctoOwnerFirst(options, "approve"), "refuse") || strings.EqualFold(ctoOwnerFirst(options, "approve"), "decline") {
			action = "r"
		}
		message := ask.MessageID
		if message == "" {
			message = strings.TrimPrefix(ask.Ref, "held:")
		}
		row["action"] = action
		row["answer"] = ctoOwnerCall("answer", map[string]any{"kind": "held", "message_id": message, "action": action})
	case protocol.AskKindEscalation, protocol.AskKindRejection:
		if len(options) == 0 {
			options = []string{"let_stand", "overrule"}
			row["options"] = options
		}
		action := ctoOwnerFirst(options, "let_stand")
		if action != "overrule" {
			action = "let_stand"
		}
		row["action"] = action
		row["answer"] = ctoOwnerCall("answer", map[string]any{"kind": "outcome", "reply_id": ask.MessageID, "action": action})
	case protocol.AskKindApproval:
		if len(options) == 0 {
			options = []string{"approve", "refuse"}
			row["options"] = options
		}
		answer := ctoOwnerFirst(options, "approve")
		row["action"] = answer
		row["answer"] = ctoOwnerCall("answer", map[string]any{"kind": "inbox", "id": ask.ID, "answer": answer})
	default:
		answer := ctoOwnerFirst(options, "")
		row["action"] = answer
		row["answer"] = ctoOwnerCall("answer", map[string]any{"kind": "inbox", "id": ask.ID, "answer": answer})
	}
	return row
}

func ctoOwnerProposalRow(item proposal) map[string]any {
	text := "CTO proposes " + strings.ReplaceAll(item.Call.Tool, "_", " ")
	if item.Call.Action != "" {
		text += " " + item.Call.Action
	}
	if reason := ctoOwnerSummary(item.Reason); reason != "" {
		text += ": " + reason
	}
	return map[string]any{
		"id": item.ID, "tool": item.Call.Tool, "action": item.Call.Action, "status": "open",
		"reason": ctoCut(item.Reason, 80), "created": ctoCatalogTime(item.CreatedAt),
		"kind": protocol.AskKindProposal, "from": "CTO", "summary": text,
		"options": []string{"approve", "decline"},
		"answer":  ctoOwnerCall("proposal_ask", map[string]any{"id": item.ID}),
	}
}

package daemon

// The CTO's one read of everything that waits for the owner: charter drafts,
// its own open proposals, held requests, permission proposals and open Inbox
// asks. Each row names the
// exact call that resolves it, so the CTO can act without the owner's screen.
// cto_proposals_find carries it as a kind, so the CTO tool list does not grow.

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

var ctoOwnerItemKinds = map[string]bool{
	protocol.AskKindDraft: true, protocol.AskKindHeld: true, protocol.AskKindLearned: true,
	protocol.AskKindApproval: true, protocol.AskKindQuestion: true,
	protocol.AskKindEscalation: true, protocol.AskKindRejection: true,
	protocol.AskKindPermission: true,
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
	case "all", "inbox", protocol.AskKindDraft, protocol.AskKindLearned, protocol.AskKindProposal, protocol.AskKindHeld,
		protocol.AskKindApproval, protocol.AskKindQuestion, protocol.AskKindEscalation, protocol.AskKindRejection, protocol.AskKindPermission:
		return kind, nil
	}
	return "", errors.New(`kind must be all, draft, learned, proposal, held, inbox, approval, question, escalation, rejection or permission. Example: {"kind":"draft"}`)
}

// ctoOwnerItemRows reads the gateway's open asks and the daemon's own open
// proposals as the flat rows of cto_proposals_find.
func (d *Daemon) ctoOwnerItemRows(ctx context.Context, kind string) ([]map[string]any, error) {
	rows := make([]map[string]any, 0)
	if kind == protocol.AskKindDraft {
		// The drafts themselves, with each one's text, besides the asks that
		// wait for the owner: an ask the chief sent on holds no owner row, and
		// the CTO decides a field at a time.
		drafts, err := d.ctoCharterDrafts(ctx)
		if err != nil {
			return nil, err
		}
		for _, draft := range drafts {
			rows = append(rows, ctoCharterDraftRow(draft))
		}
	}
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
		items, err := d.proposalRunner().listOpen()
		if err != nil {
			return nil, err
		}
		for _, item := range items {
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
	case protocol.AskKindLearned:
		// A pending major learned proposal is decided by its suggestion ID, the
		// same call the owner's accept and reject keys make.
		if len(options) == 0 {
			options = []string{"accept", "reject"}
			row["options"] = options
		}
		action := "accept"
		if strings.EqualFold(ctoOwnerFirst(options, "accept"), "reject") {
			action = "reject"
		}
		row["action"] = action
		row["tool"] = "settings_change"
		id, parseErr := strconv.ParseInt(strings.TrimPrefix(ask.Ref, "learned:"), 10, 64)
		if parseErr == nil && strings.HasPrefix(ask.Ref, "learned:") {
			row["answer"] = ctoOwnerCall("settings_change", map[string]any{
				"area": "operator", "method": "learned_answer",
				"arguments": map[string]any{"suggestion_id": id, "action": action},
			})
		} else {
			row["answer"] = ctoOwnerCall("answer", map[string]any{"kind": "inbox", "id": ask.ID, "answer": action})
		}
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
	case protocol.AskKindPermission:
		// The row is the proposal itself, by its pp_ id. An open ask means the
		// proposal is pending. The CTO applies it on the owner's chat words.
		id := strings.TrimPrefix(ask.Ref, "permission:")
		row["id"], row["status"], row["tool"], row["action"] = id, "pending", "cto_op", protocol.OpPermissionApply
		row["answer"] = ctoPermissionApplyCall(id)
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

// ctoPermissionApplyCall is the call that applies a waiting permission
// proposal on the owner's own chat words.
func ctoPermissionApplyCall(id string) string {
	return ctoOwnerCall("cto_op", map[string]any{"method": protocol.OpPermissionApply, "args": map[string]any{"id": id, "owner_words": "<quote the owner's instruction>"}})
}

// ctoPermissionNote tells the CTO when it may apply a permission proposal.
const ctoPermissionNote = "A permission proposal waits on the owner's Desk. If the owner told you in this chat to grant it, apply it with cto_op permission_apply and quote his exact words as owner_words: the daemon checks them against what he typed, and it applies at once with no Desk item. Never file a Desk proposal for something he asked. Otherwise leave it on the Desk."

// ctoPermissionProposal reads one permission proposal by its pp_ id as a
// cto_proposals_find row.
func (d *Daemon) ctoPermissionProposal(ctx context.Context, id string) (map[string]any, error) {
	value, err := d.call(ctx, protocol.FramePermissionGet, protocol.PermissionGetFrame{ProposalID: id, Owner: d.ownerSessionID()})
	if err != nil {
		return nil, err
	}
	var p protocol.PermissionProposal
	if err := ctoDecode(value, &p); err != nil {
		return nil, err
	}
	var targets []string
	for _, t := range p.Targets {
		targets = append(targets, t.Name)
	}
	summary := "Permission proposal for " + strings.Join(targets, ", ")
	if len(p.Rules) > 0 {
		summary += ": " + strings.Join(p.Rules, ", ")
	}
	row := map[string]any{
		"id": p.ID, "tool": "cto_op", "action": protocol.OpPermissionApply, "status": p.Status,
		"reason": ctoCut(p.Reason, 80), "created_at": ctoCatalogTime(time.Unix(0, p.CreatedAt)),
		"kind": protocol.AskKindPermission, "from": p.ProposedName, "summary": ctoCut(summary, 200),
		"options": []string{"approve", "reject"}, "note": ctoPermissionNote,
	}
	if p.Status == "pending" {
		row["answer"] = ctoPermissionApplyCall(p.ID)
	} else {
		row["note"] = "This permission proposal is " + p.Status + "; nothing waits."
	}
	return row, nil
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

// ctoCharterDraftPrefix starts the id of a charter draft row. A draft is
// also found by its plain number, as the chief names it.
const ctoCharterDraftPrefix = "cd_"

// ctoCharterDraftID reads a draft number from cd_<n> or <n>.
func ctoCharterDraftID(id string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(id), ctoCharterDraftPrefix), 10, 64)
	return n, err == nil && n > 0
}

// ctoCharterDrafts lists the pending charter drafts, oldest first.
func (d *Daemon) ctoCharterDrafts(ctx context.Context) ([]protocol.CharterDraft, error) {
	value, err := d.call(ctx, "charter.drafts", map[string]any{"owner": d.ownerSessionID()})
	if err != nil {
		return nil, err
	}
	var out []protocol.CharterDraft
	return out, ctoDecode(value, &out)
}

// ctoCharterDraft reads one draft in any status.
func (d *Daemon) ctoCharterDraft(ctx context.Context, id int64) (map[string]any, error) {
	value, err := d.call(ctx, "charter.drafts", map[string]any{"owner": d.ownerSessionID(), "draft": id})
	if err != nil {
		return nil, err
	}
	var draft protocol.CharterDraft
	if err := ctoDecode(value, &draft); err != nil {
		return nil, err
	}
	return ctoCharterDraftRow(draft), nil
}

// ctoCharterDraftRow is one draft as a cto_proposals_find row. A pending draft
// names the call that applies it; the reject call is the same with action
// reject.
func ctoCharterDraftRow(draft protocol.CharterDraft) map[string]any {
	row := map[string]any{
		"id": ctoCharterDraftPrefix + strconv.FormatInt(draft.ID, 10), "kind": protocol.AskKindDraft, "tool": "cto_op", "action": protocol.OpLearnedAnswer,
		"status": draft.Status, "level": draft.Level, "scope": draft.Scope, "attr": draft.Attr, "value": draft.Value,
		"source": draft.Source, "drafted_by": draft.DraftedBy, "from": draft.DraftedBy,
		"created": ctoCatalogTime(time.Unix(0, draft.CreatedAt)), "options": []string{"accept", "reject"},
		"summary": ctoCut("Charter draft: "+draft.Level+" "+draft.Scope+" "+draft.Attr+" = "+draft.Value, 200),
	}
	if draft.Name != "" {
		row["name"] = draft.Name
	}
	if draft.DecidedBy != "" {
		row["decided_by"] = draft.DecidedBy
	}
	if draft.Status == "pending" {
		row["answer"] = ctoOwnerCall("cto_op", map[string]any{"method": protocol.OpLearnedAnswer, "args": map[string]any{"suggestion_id": -draft.ID, "action": "accept"}})
		row["note"] = "accept writes the value with the charter write and can be undone with graph_change action undo on the history_id it returns; reject writes nothing."
	}
	return row
}

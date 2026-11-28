package daemon

// The CTO's one read of everything that waits for the owner: charter drafts
// and its own open proposals. Each row names the exact call that resolves it,
// so the CTO can act without the owner's screen. cto_proposals_find carries it
// as a kind, so the CTO tool list does not grow.

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

// ctoOwnerKindFilter accepts the same values as the catalog schema.
func ctoOwnerKindFilter(kind string) (string, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return "proposal", nil
	}
	switch kind {
	case "all", "draft", "proposal":
		return kind, nil
	}
	return "", errors.New(`kind must be all, draft or proposal. Example: {"kind":"draft"}`)
}

// ctoOwnerItemRows reads pending charter drafts and the daemon's own open
// proposals as the flat rows of cto_proposals_find.
func (d *Daemon) ctoOwnerItemRows(ctx context.Context, kind string) ([]map[string]any, error) {
	rows := make([]map[string]any, 0)
	var drafts []map[string]any
	if kind == "all" || kind == "draft" {
		list, err := d.ctoCharterDrafts(ctx)
		if err != nil {
			return nil, err
		}
		for _, draft := range list {
			drafts = append(drafts, ctoCharterDraftRow(draft))
		}
	}
	if kind == "all" || kind == "proposal" {
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
	return append(drafts, rows...), nil
}

func ctoOwnerSummary(text string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	first = strings.TrimSpace(protocol.CleanWaitingText(first))
	if r := []rune(first); len(r) > 200 {
		first = string(r[:200])
	}
	return first
}

// ctoOwnerCall is the exact tool call that resolves one row.
func ctoOwnerCall(tool string, arguments map[string]any) string {
	raw, _ := json.Marshal(arguments)
	return tool + " " + string(raw)
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
		"kind": "proposal", "from": "CTO", "summary": text,
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
	if err := ctoDecode(value, &out); err != nil {
		return nil, err
	}
	sortCharterDraftsOldestFirst(out)
	return out, nil
}

// sortCharterDraftsOldestFirst orders drafts by when they were made, then by
// draft number as a number, so draft 9 comes before draft 10. The gateway
// stamps drafts in nanoseconds; two drafts in the same second still have a
// fixed order.
func sortCharterDraftsOldestFirst(drafts []protocol.CharterDraft) {
	sort.Slice(drafts, func(i, j int) bool {
		if drafts[i].CreatedAt != drafts[j].CreatedAt {
			return drafts[i].CreatedAt < drafts[j].CreatedAt
		}
		return drafts[i].ID < drafts[j].ID
	})
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
		"id": ctoCharterDraftPrefix + strconv.FormatInt(draft.ID, 10), "kind": "draft", "tool": "cto_op", "action": protocol.OpDraftAnswer,
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
		row["answer"] = ctoOwnerCall("cto_op", map[string]any{"method": protocol.OpDraftAnswer, "args": map[string]any{"draft_id": draft.ID, "action": "accept"}})
		row["note"] = "accept writes the value with the charter write and can be undone with graph_change action undo on the history_id it returns; reject writes nothing."
	}
	return row
}

func ctoCatalogTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

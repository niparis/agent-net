package inventory

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestMergeReportsKeepsFreshItemsWhenOneSourceFails(t *testing.T) {
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	previous := protocol.InventoryReport{State: protocol.InventoryOK, Items: []protocol.InventoryItem{
		{Kind: "skill", Name: "demo", Location: "/home/n/.claude/skills/demo", Harness: "claude", Scope: "user", Hash: "old", State: protocol.InventoryOK},
	}}
	fresh := protocol.InventoryReport{State: protocol.InventoryError, ScannedAt: at, Items: []protocol.InventoryItem{
		{Kind: "skill", Name: "demo", Location: "/home/n/.claude/skills/demo", Harness: "claude", Scope: "user", Hash: "new", State: protocol.InventoryOK, Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Size: 5}}}},
		{Kind: "skill", Name: "linked", Location: "/home/n/.claude/skills/linked", Harness: "claude", Scope: "user", State: protocol.InventoryError, Reason: "skill root is a symlink"},
	}}
	got := MergeReports(previous, fresh)
	if got.State != protocol.InventoryStale {
		t.Fatalf("state: %s", got.State)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items: %+v", got.Items)
	}
	if got.Items[0].Hash != "new" || got.Items[0].Manifest == nil || got.Items[0].State != protocol.InventoryOK {
		t.Fatalf("fresh item lost: %+v", got.Items[0])
	}
	if got.Items[1].State != protocol.InventoryError {
		t.Fatalf("new error lost: %+v", got.Items[1])
	}
	if !got.ScannedAt.Equal(at) {
		t.Fatalf("scan time: %v", got.ScannedAt)
	}
}

func TestMergeReportsKeepsPreviousFailedSource(t *testing.T) {
	previous := protocol.InventoryReport{State: protocol.InventoryOK, Items: []protocol.InventoryItem{
		{Kind: "skill", Name: "demo", Location: "/home/n/.claude/skills/demo", Harness: "claude", Scope: "user", Hash: "old", State: protocol.InventoryOK, Manifest: &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md"}}}},
	}}
	fresh := protocol.InventoryReport{State: protocol.InventoryError, Items: []protocol.InventoryItem{
		{Kind: "skill", Name: "demo", Location: "/home/n/.claude/skills/demo", Harness: "claude", Scope: "user", State: protocol.InventoryError, Reason: "cannot hash skill safely"},
		{Kind: "skill", Name: "other", Location: "/home/n/.claude/skills/other", Harness: "claude", Scope: "user", Hash: "new", State: protocol.InventoryOK},
	}}
	got := MergeReports(previous, fresh)
	if len(got.Items) != 2 || got.Items[0].Hash != "old" || got.Items[0].State != protocol.InventoryStale || got.Items[0].Manifest == nil {
		t.Fatalf("previous source not kept: %+v", got.Items)
	}
	if got.Items[1].Hash != "new" || got.Items[1].State != protocol.InventoryOK {
		t.Fatalf("fresh source lost: %+v", got.Items[1])
	}
}

func TestMergeReportsKeepsSkillsUnderFailedSourceRoot(t *testing.T) {
	root := "/home/n/.claude/skills"
	previous := protocol.InventoryReport{State: protocol.InventoryOK, Items: []protocol.InventoryItem{
		{Kind: "skill", Name: "one", Harness: "claude", Scope: "user", Location: root + "/one", Hash: "a", State: protocol.InventoryOK},
		{Kind: "skill", Name: "two", Harness: "claude", Scope: "user", Location: root + "/two", Hash: "b", State: protocol.InventoryOK},
	}}
	fresh := protocol.InventoryReport{State: protocol.InventoryError, Items: []protocol.InventoryItem{
		{Kind: "skill", Harness: "claude", Scope: "user", Location: root, State: protocol.InventoryError, Reason: "source contains a symlink"},
		{Kind: "skill", Name: "elsewhere", Harness: "claude", Scope: "user", Location: "/home/n/.agents/skills/elsewhere", Hash: "c", State: protocol.InventoryOK},
	}}
	got := MergeReports(previous, fresh)
	if len(got.Items) != 4 {
		t.Fatalf("items: %+v", got.Items)
	}
	kept := 0
	for _, item := range got.Items {
		if (item.Name == "one" || item.Name == "two") && item.State == protocol.InventoryStale {
			kept++
		}
	}
	if kept != 2 {
		t.Fatalf("skills under the failed root not kept: %+v", got.Items)
	}
}

func TestMergeReportsKeepsPreviousWhenNothingScanned(t *testing.T) {
	previous := protocol.InventoryReport{State: protocol.InventoryOK, Items: []protocol.InventoryItem{{Kind: "skill", Name: "demo", State: protocol.InventoryOK}}}
	got := MergeReports(previous, protocol.InventoryReport{State: protocol.InventoryError})
	if got.State != protocol.InventoryStale || len(got.Items) != 1 || got.Items[0].State != protocol.InventoryStale {
		t.Fatalf("previous report lost: %+v", got)
	}
}

func TestMergeReportsAllFailedPreservesRetainedItemReason(t *testing.T) {
	previous := protocol.InventoryReport{State: protocol.InventoryStale, Items: []protocol.InventoryItem{
		{Kind: "skill", Name: "retained", State: protocol.InventoryStale, Reason: "absolute symlink (link)"},
		{Kind: "skill", Name: "without-reason", State: protocol.InventoryOK},
	}}
	fresh := protocol.InventoryReport{State: protocol.InventoryError, Reason: "project registry unavailable"}
	got := MergeReports(previous, fresh)
	if got.Items[0].Reason != previous.Items[0].Reason {
		t.Fatalf("lost item cause: %+v", got.Items[0])
	}
	if got.Items[1].Reason != fresh.Reason {
		t.Fatalf("missing report fallback: %+v", got.Items[1])
	}
}

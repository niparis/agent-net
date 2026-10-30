package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestInventoryReportJSON(t *testing.T) {
	r := InventoryReport{Machine: "machine", ScannedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Items: []InventoryItem{{Harness: "codex", Kind: InventorySkill, Scope: InventoryProject, Project: "project", ProjectRoot: "/project", Location: "/project/.agents/skills/a", Name: "a", Source: InventoryNative, Hash: "abc", State: InventoryOK, Unsupported: []string{"manual"}}}}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var got InventoryReport
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Machine != r.Machine || len(got.Items) != 1 || got.Items[0].Hash != "abc" || got.Items[0].Unsupported[0] != "manual" || !got.ScannedAt.Equal(r.ScannedAt) {
		t.Fatalf("round trip: %+v", got)
	}
}

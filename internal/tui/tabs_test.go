package tui

import (
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestTabSwitching(t *testing.T) {
	m := newModel(80, 24)
	if !strings.HasPrefix(ansi.Strip(m.titleLine(80)), "1 Sessions   2 Quotas") {
		t.Fatal(m.titleLine(80))
	}
	m = keys(m, "2")
	if m.tab != 1 {
		t.Fatal("2 did not select quotas")
	}
	m = keys(m, "1")
	if m.tab != 0 {
		t.Fatal("1 did not select sessions")
	}
	m = keys(m, "/", "2")
	if m.tab != 0 || m.filter != "2" {
		t.Fatal("filter intercepted tab key")
	}
}

func TestMergeQuotaReadings(t *testing.T) {
	m := newModel(80, 24)
	m.usage.raw.Local.Quotas = &protocol.Quotas{GeneratedAt: 20, Providers: []protocol.QuotaProvider{{Provider: "codex", Plan: "pro"}, {Provider: "claude", Plan: "max"}}}
	m.usage.raw.Gateway.Nodes = []protocol.UsageNode{{NodeID: "other", Hostname: "other", Usage: protocol.UsageReport{Quotas: &protocol.Quotas{GeneratedAt: 10, Providers: []protocol.QuotaProvider{{Provider: "codex", Plan: "pro"}, {Provider: "claude", Plan: "team"}}}}}}
	rows := m.quotaRows()
	if len(rows) != 3 {
		t.Fatalf("rows: %+v", rows)
	}
	for _, r := range rows {
		if r.provider.Provider == "codex" && (r.at != 20 || r.machine != "") {
			t.Fatalf("merge: %+v", r)
		}
		if r.provider.Provider == "claude" && r.machine == "" {
			t.Fatalf("different plans need machine: %+v", r)
		}
	}
}

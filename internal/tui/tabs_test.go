package tui

import (
	"fmt"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestTabSwitching(t *testing.T) {
	m := newModel(80, 24)
	if !strings.HasPrefix(ansi.Strip(m.titleLine(80)), "1 Sessions  2 Quotas") {
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

func TestQuotaViewGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := newModel(size[0], size[1])
		m.tab = 1
		m.usage.raw.Local.Quotas = &protocol.Quotas{GeneratedAt: t0.Add(-40 * time.Second).Unix(), Providers: []protocol.QuotaProvider{
			{Provider: "codex", Plan: "pro", Windows: []protocol.QuotaWindow{{ID: "week", PercentUsed: 92, Pace: protocol.QuotaPaceAhead, ResetsAt: t0.Add(2 * time.Hour).Unix()}}},
			{Provider: "cursor", Plan: "business", Windows: []protocol.QuotaWindow{{ID: "month", PercentUsed: 100, Exhausted: true}}},
			{Provider: "claude", Plan: "max", Stale: true, Windows: []protocol.QuotaWindow{{ID: "5h", PercentUsed: 50, Pace: protocol.QuotaPaceBehind}}},
		}}
		m.usage.raw.Gateway.Nodes = append(m.usage.raw.Gateway.Nodes, protocol.UsageNode{NodeID: "other", Hostname: "other", Usage: protocol.UsageReport{Quotas: &protocol.Quotas{GeneratedAt: t0.Add(-time.Minute).Unix(), Providers: []protocol.QuotaProvider{{Provider: "codex", Plan: "pro"}}}}})
		view := m.View()
		for _, line := range lines(view) {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("wide line: %q", line)
			}
		}
		for _, line := range lines(ansi.Strip(view)) {
			for _, value := range []string{"behind", "ahead", "exhausted"} {
				if i := strings.Index(line, value); i >= 0 {
					col := ansi.StringWidth(line[:i])
					if value == "exhausted" {
						col = ansi.StringWidth(line[:i+len(value)]) + 1
					}
					if col != 60 {
						t.Errorf("pace starts at column %d in %q", col, line)
					}
				}
			}
		}
		if !strings.Contains(ansi.Strip(view), "exhausted") {
			t.Fatal(view)
		}
		golden(t, fmt.Sprintf("quotas_%dx%d", size[0], size[1]), view)
	}
}

func TestQuotaViewKeepsFooterWithManyRows(t *testing.T) {
	m := newModel(80, 24)
	m.tab = 1
	q := &protocol.Quotas{GeneratedAt: t0.Unix()}
	for i := 0; i < 30; i++ {
		q.Providers = append(q.Providers, protocol.QuotaProvider{Provider: fmt.Sprintf("provider%d", i), Windows: []protocol.QuotaWindow{{ID: "week", PercentUsed: 50}}})
	}
	m.usage.raw.Local.Quotas = q
	if !strings.Contains(ansi.Strip(m.View()), "workers now:") {
		t.Fatal("footer lost")
	}
}

func TestQuotaViewScrolls(t *testing.T) {
	m := newModel(80, 24)
	m.tab = 1
	q := &protocol.Quotas{}
	for i := 0; i < 30; i++ {
		q.Providers = append(q.Providers, protocol.QuotaProvider{Provider: fmt.Sprintf("p%02d", i), Windows: []protocol.QuotaWindow{{ID: "week"}}})
	}
	m.usage.raw.Local.Quotas = q
	if strings.Contains(ansi.Strip(m.View()), "p29") {
		t.Fatal("last row visible before scroll")
	}
	for i := 0; i < 80; i++ {
		m = keys(m, "down")
	}
	if !strings.Contains(ansi.Strip(m.View()), "p29") {
		t.Fatal("last row not reachable")
	}
}

func TestQuotaEmptyAndMachineErrors(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 1
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "No machine reads quotas yet: quota-axi is not installed on any machine (see docs/setup.md, Quota).") || strings.Contains(v, "PROVIDER") {
		t.Fatal(v)
	}
	m.usage.raw.Gateway.Nodes = append(m.usage.raw.Gateway.Nodes, protocol.UsageNode{NodeID: "error-node", Hostname: "build", Usage: protocol.UsageReport{QuotaError: "it failed: exit status 1"}})
	v = ansi.Strip(m.View())
	if !strings.Contains(v, "build: it failed: exit status 1") || strings.Contains(v, "not installed on any machine") || strings.Contains(v, "PROVIDER") {
		t.Fatal(v)
	}
}

func TestTabStyleAndTitleSeparator(t *testing.T) {
	if !sActiveTab.GetBold() || !sActiveTab.GetUnderline() {
		t.Fatal("active tab must be bold and underlined")
	}
	m := newModel(80, 24)
	if !strings.HasPrefix(ansi.Strip(m.titleLine(80)), "1 Sessions  2 Quotas │ ") {
		t.Fatal(ansi.Strip(m.titleLine(80)))
	}
	m.tab = 1
	if !strings.HasPrefix(ansi.Strip(m.titleLine(80)), "1 Sessions  2 Quotas │ ") {
		t.Fatal(ansi.Strip(m.titleLine(80)))
	}
}

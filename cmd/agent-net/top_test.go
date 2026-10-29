package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestReorderFlags(t *testing.T) {
	got := reorderFlags([]string{"mysession", "-n", "20", "--follow", "--before=3:1"})
	want := []string{"-n", "20", "--follow", "--before=3:1", "mysession"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reorderFlags = %v, want %v", got, want)
	}
	if got := reorderFlags([]string{"--before", "9:0", "s_1"}); !reflect.DeepEqual(got, []string{"--before", "9:0", "s_1"}) {
		t.Errorf("reorderFlags = %v", got)
	}
}

func agent(id, name, harness, host, node string) protocol.AgentView {
	return protocol.AgentView{SessionID: id, NodeID: node, Hostname: host, Harness: harness, Online: true,
		Profile: protocol.Profile{Name: name}}
}

func TestFormatUsageShowsMachinesSessionsSharedAndUnknown(t *testing.T) {
	agents := []protocol.AgentView{
		agent("s_claude", "api", "claude", "mac", "n1"),
		agent("s_pi", "wintermute", "pi", "mac", "n1"),
		agent("s_oc1", "strategies", "opencode", "vm", "n2"),
		agent("s_oc2", "docs", "opencode", "vm", "n2"),
		agent("s_old", "legacy", "claude", "oldbox", "n3"),
	}
	u := usageReply{NodeID: "n1", Hostname: "mac",
		Local: &protocol.UsageReport{
			Machine: protocol.MachineUsage{Cores: 8, CPU: 42.4, CPUAvg: 30, MemUsed: 6 << 30, MemUsedAvg: 5 << 30, MemTotal: 16 << 30, Load: [3]float64{2.5, 2, 1}, Procs: 600, Samples: 40},
			Trees: []protocol.TreeUsage{
				{PID: 10, Harness: "claude", Sessions: []string{"s_claude"}, CPU: 250, CPUAvg: 80, RSS: 3 << 30, RSSAvg: 2 << 30, Procs: 41, Samples: 40},
				{PID: 11, Harness: "pi", Sessions: []string{"s_pi"}, CPU: 1, CPUAvg: 2, RSS: 200 << 20, RSSAvg: 190 << 20, Procs: 3, Samples: 0},
			}},
		Gateway: &protocol.UsageAnswer{Nodes: []protocol.UsageNode{
			{NodeID: "n2", Hostname: "vm", AgeSeconds: 4, Usage: protocol.UsageReport{
				Machine: protocol.MachineUsage{Cores: 2, CPU: 5, CPUAvg: 6, MemUsed: 1 << 30, MemUsedAvg: 1 << 30, MemTotal: 4 << 30, Procs: 90, Samples: 3},
				Trees: []protocol.TreeUsage{{PID: 77, Harness: "opencode", Shared: true, Sessions: []string{"s_oc1", "s_oc2"}, CPU: 33, CPUAvg: 20, RSS: 900 << 20, RSSAvg: 800 << 20, Procs: 12, Samples: 3}}}},
		}}}
	var b bytes.Buffer
	formatUsage(&b, u, agents, &protocol.SessionCounts{Online: 5, Offline24h: 7, OfflineTotal: 122}, false, time.Now())
	out := b.String()
	for _, want := range []string{
		"Sessions: 5 online, 7 offline in the last 24 h (122 offline kept in all)",
		"mac (here)", "42%", "6.0/16.0G",
		"vm ", "90",
		"oldbox", "unknown", // a machine with no report
		"s_claude", "250%", "3.0G",
		"Shared by 2 opencode sessions on vm (pid 77)",
		"strategies, docs",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// The shared figures are not repeated per session, and the new tree has "..." for CPU.
	if strings.Count(out, "33%") != 1 {
		t.Errorf("shared CPU should appear once:\n%s", out)
	}
	if !strings.Contains(out, "...") {
		t.Errorf("a tree with no CPU sample should show ...:\n%s", out)
	}
	// An offline session with no figures is only listed with --all.
	if !strings.Contains(out, "s_old") {
		t.Errorf("an online session with no figures should show as unknown:\n%s", out)
	}
}

func TestFormatUsageTreatsAStaleReportAsUnknown(t *testing.T) {
	u := usageReply{NodeID: "n1", Hostname: "mac", Gateway: &protocol.UsageAnswer{Nodes: []protocol.UsageNode{
		{NodeID: "n2", Hostname: "vm", AgeSeconds: 600, Usage: protocol.UsageReport{Machine: protocol.MachineUsage{CPU: 99, Samples: 4}}},
	}}}
	var b bytes.Buffer
	formatUsage(&b, u, []protocol.AgentView{agent("s1", "x", "claude", "vm", "n2")}, nil, false, time.Now())
	if strings.Contains(b.String(), "99%") || !strings.Contains(b.String(), "unknown") {
		t.Errorf("stale figures shown:\n%s", b.String())
	}
}

func TestFormatTopListsProcesses(t *testing.T) {
	var b bytes.Buffer
	formatTop(&b, protocol.UsageTop{SessionID: "s_1", Tree: protocol.TreeUsage{PID: 5, Harness: "claude", CPU: 120, CPUAvg: 50, RSS: 2 << 30, RSSAvg: 1 << 30, Procs: 9, Samples: 4},
		ByCPU: []protocol.ProcUsage{{PID: 6, Cmd: "node", Args: "node server.js --token [REDACTED]", CPU: 99, RSS: 300 << 20}},
		ByRSS: []protocol.ProcUsage{{PID: 7, Cmd: "chrome", Args: "chrome --headless", CPU: 1, RSS: 1 << 30}}})
	for _, want := range []string{"Session s_1 (claude)", "9 in all", "Top by CPU", "Top by memory", "node server.js --token [REDACTED]", "chrome"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("lacks %q:\n%s", want, b.String())
		}
	}
}

func TestPrintTextKeepsLinesTogetherAndMarksCuts(t *testing.T) {
	var b bytes.Buffer
	printText(&b, protocol.TextPage{Entries: []protocol.TextEntry{
		{Time: 1793268000000, Role: protocol.RoleUser, Text: "line one\nline two\n"},
		{Role: protocol.RoleToolCall, Tool: "Bash", Text: "Bash: ls", Sidechain: true},
		{Role: protocol.RoleToolResult, Text: "long", Cut: true},
	}}, false)
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("lines = %q", lines)
	}
	if !strings.Contains(lines[0], "user") || !strings.HasSuffix(lines[0], "line one") || !strings.HasSuffix(lines[1], "line two") {
		t.Errorf("lines = %q", lines)
	}
	if !strings.Contains(lines[2], "--:--:--") || !strings.Contains(lines[2], "tool*") || !strings.HasSuffix(lines[3], "long [cut]") {
		t.Errorf("lines = %q", lines)
	}
}

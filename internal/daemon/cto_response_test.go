package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOCompactNodesAndAgents(t *testing.T) {
	nodes := protocol.NodesInfo{}
	agents := protocol.AgentsResult{}
	for i := 0; i < 50; i++ {
		h := protocol.HarnessInfo{Name: "pi", Version: "1"}
		for j := 0; j < 400; j++ {
			h.Models = append(h.Models, fmt.Sprintf("provider/model-%03d", j))
		}
		nodes.Nodes = append(nodes.Nodes, protocol.NodeView{NodeID: fmt.Sprint(i), Hostname: fmt.Sprintf("machine-%d", i), Online: true, Harnesses: []protocol.HarnessInfo{h}})
		agents.Agents = append(agents.Agents, protocol.AgentView{SessionID: fmt.Sprint(i), NodeID: fmt.Sprint(i), Hostname: "machine", Profile: protocol.Profile{Name: "builder", Task: "work", Expertise: strings.Repeat("long", 1000)}})
	}
	summary := compactCTONodes(nodes, agents.Agents).(map[string]any)["nodes"].([]ctoNode)
	if len(summary) != 50 || len(summary[0].Harnesses) != 1 || summary[0].Harnesses[0].ModelCount != 400 {
		t.Fatalf("nodes: %+v", summary)
	}
	sessions := compactCTOAgents(agents.Agents).(map[string]any)["agents"].([]ctoAgent)
	if len(sessions) != 50 || sessions[0].ID != "0" || sessions[0].Name != "builder" || sessions[0].Task != "work" {
		t.Fatalf("agents: %+v", sessions)
	}
	beforeNodes, _ := json.Marshal(nodes)
	beforeAgents, _ := json.Marshal(agents)
	t.Logf("before: nodes %d bytes, agents %d bytes", len(beforeNodes), len(beforeAgents))
	for name, v := range map[string]any{"nodes": compactCTONodes(nodes, agents.Agents), "agents": compactCTOAgents(agents.Agents)} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s compact: %d bytes", name, len(b))
		if len(b) >= 20000 {
			t.Errorf("%s: %d bytes", name, len(b))
		}
		if strings.Contains(string(b), "provider/model") || strings.Contains(string(b), "longlong") {
			t.Errorf("%s includes long fields", name)
		}
	}
}

func TestCTOPages(t *testing.T) {
	items := []string{"a", "b", "c", "d"}
	page, next := ctoPage(items, 2, "", func(v string) string { return v })
	if strings.Join(page, ",") != "d,c" || next != "c" {
		t.Fatalf("first: %v %q", page, next)
	}
	page, next = ctoPage(items, 2, next, func(v string) string { return v })
	if strings.Join(page, ",") != "b,a" || next != "" {
		t.Fatalf("second: %v %q", page, next)
	}
}

func TestCTODefaultPagesStaySmall(t *testing.T) {
	var projects []protocol.ProjectView
	var workers []protocol.WorkerView
	var events []protocol.FeedEvent
	for i := 0; i < 50; i++ {
		projects = append(projects, protocol.ProjectView{Name: fmt.Sprintf("project-%d", i), Hostname: "box", Path: "/src/project"})
		workers = append(workers, protocol.WorkerView{SessionID: fmt.Sprintf("s-%d", i), Hostname: "box", Project: "project", Task: "work"})
		events = append(events, protocol.FeedEvent{Event: protocol.Event{ID: int64(i + 1), Kind: "session.new", Detail: "started"}})
	}
	ps, _ := ctoPage(projects, 0, "", func(v protocol.ProjectView) string { return v.Name })
	ws, _ := ctoPage(workers, 0, "", func(v protocol.WorkerView) string { return v.SessionID })
	for name, v := range map[string]any{"projects": ps, "workers": ws, "feed": events[:ctoLimit(0)]} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d bytes", name, len(b))
		if len(b) >= 20000 {
			t.Errorf("%s: %d bytes", name, len(b))
		}
	}
}

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTONeedsYouCompact(t *testing.T) {
	now := time.Unix(1700000200, 0)
	var agents []protocol.AgentView
	for i := 0; i < 50; i++ {
		agents = append(agents, protocol.AgentView{SessionID: fmt.Sprintf("s%d", i), Hostname: "box", Online: true, Profile: protocol.Profile{Name: "builder", Project: "project", Expertise: strings.Repeat("secret", 1000)}, Waiting: &protocol.Waiting{Kind: "question", Text: "é" + strings.Repeat("x", 220), Since: now.Unix() - 40}})
	}
	got := compactCTONeedsYou(agents, now)
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) >= 20000 {
		t.Fatalf("needs_you: %d bytes", len(b))
	}
	var result struct {
		Agents []struct {
			Kind      string `json:"kind"`
			SessionID string `json:"session_id"`
			Name      string `json:"name"`
			Project   string `json:"project"`
			Machine   string `json:"machine"`
			Text      string `json:"text"`
			Age       int64  `json:"age_seconds"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Agents) != 50 {
		t.Fatalf("got %d", len(result.Agents))
	}
	a := result.Agents[0]
	if a.Kind != "question" || a.SessionID != "s0" || a.Name != "builder" || a.Project != "project" || a.Machine != "box" || len([]rune(a.Text)) != 200 || a.Age != 40 {
		t.Fatalf("first: %+v", a)
	}
	if strings.Contains(string(b), "secret") {
		t.Fatal("long fields included")
	}
}

func TestCTONeedsYouThroughGateway(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	eventually(t, "connected", func() bool { return n.d.Connected() })
	st := g.gw.Store()
	for i := 0; i < 50; i++ {
		_, err := st.UpsertSession(n.d.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: fmt.Sprintf("wait-%d", i), Name: "builder", Status: "idle", Cwd: "/src/project", Repo: protocol.RepoInfo{Project: "project"}, Waiting: &protocol.Waiting{Kind: "question", Text: strings.Repeat("x", 200), Since: time.Now().Unix() - 10}})
		if err != nil {
			t.Fatal(err)
		}
	}
	v, err := n.d.runCTOToolRaw(context.Background(), "needs_you", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Agents []json.RawMessage `json:"agents"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Agents) != 50 || len(b) >= 20000 {
		t.Fatalf("%d agents, %d bytes", len(out.Agents), len(b))
	}
}

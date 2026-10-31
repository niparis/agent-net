package daemon

import (
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

package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOSessionPage(t *testing.T) {
	agents := make([]protocol.AgentView, 40)
	for i := range agents {
		agents[i] = protocol.AgentView{SessionID: fmt.Sprintf("s%02d", i), Profile: protocol.Profile{Task: strings.Repeat("x", 200)}}
	}
	first := ctoSessionPage(agents, 0, 0)
	if first.Total != 40 || len(first.Items) != 20 || first.Next != 20 {
		t.Fatalf("first: %+v", first)
	}
	second := ctoSessionPage(agents, 0, first.Next)
	if len(second.Items) != 20 || second.Next != 0 || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("second: %+v", second)
	}
	b, _ := json.Marshal(first)
	if len(b) >= 3000 {
		t.Fatalf("page: %d bytes", len(b))
	}
}

func TestCTOConversationRows(t *testing.T) {
	messages := []protocol.Message{{ID: "m1", Body: "private body", From: "a", To: "b"}, {ID: "m2", Body: "another"}}
	feed := ctoConversationRows(messages, true)
	raw, _ := json.Marshal(feed)
	if strings.Contains(string(raw), "private body") {
		t.Fatalf("feed leaked body: %s", raw)
	}
	rows := ctoConversationRows(messages, false)
	if len(rows) != 2 || rows[0].ID != "m1" {
		t.Fatalf("rows: %+v", rows)
	}
}

func TestCTOProjectRows(t *testing.T) {
	rows := ctoProjectRows([]protocol.ProjectView{{Name: "project", DisplayName: strings.Repeat("x", 100), NodeID: "node"}})
	if len(rows) != 1 || len([]rune(rows[0].Display)) > 80 {
		t.Fatalf("rows: %+v", rows)
	}
}

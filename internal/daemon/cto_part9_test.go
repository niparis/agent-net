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

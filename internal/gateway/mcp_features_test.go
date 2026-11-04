package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The lists of all servers together stay under the answer limit: servers past
// it are named with the reason, in order, and the earlier ones keep their items.
func TestCapServerItemsKeepsAnswerUnderLimit(t *testing.T) {
	items := func(n int) json.RawMessage { return json.RawMessage(`["` + strings.Repeat("x", n) + `"]`) }
	out := []protocol.MCPServerItems{
		{Server: "a", Items: items(400)},
		{Server: "b", Error: "host b offline"},
		{Server: "c", Items: items(400)},
		{Server: "d", Items: items(200)},
	}
	capServerItems(out, 1000)
	if len(out[0].Items) == 0 || out[0].Error != "" {
		t.Fatalf("a = %+v", out[0])
	}
	if out[1].Error != "host b offline" {
		t.Fatalf("b = %+v", out[1])
	}
	if len(out[2].Items) == 0 {
		t.Fatalf("c = %+v", out[2])
	}
	if len(out[3].Items) != 0 || !strings.Contains(out[3].Error, "larger than 1000 bytes") {
		t.Fatalf("d = %+v", out[3])
	}
}

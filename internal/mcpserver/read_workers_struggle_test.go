package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// detail full shows the run's struggle counts, and says "no trace" when the
// harness has no tool-call record. detail concise stays short.

func TestWorkersFindFullShowsStruggle(t *testing.T) {
	agent := protocol.AgentView{SessionID: "s_w", Harness: "pi", Worker: &protocol.WorkerInfo{ID: "w", Owner: "s_o",
		Project: "shop", Struggle: &protocol.Struggle{Trace: protocol.StruggleNoTrace}}}
	row, ok := roWorkerRowOf(agent, nil, true)
	if !ok {
		t.Fatal("worker row")
	}
	raw, _ := json.Marshal(row)
	if !strings.Contains(string(raw), `"trace":"no trace"`) {
		t.Fatalf("full detail has no no-trace note: %s", raw)
	}

	concise, _ := roWorkerRowOf(agent, nil, false)
	raw, _ = json.Marshal(concise)
	if strings.Contains(string(raw), "struggle") {
		t.Fatalf("concise detail shows struggle counts: %s", raw)
	}
}

func TestWorkersFindFullShowsCounts(t *testing.T) {
	agent := protocol.AgentView{SessionID: "s_w", Harness: "claude", Worker: &protocol.WorkerInfo{ID: "w", Owner: "s_o",
		Project: "shop", Struggle: &protocol.Struggle{Trace: "transcript", Failed: 4, Repeats: 1}}}
	row, _ := roWorkerRowOf(agent, nil, true)
	raw, _ := json.Marshal(row)
	for _, want := range []string{`"failed":4`, `"repeats":1`, `"trace":"transcript"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("full detail lacks %s: %s", want, raw)
		}
	}
}

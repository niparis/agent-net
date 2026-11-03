package gateway

import (
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestWorkerSignInCandidates(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	profiles := []protocol.WorkerProfile{{Name: "test", Harness: "codex", Model: "test-model"}}
	for _, state := range []string{"no", "yes", ""} {
		n := &protocol.NodeView{NodeID: "test-node", Hostname: "test-host", Harnesses: []protocol.HarnessInfo{{Name: "codex", SignedIn: state, Models: []string{"test-model"}}}}
		candidates, notes, _, err := g.workerModelCandidates(n, profiles, protocol.WorkerProviders{}, nil, "", false, protocol.ProtectedProviders{})
		if err != nil {
			t.Fatal(err)
		}
		if state == "no" {
			if len(candidates) != 0 || !strings.Contains(strings.Join(notes, "; "), "codex skipped on test-host: not signed in") {
				t.Fatalf("%v %v", candidates, notes)
			}
		} else if len(candidates) == 0 {
			t.Fatalf("state %q: no candidates", state)
		}
	}
}

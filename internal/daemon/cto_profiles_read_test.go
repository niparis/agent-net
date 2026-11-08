package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Worker profiles are public: the CTO reads them before it proposes a new one,
// so it can say whether one with that name or model already exists.
func TestCTOReadsWorkerProfiles(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	if err := g.gw.Store().AddWorkerProfile(protocol.WorkerProfile{Name: "terra", Harness: "codex", Model: "gpt-6.1-terra:high", AddedBy: w.ownerID()}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, args string }{{"agent_worker_profiles_find", `{}`}} {
		v, err := w.d.runCTOToolRaw(context.Background(), tc.name, json.RawMessage(tc.args))
		raw, _ := json.Marshal(v)
		if err != nil || !strings.Contains(string(raw), "gpt-6.1-terra:high") {
			t.Errorf("%s: %s, %v", tc.name, raw, err)
		}
		if !strings.Contains(string(raw), `"score":42.3`) || !strings.Contains(string(raw), `"score_source":"artificialanalysis"`) {
			t.Errorf("%s lost the profile score: %s", tc.name, raw)
		}
	}
}

package daemon

import (
	"context"
	"encoding/json"
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
	if err := g.gw.Store().AddWorkerProfile(protocol.WorkerProfile{Name: "unknown", Harness: "codex", Model: "unscored-model", AddedBy: w.ownerID()}); err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{`{}`, `{"detail":"concise"}`} {
		t.Run(args, func(t *testing.T) {
			v, err := w.d.runCTOToolRaw(context.Background(), "agent_worker_profiles_find", json.RawMessage(args))
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			var out struct {
				Items []map[string]any `json:"items"`
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatal(err)
			}
			rows := make(map[string]map[string]any)
			for _, row := range out.Items {
				rows[row["name"].(string)] = row
			}
			if row := rows["terra"]; row["score"] != 42.3 || row["score_source"] != "artificialanalysis" {
				t.Errorf("scored profile: %v", row)
			}
			row, ok := rows["unknown"]
			if !ok {
				t.Fatal("unscored profile missing")
			}
			if _, ok := row["score"]; ok {
				t.Errorf("unexpected score: %v", row)
			}
			if _, ok := row["score_source"]; ok {
				t.Errorf("unexpected score source: %v", row)
			}
		})
	}
}

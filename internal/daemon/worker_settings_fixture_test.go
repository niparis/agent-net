package daemon

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerSettingsBaseline(t *testing.T) {
	local := NodeConfig{Workers: &NodeWorkers{Max: 3, Idle: "12m", MaxAge: "3h", OwnerGrace: "4m", CPUTarget: 42, MemReserve: "2GB", QueueTimeout: "20m", StartGap: "0s"}}
	configured, err := local.WorkerSettings()
	if err != nil {
		t.Fatal(err)
	}
	auto := map[string]bool{}
	for _, enabled := range []bool{false, true} {
		for count := 0; count <= 3; count++ {
			w := &worker{autoContinue: enabled, autoContinueCount: count, sessionID: "session"}
			key, _ := json.Marshal([]any{enabled, count})
			auto[string(key)] = autoContinueEligible(w)
		}
	}
	out := map[string]any{"defaults": (WorkerSettings{}).filled(), "node_json": configured.filled(), "auto_continue": auto, "continue_text": protocol.WorkerAutoContinueText}
	got, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	const path = "testdata/worker-settings-baseline.json"
	if os.Getenv("UPDATE_WORKER_SETTINGS_FIXTURE") == "1" {
		if err := os.WriteFile(path, got, 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("worker settings differ from baseline fixture")
	}
}

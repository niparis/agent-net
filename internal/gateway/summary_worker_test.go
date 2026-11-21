package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The skill summary worker runs with the settings the owner has set, so its
// task carries the owner's worker instructions.
func TestSummaryWorkerBodyUsesSettingsSnapshot(t *testing.T) {
	s, _ := newTestStore(t)
	change := SettingsChange{Key: "delivery.worker_agy_commit_instruction", Level: "network", Scope: "network", Value: json.RawMessage(`"custom instruction"`)}
	if _, err := s.WriteSettings([]SettingsChange{change}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	body, err := (summaryWorker{g: &Gateway{store: s}}).taskBody("agy", "do work")
	if err != nil || !strings.Contains(body, "custom instruction") {
		t.Fatalf("body: %q %v", body, err)
	}
}

// blockingSummaryWorker holds a summary run until the test releases it, so the
// link stays busy while the gateway waits for the worker.
type blockingSummaryWorker struct {
	started chan struct{}
	release chan struct{}
}

func (f *blockingSummaryWorker) Run(ctx context.Context, _ protocol.WorkerStartRequest) (string, error) {
	close(f.started)
	select {
	case <-f.release:
		return "a and b differ in the install step", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

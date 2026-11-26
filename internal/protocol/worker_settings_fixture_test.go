package protocol

import (
	"bytes"
	"encoding/json"
	"github.com/niparis/agent-net/internal/settings"
	"os"
	"testing"
)

// TestWorkerSettingsBaseline records worker brief output before the settings cutover.
func TestWorkerSettingsBaseline(t *testing.T) {
	out := map[string]string{}
	for _, harness := range []string{"", "codex", "claude", "opencode", "pi", "agy"} {
		for _, level := range []string{"", WorkersSandboxed} {
			for _, worktree := range []string{"", "/tmp/worktree"} {
				out[harness+"/"+level+"/"+worktree] = WorkerTaskBodyBriefBar(harness, WorkerBrief{Notes: "do work"}, worktree, "agent-net/w_test", "", "project rules", "charter rules", level, "", settings.Snapshot{}, "", nil)
			}
		}
	}
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
		t.Fatal("worker brief output differs from baseline fixture")
	}
}

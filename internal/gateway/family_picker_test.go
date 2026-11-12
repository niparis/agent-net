package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestFamilyPickerExcludesCandidates(t *testing.T) {
	candidates := []protocol.WorkerModelCandidate{{Harness: "codex", Model: "gpt-6.1-sol"}, {Harness: "opencode", Model: "anthropic/claude-sonnet"}}
	kept, note, err := filterWorkerFamilies(candidates, []string{"openai"}, nil)
	if err != nil || len(kept) != 1 || kept[0].Model != candidates[1].Model || !strings.Contains(note, "gpt-6.1-sol") {
		t.Fatalf("kept=%v note=%q err=%v", kept, note, err)
	}
	choice, _, err := chooseProtectedWorker(kept, "normal", []protocol.ModelScore{{Match: "claude-sonnet", Score: 32.5}}, nil, nil, nil, nil, protocol.ProtectedProviders{})
	if err != nil || choice.Model != candidates[1].Model {
		t.Fatalf("choice=%v err=%v", choice, err)
	}
}

func TestFamilyPickerAllExcluded(t *testing.T) {
	candidates := []protocol.WorkerModelCandidate{{Harness: "codex", Model: "gpt-6.1-sol"}, {Harness: "opencode", Model: "anthropic/claude-sonnet"}}
	_, _, err := filterWorkerFamilies(candidates, []string{"openai", "anthropic"}, nil)
	if err == nil || !strings.Contains(err.Error(), "openai") || !strings.Contains(err.Error(), "anthropic") || !strings.Contains(err.Error(), "available") {
		t.Fatalf("error: %v", err)
	}
}

package protocol

import (
	"strings"
	"testing"
	"time"
)

func TestChooseWorkerModel(t *testing.T) {
	now := time.Unix(1000000, 0)
	scores := []ModelScore{{Match: "a", Score: 51}, {Match: "b", Score: 52}, {Match: "c", Score: 42}, {Match: "d", Score: 29}}
	candidates := []WorkerModelCandidate{{Harness: "pi", Model: "a"}, {Harness: "pi", Model: "b"}, {Harness: "pi", Model: "c"}, {Harness: "pi", Model: "d"}}
	q := &Quotas{GeneratedAt: now.Unix(), Providers: []QuotaProvider{{Provider: "a", Windows: []QuotaWindow{{Kind: "weekly", PercentUsed: 40, ResetsAt: now.Add(3 * 24 * time.Hour).Unix()}}}, {Provider: "b", Windows: []QuotaWindow{{Kind: "weekly", PercentUsed: 70, ResetsAt: now.Add(3 * 24 * time.Hour).Unix()}}}}}
	candidates[0].Provider = "a"
	candidates[1].Provider = "b"
	tests := []struct {
		name    string
		q       *Quotas
		running map[string]int
		failed  bool
		rotate  int
		want    string
	}{
		{"quota room", q, nil, false, 0, "a"}, {"workers consume room", q, map[string]int{"a": 7}, false, 0, "b"},
		{"round robin first", nil, nil, false, 0, "a"}, {"round robin second", nil, nil, false, 1, "b"}, {"failed last", nil, nil, true, 0, "b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := append([]WorkerModelCandidate(nil), candidates...)
			c[0].Failed = tt.failed
			got, err := ChooseWorkerModel(c, "hard", scores, nil, tt.q, tt.running, tt.rotate)
			if err != nil || got.Model != tt.want {
				t.Fatalf("got %+v: %v", got, err)
			}
		})
	}
	q.Providers[0].Windows[0].PercentUsed = 91
	got, err := ChooseWorkerModel(candidates, "hard", scores, nil, q, nil, 0)
	if err != nil || got.Model != "b" {
		t.Fatalf("high quota: %+v %v", got, err)
	}
	_, err = ChooseWorkerModel([]WorkerModelCandidate{{Model: "d"}}, "very_complex", scores, nil, nil, nil, 0)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("no eligible: %v", err)
	}
}

func TestChooseWorkerModelUsesHarnessDefaultEffort(t *testing.T) {
	candidates := []WorkerModelCandidate{{Harness: "codex", Model: "gpt-6.1-terra", DefaultEffort: "high"}}
	choice, err := ChooseWorkerModel(candidates, "hard", nil, nil, nil, nil, 0)
	if err != nil || choice.Score != 42.3 || choice.Fit != "good" {
		t.Fatalf("choice: %+v %v", choice, err)
	}
}

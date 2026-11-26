package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/model"
	"github.com/niparis/agent-net/internal/protocol"
)

// The ranker and the gate read one exhaustion test. A model the gate would
// refuse is not the ranker's choice, even when no other candidate is allowed.
func TestRankerAndGateAgreeOnAnExhaustedProvider(t *testing.T) {
	exhausted := protocol.WorkerModelCandidate{Name: "go", Harness: "opencode", Model: "opencode-go/deepseek-v4.1-flash"}
	fresh := protocol.WorkerModelCandidate{Name: "fresh", Harness: "opencode", Model: "fresh/unscored"}
	quotas := &protocol.Quotas{Providers: []protocol.QuotaProvider{{
		Provider: "opencode-go",
		Windows:  []protocol.QuotaWindow{{Kind: "monthly", Exhausted: true}},
	}}}
	in := &protocol.PickerInput{D: 2.5, Table: protocol.BuildPickerTable(nil, protocol.DefaultPickerSettings())}
	order, _, err := protocol.WorkerPickOrder([]protocol.WorkerModelCandidate{exhausted, fresh}, "normal",
		[]protocol.ModelScore{{Match: "opencode-go/deepseek-v4.1-flash", Score: 42.3}}, nil, quotas, nil, &protocol.WorkerRotation{}, in)
	if err == nil {
		t.Fatalf("the ranker chose %q, but the gate refuses it", order[0].Model)
	}

	facts := model.Facts{
		Now:         time.Unix(1_800_000_000, 0),
		Harnesses:   map[string]model.HarnessFact{"opencode": {Installed: true, SignedIn: "yes"}},
		Quotas:      quotas,
		AuthBlocked: map[string]bool{},
	}
	if _, _, gateErr := model.Gate([]model.Candidate{exhausted}, model.Checks{Block90: true}, facts); gateErr == nil {
		t.Fatal("the gate granted the exhausted model")
	}
}

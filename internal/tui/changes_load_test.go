package tui

import (
	"context"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// fakeDelivery is the fake source of the Sessions change loader.
type fakeDelivery struct {
	changes []deliveryChange
	err     error
	calls   int
	repo    string
}

func (f *fakeDelivery) Changes(_ context.Context, repo string) ([]deliveryChange, error) {
	f.calls++
	f.repo = repo
	return f.changes, f.err
}

// deliveryScenes returns changes in the states the Sessions screen reads.
func deliveryScenes() []deliveryChange {
	return []deliveryChange{
		{ID: "c_1", Repo: "agent-net", State: protocol.ChangeOpen, Step: "review", OwnerKind: protocol.OwnerNode, StepState: protocol.StepStateReady},
		{ID: "c_2", Repo: "agent-net", State: protocol.ChangeMerged, Step: "test", OwnerKind: protocol.OwnerCaller, StepState: protocol.StepStateReady},
		{ID: "c_3", Repo: "voicemechs", State: protocol.ChangeOpen, Steps: []deliveryStep{{Name: "test", State: protocol.StepStateRunning, Note: "claimed by s_0000000000000004"}}},
	}
}

func deliveryModel(src deliverySource, w, h int) Model {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.snap = fakeSnapshot()
	m.haveData = true
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.width, m.height = w, h
	m.tab = sessionsTab
	return m.withDeliverySource(src)
}

// loadDelivery runs the first load and returns the model with its changes.
func loadDelivery(t *testing.T, m Model) Model {
	t.Helper()
	cmd := m.deliveryLoad()
	if cmd == nil {
		t.Fatal("no load command")
	}
	next, _ := m.Update(cmd())
	return next.(Model)
}

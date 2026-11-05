package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestPickModelHealth(t *testing.T) {
	s, _ := newTestStore(t)
	now := time.Date(2026, 11, 5, 20, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	g := &Gateway{store: s}
	candidates := []protocol.WorkerModelCandidate{{Harness: "opencode", Model: "provider/a"}, {Harness: "opencode", Model: "provider/b"}}
	scores := []protocol.ModelScore{{Match: "provider/a", Score: 40}, {Match: "provider/b", Score: 40}}
	add := func(outcome string) {
		t.Helper()
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: "node", Harness: "opencode", Model: "provider/a", Outcome: outcome, EndedAt: now.Unix()}); err != nil {
			t.Fatal(err)
		}
	}
	outcome := protocol.WorkerOutcomeDetail(protocol.StopModelFails, protocol.StatusFailed, nil, "Timed out waiting for response headers")
	if outcome != protocol.OutcomeInfra {
		t.Fatalf("timeout classified %q", outcome)
	}
	add(outcome)
	add(outcome)
	pick := func(c []protocol.WorkerModelCandidate) (protocol.WorkerModelChoice, string) {
		t.Helper()
		p, n, e := g.chooseAutomaticWorker(c, "normal", scores, nil, nil, nil, protocol.ProtectedProviders{}, false)
		if e != nil {
			t.Fatal(e)
		}
		return p, n
	}
	p, n := pick(candidates)
	if p.Model != "provider/b" || !strings.Contains(n, "cooling down") {
		t.Fatalf("pick %s note %q", p.Model, n)
	}
	p, n = pick(candidates[:1])
	if p.Model != "provider/a" || !strings.Contains(n, "cooling down") {
		t.Fatalf("fallback %s note %q", p.Model, n)
	}
	health, err := s.ModelHealth("opencode", "provider/a")
	if err != nil || health.Failures != 2 {
		t.Fatalf("health %+v %v", health, err)
	}
	now = now.Add(16 * time.Minute)
	health, err = s.ModelHealth("opencode", "provider/a")
	if err != nil || health.Cooling {
		t.Fatalf("expired %+v %v", health, err)
	}
	p, _ = pick(candidates[:1])
	if p.Model != "provider/a" {
		t.Fatalf("recovered pick: %s", p.Model)
	}
	add(protocol.OutcomeInfra)
	add(protocol.OutcomeComplete)
	health, err = s.ModelHealth("opencode", "provider/a")
	if err != nil || health.Cooling || health.Failures != 0 {
		t.Fatalf("success %+v %v", health, err)
	}
	add(protocol.OutcomeFailed)
	health, err = s.ModelHealth("opencode", "provider/a")
	if err != nil || health.Failures != 0 {
		t.Fatalf("bad work %+v %v", health, err)
	}
	tr, ok, err := s.TrackRecord("node", "opencode", "provider/a", 0)
	if err != nil || !ok || tr.Failed != 1 || tr.Infra != 3 {
		t.Fatalf("track %+v %v", tr, err)
	}
}

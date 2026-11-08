package gateway

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// setOwnerScores writes the owner overrides the same way handleScoresSet does.
func setOwnerScores(t *testing.T, s *Store, scores []protocol.ModelScore) {
	t.Helper()
	raw, err := json.Marshal(scores)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO owner_settings (key,value,updated_at) VALUES (?,?,?) ON CONFLICT (key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`,
		scoresKey, string(raw), s.now().UnixNano()); err != nil {
		t.Fatal(err)
	}
}

// A profile row carries the score of its resolved model and effort. An owner
// override replaces the embedded score for that model.
func TestWorkerProfileListIncludesModelScore(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeHello(node, "host", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetNodeHarnesses(node, []protocol.HarnessInfo{{Name: "claude", SignedIn: "yes", Models: []string{"claude-haiku-5-5"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddWorkerProfile(protocol.WorkerProfile{Name: "haiku high", Harness: "claude", Model: "claude-haiku-5-5", Effort: "high"}); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s, conns: map[string]*nodeConn{node: {id: node}}}

	list := func() protocol.WorkerProfile {
		t.Helper()
		v, err := g.workerProfileAction(node, "worker.profile.list", protocol.WorkerProfileFrame{})
		if err != nil {
			t.Fatal(err)
		}
		return v.([]protocol.WorkerProfile)[0]
	}

	embedded := list()
	if embedded.Score == nil || *embedded.Score != 29.6 || embedded.ScoreSource != "artificialanalysis" {
		t.Fatalf("embedded score: %+v", embedded)
	}

	setOwnerScores(t, s, []protocol.ModelScore{{Match: "claude-haiku-5-5", Score: 55, Source: "owner"}})
	overridden := list()
	if overridden.Score == nil || *overridden.Score != 55 || overridden.ScoreSource != "owner" {
		t.Fatalf("owner override did not win: %+v", overridden)
	}
}

// A model row lists every score effort that matches it.
func TestMachineGetListsModelScores(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeHello(node, "host", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetNodeHarnesses(node, []protocol.HarnessInfo{{Name: "claude", Models: []string{"claude-haiku-5-5", "unscored-model"}}}); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s, conns: map[string]*nodeConn{node: {id: node}}}

	out, err := g.MachineGet(context.Background(), protocol.MachineGetRequest{Machine: "host", Include: []string{"models"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Models == nil {
		t.Fatal("no models page")
	}
	var haiku *protocol.MachineModelRow
	for i := range out.Models.Items {
		if out.Models.Items[i].ID == "claude-haiku-5-5" {
			haiku = &out.Models.Items[i]
		}
		if out.Models.Items[i].ID == "unscored-model" && len(out.Models.Items[i].Scores) != 0 {
			t.Fatalf("unscored model got scores: %+v", out.Models.Items[i])
		}
	}
	if haiku == nil {
		t.Fatalf("haiku row missing: %+v", out.Models.Items)
	}
	want := []protocol.ModelEffortScore{
		{Effort: "low", Score: 18.6, Source: "artificialanalysis"},
		{Effort: "medium", Score: 24.6, Source: "artificialanalysis"},
		{Effort: "high", Score: 29.6, Source: "artificialanalysis"},
		{Effort: "xhigh", Score: 34.3, Source: "artificialanalysis"},
		{Effort: "max", Score: 36.2, Source: "artificialanalysis"},
	}
	if !reflect.DeepEqual(haiku.Scores, want) {
		t.Fatalf("haiku scores = %+v, want %+v", haiku.Scores, want)
	}
}

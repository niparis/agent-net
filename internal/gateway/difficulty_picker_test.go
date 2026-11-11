package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestDifficultyJudgePickerRotationAndProtection(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test-machine", "linux")
	if err := s.SetNodeHello(node, "test-machine", "test"); err != nil {
		t.Fatal(err)
	}
	_, err := s.SetNodeHarnesses(node, []protocol.HarnessInfo{{Name: "claude", SignedIn: "yes", Models: []string{"claude-test"}}, {Name: "pi", SignedIn: "yes", Models: []string{"google/gemini-test"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []protocol.WorkerProfile{{Name: "claude", Harness: "claude", Model: "claude-test"}, {Name: "gemini", Harness: "pi", Model: "google/gemini-test"}} {
		if err := s.AddWorkerProfile(p); err != nil {
			t.Fatal(err)
		}
	}
	scores := `[{"match":"claude-test","score":32},{"match":"gemini-test","score":32}]`
	if _, err := s.db.Exec(`INSERT INTO owner_settings (key,value,updated_at) VALUES (?,?,0)`, scoresKey, scores); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO owner_settings (key,value,updated_at) VALUES (?,?,0)`, workerProvidersKey, `{"providers":["anthropic","google"]}`); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s, logger: log.New(io.Discard, "", 0), conns: map[string]*nodeConn{node: {id: node}}}
	pick := func(exclude string) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"exclude_provider": exclude})
		v, err := g.handleWorker(context.Background(), &nodeConn{id: node}, "worker.judge.pick", raw)
		if err != nil {
			t.Fatalf("pick judge: %v", err)
		}
		b, _ := json.Marshal(v)
		var out map[string]any
		_ = json.Unmarshal(b, &out)
		return out
	}
	first, second := pick(""), pick("")
	if first["model"] == second["model"] || first["model"] == "" || second["model"] == "" {
		t.Fatalf("rotation: %v then %v", first, second)
	}
	backup := pick(first["provider"].(string))
	if backup["provider"] == first["provider"] || backup["model"] == "" {
		t.Fatalf("backup %v has first provider %v", backup, first)
	}
	if _, err := s.db.Exec(`INSERT INTO owner_settings (key,value,updated_at) VALUES (?,?,0)`, protectedProvidersKey, `{"protected_providers":["anthropic"]}`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if got := pick(""); got["harness"] != "pi" {
			t.Fatalf("protected provider chosen: %v", got)
		}
	}
	if reflect.DeepEqual(first, second) {
		t.Fatal("rotation returned the same pick")
	}
}

func TestDifficultyJudgePickerNoCandidates(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test-machine", "linux")
	if err := s.SetNodeHello(node, "test-machine", "test"); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s, logger: log.New(io.Discard, "", 0), conns: map[string]*nodeConn{node: {id: node}}}
	v, err := g.handleWorker(context.Background(), &nodeConn{id: node}, "worker.judge.pick", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	if result["machine"] != "test-machine" || result["model"] != nil && result["model"] != "" {
		t.Fatalf("no candidates: %s", raw)
	}
}

func TestDifficultyJudgePickerChecksMachineAvailabilityAndLevel(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test-machine", "linux")
	if err := s.SetNodeHello(node, "test-machine", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetNodeHarnesses(node, []protocol.HarnessInfo{
		{Name: "claude", SignedIn: "no", Models: []string{"claude-test"}},
		{Name: "agy", SignedIn: "yes", Models: []string{"gemini-other"}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []protocol.WorkerProfile{{Name: "signed-out", Harness: "claude", Model: "claude-test"}, {Name: "stale", Harness: "agy", Model: "gemini-test"}} {
		if err := s.AddWorkerProfile(p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO owner_settings (key,value,updated_at) VALUES (?,?,0)`, scoresKey, `[{"match":"claude-test","score":32},{"match":"gemini-test","score":32}]`); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s, logger: log.New(io.Discard, "", 0), conns: map[string]*nodeConn{node: {id: node}}}
	pick := func() protocol.DifficultyJudgeChoice {
		t.Helper()
		v, err := g.handleWorker(context.Background(), &nodeConn{id: node}, "worker.judge.pick", []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		return v.(protocol.DifficultyJudgeChoice)
	}
	// A signed-out harness is skipped. A model the stored list lacks is not: the
	// start tests it, so the picker still offers that profile.
	if got := pick(); got.Model != "gemini-test" || got.Harness != "agy" {
		t.Fatalf("signed-out harness or live-tested model picked wrongly: %+v", got)
	}
	if _, err := s.SetNodeHarnesses(node, []protocol.HarnessInfo{{Name: "agy", SignedIn: "yes", Models: []string{"gemini-test"}}}); err != nil {
		t.Fatal(err)
	}
	if got := pick(); got.Model != "gemini-test" {
		t.Fatalf("available model not selected: %+v", got)
	}
	// Decision 0105 replaced the max-or-one-level-below band with the cell rule.
	// A strong model that is the only one available is used for normal work.
	if _, err := s.db.Exec(`UPDATE owner_settings SET value=? WHERE key=?`, `[{"match":"claude-test","score":32},{"match":"gemini-test","score":52}]`, scoresKey); err != nil {
		t.Fatal(err)
	}
	if got := pick(); got.Model != "gemini-test" {
		t.Fatalf("a lone strong model must still be used: %+v", got)
	}
	// With a weaker model signed in and in quota, neither has a record and both
	// are allowed by their benchmark. The strong one is not left out for being
	// above the work, so the rotation uses both.
	if _, err := s.SetNodeHarnesses(node, []protocol.HarnessInfo{
		{Name: "claude", SignedIn: "yes", Models: []string{"claude-test"}},
		{Name: "agy", SignedIn: "yes", Models: []string{"gemini-test"}},
	}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for i := 0; i < 8; i++ {
		seen[pick().Model]++
	}
	if seen["claude-test"] == 0 || seen["gemini-test"] == 0 || len(seen) != 2 {
		t.Fatalf("want both untried models in the rotation, got %v", seen)
	}
}

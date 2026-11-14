package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// harnessChoice is the harness workerHarnessOrder picks first, as the start
// uses it, and the error when no harness lists the model.
func harnessChoice(g *Gateway, node *protocol.NodeView, model string) (string, error) {
	order, _, err := g.workerHarnessOrder(node, model, false, nil)
	if err != nil {
		return "", err
	}
	if len(order) == 0 {
		return "", fmt.Errorf("no harness on %s lists %q", node.Hostname, model)
	}
	return order[0].Harness, nil
}

func TestWorkerModelHarnessSignIn(t *testing.T) {
	for _, tc := range []struct{ oc, codex, want string }{{"", "yes", "codex"}, {"no", "", "codex"}, {"", "", "codex"}, {"yes", "yes", "codex"}, {"yes", "", "opencode"}, {"no", "no", ""}} {
		t.Run(tc.oc+"_"+tc.codex, func(t *testing.T) {
			s, _ := newTestStore(t)
			g := &Gateway{store: s, logger: log.New(io.Discard, "", 0)}
			n := &protocol.NodeView{NodeID: "test", Hostname: "host", Harnesses: []protocol.HarnessInfo{{Name: "opencode", SignedIn: tc.oc, Models: []string{"openai/gpt-6.1-sol"}}, {Name: "codex", SignedIn: tc.codex, Models: []string{"gpt-6.1-sol"}}}}
			got, err := harnessChoice(g, n, "gpt-6.1-sol:medium")
			if got != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestWorkerModelProfileName(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s, logger: log.New(io.Discard, "", 0)}
	if err := s.AddWorkerProfile(protocol.WorkerProfile{Name: "Sol Codex", Harness: "codex", Model: "gpt-6.1-sol", Effort: "medium"}); err != nil {
		t.Fatal(err)
	}
	n := &protocol.NodeView{NodeID: "test", Hostname: "host", Harnesses: []protocol.HarnessInfo{{Name: "opencode", Models: []string{"openai/gpt-6.1-sol"}}, {Name: "codex", Models: []string{"gpt-6.1-sol"}}}}
	h, err := harnessChoice(g, n, "sOL cODEX")
	if err != nil || h != "codex" {
		t.Fatalf("harness %q: %v", h, err)
	}
	got, note, err := g.resolveWorkerModel(n, &n.Harnesses[1], h, "sOL cODEX", "", nil)
	if err != nil || got != "gpt-6.1-sol:medium" || !strings.Contains(note, "Sol Codex") {
		t.Fatalf("model %q note %q: %v", got, note, err)
	}
}

func TestWorkerModelAuthFailure(t *testing.T) {
	for _, failure := range []string{"Request failed: 401", "HTTP 403 forbidden", "not logged in"} {
		t.Run(failure, func(t *testing.T) {
			s, clock := newTestStore(t)
			g := &Gateway{store: s, logger: log.New(io.Discard, "", 0)}
			node, _, _ := s.CreateNode("host", "linux")
			sid, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "worker", Model: "openai/gpt-6.1-sol", Worker: &protocol.WorkerInfo{ID: "w_test"}})
			if err != nil {
				t.Fatal(err)
			}
			n := &protocol.NodeView{NodeID: node, Hostname: "host", Harnesses: []protocol.HarnessInfo{{Name: "opencode", SignedIn: "yes", Models: []string{"openai/gpt-6.1-sol", "openai/gpt-6-luna", "other/model"}}, {Name: "codex", Models: []string{"gpt-6.1-sol", "gpt-6-luna"}}}}
			raw, _ := json.Marshal(map[string]any{"session_id": sid, "harness": "opencode", "model": "openai/gpt-6.1-sol", "reason": protocol.StopModelFails, "failure": failure})
			if _, err := g.handleWorker(context.Background(), &nodeConn{id: node}, "worker.stopped", raw); err != nil {
				t.Fatal(err)
			}
			clock.t = clock.t.Add(48 * time.Hour)
			only := *n
			only.Harnesses = n.Harnesses[:1]
			if got, err := harnessChoice(g, &only, "gpt-6.1-sol"); err == nil {
				t.Errorf("auth-failed harness selected: %q", got)
			}
			for _, model := range []string{"gpt-6.1-sol", "gpt-6-luna"} {
				got, err := harnessChoice(g, n, model)
				if err != nil || got != "codex" {
					t.Errorf("%s chose %q: %v", model, got, err)
				}
			}
			if got, err := harnessChoice(g, n, "other/model"); err != nil || got != "opencode" {
				t.Fatalf("other provider: %q %v", got, err)
			}
			if err := s.RecordModelSuccess(node, "opencode", "openai/gpt-6-luna"); err != nil {
				t.Fatal(err)
			}
			if got, err := harnessChoice(g, n, "gpt-6.1-sol"); err != nil || got != "opencode" {
				t.Fatalf("after success: %q %v", got, err)
			}
		})
	}
}

func TestWorkerProfileListOnlineAvailability(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("host", "linux")
	if err := s.SetNodeHello(node, "host", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetNodeHarnesses(node, []protocol.HarnessInfo{{Name: "codex", SignedIn: "yes", Models: []string{"gpt-6.1-sol"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddWorkerProfile(protocol.WorkerProfile{Name: "sol", Harness: "codex", Model: "gpt-6.1-sol"}); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s, conns: map[string]*nodeConn{node: {id: node}}}
	v, err := g.workerProfileAction(node, "worker.profile.list", protocol.WorkerProfileFrame{})
	if err != nil {
		t.Fatal(err)
	}
	p := v.([]protocol.WorkerProfile)[0]
	if p.Available == nil || !*p.Available || len(p.MachineAvailability) != 1 || !p.MachineAvailability[0].Available {
		t.Fatalf("availability: %+v", p)
	}
}

func TestWorkerModelAuthProviderScope(t *testing.T) {
	s, clock := newTestStore(t)
	check := func(node, harness, model string, want bool) {
		t.Helper()
		got, err := s.ModelAuthBlocked(node, harness, model)
		if err != nil || got != want {
			t.Fatalf("%s/%s/%s: %v, %v; want %v", node, harness, model, got, err, want)
		}
	}
	if err := s.RecordModelFailure("node", "opencode", "openai/sol:medium", "401"); err != nil {
		t.Fatal(err)
	}
	check("node", "opencode", "openai/luna", true)
	check("other", "opencode", "openai/luna", false)
	check("node", "codex", "gpt-6.1-sol", false)
	check("node", "opencode", "openai-codex/sol", false)
	clock.t = clock.t.Add(time.Second)
	if err := s.RecordModelSuccess("node", "opencode", "openai/luna:high"); err != nil {
		t.Fatal(err)
	}
	check("node", "opencode", "openai/sol", false)
	clock.t = clock.t.Add(time.Second)
	if err := s.RecordModelFailure("node", "opencode", "openai/sol:medium", "429 rate limit"); err != nil {
		t.Fatal(err)
	}
	check("node", "opencode", "openai/sol", false)
	clock.t = clock.t.Add(time.Second)
	if err := s.RecordModelFailure("node", "opencode", "openai/sol:medium", "403"); err != nil {
		t.Fatal(err)
	}
	check("node", "opencode", "openai/luna", true)
}

func TestWorkerModelProfileEffortOverride(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s, logger: log.New(io.Discard, "", 0)}
	if err := s.AddWorkerProfile(protocol.WorkerProfile{Name: "Sol Codex", Harness: "codex", Model: "gpt-6.1-sol", Effort: "medium"}); err != nil {
		t.Fatal(err)
	}
	n := &protocol.NodeView{NodeID: "test", Hostname: "host", Harnesses: []protocol.HarnessInfo{{Name: "codex", Models: []string{"gpt-6.1-sol"}}}}
	got, _, err := g.resolveWorkerModel(n, &n.Harnesses[0], "codex", "Sol Codex:high", "", nil)
	if err != nil || got != "gpt-6.1-sol:high" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestWorkerAgyEffortInventory(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s, logger: log.New(io.Discard, "", 0)}
	n := &protocol.NodeView{NodeID: "test", Hostname: "host"}
	hi := &protocol.HarnessInfo{Name: "agy", Models: []string{"gemini-4-argon-low", "gemini-4-argon-high"}}
	for _, query := range []string{"argon:high", "gemini-4-argon-high"} {
		got, _, err := g.resolveWorkerModel(n, hi, "agy", query, "", nil)
		if err != nil || got != "gemini-4-argon-high" {
			t.Fatalf("%s got %q, %v", query, got, err)
		}
	}
	_, _, err := g.resolveWorkerModel(n, hi, "agy", "gemini-4-argon:xhigh", "", nil)
	if err == nil || !strings.Contains(err.Error(), "high, low") {
		t.Fatalf("got %v", err)
	}
}

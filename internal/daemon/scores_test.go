package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestScoresOwnerGate(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi", exe: "/opt/pi"})
	pi, _ := helloPi(t, n, "p1", "/w/p", false)
	score := 42.0
	if _, err := pi.call(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "model", Score: &score}); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
		t.Fatalf("gate: %v", err)
	}
	cli := cliClient(t, n)
	cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "model", Score: &score})
	var out []protocol.ModelScore
	raw := cli.mustCall(protocol.OpScoresGet, nil)
	if err := json.Unmarshal(raw, &out); err != nil || len(out) != 1 || out[0].Score != score {
		t.Fatalf("scores: %+v %v", out, err)
	}
}

func TestDifficultyOwnerGate(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi", exe: "/opt/pi"})
	pi, _ := helloPi(t, n, "p1", "/w/p", false)
	if _, err := pi.call("difficulty_set", map[string]float64{"hard": 45}); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
		t.Fatalf("gate: %v", err)
	}
	cli := cliClient(t, n)
	cli.mustCall("difficulty_set", map[string]float64{"hard": 45})
	raw := cli.mustCall("difficulty_get", nil)
	var out map[string]float64
	if err := json.Unmarshal(raw, &out); err != nil || out["hard"] != 45 {
		t.Fatalf("got %s: %v", raw, err)
	}
}

func TestWorkerProvidersOwnerGate(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi", exe: "/opt/pi"})
	pi, _ := helloPi(t, n, "p1", "/w/p", false)
	q := protocol.WorkerProviders{Providers: []string{"openrouter"}}
	if _, err := pi.call(protocol.OpWorkerProvidersSet, q); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
		t.Fatalf("gate: %v", err)
	}
	cli := cliClient(t, n)
	cli.mustCall(protocol.OpWorkerProvidersSet, q)
	raw := cli.mustCall(protocol.OpWorkerProvidersGet, nil)
	var out protocol.WorkerProviders
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Providers) != 1 || out.Providers[0] != "openrouter" {
		t.Fatalf("got %s %v", raw, err)
	}
}

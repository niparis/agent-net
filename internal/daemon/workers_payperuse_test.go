package daemon

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// startModelNode starts a worker node whose OpenCode and Codex harnesses list
// the given models. Codex can be used without a model list by passing nil.
func startModelNode(t *testing.T, g *testGateway, codexModels, opencodeModels []string) *workerNode {
	t.Helper()
	t.Setenv(envFakeCodex, "1")
	t.Setenv("CODEX_HOME", t.TempDir())
	return startWorkerNodeWithDetect(t, g, shortSet, func(ctx context.Context) []protocol.HarnessInfo {
		list := fakeInventory(ctx)
		for i := range list {
			switch list[i].Name {
			case "codex":
				list[i].Models = codexModels
			case "opencode":
				list[i].Models = opencodeModels
			}
		}
		return list
	}, func(c *Config) { c.HarnessPaths = map[string]string{"codex": os.Args[0]} })
}

// A model name without a harness that matches on several harnesses picks the
// subscription-backed one. The real case: gpt-6.1-sol:medium must run on the
// Codex subscription, not on OpenCode Zen.
func TestWorkerExplicitModelPrefersSubscriptionHarness(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startModelNode(t, g, []string{"gpt-6.1-sol"}, []string{"opencode/gpt-6.1-sol"})
	w.register(t, newRepo(t, "shop"))

	res, err := w.start(protocol.WorkerStartRequest{Project: "shop", Model: "gpt-6.1-sol:medium", Task: "say ok"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Harness != "codex" || res.Model != "gpt-6.1-sol:medium" {
		t.Fatalf("started %s %s, want codex gpt-6.1-sol:medium", res.Harness, res.Model)
	}
	if strings.Contains(res.ModelNote, "pay-per-use") {
		t.Errorf("the note calls a subscription provider pay-per-use: %q", res.ModelNote)
	}
}

// A bare name that only matches a pay-per-use provider is refused before a
// worker starts, and the message names the full id to use.
func TestWorkerBarePayPerUseModelRefused(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startModelNode(t, g, nil, []string{"opencode/gpt-6.1-sol"})
	w.register(t, newRepo(t, "shop"))

	_, err := w.start(protocol.WorkerStartRequest{Project: "shop", Model: "gpt-6.1-sol", Task: "x"})
	if err == nil {
		t.Fatal("the bare name on a pay-per-use provider was accepted")
	}
	for _, want := range []string{"pay-per-use", "opencode/gpt-6.1-sol"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q lacks %q", err, want)
		}
	}
	if n := w.d.workerCount(); n != 0 {
		t.Errorf("%d worker(s) left after the refusal", n)
	}
}

// The full provider id is accepted, and the start result says that it is
// pay-per-use.
func TestWorkerFullPayPerUseModelAccepted(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startModelNode(t, g, nil, []string{"opencode/gpt-6.1-sol"})
	w.register(t, newRepo(t, "shop"))

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Model: "opencode/gpt-6.1-sol", Task: "say ok"})
	if res.Model != "opencode/gpt-6.1-sol" || !strings.Contains(res.ModelNote, "pay-per-use provider") {
		t.Fatalf("full id: model %q, note %q", res.Model, res.ModelNote)
	}
}

// A loose name that matches both OpenCode Zen and opencode-go picks the
// subscription provider.
func TestWorkerLooseModelPrefersSubscriptionProvider(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startModelNode(t, g, nil, []string{"opencode/gpt-6.1-sol", "opencode-go/gpt-6.1-sol"})
	w.register(t, newRepo(t, "shop"))

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Model: "gpt-6.1-sol", Task: "say ok"})
	if res.Model != "opencode-go/gpt-6.1-sol" {
		t.Fatalf("resolved to %q, want opencode-go/gpt-6.1-sol", res.Model)
	}
	if strings.Contains(res.ModelNote, "pay-per-use") {
		t.Errorf("the note calls a subscription provider pay-per-use: %q", res.ModelNote)
	}
}

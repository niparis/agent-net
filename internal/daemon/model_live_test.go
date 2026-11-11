package daemon

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestListModelsLiveCachesForFiveMinutes(t *testing.T) {
	var calls atomic.Int32
	d := &Daemon{cfg: Config{ListModels: func(context.Context, string) ([]string, error) {
		calls.Add(1)
		return []string{"alibaba-token-plan/deepseek-v4.1-flash-1017", "openai/gpt-6"}, nil
	}}}
	first := d.listModelsLive(context.Background(), "opencode", "alibaba")
	if len(first.Harnesses) != 1 || len(first.Harnesses[0].Models) != 1 || first.Harnesses[0].Cached {
		t.Fatalf("first live read: %+v", first)
	}
	second := d.listModelsLive(context.Background(), "opencode", "alibaba")
	if len(second.Harnesses) != 1 || !second.Harnesses[0].Cached {
		t.Fatalf("second read was not served from cache: %+v", second)
	}
	if calls.Load() != 1 {
		t.Fatalf("harness listed %d times, want 1", calls.Load())
	}
}

// TestStartUsesLiveListAndExplainsFailure covers both halves of the live rule: a
// model the stored list lacks still launches, and a harness failure carries its
// own error plus one live lookup of the model's base name.
func TestStartUsesLiveListAndExplainsFailure(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNodeWithDetect(t, g, shortSet,
		inventoryWithModels("opencode", []string{"fake/m1"}),
		func(c *Config) {
			c.ListModels = func(context.Context, string) ([]string, error) {
				return []string{"fake/m1", "fake/m2"}, nil
			}
		})
	repo := newRepo(t, "shop")
	w.register(t, repo)

	// Not refused before launch even though no stored list has fake/m2.
	t.Setenv("FAKE_OC_EXIT", "1")
	_, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m2", Task: "x"})
	if err == nil {
		t.Fatal("the fake harness failure did not reach the caller")
	}
	if !strings.Contains(err.Error(), "live opencode model lookup") || !strings.Contains(err.Error(), "fake/m2") {
		t.Fatalf("failure does not explain the live lookup: %v", err)
	}
	os.Unsetenv("FAKE_OC_EXIT")
}

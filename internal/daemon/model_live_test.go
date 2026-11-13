package daemon

import (
	"context"
	"os"
	"path/filepath"
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

// A failed live read keeps the harness's own words for the owner, and the
// failure is not cached: the next call runs the harness again, so a login the
// owner just finished is seen at once. The fake harness is a small script, so
// the real opencode is never run and no model call is made. No sleep is needed:
// the two reads happen in the test's own order.
func TestStartExplainsHarnessModelErrorAndDoesNotCache(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "models-read")
	t.Setenv("FAKE_MODELS_STATE", stateFile)
	bin := filepath.Join(dir, "opencode")
	// The script uses only shell builtins: the harness runs with a narrow PATH,
	// so commands such as cat and sed are not there.
	script := `#!/bin/sh
if [ "$1" = models ]; then
	n=0
	[ -f "$FAKE_MODELS_STATE" ] && read n < "$FAKE_MODELS_STATE"
	echo $((n+1)) > "$FAKE_MODELS_STATE"
	if [ "$n" = 0 ]; then
		echo 'please run opencode auth login' >&2
		exit 1
	fi
	echo 'fake/m1'
	exit 0
fi
exit 3
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNodeWithDetect(t, g, shortSet,
		inventoryWithModels("opencode", []string{"fake/m1"}),
		func(c *Config) {
			// The daemon reads the harness itself here, so ListModels and its
			// error plumbing are exercised, not a fake ListModels.
			c.ListModels = nil
			c.HarnessPaths = map[string]string{"opencode": bin}
		})
	repo := newRepo(t, "shop")
	w.register(t, repo)

	_, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m2", Task: "x"})
	if err == nil {
		t.Fatal("the fake harness failure did not reach the caller")
	}
	if !strings.Contains(err.Error(), "please run opencode auth login") {
		t.Fatalf("failure does not carry the harness's own error: %v", err)
	}

	// A failed read must not be cached: this one runs the harness again and
	// sees the list it now prints.
	next := w.d.listModelsLive(context.Background(), "opencode", "fake/m1")
	if len(next.Harnesses) != 1 || next.Harnesses[0].Cached || next.Harnesses[0].Error != "" || len(next.Harnesses[0].Models) != 1 {
		t.Fatalf("the failed read was cached: %+v", next)
	}
	raw, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(raw)); got != "2" {
		t.Fatalf("harness read %q times, want 2", got)
	}
}

// testModel must hand an effort to opencode on the model as model#variant,
// because 2.3.0 has no --variant flag. A tiny fake harness prints its argv, so
// the real opencode is never run and no model call is made.
func TestModelTestOpencodeEffortOnModel(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "opencode")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{HarnessPaths: map[string]string{"opencode": bin}}}
	const model = "alibaba-token-plan/deepseek-v4.1-flash-1017"
	res := d.testModel(context.Background(), protocol.ModelTestRequest{Harness: "opencode", Model: model, Effort: "high"})
	if !res.OK {
		t.Fatalf("the fake harness did not run: %s", res.Output)
	}
	lines := strings.Split(res.Output, "\n")
	has := func(want string) bool {
		for _, line := range lines {
			if line == want {
				return true
			}
		}
		return false
	}
	for _, line := range lines {
		if strings.Contains(line, ":high") {
			t.Fatalf("the model id carries the effort, which opencode does not take: %q", line)
		}
	}
	if !has("--model") || !has(model+"#high") {
		t.Fatalf("argv does not carry the effort on the model: %q", res.Output)
	}
	if has("--variant") {
		t.Fatalf("argv passes a flag this opencode does not have: %q", res.Output)
	}
}

// An older opencode still takes --variant; its own help is the evidence.
func TestModelTestOpencodeEffortKeepsVariantOnOlderBuild(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "opencode")
	script := "#!/bin/sh\nif [ \"$1\" = run ] && [ \"$2\" = --help ]; then echo '--variant string'; exit 0; fi\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{HarnessPaths: map[string]string{"opencode": bin}}}
	const model = "alibaba-token-plan/deepseek-v4.1-flash-1017"
	res := d.testModel(context.Background(), protocol.ModelTestRequest{Harness: "opencode", Model: model, Effort: "high"})
	if !res.OK {
		t.Fatalf("the fake harness did not run: %s", res.Output)
	}
	lines := strings.Split(res.Output, "\n")
	has := func(want string) bool {
		for _, line := range lines {
			if line == want {
				return true
			}
		}
		return false
	}
	if !has("--variant") || !has("high") || !has(model) {
		t.Fatalf("older build did not get the variant flag: %q", res.Output)
	}
}

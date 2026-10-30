package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestProjectWorkerRulesBoundedAndClean(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".agent-net"), 0755); err != nil {
		t.Fatal(err)
	}
	data := "rule\x00\x1b\n" + strings.Repeat("x", 9000)
	if err := os.WriteFile(filepath.Join(repo, ".agent-net", "worker.md"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	rules := projectWorkerRules(repo)
	if len(rules) > 8192 || strings.ContainsAny(rules, "\x00\x1b") || !strings.HasPrefix(rules, "rule\n") {
		t.Fatalf("unclean or unbounded rules: %q", rules[:10])
	}
}

func TestWorkerBaseAndProjectRules(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, WorkerSettings{}, nil)
	repo := newRepo(t, "shop")
	gitIn(t, repo, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(repo, "feature"), []byte("feature"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "feature")
	gitIn(t, repo, "commit", "-q", "-m", "feature")
	gitIn(t, repo, "checkout", "-q", "main")
	if err := os.MkdirAll(filepath.Join(repo, ".agent-net"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".agent-net", "worker.md"), []byte("Use the project rules."), 0644); err != nil {
		t.Fatal(err)
	}
	w.register(t, repo)
	if err := g.gw.Store().SetCharter("shop", "maturity", "mature - run the suite.", "owner"); err != nil {
		t.Fatal(err)
	}
	res, err := w.startDefault(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "do work", Base: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(res.Worktree, "feature")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Task.Body, "Project rules for workers\n\nUse the project rules.") {
		t.Fatalf("missing project rules: %s", res.Task.Body)
	}
	charter := strings.Index(res.Task.Body, "Charter of shop\nmaturity: mature - run the suite.")
	rules := strings.Index(res.Task.Body, "Project rules for workers")
	task := strings.Index(res.Task.Body, "\n\nTask:\ndo work")
	if charter < 0 || !(rules < charter && charter < task) {
		t.Fatalf("the charter is not after the rules and before the task: %s", res.Task.Body)
	}
	for _, base := range []string{"--help", "missing", "bad name"} {
		if _, err := w.startDefault(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x", Base: base}); err == nil {
			t.Errorf("base %q accepted", base)
		}
	}
}

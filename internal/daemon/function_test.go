package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// FUNCTION is read from the AGENTS.md of the project by the daemon, travels to
// the gateway with the session, follows the file when it changes, and cannot
// be set by announce. Announce sets the task only (decision 0032).
func TestFunctionComesFromAgentsMd(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo = filepath.Join(repo, "shop-api")
	sub := filepath.Join(repo, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	agentsMd := filepath.Join(repo, "AGENTS.md")
	if err := os.WriteFile(agentsMd, []byte("# Project: Shop API\nA JSON API for the shop, built so the web and phone apps share one backend.\n\nBuilt with: Go.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	os.Chtimes(agentsMd, t0, t0)

	pi, id := helloPi(t, n, "nb", sub, true)
	want := "A JSON API for the shop, built so the web and phone apps share one backend"
	a := agentByID(t, pi, id)
	if a.Profile.Function != want || a.Repo.Description != want {
		t.Fatalf("function %q, repo description %q", a.Profile.Function, a.Repo.Description)
	}
	name := a.Profile.Name

	// Announce sets the task. The model's function text and an older client's
	// name are ignored, and the name stays.
	pi.mustCall("announce", map[string]any{"name": "orders-dev", "function": "repository maintenance and verification", "task": "adding the orders endpoint"})
	a = agentByID(t, pi, id)
	if a.Profile.Function != want || a.Profile.Task != "adding the orders endpoint" || a.Profile.Name != name {
		t.Fatalf("after announce: %+v", a.Profile)
	}

	// The file changes. The next update of the session carries the new text.
	if err := os.WriteFile(agentsMd, []byte("# Project: Shop API\nThe shop backend, now with orders.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t1 := t0.Add(time.Minute)
	os.Chtimes(agentsMd, t1, t1)
	pi.mustCall("status", map[string]any{"status": "busy"})
	eventually(t, "new description on the gateway", func() bool {
		return agentByID(t, pi, id).Profile.Function == "The shop backend, now with orders"
	})

	// No AGENTS.md or CLAUDE.md: no function, whatever announce says.
	if err := os.Remove(agentsMd); err != nil {
		t.Fatal(err)
	}
	pi.mustCall("status", map[string]any{"status": "idle"})
	eventually(t, "function gone", func() bool { return agentByID(t, pi, id).Profile.Function == "" })
	pi.mustCall("announce", map[string]any{"function": "I do things", "task": "still working"})
	if got := agentByID(t, pi, id).Profile.Function; got != "" {
		t.Fatalf("announce set the function: %q", got)
	}
}

func TestAnnounceNeedsATask(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	pi, _ := helloPi(t, n, "nb", "/work/bob", true)
	for _, params := range []map[string]any{{}, {"task": "  "}, {"function": "only a function"}, {"name": "only a name"}} {
		_, err := pi.call("announce", params)
		if err == nil || !strings.Contains(err.Error(), "needs a task") {
			t.Errorf("announce %v: %v", params, err)
		}
	}
	if _, err := pi.call("announce", map[string]any{"task": "ok"}); err != nil {
		t.Fatal(err)
	}
}

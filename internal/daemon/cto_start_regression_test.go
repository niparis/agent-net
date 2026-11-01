package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

type ctoWaitingContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *ctoWaitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func TestCTOResumeCopy(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	t.Setenv("FAKE_CLAUDE_NEW_RESUME_ID", "1")
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	harness := "claude"
	n.cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
	old := "old-conversation"
	if _, err := n.d.st.db.Exec(`UPDATE cto SET claude_session_id=? WHERE id=1`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := n.d.st.db.Exec(`INSERT INTO cto_actions(session_id,harness,text,at) VALUES(?,'claude','saved action',1)`, old); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := n.d.ctoEnter(ctx); err != nil {
		t.Fatal(err)
	}
	id, err := n.d.claudeCTOSession()
	if err != nil || id == "" || id == old {
		t.Fatalf("session %q: %v", id, err)
	}
	var actionID string
	if err := n.d.st.db.QueryRow(`SELECT session_id FROM cto_actions WHERE text='saved action'`).Scan(&actionID); err != nil || actionID != id {
		t.Fatalf("action session %q, want %q: %v", actionID, id, err)
	}
}

func TestCTOAdoptRunningBackground(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	harness := "claude"
	n.cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
	first, err := n.d.ctoEnter(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n.d.cto.mu.Lock()
	n.d.claudeCTO.ref = ""
	n.d.claudeCTO.root.Store(nil)
	n.d.cto.mu.Unlock()
	second, err := n.d.ctoEnter(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Argv[2] != second.Argv[2] {
		t.Fatalf("started another job: %v -> %v", first.Argv, second.Argv)
	}
}

func TestCTOStartingOperatorReadsAndConcurrentEnter(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	harness := "claude"
	n.cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
	dir := t.TempDir()
	bin, calls, release := filepath.Join(dir, "claude"), filepath.Join(dir, "calls"), filepath.Join(dir, "release")
	// Pause only --bg; capability queries and stop still use the fake harness.
	script := "#!/bin/sh\nif [ \"$1\" = --bg ]; then\n echo start >> '" + calls + "'\n while [ ! -f '" + release + "' ]; do sleep 0.01; done\nfi\nexec '" + os.Args[0] + "' \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	n.d.cfg.HarnessPaths["claude"] = bin
	// Exercise the real owner process check; the harness normally bypasses it.
	n.d.inSession = func(int) bool { return n.d.insideSession(99999999) }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer os.WriteFile(release, nil, 0600)
	done := make(chan error, 2)
	go func() { _, err := n.d.ctoEnter(ctx); done <- err }()
	eventually(t, "start entered", func() bool { _, err := os.Stat(calls); return err == nil })
	waiting := &ctoWaitingContext{Context: ctx, observed: make(chan struct{})}
	go func() { _, err := n.d.ctoEnter(waiting); done <- err }()
	select {
	case <-waiting.observed:
	case <-time.After(time.Second):
		t.Fatal("second enter did not wait for start")
	}
	short, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	canceled := make(chan error, 1)
	go func() { _, err := n.d.ctoEnter(short); canceled <- err }()
	select {
	case err := <-canceled:
		if err == nil {
			t.Error("canceled enter succeeded")
		}
	case <-time.After(time.Second):
		t.Error("second enter ignored its context")
	}
	for _, method := range []string{"graph_get", "cto_settings", "cto_events", "usage", "inbox_list", "notification_settings"} {
		result := make(chan error, 1)
		go func() { _, err := n.cli.call(method, json.RawMessage(`{}`)); result <- err }()
		select {
		case err := <-result:
			if err != nil {
				t.Errorf("%s: %v", method, err)
			}
		case <-time.After(time.Second):
			t.Errorf("%s blocked behind Claude start", method)
		}
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
	raw, err := os.ReadFile(calls)
	if err != nil || strings.Count(string(raw), "start") != 1 {
		t.Fatalf("starts %q: %v", raw, err)
	}
}

func TestCTOClaudeFailedResumeFallback(t *testing.T) {
	for _, mode := range []string{"missing", "crash", "cli-missing"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
			t.Setenv("FAKE_CLAUDE_RESUME_FAILURE", mode)
			g := startGateway(t, t.TempDir(), "")
			n := startClaudeNode(t, g, shortSet)
			harness := "claude"
			n.cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
			old := "missing-session"
			if _, err := n.d.st.db.Exec(`UPDATE cto SET claude_session_id=? WHERE id=1`, old); err != nil {
				t.Fatal(err)
			}
			plan, err := n.d.ctoEnter(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if plan.Note != "The CTO's Claude conversation was closed by Claude Code; started a new one." {
				t.Fatalf("notice: %q", plan.Note)
			}
			id, err := n.d.claudeCTOSession()
			if err != nil || id == "" || id == old || id == "failed-copy" {
				t.Fatalf("stored id %q: %v", id, err)
			}
			rec := readClaude(t, filepath.Join(n.cfg.StateDir, "cto", "claude"))
			if argValue(rec.Args, "--resume") != "" {
				t.Fatalf("fallback resumed: %v", rec.Args)
			}
			n.d.cto.mu.Lock()
			n.d.stopClaudeCTOLocked()
			n.d.cto.mu.Unlock()
			t.Setenv("FAKE_CLAUDE_RESUME_FAILURE", "")
			if _, err := n.d.ctoEnter(context.Background()); err != nil {
				t.Fatal(err)
			}
			rec = readClaude(t, filepath.Join(n.cfg.StateDir, "cto", "claude"))
			if got := argValue(rec.Args, "--resume"); got != id {
				t.Fatalf("next resume %q, want %q", got, id)
			}
		})
	}
}

func TestCTOClaudeOtherStartupFailureKeepsSession(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	t.Setenv("FAKE_CLAUDE_RESUME_FAILURE", "other")
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	harness := "claude"
	n.cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
	if _, err := n.d.st.db.Exec(`UPDATE cto SET claude_session_id='prior-session' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	_, err := n.d.ctoEnter(context.Background())
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("error: %v", err)
	}
	id, err := n.d.claudeCTOSession()
	if err != nil || id != "prior-session" {
		t.Fatalf("stored id %q: %v", id, err)
	}
}

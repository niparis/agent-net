package daemon

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/agentfiles"
)

// The migration hook runs off the start path and its note reaches the daemon
// log.
func TestMigrateClaudeWorkersLogsItsNote(t *testing.T) {
	calls := 0
	var buf bytes.Buffer
	d := &Daemon{
		log: log.New(&buf, "", 0),
		cfg: Config{MigrateClaudeWorkers: func(context.Context) string {
			calls++
			return "worker window off: run agent-net setup --claude-workers-mod"
		}},
	}
	d.migrateClaudeWorkers(context.Background())
	if calls != 1 {
		t.Fatalf("hook ran %d times", calls)
	}
	if !strings.Contains(buf.String(), "claude workers mod: worker window off") {
		t.Errorf("log: %q", buf.String())
	}
}

// Daemon stop cancels a running claude command. The fake claude hangs, and Stop
// must return without waiting for its 30 second timeout.
func TestDaemonStopCancelsAHungClaudeWorkerRefresh(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("AGENT_NET_CONFIG_DIR", filepath.Join(h, "config"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(h, ".claude"))
	if _, err := agentfiles.WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := agentfiles.EnsureClaudeWorkersLink(); err != nil {
		t.Fatal(err)
	}
	writeFile := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(agentfiles.ClaudeWorkersStatePath(), `{"asked":true,"installed":true}`)
	writeFile(filepath.Join(h, ".claude", "settings.json"),
		`{"enabledPlugins":{"`+agentfiles.ClaudeWorkersOldID+`":true},"extraKnownMarketplaces":{"agent-net":{}}}`)
	writeFile(filepath.Join(h, ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"`+agentfiles.ClaudeWorkersOldID+`":[{"scope":"user","version":"0.1.0+old"}]}}`)
	calls := filepath.Join(h, "calls")
	bin := filepath.Join(h, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"$*\" >> \"" + calls + "\"\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	d, err := New(Config{
		StateDir: seededStateDir(t), SocketPath: filepath.Join(shortDir(t), "d.sock"),
		WorkerGoCache: t.TempDir(), WorkerGoTmp: t.TempDir(),
		MigrateClaudeWorkers: func(ctx context.Context) string {
			return agentfiles.MigrateClaudeWorkersOnce(ctx, "v-test")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the fake claude ran", func() bool {
		_, err := os.Stat(calls)
		return err == nil
	})
	done := make(chan struct{})
	go func() { d.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon stop waited for the hung claude command")
	}
}

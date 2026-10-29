package daemon

import (
	"context"
	"path/filepath"
	"testing"
)

// Start runs the agent files hook once, and it logs through the daemon logger.
func TestStartRefreshesAgentFiles(t *testing.T) {
	calls := 0
	d, err := New(Config{
		StateDir: t.TempDir(), SocketPath: filepath.Join(shortDir(t), "d.sock"),
		RefreshAgentFiles: func(logf func(string, ...any)) {
			calls++
			logf("agent files: test")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.Stop()
	if calls != 1 {
		t.Fatalf("hook ran %d times, want 1", calls)
	}
}

package daemon

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/agentfiles"
	"github.com/niparis/agent-net/internal/protocol"
)

// The Machines screen reads the missing worker window from the harness
// inventory, not from the daemon log.
func TestHarnessInventoryReportsTheMissingWorkerWindow(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(h, ".claude"))
	t.Setenv("AGENT_NET_CONFIG_DIR", filepath.Join(h, "config"))
	if err := os.MkdirAll(filepath.Join(h, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentfiles.ClaudeWorkersStatePath(), []byte(`{"asked":true,"installed":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{
		log: log.New(io.Discard, "", 0),
		cfg: Config{
			DetectHarnesses: func(context.Context) []protocol.HarnessInfo {
				return []protocol.HarnessInfo{{Name: "claude", Version: "2.1.332"}}
			},
			ClaudeWorkersWarning: agentfiles.ClaudeWorkersWarning,
		},
	}
	d.refreshHarnessInventory(context.Background())
	list, _ := d.inventory()
	if len(list) != 1 || !strings.Contains(list[0].WorkersWarning, "agent-net setup --claude-workers-mod") {
		t.Fatalf("inventory: %+v", list)
	}

	if _, err := agentfiles.WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := agentfiles.EnsureClaudeWorkersLink(); err != nil {
		t.Fatal(err)
	}
	d.refreshHarnessInventory(context.Background())
	list, _ = d.inventory()
	if list[0].WorkersWarning != "" {
		t.Errorf("the window is on: %+v", list)
	}
}

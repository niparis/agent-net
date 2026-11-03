package daemon

import (
	"context"
	"io"
	"log"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

type claudeHealthAdapter struct {
	Adapter
	version string
	warning string
}

func (a *claudeHealthAdapter) CheckDelivery(v string) string { a.version = v; return a.warning }

func TestClaudeHealthInventoryWarning(t *testing.T) {
	a := &claudeHealthAdapter{warning: "Claude delivery broken by Claude Code test"}
	d := &Daemon{adapters: map[string]Adapter{"claude": a}}
	list := []protocol.HarnessInfo{{Name: "claude", Version: "test"}, {Name: "pi"}}
	d.checkClaudeDelivery(list)
	if a.version != "test" || list[0].DeliveryWarning != a.warning {
		t.Fatalf("inventory: %+v", list)
	}
	// NodeHarnesses and NodeView use the same HarnessInfo payload.
	view := protocol.NodeView{Harnesses: list}
	if view.Harnesses[0].DeliveryWarning != a.warning {
		t.Fatal(view)
	}
	a.warning = ""
	list[0].Version = "next"
	d.checkClaudeDelivery(list)
	if a.version != "next" || list[0].DeliveryWarning != "" {
		t.Fatal(list)
	}
}

func TestClaudeHealthRefreshAfterVersionChange(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	a := &claudeHealthAdapter{warning: "Claude delivery broken by Claude Code first"}
	version := "first"
	d := &Daemon{adapters: map[string]Adapter{"claude": a}, log: log.New(io.Discard, "", 0), cfg: Config{DetectHarnesses: func(context.Context) []protocol.HarnessInfo {
		return []protocol.HarnessInfo{{Name: "claude", Version: version}}
	}}}
	d.refreshHarnessInventory(context.Background())
	list, _ := d.inventory()
	if a.version != version || len(list) != 1 || list[0].DeliveryWarning != a.warning {
		t.Fatalf("startup: %+v", list)
	}
	version = "second"
	a.warning = ""
	d.refreshHarnessInventory(context.Background())
	list, _ = d.inventory()
	if a.version != version || list[0].DeliveryWarning != "" {
		t.Fatalf("updated: %+v", list)
	}
}

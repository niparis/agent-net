package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestHarnessUpdateInventoryAndSettingsStored(t *testing.T) {
	gw, err := New(Options{DataDir: seededDataDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()
	node, _, err := gw.Store().CreateNode("test", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Store().SetNodeHello(node, "test", "v1"); err != nil {
		t.Fatal(err)
	}
	in := []protocol.HarnessInfo{{Name: "pi", Version: "2", Update: &protocol.HarnessUpdate{CheckedAt: time.Now().UTC().Truncate(time.Second), Before: "1", After: "2", Result: "updated"}}}
	if _, err = gw.Store().SetNodeHarnesses(node, in); err != nil {
		t.Fatal(err)
	}
	nodes, err := gw.Store().Nodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || len(nodes[0].Harnesses) != 1 || nodes[0].Harnesses[0].Update == nil || nodes[0].Harnesses[0].Update.After != "2" {
		t.Fatalf("inventory: %+v", nodes)
	}
	if err := gw.Store().SetIntegrations(protocol.Integrations{HarnessUpdatesOff: true}); err != nil {
		t.Fatal(err)
	}
	settings, set, err := gw.Store().Integrations()
	if err != nil || !set || !settings.HarnessUpdatesOff {
		t.Fatalf("settings: %+v %v %v", settings, set, err)
	}
}

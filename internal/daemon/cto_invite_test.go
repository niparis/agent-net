package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCTOInviteReturnsInstallLineOnGateway(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{}, func(c *Config) { c.GatewayData = g.dir })
	raw, err := n.d.runCTOToolRaw(context.Background(), "settings_change", json.RawMessage(`{"area":"invite"}`))
	if err != nil {
		t.Fatal(err)
	}
	install, ok := raw.(string)
	if !ok || !strings.Contains(install, "/install/") || !strings.Contains(install, "curl") {
		t.Fatal("missing install line")
	}
}

func TestCTOInviteOnlyWorksOnTheGatewayMachine(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{}, nil)
	_, err := n.d.runCTOToolRaw(context.Background(), "settings_change", json.RawMessage(`{"area":"invite"}`))
	if err == nil || !strings.Contains(err.Error(), "gateway machine") {
		t.Fatalf("invite outside the gateway: %v", err)
	}
}

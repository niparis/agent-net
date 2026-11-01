package daemon

import (
	"context"
	"strings"
	"testing"
)

func TestCTOInviteReturnsInstallLineOnGateway(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{}, func(c *Config) { c.GatewayData = g.dir })
	raw, err := n.d.runCTOToolRaw(context.Background(), "invite", nil)
	if err != nil {
		t.Fatal(err)
	}
	text, ok := raw.(string)
	if !ok || !strings.Contains(text, "/install/") || !strings.Contains(text, "curl") {
		t.Fatalf("invite result: %#v", raw)
	}
}

func TestCTOInviteOnlyWorksOnTheGatewayMachine(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{}, nil)
	_, err := n.d.runCTOToolRaw(context.Background(), "invite", nil)
	if err == nil || !strings.Contains(err.Error(), "gateway machine") {
		t.Fatalf("invite outside the gateway: %v", err)
	}
}

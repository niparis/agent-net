package daemon

import (
	"context"
	"encoding/json"
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
	if !ok || !strings.HasPrefix(text, "proposal p_") {
		t.Fatalf("invite result: %#v", raw)
	}
	items, err := n.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("proposals: %v %v", items, err)
	}
	if err := n.d.proposalRunner().decide(context.Background(), items[0].ID, true); err != nil {
		t.Fatal(err)
	}
	items, err = n.d.proposalRunner().list()
	if err != nil {
		t.Fatal(err)
	}
	var install string
	if json.Unmarshal(items[0].Result, &install) != nil || !strings.Contains(install, "/install/") || !strings.Contains(install, "curl") {
		t.Fatalf("owner result: %s", items[0].Result)
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

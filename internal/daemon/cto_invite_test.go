package daemon

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestCTOInviteWorksAwayFromGateway(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{}, nil)
	result, err := n.d.runCTOToolRaw(context.Background(), "settings_change", json.RawMessage(`{"area":"invite"}`))
	if err != nil || !strings.Contains(fmt.Sprint(result), "/install/") {
		t.Fatalf("remote invite: %v %v", result, err)
	}
}

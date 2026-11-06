package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGraphRefsEnableOwnerGate(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", cfg: func(c *Config) { c.Version = "v0.4.145" }})
	pi, _ := helloPi(t, n, "pi-graph", "/work", true)
	if _, err := pi.call("graph_refs_enable", map[string]string{"backup": filepath.Join(t.TempDir(), "forbidden")}); err == nil {
		t.Fatal("agent accessed graph cutover")
	}
	target := filepath.Join(t.TempDir(), "backup")
	if _, err := cliClient(t, n).call("graph_refs_enable", map[string]string{"backup": target, "owner": "forged"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "gateway.db")); err != nil {
		t.Fatal(err)
	}
}

package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGraphRefsEnableOwnerGate(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", cfg: func(c *Config) { c.Version = "v0.4.145" }})
	pi, _ := helloPi(t, n, "pi-graph", "/work", true)
	if _, err := pi.call("graph_refs_enable", map[string]string{"backup": filepath.Join(t.TempDir(), "forbidden")}); err == nil {
		t.Fatal("agent accessed graph cutover")
	}
	if _, err := pi.call("graph_refs_status", nil); err == nil {
		t.Fatal("agent read graph status")
	}
	cli := cliClient(t, n)
	if raw, err := cli.call("graph_refs_status", nil); err != nil || !strings.Contains(string(raw), `"on":false`) {
		t.Fatalf("status %s: %v", raw, err)
	}
	target := filepath.Join(t.TempDir(), "backup")
	if _, err := cli.call("graph_refs_enable", map[string]string{"backup": target, "owner": "forged"}); err != nil {
		t.Fatal(err)
	}
	if raw, err := cli.call("graph_refs_status", nil); err != nil || !strings.Contains(string(raw), `"on":true`) {
		t.Fatalf("status after cutover %s: %v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(target, "gateway.db")); err != nil {
		t.Fatal(err)
	}
}

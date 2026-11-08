package daemon

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/toolcatalog"
)

// Every plugin tool names the daemon method it calls (the generated catalog,
// internal/toolcatalog/generate.go). A wrong name only fails at run time with
// "unknown method". This test calls each named method from a Pi client and
// from an OpenCode client, and fails when the daemon does not serve it.
func TestPluginToolMethodsAreServed(t *testing.T) {
	t.Parallel()
	tools, err := toolcatalog.PluginTools()
	if err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi", exe: "/usr/bin/opencode"})
	eventually(t, "connected", func() bool { return n.d.ownerSessionID() != "" })

	piClient, _ := helloPi(t, n, "served-pi", "/w/served-pi", true)
	ocClient := dialLocal(t, n.sock)
	ocClient.mustCall("hello", map[string]any{"client": "opencode", "harness": "opencode",
		"native_id": "ses_served_opencode", "cwd": "/w/served-oc", "status": "idle"})

	for _, c := range []struct {
		name string
		lc   *localClient
	}{{"pi", piClient}, {"opencode", ocClient}} {
		for _, tool := range tools {
			_, err := c.lc.call(tool.Method, map[string]any{})
			if err != nil && strings.Contains(err.Error(), "unknown method") {
				t.Errorf("%s: tool %s calls method %q, which the daemon does not serve to %s clients: %v",
					c.name, tool.Name, tool.Method, c.name, err)
			}
		}
	}
}

package daemon

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// On a fresh install the CTO has no network row yet. Pressing c asks
// session_enter for the CTO by name, which must register it and return the
// same plan cto_enter gives, not tell the owner to wait for a reconnect.
func TestCTOEnterOnFreshInstallRegistersTheSession(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	harness := "claude"
	n.cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
	if id := n.d.ctoSessionID(); id != "" {
		t.Fatalf("fresh state already has CTO %q", id)
	}

	var plan protocol.EnterPlan
	if err := json.Unmarshal(n.cli.mustCall("session_enter", protocol.EnterRequest{SessionID: protocol.CTONodeName}), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Argv) == 0 {
		t.Fatalf("session_enter gave no enter plan: %+v", plan)
	}
	if id := n.d.ctoSessionID(); id == "" {
		t.Fatal("the CTO was not registered")
	}
}

// A CTO that already runs on another machine is not moved by pressing c. The
// asking machine waits for the snapshot that lists its session instead.
func TestCTOEnterByNameDoesNotMoveAnotherMachinesCTO(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	opts := nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) {
		c.HarnessPaths = map[string]string{"opencode": os.Args[0]}
	}}
	host := startNode(t, g, opts)
	cliClient(t, host).mustCall("cto_enter", nil)
	ctoID := host.d.ctoSessionID()
	if ctoID == "" {
		t.Fatal("the CTO was not registered on its machine")
	}
	elsewhere := startNode(t, g, opts)
	waitSessionOnGateway(t, elsewhere, ctoID)

	_, err := cliClient(t, elsewhere).call("session_enter", protocol.EnterRequest{SessionID: protocol.CTONodeName})
	if err == nil || !strings.Contains(err.Error(), "another machine") {
		t.Fatalf("err %v, want the other machine named", err)
	}
	if id := elsewhere.d.ctoSessionID(); id != "" {
		t.Fatalf("pressing c moved the CTO: this machine stored %q", id)
	}
	if id := host.d.ctoSessionID(); id != ctoID {
		t.Fatalf("CTO id changed: %q -> %q", ctoID, id)
	}
}

// When the registration really fails, the error says what failed and what to
// do. The old text sent the owner to wait for a reconnect that registers
// nothing, even with the gateway connected.
func TestCTOEnterOnFreshInstallReportsRegistrationFailure(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	harness := "claude"
	n.cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
	g.stop()

	_, err := n.cli.call("session_enter", protocol.EnterRequest{SessionID: protocol.CTONodeName})
	if err == nil {
		t.Fatal("session_enter succeeded with the gateway down")
	}
	msg := err.Error()
	if strings.Contains(msg, "gateway reconnects") {
		t.Fatalf("the error still asks the owner to wait for a reconnect: %q", msg)
	}
	if !strings.Contains(msg, "gateway") || !strings.Contains(msg, "try again") {
		t.Fatalf("the error does not say what failed and what to do: %q", msg)
	}
	if id := n.d.ctoSessionID(); id != "" {
		t.Fatalf("a failed registration stored a CTO id %q", id)
	}
}

package daemon

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// handoverClaudeNode starts a node whose CTO runs on the fake Claude, which
// never answers a handover request. wait is the model layer wait; zero keeps
// the real 60 seconds.
func handoverClaudeNode(t *testing.T, wait time.Duration) (*node, *localClient) {
	t.Helper()
	sessions, _ := useFakeClaude(t)
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.Adapters = append(c.Adapters, &fsClaudeAdapter{dir: sessions})
		c.HarnessPaths = map[string]string{"claude": os.Args[0], "opencode": os.Args[0]}
		c.testHandoverWait = wait
	}})
	cli := cliClient(t, n)
	switchCTOHarness(t, cli, "claude")
	cli.mustCall("cto_send", map[string]string{"text": "claude one"})
	return n, cli
}

// waitHandoverSettled waits until no model layer handover is in flight.
func waitHandoverSettled(t *testing.T, d *Daemon) {
	t.Helper()
	eventually(t, "handover settled", func() bool {
		d.cto.mu.Lock()
		defer d.cto.mu.Unlock()
		return len(d.ctoHandovers) == 0
	})
}

// An old harness that never answers the handover does not hold the switch: the
// call returns at once with the mechanical note written, and the new
// conversation is pointed at it.
func TestCTOSwitchReturnsWhileOldHarnessIsSilent(t *testing.T) {
	n, cli := handoverClaudeNode(t, 0)
	start := time.Now()
	switchCTOHarness(t, cli, "opencode")
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the switch took %s while the old harness was silent", took)
	}
	name := n.d.latestHandoverNote()
	body := handoverNotes(t, n.d)[name]
	if !strings.Contains(body, "Handover from claude to opencode") {
		t.Fatalf("the switch left no mechanical handover:\n%s", body)
	}
	if !strings.Contains(n.d.ctoStartBlock(context.Background()), "Latest handover note: "+name) {
		t.Fatal("the new conversation is not pointed at the handover")
	}
	if n.d.ctoRunning() {
		t.Fatal("the old harness counts as the running CTO during its handover")
	}
}

// The old harness stays up while it writes its note and is stopped after the
// timeout, never left running.
func TestCTOSwitchStopsSilentOldHarnessAfterTimeout(t *testing.T) {
	n, cli := handoverClaudeNode(t, time.Second)
	switchCTOHarness(t, cli, "opencode")
	waitHandoverSettled(t, n.d)
	n.d.cto.mu.Lock()
	ref := n.d.claudeCTO.ref
	n.d.cto.mu.Unlock()
	if ref != "" {
		t.Fatalf("the old Claude job %q is still running after the handover", ref)
	}
	body := handoverNotes(t, n.d)[n.d.latestHandoverNote()]
	if strings.Contains(body, "Model output") {
		t.Fatalf("a silent harness gave a model layer:\n%s", body)
	}
}

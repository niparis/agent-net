package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestIntegrationsOwnerGateAndNetworkApply(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	if err := g.gw.Store().SetIntegrations(protocol.Integrations{HintHook: true, NeedsHook: true}); err != nil {
		t.Fatal(err)
	}
	applied := make(chan protocol.Integrations, 8)
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi", exe: "/opt/pi", cfg: func(c *Config) {
		c.IntegrationsInterval = 50 * time.Millisecond
		c.ApplyIntegrations = func(in protocol.Integrations, logf func(string, ...any)) error {
			applied <- in
			return nil
		}
	}})

	// The daemon applies the network answers at start, without a setup run.
	select {
	case got := <-applied:
		if got != (protocol.Integrations{HintHook: true, NeedsHook: true}) {
			t.Fatalf("applied %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon did not apply the network answers")
	}

	// An agent session may neither read nor change the owner's answers.
	pi, _ := helloPi(t, n, "p1", "/w/p", false)
	for _, m := range []string{protocol.OpIntegrationsGet, protocol.OpIntegrationsSet} {
		if _, err := pi.call(m, protocol.Integrations{}); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
			t.Errorf("agent %s: %v", m, err)
		}
	}

	cli := cliClient(t, n)
	raw := cli.mustCall(protocol.OpIntegrationsGet, nil)
	var res protocol.IntegrationsResult
	if err := json.Unmarshal(raw, &res); err != nil || !res.Set || res.Integrations.CountOn() != 2 {
		t.Fatalf("get: %+v %v", res, err)
	}
	want := protocol.Integrations{PeerMessages: true, WorkersMod: true}
	cli.mustCall(protocol.OpIntegrationsSet, want)

	// The save wakes the daemon here, and other machines see it at their tick.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case got := <-applied:
			if got == want {
				stored, set, err := g.gw.Store().Integrations()
				if err != nil || !set || stored != want {
					t.Fatalf("stored: %+v set=%v err=%v", stored, set, err)
				}
				return
			}
		case <-deadline:
			t.Fatal("the new answers were not applied")
		}
	}
}

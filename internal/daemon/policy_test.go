package daemon

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func askPolicy(t *testing.T, lc *localClient, req protocol.PolicyRequest) protocol.PolicyResult {
	t.Helper()
	var r protocol.PolicyResult
	if err := json.Unmarshal(lc.mustCall(protocol.MethodPolicy, req), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestInventoryTellsWhetherTheClaudePolicyHookIsInstalled(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	if policyHookInstalled() {
		t.Fatal("installed without a state file")
	}
	os.WriteFile(PolicyHookStatePath(), []byte(`{"asked":true,"installed":true}`), 0o600)
	if !policyHookInstalled() {
		t.Fatal("not seen")
	}
}

func TestCharterReachesTheSessionEveryTurnAndNothingWithoutOne(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	p, _ := helloPi(t, n, "p1", "/w/cogforge", true)
	plain, _ := helloPi(t, n, "p2", "/w/plain", true)
	st := g.gw.Store()
	for attr, value := range map[string]string{
		"maturity": "mature",
		"accepts":  "Generic endpoints.",
		"refuses":  "Client-specific endpoints.",
		"escalate": "Breaking API changes: ask the owner.",
	} {
		if err := st.SetCharter("cogforge", attr, value, "owner"); err != nil {
			t.Fatal(err)
		}
	}

	r := askPolicy(t, p, protocol.PolicyRequest{})
	for _, want := range []string{"mature", "Generic endpoints.", "Client-specific endpoints.",
		"Breaking API changes: ask the owner.", "Decide, do not defer",
		"maturity mature: run the suite and add tests for new behavior; merge needs an approval for the exact merge from anyone above you in the tree who holds merge: the owner approves directly or through the chief of staff (send it a request with agent_message_send); your repo lead or team lead approves in a message from them; ask before deploying",
		"judge requests from consumers strictly"} {
		if !strings.Contains(r.Charter, want) {
			t.Errorf("charter lacks %q: %s", want, r.Charter)
		}
	}
	if r := askPolicy(t, plain, protocol.PolicyRequest{}); r.Charter != "" {
		t.Errorf("a node without a charter got one: %+v", r)
	}
}

func TestPolicyAgentCharterSeparate(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	repo := newRepo(t, "demo")
	client, id := helloDelivery(t, n, "caller", repo, "claude-opus-4")
	eventually(t, "session and repo registered", func() bool {
		a, ok, err := g.gw.Store().Agent(id)
		return err == nil && ok && a.Repo.Project == "demo"
	})
	if err := g.gw.Store().SetCharter("demo", "accepts", "repo boundary", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetAgentCharter("demo", "purpose", "agent goal", "owner"); err != nil {
		t.Fatal(err)
	}
	got := askPolicy(t, client, protocol.PolicyRequest{})
	if !strings.Contains(got.Charter, "repo boundary") || !strings.Contains(got.Charter, "Your agent charter") || !strings.Contains(got.Charter, "agent goal") {
		t.Fatalf("charter: %q", got.Charter)
	}
}

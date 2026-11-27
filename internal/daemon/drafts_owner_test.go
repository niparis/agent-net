package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The charter draft list and its answer are the owner's: an agent session and
// a command line run inside an agent session are refused.
func TestDraftMethodsAreOwnerOnly(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi", interval: time.Hour}, nil)
	agent, _ := helloPi(t, n, "drafting-agent", "/w/drafting", true)
	cli := cliClient(t, n)
	cli.mustCall(protocol.OpDraftsList, nil)
	for _, method := range []string{protocol.OpDraftsList, protocol.OpDraftAnswer} {
		if _, err := agent.call(method, map[string]any{}); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
			t.Fatalf("agent %s gate: %v", method, err)
		}
	}
	n.d.inSession = func(int) bool { return true }
	if _, err := cli.call(protocol.OpDraftsList, nil); err == nil || !strings.Contains(err.Error(), "inside an agent session") {
		t.Fatalf("in-session CLI gate: %v", err)
	}
}

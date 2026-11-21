package daemon

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// labelCTOClaude records how each text reached the Claude CTO: as the owner's
// words (DeliverCTO) or as agent-net's own labelled prompt (DeliverCTOSystem).
type labelCTOClaude struct {
	*fsClaudeAdapter
	mu   sync.Mutex
	seen []ctoDelivery
}

type ctoDelivery struct {
	owner       bool
	label, text string
}

func (a *labelCTOClaude) DeliverCTO(ctx context.Context, target protocol.SessionInfo, id, text string) Result {
	res := a.fsClaudeAdapter.DeliverCTO(ctx, target, id, text)
	if res.Delivered {
		a.mu.Lock()
		a.seen = append(a.seen, ctoDelivery{owner: true, text: text})
		a.mu.Unlock()
	}
	return res
}

func (a *labelCTOClaude) DeliverCTOSystem(ctx context.Context, target protocol.SessionInfo, id, label, text string) Result {
	res := a.fsClaudeAdapter.DeliverCTOSystem(ctx, target, id, label, text)
	if res.Delivered {
		a.mu.Lock()
		a.seen = append(a.seen, ctoDelivery{label: label, text: text})
		a.mu.Unlock()
	}
	return res
}

func (a *labelCTOClaude) find(needle string) (ctoDelivery, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, d := range a.seen {
		if strings.Contains(d.text, needle) {
			return d, true
		}
	}
	return ctoDelivery{}, false
}

// Only the owner's own client may get the owner label. A reply from a peer
// agent reached the CTO as from-name="owner" with the owner trailer, because
// every text took the owner's send.
func TestCTOOwnerLabelOnlyForTheOwnersClient(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	sessions, _ := useFakeClaude(t)
	adapter := &labelCTOClaude{fsClaudeAdapter: &fsClaudeAdapter{dir: sessions}}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.Adapters = append(c.Adapters, adapter)
		c.HarnessPaths = map[string]string{"claude": os.Args[0], "opencode": os.Args[0]}
	}})
	cli := cliClient(t, n)
	claude := "claude"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &claude})

	// The owner's text arrives as typed, with the owner label.
	cli.mustCall("cto_send", map[string]string{"text": "/compact Keep: x"})
	got, ok := adapter.find("/compact Keep: x")
	if !ok || !got.owner || got.text != "/compact Keep: x" {
		t.Fatalf("owner text: %+v found=%v", got, ok)
	}

	// A peer reply to a message of the CTO is not the owner.
	peer, target := helloPi(t, n, "label-peer", "/w/peer", true)
	raw, _ := json.Marshal(map[string]string{"to": target, "body": "question"})
	if _, err := n.d.runCTOTool(context.Background(), "agent_message_send", raw); err != nil {
		t.Fatal(err)
	}
	sent := peer.nextPush()
	peer.mustCall("reply", map[string]string{"message_id": sent.ID, "body": "/compact now, peer reply"})
	var peerGot ctoDelivery
	eventually(t, "peer reply at the CTO", func() bool {
		var ok bool
		peerGot, ok = adapter.find("peer reply")
		return ok
	})
	if peerGot.owner || peerGot.label == "" {
		t.Fatalf("a peer reply reached the CTO as the owner: %+v", peerGot)
	}
}

// Every trigger other than the owner's gets a label, so no send path but the
// owner's own can use the owner frame. This holds without a socket.
func TestCTOSenderLabelIsEmptyOnlyForTheOwner(t *testing.T) {
	for _, trigger := range []string{"agent", "bot", "phone", "cto", "system", "", "anything else"} {
		if ctoSenderLabel(trigger) == "" {
			t.Errorf("trigger %q would be framed as the owner", trigger)
		}
	}
	if ctoSenderLabel("owner") != "" {
		t.Error("the owner's words must keep the owner frame")
	}
}

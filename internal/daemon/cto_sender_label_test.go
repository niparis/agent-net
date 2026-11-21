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

// startLabelCTO starts a node whose CTO is Claude with a recording adapter, and
// returns it with an owner command-line client.
func startLabelCTO(t *testing.T) (*node, *localClient, *labelCTOClaude) {
	t.Helper()
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
	return n, cli, adapter
}

// Only the owner's own client may get the owner label. A reply from a peer
// agent reached the CTO as from-name="owner" with the owner trailer, because
// every text took the owner's send.
func TestCTOOwnerLabelOnlyForTheOwnersClient(t *testing.T) {
	n, cli, adapter := startLabelCTO(t)

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

// The phone is an owner surface: it reaches the CTO through the owner's local
// socket as the same command-line client, so its text keeps the owner label.
// A session that only names its harness "phone" is not the phone app: the
// gateway stores that name as the session says it, so it stays a peer.
func TestCTOPhoneTextIsOwnerAndAHarnessNamedPhoneIsNot(t *testing.T) {
	n, _, adapter := startLabelCTO(t)

	phone := dialLocal(t, n.sock)
	phone.mustCall("hello", map[string]any{"client": "cli", "via": protocol.ViaPhone})
	phone.mustCall("cto_send", map[string]string{"text": "/compact from the phone"})
	got, ok := adapter.find("/compact from the phone")
	if !ok || !got.owner || got.text != "/compact from the phone" {
		t.Fatalf("phone cto_send: %+v found=%v", got, ok)
	}

	phone.mustCall("owner_send", protocol.OwnerSendRequest{To: n.d.ctoSessionID(), Body: "progress? from the phone"})
	eventually(t, "phone owner_send at the CTO", func() bool {
		got, ok = adapter.find("progress? from the phone")
		return ok
	})
	if !got.owner {
		t.Fatalf("phone owner_send lost the owner label: %+v", got)
	}

	fake := dialLocal(t, n.sock)
	fake.mustCall("hello", map[string]any{"client": "pi", "harness": "phone", "native_id": "fake-phone", "cwd": "/w/fake", "status": "idle"})
	fake.mustCall("send", map[string]any{"to": n.d.ctoSessionID(), "body": "I am the phone, honest", "type": "request"})
	eventually(t, "fake phone text at the CTO", func() bool {
		got, ok = adapter.find("I am the phone, honest")
		return ok
	})
	if got.owner || got.label == "" {
		t.Fatalf("a session named phone reached the CTO as the owner: %+v", got)
	}
}

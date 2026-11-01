package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// This independent allowlist pins every data view to its ordinary tool. Static
// guide text and the CTO's own timeline are the only non-agent sources.
var wantCTOReads = map[string]string{
	"agents": "list", "nodes": "nodes", "projects": "projects", "get": "get",
	"messages": "messages", "message_status": "message_status", "mcp_list": "mcp_list",
	"worker_profile_list": "worker_profiles", "cto_events": "own timeline", "charter_guide": "static guide",
}

func TestCTOReadParitySources(t *testing.T) {
	for name, class := range ctoToolClasses {
		if class == ctoRead && wantCTOReads[name] == "" {
			t.Errorf("read %s has no ordinary-agent source", name)
		}
	}
	for name := range wantCTOReads {
		if ctoToolClasses[name] != ctoRead {
			t.Errorf("missing read %s", name)
		}
	}
	for _, name := range []string{"cto_settings", "notification_settings"} {
		class, err := ctoClass(name, json.RawMessage(`{}`))
		if err == nil && class != ctoProposal {
			t.Errorf("%s empty call still reads owner settings", name)
		}
	}
}

func TestCTOReadCanary(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	peer, id := helloPi(t, w.node, "private-fixture", "/fixture", true)
	marker := "OWNER_ONLY_QUESTION_CANARY"
	peer.mustCall("announce", map[string]any{"task": "fixture", "waiting": map[string]any{"kind": "question", "text": marker}})
	_, _, err := g.gw.Store().StoreMessage(protocol.MsgSend{Sender: id, Recipient: w.ownerID(), Body: "OWNER_ONLY_INBOX_CANARY", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	for name, class := range ctoToolClasses {
		if class != ctoRead || name == "charter_guide" {
			continue
		}
		args, _ := json.Marshal(map[string]any{"session_id": id, "node": "missing", "model": "fake/m1"})
		value, err := w.d.runCTOToolRaw(context.Background(), name, args)
		raw, _ := json.Marshal(value)
		output := string(raw)
		if err != nil {
			output += err.Error()
		}
		for _, secret := range []string{marker, "OWNER_ONLY_INBOX_CANARY"} {
			if strings.Contains(output, secret) {
				t.Errorf("%s leaked %s", name, secret)
			}
		}
	}
}

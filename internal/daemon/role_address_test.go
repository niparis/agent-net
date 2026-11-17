package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
)

// agentToolClient serves the agent MCP tools, agent_message_send among them,
// bound to a session on n. The tool sends the same local "send" frame a real
// agent session uses, so a role address is resolved on the way to the gateway.
func agentToolClient(t *testing.T, n *node, nativeID, cwd string) *mcp.ClientSession {
	t.Helper()
	md := mcpserver.NewDaemon(n.sock, map[string]any{"client": "pi", "harness": "pi", "native_id": nativeID, "cwd": cwd, "status": "idle"})
	t.Cleanup(md.Close)
	ctx, cancel := context.WithTimeout(context.Background(), scaled(20*time.Second))
	t.Cleanup(cancel)
	st, ct := mcp.NewInMemoryTransports()
	server, err := mcpserver.New(md.Call, "").Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	client, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func sendTool(t *testing.T, client *mcp.ClientSession, to, body string) messageReceipts {
	t.Helper()
	res, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_send",
		Arguments: map[string]any{"to": to, "body": body}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("agent_message_send to %q: %s", to, res.Content[0].(*mcp.TextContent).Text)
	}
	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out messageReceipts
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("receipt %s: %v", data, err)
	}
	return out
}

// An agent's agent_message_send to "chief" reaches the pinned chief, and the
// receipt names the session that got the message (decision 0171, phase 2).
func TestSendToChiefAddressReachesPinnedChief(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	chief, chiefID := helloPi(t, n, "c1", "/w/chief-one", true)
	if err := g.gw.Store().SetChief(protocol.ChiefSetRequest{SessionID: chiefID}, "owner"); err != nil {
		t.Fatal(err)
	}

	got := sendTool(t, agentToolClient(t, n, "sender", "/w/sender"), "chief", "a question for the chief")
	if len(got.Sent) != 1 {
		t.Fatalf("sent %d receipts: %+v", len(got.Sent), got.Sent)
	}
	receipt := got.Sent[0]
	if receipt.To.ID != chiefID || receipt.To.Name == "" || receipt.MessageID == "" {
		t.Fatalf("receipt names %+v, want the pinned chief %s", receipt.To, chiefID)
	}
	if m := chief.nextPush(); m.ID != receipt.MessageID || m.To != chiefID || m.Body != "a question for the chief" {
		t.Fatalf("chief received %+v, want the message the receipt names", m)
	}
}

// The owner's message to "cto" reaches the registered CTO session. Agents keep
// the reply-only rule, so their request to the same address is still refused.
func TestSendToCTOAddressKeepsTheReplyOnlyRule(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	eventually(t, "owner session", func() bool { return n.d.ownerSessionID() != "" })
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctoID := n.d.ctoSessionID()
	if ctoID == "" {
		t.Fatal("no CTO session registered")
	}

	cli := cliClient(t, n)
	raw, err := cli.call("owner_send", map[string]any{"to": "cto", "body": "from the owner"})
	if err != nil {
		t.Fatalf("owner send to cto: %v", err)
	}
	if m := sendResult(t, raw); m.To != ctoID || m.Body != "from the owner" {
		t.Fatalf("owner message went to %+v, want the CTO session %s", m, ctoID)
	}

	client := agentToolClient(t, n, "sender", "/w/sender")
	res, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_send",
		Arguments: map[string]any{"to": "cto", "body": "hello CTO"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "The CTO only takes replies.") {
		t.Fatalf("agent request to cto: %+v, want the reply-only refusal", res)
	}
}

// With no chief pinned, the tool gives the role address reason, not the
// unknown-name error that sent senders looking for a session.
func TestSendToChiefAddressWithNoChief(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	client := agentToolClient(t, n, "sender", "/w/sender")
	res, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_send",
		Arguments: map[string]any{"to": "chief", "body": "anyone there?"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("send to chief with no chief pinned was accepted: %+v", res)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "no chief of staff is set") || strings.Contains(text, "no session is named") {
		t.Fatalf("send to chief with no chief pinned: %q, want the role address reason", text)
	}
}

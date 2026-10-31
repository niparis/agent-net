package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// callRaw sends an mcp.call frame body as the daemon of node would.
func callRaw(g *Gateway, node, body string) error {
	_, err := g.handleMCP(context.Background(), &nodeConn{id: node}, "mcp.call", []byte(body))
	return err
}

func callAs(g *Gateway, node, session string) error {
	raw, _ := json.Marshal(protocol.MCPCall{Server: "files", Tool: "read", SessionID: session})
	return callRaw(g, node, string(raw))
}

// A call that passes the check goes on to the host. The host of these tests
// has no connection, so a passed check ends at "offline" and a refused one at
// "not assigned".
func passedCheck(err error) bool {
	return err != nil && strings.Contains(err.Error(), "offline")
}
func refused(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not assigned to this project")
}

func TestCallRefusedForAProjectTheServerIsNotAssignedTo(t *testing.T) {
	g, node := scopeGateway(t)
	alpha := scopedSession(t, g, node, "a", "alpha")
	beta := scopedSession(t, g, node, "b", "beta")
	none := scopedSession(t, g, node, "n", "")
	assignMCP(t, g, "files", protocol.ToolAssignment{Scope: "projects", Projects: []string{"alpha"}})

	if err := callAs(g, node, alpha); !passedCheck(err) {
		t.Fatalf("assigned project: %v", err)
	}
	if err := callAs(g, node, beta); !refused(err) {
		t.Fatalf("other project: %v", err)
	}
	// Fail closed: no project, or no session at all, cannot call a
	// project-only server.
	if err := callAs(g, node, none); !refused(err) {
		t.Fatalf("session without project: %v", err)
	}
	if err := callAs(g, node, ""); !refused(err) {
		t.Fatalf("caller without a session: %v", err)
	}
	// A global server stays callable by everyone.
	assignMCP(t, g, "files", protocol.ToolAssignment{Scope: "global"})
	if err := callAs(g, node, none); !passedCheck(err) {
		t.Fatalf("global server: %v", err)
	}
	// Uninstalled: nobody.
	assignMCP(t, g, "files", protocol.ToolAssignment{Scope: "uninstalled"})
	if err := callAs(g, node, alpha); !refused(err) {
		t.Fatalf("uninstalled server: %v", err)
	}
}

func TestStaleCallIsRefusedAfterReassignment(t *testing.T) {
	g, node := scopeGateway(t)
	alpha := scopedSession(t, g, node, "a", "alpha")
	// The client listed the tool while the server was global and cached it.
	if err := callAs(g, node, alpha); !passedCheck(err) {
		t.Fatalf("before: %v", err)
	}
	assignMCP(t, g, "files", protocol.ToolAssignment{Scope: "projects", Projects: []string{"beta"}})
	// The same cached call now fails at the gateway.
	if err := callAs(g, node, alpha); !refused(err) {
		t.Fatalf("stale call was not refused: %v", err)
	}
}

func TestCallIdentityCannotBeSpoofed(t *testing.T) {
	g, node := scopeGateway(t)
	other, _, err := g.store.CreateNode("laptop", "linux")
	if err != nil {
		t.Fatal(err)
	}
	beta := scopedSession(t, g, node, "b", "beta")
	foreign := scopedSession(t, g, other, "x", "alpha")
	assignMCP(t, g, "files", protocol.ToolAssignment{Scope: "projects", Projects: []string{"alpha"}})

	// Claiming a session of another node is refused outright.
	if err := callAs(g, node, foreign); err == nil || passedCheck(err) || !strings.Contains(err.Error(), "not a session of this node") {
		t.Fatalf("foreign session accepted: %v", err)
	}
	// A project in the request is not read: the session's own project counts.
	body := `{"server":"files","tool":"read","session_id":"` + beta + `","project":"alpha","session":"` + foreign + `"}`
	if err := callRaw(g, node, body); !refused(err) {
		t.Fatalf("project field was believed: %v", err)
	}
	// An owner claim from a node with no owner session is not operator
	// authority.
	raw, _ := json.Marshal(protocol.MCPCall{Server: "files", Tool: "read", SessionID: beta, Owner: beta})
	if err := callRaw(g, node, string(raw)); !refused(err) {
		t.Fatalf("fake owner was believed: %v", err)
	}
}

func TestOperatorCallUsesOwnerAuthority(t *testing.T) {
	g, node := scopeGateway(t)
	assignMCP(t, g, "files", protocol.ToolAssignment{Scope: "projects", Projects: []string{"alpha"}})
	info := protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Name: "owner", Status: "idle"}
	owner, err := g.store.UpsertSession(node, info)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.store.db.Exec(`UPDATE sessions SET online=1 WHERE id=?`, owner); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(protocol.MCPCall{Server: "files", Tool: "read", Owner: owner})
	if err := callRaw(g, node, string(raw)); !passedCheck(err) {
		t.Fatalf("owner call: %v", err)
	}
}

// The owner authority is checked against the gateway's own record: the owner
// session must be on the node that sent the frame, and must be online.
func TestOwnerAuthorityNeedsSameNodeAndOnline(t *testing.T) {
	g, node := scopeGateway(t)
	other, _, err := g.store.CreateNode("laptop", "linux")
	if err != nil {
		t.Fatal(err)
	}
	assignMCP(t, g, "files", protocol.ToolAssignment{Scope: "projects", Projects: []string{"alpha"}})
	info := protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Name: "owner", Status: "idle"}
	owner, err := g.store.UpsertSession(other, info)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.store.db.Exec(`UPDATE sessions SET online=1 WHERE id=?`, owner); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(protocol.MCPCall{Server: "files", Tool: "read", Owner: owner})
	// An owner session of another node is not authority for this node.
	if err := callRaw(g, node, string(raw)); passedCheck(err) {
		t.Fatalf("owner of another node was believed: %v", err)
	}
	// The same owner on its own node passes while online.
	if err := callRaw(g, other, string(raw)); !passedCheck(err) {
		t.Fatalf("online owner on its node: %v", err)
	}
	// An offline owner session is not authority.
	if _, err = g.store.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, owner); err != nil {
		t.Fatal(err)
	}
	if err := callRaw(g, other, string(raw)); passedCheck(err) {
		t.Fatalf("offline owner was believed: %v", err)
	}
}

// Import and native removal change what runs on a machine, so the gateway
// checks the owner authority itself: the actor named in the frame is not
// enough, and neither is an owner claim the gateway cannot match to an online
// owner session of the sending node.
func TestImportAndNativeRemoveNeedOwnerAuthorityAtTheGateway(t *testing.T) {
	g, node := scopeGateway(t)
	other, _, err := g.store.CreateNode("laptop", "linux")
	if err != nil {
		t.Fatal(err)
	}
	info := protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Name: "owner", Status: "idle"}
	owner, err := g.store.UpsertSession(node, info)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.store.db.Exec(`UPDATE sessions SET online=1 WHERE id=?`, owner); err != nil {
		t.Fatal(err)
	}
	plain := scopedSession(t, g, node, "p", "alpha")
	for _, method := range []string{"mcp.import", "mcp.native_remove"} {
		frame := func(actor, who string) string {
			return `{"candidate":"no-such-candidate","actor":"` + actor + `","owner":"` + who + `"}`
		}
		call := func(on, actor, who string) error {
			_, err := g.handleMCP(context.Background(), &nodeConn{id: on}, method, []byte(frame(actor, who)))
			return err
		}
		denied := func(err error) bool { return err != nil && strings.Contains(err.Error(), "owner authority required") }
		if err := call(node, "agent", owner); !denied(err) {
			t.Errorf("%s: actor agent: %v", method, err)
		}
		if err := call(node, "", owner); !denied(err) {
			t.Errorf("%s: no actor: %v", method, err)
		}
		if err := call(node, "owner", ""); !denied(err) {
			t.Errorf("%s: no owner session: %v", method, err)
		}
		if err := call(node, "owner", plain); !denied(err) {
			t.Errorf("%s: a plain session as owner: %v", method, err)
		}
		if err := call(other, "owner", owner); !denied(err) {
			t.Errorf("%s: owner of another node: %v", method, err)
		}
		// With the real owner the gate opens, and the request fails later on
		// the candidate, which does not exist.
		if err := call(node, "owner", owner); err == nil || denied(err) {
			t.Errorf("%s: real owner: %v", method, err)
		}
	}
}

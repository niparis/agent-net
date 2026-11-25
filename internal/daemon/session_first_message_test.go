package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A first message the CTO wrote goes out from the CTO's session, so the agent
// reads it as model output and not as its user's request. This covers the
// starts that send it as an agent-net message: Codex, Pi, and Claude with an
// agent identity. These tests run a gateway and the daemon socket; a sandbox
// without unix sockets cannot run them.

const ctoFirstMessage = "Inspect this project and report."

// requireOnlyMessageFrom waits for the first message to a started session and
// checks that it is the only message to it and came from the given session.
func requireOnlyMessageFrom(t *testing.T, g *testGateway, node, sid, text, from string) {
	t.Helper()
	var got []protocol.Message
	eventually(t, "the first message is stored", func() bool {
		all, err := g.gw.Store().Messages(node, protocol.QueryMessages{SessionID: sid, Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		got = got[:0]
		for _, m := range all {
			if m.To == sid {
				got = append(got, m)
			}
		}
		return len(got) != 0
	})
	if len(got) != 1 || got[0].Body != text || got[0].From != from || got[0].OwnerApproved {
		t.Fatalf("messages to %s: %+v; want one %q from %s", sid, got, text, from)
	}
}

func registerCTO(t *testing.T, d *Daemon) string {
	t.Helper()
	if err := d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	return d.ctoSessionID()
}

func TestCTOFirstMessageOnCodexComesFromTheCTO(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	n := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.HarnessPaths = fakeBin(t, "codex")
		c.CodexHome = f.home
		c.CodexInterval = 20 * time.Millisecond
	})
	repo := newRepo(t, "shop")
	n.register(t, repo)
	cto := registerCTO(t, n.d)
	r, err := n.d.sessionStart(context.Background(), protocol.SessionStartRequest{Project: repo, Harness: "codex", CTOProposal: true, FirstMessage: ctoFirstMessage})
	if err != nil || r.Status != "started" || r.SessionID == "" {
		t.Fatalf("start: %+v %v", r, err)
	}
	requireOnlyMessageFrom(t, g, n.d.NodeID(), r.SessionID, ctoFirstMessage, cto)
}

func TestCTOFirstMessageOnPiComesFromTheCTO(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"pi": "/fake/pi"} })
	repo := newRepo(t, "shop")
	n.register(t, repo)
	fakeOpenHerdr(t)
	cto := registerCTO(t, n.d)
	r, err := n.d.sessionStart(context.Background(), protocol.SessionStartRequest{Project: "shop", Harness: "pi", CTOProposal: true, FirstMessage: ctoFirstMessage})
	if err != nil {
		t.Fatal(err)
	}
	lc, sid := helloPi(t, n.node, r.NativeID, repo, true)
	if push := lc.nextPush(); push.Body != ctoFirstMessage || push.From != cto || push.FromHarness == protocol.OwnerHarness {
		t.Fatalf("first message: %+v, want it from the CTO %s", push, cto)
	}
	requireOnlyMessageFrom(t, g, n.d.NodeID(), sid, ctoFirstMessage, cto)
}

// A Claude start with an agent identity sends its first message once the
// session is registered, not on the command line.
func TestCTOFirstMessageOnClaudeWithAnAgentComesFromTheCTO(t *testing.T) {
	g, c, _ := startClaudeFirstMessageNode(t)
	c.register(t, newRepo(t, "chosen"))
	cto := registerCTO(t, c.d)
	r, err := c.d.sessionStart(context.Background(), protocol.SessionStartRequest{Project: "shop", Harness: "claude", AgentID: "chosen", CTOProposal: true, FirstMessage: ctoFirstMessage})
	if err != nil {
		t.Fatal(err)
	}
	started := waitClaudeStart(t, c.d, r.ID)
	requireOnlyMessageFrom(t, g, c.d.NodeID(), started.SessionID, ctoFirstMessage, cto)
}

// A start on another machine: that machine cannot send as the CTO, so the
// CTO's machine keeps the message and sends it once the session is up.
func TestCTOFirstMessageOfARelayedStartComesFromTheCTO(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	a := startWorkerNode(t, g, shortSet, nil)
	b := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"pi": "/fake/pi"} })
	repo := newRepo(t, "remote-repo")
	b.register(t, repo)
	fakeOpenHerdr(t)
	cto := registerCTO(t, a.d)
	r, err := a.d.sessionStart(context.Background(), protocol.SessionStartRequest{Project: "remote-repo", NodeID: b.d.NodeID(), Harness: "pi", CTOProposal: true, FirstMessage: ctoFirstMessage})
	if err != nil {
		t.Fatal(err)
	}
	lc, sid := helloPi(t, b.node, r.NativeID, r.Path, true)
	if push := lc.nextPushOrLog("the first message to the remote Pi session", a.logs); push.Body != ctoFirstMessage || push.From != cto {
		t.Fatalf("first message: %+v, want it from the CTO %s", push, cto)
	}
	requireOnlyMessageFrom(t, g, b.d.NodeID(), sid, ctoFirstMessage, cto)
}

// A machine that runs an older agent-net relays a first message without its
// writer. It is never sent as the owner: the start fails with the reason and
// keeps the text.
func TestFirstMessageWithUnknownWriterIsNotSentAsTheOwner(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"pi": "/fake/pi"} })
	repo := newRepo(t, "shop")
	n.register(t, repo)
	fakeOpenHerdr(t)
	r, err := n.d.sessionStartLocal(context.Background(), protocol.SessionStartRequest{Project: "shop", Harness: "pi", FirstMessage: ctoFirstMessage})
	if err != nil {
		t.Fatal(err)
	}
	_, sid := helloPi(t, n.node, r.NativeID, repo, true)
	var got map[string]any
	eventually(t, "the start fails", func() bool {
		mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &got)
		return got["status"] == "failed"
	})
	if errText, _ := got["error"].(string); !strings.Contains(errText, "does not know who wrote it") || got["unsent_first_message"] != ctoFirstMessage {
		t.Fatalf("failed start: %+v", got)
	}
	all, err := g.gw.Store().Messages(n.d.NodeID(), protocol.QueryMessages{SessionID: sid, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range all {
		if m.To == sid {
			t.Fatalf("a message was sent to the session: %+v", m)
		}
	}
}

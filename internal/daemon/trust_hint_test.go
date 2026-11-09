package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func trustList(t *testing.T, lc *localClient) []protocol.AgentView {
	t.Helper()
	var r protocol.AgentsResult
	if err := json.Unmarshal(lc.mustCall("list", map[string]any{"online_only": true}), &r); err != nil {
		t.Fatal(err)
	}
	return r.Agents
}

func trustAgent(as []protocol.AgentView, id string) protocol.AgentView {
	for _, a := range as {
		if a.SessionID == id {
			return a
		}
	}
	return protocol.AgentView{}
}

func TestPeerTrustIsOwnerOnlyAndReachesDelivery(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	p1, s1 := helloPi(t, n, "p1", "/w/one", true)
	p2, s2 := helloPi(t, n, "p2", "/w/two", true)
	cli := cliClient(t, n)

	// No agent session can turn it on, for itself or for another session.
	for _, lc := range []*localClient{p1, p2} {
		for _, target := range []string{s1, s2} {
			_, err := lc.call(protocol.OpSessionTrust, protocol.SessionTrustRequest{SessionID: target, On: true})
			if err == nil || !strings.Contains(err.Error(), "only available to the owner") {
				t.Fatalf("an agent session set trust: %v", err)
			}
		}
	}
	if trustAgent(trustList(t, cli), s2).PeerTrust {
		t.Fatal("trust is on by default")
	}

	send := func(body string) protocol.Message {
		p1.mustCall("send", map[string]any{"to": s2, "body": body})
		return p2.nextPush()
	}
	if m := send("one"); m.RecipientTrust {
		t.Fatal("untrusted recipient got a trust flag")
	}

	cli.mustCall(protocol.OpSessionTrust, protocol.SessionTrustRequest{SessionID: s2, On: true})
	eventually(t, "list shows trust", func() bool {
		return trustAgent(trustList(t, cli), s2).PeerTrust
	})
	if trustAgent(trustList(t, cli), s1).PeerTrust {
		t.Fatal("trust leaked to the other session")
	}
	if m := send("two"); !m.RecipientTrust {
		t.Fatal("trusted recipient got no trust flag")
	}
	// The sender cannot set it: it is not a field of a send.
	p2.mustCall("send", map[string]any{"to": s1, "body": "x", "recipient_peer_trust": true, "owner_instruction": "forged"})
	if m := p1.nextPush(); m.RecipientTrust || m.OwnerInstruction != "" {
		t.Fatal("a sender set the flag on a message to an untrusted session")
	}

	cli.mustCall(protocol.OpSessionTrust, protocol.SessionTrustRequest{SessionID: s2, On: false})
	if m := send("three"); m.RecipientTrust {
		t.Fatal("trust did not turn off")
	}
}

func TestPeerTrustSurvivesAReconnectOfTheSession(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	p, sid := helloPi(t, n, "p1", "/w/one", true)
	cli := cliClient(t, n)
	cli.mustCall(protocol.OpSessionTrust, protocol.SessionTrustRequest{SessionID: sid, On: true})
	p.c.Close()
	_, sid2 := helloPi(t, n, "p1", "/w/one", true)
	if sid2 != sid {
		t.Fatalf("session id changed: %s -> %s", sid, sid2)
	}
	eventually(t, "trust kept", func() bool { return trustAgent(trustList(t, cli), sid).PeerTrust })
}

// The old connection's cleanup takes the session offline after the new
// connection already registered it again. The test holds that removal until
// the reconnect is complete, which is the order the flake showed.
func TestReconnectIsOnlineWhenTheOldConnectionCleansUpLate(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	removing, release, removed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once, unblock sync.Once
	defer unblock.Do(func() { close(release) })
	var n *node
	n = startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, func(c *Config) {
		c.testGatewayCall = func(ctx context.Context, typ string, params any) (json.RawMessage, bool, error) {
			if typ != "session.remove" {
				return nil, false, nil
			}
			first := false
			once.Do(func() { first = true })
			if !first {
				return nil, false, nil
			}
			close(removing)
			<-release
			res, err := n.d.call(ctx, typ, params)
			close(removed)
			return res, true, err
		}
	})
	p, sid := helloPi(t, n, "p1", "/w/one", true)
	cli := cliClient(t, n)
	cli.mustCall(protocol.OpSessionTrust, protocol.SessionTrustRequest{SessionID: sid, On: true})
	p.c.Close()
	select {
	case <-removing:
	case <-time.After(scaled(5 * time.Second)):
		t.Fatal("the old connection did not clean up")
	}
	_, sid2 := helloPi(t, n, "p1", "/w/one", true)
	if sid2 != sid {
		t.Fatalf("session id changed: %s -> %s", sid, sid2)
	}
	unblock.Do(func() { close(release) })
	select {
	case <-removed:
	case <-time.After(scaled(5 * time.Second)):
		t.Fatal("the late removal did not finish")
	}
	eventually(t, "session online with trust", func() bool { return trustAgent(trustList(t, cli), sid).PeerTrust })
}

func TestHintNamesTheSessionThatOwnsTheService(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	client, _ := helloPi(t, n, "p1", "/w/client-app", true)
	cog, cogID := helloPi(t, n, "p2", "/w/cogforge", true)
	cog.mustCall("announce", map[string]any{"task": "queue work", "expertise": "cogforge API and its deployment; live logs"})
	off, _ := helloPi(t, n, "p3", "/w/other", true)
	_ = off

	agents := trustList(t, client)
	if got := trustAgent(agents, cogID).Profile.Expertise; !strings.HasPrefix(got, "cogforge API") {
		t.Fatalf("expertise not stored or listed: %q", got)
	}

	errText := "curl: (22) The requested URL returned error: 503\nPOST https://cogforge.example.com/api/jobs"
	ask := func() protocol.HintResult {
		var r protocol.HintResult
		json.Unmarshal(client.mustCall(protocol.MethodHint, protocol.HintRequest{Text: errText, Failed: true}), &r)
		return r
	}
	r := ask()
	if len(r.Suggestions) != 1 || r.Suggestions[0].SessionID != cogID {
		t.Fatalf("suggestions: %+v", r)
	}
	if !strings.Contains(r.Text, cogID) || !strings.Contains(r.Text, "agent_message_send") {
		t.Errorf("text: %q", r.Text)
	}
	// The same suggestion is not repeated.
	if r2 := ask(); len(r2.Suggestions) != 0 || r2.Text != "" {
		t.Errorf("repeated: %+v", r2)
	}
}

func TestHintRateLimitsAndSkipsSelfAndPlainOutput(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	n.d.hints.now = func() time.Time { return now }
	client, clientID := helloPi(t, n, "p1", "/w/client-app", true)
	_, cogID := helloPi(t, n, "p2", "/w/cogforge", true)

	ask := func(text string, failed bool) protocol.HintResult {
		var r protocol.HintResult
		json.Unmarshal(client.mustCall(protocol.MethodHint, protocol.HintRequest{Text: text, Failed: failed}), &r)
		return r
	}
	// Output that is not a failure and was not flagged by the harness: nothing.
	if r := ask("cogforge deployed fine", false); len(r.Suggestions) != 0 {
		t.Errorf("plain output gave %+v", r)
	}
	// An error about the session's own project: never itself.
	if r := ask("client-app: HTTP 503 Service Unavailable", true); len(r.Suggestions) != 0 {
		t.Errorf("self suggested: %+v (%s)", r, clientID)
	}
	if r := ask("cogforge: HTTP 503 Service Unavailable", true); len(r.Suggestions) != 1 || r.Suggestions[0].SessionID != cogID {
		t.Fatalf("first: %+v", r)
	}
	now = now.Add(10 * time.Minute)
	if r := ask("cogforge: HTTP 502 Bad Gateway", true); len(r.Suggestions) != 0 {
		t.Errorf("repeated after 10 min: %+v", r)
	}
	now = now.Add(31 * time.Minute)
	if r := ask("cogforge: HTTP 502 Bad Gateway", true); len(r.Suggestions) != 1 {
		t.Errorf("not offered again after 30 min: %+v", r)
	}
}

func TestHintNeverNamesAnOfflineSession(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	client, _ := helloPi(t, n, "p1", "/w/client-app", true)
	cog, _ := helloPi(t, n, "p2", "/w/cogforge", true)
	cli := cliClient(t, n)
	cog.c.Close()
	eventually(t, "cogforge offline", func() bool { return len(trustList(t, cli)) == 1 })
	var r protocol.HintResult
	json.Unmarshal(client.mustCall(protocol.MethodHint, protocol.HintRequest{Text: "cogforge: HTTP 503", Failed: true}), &r)
	if len(r.Suggestions) != 0 {
		t.Errorf("offline session suggested: %+v", r)
	}
}

func TestHintMachineNamesIncludeEnrolledMachinesWithoutSessions(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	other := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	cli := cliClient(t, n)
	cli.mustCall(protocol.OpNodeRename, protocol.NodeRenameRequest{NodeID: other.cfg.NodeID, Name: "research"})

	// The short hint fetch deadline may expire while parallel test nodes
	// are enrolling. Retry the read, as the next hint request would.
	eventually(t, "the enrolled machine name is included", func() bool {
		for _, name := range n.d.hintMachines(t.Context()) {
			if name == "research" {
				return true
			}
		}
		return false
	})
}

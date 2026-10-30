package daemon

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func fakeSession(native, name string) protocol.SessionInfo {
	return protocol.SessionInfo{Harness: "fake", NativeID: native, PID: 1, Cwd: "/work/" + name, Name: name,
		Status: "idle", Delivery: "native-claude"}
}

func sendResult(t *testing.T, raw json.RawMessage) protocol.Message {
	t.Helper()
	var r struct {
		Message protocol.Message `json:"message"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || r.Message.ID == "" {
		t.Fatalf("no message in result %s", raw)
	}
	return r.Message
}

// A fake-adapter session on node A and a Pi plugin client on node B exchange
// a request and a reply through a real gateway over TLS.
func TestSendDeliverReply(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	a := startNode(t, g, nodeOpts{adapter: fa})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "alice has a session id", func() bool { return fa.host.SessionID("fake", "na") != "" })
	aliceID := fa.host.SessionID("fake", "na")
	_ = a

	pi, bobID := helloPi(t, b, "nb", "/work/bob", true)

	// Pi sends by agent name: alice is the only online agent with that name.
	m := sendResult(t, pi.mustCall("send", map[string]any{"to": "alice", "body": "ping"}))
	if m.To != aliceID || m.From != bobID {
		t.Fatalf("wrong route: %+v", m)
	}
	eventually(t, "adapter got the message", func() bool { return len(fa.delivered()) == 1 })
	got := fa.delivered()[0]
	if got.Body != "ping" || got.FromName != "bob" || got.FromHarness != "pi" || got.ID != m.ID {
		t.Fatalf("unexpected delivery: %+v", got)
	}
	eventually(t, "state delivered", func() bool { return messageState(t, pi, m.ID) == protocol.StateDelivered })

	// The fake session replies the way the Claude adapter does.
	if err := fa.host.Send(context.Background(), protocol.MsgSend{Sender: aliceID, Recipient: bobID,
		Type: protocol.TypeReply, Body: "pong", ParentID: m.ID}); err != nil {
		t.Fatal(err)
	}
	reply := pi.nextPush()
	if reply.Body != "pong" || reply.ParentID != m.ID || reply.ConversationID != m.ConversationID || reply.Hop != 1 {
		t.Fatalf("unexpected reply: %+v", reply)
	}
	eventually(t, "parent replied", func() bool { return messageState(t, pi, m.ID) == protocol.StateReplied })
	eventually(t, "reply delivered", func() bool { return messageState(t, pi, reply.ID) == protocol.StateDelivered })

	// Pi can also reply through the reply method.
	m2 := sendResult(t, pi.mustCall("send", map[string]any{"to": aliceID, "body": "second"}))
	eventually(t, "second delivered", func() bool { return len(fa.delivered()) == 2 })
	if m2.ConversationID == m.ConversationID {
		t.Fatal("a new send must start a new conversation")
	}
}

func TestNameResolutionAmbiguous(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	startNode(t, g, nodeOpts{adapter: fa})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	fa.setSessions(fakeSession("n1", "twin"), fakeSession("n2", "twin"))
	eventually(t, "both sessions known", func() bool {
		return fa.host.SessionID("fake", "n1") != "" && fa.host.SessionID("fake", "n2") != ""
	})
	pi, _ := helloPi(t, b, "nb", "/work/bob", true)
	_, err := pi.call("send", map[string]any{"to": "twin", "body": "x"})
	if err == nil || !strings.Contains(err.Error(), "2 online agents") || !strings.Contains(err.Error(), "s_") {
		t.Fatalf("want an error listing both matches, got %v", err)
	}
	if _, err := pi.call("send", map[string]any{"to": "nobody", "body": "x"}); err == nil {
		t.Fatal("unknown name accepted")
	}
}

func TestRecipientOfflineThenBack(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	startNode(t, g, nodeOpts{adapter: fa})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "alice id", func() bool { return fa.host.SessionID("fake", "na") != "" })
	aliceID := fa.host.SessionID("fake", "na")

	pi, bobID := helloPi(t, b, "nb", "/work/bob", true)
	pi.c.Close()
	cli := dialLocal(t, b.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	eventually(t, "bob offline", func() bool {
		raw, err := cli.call("get", map[string]any{"session_id": bobID})
		return err == nil && strings.Contains(string(raw), `"online":false`)
	})

	if err := fa.host.Send(context.Background(), protocol.MsgSend{Sender: aliceID, Recipient: bobID, Body: "while away"}); err != nil {
		t.Fatal(err)
	}
	var id string
	eventually(t, "message stored", func() bool {
		raw, err := cli.call("messages", map[string]any{"session_id": bobID})
		if err != nil {
			return false
		}
		var r struct {
			Messages []protocol.Message `json:"messages"`
		}
		json.Unmarshal(raw, &r)
		if len(r.Messages) == 1 && r.Messages[0].State == protocol.StateStored {
			id = r.Messages[0].ID
			return true
		}
		return false
	})

	pi2, bobID2 := helloPi(t, b, "nb", "/work/bob", true)
	if bobID2 != bobID {
		t.Fatalf("session id changed: %s -> %s", bobID, bobID2)
	}
	if got := pi2.nextPush(); got.ID != id || got.Body != "while away" {
		t.Fatalf("unexpected push %+v", got)
	}
	eventually(t, "delivered after return", func() bool { return messageState(t, cli, id) == protocol.StateDelivered })
}

func TestGatewayRestartLosesNothing(t *testing.T) {
	dir := t.TempDir()
	g := startGateway(t, dir, "")
	fa := &fakeAdapter{harness: "fake"}
	startNode(t, g, nodeOpts{adapter: fa})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "alice id", func() bool { return fa.host.SessionID("fake", "na") != "" })
	aliceID := fa.host.SessionID("fake", "na")
	pi, bobID := helloPi(t, b, "nb", "/work/bob", true)
	pi.c.Close()
	cli := dialLocal(t, b.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	eventually(t, "bob offline", func() bool {
		raw, err := cli.call("get", map[string]any{"session_id": bobID})
		return err == nil && strings.Contains(string(raw), `"online":false`)
	})

	// One message is accepted before the restart, one is made while the gateway is down.
	if err := fa.host.Send(context.Background(), protocol.MsgSend{Sender: aliceID, Recipient: bobID, Body: "one", IdempotencyKey: "k1"}); err != nil {
		t.Fatal(err)
	}
	addr := g.addr
	g.stop()
	eventually(t, "node A sees the gateway go", func() bool { return !fa.host.(*Daemon).Connected() })
	if err := fa.host.Send(context.Background(), protocol.MsgSend{Sender: aliceID, Recipient: bobID, Body: "two", IdempotencyKey: "k2"}); err != nil {
		t.Fatal(err)
	}

	g2 := startGateway(t, dir, addr)
	eventually(t, "node A reconnected and flushed", func() bool {
		d := fa.host.(*Daemon)
		return d.Connected() && d.st.outboxLen() == 0
	})
	eventually(t, "node B reconnected", b.d.Connected)

	pi2, _ := helloPi(t, b, "nb", "/work/bob", true)
	first, second := pi2.nextPush(), pi2.nextPush()
	if first.Body != "one" || second.Body != "two" {
		t.Fatalf("order or content lost: %q then %q", first.Body, second.Body)
	}
	eventually(t, "both delivered", func() bool {
		return messageState(t, cli, first.ID) == protocol.StateDelivered && messageState(t, cli, second.ID) == protocol.StateDelivered
	})
	_ = g2
}

func TestDuplicateDeliverOnce(t *testing.T) {
	fa := &fakeAdapter{harness: "fake"}
	d, err := New(Config{StateDir: t.TempDir(), SocketPath: filepath.Join(shortDir(t), "d.sock"), Adapters: []Adapter{fa}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	fa.host = d
	d.sessions[key("fake", "na")] = &localSession{info: fakeSession("na", "alice"), sessionID: "s_a"}
	m := protocol.Message{ID: "m_1", To: "s_a", From: "s_b", Body: "hi"}
	d.onDeliver(context.Background(), m)
	d.onDeliver(context.Background(), m)
	if n := len(fa.delivered()); n != 1 {
		t.Fatalf("delivered %d times, want 1", n)
	}
	if !d.st.isDelivered("m_1") {
		t.Fatal("not recorded as delivered")
	}
}

func TestUnconfirmedPluginIsNotRetried(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	startNode(t, g, nodeOpts{adapter: fa})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "alice id", func() bool { return fa.host.SessionID("fake", "na") != "" })
	pi, bobID := helloPi(t, b, "nb", "/work/bob", false) // never confirms
	if err := fa.host.Send(context.Background(), protocol.MsgSend{Sender: fa.host.SessionID("fake", "na"), Recipient: bobID, Body: "x"}); err != nil {
		t.Fatal(err)
	}
	m := pi.nextPush()
	cli := dialLocal(t, b.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	eventually(t, "unconfirmed counts as delivered", func() bool { return messageState(t, cli, m.ID) == protocol.StateDelivered })
	select {
	case again := <-pi.Pushes:
		t.Fatalf("pushed again: %+v", again)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestFailedDeliveryIsReported(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake", fail: "socket missing"}
	startNode(t, g, nodeOpts{adapter: fa})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "alice id", func() bool { return fa.host.SessionID("fake", "na") != "" })
	pi, _ := helloPi(t, b, "nb", "/work/bob", true)
	m := sendResult(t, pi.mustCall("send", map[string]any{"to": fa.host.SessionID("fake", "na"), "body": "x"}))
	eventually(t, "state failed", func() bool { return messageState(t, pi, m.ID) == protocol.StateFailed })
}

// ---- local socket ----

func TestPeerCheckAndBoundSender(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	sessDir := t.TempDir()
	// The test process stands in for `agent-net mcp`; its parent stands in for Claude Code.
	file := filepath.Join(sessDir, strconv.Itoa(os.Getppid())+".json")
	if err := os.WriteFile(file, []byte(`{"pid":1,"sessionId":"sess-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fa := &fakeAdapter{harness: "claude"}
	var logs strings.Builder
	var logMu sync.Mutex
	logger := log.New(writerFunc(func(p []byte) (int, error) { logMu.Lock(); defer logMu.Unlock(); return logs.Write(p) }), "", 0)
	a := startNode(t, g, nodeOpts{adapter: fa, claudeDir: sessDir, cmdline: "node something", logger: logger})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	fa.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "sess-1", Name: "claude-one", Status: "idle"})
	eventually(t, "claude session id", func() bool { return fa.host.SessionID("claude", "sess-1") != "" })
	claudeID := fa.host.SessionID("claude", "sess-1")
	pi, bobID := helloPi(t, b, "nb", "/work/bob", true)

	// A wrong native_id is refused.
	bad := dialLocal(t, a.sock)
	if _, err := bad.call("hello", map[string]any{"client": "mcp", "harness": "claude", "native_id": "someone-else"}); err == nil ||
		!strings.Contains(err.Error(), "peer check failed") {
		t.Fatalf("wrong native_id: got %v", err)
	}
	// So is a Pi claim from a process that is not Pi.
	notPi := dialLocal(t, a.sock)
	if _, err := notPi.call("hello", map[string]any{"client": "pi", "harness": "pi", "native_id": "x", "cwd": "/"}); err == nil ||
		!strings.Contains(err.Error(), "not Pi") {
		t.Fatalf("non-pi process: got %v", err)
	}
	// Nothing works before hello.
	raw := dialLocal(t, a.sock)
	if _, err := raw.call("send", map[string]any{"to": bobID, "body": "x"}); err == nil {
		t.Fatal("call before hello accepted")
	}

	good := dialLocal(t, a.sock)
	res := good.mustCall("hello", map[string]any{"client": "mcp", "harness": "claude", "native_id": "sess-1", "version": "v0.0.7"})
	// The daemon log says which agent-net version each Claude session runs, so an
	// upgrade that did not reach a session can be seen.
	logMu.Lock()
	gotLog := logs.String()
	logMu.Unlock()
	if !strings.Contains(gotLog, "mcp hello: claude session sess-1") || !strings.Contains(gotLog, "runs agent-net v0.0.7") {
		t.Fatalf("daemon log lacks the mcp hello line:\n%s", gotLog)
	}
	if !strings.Contains(string(res), claudeID) {
		t.Fatalf("hello result %s lacks the session id", res)
	}
	// The MCP process uses the daemon's version to decide to switch to a newer binary.
	if !strings.Contains(string(res), `"version"`) {
		t.Fatalf("hello result %s lacks the daemon version", res)
	}
	// Params cannot name another sender.
	m := sendResult(t, good.mustCall("send", map[string]any{"to": bobID, "body": "hello", "message_type": "notification",
		"sender_session_id": bobID, "from": bobID, "session_id": bobID}))
	if m.From != claudeID {
		t.Fatalf("sender is %s, want the bound session %s", m.From, claudeID)
	}
	if push := pi.nextPush(); push.From != claudeID {
		t.Fatalf("pushed sender %s", push.From)
	}

	// The CLI is read-only.
	cli := dialLocal(t, a.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	if _, err := cli.call("send", map[string]any{"to": bobID, "body": "x"}); err == nil {
		t.Fatal("cli could send")
	}
	if _, err := cli.call("list", map[string]any{"online_only": true}); err != nil {
		t.Fatalf("cli list: %v", err)
	}
}

func TestAnnounceAndList(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	pi, bobID := helloPi(t, b, "nb", "/work/bob", true)
	pi.mustCall("announce", map[string]any{"name": "builder", "function": "builds things", "project": "p1",
		"capabilities": []string{"go", "docker"}, "task": "compiling"})
	raw := pi.mustCall("list", map[string]any{"capability": "go", "online_only": true})
	var r struct {
		Agents []protocol.AgentView `json:"agents"`
	}
	json.Unmarshal(raw, &r)
	if len(r.Agents) != 1 || r.Agents[0].SessionID != bobID || r.Agents[0].Profile.Name != "builder" ||
		r.Agents[0].Harness != "pi" || r.Agents[0].Hostname == "" {
		t.Fatalf("unexpected list: %s", raw)
	}
	if n := len(listAgents(t, pi, map[string]any{"capability": "rust"})); n != 0 {
		t.Fatalf("capability filter returned %d agents", n)
	}
}

func listAgents(t *testing.T, lc *localClient, p map[string]any) []protocol.AgentView {
	var r struct {
		Agents []protocol.AgentView `json:"agents"`
	}
	json.Unmarshal(lc.mustCall("list", p), &r)
	return r.Agents
}

// ---- enrollment ----

func TestJoinRefusesWrongFingerprint(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	code, _ := g.gw.Store().CreateEnrollCode(10 * time.Minute)
	bad := strings.Repeat("ab", 32)
	cfgDir := t.TempDir()
	_, err := Join(context.Background(), "https://"+g.addr+"#"+code+"#"+bad, cfgDir)
	if err == nil || !strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Fatalf("want fingerprint mismatch, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "node.json")); err == nil {
		t.Fatal("node.json written after a failed join")
	}
	// The refused attempt must not have used up the code.
	if _, err := Join(context.Background(), "https://"+g.addr+"#"+code+"#"+g.gw.Fingerprint(), cfgDir); err != nil {
		t.Fatalf("good join after bad one: %v", err)
	}
	fi, err := os.Stat(filepath.Join(cfgDir, "node.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("node.json missing or mode %v (%v)", fi, err)
	}
	// Same code again is refused.
	if _, err := Join(context.Background(), "https://"+g.addr+"#"+code+"#"+g.gw.Fingerprint(), t.TempDir()); err == nil {
		t.Fatal("code worked twice")
	}
}

func TestJoinCodeExpires(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	code, _ := g.gw.Store().CreateEnrollCode(time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	if _, err := Join(context.Background(), "https://"+g.addr+"#"+code+"#"+g.gw.Fingerprint(), t.TempDir()); err == nil {
		t.Fatal("expired code accepted")
	}
}

func TestBadTokenRefused(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	cfgDir := t.TempDir()
	nc, err := Join(context.Background(), g.joinString(), cfgDir)
	if err != nil {
		t.Fatal(err)
	}
	_, ca, _ := LoadNode(cfgDir)
	d, err := New(Config{GatewayURL: nc.GatewayURL, NodeID: nc.NodeID, Token: "wrong", CAPEM: ca,
		StateDir: t.TempDir(), SocketPath: filepath.Join(shortDir(t), "d.sock"), BackoffMin: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	time.Sleep(200 * time.Millisecond)
	if d.Connected() {
		t.Fatal("connected with a bad token")
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestOutcomeOverruleEndToEnd(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	startNode(t, g, nodeOpts{adapter: fa})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "alice registered", func() bool { return fa.host.SessionID("fake", "na") != "" })
	pi, bobID := helloPi(t, b, "nb", "/work/bob", true)
	if err := fa.host.Send(context.Background(), protocol.MsgSend{Sender: fa.host.SessionID("fake", "na"), Recipient: bobID, Body: "please do this\ncontext"}); err != nil {
		t.Fatal(err)
	}
	req := pi.nextPush()
	rep := sendResult(t, pi.mustCall("reply", map[string]any{"message_id": req.ID, "body": "I cannot decide", "outcome": "escalate", "reason": "unclear", "recommendation": "do it"}))
	cli := cliClient(t, b)
	var entries struct {
		Entries []protocol.OutcomeInboxEntry `json:"entries"`
	}
	raw := cli.mustCall("outcome_inbox", nil)
	if err := json.Unmarshal(raw, &entries); err != nil || len(entries.Entries) != 1 || entries.Entries[0].ReplyID != rep.ID {
		t.Fatalf("inbox %s %v", raw, err)
	}
	cli.mustCall("outcome_overrule", map[string]any{"reply_id": rep.ID, "action": "overrule"})
	msg := pi.nextPush()
	if !msg.Overruled || msg.Body != req.Body || msg.FromHarness != protocol.OwnerHarness {
		t.Fatalf("overrule: %+v", msg)
	}
}

package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// claudePeer is a fake Claude Code session on a node with the real Claude
// adapter: a session file and an inbox socket that collects frames.
type claudePeer struct {
	t                 *testing.T
	sessions, replies string
	inbox             string
	frames            chan string
}

func newClaudePeer(t *testing.T) *claudePeer {
	t.Helper()
	dir := shortDir(t)
	p := &claudePeer{t: t, sessions: filepath.Join(dir, "sessions"), replies: filepath.Join(dir, "r"),
		inbox: filepath.Join(dir, "claude.sock"), frames: make(chan string, 8)}
	if err := os.MkdirAll(p.sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", p.inbox)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(c).ReadString('\n')
			c.Close()
			if strings.TrimSpace(line) != "" { // the adapter's health check writes nothing
				p.frames <- line
			}
		}
	}()
	body := fmt.Sprintf(`{"pid":%d,"sessionId":"claude-peer","cwd":"/work/claude","name":"claude peer","status":"idle","messagingSocketPath":%q}`,
		os.Getpid(), p.inbox)
	if err := os.WriteFile(filepath.Join(p.sessions, fmt.Sprintf("%d.json", os.Getpid())), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p *claudePeer) adapter() Adapter { return TestClaudeAdapterFactory(p.sessions, p.replies) }

// nextFrame returns the reply address of the next frame in the inbox.
func (p *claudePeer) nextFrame() string {
	p.t.Helper()
	select {
	case line := <-p.frames:
		var f struct {
			From string `json:"from"`
		}
		if err := json.Unmarshal([]byte(line), &f); err != nil || !strings.HasPrefix(f.From, "uds:") {
			p.t.Fatalf("frame without a reply address: %s", line)
		}
		return strings.TrimPrefix(f.From, "uds:")
	case <-time.After(5 * time.Second):
		p.t.Fatal("no frame reached the Claude inbox")
	}
	return ""
}

// answer writes what Claude's SendMessage tool writes to a reply address.
func (p *claudePeer) answer(addr, id, body string) {
	p.t.Helper()
	c, err := net.Dial("unix", addr)
	if err != nil {
		p.t.Fatal(err)
	}
	defer c.Close()
	line, _ := json.Marshal(map[string]any{"type": "user", "msgV": 1, "msg_id": id, "from": "uds:" + p.inbox,
		"message": map[string]any{"role": "user", "content": body}})
	if _, err := c.Write(append(line, '\n')); err != nil {
		p.t.Fatal(err)
	}
}

func startClaudePeerNode(t *testing.T, g *testGateway, p *claudePeer) (*node, string) {
	t.Helper()
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) {
		c.Adapters = []Adapter{p.adapter()}
	}})
	var id string
	eventually(t, "Claude session registered", func() bool { id = n.d.SessionID("claude", "claude-peer"); return id != "" })
	return n, id
}

// A Claude answer written after a daemon restart still answers the request
// delivered before it: the match is in daemon.db and the reply address is
// listened on again (W4.4).
func TestClaudeReplyAfterDaemonRestartAnswersItsRequest(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	p := newClaudePeer(t)
	a, claudeID := startClaudePeerNode(t, g, p)
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	pi, _ := helloPi(t, b, "nb", "/work/bob", true)

	req := sendResult(t, pi.mustCall("send", map[string]any{"to": claudeID, "body": "please check"}))
	addr := p.nextFrame()

	a.d.Stop()
	eventually(t, "reply socket closed with the daemon", func() bool { _, err := os.Stat(addr); return os.IsNotExist(err) })
	a.cfg.Adapters = []Adapter{p.adapter()} // a new process has no memory of the delivery
	a.restart()
	eventually(t, "Claude session registered again", func() bool { return a.d.SessionID("claude", "claude-peer") == claudeID })
	eventually(t, "reply socket open again", func() bool { _, err := os.Stat(addr); return err == nil })

	p.answer(addr, "after-restart", "checked")
	reply := pi.nextPush()
	if reply.Body != "checked" || reply.ParentID != req.ID || reply.Type != protocol.TypeReply || reply.ConversationID != req.ConversationID {
		t.Fatalf("answer after restart = %+v, want a reply to %s", reply, req.ID)
	}
}

// Two open requests from one sender in one conversation, and an answer that
// names neither: the gateway stores it in that conversation with no parent,
// and both requests count as answered.
func TestClaudeUnsureReplyIsThreadedToTheConversation(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	p := newClaudePeer(t)
	_, claudeID := startClaudePeerNode(t, g, p)
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	pi, bobID := helloPi(t, b, "nb", "/work/bob", true)

	first := sendResult(t, pi.mustCall("send", map[string]any{"to": claudeID, "body": "first"}))
	addr := p.nextFrame()
	p.answer(addr, "answer-1", "first done")
	firstReply := pi.nextPush()
	if firstReply.ParentID != first.ID {
		t.Fatalf("first answer = %+v, want a reply to %s", firstReply, first.ID)
	}

	var follow []protocol.Message
	for _, body := range []string{"also this", "and that"} {
		raw, err := b.d.send(context.Background(), protocol.MsgSend{Sender: bobID, Recipient: claudeID, Type: protocol.TypeRequest,
			Body: body, ParentID: firstReply.ID})
		if err != nil {
			t.Fatal(err)
		}
		bs, _ := json.Marshal(raw)
		follow = append(follow, sendResult(t, bs))
		if got := p.nextFrame(); got != addr {
			t.Fatalf("a follow-up in the same conversation has reply address %s, want %s", got, addr)
		}
	}
	p.answer(addr, "answer-2", "both done")
	reply := pi.nextPush()
	if reply.Body != "both done" || reply.ParentID != "" || reply.ConversationID != first.ConversationID || reply.Type != protocol.TypeReply {
		t.Fatalf("unsure answer = %+v, want a reply in %s with no parent", reply, first.ConversationID)
	}
	for _, m := range follow {
		eventually(t, "follow-up "+m.ID+" answered", func() bool { return messageState(t, pi, m.ID) == protocol.StateReplied })
	}
}

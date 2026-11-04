package daemon

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

type ctoLogBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *ctoLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}
func (b *ctoLogBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }

func TestCTOWakeAfterCrashAndRestart(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	logs := &ctoLogBuffer{}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory, interval: time.Hour, logger: log.New(logs, "", 0), cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	cli := cliClient(t, n)
	peer, _ := helloPi(t, n, "wake-peer", "/w/peer", true)
	cli.mustCall("cto_send", map[string]string{"text": "initial"})
	id := n.d.ctoSessionID()
	conversation, err := n.d.ctoSession()
	if err != nil || conversation == "" {
		t.Fatalf("conversation: %q %v", conversation, err)
	}
	rec := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	n.d.cto.mu.Lock()
	process := n.d.cto.cmd.Process
	n.d.cto.mu.Unlock()
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "CTO crash logged", func() bool { return strings.Contains(logs.String(), "cto.crash harness=opencode") })
	eventually(t, "CTO dormant and reachable", func() bool { a, _, _ := g.gw.Store().Agent(id); return a.Online && a.Status == "dormant" })
	peer.mustCall("send", map[string]string{"to": id, "body": "wake from peer"})
	path := filepath.Join(rec.Env["XDG_DATA_HOME"], "fake-session-"+conversation+".json")
	eventually(t, "peer message delivered after crash", func() bool {
		b, e := os.ReadFile(path)
		return e == nil && strings.Contains(string(b), "wake from peer")
	})
	eventually(t, "CTO awake", func() bool { a, _, _ := g.gw.Store().Agent(id); return a.Online && a.Status == "idle" })
	if err := g.gw.Store().SetNodeOffline(n.cfg.NodeID); err != nil {
		t.Fatal(err)
	}
	peer.mustCall("send", map[string]string{"to": id, "body": "queued while offline"})
	n.restart()
	eventually(t, "CTO reachable after restart", func() bool { a, _, _ := g.gw.Store().Agent(id); return a.Online })
	eventually(t, "queued message delivered after restart", func() bool {
		b, e := os.ReadFile(path)
		return e == nil && strings.Contains(string(b), "queued while offline")
	})
	peer, _ = helloPi(t, n, "wake-peer-again", "/w/peer", true)
	peer.mustCall("send", map[string]string{"to": id, "body": "wake after restart"})
	eventually(t, "message delivered after restart", func() bool {
		b, e := os.ReadFile(path)
		return e == nil && strings.Contains(string(b), "wake after restart")
	})
	got, err := n.d.ctoSession()
	if err != nil || got != conversation {
		t.Fatalf("conversation changed: %q %v", got, err)
	}
	lines := logs.String()
	for _, want := range []struct {
		text  string
		count int
	}{
		{"cto.start harness=opencode", 3}, {"cto.stop harness=opencode reason=daemon_shutdown", 1},
		{"cto.stop harness=opencode reason=crash", 1}, {"cto.wake trigger=agent harness=opencode", 2},
		{"cto.wake trigger=owner harness=opencode", 1}, {"cto.crash harness=opencode", 1},
	} {
		if count := strings.Count(lines, want.text); count != want.count {
			t.Errorf("%q occurred %d times, want %d:\n%s", want.text, count, want.count, lines)
		}
	}
}

func TestCTOClaudeWakeAfterHarnessSwitch(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	sessions, _ := useFakeClaude(t)
	logs := &ctoLogBuffer{}
	adapter := &recordingCTOClaude{fsClaudeAdapter: &fsClaudeAdapter{dir: sessions}, received: make(chan string, 10)}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory, interval: time.Hour, logger: log.New(logs, "", 0), cfg: func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.Adapters = append(c.Adapters, adapter)
		c.HarnessPaths = map[string]string{"claude": os.Args[0], "opencode": os.Args[0]}
	}})
	cli := cliClient(t, n)
	peer, _ := helloPi(t, n, "claude-wake-peer", "/w/peer", true)
	harness := "claude"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
	cli.mustCall("cto_send", map[string]string{"text": "initial"})
	<-adapter.received
	id := n.d.ctoSessionID()
	old, err := n.d.claudeCTOSession()
	if err != nil || old == "" {
		t.Fatalf("Claude conversation: %q %v", old, err)
	}
	harness = "opencode"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
	harness = "claude"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
	eventually(t, "switched CTO dormant", func() bool { a, _, _ := g.gw.Store().Agent(id); return a.Online && a.Status == "dormant" })
	peer.mustCall("send", map[string]string{"to": id, "body": "wake Claude"})
	select {
	case text := <-adapter.received:
		if !strings.Contains(text, "wake Claude") {
			t.Fatalf("wrong text: %q", text)
		}
	case <-time.After(scaled(10 * time.Second)):
		t.Fatal("message did not wake Claude")
	}
	got, err := n.d.claudeCTOSession()
	if err != nil || got != old {
		t.Fatalf("Claude conversation changed: %q %v", got, err)
	}
	eventually(t, "Claude awake", func() bool { a, _, _ := g.gw.Store().Agent(id); return a.Online && a.Status == "idle" })
	// The monitor notices a failed background job even if nobody opens the CTO.
	n.d.cto.mu.Lock()
	ref := n.d.claudeCTO.ref
	n.d.cto.mu.Unlock()
	job := filepath.Join(filepath.Dir(sessions), "jobs", ref)
	if err := os.MkdirAll(job, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "state.json"), []byte(`{"state":"crashed","detail":"fake job failed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Claude crash logged", func() bool { return strings.Contains(logs.String(), "cto.crash harness=claude") })
	if !strings.Contains(logs.String(), `detail="fake job failed"`) {
		t.Fatalf("crash detail missing: %s", logs.String())
	}
	eventually(t, "crashed Claude dormant", func() bool { a, _, _ := g.gw.Store().Agent(id); return a.Online && a.Status == "dormant" })
	peer.mustCall("send", map[string]string{"to": id, "body": "wake crashed Claude"})
	select {
	case text := <-adapter.received:
		if !strings.Contains(text, "wake crashed Claude") {
			t.Fatalf("wrong text: %q", text)
		}
	case <-time.After(scaled(10 * time.Second)):
		t.Fatal("message did not wake crashed Claude")
	}
	got, err = n.d.claudeCTOSession()
	if err != nil || got != old {
		t.Fatalf("Claude conversation changed after crash: %q %v", got, err)
	}
	lines := logs.String()
	for _, want := range []struct {
		text  string
		count int
	}{
		{"cto.start harness=claude", 3}, {"cto.stop harness=claude reason=harness_switch", 1},
		{"cto.wake trigger=owner harness=claude", 1}, {"cto.wake trigger=agent harness=claude", 2},
		{"cto.crash harness=claude", 1}, {"cto.stop harness=claude reason=crash", 1},
	} {
		if count := strings.Count(lines, want.text); count != want.count {
			t.Errorf("%q occurred %d times, want %d:\n%s", want.text, count, want.count, lines)
		}
	}
}

func TestCTOWakeForOwnerPhoneAndBot(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	logs := &ctoLogBuffer{}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, logger: log.New(logs, "", 0), cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	cli := cliClient(t, n)
	cli.mustCall("cto_send", map[string]string{"text": "initial"})
	id := n.d.ctoSessionID()
	session, err := n.d.ctoSession()
	if err != nil {
		t.Fatal(err)
	}
	rec := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	path := filepath.Join(rec.Env["XDG_DATA_HOME"], "fake-session-"+session+".json")
	stop := func() {
		n.d.cto.mu.Lock()
		n.d.stopOpenCodeCTOLocked("test")
		n.d.cto.mu.Unlock()
		eventually(t, "stopped CTO reachable", func() bool { a, _, _ := g.gw.Store().Agent(id); return a.Online && a.Status == "dormant" })
	}
	for _, name := range []string{"owner", "phone"} {
		stop()
		body := "from " + name
		// The phone uses the same owner_send operator method as the TUI.
		cli.mustCall("owner_send", protocol.OwnerSendRequest{To: id, Body: body})
		eventually(t, body, func() bool { b, e := os.ReadFile(path); return e == nil && strings.Contains(string(b), body) })
	}
	stop()
	m := protocol.Message{ID: "m_test_bot", To: id, From: "s_test_bot", FromHarness: "bot", FromName: "test bot", Body: "from bot"}
	result := n.d.deliverTo(context.Background(), &localSession{info: protocol.SessionInfo{Harness: "cto"}}, m)
	if !result.Delivered {
		t.Fatalf("bot delivery: %+v", result)
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "from bot") || !strings.Contains(string(b), "peer input") {
		t.Fatalf("bot text missing peer framing: %s %v", b, err)
	}
	lines := logs.String()
	if strings.Count(lines, "cto.wake trigger=bot harness=opencode") != 1 {
		t.Fatalf("bot wake: %s", lines)
	}
	if strings.Count(lines, "cto.wake trigger=owner harness=opencode") != 3 {
		t.Fatalf("owner and phone wakes: %s", lines)
	}
}

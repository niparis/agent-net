package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTORuntime(t *testing.T) {
	t.Setenv("FAKE_OC_AUTH_RECORD", "1")
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	cli := cliClient(t, n)
	cli.mustCall("cto_events", nil)
	cli.mustCall("cto_new", nil)
	if _, err := os.Stat(filepath.Join(n.cfg.StateDir, "cto")); !os.IsNotExist(err) {
		t.Fatalf("server started before first send: %v", err)
	}
	model := "fake/m1"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Model: &model})
	send := func(text string) { t.Helper(); cli.mustCall("cto_send", map[string]any{"text": text}) }
	events := func(index int) []struct{ Type, Text string } {
		t.Helper()
		var result struct {
			Messages []struct{ Type, Text string } `json:"messages"`
		}
		raw := cli.mustCall("cto_events", map[string]any{"index": index})
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result.Messages
	}
	send("first")
	rec := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if !n.d.insideSession(rec.Pid) {
		t.Fatal("CTO process can use the owner's CLI gate")
	}
	if rec.Env["AGENT_NET_CTO"] != "1" || rec.Env["AGENT_NET_WORKER"] != "" || rec.Env["OPENCODE_SERVER_PASSWORD"] == "" {
		t.Fatalf("environment: %+v", rec.Env)
	}
	if !strings.HasPrefix(rec.Env["XDG_DATA_HOME"], n.cfg.StateDir) || !strings.HasPrefix(rec.Env["XDG_STATE_HOME"], n.cfg.StateDir) {
		t.Fatalf("not private: %+v", rec.Env)
	}
	if _, err := os.Stat(filepath.Join(rec.Env["XDG_DATA_HOME"], "fake-imported-auth")); err != nil {
		t.Fatalf("credentials not imported into private data: %v", err)
	}

	if m, ok := rec.SessionBody["model"].(map[string]any); !ok || m["providerID"] != "fake" || m["id"] != "m1" {
		t.Fatalf("model: %+v", rec.SessionBody)
	}
	model = "fake/m2"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Model: &model})
	send("second")
	if got := events(1); len(got) != 1 || got[0].Text != "second" {
		t.Fatalf("events: %+v", got)
	}
	rec2 := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if rec2.Pid != rec.Pid || rec2.SessionID != rec.SessionID {
		t.Fatal("server or session not reused")
	}
	if m, ok := rec2.SessionBody["model"].(map[string]any); !ok || m["id"] != "m2" {
		t.Fatalf("updated model: %+v", rec2.SessionBody)
	}

	n.restart()
	cli = cliClient(t, n)
	send("third")
	rec2 = readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if rec2.SessionID != rec.SessionID || rec2.Pid == rec.Pid {
		t.Fatalf("restart: old=%+v new=%+v", rec, rec2)
	}
	if got := events(0); len(got) != 3 || got[0].Text != "first" || got[2].Text != "third" {
		t.Fatalf("restart events: %+v", got)
	}
	cli.mustCall("cto_new", nil)
	send("new")
	rec3 := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if rec3.SessionID == rec.SessionID {
		t.Fatal("new conversation reused old session")
	}
	if got := events(0); len(got) != 1 || got[0].Text != "new" {
		t.Fatalf("new events: %+v", got)
	}
	n.d.wmu.Lock()
	count := len(n.d.workers)
	n.d.wmu.Unlock()
	if count != 0 {
		t.Fatalf("CTO listed as worker: %d", count)
	}
	n.d.mu.Lock()
	for _, session := range n.d.sessions {
		if session.info.NativeID == rec.SessionID || session.info.NativeID == rec3.SessionID {
			n.d.mu.Unlock()
			t.Fatal("CTO registered as a network session")
		}
	}
	n.d.mu.Unlock()
}

func TestCTOMethodsOwnerGate(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi", exe: "/opt/pi"})
	pi, _ := helloPi(t, n, "p1", "/w/p", false)
	for _, method := range []string{"cto_send", "cto_new", "cto_events", "cto_settings"} {
		if _, err := pi.call(method, map[string]any{"text": "x"}); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
			t.Errorf("%s: %v", method, err)
		}
	}
}

func TestCTORecoversAfterServerExit(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	cli := cliClient(t, n)
	cli.mustCall("cto_send", map[string]string{"text": "before"})
	rec := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if err := syscall.Kill(-rec.Pgid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	select {
	case <-n.d.cto.exited:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not exit")
	}
	cli.mustCall("cto_send", map[string]string{"text": "after"})
	rec2 := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if rec2.SessionID != rec.SessionID || rec2.Pid == rec.Pid {
		t.Fatalf("server not resumed: %+v %+v", rec, rec2)
	}
	var result struct {
		Messages []struct{ Text string } `json:"messages"`
	}
	json.Unmarshal(cli.mustCall("cto_events", nil), &result)
	if len(result.Messages) != 2 || result.Messages[0].Text != "before" || result.Messages[1].Text != "after" {
		t.Fatalf("messages: %+v", result.Messages)
	}
}

func TestCTOStartupFailure(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	t.Setenv("FAKE_OC_EXIT", "1")
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	cli := cliClient(t, n)
	if _, err := cli.call("cto_send", map[string]string{"text": "hello"}); err == nil || !strings.Contains(err.Error(), "exited at start") {
		t.Fatalf("startup: %v", err)
	}
}

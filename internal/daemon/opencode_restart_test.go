package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// OpenCode can release its project/plugin context while its shared service
// stays alive. A new daemon must load a context even with an empty registry.
func TestOpenCodeSharedServiceAfterDaemonRestart(t *testing.T) {
	root := t.TempDir()
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, filepath.Join(root, k))
		if err := os.MkdirAll(os.Getenv(k), 0700); err != nil {
			t.Fatal(err)
		}
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{exe: "/test/opencode", cfg: func(c *Config) { c.Workers = shortSet }})
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	cmd := exec.Command(os.Args[0], "serve", "--hostname", "127.0.0.1", "--port", fmt.Sprint(port))
	cmd.Dir = root
	cmd.Env = workerEnv(os.Environ(), map[string]string{"OPENCODE_SERVER_PASSWORD": "test-owner-password", "AGENT_NET_SOCKET": n.sock, "AGENT_NET_WORKER": "", "AGENT_NET_CTO": "", "AGENT_NET_WORKER_SESSION_FILE": "", "FAKE_OC_REQUIRE_LOCATION": "1"})
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); cmd.Wait() }()
	api := openCodeWorkerAPI(context.Background(), base, "test-owner-password")
	eventually(t, "fake shared service", func() bool { _, status, e := api("GET", "/api/info", nil); return e == nil && status == 200 })
	b, status, err := api("POST", "/api/session", map[string]string{"title": "owner"})
	if err != nil || status != 200 {
		t.Fatalf("create: %d %v", status, err)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(b, &created)
	if created.Data.ID == "" {
		t.Fatalf("session: %s", b)
	}
	if _, _, err = api("GET", "/api/location", nil); err != nil {
		t.Fatal(err)
	}
	native := created.Data.ID
	eventually(t, "initial registration", func() bool {
		n.d.mu.Lock()
		defer n.d.mu.Unlock()
		ls := n.d.sessions[key("opencode", native)]
		return ls != nil && ls.sessionID != ""
	})
	n.d.mu.Lock()
	id := n.d.sessions[key("opencode", native)].sessionID
	n.d.mu.Unlock()
	// Publish the running shared service, as opencode serve --service does.
	writeOwnerOpenCodeService(t, os.Getenv("XDG_STATE_HOME"), base)
	n.restart()
	eventually(t, "shared service re-registered after restart", func() bool {
		n.d.mu.Lock()
		defer n.d.mu.Unlock()
		ls := n.d.sessions[key("opencode", native)]
		return ls != nil && ls.sessionID == id
	})
	eventually(t, "gateway session online after restart", func() bool { a, ok, e := g.gw.Store().Agent(id); return e == nil && ok && a.Online })
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("shared service ended: %v", err)
	}
	// Discovery is presence, not user activity.
	if a, _, _ := g.gw.Store().Agent(id); a.Status != "idle" {
		t.Fatalf("status after recovery: %s", a.Status)
	}
}

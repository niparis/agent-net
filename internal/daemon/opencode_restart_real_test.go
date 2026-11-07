package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Opt-in integration test: real OpenCode, no model calls or owner data.
func TestOpenCodeRealDaemonRestart(t *testing.T) {
	if os.Getenv("AGENT_NET_TEST_REAL_OPENCODE") != "1" {
		t.Skip("set AGENT_NET_TEST_REAL_OPENCODE=1")
	}
	root := shortDir(t)
	for _, k := range []string{"AGENT_NET_CONFIG_DIR", "AGENT_NET_STATE_DIR", "AGENT_NET_GATEWAY_DATA", "AGENT_NET_CLAUDE_SESSIONS_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, filepath.Join(root, k))
	}
	for _, k := range []string{"AGENT_NET_WORKER", "AGENT_NET_CTO", "AGENT_NET_WORKER_SESSION_FILE"} {
		t.Setenv(k, "")
	}
	config := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "opencode", "plugins")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"agent-net.ts", "tools.gen.ts"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "plugins", "opencode", name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(config, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{realGate: true})
	t.Setenv("AGENT_NET_SOCKET", n.sock)
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	t.Setenv("OPENCODE_SERVER_PASSWORD", "test-pass")
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(root, "opencode.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	cmd := exec.Command("opencode", "serve", "--hostname", "127.0.0.1", "--port", fmt.Sprint(port), "--service")
	cmd.Dir = project
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Wait()
		data, _ := os.ReadFile(logFile.Name())
		t.Logf("OpenCode log: %s", data)
	}()
	password := ""
	client := &http.Client{Timeout: 2 * time.Second}
	api := func(method, path, body string) ([]byte, int) {
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		req.SetBasicAuth("opencode", password)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-opencode-directory", project)
		resp, e := client.Do(req)
		if e != nil {
			return nil, 0
		}
		defer resp.Body.Close()
		var b bytes.Buffer
		b.ReadFrom(resp.Body)
		return b.Bytes(), resp.StatusCode
	}
	eventually(t, "real OpenCode ready", func() bool {
		service, err := ownerOpenCodeService()
		if err != nil {
			return false
		}
		password = service.Password
		_, status := api("GET", "/api/info", "")
		return status == 200
	})
	// Loading a project initializes its plugin instances.
	if b, s := api("GET", "/api/location", ""); s != 200 {
		t.Fatalf("location %d %s", s, b)
	}
	b, s := api("POST", "/api/session", `{"title":"restart test"}`)
	if s != 200 {
		t.Fatalf("create %d %s", s, b)
	}
	var session struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &session) != nil || session.Data.ID == "" {
		t.Fatalf("session %s", b)
	}
	id := session.Data.ID
	eventually(t, "real session registered", func() bool {
		n.d.mu.Lock()
		defer n.d.mu.Unlock()
		ls := n.d.sessions[key("opencode", id)]
		return ls != nil && ls.sessionID != ""
	})
	n.d.mu.Lock()
	sid := n.d.sessions[key("opencode", id)].sessionID
	n.d.mu.Unlock()
	t.Logf("before restart native=%s gateway=%s", id, sid)
	if os.Getenv("AGENT_NET_TEST_REAL_OPENCODE_EVICT") == "1" {
		if b, s := api("DELETE", "/api/debug/location?location%5Bdirectory%5D="+url.QueryEscape(project), ""); s != 204 {
			t.Fatalf("evict: %d %s", s, b)
		}
		eventually(t, "plugin context released", func() bool { n.d.mu.Lock(); defer n.d.mu.Unlock(); return n.d.sessions[key("opencode", id)] == nil })
	}
	if os.Getenv("AGENT_NET_TEST_REAL_OPENCODE_RELOAD") == "1" {
		data, err := os.ReadFile(filepath.Join(config, "agent-net.ts"))
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, []byte("\n// upgrade reproduction\n")...)
		if err = os.WriteFile(filepath.Join(config, "agent-net.ts"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	n.restart()
	eventually(t, "real session re-registered after daemon restart", func() bool {
		n.d.mu.Lock()
		defer n.d.mu.Unlock()
		ls := n.d.sessions[key("opencode", id)]
		return ls != nil && ls.sessionID != ""
	})
	// Poll past the observed five minute window without any session activity.
	until := time.Now().Add(6 * time.Minute)
	if os.Getenv("AGENT_NET_TEST_REAL_OPENCODE_SHORT") == "1" {
		until = time.Now().Add(3 * time.Second)
	}
	for time.Now().Before(until) {
		a, ok, e := g.gw.Store().Agent(sid)
		if e != nil || !ok || !a.Online {
			t.Fatalf("offline after restart: %+v %v", a, e)
		}
		time.Sleep(time.Second)
	}
	t.Log("real OpenCode session stayed online past observation window")
}

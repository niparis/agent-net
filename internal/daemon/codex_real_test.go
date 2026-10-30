package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The real Codex, with a fake model (decision 0012). It starts a private
// `codex app-server --listen unix://` in a throwaway CODEX_HOME and HOME, a
// model server on a loopback port that answers Codex's Responses API requests,
// and the real `agent-net mcp --codex` binary as the thread's MCP server. No
// model is paid and the owner's Codex is never opened.
//
// It runs only with AGENT_NET_CODEX_E2E=1 and a `codex` on PATH:
//
//	AGENT_NET_CODEX_E2E=1 go test -run TestCodexRealAppServer ./internal/daemon

// mockModel answers /v1/responses. A user message "CALLTOOL <tool>" makes the
// model call that tool with the arguments the test set; any other user message
// gets a plain answer; the result of a tool call gets "tool done".
type mockModel struct {
	mu   sync.Mutex
	args string
	user []string // the text of every user message that reached the model
}

func (m *mockModel) setArgs(a string) { m.mu.Lock(); m.args = a; m.mu.Unlock() }

func (m *mockModel) users() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.user...)
}

func (m *mockModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Input []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
		Tools []struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"tools"`
	}
	json.Unmarshal(body, &req)
	lastUser, lastType := "", ""
	for _, it := range req.Input {
		lastType = it.Type
		if it.Type == "message" && it.Role == "user" {
			lastUser = ""
			for _, c := range it.Content {
				lastUser += c.Text
			}
		}
	}
	id := fmt.Sprintf("resp_%d", time.Now().UnixNano())
	events := []string{`{"type":"response.created","response":{"id":"` + id + `"}}`}
	text := func(s string) {
		b, _ := json.Marshal(s)
		events = append(events, `{"type":"response.output_item.done","item":{"type":"message","role":"assistant","id":"m`+id+`","content":[{"type":"output_text","text":`+string(b)+`}]}}`)
	}
	switch {
	case lastType == "function_call_output":
		text("tool done")
	case strings.Contains(lastUser, "CALLTOOL "):
		m.mu.Lock()
		m.user = append(m.user, lastUser)
		args := m.args
		m.mu.Unlock()
		tool := strings.Fields(lastUser[strings.Index(lastUser, "CALLTOOL ")+9:])[0]
		ns := ""
		for _, t := range req.Tools {
			for _, in := range t.Tools {
				if in.Name == tool && t.Type == "namespace" {
					ns = t.Name
				}
			}
		}
		argsJSON, _ := json.Marshal(args)
		events = append(events, `{"type":"response.output_item.done","item":{"type":"function_call","namespace":"`+ns+`","name":"`+tool+`","arguments":`+string(argsJSON)+`,"call_id":"call_`+id+`"}}`)
	default:
		m.mu.Lock()
		m.user = append(m.user, lastUser)
		m.mu.Unlock()
		text("mock answer")
	}
	events = append(events, `{"type":"response.completed","response":{"id":"`+id+`","usage":{"input_tokens":1,"input_tokens_details":null,"output_tokens":1,"output_tokens_details":null,"total_tokens":2}}}`)
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		var ev struct {
			Type string `json:"type"`
		}
		json.Unmarshal([]byte(e), &ev)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, e)
	}
}

func waitFor(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestCodexRealAppServer(t *testing.T) {
	if os.Getenv("AGENT_NET_CODEX_E2E") != "1" {
		t.Skip("set AGENT_NET_CODEX_E2E=1 to run against a real codex")
	}
	codexBin, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex is not installed")
	}
	// Built from this tree: the shim that Codex starts is the real one.
	exe := filepath.Join(t.TempDir(), "agent-net")
	if out, err := exec.Command("go", "build", "-o", exe, "github.com/niparis/agent-net/cmd/agent-net").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	model := &mockModel{}
	ms := httptest.NewServer(model)
	t.Cleanup(ms.Close)

	g := startGateway(t, t.TempDir(), "")
	codexHome, home := shortDir(t), shortDir(t)
	n := startNode(t, g, nodeOpts{cmdline: "pi", cfg: func(c *Config) { c.CodexHome = codexHome; c.CodexInterval = 200 * time.Millisecond }})
	cfg := fmt.Sprintf(`model_provider = "mock"
model = "mock-model"
approval_policy = "never"
sandbox_mode = "read-only"

[model_providers.mock]
name = "mock"
base_url = "%s/v1"
wire_api = "responses"
requires_openai_auth = false

[mcp_servers.agent-net]
command = %q
args = ["mcp", "--codex"]
default_tools_approval_mode = "approve"
env = { AGENT_NET_SOCKET = %q, AGENT_NET_NO_AUTO_UPGRADE = "1" }
`, ms.URL, exe, n.sock)
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	// The shared app server, in its own process group so that it and its MCP
	// servers all go away.
	srv := exec.Command(codexBin, "app-server", "--listen", "unix://")
	srv.Env = append(os.Environ(), "HOME="+home, "CODEX_HOME="+codexHome, "XDG_CONFIG_HOME="+home+"/.config",
		"XDG_STATE_HOME="+home+"/.state", "XDG_DATA_HOME="+home+"/.data")
	srv.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Kill(-srv.Process.Pid, syscall.SIGKILL); srv.Wait() })

	// The window: a client that starts a thread, so it is open and subscribed.
	var nmu sync.Mutex
	var notes []string
	var window *codexRPC
	waitFor(t, "the app server accepts a connection", 30*time.Second, func() bool {
		window, err = dialCodexSocket(context.Background(), codexSocketPath(codexHome), "test", func(method string, params json.RawMessage) {
			nmu.Lock()
			notes = append(notes, method+" "+string(params))
			nmu.Unlock()
		})
		return err == nil
	})
	t.Cleanup(window.close)
	work := t.TempDir()
	raw, err := window.call(context.Background(), "thread/start", map[string]any{"cwd": work})
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	json.Unmarshal(raw, &started)
	thread := started.Thread.ID
	if thread == "" {
		t.Fatalf("thread/start: %s", raw)
	}
	turn := func(text string) {
		t.Helper()
		if _, err := window.call(context.Background(), "turn/start", map[string]any{"threadId": thread,
			"input": []map[string]any{{"type": "text", "text": text, "text_elements": []any{}}}}); err != nil {
			t.Fatal(err)
		}
	}
	sawNote := func(parts ...string) bool {
		nmu.Lock()
		defer nmu.Unlock()
		for _, s := range notes {
			ok := true
			for _, p := range parts {
				ok = ok && strings.Contains(s, p)
			}
			if ok {
				return true
			}
		}
		return false
	}

	// 1. The open thread is a session, found through the control socket.
	pi, piID := helloPi(t, n, "np", "/work/pi", true)
	waitFor(t, "the thread is a session", 10*time.Second, func() bool { return n.d.SessionID("codex", thread) != "" })
	codexID := n.d.SessionID("codex", thread)
	if got := codexAgents(t, pi)[work]; got.SessionID != codexID || got.Harness != "codex" {
		t.Fatalf("listed as %+v", got)
	}
	if pid := n.d.adapters["codex"].(*codexAdapter).serverPID(); pid != srv.Process.Pid {
		t.Fatalf("server pid from the socket is %d, the process is %d", pid, srv.Process.Pid)
	}

	// 2. Receive: a message is queued, and the thread runs it as a user message.
	m := sendResult(t, pi.mustCall("send", map[string]any{"to": codexID, "body": "please check the build"}))
	waitFor(t, "the thread shows the message", 15*time.Second, func() bool {
		return sawNote("item/started", `"userMessage"`, "please check the build", `"clientId":"`+m.ID+`"`)
	})
	waitFor(t, "the model got it, framed", 15*time.Second, func() bool {
		for _, u := range model.users() {
			if strings.HasPrefix(u, "please check the build") && strings.Contains(u, "[agent-net] message "+m.ID) {
				return true
			}
		}
		return false
	})
	waitFor(t, "the turn ended", 15*time.Second, func() bool { return sawNote("turn/completed") })

	// 3. Send: the thread's own agent-net MCP server (a child of the app
	// server) sends as this thread, with the thread taken from _meta.
	model.setArgs(fmt.Sprintf(`{"to":%q,"body":"hello from real codex"}`, piID))
	turn("CALLTOOL agent_send")
	got := pi.nextPush()
	if got.Body != "hello from real codex" || got.From != codexID || got.FromHarness != "codex" {
		t.Fatalf("message from codex: %+v", got)
	}
	waitFor(t, "the tool call completed", 15*time.Second, func() bool {
		return sawNote("item/completed", `"mcpToolCall"`, `"status":"completed"`)
	})
}

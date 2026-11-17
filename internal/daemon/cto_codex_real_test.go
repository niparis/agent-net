package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The real-protocol check of the Codex CTO (decision 0171, phase 3). It runs
// the codex on PATH as the CTO's private app server, the way the daemon starts
// it, in a throwaway HOME and CODEX_HOME. The model is the fake Responses
// endpoint of codex_real_test.go on loopback: each message names the one tool
// call the fake makes, so no model is paid and the owner's Codex is never
// opened. The cto-mcp server is this tree's agent-net binary. Run it with
// AGENT_NET_TEST_REAL_CODEX=1, or with the path of codex when the test's PATH
// does not have it.
//
// What the real server did on 2026-11-17 (codex-cli 0.169.0), with the thread
// on approval "never" and the "danger-full-access" sandbox:
//
//	tools offered        exec_command for the shell, no apply_patch for this
//	                     model; every cto-mcp tool, so the server's MCP child
//	                     passed the cto-mcp process-tree check
//	shell "uname; id"    ran, no prompt
//	append to a file     ran, no prompt; the file outside the CTO's folder
//	outside the folder   changed
//	agent_notes_find     ran, no prompt; returned the CTO's notes
//	delivery confirm     the user message item carried the message ID, and
//	                     the daemon logged cto.delivery_confirmed
//	approval requests    none reached the daemon
func TestCTOCodexRealServerRunsToolsWithoutPrompts(t *testing.T) {
	codexBin := os.Getenv("AGENT_NET_TEST_REAL_CODEX")
	if codexBin == "" {
		t.Skip("set AGENT_NET_TEST_REAL_CODEX to the path of codex, or to 1 for the one on PATH")
	}
	if codexBin == "1" {
		var err error
		if codexBin, err = exec.LookPath("codex"); err != nil {
			t.Skip("codex is not on PATH")
		}
	}
	exe := filepath.Join(t.TempDir(), "agent-net")
	if out, err := exec.Command("go", "build", "-o", exe, "github.com/niparis/agent-net/cmd/agent-net").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	home, codexHome := shortDir(t), shortDir(t)
	for k, v := range map[string]string{"HOME": home, "XDG_CONFIG_HOME": home + "/.config", "XDG_STATE_HOME": home + "/.state", "XDG_DATA_HOME": home + "/.data", "CODEX_HOME": codexHome} {
		t.Setenv(k, v)
	}
	model := &toolLogModel{inner: &mockModel{}}
	ms := httptest.NewServer(model)
	t.Cleanup(ms.Close)
	cfg := fmt.Sprintf(`model_provider = "mock"
model = "mock-model"

[model_providers.mock]
name = "mock"
base_url = "%s/v1"
wire_api = "responses"
requires_openai_auth = false
`, ms.URL)
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	logs := &logBuf{}
	g := startGateway(t, t.TempDir(), "")
	detect := func(context.Context) []protocol.HarnessInfo {
		return []protocol.HarnessInfo{{Name: "codex", Path: codexBin, Version: "real", Models: []string{"mock-model"}}}
	}
	n := startNode(t, g, nodeOpts{detect: detect, interval: time.Hour, logger: log.New(logs, "", 0), cfg: func(c *Config) {
		c.CodexHome = codexHome
		c.Exe = exe
		c.HarnessPaths = map[string]string{"codex": codexBin}
		c.testStopCTOOnExit = true
		c.testNoDeliveryConfirm = false
		c.deliveryConfirmWait = 5 * time.Second
	}})
	cli := cliClient(t, n)
	harness, name := "codex", "mock-model"
	cli.mustCall(protocol.OpCTOSettings, protocol.CTOSettingsRequest{Harness: &harness, Model: &name})
	send := func(text string) {
		t.Helper()
		cli.mustCall(protocol.OpCTOSend, map[string]string{"text": text})
	}

	outside := filepath.Join(home, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(outside, "readme.txt")
	if err := os.WriteFile(readme, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Each step is one turn: the fake model makes the one tool call, then
	// answers "tool done" when the call's output comes back.
	step := func(what, tool, args string) string {
		t.Helper()
		before := len(model.outputs())
		model.inner.setArgs(args)
		send("CALLTOOL " + tool)
		waitFor(t, what+" returns its output to the model", 90*time.Second, func() bool { return len(model.outputs()) > before })
		out := model.outputs()[before]
		t.Logf("%s: %s", what, out)
		return out
	}
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	shell := step("shell command", "exec_command", `{"cmd":`+q("uname -s; id -un")+`}`)
	edit := step("edit outside the folder", "exec_command", `{"cmd":`+q("printf 'after\\n' >> "+readme)+`}`)
	tool := step("agent-net MCP tool", "agent_notes_find", `{}`)

	if !strings.Contains(shell, "Linux") && !strings.Contains(shell, "Darwin") {
		t.Errorf("shell output %s", shell)
	}
	if got, _ := os.ReadFile(readme); string(got) != "before\nafter\n" {
		t.Errorf("the file outside the folder is %q (edit output %s)", got, edit)
	}
	if strings.Contains(strings.ToLower(tool), "rejected") || strings.Contains(strings.ToLower(tool), "approval") || strings.Contains(tool, "only the CTO") {
		t.Errorf("MCP tool output %s", tool)
	}
	eventually(t, "a delivery is confirmed by the user message item", func() bool { return strings.Contains(logs.String(), "cto.delivery_confirmed") })
	if strings.Contains(logs.String(), "cto.codex_request") {
		t.Errorf("Codex asked for an approval:\n%s", logs.String())
	}
	t.Logf("daemon log:\n%s", logs.String())
}

// toolLogModel records the tool names Codex offers, then answers as mockModel.
type toolLogModel struct {
	inner *mockModel
	mu    sync.Mutex
	tools map[string]bool
	// outs are the tool outputs Codex sent back, one per call, in order.
	outs []string
	seen map[string]bool
}

func (m *toolLogModel) outputs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.outs...)
}

func (m *toolLogModel) toolNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for k := range m.tools {
		out = append(out, k)
	}
	return out
}

func (m *toolLogModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Input []struct {
			Type   string          `json:"type"`
			CallID string          `json:"call_id"`
			Output json.RawMessage `json:"output"`
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
	m.mu.Lock()
	if m.tools == nil {
		m.tools, m.seen = map[string]bool{}, map[string]bool{}
	}
	for _, in := range req.Input {
		if in.Type == "function_call_output" && !m.seen[in.CallID] {
			m.seen[in.CallID] = true
			m.outs = append(m.outs, string(in.Output))
		}
	}
	for _, tool := range req.Tools {
		m.tools[tool.Type+":"+tool.Name] = true
		for _, in := range tool.Tools {
			m.tools[tool.Name+"/"+in.Name] = true
		}
	}
	m.mu.Unlock()
	r.Body = io.NopCloser(bytes.NewReader(body))
	m.inner.ServeHTTP(w, r)
}

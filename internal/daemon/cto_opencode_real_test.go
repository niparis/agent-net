package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The real-protocol check of the CTO's OpenCode rules (decision 0171). It runs
// the opencode on PATH, with the CTO's rules from ctoPermissions, in a temporary
// home on loopback. The model is a fake OpenAI Responses endpoint on loopback,
// and each prompt names the one tool call the fake makes, so no model is called
// and no owner data is read. Run it with AGENT_NET_TEST_REAL_OPENCODE=1.
//
// What the real server did on 2026-11-17 (OpenCode 2.3.0):
//
//	with the rules as they were, before the two read rules:
//	  shell "ssh -V"                    ran, no prompt
//	  write a file outside the folder   ran, no prompt
//	  read a file outside the folder    ran, no prompt
//	  edit a file outside the folder    ran, no prompt
//	  read cto/.env inside the folder   PROMPTED: a pending permission for
//	                                    read ".env" that nobody answers, so the
//	                                    CTO stalls
//	  the same shell call three times   ran, no prompt (no doom_loop prompt)
//
//	with the read rules of ctoPermissions:
//	  read cto/.env                     ran, no prompt
//
// The first run of this check used the default rules and a probe-only fake
// provider; the results above are what that run printed.
func TestCTOOpenCodeRealServerRunsToolsWithoutPrompts(t *testing.T) {
	if os.Getenv("AGENT_NET_TEST_REAL_OPENCODE") != "1" {
		t.Skip("set AGENT_NET_TEST_REAL_OPENCODE=1")
	}
	bin, err := exec.LookPath("opencode")
	if err != nil {
		t.Skip("opencode is not on PATH")
	}
	root := shortDir(t)
	for _, k := range []string{"HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		dir := filepath.Join(root, k)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(k, dir)
	}
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	provider := newCTOProbeProvider(t)
	cwd := filepath.Join(root, "cto")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	permissions, err := json.Marshal(ctoPermissions())
	if err != nil {
		t.Fatal(err)
	}
	// The daemon writes this file in the CTO's folder, and OpenCode reads it
	// from there. The model is the fake provider.
	config := fmt.Sprintf(`{"agents":{"cto":{"mode":"primary","description":"The owner's CTO","system":"probe","permissions":%s}},`+
		`"provider":{"openai":{"options":{"baseURL":%q,"apiKey":"not-a-key"},"models":{"probe-model":{"name":"probe-model"}}}},"model":"openai/probe-model"}`,
		permissions, provider.srv.URL+"/v1")
	if err := os.WriteFile(filepath.Join(cwd, "opencode.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "readme.txt"), []byte("outside-readme"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".env"), []byte("SECRET=probe"), 0o600); err != nil {
		t.Fatal(err)
	}

	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	const password = "probe-pass"
	logFile, err := os.Create(filepath.Join(root, "opencode.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	serve := exec.Command(bin, "serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(port))
	serve.Dir = cwd
	serve.Env = append(os.Environ(), "OPENCODE_SERVER_PASSWORD="+password)
	serve.Stdout, serve.Stderr = logFile, logFile
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = serve.Process.Kill()
		_, _ = serve.Process.Wait()
	})
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	eventually(t, "the opencode server answers", func() bool {
		_, status, err := probeAPI(base, password, http.MethodGet, "/api/info", nil)
		return err == nil && status == http.StatusOK
	})

	raw, status, err := probeAPI(base, password, http.MethodPost, "/api/session", map[string]any{
		"title": "CTO probe", "agent": "cto", "model": map[string]string{"providerID": "openai", "id": "probe-model"},
	})
	if err != nil || status/100 != 2 {
		t.Fatalf("create session: %d %v %s", status, err, raw)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.Data.ID == "" {
		t.Fatalf("session id: %s %v", raw, err)
	}
	session := created.Data.ID

	probes := []struct {
		name, call string
		effect     func() bool
	}{
		{"shell ssh", `CALL shell {"command":"ssh -V","description":"probe"}`, nil},
		{"write outside", fmt.Sprintf(`CALL write {"path":%q,"content":"probe"}`, filepath.Join(outside, "written.txt")), func() bool {
			_, err := os.Stat(filepath.Join(outside, "written.txt"))
			return err == nil
		}},
		{"read outside", fmt.Sprintf(`CALL read {"path":%q}`, filepath.Join(outside, "readme.txt")), nil},
		{"edit outside", fmt.Sprintf(`CALL edit {"path":%q,"oldString":"outside-readme","newString":"edited"}`, filepath.Join(outside, "readme.txt")), func() bool {
			data, _ := os.ReadFile(filepath.Join(outside, "readme.txt"))
			return string(data) == "edited"
		}},
		{"read env", fmt.Sprintf(`CALL read {"path":%q}`, filepath.Join(cwd, ".env")), nil},
	}
	for _, probe := range probes {
		before := provider.count()
		if _, status, err := probeAPI(base, password, http.MethodPost, "/api/session/"+session+"/prompt", map[string]string{"text": probe.call, "delivery": "queue"}); err != nil || status/100 != 2 {
			t.Fatalf("%s: prompt %d %v", probe.name, status, err)
		}
		eventually(t, probe.name+" returns its result to the model without a prompt", func() bool {
			return provider.count() > before
		})
		if pending, _, _ := probeAPI(base, password, http.MethodGet, "/api/session/"+session+"/permission", nil); strings.Contains(string(pending), `"id"`) {
			t.Fatalf("%s is waiting for a permission: %s", probe.name, pending)
		}
		if probe.effect != nil && !probe.effect() {
			t.Fatalf("%s ran but its effect is missing", probe.name)
		}
	}
}

// probeAPI calls the opencode server with the CTO's basic-auth password.
func probeAPI(base, password, method, path string, body any) ([]byte, int, error) {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rd = strings.NewReader(string(raw))
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		return nil, 0, err
	}
	req.SetBasicAuth("opencode", password)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	return out, resp.StatusCode, err
}

// ctoProbeProvider answers the OpenAI Responses API. A prompt "CALL <tool> <json>"
// makes one function call; the tool's output gets a text answer. count is how many
// tool outputs the model got back.
type ctoProbeProvider struct {
	srv *httptest.Server
	mu  sync.Mutex
	got int
}

func newCTOProbeProvider(t *testing.T) *ctoProbeProvider {
	t.Helper()
	p := &ctoProbeProvider{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Input []struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Input) == 0 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		last := body.Input[len(body.Input)-1]
		if last.Type == "function_call_output" {
			p.mu.Lock()
			p.got++
			p.mu.Unlock()
			writeProbeEvents(w, probeTextEvents("probe answer"))
			return
		}
		prompt := ""
		for _, c := range last.Content {
			if c.Type == "input_text" {
				prompt = c.Text
			}
		}
		if rest, ok := strings.CutPrefix(prompt, "CALL "); ok {
			name, args, _ := strings.Cut(rest, " ")
			writeProbeEvents(w, probeCallEvents(name, args))
			return
		}
		writeProbeEvents(w, probeTextEvents("probe: send a CALL prompt"))
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *ctoProbeProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.got
}

// writeProbeEvents writes server-sent events, one per (name, data) pair.
func writeProbeEvents(w http.ResponseWriter, events []probeEvent) {
	for _, e := range events {
		raw, _ := json.Marshal(e.data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.name, raw)
	}
}

type probeEvent struct {
	name string
	data map[string]any
}

func probeTextEvents(text string) []probeEvent {
	item := map[string]any{"type": "message", "id": "msg_probe", "role": "assistant", "status": "completed",
		"content": []map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}}}
	return []probeEvent{
		{"response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_probe", "object": "response", "status": "in_progress", "model": "probe-model", "output": []any{}}}},
		{"response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_probe", "role": "assistant", "status": "in_progress", "content": []any{}}}},
		{"response.content_part.added", map[string]any{"type": "response.content_part.added", "item_id": "msg_probe", "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}}},
		{"response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": "msg_probe", "output_index": 0, "content_index": 0, "delta": text}},
		{"response.output_text.done", map[string]any{"type": "response.output_text.done", "item_id": "msg_probe", "output_index": 0, "content_index": 0, "text": text}},
		{"response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}},
		{"response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_probe", "object": "response", "status": "completed", "model": "probe-model", "output": []any{item}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}}},
	}
}

func probeCallEvents(name, args string) []probeEvent {
	item := map[string]any{"type": "function_call", "id": "fc_probe", "call_id": "call_probe", "name": name, "arguments": args, "status": "completed"}
	return []probeEvent{
		{"response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_probe", "object": "response", "status": "in_progress", "model": "probe-model", "output": []any{}}}},
		{"response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "function_call", "id": "fc_probe", "call_id": "call_probe", "name": name, "arguments": "", "status": "in_progress"}}},
		{"response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "item_id": "fc_probe", "output_index": 0, "delta": args}},
		{"response.function_call_arguments.done", map[string]any{"type": "response.function_call_arguments.done", "item_id": "fc_probe", "output_index": 0, "arguments": args}},
		{"response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}},
		{"response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_probe", "object": "response", "status": "completed", "model": "probe-model", "output": []any{item}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}}},
	}
}

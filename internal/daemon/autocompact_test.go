package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCodexRolloutContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	lines := []string{
		`{"type":"event_msg","payload":{"type":"task_started","model_context_window":258400}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":12000},"model_context_window":258400}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":210000},"model_context_window":258400}}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tokens, limit := codexRolloutContext(path)
	if tokens != 210000 || limit != 258400 {
		t.Fatalf("context=%d/%d", tokens, limit)
	}
}

func TestAutoCompactPiByFill(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	lc := dialLocal(t, n.sock)
	res, err := lc.call("hello", map[string]any{
		"client": "pi", "harness": "pi", "native_id": "npi", "cwd": t.TempDir(),
		"status": "idle", "mode": "background", "model": "anthropic/claude-sonnet-5",
		"context_tokens": 850000, "context_limit": 1000000,
	})
	if err != nil {
		t.Fatal(err)
	}
	var hello struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(res, &hello)
	if hello.SessionID == "" {
		t.Fatal("pi hello returned no session")
	}
	n.d.autoCompactOnce(context.Background())
	select {
	case method := <-lc.Other:
		if method != "compact" {
			t.Fatalf("push method=%q, want compact", method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pi session was not asked to compact")
	}
	if _, err := lc.call("compacted", map[string]any{"ok": true, "reason": "fill 85%", "at": 1700000000}); err != nil {
		t.Fatal(err)
	}
	info := n.d.infoOf(n.d.sessionByID(hello.SessionID))
	if info.LastCompactedAt != 1700000000 || info.LastCompactedReason != "fill 85%" {
		t.Fatalf("row note=%d %q", info.LastCompactedAt, info.LastCompactedReason)
	}
	// One request per idle stretch.
	n.d.autoCompactOnce(context.Background())
	select {
	case method := <-lc.Other:
		t.Fatalf("second compaction push %q", method)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAutoCompactSkipsAttachedAndBusy(t *testing.T) {
	for _, tc := range []struct{ name, mode, status string }{
		{"attached", "foreground", "idle"},
		{"busy", "background", "busy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			n := startNode(t, g, nodeOpts{cmdline: "pi"})
			lc := dialLocal(t, n.sock)
			if _, err := lc.call("hello", map[string]any{
				"client": "pi", "harness": "pi", "native_id": "npi-" + tc.name, "cwd": t.TempDir(),
				"status": tc.status, "mode": tc.mode, "model": "anthropic/claude-sonnet-5",
				"context_tokens": 900000, "context_limit": 1000000,
			}); err != nil {
				t.Fatal(err)
			}
			n.d.autoCompactOnce(context.Background())
			select {
			case method := <-lc.Other:
				t.Fatalf("%s session got a %q push", tc.name, method)
			case <-time.After(100 * time.Millisecond):
			}
		})
	}
}

func TestAutoCompactNeverTouchesAWorker(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	lc := dialLocal(t, n.sock)
	res, err := lc.call("hello", map[string]any{
		"client": "pi", "harness": "pi", "native_id": "npi-worker", "cwd": t.TempDir(),
		"status": "idle", "mode": "background", "model": "anthropic/claude-sonnet-5",
		"context_tokens": 900000, "context_limit": 1000000,
	})
	if err != nil {
		t.Fatal(err)
	}
	var hello struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(res, &hello)
	ls := n.d.sessionByID(hello.SessionID)
	if ls == nil {
		t.Fatal("no session")
	}
	n.d.mu.Lock()
	ls.info.Worker = &protocol.WorkerInfo{ID: "w1"}
	n.d.mu.Unlock()
	n.d.autoCompactOnce(context.Background())
	select {
	case method := <-lc.Other:
		t.Fatalf("worker got a %q push", method)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAutoCompactOpenCodeByFill(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{exe: "/opt/x/node_modules/@opencode/cli/bin/opencode.exe"})
	dir := t.TempDir()
	var mu sync.Mutex
	var compacted []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "opencode" || pass != "test-owner-password" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/openapi.json" {
			http.NotFound(w, r)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/session/active":
			if r.URL.Query().Get("directory") != "" {
				break
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
		case "GET /api/session/ses_auto":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "ses_auto", "model": map[string]any{"providerID": "fake", "id": "m1"}}})
		case "GET /api/session/ses_auto/message":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "msg_1", "type": "assistant", "tokens": map[string]any{"input": 850000}}}})
		case "GET /api/model":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"providerID": "fake", "id": "m1", "limit": map[string]any{"context": 1000000}}}})
		case "POST /api/session/ses_auto/compact":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			compacted = append(compacted, body["delivery"])
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "cmp"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	writeOwnerOpenCodeService(t, state, server.URL)
	lc, id := helloOpenCode(t, n, "ses_auto", dir)
	_ = lc
	n.d.autoCompactOnce(context.Background())
	mu.Lock()
	calls := append([]string(nil), compacted...)
	mu.Unlock()
	if len(calls) != 1 || calls[0] != "steer" {
		t.Fatalf("compact calls=%v", calls)
	}
	info := n.d.infoOf(n.d.sessionByID(id))
	if info.LastCompactedReason != "fill 85%" {
		t.Fatalf("row reason=%q", info.LastCompactedReason)
	}
}

func TestCompactionUnsupportedHarness(t *testing.T) {
	if protocol.CompactionSupported("agy") {
		t.Fatal("agy reports compaction support")
	}
	for _, harness := range []string{"claude", "opencode", "codex", "pi"} {
		if !protocol.CompactionSupported(harness) {
			t.Fatalf("%s does not report compaction support", harness)
		}
	}
}

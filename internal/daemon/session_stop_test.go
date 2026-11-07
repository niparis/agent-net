package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSessionStopOwnerGate(t *testing.T) {
	for _, client := range []string{"mcp", "pi", "opencode", "cli"} {
		d := &Daemon{inSession: func(int) bool { return true }}
		lc := &localConn{d: d, client: client}
		_, handled, err := lc.dispatchOperator(context.Background(), "session_stop", json.RawMessage(`{"session_id":"s_test"}`))
		if !handled || err == nil || !(strings.Contains(err.Error(), "only available to the owner") || strings.Contains(err.Error(), "inside an agent session")) {
			t.Errorf("%s: handled=%v err=%v", client, handled, err)
		}
	}
}

func TestSessionStopClaudeBackground(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	args := filepath.Join(t.TempDir(), "args")
	bins := fakeBin(t, "claude")
	if err := os.WriteFile(bins["claude"], []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+shellQuote(args)+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	fa := &enterAdapter{fakeAdapter: &fakeAdapter{harness: "claude"}, kind: "bg", job: "abcd1234"}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.HarnessPaths = bins; c.Adapters = []Adapter{fa} }})
	fa.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "abcd1234-session", Name: "shop", Status: "busy", PID: 1})
	var id string
	eventually(t, "session registered", func() bool {
		for _, s := range n.d.Sessions() {
			id = s.SessionID
		}
		return id != ""
	})
	cliClient(t, n).mustCall("session_stop", map[string]string{"session_id": id})
	n.d.mu.Lock()
	ending := n.d.sessions[key("claude", "abcd1234-session")].ownerStopped
	n.d.mu.Unlock()
	if !ending {
		t.Fatal("owner stop was not recorded before discovery removes the busy session")
	}
	got, err := os.ReadFile(args)
	if err != nil || string(got) != "stop\nabcd1234\n" {
		t.Fatalf("stop command %q: %v", got, err)
	}
}

func TestSessionStopTerminalRefusal(t *testing.T) {
	t.Parallel()
	for _, h := range []string{"claude", "codex", "pi"} {
		t.Run(h, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			n, _, id := enterNode(t, g, h, protocol.SessionInfo{NativeID: "terminal", Name: "shop", PID: 1, Status: "idle", Mode: protocol.ModeForeground}, nil, nil)
			_, err := cliClient(t, n).call("session_stop", map[string]string{"session_id": id})
			want := "shop runs in your terminal on " + n.d.hostName() + ": end it there"
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("want %q, got %v", want, err)
			}
		})
	}
}

func TestSessionStopTerminalNamedWithFolder(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := filepath.Join(home, "code", "niparis", "video")
	n, _, id := enterNode(t, g, "claude", protocol.SessionInfo{
		NativeID: "b0e105f0-ab2e-45e8-bf38-e3608761c8e9",
		Cwd:      cwd,
		Repo:     protocol.RepoInfo{Project: "video"},
		PID:      1,
		Status:   "idle",
		Mode:     protocol.ModeForeground,
	}, nil, nil)
	_, err := cliClient(t, n).call("session_stop", map[string]string{"session_id": id})
	want := "video (~/code/niparis/video) runs in your terminal on " + n.d.hostName() + ": end it there"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("want %q, got %v", want, err)
	}
}

func TestSessionStopOpenCodeService(t *testing.T) {
	withFakeOpenCodeRouteSets(t, testSessionStopOpenCodeService)
}

func testSessionStopOpenCodeService(t *testing.T) {
	for _, status := range []string{"busy", "idle"} {
		t.Run(status, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			n, _, id := enterNode(t, g, "opencode", protocol.SessionInfo{NativeID: "ses_owner", Name: "shop", Status: status, Cwd: "/src/shop space"}, nil, nil)
			state := t.TempDir()
			t.Setenv("XDG_STATE_HOME", state)
			calls := make(chan string, 1)
			mux := http.NewServeMux()
			old := os.Getenv("FAKE_OC_OLD_ROUTES") != ""
			fakeOpenCodeCapabilities(mux, old, func(*http.Request) bool { return true }, func(w http.ResponseWriter, r *http.Request) {
				user, password, ok := r.BasicAuth()
				if !ok || user != "opencode" || password != "test-owner-password" {
					t.Errorf("missing service authentication")
				}
				if r.Header.Get("x-opencode-directory") != "%2Fsrc%2Fshop%20space" {
					t.Errorf("directory %q", r.Header.Get("x-opencode-directory"))
				}
				calls <- r.Method + " " + r.URL.Path
				fmt.Fprint(w, `{"interrupted":true}`)
			})
			server := httptest.NewServer(mux)
			defer server.Close()
			writeOwnerOpenCodeService(t, state, server.URL)
			raw := cliClient(t, n).mustCall("session_stop", map[string]string{"session_id": id})
			if !strings.Contains(string(raw), "stays in the service") {
				t.Fatalf("result %s", raw)
			}
			select {
			case got := <-calls:
				action := "interrupt"
				if old {
					action = "abort"
				}
				if got != "POST /api/session/ses_owner/"+action {
					t.Fatalf("interrupt %q", got)
				}
			default:
				t.Fatal("no interrupt")
			}
		})
	}
}

func TestSessionStopOpenCodeMissingInterruptRoute(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n, _, id := enterNode(t, g, "opencode", protocol.SessionInfo{NativeID: "ses_owner", Status: "busy"}, nil, nil)
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/openapi.json" {
			fmt.Fprint(w, `{"paths":{}}`)
			return
		}
		t.Errorf("unexpected request %s", r.URL.Path)
	}))
	defer server.Close()
	writeOwnerOpenCodeService(t, state, server.URL)
	_, err := cliClient(t, n).call("session_stop", map[string]string{"session_id": id})
	if err == nil || !strings.Contains(err.Error(), "has no interrupt route") {
		t.Fatalf("missing route: %v", err)
	}
}

func TestSessionStopOpenCodeServiceFallbackAbort(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n, _, id := enterNode(t, g, "opencode", protocol.SessionInfo{NativeID: "ses_owner", Name: "shop", Status: "busy", Cwd: "/src/shop space"}, nil, nil)
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	calls := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/openapi.json" {
			fmt.Fprint(w, `{"paths":{"/api/session/{sessionID}/interrupt":{"post":{}}}}`)
			return
		}
		calls <- r.Method + " " + r.URL.Path
		if r.URL.Path == "/api/session/ses_owner/interrupt" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/api/session/ses_owner/abort" {
			fmt.Fprint(w, `{"data":true}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	writeOwnerOpenCodeService(t, state, server.URL)
	raw := cliClient(t, n).mustCall("session_stop", map[string]string{"session_id": id})
	if !strings.Contains(string(raw), "stays in the service") {
		t.Fatalf("result %s", raw)
	}
	var gotCalls []string
	for len(calls) > 0 {
		gotCalls = append(gotCalls, <-calls)
	}
	wantCalls := []string{"POST /api/session/ses_owner/interrupt", "POST /api/session/ses_owner/abort"}
	if len(gotCalls) != 2 || gotCalls[0] != wantCalls[0] || gotCalls[1] != wantCalls[1] {
		t.Fatalf("calls %v, want %v", gotCalls, wantCalls)
	}
}

func TestSessionStopOpenCodeInterruptHTTPError(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			n, _, id := enterNode(t, g, "opencode", protocol.SessionInfo{NativeID: "ses_owner", Status: "busy"}, nil, nil)
			state := t.TempDir()
			t.Setenv("XDG_STATE_HOME", state)
			calls := make(chan string, 3)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/openapi.json" {
					fmt.Fprint(w, `{"paths":{"/api/session/{sessionID}/interrupt":{"post":{}}}}`)
					return
				}
				calls <- r.URL.Path
				w.WriteHeader(status)
			}))
			defer server.Close()
			writeOwnerOpenCodeService(t, state, server.URL)
			_, err := cliClient(t, n).call("session_stop", map[string]string{"session_id": id})
			want := fmt.Sprintf("OpenCode %s: interrupt failed (HTTP %d)", n.d.openCodeVersion(), status)
			wantCalls := 1
			if status == http.StatusNotFound {
				want = "OpenCode " + n.d.openCodeVersion() + " has no interrupt route"
				wantCalls = 2
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("want %q, got %v", want, err)
			}
			if len(calls) != wantCalls {
				t.Fatalf("calls %v, want %d", calls, wantCalls)
			}
		})
	}
}

func TestSessionStopUnknownSession(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n, _, _ := enterNode(t, g, "codex", protocol.SessionInfo{NativeID: "thread", Name: "shop", Status: "idle"}, nil, nil)
	_, err := cliClient(t, n).call("session_stop", map[string]string{"session_id": "s_elsewhere"})
	if err == nil || !strings.Contains(err.Error(), "not known") {
		t.Fatalf("unknown stop: %v", err)
	}
}

// A session in the owner's default tmux server must never be stopped, even
// though Enter could attach its pane. Only the private host may be queried.

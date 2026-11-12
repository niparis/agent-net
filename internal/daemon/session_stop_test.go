package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

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

func TestSessionStopTerminal(t *testing.T) {
	for _, h := range []string{"claude", "codex", "pi", "opencode"} {
		t.Run(h, func(t *testing.T) {
			pid, _ := startSleeper(t)
			g := startGateway(t, t.TempDir(), "")
			n, _, id := enterNode(t, g, h, protocol.SessionInfo{NativeID: "terminal", Name: "shop", PID: pid, Status: "idle", Mode: protocol.ModeForeground}, nil, nil)
			res, err := n.d.sessionStopLocal(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if res.Note != "Stopped shop. The conversation is kept." {
				t.Fatal(res.Note)
			}
			eventually(t, "harness ended", func() bool { _, live := procToken(pid); return !live })
		})
	}
}

func TestSessionStopChangedPID(t *testing.T) {
	pid, _ := startSleeper(t)
	g := startGateway(t, t.TempDir(), "")
	n, _, id := enterNode(t, g, "claude", protocol.SessionInfo{NativeID: "terminal", PID: pid}, nil, nil)
	ls := n.d.sessionByID(id)
	n.d.mu.Lock()
	ls.stopToken = "different run"
	n.d.mu.Unlock()
	if _, err := n.d.sessionStopLocal(context.Background(), id); err == nil {
		t.Fatal("accepted changed PID")
	}
	if _, live := procToken(pid); !live {
		t.Fatal("signalled changed PID")
	}
}

// A terminal session in the owner's own herdr is stopped by signalling its
// process; its pane is not closed (decision 0154).
func TestSessionStopTerminalHerdr(t *testing.T) {
	f := newFakeHerdr(t)
	pid := f.session(t, "pane-1")
	f.panes(t, `{"pane_id":"pane-1"}`)
	f.holds(t, "pane-1", pid)
	g := startGateway(t, t.TempDir(), "")
	n, _, id := enterNode(t, g, "claude", protocol.SessionInfo{NativeID: "terminal", PID: pid}, nil, nil)
	if _, err := n.d.sessionStopLocal(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the process ends", func() bool { _, live := procToken(pid); return !live })
	f.readOnly(t)
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
			if !strings.Contains(string(raw), "Closed shop space. The conversation is kept; press v to see ended sessions and Enter to reopen it.") {
				t.Fatalf("result %s", raw)
			}
			view, err := n.d.agentView(context.Background(), id)
			if err != nil || view.Online {
				t.Fatalf("closed session: %+v %v", view, err)
			}
			select {
			case got := <-calls:
				if status == "idle" {
					t.Fatal("idle session was interrupted")
				}
				action := "interrupt"
				if old {
					action = "abort"
				}
				if got != "POST /api/session/ses_owner/"+action {
					t.Fatalf("interrupt %q", got)
				}
			default:
				if status == "busy" {
					t.Fatal("no interrupt")
				}
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
	if !strings.Contains(string(raw), "Closed shop space. The conversation is kept; press v to see ended sessions and Enter to reopen it.") {
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

func TestSessionStopEscalates(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command("sh", "-c", "trap '' TERM; : > "+shellQuote(ready)+"; while :; do read line; done")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); cmd.Process.Kill(); cmd.Wait() }()
	eventually(t, "trap installed", func() bool { _, err := os.Stat(ready); return err == nil })
	token, _ := procToken(cmd.Process.Pid)
	if err := stopSessionProcess(context.Background(), cmd.Process.Pid, token, 80*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	eventually(t, "killed harness", func() bool { _, live := procToken(cmd.Process.Pid); return !live })
}

func TestSessionStopSignalTERM(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	token, _ := procToken(cmd.Process.Pid)
	if err := stopSessionProcess(context.Background(), cmd.Process.Pid, token, time.Second); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	if err == nil {
		t.Fatal("expected signal exit")
	}
	status := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !status.Signaled() || status.Signal() != syscall.SIGTERM {
		t.Fatalf("exit: %v", status)
	}
}

func TestSessionStopOpenCodeMissingServiceDoesNotSignal(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, mode := range []string{"", protocol.ModeBackground} {
		t.Run(mode, func(t *testing.T) {
			pid, _ := startSleeper(t)
			ls := &localSession{sessionID: "s_service", info: protocol.SessionInfo{
				Harness: "opencode", NativeID: "service", PID: pid, Mode: mode,
			}}
			d := &Daemon{cfg: Config{Hostname: "test-host"}, sessions: map[string]*localSession{"service": ls}}
			_, err := d.sessionStopLocal(context.Background(), ls.sessionID)
			_, live := procToken(pid)
			if !live {
				t.Error("shared service process was signalled")
			}
			want := "the OpenCode service is not running on " + d.hostName()
			if err == nil || err.Error() != want {
				t.Fatalf("want %q, got %v", want, err)
			}
		})
	}
}

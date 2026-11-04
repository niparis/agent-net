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
	fa.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "abcd1234-session", Name: "shop", Status: "idle", PID: 1})
	var id string
	eventually(t, "session registered", func() bool {
		for _, s := range n.d.Sessions() {
			id = s.SessionID
		}
		return id != ""
	})
	cliClient(t, n).mustCall("session_stop", map[string]string{"session_id": id})
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

func TestSessionStopOpenCodeService(t *testing.T) {
	for _, status := range []string{"busy", "idle"} {
		t.Run(status, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			n, _, id := enterNode(t, g, "opencode", protocol.SessionInfo{NativeID: "ses_owner", Name: "shop", Status: status, Cwd: "/src/shop space"}, nil, nil)
			state := t.TempDir()
			t.Setenv("XDG_STATE_HOME", state)
			calls := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				user, password, ok := r.BasicAuth()
				if !ok || user != "opencode" || password != "test-owner-password" {
					t.Errorf("missing service authentication")
				}
				if r.Header.Get("x-opencode-directory") != "%2Fsrc%2Fshop%20space" {
					t.Errorf("directory %q", r.Header.Get("x-opencode-directory"))
				}
				calls <- r.Method + " " + r.URL.Path
				fmt.Fprint(w, `{"data":true}`)
			}))
			defer server.Close()
			writeOwnerOpenCodeService(t, state, server.URL)
			raw := cliClient(t, n).mustCall("session_stop", map[string]string{"session_id": id})
			if !strings.Contains(string(raw), "stays in the service") {
				t.Fatalf("result %s", raw)
			}
			select {
			case got := <-calls:
				if got != "POST /api/session/ses_owner/abort" {
					t.Fatalf("abort %q", got)
				}
			default:
				t.Fatal("no abort")
			}
		})
	}
}

func TestSessionStopPiPrivateWindow(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n, _, id := enterNode(t, g, "pi", protocol.SessionInfo{NativeID: "pi_owner", Name: "shop", PID: 300, Status: "idle"}, nil, nil)
	fakeProcessTree(t, map[int]int{300: 200, 200: 1}, nil)
	args := fakeTmux(t, "200|%3|0")
	cliClient(t, n).mustCall("session_stop", map[string]string{"session_id": id})
	got, err := os.ReadFile(args)
	if err != nil || !strings.Contains(string(got), "-L agent-net list-panes -a") || !strings.Contains(string(got), "-L agent-net kill-window -t %3") {
		t.Fatalf("tmux commands %q: %v", got, err)
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
func TestSessionStopPiRefusesOwnerTmux(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n, _, id := enterNode(t, g, "pi", protocol.SessionInfo{NativeID: "pi_terminal", Name: "shop", PID: 300, Status: "idle"}, nil, nil)
	fakeProcessTree(t, map[int]int{300: 200, 200: 1}, nil)
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$@\" >> " + shellQuote(args) + "\nif [ \"$1\" != -L ]; then echo '200|%3|0'; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, err := cliClient(t, n).call("session_stop", map[string]string{"session_id": id})
	if err == nil || !strings.Contains(err.Error(), "runs in your terminal") {
		t.Fatalf("terminal stop: %v", err)
	}
	got, err := os.ReadFile(args)
	if err != nil || !strings.HasPrefix(string(got), "-L agent-net list-panes") || strings.Contains(string(got), "kill-window") {
		t.Fatalf("tmux commands %q: %v", got, err)
	}
}

func TestSessionStopPiUnknownPIDDoesNotQueryTmux(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n, _, id := enterNode(t, g, "pi", protocol.SessionInfo{NativeID: "pi_unknown", Name: "shop", PID: 1, Status: "idle"}, nil, nil)
	args := fakeTmux(t, "200|%3|0")
	_, err := cliClient(t, n).call("session_stop", map[string]string{"session_id": id})
	if err == nil || !strings.Contains(err.Error(), "runs in your terminal") {
		t.Fatalf("stop: %v", err)
	}
	if _, err := os.Stat(args); !os.IsNotExist(err) {
		t.Fatalf("queried tmux without a known session process: %v", err)
	}
}

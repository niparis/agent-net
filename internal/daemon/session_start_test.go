package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSessionStartServiceHosts(t *testing.T) {
	fakeProcessTree(t, map[int]int{300: 200, 200: 100, 400: 1}, nil)
	for _, args := range [][]string{{"/bin/codex", "app-server"}, {"/bin/opencode", "serve"}} {
		processArgs = func(pid int) []string {
			if pid == 100 {
				return args
			}
			return nil
		}
		d := &Daemon{}
		d.inSession = d.insideSession
		lc := &localConn{d: d, client: "cli", peerPID: 300}
		for _, method := range []string{"session_start", "session_start_get", "session_resume", "session_stop"} {
			_, handled, err := lc.dispatchOperator(context.Background(), method, json.RawMessage(`{"project":"shop","harness":"opencode"}`))
			if !handled || err == nil || !strings.Contains(err.Error(), "inside an agent session") {
				t.Errorf("%v %s: handled=%v err=%v", args, method, handled, err)
			}
		}
	}
}

func TestSessionStartOwnerGate(t *testing.T) {
	for _, method := range []string{"session_start", "session_start_get", "session_resume", "session_stop"} {
		for _, client := range []string{"mcp", "pi", "opencode", "cli"} {
			d := &Daemon{inSession: func(int) bool { return true }}
			lc := &localConn{d: d, client: client}
			_, handled, err := lc.dispatchOperator(context.Background(), method, json.RawMessage(`{"project":"shop","harness":"opencode"}`))
			if !handled || err == nil || !(strings.Contains(err.Error(), "only available to the owner") || strings.Contains(err.Error(), "inside an agent session")) {
				t.Errorf("%s client %s: handled=%v err=%v", method, client, handled, err)
			}
		}
	}
}

type sessionStartTestResult struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	SessionID string `json:"session_id"`
	NativeID  string `json:"native_id"`
	Path      string `json:"path"`
	Error     string `json:"error"`
}

func TestSessionStartClaude(t *testing.T) {
	for _, withMessage := range []bool{false, true} {
		name := "default"
		if withMessage {
			name = "model-and-message"
		}
		t.Run(name, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			c := startClaudeNode(t, g, shortSet)
			repo := newRepo(t, "shop")
			c.register(t, repo)
			trustClaudeFolder(t, repo)
			t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", c.sessions)
			req := map[string]any{"project": "shop", "harness": "claude"}
			if withMessage {
				c.register(t, newRepo(t, "chosen"))
				req["agent_id"] = "chosen"
				req["model"] = "haiku"
				req["first_message"] = "Inspect this project"
			}
			var r sessionStartTestResult
			mustJSON(t, c.cli.mustCall("session_start", req), &r)
			if r.Status != "starting" || r.ID == "" || r.SessionID != "" {
				t.Fatalf("initial result: %+v", r)
			}
			t.Cleanup(func() {
				raw, _ := os.ReadFile(filepath.Join(repo, "fake-claude.json"))
				var rec fakeClaudeRecord
				if json.Unmarshal(raw, &rec) == nil && rec.Pgid > 1 {
					syscall.Kill(-rec.Pgid, syscall.SIGKILL)
				}
			})
			eventually(t, "registered owner session", func() bool {
				mustJSON(t, c.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
				if r.Status == "failed" {
					t.Fatalf("start failed: %+v", r)
				}
				return r.Status == "started" && r.SessionID != ""
			})
			rec := readClaude(t, repo)
			if rec.Cwd != repo || r.Path != repo || r.NativeID != rec.Session {
				t.Fatalf("record %+v; launch %+v", r, rec)
			}
			if rec.Args[0] != "--bg" || argValue(rec.Args, "--name") != "shop" {
				t.Errorf("args %v", rec.Args)
			}
			if withMessage {
				if argValue(rec.Args, "--model") != "haiku" {
					t.Errorf("args %v", rec.Args)
				}
			} else if argValue(rec.Args, "--model") != "" {
				t.Errorf("default args %v", rec.Args)
			}
			for _, flag := range []string{"--mcp-config", "--resume"} {
				if argValue(rec.Args, flag) != "" {
					t.Errorf("worker flag %s in %v", flag, rec.Args)
				}
			}
			settingsEnv, _ := rec.Settings["env"].(map[string]any)
			if rec.Env["CLAUDE_CODE_DISABLE_MOUSE"] != "0" || settingsEnv["CLAUDE_CODE_DISABLE_MOUSE"] != "0" || rec.Env["AGENT_NET_WORKER"] != "" {
				t.Errorf("owner environment: settings %v session %v", rec.Settings, rec.Env)
			}
			if rec.CommandEnv["CLAUDE_CODE_DISABLE_MOUSE"] != "" {
				t.Errorf("launch command carries the mouse setting: %v", rec.CommandEnv)
			}
			noFlagThatSkipsPermissions(t, rec.Args)
			a := c.agentOf(t, r.SessionID)
			wantAgent := "shop"
			if withMessage {
				wantAgent = "chosen"
			}
			if a == nil || a.Worker != nil || a.AgentID != wantAgent {
				t.Fatalf("owner session: %+v", a)
			}
			if withMessage {
				eventually(t, "first message after Agent registration", func() bool {
					for _, m := range c.adapter.deliveredTo(r.SessionID) {
						if m.Body == "Inspect this project" {
							return c.agentOf(t, r.SessionID).AgentID == "chosen"
						}
					}
					return false
				})
			}
			rows, _ := c.d.st.workers()
			if len(rows) != 0 {
				t.Errorf("worker records %+v", rows)
			}
			if ws, err := c.d.workersView(context.Background(), protocol.WorkersQuery{Recent: true}); err != nil || len(ws.Workers) != 0 {
				t.Errorf("workers: %+v %v", ws, err)
			}
			for _, path := range []string{filepath.Join(repo, ".agent-net", "worker.md"), filepath.Join(repo, ".worktrees")} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("created worker artifact %s: %v", path, err)
				}
			}
			if got := gitOutT(t, repo, "worktree", "list", "--porcelain"); strings.Count(got, "worktree ") != 1 {
				t.Errorf("worktrees: %s", got)
			}
			if len(c.notices(t)) != 0 {
				t.Errorf("owner start sent a worker notice")
			}
			c.d.checkWorkers(context.Background())
			c.d.cleanupWorkers(context.Background())
			if !alive(rec.Pid) || len(c.stops()) != 0 {
				t.Errorf("owner session was cleaned up")
			}
			c.d.Stop()
			if !alive(rec.Pid) {
				t.Error("daemon stop ended owner session")
			}
		})
	}
}

// Decision 0128: the wheel scrolls every Claude session agent-net starts. The
// setting must be in the launch settings, because Claude's background service
// gives every session it starts later its own environment. The launch command
// must not carry it either way.
func TestClaudeSessionLaunchSettingsKeepMouseTrackingOn(t *testing.T) {
	t.Setenv("CLAUDE_CODE_DISABLE_MOUSE", "1") // the background service has it off
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	c.register(t, repo)
	trustClaudeFolder(t, repo)
	t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", c.sessions)
	var r sessionStartTestResult
	mustJSON(t, c.cli.mustCall("session_start", map[string]any{"project": "shop", "harness": "claude"}), &r)
	t.Cleanup(func() { killFakeClaude(t, repo) })
	eventually(t, "started owner session", func() bool {
		mustJSON(t, c.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		if r.Status == "failed" {
			t.Fatalf("start failed: %+v", r)
		}
		return r.Status == "started"
	})
	rec := readClaude(t, repo)
	settingsEnv, _ := rec.Settings["env"].(map[string]any)
	if settingsEnv["CLAUDE_CODE_DISABLE_MOUSE"] != "0" {
		t.Fatalf("launch settings must turn mouse tracking on: %v", rec.Settings)
	}
	if rec.Env["CLAUDE_CODE_DISABLE_MOUSE"] != "0" {
		t.Fatalf("session mouse setting = %q, want 0", rec.Env["CLAUDE_CODE_DISABLE_MOUSE"])
	}
	if rec.CommandEnv["CLAUDE_CODE_DISABLE_MOUSE"] != "" {
		t.Fatalf("launch command must not pass the variable on: %q", rec.CommandEnv["CLAUDE_CODE_DISABLE_MOUSE"])
	}
}

func TestSessionResumeClaude(t *testing.T) {
	for _, copySession := range []bool{false, true} {
		t.Run(fmt.Sprint("copy=", copySession), func(t *testing.T) {
			if copySession {
				t.Setenv("FAKE_CLAUDE_NEW_RESUME_ID", "1")
			}
			g := startGateway(t, t.TempDir(), "")
			c := startClaudeNode(t, g, shortSet)
			t.Setenv("AGENT_NET_WORKER", "must-not-reach-owner")
			repo := newRepo(t, "shop")
			native := "11111111-0000-4000-8000-000000000000"
			id, err := g.gw.Store().UpsertSession(c.d.cfg.NodeID, protocol.SessionInfo{Harness: "claude", NativeID: native, Cwd: repo, Name: "shop"})
			if err != nil {
				t.Fatal(err)
			}
			if err := g.gw.Store().SetSessionOffline(c.d.cfg.NodeID, id); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				raw, _ := os.ReadFile(filepath.Join(repo, "fake-claude.json"))
				var rec fakeClaudeRecord
				if json.Unmarshal(raw, &rec) == nil && rec.Pgid > 1 {
					syscall.Kill(-rec.Pgid, syscall.SIGKILL)
				}
			})
			var r sessionStartTestResult
			mustJSON(t, c.cli.mustCall("session_resume", map[string]string{"session_id": id}), &r)
			if a, ok, err := g.gw.Store().Agent(id); err != nil || !ok || a.OwnerUses != 1 {
				t.Fatalf("resume owner use: %+v %v", a, err)
			}
			eventually(t, "resumed session", func() bool {
				mustJSON(t, c.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
				if r.Status == "failed" {
					t.Fatalf("resume: %+v", r)
				}
				return r.Status == "started"
			})
			rec := readClaude(t, repo)
			if argValue(rec.Args, "--resume") != native || rec.Cwd != repo || r.NativeID != rec.Session || (r.NativeID != native) != copySession {
				t.Fatalf("resume %+v record %+v", r, rec)
			}
			settingsEnv, _ := rec.Settings["env"].(map[string]any)
			if rec.Env["CLAUDE_CODE_DISABLE_MOUSE"] != "0" || settingsEnv["CLAUDE_CODE_DISABLE_MOUSE"] != "0" || rec.Env["AGENT_NET_WORKER"] != "" || argValue(rec.Args, "--mcp-config") != "" {
				t.Fatalf("owner settings: %+v", rec)
			}
			if rec.CommandEnv["CLAUDE_CODE_DISABLE_MOUSE"] != "" {
				t.Fatalf("launch command carries the mouse setting: %v", rec.CommandEnv)
			}
		})
	}
}

func TestSessionResumeMissingFolder(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	id, err := g.gw.Store().UpsertSession(c.d.cfg.NodeID, protocol.SessionInfo{Harness: "claude", NativeID: "old", Cwd: filepath.Join(t.TempDir(), "gone")})
	if err != nil {
		t.Fatal(err)
	}
	if err = g.gw.Store().SetSessionOffline(c.d.cfg.NodeID, id); err != nil {
		t.Fatal(err)
	}
	_, err = c.cli.call("session_resume", map[string]string{"session_id": id})
	if err == nil || !strings.Contains(err.Error(), "folder is gone") {
		t.Fatalf("missing folder: %v", err)
	}
}

// The fake owner's service uses the real daemon/plugin hello path, without a
// private worker server or a model call.
func TestSessionStartOpenCode(t *testing.T) {
	for _, message := range []string{"", "Inspect this project"} {
		t.Run(fmt.Sprintf("message=%v", message != ""), func(t *testing.T) {
			state := t.TempDir()
			t.Setenv("XDG_STATE_HOME", state)
			g := startGateway(t, t.TempDir(), "")
			n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} })
			repo := newRepo(t, "shop space")
			n.register(t, repo)
			n.register(t, newRepo(t, "chosen"))
			var creates, prompts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				user, pass, ok := r.BasicAuth()
				if !ok || user != "opencode" || pass != "test-owner-password" {
					t.Error("missing service authentication")
					w.WriteHeader(401)
					return
				}
				if r.Method == "POST" && r.Header.Get("x-opencode-directory") != strings.ReplaceAll(url.QueryEscape(repo), "+", "%20") {
					t.Errorf("directory header %q", r.Header.Get("x-opencode-directory"))
				}
				switch r.Method + " " + r.URL.Path {
				case "POST /api/session":
					var body struct {
						Title string
						Model struct{ ProviderID, ID, Variant string }
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.Title != "shop space" || body.Model.ProviderID != "test" || body.Model.ID != "model" || body.Model.Variant != "high" {
						t.Errorf("create: %+v", body)
					}
					creates.Add(1)
					helloOpenCode(t, n.node, "ses_owner", repo)
					fmt.Fprint(w, `{"data":{"id":"ses_owner"}}`)
				case "POST /api/session/ses_owner/prompt":
					a := n.agentOf(t, n.d.SessionID("opencode", "ses_owner"))
					if a == nil || a.AgentID != "chosen" {
						t.Errorf("first prompt identity: %+v", a)
					}
					var body struct{ Text, Delivery string }
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.Text != message || body.Delivery != "queue" {
						t.Errorf("prompt: %+v", body)
					}
					prompts.Add(1)
					fmt.Fprint(w, `{"data":{}}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			writeOwnerOpenCodeService(t, state, server.URL)
			var r protocol.StartedSession
			mustJSON(t, n.cli.mustCall("session_start", protocol.SessionStartRequest{Project: repo, Harness: "opencode", AgentID: "chosen", Model: "test/model:high", FirstMessage: message}), &r)
			if r.Status != "starting" {
				t.Fatalf("initial result: %+v", r)
			}
			eventually(t, "owner OpenCode session registered", func() bool {
				mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
				if r.Status == "failed" {
					t.Fatalf("start: %+v", r)
				}
				return r.Status == "started"
			})
			wantPrompts := int32(0)
			if message != "" {
				wantPrompts = 1
			}
			if creates.Load() != 1 || prompts.Load() != wantPrompts || r.NativeID != "ses_owner" || r.SessionID == "" || r.Path != repo {
				t.Fatalf("result %+v creates %d prompts %d", r, creates.Load(), prompts.Load())
			}
			a := n.agentOf(t, r.SessionID)
			if a == nil || a.Worker != nil {
				t.Fatalf("owner row: %+v", a)
			}
			plan, err := n.d.localEnter(context.Background(), protocol.EnterRequest{SessionID: r.SessionID}, false)
			if err != nil || len(plan.Argv) != 3 || plan.Argv[1] != "--session" || plan.Argv[2] != "ses_owner" {
				t.Fatalf("enter plan: %+v %v", plan, err)
			}
			rows, _ := n.d.st.workers()
			if len(rows) != 0 {
				t.Fatalf("worker records: %+v", rows)
			}
		})
	}
}

func writeOwnerOpenCodeService(t *testing.T, state, address string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(state, "opencode"), 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"url": address, "password": "test-owner-password"})
	if err := os.WriteFile(filepath.Join(state, "opencode", "service.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSessionStartOpenCodeServiceDown(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			state := t.TempDir()
			t.Setenv("XDG_STATE_HOME", state)
			g := startGateway(t, t.TempDir(), "")
			n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} })
			repo := newRepo(t, "shop")
			n.register(t, repo)
			if present {
				server := httptest.NewServer(http.NotFoundHandler())
				server.Close()
				writeOwnerOpenCodeService(t, state, server.URL)
			}
			_, err := n.cli.call("session_start", protocol.SessionStartRequest{Project: repo, Harness: "opencode"})
			want := "the OpenCode service is not running on " + n.d.cfg.Hostname
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("refusal: %v, want %q", err, want)
			}
		})
	}
}

func TestSessionStartOpenCodeServiceFailuresKeepPasswordPrivate(t *testing.T) {
	for _, failure := range []string{"create", "malformed", "redirect", "prompt"} {
		t.Run(failure, func(t *testing.T) {
			state := t.TempDir()
			t.Setenv("XDG_STATE_HOME", state)
			g := startGateway(t, t.TempDir(), "")
			n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} })
			repo := newRepo(t, "shop")
			n.register(t, repo)
			var redirected atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/openapi.json" && failure != "redirect" {
					http.NotFound(w, r)
					return
				}
				if r.URL.Path == "/api/session" && failure == "prompt" {
					fmt.Fprint(w, `{"data":{"id":"ses_owner"}}`)
					return
				}
				switch failure {
				case "malformed":
					fmt.Fprint(w, "test-owner-password")
				case "redirect":
					http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
				default:
					http.Error(w, "test-owner-password", http.StatusInternalServerError)
				}
			}))
			defer server.Close()
			writeOwnerOpenCodeService(t, state, server.URL)
			raw, err := n.cli.call("session_start", protocol.SessionStartRequest{Project: repo, Harness: "opencode", FirstMessage: "Hello"})
			if failure == "prompt" {
				if err != nil {
					t.Fatal(err)
				}
				var r protocol.StartedSession
				mustJSON(t, raw, &r)
				eventually(t, "prompt failure", func() bool {
					mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
					return r.Status != "starting"
				})
				if r.Status != "failed" || !strings.Contains(r.Error, "first message") {
					t.Fatalf("result: %+v", r)
				}
				if lines := n.logs.lines("event=session.start_failed", "start="+r.ID); len(lines) != 1 {
					t.Fatalf("want one failure log line for the start, got %q", n.logs.String())
				}
				raw, _ = json.Marshal(r)
			} else if err == nil {
				t.Fatal("service failure accepted")
			}
			if strings.Contains(string(raw)+fmt.Sprint(err), "test-owner-password") {
				t.Fatal("password in result")
			}
			for _, line := range n.logs.lines() {
				if strings.Contains(line, "test-owner-password") {
					t.Fatal("password in logs")
				}
			}
			if redirected.Load() != 0 {
				t.Fatal("followed service redirect")
			}
			if len(n.notices(t)) != 0 {
				t.Fatal("owner start sent a worker notice")
			}
		})
	}
}

func TestSessionStartOpenCodeRejectsUnsafeServiceAddress(t *testing.T) {
	for _, address := range []string{"http://192.0.2.1:1234", "http://localhost:1234", "http://user:pass@127.0.0.1:1234", "https://127.0.0.1:1234", "http://127.0.0.1:1234/path", "http://127.0.0.1:1234?token=x"} {
		t.Run(address, func(t *testing.T) {
			state := t.TempDir()
			t.Setenv("XDG_STATE_HOME", state)
			writeOwnerOpenCodeService(t, state, address)
			if _, err := ownerOpenCodeService(); err == nil {
				t.Fatal("unsafe service address accepted")
			}
		})
	}
}

func TestSessionStartCodexPlan(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "codex")
	n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = bins })
	repo := newRepo(t, "shop space")
	n.register(t, repo)
	for _, tc := range []struct {
		model, prompt string
		args          []string
	}{
		{"", "", []string{bins["codex"]}},
		{"model-x", "Inspect this project", []string{bins["codex"], "-m", "model-x", "Inspect this project"}},
	} {
		var r struct {
			Status string             `json:"status"`
			Plan   protocol.EnterPlan `json:"enter_plan"`
		}
		mustJSON(t, n.cli.mustCall("session_start", protocol.SessionStartRequest{Project: repo, Harness: "codex", Model: tc.model, FirstMessage: tc.prompt}), &r)
		if r.Status != "started" || r.Plan.Dir != repo || !reflect.DeepEqual(r.Plan.Argv, tc.args) {
			t.Fatalf("plan: %+v, want %v in %s", r, tc.args, repo)
		}
	}
}

// A running shared server must own the thread. Otherwise Codex may silently
// start an embedded server, whose MCP child the daemon refuses.
func TestSessionStartCodexUsesConnectedServer(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	bins := fakeBin(t, "codex")
	n := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.HarnessPaths = bins
		c.CodexHome = f.home
		c.CodexInterval = 50 * time.Millisecond
	})
	eventually(t, "connected to shared server", func() bool {
		return n.d.adapters["codex"].(*codexAdapter).serverPID() != 0
	})
	repo := newRepo(t, "shop")
	n.register(t, repo)
	var r struct {
		Plan protocol.EnterPlan `json:"enter_plan"`
	}
	mustJSON(t, n.cli.mustCall("session_start", protocol.SessionStartRequest{
		Project: repo, Harness: "codex", Model: "model-x", FirstMessage: "hello",
	}), &r)
	want := []string{bins["codex"], "--remote", "unix://", "-m", "model-x", "hello"}
	if !reflect.DeepEqual(r.Plan.Argv, want) {
		t.Fatalf("plan: %v, want %v", r.Plan.Argv, want)
	}
}

func TestSessionStartAgentRefusedBeforeLaunch(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.HarnessPaths = map[string]string{"pi": "/fake/pi", "codex": "/fake/codex", "opencode": "/fake/opencode"}
	})
	n.register(t, newRepo(t, "shop"))
	for _, tc := range []struct{ harness, agent, want string }{
		{"pi", "missing", "unknown or tombstoned"},
	} {
		_, err := n.cli.call("session_start", protocol.SessionStartRequest{Project: "shop", Harness: tc.harness, AgentID: tc.agent})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v", tc.harness, err)
		}
	}
}

func TestSessionStartCodexAgent(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	bins := fakeBin(t, "codex")
	n := startWorkerNode(t, g, shortSet, func(c *Config) { c.CodexHome = f.home; c.CodexInterval = 20 * time.Millisecond; c.HarnessPaths = bins })
	n.register(t, newRepo(t, "shop"))
	n.register(t, newRepo(t, "chosen"))
	var r protocol.StartedSession
	mustJSON(t, n.cli.mustCall("session_start", protocol.SessionStartRequest{Project: "shop", Harness: "codex", AgentID: "chosen", FirstMessage: "Inspect this project"}), &r)
	if r.NativeID == "" || r.SessionID == "" || r.Status != "started" {
		t.Fatalf("start: %+v", r)
	}
	a := n.agentOf(t, r.SessionID)
	if a == nil || a.AgentID != "chosen" {
		t.Fatalf("first identity: %+v", a)
	}
	if r.EnterPlan == nil || !reflect.DeepEqual(r.EnterPlan.Argv, []string{bins["codex"], "--remote", "unix://", "resume", r.NativeID, "Inspect this project"}) {
		t.Fatalf("plan: %+v", r.EnterPlan)
	}
}

func TestCTOSessionStartChosenAgentProposal(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	c.register(t, newRepo(t, "chosen"))
	path := filepath.Join(home, "code", "new-project")
	args, _ := json.Marshal(map[string]any{"action": "create", "path": path, "harness": "claude", "agent_id": "chosen", "first_message": "hello"})
	if _, err := legacyProposalFixture(context.Background(), c.d, "project_change", args); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("started before approval: %v", err)
	}
	items, err := c.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("proposals: %+v %v", items, err)
	}
	if err = c.d.proposalRunner().decide(context.Background(), items[0].ID, true); err != nil {
		t.Fatal(err)
	}
	items, err = c.d.proposalRunner().list()
	if err != nil {
		t.Fatal(err)
	}
	var result protocol.StartedSession
	if err = json.Unmarshal(items[0].Result, &result); err != nil {
		t.Fatal(err)
	}
	result = waitSessionStartPath(t, c.workerNode, result)
	t.Cleanup(func() { killFakeClaude(t, path) })
	if result.Status != "started" {
		t.Fatalf("start: %+v", result)
	}
	if a := c.agentOf(t, result.SessionID); a == nil || a.AgentID != "chosen" {
		t.Fatalf("approved Agent: %+v", a)
	}
}

func TestSessionStartCodexUnavailableDoesNotHoldUnrelatedSessions(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "codex")
	n := startWorkerNode(t, g, shortSet, func(c *Config) { c.CodexHome = t.TempDir(); c.HarnessPaths = bins })
	repo := newRepo(t, "shop")
	n.register(t, repo)
	n.register(t, newRepo(t, "chosen"))
	if _, err := n.cli.call("session_start", protocol.SessionStartRequest{Project: "shop", Harness: "codex", AgentID: "chosen"}); err == nil {
		t.Fatal("started without app server")
	}
	id, err := g.gw.Store().UpsertSession(n.d.NodeID(), protocol.SessionInfo{Harness: "codex", NativeID: "manual", Cwd: repo, Repo: protocol.RepoInfo{Project: "shop"}})
	if err != nil {
		t.Fatal(err)
	}
	if a := n.agentOf(t, id); a == nil || a.AgentID != "shop" {
		t.Fatalf("failed preflight left a hold: %+v", a)
	}
}

func TestSessionStartRemoteAgentRejectsOldGateway(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var relayed atomic.Bool
	n := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.testGatewayCall = func(_ context.Context, method string, _ any) (json.RawMessage, bool, error) {
			if method == "session.agent.reserve" {
				return nil, true, errors.New("unknown frame type session.agent.reserve")
			}
			if method == protocol.FrameSessionStart {
				relayed.Store(true)
				return json.RawMessage(`{"id":"start_old","status":"started","node_id":"remote"}`), true, nil
			}
			return nil, false, nil
		}
	})
	_, err := n.cli.call("session_start", protocol.SessionStartRequest{Project: "shop", NodeID: "remote", Harness: "pi", AgentID: "chosen"})
	if err == nil || !strings.Contains(err.Error(), "gateway is too old") || relayed.Load() {
		t.Fatalf("selection silently dropped: err=%v relayed=%v", err, relayed.Load())
	}
}

func TestSessionStartAgentFailureReportsAndNextStarts(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	c.register(t, repo)
	trustClaudeFolder(t, repo)
	c.register(t, newRepo(t, "chosen"))
	t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", c.sessions)
	t.Setenv(envFakeStartFails, "1")
	var r protocol.StartedSession
	req := protocol.SessionStartRequest{Project: "shop", Harness: "claude", AgentID: "chosen"}
	mustJSON(t, c.cli.mustCall("session_start", req), &r)
	eventually(t, "failed selected start", func() bool {
		mustJSON(t, c.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		return r.Status == "failed"
	})
	items, err := inboxAndLog(g.gw.Store())
	if err != nil || len(items) != 1 {
		t.Fatalf("failed-start Inbox: %+v %v", items, err)
	}
	t.Setenv(envFakeStartFails, "")
	mustJSON(t, c.cli.mustCall("session_start", req), &r)
	t.Cleanup(func() { killFakeClaude(t, repo) })
	eventually(t, "next selected start", func() bool {
		mustJSON(t, c.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		if r.Status == "failed" {
			t.Fatalf("next start: %+v", r)
		}
		return r.Status == "started"
	})
	if a := c.agentOf(t, r.SessionID); a == nil || a.AgentID != "chosen" {
		t.Fatalf("next Agent: %+v", a)
	}
	items, err = inboxAndLog(g.gw.Store())
	if err != nil || len(items) != 1 {
		t.Fatalf("duplicate Inbox: %+v %v", items, err)
	}
}

// A start the CTO asked for can fail after the daemon answered "starting". The
// failure must not be silent: one daemon log line, a gateway event, and a notice
// to the owner's TUI with the error.
func TestSessionStartAsyncFailureLogsRecordsAndNotifies(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	repo := newHomeRepo(t, home, "code/garden")
	c.register(t, repo)
	trustClaudeFolder(t, repo)
	notices := herdrNoticeChannel(t, c.node)
	t.Setenv(envFakeStartFails, "1")
	args, _ := json.Marshal(map[string]any{"action": "session_start", "project": "garden", "harness": "claude"})
	if _, err := legacyProposalFixture(context.Background(), c.d, "session_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := c.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("proposals: %+v %v", items, err)
	}
	if err := c.d.proposalRunner().decide(context.Background(), items[0].ID, true); err != nil {
		t.Fatal(err)
	}
	items, err = c.d.proposalRunner().list()
	if err != nil {
		t.Fatal(err)
	}
	var r protocol.StartedSession
	if err := json.Unmarshal(items[0].Result, &r); err != nil || r.ID == "" {
		t.Fatalf("approved start: %s %v", items[0].Result, err)
	}
	eventually(t, "the failed start", func() bool {
		mustJSON(t, c.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		return r.Status == "failed"
	})
	if !strings.Contains(r.Error, "does not trust") {
		t.Fatalf("failed start error %q", r.Error)
	}
	if lines := c.logs.lines("event=session.start_failed", "start="+r.ID); len(lines) != 1 {
		t.Fatalf("want one failure log line, got %q", c.logs.String())
	}
	events, err := g.gw.Store().Events("session.start_failed")
	if err != nil || len(events) != 1 || !strings.Contains(events[0], r.ID) || !strings.Contains(events[0], "does not trust") {
		t.Fatalf("failure event: %v %v", events, err)
	}
	notice := waitHerdrNotice(t, notices)
	if notice["daemon_notice"] != true || !strings.Contains(fmt.Sprint(notice["text"]), "garden") ||
		!strings.Contains(fmt.Sprint(notice["text"]), "does not trust") {
		t.Fatalf("owner notice %+v", notice)
	}
}

func TestSessionResumeOpenCodeRegistered(t *testing.T) {
	_, viewerLog := fakeOpenHerdr(t)
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "opencode")
	n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = bins })
	repo := newRepo(t, "shop")
	n.register(t, repo)
	native := "ses_resume"
	id, err := g.gw.Store().UpsertSession(n.d.NodeID(), protocol.SessionInfo{Harness: "opencode", NativeID: native, Cwd: repo})
	if err != nil {
		t.Fatal(err)
	}
	g.gw.Store().SetSessionOffline(n.d.NodeID(), id)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/openapi.json" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/api/location" {
			// An empty daemon also boots shared-service plugin discovery.
			fmt.Fprint(w, `{"data":{}}`)
			return
		}
		if r.URL.Path == "/api/session/active" {
			fmt.Fprint(w, `{"data":[]}`)
			return
		}
		if r.Method != "GET" || r.URL.Path != "/api/session/"+native || r.Header.Get("x-opencode-directory") != strings.ReplaceAll(url.QueryEscape(repo), "+", "%20") {
			t.Errorf("unexpected load: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"data":{"id":%q}}`, native)
	}))
	defer server.Close()
	writeOwnerOpenCodeService(t, state, server.URL)
	var r protocol.StartedSession
	mustJSON(t, n.cli.mustCall("session_resume", map[string]string{"session_id": id, "herdr_socket": "/tmp/herdr-test.sock", "herdr_pane_id": "w1:p0"}), &r)
	if r.Status != "started" || r.EnterPlan == nil || !r.EnterPlan.OpenedHerdr {
		t.Fatalf("viewer not opened: %+v", r)
	}
	requireHerdrOpened(t, viewerLog, "shop", bins["opencode"], "--session", native)
	// Only viewing emits session.viewed; reading through HTTP does not register.
	helloOpenCode(t, n.node, native, repo)
	eventually(t, "resume registered", func() bool {
		mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		return r.SessionID != ""
	})
	a := n.agentOf(t, r.SessionID)
	if a == nil || !a.Online || a.NativeID != native {
		t.Fatalf("not online: %+v", a)
	}
}
func TestSessionResumeCodexRegistered(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	bins := fakeBin(t, "codex")
	n := startWorkerNode(t, g, shortSet, func(c *Config) { c.CodexHome = f.home; c.CodexInterval = 20 * time.Millisecond; c.HarnessPaths = bins })
	repo := newRepo(t, "shop")
	n.register(t, repo)
	native := "thread_resume"
	id, err := g.gw.Store().UpsertSession(n.d.NodeID(), protocol.SessionInfo{Harness: "codex", NativeID: native, Cwd: repo})
	if err != nil {
		t.Fatal(err)
	}
	g.gw.Store().SetSessionOffline(n.d.NodeID(), id)
	var r protocol.StartedSession
	mustJSON(t, n.cli.mustCall("session_resume", map[string]string{"session_id": id}), &r)
	a := n.agentOf(t, r.SessionID)
	if a == nil || !a.Online || a.NativeID != native {
		t.Fatalf("not online: %+v", a)
	}
	if r.EnterPlan == nil || r.EnterPlan.Env["CODEX_HOME"] != f.home {
		t.Fatalf("home missing: %+v", r.EnterPlan)
	}
}

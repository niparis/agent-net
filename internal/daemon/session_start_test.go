package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

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
		for _, method := range []string{"session_start", "session_start_get", "session_resume"} {
			_, handled, err := lc.dispatchOperator(context.Background(), method, json.RawMessage(`{}`))
			if !handled || err == nil || !strings.Contains(err.Error(), "inside an agent session") {
				t.Errorf("%v %s: handled=%v err=%v", args, method, handled, err)
			}
		}
	}
}

func TestSessionStartOwnerGate(t *testing.T) {
	for _, method := range []string{"session_start", "session_start_get", "session_resume"} {
		for _, client := range []string{"mcp", "pi", "opencode", "cli"} {
			d := &Daemon{inSession: func(int) bool { return true }}
			lc := &localConn{d: d, client: client}
			_, handled, err := lc.dispatchOperator(context.Background(), method, json.RawMessage(`{}`))
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
			t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", c.sessions)
			req := map[string]any{"project": "shop", "harness": "claude"}
			if withMessage {
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
				if argValue(rec.Args, "--model") != "haiku" || rec.Args[len(rec.Args)-1] != "Inspect this project" {
					t.Errorf("args %v", rec.Args)
				}
			} else if argValue(rec.Args, "--model") != "" {
				t.Errorf("default args %v", rec.Args)
			}
			for _, flag := range []string{"--settings", "--mcp-config", "--resume"} {
				if argValue(rec.Args, flag) != "" {
					t.Errorf("worker flag %s in %v", flag, rec.Args)
				}
			}
			if rec.Env["CLAUDE_CODE_DISABLE_MOUSE"] != "1" || rec.Env["AGENT_NET_WORKER"] != "" {
				t.Errorf("owner environment: %v", rec.Env)
			}
			noFlagThatSkipsPermissions(t, rec.Args)
			a := c.agentOf(t, r.SessionID)
			if a == nil || a.Worker != nil {
				t.Fatalf("owner session: %+v", a)
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
			if rec.Env["CLAUDE_CODE_DISABLE_MOUSE"] != "1" || rec.Env["AGENT_NET_WORKER"] != "" || argValue(rec.Args, "--mcp-config") != "" || argValue(rec.Args, "--settings") != "" {
				t.Fatalf("owner settings: %+v", rec)
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

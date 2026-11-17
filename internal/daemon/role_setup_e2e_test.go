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
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/daemon/ctoskill"
	"github.com/niparis/agent-net/internal/protocol"
)

// noClaudeMachine points every agent-net path at a temp home and puts an empty
// folder first on PATH, so the test cannot reach a real Claude Code or the
// owner's setup. It returns the folder for the fake harness wrappers.
func noClaudeMachine(t *testing.T) (home, binDir string) {
	t.Helper()
	home = shortDir(t)
	binDir = filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "xdg-state"))
	t.Setenv("AGENT_NET_CONFIG_DIR", filepath.Join(home, "config"))
	t.Setenv("AGENT_NET_STATE_DIR", filepath.Join(home, "state"))
	t.Setenv("AGENT_NET_SOCKET", filepath.Join(home, "daemon.sock"))
	t.Setenv("AGENT_NET_GATEWAY_DATA", filepath.Join(home, "gateway"))
	t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", filepath.Join(home, "claude"))
	t.Setenv("FAKE_ROLE_BINARY", os.Args[0])
	if _, err := exec.LookPath("claude"); err == nil {
		t.Fatal("Claude is on the isolated PATH")
	}
	return home, binDir
}

// fakeHarnessWrapper answers the one-shot hello probe itself and hands every
// other call to the fake harness in this test binary.
func fakeHarnessWrapper(t *testing.T, binDir, name, probe string) string {
	t.Helper()
	bin := filepath.Join(binDir, name)
	script := "#!/bin/sh\nif [ \"$1\" = " + map[string]string{"codex": "exec", "opencode": "run"}[name] + " ]; then\n" + probe + "\nexit 0\nfi\nexec \"$FAKE_ROLE_BINARY\" \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func roleSetupResult(t *testing.T, n *workerNode) protocol.RoleSetupResult {
	t.Helper()
	raw, err := n.cli.callWithin(scaled(60*time.Second), protocol.OpRoleSetup, protocol.RoleSetupRequest{Start: true})
	if err != nil {
		t.Fatal(err)
	}
	var result protocol.RoleSetupResult
	if err := json.Unmarshal(raw, &result); err != nil || len(result.Failures) != 0 || len(result.Models) != 1 {
		t.Fatalf("role setup = %s, %v", raw, err)
	}
	return result
}

// The whole first start uses temp homes and fake Codex servers. There is no
// Claude binary on PATH and the fake hello makes no model call.
func TestRoleSetupWithoutClaudeStartsCodexRolesAndWorker(t *testing.T) {
	home, binDir := noClaudeMachine(t)
	t.Setenv(envFakeCodex, "1")
	bin := fakeHarnessWrapper(t, binDir, "codex", `while [ "$#" -gt 0 ]; do if [ "$1" = --output-last-message ]; then shift; printf OK > "$1"; fi; shift; done`)
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	f.autoTurn = true
	detect := func(context.Context) []protocol.HarnessInfo {
		return []protocol.HarnessInfo{{Name: "codex", Path: bin, Models: []string{"gpt-fake"}, SignedIn: "yes"}}
	}
	n := startWorkerNodeWithDetect(t, g, shortSet, detect, func(c *Config) {
		c.HomeDir = home
		c.StateDir = filepath.Join(home, "state")
		c.CodexHome = f.home
		c.CodexInterval = 20 * time.Millisecond
		c.HarnessPaths = map[string]string{"codex": bin}
		c.ListModels = func(context.Context, string) ([]string, error) { return []string{"gpt-fake"}, nil }
		c.testNoDeliveryConfirm = false
	})
	eventually(t, "codex inventory", func() bool { list, _ := n.d.inventory(); return len(list) > 0 })
	roleSetupResult(t, n)
	cto := readCodex(t, codexCTODir(n.node), func(r fakeCodexRecord) bool { return len(r.Turns) > 0 && len(r.SkillRoots) > 0 })
	if _, err := os.Stat(filepath.Join(cto.SkillRoots[0], ctoskill.Name, "SKILL.md")); err != nil {
		t.Fatalf("CTO skill: %v", err)
	}
	// A shell command and a file edit run with no approval prompt and no
	// sandbox (decision 0157).
	if cto.Thread["approvalPolicy"] != "never" || cto.Thread["sandbox"] != "danger-full-access" {
		t.Fatalf("CTO thread policy = %v", cto.Thread)
	}
	chief, err := g.gw.Store().ChiefStatus()
	if err != nil || chief.SessionID == "" {
		t.Fatalf("chief = %+v, %v", chief, err)
	}
	eventually(t, "chief takes its first turn", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, turn := range f.turns {
			if strings.Contains(turn, chiefFirstMessage) {
				return true
			}
		}
		return false
	})
	repo := newRepo(t, "shop")
	n.register(t, repo)
	n.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Model: "gpt-fake", Isolation: protocol.IsolationNone, Task: "say ok"})
	rec := readCodex(t, repo, func(r fakeCodexRecord) bool { return len(r.Turns) > 0 })
	if len(rec.Turns) == 0 {
		t.Fatal("worker got no turn")
	}
}

// The same first start on OpenCode. The CTO and the worker run on fake private
// servers; the chief runs in a fake of the owner's shared service, which must
// create it with the allow-all rule and receive its first message.
func TestRoleSetupWithoutClaudeStartsOpenCodeRolesAndWorker(t *testing.T) {
	home, binDir := noClaudeMachine(t)
	bin := fakeHarnessWrapper(t, binDir, "opencode", `printf '%s\n' '{"type":"text","part":{"type":"text","text":"OK"}}'`)
	var mu sync.Mutex
	var chiefRules []ctoPermission
	var chiefPrompts []string
	g := startGateway(t, t.TempDir(), "")
	detect := func(context.Context) []protocol.HarnessInfo {
		return []protocol.HarnessInfo{{Name: "opencode", Path: bin, Version: "2.2.6", Models: []string{"fake/model"}, SignedIn: "yes"}}
	}
	n := startWorkerNodeWithDetect(t, g, shortSet, detect, func(c *Config) {
		c.HomeDir = home
		c.StateDir = filepath.Join(home, "state")
		c.HarnessPaths = map[string]string{"opencode": bin}
		c.ListModels = func(context.Context, string) ([]string, error) { return []string{"fake/model"}, nil }
	})
	chiefDir := filepath.Join(home, "state", "chief")
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/openapi.json" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("x-opencode-directory") == "" {
			fmt.Fprint(w, `{"data":[]}`)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /api/session":
			var body struct {
				Permissions []ctoPermission `json:"permissions"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			chiefRules = body.Permissions
			mu.Unlock()
			helloOpenCode(t, n.node, "ses_chief", chiefDir)
			encoded, _ := json.Marshal(body.Permissions)
			fmt.Fprintf(w, `{"data":{"id":"ses_chief","permissions":%s}}`, encoded)
		case "POST /api/session/ses_chief/prompt":
			var body struct {
				Text string `json:"text"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			chiefPrompts = append(chiefPrompts, body.Text)
			mu.Unlock()
			fmt.Fprint(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(service.Close)
	writeOwnerOpenCodeService(t, os.Getenv("XDG_STATE_HOME"), service.URL)
	eventually(t, "opencode inventory", func() bool { list, _ := n.d.inventory(); return len(list) > 0 })
	roleSetupResult(t, n)

	cto := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	var config struct {
		Skills []string `json:"skills"`
	}
	if err := json.Unmarshal([]byte(cto.Env["OPENCODE_CONFIG_CONTENT"]), &config); err != nil || len(config.Skills) == 0 {
		t.Fatalf("CTO config %q: %v", cto.Env["OPENCODE_CONFIG_CONTENT"], err)
	}
	if _, err := os.Stat(filepath.Join(config.Skills[0], ctoskill.Name, "SKILL.md")); err != nil {
		t.Fatalf("CTO skill: %v", err)
	}
	var harness string
	if err := n.d.st.db.QueryRow(`SELECT active_harness FROM cto WHERE id=1`).Scan(&harness); err != nil || harness != "opencode" {
		t.Fatalf("CTO harness = %q, %v", harness, err)
	}
	eventually(t, "CTO takes its first turn", func() bool {
		return len(readFake(t, filepath.Join(n.cfg.StateDir, "cto")).Turns) > 0
	})
	eventually(t, "chief gets the allow-all rule and its first message", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(chiefRules) == 1 && chiefRules[0] == (ctoPermission{Action: "*", Resource: "*", Effect: "allow"}) &&
			len(chiefPrompts) > 0 && strings.Contains(chiefPrompts[0], chiefFirstMessage)
	})
	repo := newRepo(t, "shop")
	n.register(t, repo)
	n.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/model", Task: "say ok"})
	if rec := readFake(t, repo); rec.SessionID == "" {
		t.Fatal("worker got no session")
	}
}

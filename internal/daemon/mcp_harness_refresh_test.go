package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// refreshWorld is a host node that runs one global MCP server "probe", and a
// work node with two registered projects, alpha and beta.
type refreshWorld struct {
	host, work *node
	cli        *localClient
	repos      map[string]string
}

func newRefreshWorld(t *testing.T, o nodeOpts) *refreshWorld {
	t.Helper()
	useHelper(t)
	g := startGateway(t, t.TempDir(), "")
	w := &refreshWorld{repos: map[string]string{}}
	w.host = startNode(t, g, nodeOpts{cmdline: "pi"})
	if o.cmdline == "" {
		o.cmdline = "pi"
	}
	w.work = startNode(t, g, o)
	root := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		gitIn(t, dir, "init", "-q", "-b", "main")
		w.repos[name] = dir
	}
	w.cli = cliClient(t, w.work)
	w.cli.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{w.repos["alpha"], w.repos["beta"]}})
	cliClient(t, w.host).mustCall("mcp_add", protocol.MCPAdd{Server: stdioSpec("probe")})
	return w
}

// assignProbe scopes the server "probe" to one project with the owner's plan.
func (w *refreshWorld) assignProbe(t *testing.T, project string) {
	t.Helper()
	var page protocol.ToolsPage
	if err := json.Unmarshal(w.cli.mustCall("tools_items", map[string]any{"kind": "mcp"}), &page); err != nil {
		t.Fatal(err)
	}
	var id string
	for _, it := range page.Items {
		if it.Name == "probe" {
			id = it.ID
		}
	}
	if id == "" {
		t.Fatal("probe is not listed")
	}
	target := protocol.ToolAssignment{Scope: "global"}
	if project != "" {
		target = protocol.ToolAssignment{Scope: "projects", Projects: []string{project}}
	}
	var plan protocol.ToolPlan
	raw := w.cli.mustCall("tools_plan", protocol.ToolPlanRequest{Item: id, Target: target})
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	w.cli.mustCall("tools_apply", protocol.ToolPlanID{ID: plan.ID})
}

// A Pi and an OpenCode plugin both hear mcp_changed after an assignment, and
// the tool list they read next is filtered for their project.
func TestMCPScopedRefreshForPluginHarnesses(t *testing.T) {
	for _, client := range []string{"pi", "opencode"} {
		t.Run(client, func(t *testing.T) {
			w := newRefreshWorld(t, nodeOpts{cmdline: "pi", exe: "/usr/bin/opencode"})
			hello := func(native, cwd string) *localClient {
				lc := dialLocal(t, w.work.sock)
				lc.mustCall("hello", map[string]any{"client": client, "harness": client, "native_id": native, "cwd": cwd, "status": "idle"})
				return lc
			}
			alpha, beta := hello("na", w.repos["alpha"]), hello("nb", w.repos["beta"])
			if a, b := scopedCatalog(t, alpha), scopedCatalog(t, beta); a != "probe" || b != "probe" {
				t.Fatalf("before: alpha %q, beta %q", a, b)
			}
			w.assignProbe(t, "alpha")
			waitPush(t, alpha, "mcp_changed")
			waitPush(t, beta, "mcp_changed")
			if a, b := scopedCatalog(t, alpha), scopedCatalog(t, beta); a != "probe" || b != "" {
				t.Fatalf("after: alpha %q, beta %q", a, b)
			}
			if _, err := callTool(t, beta, "probe", "echo", map[string]string{"text": "x"}); err == nil || !strings.Contains(err.Error(), "not assigned to this project") {
				t.Fatalf("stale call: %v", err)
			}
		})
	}
}

// shimToolNames returns the federated tool names a Codex shim lists.
func shimToolNames(t *testing.T, sh *codexShim) string {
	t.Helper()
	raw, errMsg := sh.call("tools/list", map[string]any{})
	if errMsg != "" {
		t.Fatalf("tools/list: %s", errMsg)
	}
	var r struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	json.Unmarshal(raw, &r)
	var names []string
	for _, x := range r.Tools {
		if x.Name == "probe__echo" {
			names = append(names, x.Name)
		}
	}
	return strings.Join(names, ",")
}

func eventuallyStr(t *testing.T, what, want string, get func() string) {
	t.Helper()
	deadline := time.Now().Add(scaled(20 * time.Second))
	var got string
	for time.Now().Before(deadline) {
		if got = get(); got == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s: got %q, want %q", what, got, want)
}

// Codex: one agent-net process serves every thread, so it lists the tools for
// the whole app server. A project-scoped server is not listed to the process
// (it has no project), a global one is, and a call is checked per thread.
func TestMCPScopedRefreshForCodex(t *testing.T) {
	f := startFakeShared(t)
	w := newRefreshWorld(t, nodeOpts{cfg: func(c *Config) { c.CodexHome = f.home; c.CodexInterval = time.Second }})
	f.set(fakeThread{ID: "t1", Cwd: w.repos["alpha"], Name: "alpha", Status: "idle", Source: "cli"})
	f.set(fakeThread{ID: "t2", Cwd: w.repos["beta"], Name: "beta", Status: "idle", Source: "cli"})
	eventually(t, "threads", func() bool { return w.work.d.SessionID("codex", "t1") != "" && w.work.d.SessionID("codex", "t2") != "" })
	sh := startCodexShim(t, w.work)
	eventuallyStr(t, "global server listed", "probe__echo", func() string { return shimToolNames(t, sh) })
	// A global server works for every thread.
	for _, th := range []string{"t1", "t2"} {
		if txt, isErr := sh.tool(th, "probe__echo", map[string]any{"text": "ok"}); isErr || !strings.Contains(txt, "echo: ok") {
			t.Fatalf("%s before assignment: %q %v", th, txt, isErr)
		}
	}
	w.assignProbe(t, "alpha")
	// The process has no project, so the list drops the scoped server for all
	// threads. Codex gets the change in its next tools/list.
	eventuallyStr(t, "scoped server hidden from the shared list", "", func() string { return shimToolNames(t, sh) })
	for _, th := range []string{"t1", "t2"} {
		if txt, isErr := sh.tool(th, "probe__echo", map[string]any{"text": "x"}); !isErr {
			t.Fatalf("%s still reaches the scoped server: %q", th, txt)
		}
	}
	// Back to global: the tool returns.
	w.assignProbe(t, "")
	eventuallyStr(t, "global again", "probe__echo", func() string { return shimToolNames(t, sh) })
}

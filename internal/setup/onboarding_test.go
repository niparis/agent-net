package setup

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/quotaaxi"
)

type setupDaemonFixture struct {
	info      protocol.OperatorInfo
	nodes     protocol.NodesInfo
	agents    protocol.AgentsResult
	projects  protocol.ProjectsResult
	graph     protocol.GraphGot
	protected protocol.ProtectedProviders
	workers   protocol.WorkersResult
	listAfter int
}

func fakeSetupDaemon(t *testing.T, fixture setupDaemonFixture) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_NET_SOCKET", sock)
	t.Cleanup(func() { listener.Close() })
	var mu sync.Mutex
	listCalls := 0
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				scanner := bufio.NewScanner(conn)
				for scanner.Scan() {
					var request struct {
						ID     string          `json:"id"`
						Method string          `json:"method"`
						Params json.RawMessage `json:"params"`
					}
					if json.Unmarshal(scanner.Bytes(), &request) != nil {
						continue
					}
					var result any
					switch request.Method {
					case "hello":
						result = map[string]any{}
					case "info":
						result = fixture.info
					case "nodes":
						result = fixture.nodes
					case "list":
						mu.Lock()
						listCalls++
						calls := listCalls
						mu.Unlock()
						result = fixture.agents
						if fixture.listAfter > calls {
							result = protocol.AgentsResult{}
						}
					case "projects":
						result = fixture.projects
					case "graph_get":
						result = fixture.graph
					case protocol.OpProtectedProvidersGet:
						result = fixture.protected
					case "workers":
						result = fixture.workers
					case protocol.OpToolsDifferences:
						result = map[string]any{"differences": []any{}, "to_look_at": 0}
					default:
						fmt.Fprintf(conn, "{\"id\":%q,\"error\":%q}\n", request.ID, "unknown method")
						continue
					}
					body, _ := json.Marshal(result)
					fmt.Fprintf(conn, "{\"id\":%q,\"result\":%s}\n", request.ID, body)
				}
			}(conn)
		}
	}()
}

func onboardingFixture(name string) (setupDaemonFixture, bool, []string, []string) {
	makeNode := func(id, host string, harnesses []string) protocol.NodeView {
		launch := true
		return protocol.NodeView{NodeID: id, Hostname: host, Online: true, Launch: &launch, Harnesses: harnessInfos(harnesses)}
	}
	localNode := func(id, host string, harnesses []string) protocol.OperatorInfo {
		return protocol.OperatorInfo{Connected: true, NodeID: id, Hostname: host, GatewayURL: "https://gateway.example:7420", Launch: true}
	}
	switch name {
	case "first-machine":
		info := localNode("n_laptop", "laptop", []string{"claude", "codex", "pi", "opencode"})
		info.Sessions = []protocol.SessionInfo{{Harness: "pi", ToolsOld: true}, {Harness: "pi", ToolsOld: true}}
		agents := []protocol.AgentView{
			{SessionID: "s_c1", NodeID: "n_laptop", Hostname: "laptop", Harness: "claude", Status: "busy", Online: true},
			{SessionID: "s_c2", NodeID: "n_laptop", Hostname: "laptop", Harness: "claude", Status: "busy", Online: true},
			{SessionID: "s_c3", NodeID: "n_laptop", Hostname: "laptop", Harness: "claude", Status: "idle", Online: true},
			{SessionID: "s_codex", NodeID: "n_laptop", Hostname: "laptop", Harness: "codex", Status: "idle", Online: true},
		}
		projects := make([]protocol.ProjectView, 8)
		graphNodes := make([]protocol.Node, 8)
		for i := range projects {
			projects[i] = protocol.ProjectView{Name: fmt.Sprintf("project-%d", i+1), NodeID: "n_laptop", Hostname: "laptop"}
			graphNodes[i] = protocol.Node{Name: projects[i].Name, Kind: "project"}
		}
		return setupDaemonFixture{info: info, nodes: protocol.NodesInfo{Nodes: []protocol.NodeView{makeNode("n_laptop", "laptop", []string{"claude", "codex", "pi", "opencode"})}},
			agents: protocol.AgentsResult{Agents: agents}, projects: protocol.ProjectsResult{Projects: projects}, graph: protocol.GraphGot{Graph: protocol.Graph{Nodes: graphNodes}},
			protected: protocol.ProtectedProviders{Providers: []string{"anthropic"}, Configured: true}}, false, []string{"claude", "codex", "pi", "opencode"}, []string{"claude", "codex"}
	case "empty-first-machine":
		return setupDaemonFixture{info: localNode("n_laptop", "laptop", nil), nodes: protocol.NodesInfo{Nodes: []protocol.NodeView{makeNode("n_laptop", "laptop", nil)}},
			projects: protocol.ProjectsResult{}, graph: protocol.GraphGot{}, protected: protocol.ProtectedProviders{}}, false, nil, []string{"codex"}
	case "joining-machine":
		info := localNode("n_build", "build-vm", []string{"pi", "opencode"})
		info.Sessions = []protocol.SessionInfo{{Harness: "pi", ToolsOld: true}, {Harness: "opencode", ToolsOld: true}}
		agents := []protocol.AgentView{
			{SessionID: "s_pi", NodeID: "n_build", Hostname: "build-vm", Harness: "pi", Status: "idle", Online: true},
			{SessionID: "s_oc", NodeID: "n_build", Hostname: "build-vm", Harness: "opencode", Status: "idle", Online: true},
			{SessionID: "s_laptop", NodeID: "n_laptop", Hostname: "laptop", Harness: "claude", Status: "busy", Online: true},
		}
		return setupDaemonFixture{info: info, nodes: protocol.NodesInfo{Nodes: []protocol.NodeView{makeNode("n_laptop", "laptop", []string{"claude", "codex"}), makeNode("n_build", "build-vm", []string{"pi", "opencode"})}},
			agents: protocol.AgentsResult{Agents: agents}, projects: protocol.ProjectsResult{}, graph: protocol.GraphGot{}, protected: protocol.ProtectedProviders{}}, true, []string{"pi", "opencode"}, nil
	default:
		panic("unknown onboarding fixture " + name)
	}
}

func harnessInfos(names []string) []protocol.HarnessInfo {
	out := make([]protocol.HarnessInfo, 0, len(names))
	for _, name := range names {
		out = append(out, protocol.HarnessInfo{Name: name})
	}
	return out
}

func TestSetupOnboardingGolden(t *testing.T) {
	for _, name := range []string{"first-machine", "empty-first-machine", "joining-machine", "failed-run"} {
		for _, colored := range []bool{false, true} {
			mode := "plain"
			if colored {
				mode = "color"
			}
			t.Run(name+"/"+mode, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("AGENT_NET_CONFIG_DIR", filepath.Join(home, "config"))
				t.Setenv("AGENT_NET_STATE_DIR", filepath.Join(home, "state"))
				t.Setenv("AGENT_NET_GATEWAY_DATA", filepath.Join(home, "gateway"))
				t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", filepath.Join(home, "claude-sessions"))
				fixtureName := name
				if name == "failed-run" {
					fixtureName = "first-machine"
				}
				fixture, joining, harnesses, quotaProviders := onboardingFixture(fixtureName)
				fakeSetupDaemon(t, fixture)
				if len(quotaProviders) > 0 {
					providers := make([]quotaaxi.AuthProvider, 0, len(quotaProviders))
					for _, provider := range quotaProviders {
						providers = append(providers, quotaaxi.AuthProvider{Provider: provider, Status: "found"})
					}
					if err := quotaaxi.SaveAuth(daemon.ConfigDir(), quotaaxi.AuthResult{Providers: providers}); err != nil {
						t.Fatal(err)
					}
				}
				oldTTY := openTTY
				openTTY = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("n\n")), nil }
				t.Cleanup(func() { openTTY = oldTTY })
				var out strings.Builder
				r := &runner{Options: Options{Out: &out, Exe: "/fake/agent-net"}, colors: setupColor{enabled: colored}, joining: joining, foundHarnesses: harnesses, interactive: true}
				ctx := context.Background()
				if name == "failed-run" {
					r.step("daemon service", "", errors.New("systemctl --user start: exit 1"))
				} else {
					r.daemonStatus(ctx)
				}
				r.finish(ctx)
				got := out.String()
				golden := filepath.Join("testdata", "onboarding", name+"-"+mode+".golden")
				if os.Getenv("UPDATE_SETUP_GOLDEN") == "1" {
					if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
						t.Fatal(err)
					}
					return
				}
				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatal(err)
				}
				if got != string(want) {
					t.Fatalf("output differs from %s\n--- got ---\n%s\n--- want ---\n%s", golden, got, want)
				}
			})
		}
	}
}

func TestSetupWaitsForFirstNonEmptyAgentList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENT_NET_CONFIG_DIR", filepath.Join(home, "config"))
	t.Setenv("AGENT_NET_STATE_DIR", filepath.Join(home, "state"))
	t.Setenv("AGENT_NET_GATEWAY_DATA", filepath.Join(home, "gateway"))
	t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", filepath.Join(home, "claude-sessions"))
	fixture, joining, harnesses, _ := onboardingFixture("first-machine")
	fixture.listAfter = 2
	fakeSetupDaemon(t, fixture)
	oldTTY := openTTY
	openTTY = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("n\n")), nil }
	t.Cleanup(func() { openTTY = oldTTY })
	var out strings.Builder
	r := &runner{Options: Options{Out: &out}, joining: joining, foundHarnesses: harnesses}
	r.daemonStatus(context.Background())
	r.finish(context.Background())
	if !strings.Contains(out.String(), "4 sessions on laptop: 3 claude, 1 codex.") {
		t.Fatalf("setup did not wait for session discovery:\n%s", out.String())
	}
}

func TestSetupOpenQuestionRunsTUIOnDefaultAccept(t *testing.T) {
	oldTTY, oldExec := openTTY, execSetupTUI
	t.Cleanup(func() { openTTY, execSetupTUI = oldTTY, oldExec })
	openTTY = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("\n")), nil }
	var got string
	execSetupTUI = func(exe string) error { got = exe; return nil }
	var out strings.Builder
	r := &runner{Options: Options{Out: &out, Exe: "/fake/agent-net"}, interactive: true}
	r.openQuestion()
	if got != "/fake/agent-net" {
		t.Fatalf("TUI exec path %q", got)
	}
	if !strings.Contains(out.String(), "Open agent-net now? [Y/n]") {
		t.Fatalf("open question: %q", out.String())
	}
}

func TestSetupOpenQuestionIgnoresTerminalEscapeLine(t *testing.T) {
	oldTTY, oldExec := openTTY, execSetupTUI
	t.Cleanup(func() { openTTY, execSetupTUI = oldTTY, oldExec })
	openTTY = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("\x1b]11;rgb:0000/0000/0000\x1b\\\ny\n")), nil
	}
	called := false
	execSetupTUI = func(string) error { called = true; return nil }
	r := &runner{Options: Options{Out: io.Discard}, interactive: true}
	r.openQuestion()
	if !called {
		t.Fatal("TUI was not opened after ignoring the terminal escape reply")
	}
}

func TestSetupOpenQuestionDefaultsToNoWhenJoining(t *testing.T) {
	oldTTY, oldExec := openTTY, execSetupTUI
	t.Cleanup(func() { openTTY, execSetupTUI = oldTTY, oldExec })
	openTTY = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("\n")), nil }
	called := false
	execSetupTUI = func(string) error { called = true; return nil }
	var out strings.Builder
	r := &runner{Options: Options{Out: &out}, joining: true, interactive: true}
	r.openQuestion()
	if called {
		t.Fatal("joining setup opened the TUI on Enter")
	}
	if !strings.Contains(out.String(), "Open agent-net here? [y/N]") {
		t.Fatalf("open question: %q", out.String())
	}
}

func TestSetupOpenQuestionSkipsTTYWhenNonInteractive(t *testing.T) {
	oldTTY := openTTY
	t.Cleanup(func() { openTTY = oldTTY })
	opened := false
	openTTY = func() (io.ReadCloser, error) {
		opened = true
		return nil, errors.New("unexpected tty")
	}
	var out strings.Builder
	r := &runner{Options: Options{Out: &out}}
	r.openQuestion()
	if opened {
		t.Fatal("non-interactive setup checked for a controlling terminal")
	}
	if !strings.Contains(out.String(), "Open agent-net: run agent-net tui") {
		t.Fatalf("non-interactive instruction: %q", out.String())
	}
}

// The projects call lists a folder per machine, so it also carries worker
// worktrees, Claude Code worktrees and temporary test folders. The Map line
// must count project identities, as the Map tab does, not folders.
func TestMapPointCountsProjectIdentitiesNotFolders(t *testing.T) {
	summary := setupSummary{
		projectsKnown: true,
		projects: []protocol.ProjectView{
			{Name: "agent-net", Path: "/home/owner/code/agent-net"},
			{Name: "w_4846e4b5b1eadf29", Path: "/home/owner/.local/state/agent-net/daemon/worktrees/w_4846e4b5b1eadf29"},
			{Name: "w_2f1c8a7d3b4e5f60", Path: "/home/owner/.local/state/agent-net/daemon/worktrees/w_2f1c8a7d3b4e5f60"},
			{Name: "w_9d0e1f2a3b4c5d6e", Path: "/home/owner/.local/state/agent-net/daemon/worktrees/w_9d0e1f2a3b4c5d6e"},
			{Name: "tmp123456", Path: "/tmp/tmp123456"},
		},
		graphKnown: true,
		graph: protocol.Graph{Nodes: []protocol.Node{
			{Name: "agent-net", Kind: "component", Charter: protocol.Charter{Purpose: "The agent network."}},
		}},
	}
	point := (&runner{}).mapPoint(summary)
	if point.first != "1 project seen. All have charters." {
		t.Fatalf("Map line %q, want one project", point.first)
	}
}

// A graph with no visible nodes has no projects, even when the projects call
// still lists temporary folders or old worktrees.
func TestMapPointSaysNoProjectsWhenTheGraphIsEmpty(t *testing.T) {
	summary := setupSummary{
		projectsKnown: true,
		projects: []protocol.ProjectView{
			{Name: "tmp123456", Path: "/tmp/tmp123456"},
			{Name: "w_4846e4b5b1eadf29", Path: "/home/owner/.local/state/agent-net/daemon/worktrees/w_4846e4b5b1eadf29"},
		},
		graphKnown: true,
		graph:      protocol.Graph{},
	}
	point := (&runner{}).mapPoint(summary)
	if point.first != "No projects yet. Each project you work in becomes one." {
		t.Fatalf("Map line %q, want no projects", point.first)
	}
}

// Without the graph (for example when owner-only calls are refused), the
// folder list must never be counted: it holds every checkout ever seen.
func TestMapPointWithoutTheGraphGivesNoFolderCount(t *testing.T) {
	summary := setupSummary{
		projectsKnown: true,
		projects: []protocol.ProjectView{
			{Name: "agent-net", Path: "/home/owner/code/agent-net"},
			{Name: "w_4846e4b5b1eadf29", Path: "/home/owner/.local/state/agent-net/daemon/worktrees/w_4846e4b5b1eadf29"},
			{Name: "tmp123456", Path: "/tmp/tmp123456"},
		},
	}
	point := (&runner{}).mapPoint(summary)
	if strings.ContainsAny(point.first, "0123456789") {
		t.Fatalf("Map line %q counts folders without the graph", point.first)
	}
}

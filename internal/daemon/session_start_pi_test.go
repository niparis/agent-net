package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Pi host (design start-sessions.md, part 6). Pi gets a private tmux
// server: a fake tmux records the exact command, and a fake Pi extension
// registers the session through the normal hello path.

func TestSessionStartPiTmuxArgvIsExact(t *testing.T) {
	const uuid = "11111111-2222-4333-8444-555555555555"
	got := piTmuxStartArgv("/usr/bin/tmux", "/src/shop", "pi", uuid, "shop", "")
	want := []string{"/usr/bin/tmux", "-L", "agent-net", "new-session", "-d", "-c", "/src/shop",
		"--", "pi", "--session-id", uuid, "--name", "shop"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("start argv\n got %v\nwant %v", got, want)
	}
	got = piTmuxStartArgv("/usr/bin/tmux", "/src/shop", "pi", uuid, "shop", "openai/gpt-5")
	want = append(want, "--model", "openai/gpt-5")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("model argv\n got %v\nwant %v", got, want)
	}
	if got := piTmuxResumeArgv("/usr/bin/tmux", "/src/shop", "pi", uuid, false); !reflect.DeepEqual(got,
		[]string{"/usr/bin/tmux", "-L", "agent-net", "new-window", "-d", "-c", "/src/shop", "--", "pi", "--session", uuid}) {
		t.Fatalf("resume window argv %v", got)
	}
	if got := piTmuxResumeArgv("/usr/bin/tmux", "/src/shop", "pi", uuid, true); !reflect.DeepEqual(got,
		[]string{"/usr/bin/tmux", "-L", "agent-net", "new-session", "-d", "-c", "/src/shop", "--", "pi", "--session", uuid}) {
		t.Fatalf("resume session argv %v", got)
	}
}

// fakeTmuxHost writes a tmux that records every argument and answers only
// has-session. It never starts a real tmux server.
func fakeTmuxHost(t *testing.T, hasSession bool) (path, argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	has := "0"
	if hasSession {
		has = "1"
	}
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\0' "$@" >> %s
printf '\n' >> %s
if [ "$3" = has-session ]; then
  [ "%s" = 1 ] && exit 0
  exit 1
fi
exit 0
`, shellQuote(argsFile), shellQuote(argsFile), has)
	path = filepath.Join(dir, "tmux")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path, argsFile
}

func readTmuxCalls(t *testing.T, path string) [][]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if line == "" {
			continue
		}
		calls = append(calls, strings.Split(strings.TrimSuffix(line, "\x00"), "\x00"))
	}
	return calls
}

func TestSessionStartPiRunsInPrivateTmux(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.HarnessPaths = map[string]string{"pi": "/fake/pi"}
	})
	repo := newRepo(t, "shop")
	n.register(t, repo)
	_, argsFile := fakeTmuxHost(t, false)

	var r sessionStartTestResult
	mustJSON(t, n.cli.mustCall("session_start", protocol.SessionStartRequest{Project: "shop", Harness: "pi",
		Model: "openai/gpt-5:high"}), &r)
	if r.Status != "starting" || r.ID == "" || r.NativeID == "" || r.Path != repo {
		t.Fatalf("initial result: %+v", r)
	}
	calls := readTmuxCalls(t, argsFile)
	want := []string{"-L", "agent-net", "new-session", "-d", "-c", repo, "--", "/fake/pi",
		"--session-id", r.NativeID, "--name", "shop", "--model", "openai/gpt-5:high"}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("tmux calls\n got %v\nwant %v", calls, want)
	}
	// The extension registers the chosen session ID from inside the tmux pane.
	helloPi(t, n.node, r.NativeID, repo, true)
	eventually(t, "owner Pi session registered", func() bool {
		mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		if r.Status == "failed" {
			t.Fatalf("start: %+v", r)
		}
		return r.Status == "started" && r.SessionID != ""
	})
	a := n.agentOf(t, r.SessionID)
	if a == nil || a.Worker != nil || a.Harness != "pi" {
		t.Fatalf("owner row: %+v", a)
	}
	rows, _ := n.d.st.workers()
	if len(rows) != 0 {
		t.Fatalf("worker records: %+v", rows)
	}
}

func TestSessionStartPiNeedsTmux(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.HarnessPaths = map[string]string{"pi": "/fake/pi"}
	})
	repo := newRepo(t, "shop")
	n.register(t, repo)
	t.Setenv("PATH", t.TempDir()) // no tmux on this machine
	_, err := n.cli.call("session_start", protocol.SessionStartRequest{Project: "shop", Harness: "pi"})
	want := "pi needs tmux on " + n.d.cfg.Hostname + " to run in the background"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal: %v, want %q", err, want)
	}
}

func TestSessionStartPiFirstMessageIsAnOwnerMessage(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.HarnessPaths = map[string]string{"pi": "/fake/pi"}
	})
	repo := newRepo(t, "shop")
	n.register(t, repo)
	fakeTmuxHost(t, false)

	var r sessionStartTestResult
	mustJSON(t, n.cli.mustCall("session_start", protocol.SessionStartRequest{Project: "shop", Harness: "pi",
		FirstMessage: "Inspect this project"}), &r)
	lc, sessionID := helloPi(t, n.node, r.NativeID, repo, true)
	push := lc.nextPush()
	if push.Body != "Inspect this project" || push.From != n.ownerID() || push.To != sessionID ||
		push.Type != protocol.TypeRequest || push.FromHarness != protocol.OwnerHarness {
		t.Fatalf("first message: %+v", push)
	}
	eventually(t, "sender session registered", func() bool {
		a := n.agentOf(t, sessionID)
		return a != nil && a.Harness == "pi"
	})
	eventually(t, "owner Pi start finished", func() bool {
		mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		if r.Status == "failed" {
			t.Fatalf("start: %+v", r)
		}
		return r.Status == "started"
	})
}

func TestEnterPiUsesThePrivateTmuxPane(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	repo := newRepo(t, "shop")
	n, _, id := enterNode(t, g, "pi", protocol.SessionInfo{NativeID: "pi-1", PID: 400, Cwd: repo,
		Name: "shop", Status: "idle", Mode: protocol.ModeBackground}, nil, nil)
	tmuxArgs := fakeTmux(t, "200|%3|0")
	fakeProcessTree(t, map[int]int{400: 200, 200: 100, 100: 1}, nil)
	processEnviron = func(pid int) []string {
		if pid == 400 {
			return []string{"TMUX=/tmp/tmux-1000/agent-net,999,0"}
		}
		return nil
	}
	plan := enterOf(t, n, id, "")
	want := []string{"tmux", "-S", "/tmp/tmux-1000/agent-net", "select-window", "-t", "%3", ";",
		"select-pane", "-t", "%3", ";", "attach-session", "-t", "0"}
	if !reflect.DeepEqual(plan.Argv, want) {
		t.Fatalf("enter plan\n got %v\nwant %v", plan.Argv, want)
	}
	if !strings.Contains(plan.Note, "keeps running") {
		t.Fatalf("note: %q", plan.Note)
	}
	if calls, _ := os.ReadFile(tmuxArgs); !strings.Contains(string(calls), "-S /tmp/tmux-1000/agent-net list-panes -a") {
		t.Fatalf("tmux was not asked on the private socket: %q", calls)
	}
}

func TestSessionResumePiOpensANewWindow(t *testing.T) {
	for _, hasSession := range []bool{true, false} {
		t.Run(fmt.Sprint("session=", hasSession), func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			n := startWorkerNode(t, g, shortSet, func(c *Config) {
				c.HarnessPaths = map[string]string{"pi": "/fake/pi"}
			})
			repo := newRepo(t, "shop")
			n.register(t, repo)
			_, argsFile := fakeTmuxHost(t, hasSession)
			native := "11111111-2222-4333-8444-555555555555"
			id, err := g.gw.Store().UpsertSession(n.d.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: native, Cwd: repo, Name: "shop"})
			if err != nil {
				t.Fatal(err)
			}
			if err := g.gw.Store().SetSessionOffline(n.d.cfg.NodeID, id); err != nil {
				t.Fatal(err)
			}
			var r sessionStartTestResult
			mustJSON(t, n.cli.mustCall("session_resume", map[string]string{"session_id": id}), &r)
			if r.Status != "starting" || r.NativeID != native || r.Path != repo {
				t.Fatalf("resume result: %+v", r)
			}
			calls := readTmuxCalls(t, argsFile)
			sub := "new-window"
			if !hasSession {
				sub = "new-session"
			}
			want := []string{"-L", "agent-net", sub, "-d", "-c", repo, "--", "/fake/pi", "--session", native}
			if len(calls) != 2 || !reflect.DeepEqual(calls[1], want) {
				t.Fatalf("tmux calls\n got %v\nwant %v", calls, want)
			}
			helloPi(t, n.node, native, repo, true)
			eventually(t, "resumed Pi session registered", func() bool {
				mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
				if r.Status == "failed" {
					t.Fatalf("resume: %+v", r)
				}
				return r.Status == "started"
			})
		})
	}
}

func TestSessionResumePiNeedsTmux(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.HarnessPaths = map[string]string{"pi": "/fake/pi"}
	})
	repo := newRepo(t, "shop")
	n.register(t, repo)
	native := "11111111-2222-4333-8444-555555555555"
	id, err := g.gw.Store().UpsertSession(n.d.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: native, Cwd: repo})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetSessionOffline(n.d.cfg.NodeID, id); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	_, err = n.cli.call("session_resume", map[string]string{"session_id": id})
	want := "pi needs tmux on " + n.d.cfg.Hostname + " to run in the background"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal: %v, want %q", err, want)
	}
}

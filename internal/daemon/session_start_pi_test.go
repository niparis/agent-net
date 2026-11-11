package daemon

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestSessionStartPiFirstMessageIsAnOwnerMessage(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.HarnessPaths = map[string]string{"pi": "/fake/pi"}
	})
	repo := newRepo(t, "shop")
	n.register(t, repo)
	n.register(t, newRepo(t, "chosen"))
	fakeOpenHerdr(t)

	var r sessionStartTestResult
	mustJSON(t, n.cli.mustCall("session_start", map[string]any{"project": "shop", "harness": "pi", "agent_id": "chosen", "first_message": "Inspect this project"}), &r)
	lc, sessionID := helloPi(t, n.node, r.NativeID, repo, true)
	push := lc.nextPush()
	if a := n.agentOf(t, sessionID); a == nil || a.AgentID != "chosen" {
		t.Fatalf("first delivery agent: %+v", a)
	}
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

func TestSessionResumePiHerdr(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "pi")
	n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = bins })
	repo := newRepo(t, "shop")
	n.register(t, repo)
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
	if r.NativeID != native || r.Path != repo {
		t.Fatalf("resume: %+v", r)
	}
	requireHerdrOpened(t, log, "shop", bins["pi"], "--session", native)
	helloPi(t, n.node, native, repo, true)
	eventually(t, "resumed Pi", func() bool {
		mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		return r.Status == "started"
	})
}

// Pi's fullscreen TUI captures the mouse, which stops the terminal's own text
// selection, so copy-paste stops working. Every Pi TUI agent-net starts or
// resumes asks for regular mode. Claude sessions instead get mouse tracking on
// from the user's Claude settings (decisions 0128).
func TestSessionStartAndResumePiRunInRegularTuiMode(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "pi")
	n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = bins })
	repo := newRepo(t, "shop")
	n.register(t, repo)

	var started sessionStartTestResult
	mustJSON(t, n.cli.mustCall("session_start", map[string]any{"project": "shop", "harness": "pi"}), &started)
	requireHerdrOpened(t, log, "shop", bins["pi"], "--session-id", started.NativeID, "--name", "shop", "--tui-mode", "regular")

	// Clear the calls so the resume check cannot pass on the start's command.
	if err := os.WriteFile(log, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	native := "11111111-2222-4333-8444-555555555555"
	id, err := g.gw.Store().UpsertSession(n.d.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: native, Cwd: repo, Name: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetSessionOffline(n.d.cfg.NodeID, id); err != nil {
		t.Fatal(err)
	}
	var resumed sessionStartTestResult
	mustJSON(t, n.cli.mustCall("session_resume", map[string]string{"session_id": id}), &resumed)
	requireHerdrOpened(t, log, "shop", bins["pi"], "--session", native, "--tui-mode", "regular")
}

func TestSessionStartPiRequiresHerdr(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	d := &Daemon{cfg: Config{HarnessPaths: map[string]string{"pi": "/fake/pi"}, HarnessSearch: &harnesses.Options{Home: home, PATH: dir, Dirs: []string{}, LoginShellPATH: func(context.Context) string { return "" }}}}
	_, err := d.launchOwnerPi(protocol.StartedSession{Project: "shop", Path: dir}, "", "", "", "", false)
	if err == nil || !strings.Contains(err.Error(), "herdr not found (looked in: "+dir) {
		t.Fatalf("error: %v", err)
	}
}

func TestSessionStartPiRemoteAgent(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	a := startWorkerNode(t, g, shortSet, nil)
	b := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"pi": "/fake/pi"} })
	b.register(t, newRepo(t, "remote-repo"))
	b.register(t, newRepo(t, "chosen"))
	fakeOpenHerdr(t)
	var r protocol.StartedSession
	mustJSON(t, a.cli.mustCall("session_start", protocol.SessionStartRequest{Project: "remote-repo", NodeID: b.d.NodeID(), Harness: "pi", AgentID: "chosen", FirstMessage: "Inspect remote"}), &r)
	lc, id := helloPi(t, b.node, r.NativeID, r.Path, true)
	push := lc.nextPushOrLog("the first message to the remote Pi session", b.logs)
	if push.Body != "Inspect remote" {
		t.Fatalf("message: %+v", push)
	}
	if agent := b.agentOf(t, id); agent == nil || agent.AgentID != "chosen" {
		t.Fatalf("remote first identity: %+v", agent)
	}
}

// A Pi start that never registers its session fails in the background after
// the daemon answered "starting". The failure must be logged and recorded.
func TestSessionStartPiFailureLogsAndRecords(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.StartTimeout = scaled(time.Second)
	n := startWorkerNode(t, g, set, func(c *Config) {
		c.HarnessPaths = map[string]string{"pi": "/fake/pi"}
	})
	repo := newRepo(t, "shop")
	n.register(t, repo)
	fakeOpenHerdr(t)

	var r sessionStartTestResult
	mustJSON(t, n.cli.mustCall("session_start", map[string]any{"project": "shop", "harness": "pi"}), &r)
	eventually(t, "the Pi start fails", func() bool {
		mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		return r.Status == "failed"
	})
	if !strings.Contains(r.Error, "did not register the Pi session") {
		t.Fatalf("failed start error %q", r.Error)
	}
	if lines := n.logs.lines("event=session.start_failed", "start="+r.ID); len(lines) != 1 {
		t.Fatalf("want one failure log line, got %q", n.logs.String())
	}
	events, err := g.gw.Store().Events("session.start_failed")
	if err != nil || len(events) != 1 || !strings.Contains(events[0], r.ID) {
		t.Fatalf("failure event: %v %v", events, err)
	}
}

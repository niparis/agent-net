package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Build plan part 8 (docs/design/start-sessions.md): an owner session start,
// resume or stop on another machine is relayed through the gateway with the
// owner flag. The receiving daemon checks that flag against the gateway.

func TestSessionStartRelaysToAnotherMachine(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	g := startGateway(t, t.TempDir(), "")
	a := startWorkerNode(t, g, shortSet, nil)
	b := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.HarnessPaths = map[string]string{"opencode": os.Args[0]}
	})
	repo := newRepo(t, "homelab")
	b.register(t, repo)
	var creates atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/session":
			creates.Add(1)
			helloOpenCode(t, b.node, "ses_relay", repo)
			fmt.Fprint(w, `{"data":{"id":"ses_relay"}}`)
		case "POST /api/session/ses_relay/prompt":
			fmt.Fprint(w, `{"data":{}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	writeOwnerOpenCodeService(t, state, server.URL)

	// The TUI sends the node ID of the project row. Without it, the daemon
	// finds the one machine that holds the project.
	if target, err := a.d.sessionStartMachine(context.Background(), protocol.SessionStartRequest{Project: "homelab"}); err != nil || target != b.d.NodeID() {
		t.Fatalf("machine for the project: %q %v", target, err)
	}
	req := protocol.SessionStartRequest{Project: "homelab", Harness: "opencode", NodeID: b.d.NodeID(),
		FirstMessage: "Set up the lab", Folder: "~/src/homelab"}
	var r protocol.StartedSession
	mustJSON(t, a.cli.mustCall("session_start", req), &r)
	if r.ID == "" || r.NodeID != b.d.NodeID() {
		t.Fatalf("relayed start: %+v", r)
	}
	eventually(t, "the start on the other machine finishes", func() bool {
		mustJSON(t, a.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		if r.Status == "failed" {
			t.Fatalf("relayed start failed: %+v", r)
		}
		return r.Status == "started" && r.SessionID != ""
	})
	if r.Hostname == "" || r.NodeID != b.d.NodeID() {
		t.Fatalf("relayed result names the wrong machine: %+v", r)
	}
	if b.d.sessionByID(r.SessionID) == nil {
		t.Fatal("the session was not started on the other machine")
	}
	if creates.Load() != 1 {
		t.Fatalf("the other machine created %d OpenCode sessions", creates.Load())
	}
	// The gateway saw the owner flag and the machine that sent it.
	owner := a.ownerID()
	if len(g.logs.lines("session.started", "owner="+owner, "from_node="+a.d.NodeID())) == 0 {
		t.Fatalf("gateway log has no session.started with the owner flag: %v", g.logs.lines("session.started"))
	}
}

// relayStopAdapter is the fake Claude adapter with a background reference, so
// the stop path can be exercised as it is on a real machine.
type relayStopAdapter struct {
	*fsClaudeAdapter
	native string
}

func (a *relayStopAdapter) EnterRef(native string) (kind, job string) {
	if native == a.native {
		return "bg", "job-abcd1234"
	}
	return "", ""
}

func TestSessionResumeAndStopRelayToAnotherMachine(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	a := startWorkerNode(t, g, shortSet, nil)
	sessions, stops := useFakeClaude(t)
	native := "11111111-0000-4000-8000-000000000000"
	b := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.Adapters = append(c.Adapters, &relayStopAdapter{fsClaudeAdapter: &fsClaudeAdapter{dir: sessions}, native: native})
		c.HarnessPaths = map[string]string{"claude": os.Args[0]}
	})
	repo := newRepo(t, "shop")
	b.register(t, repo)
	old, err := g.gw.Store().UpsertSession(b.d.NodeID(), protocol.SessionInfo{Harness: "claude", NativeID: native, Cwd: repo, Name: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetSessionOffline(b.d.NodeID(), old); err != nil {
		t.Fatal(err)
	}
	var r protocol.StartedSession
	mustJSON(t, a.cli.mustCall("session_resume", protocol.SessionResumeRequest{SessionID: old, NodeID: b.d.NodeID()}), &r)
	if r.ID == "" || r.NodeID != b.d.NodeID() {
		t.Fatalf("relayed resume: %+v", r)
	}
	eventually(t, "the other machine resumes the session", func() bool {
		mustJSON(t, a.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		if r.Status == "failed" {
			t.Fatalf("relayed resume failed: %+v", r)
		}
		return r.Status == "started" && r.SessionID != ""
	})
	rec := readClaude(t, repo)
	if argValue(rec.Args, "--resume") != native || r.NativeID != rec.Session {
		t.Fatalf("resume on the other machine: %+v record %+v", r, rec)
	}
	note := a.cli.mustCall("session_stop", map[string]string{"session_id": r.SessionID})
	if !strings.Contains(string(note), "conversation is kept") {
		t.Fatalf("relayed stop note: %s", note)
	}
	stopsOf := func() []string {
		raw, _ := os.ReadFile(stops)
		return strings.Fields(strings.ReplaceAll(string(raw), "stop ", ""))
	}
	eventually(t, "the other machine stops the session", func() bool { return len(stopsOf()) > 0 })
	if len(g.logs.lines("session.stopped", "owner="+a.ownerID(), "from_node="+a.d.NodeID())) == 0 {
		t.Fatalf("gateway log has no session.stopped with the owner flag: %v", g.logs.lines("session.stopped"))
	}
}

func TestSessionRelayRefusesAnAgentSession(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	a := startWorkerNode(t, g, shortSet, nil)
	b := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "homelab")
	b.register(t, repo)
	_, agentID := helloPi(t, a.node, "pi_agent", repo, true)
	eventually(t, "the gateway knows the agent session", func() bool {
		_, err := a.d.call(context.Background(), "query.agent", protocol.QueryAgent{SessionID: agentID})
		return err == nil
	})
	for _, typ := range []string{protocol.FrameSessionStart, protocol.FrameSessionResume, protocol.FrameSessionStop} {
		var frame any
		switch typ {
		case protocol.FrameSessionStart:
			frame = protocol.SessionStartRelay{Owner: agentID, NodeID: b.d.NodeID(),
				SessionStartRequest: protocol.SessionStartRequest{Project: "homelab", Harness: "pi"}}
		default:
			frame = protocol.SessionRelay{Owner: agentID, NodeID: b.d.NodeID(), SessionID: "s_unknown"}
		}
		_, err := a.d.call(context.Background(), typ, frame)
		if err == nil || !strings.Contains(err.Error(), "owner session") {
			t.Errorf("%s from an agent session: %v", typ, err)
		}
	}
}

func TestSessionRelayRefusesAnOfflineMachine(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	a := startWorkerNode(t, g, shortSet, nil)
	b := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "homelab")
	b.register(t, repo)
	target := b.d.NodeID()
	host := b.d.cfg.Hostname
	b.d.Stop()
	_, err := a.cli.call("session_start", protocol.SessionStartRequest{Project: "homelab", Harness: "claude", NodeID: target})
	if err == nil || !strings.Contains(err.Error(), "offline") || !strings.Contains(err.Error(), host) {
		t.Fatalf("offline machine: %v", err)
	}
}

func TestSessionRelayRefusesAnOldMachine(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	a := startWorkerNode(t, g, shortSet, nil)
	b := startWorkerNode(t, g, shortSet, func(c *Config) { c.Version = "0.4.45" })
	repo := newRepo(t, "homelab")
	b.register(t, repo)
	_, err := a.cli.call("session_start", protocol.SessionStartRequest{Project: "homelab", Harness: "claude", NodeID: b.d.NodeID()})
	if err == nil || !strings.Contains(err.Error(), "older agent-net") || !strings.Contains(err.Error(), b.d.cfg.Hostname) {
		t.Fatalf("old machine: %v", err)
	}
}

// Codex has no background start: on another machine the plan must run ssh to
// that machine, in the owner's terminal, not a local Codex.
func TestSessionStartRelaysCodexThroughSSH(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	a := startWorkerNode(t, g, shortSet, nil)
	bins := fakeBin(t, "codex")
	b := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = bins })
	repo := newRepo(t, "homelab")
	b.register(t, repo)
	var r protocol.StartedSession
	mustJSON(t, a.cli.mustCall("session_start", protocol.SessionStartRequest{Project: "homelab", Harness: "codex",
		Model: "model-x", NodeID: b.d.NodeID()}), &r)
	if r.Status != "started" || r.EnterPlan == nil || len(r.EnterPlan.Argv) < 2 || r.EnterPlan.Argv[0] != "ssh" {
		t.Fatalf("Codex on another machine: %+v", r)
	}
	run := r.EnterPlan.Argv[len(r.EnterPlan.Argv)-1]
	if !strings.Contains(run, "codex") || !strings.Contains(run, "model-x") || !strings.Contains(run, repo) {
		t.Fatalf("remote Codex command %q", run)
	}
	if r.EnterNote == "" || !strings.Contains(r.EnterNote, "ssh") {
		t.Fatalf("enter note: %q", r.EnterNote)
	}
}

// The folder choice of the form is carried to the machine that runs the
// start. Part 10 adds the checks and the confirm; this part only relays it.
func TestSessionStartRelayCarriesTheFolderField(t *testing.T) {
	raw, err := json.Marshal(protocol.SessionOwnerRequest{
		Owner: "s_owner", FromNode: "n_a",
		SessionStartRequest: protocol.SessionStartRequest{Project: "garden", Folder: "~/code/garden-planner"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var back protocol.SessionOwnerRequest
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Folder != "~/code/garden-planner" {
		t.Fatalf("the relay dropped the folder field: %s", raw)
	}
}

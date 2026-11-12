package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestMaxSessionStartLocalAndRemote(t *testing.T) {
	for _, remote := range []bool{false, true} {
		name := "local"
		if remote {
			name = "remote"
		}
		t.Run(name, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			a := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"claude": os.Args[0]} })
			b := a
			if remote {
				b = startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"claude": os.Args[0]} })
			}
			repo := newRepo(t, "demo")
			b.register(t, repo)
			if _, err := g.gw.Store().UpsertSession(b.d.NodeID(), protocol.SessionInfo{Harness: "pi", NativeID: "terminal", Repo: protocol.RepoInfo{Project: "demo", Root: repo}}); err != nil {
				t.Fatal(err)
			}
			if _, err := g.gw.Store().WriteSettings([]protocol.SettingsChange{{Key: "max_agents", Level: "repo", Scope: "demo", Value: json.RawMessage(`1`)}}, "owner", "limit"); err != nil {
				t.Fatal(err)
			}
			req := protocol.SessionStartRequest{Project: "demo", Harness: "claude"}
			if remote {
				req.NodeID = b.d.NodeID()
			}
			var got protocol.StartedSession
			mustJSON(t, a.cli.mustCall("session_start", req), &got)
			if got.Status != "failed" || got.Error != "demo already has 1 agents (max 1). Stop one, or raise the max to 2." || got.SuggestedChange == nil || string(got.SuggestedChange.Value) != "2" {
				t.Fatalf("result: %+v", got)
			}
			if remote && len(g.logs.lines("session.started", "project=demo")) != 0 {
				t.Fatal("refused start logged as started")
			}
		})
	}
}

func TestLeadMessageDeliveryReceiptEndToEnd(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "pi"})
	cli := cliClient(t, n)
	if _, err := g.gw.Store().AddProjects(n.d.NodeID(), protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "demo", Path: "/work/demo"}}}); err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().AddArchitect("helper", "owner"); err != nil {
		t.Fatal(err)
	}
	lead := fakeSession("lead", "lead")
	lead.Cwd = "/work/demo"
	lead.Repo = protocol.RepoInfo{Project: "demo", Root: "/work/demo"}
	target := fakeSession("target", "target")
	target.Cwd = "/work/demo"
	target.Repo = lead.Repo
	fa.setSessions(lead, target)
	eventually(t, "sessions known", func() bool {
		return fa.host.SessionID("fake", "lead") != "" && fa.host.SessionID("fake", "target") != ""
	})
	leadID, targetID := fa.host.SessionID("fake", "lead"), fa.host.SessionID("fake", "target")
	if _, err := cli.call("session_agent_set", map[string]string{"session_id": targetID, "agent": "helper"}); err != nil {
		t.Fatal(err)
	}
	view, ok, err := g.gw.Store().Agent(leadID)
	if err != nil || !ok || view.AgentID != "demo" {
		t.Fatalf("lead session not assigned: %+v %v", view, err)
	}
	peer, _ := helloPi(t, n, "outside", "/work/outside", true)
	raw, err := peer.call("send", map[string]any{"to": targetID, "body": "hello", "receipt": true})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Sent []struct {
			To struct {
				ID string `json:"id"`
			} `json:"to"`
			Note string `json:"note"`
		} `json:"sent"`
	}
	if err = json.Unmarshal(raw, &got); err != nil || len(got.Sent) != 1 || got.Sent[0].To.ID != leadID || !strings.Contains(got.Sent[0].Note, "Redirected to") {
		t.Fatalf("receipt: %s %v", raw, err)
	}
	eventually(t, "lead delivery", func() bool { return len(fa.delivered()) > 0 })
	if m := fa.delivered()[0]; m.To != leadID || !strings.Contains(m.Body, "originally sent to") {
		t.Fatalf("delivery: %+v", m)
	}
	crew, _ := helloPi(t, n, "crew", "/work/demo", true)
	crewMessage := sendResult(t, crew.mustCall("send", map[string]any{"to": targetID, "body": "inside"}))
	if crewMessage.To != targetID {
		t.Fatalf("crew redirected: %+v", crewMessage)
	}
	ownerMessage := sendResult(t, cli.mustCall("owner_send", protocol.OwnerSendRequest{To: targetID, Body: "owner"}))
	if ownerMessage.To != targetID {
		t.Fatalf("owner redirected: %+v", ownerMessage)
	}
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctoResult, err := n.d.runCTOToolRaw(context.Background(), "agent_message_send", json.RawMessage(fmt.Sprintf(`{"to":%q,"body":"cto"}`, targetID)))
	if err != nil {
		t.Fatal(err)
	}
	receipts, ok := ctoResult.(messageReceipts)
	if !ok || len(receipts.Sent) != 1 || receipts.Sent[0].To.ID != targetID {
		t.Fatalf("CTO redirected: %+v", ctoResult)
	}
	if _, err = n.d.call(context.Background(), "hello", map[string]any{"hostname": "fixture", "version": "v0.4.145", "graph_refs": true}); err != nil {
		t.Fatal(err)
	}
	waitGraphCutover(t, g)
	cli.mustCall("team_write", map[string]any{"action": "create", "team_id": "alpha", "name": "Alpha", "owner": map[string]string{"kind": "agent", "id": "helper"}})
	cli.mustCall("team_write", map[string]any{"action": "membership", "team_id": "alpha", "member": map[string]string{"kind": "repo", "id": "demo"}})
	for address, want := range map[string]string{"repo:demo": "demo", "team:alpha": "helper"} {
		raw, err := peer.call("send", map[string]any{"to": address, "body": "direct", "receipt": true})
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Sent []struct {
				To struct {
					ID string `json:"id"`
				} `json:"to"`
			} `json:"sent"`
		}
		if json.Unmarshal(raw, &result) != nil || len(result.Sent) != 1 {
			t.Fatalf("%s: %s", address, raw)
		}
		recipient, ok, err := g.gw.Store().Agent(result.Sent[0].To.ID)
		if err != nil || !ok || recipient.AgentID != want {
			t.Fatalf("%s: %s, recipient %+v %v", address, raw, recipient, err)
		}
	}
}

func TestMaxCTOStartOffersSettingChange(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"claude": os.Args[0]} })
	repo := newRepo(t, "demo")
	n.register(t, repo)
	if _, err := g.gw.Store().UpsertSession(n.d.NodeID(), protocol.SessionInfo{Harness: "pi", NativeID: "terminal", Repo: protocol.RepoInfo{Project: "demo", Root: repo}}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.gw.Store().WriteSettings([]protocol.SettingsChange{{Key: "max_agents", Level: "repo", Scope: "demo", Value: json.RawMessage(`1`)}}, "owner", "limit"); err != nil {
		t.Fatal(err)
	}
	out, err := n.d.runCTOToolRaw(context.Background(), "session_change", json.RawMessage(`{"action":"session_start","project":"demo"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	var got struct {
		State  string `json:"state"`
		Result struct {
			Data struct {
				SuggestedChange *protocol.SettingsChange `json:"suggested_change"`
			} `json:"data"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &got) != nil || got.State != "failed" || got.Result.Data.SuggestedChange == nil || string(got.Result.Data.SuggestedChange.Value) != "2" {
		t.Fatalf("result: %s", raw)
	}
	proposedRaw, _ := json.Marshal(map[string]any{"area": "values_write", "reason": "increase capacity", "changes": []protocol.SettingsChange{*got.Result.Data.SuggestedChange}})
	proposal, err := n.d.runCTOToolRaw(context.Background(), "settings_change", proposedRaw)
	if err != nil {
		t.Fatal(err)
	}
	assertCTOImmediate(t, proposal)
	limit, err := g.gw.Store().RepoAgentLimit("demo")
	if err != nil || limit.MaxAgents != 2 {
		t.Fatalf("approved limit: %+v %v", limit, err)
	}
}

func TestMaxFailedClaudeStartReleasesPlace(t *testing.T) {
	// The trust answer is written to Claude's config, so keep it in a temp folder.
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"claude": "/bin/false"} })
	repo := newRepo(t, "demo")
	n.register(t, repo)
	trustClaudeFolder(t, repo)
	if _, err := g.gw.Store().UpsertSession(n.d.NodeID(), protocol.SessionInfo{Harness: "pi", NativeID: "terminal", Repo: protocol.RepoInfo{Project: "demo", Root: repo}}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.gw.Store().WriteSettings([]protocol.SettingsChange{{Key: "max_agents", Level: "repo", Scope: "demo", Value: json.RawMessage(`2`)}}, "owner", "limit"); err != nil {
		t.Fatal(err)
	}
	var first protocol.StartedSession
	mustJSON(t, n.cli.mustCall("session_start", protocol.SessionStartRequest{Project: "demo", Harness: "claude"}), &first)
	if first.Status != "starting" {
		t.Fatalf("first: %+v", first)
	}
	eventually(t, "failed start", func() bool {
		mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": first.ID}), &first)
		return first.Status == "failed"
	})
	var second protocol.StartedSession
	mustJSON(t, n.cli.mustCall("session_start", protocol.SessionStartRequest{Project: "demo", Harness: "claude"}), &second)
	if second.Status != "starting" {
		t.Fatalf("place not released: %+v", second)
	}
}

func TestMaxSuccessfulClaudeStartConsumesPlace(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "demo")
	n.register(t, repo)
	trustClaudeFolder(t, repo)
	t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", n.sessions)
	if _, err := g.gw.Store().UpsertSession(n.d.NodeID(), protocol.SessionInfo{Harness: "pi", NativeID: "terminal", Repo: protocol.RepoInfo{Project: "demo", Root: repo}}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.gw.Store().WriteSettings([]protocol.SettingsChange{{Key: "max_agents", Level: "repo", Scope: "demo", Value: json.RawMessage(`2`)}}, "owner", "limit"); err != nil {
		t.Fatal(err)
	}
	var first protocol.StartedSession
	mustJSON(t, n.cli.mustCall("session_start", protocol.SessionStartRequest{Project: "demo", Harness: "claude"}), &first)
	if first.Status != "starting" {
		t.Fatalf("first: %+v", first)
	}
	eventually(t, "registered start", func() bool {
		mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": first.ID}), &first)
		if first.Status == "failed" {
			t.Fatalf("start: %+v", first)
		}
		return first.Status == "started"
	})
	var second protocol.StartedSession
	mustJSON(t, n.cli.mustCall("session_start", protocol.SessionStartRequest{Project: "demo", Harness: "claude"}), &second)
	if second.Status != "failed" || !strings.Contains(second.Error, "already has 2 agents") {
		t.Fatalf("second: %+v", second)
	}
}

// The daemon owns the shared Codex app server. When nothing has started it, a
// Codex start runs Codex's daemon command first, then creates the thread.
func TestMaxCodexStartsTheAppServerWhenItIsDown(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f, target := startFakeSharedStopped(t)
	bin, record := fakeCodexDaemonStart(t, target, false)
	n := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.HarnessPaths = map[string]string{"codex": bin}
		c.CodexHome = f.home
		c.CodexInterval = 20 * time.Millisecond
	})
	repo := newRepo(t, "demo")
	n.register(t, repo)
	if _, err := g.gw.Store().UpsertSession(n.d.NodeID(), protocol.SessionInfo{Harness: "pi", NativeID: "terminal", Repo: protocol.RepoInfo{Project: "demo", Root: repo}}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.gw.Store().WriteSettings([]protocol.SettingsChange{{Key: "max_agents", Level: "repo", Scope: "demo", Value: json.RawMessage(`2`)}}, "owner", "limit"); err != nil {
		t.Fatal(err)
	}
	var result protocol.StartedSession
	raw, err := n.cli.call("session_start", protocol.SessionStartRequest{Project: "demo", Harness: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	mustJSON(t, raw, &result)
	if result.Status != "started" || result.NativeID == "" || result.SessionID == "" || result.Error != "" {
		t.Fatalf("start: %+v", result)
	}
	rec, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the Codex app server was not started: %v", err)
	}
	if got, want := strings.TrimSpace(string(rec)), "app-server daemon start|"+f.home; got != want {
		t.Fatalf("Codex ran as %q, want %q", got, want)
	}
}

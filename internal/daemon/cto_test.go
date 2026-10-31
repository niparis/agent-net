package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOBriefTeachesGraphUse(t *testing.T) {
	for _, want := range []string{"graph_get", "rule auto", "depends", "diff", "undo", "stop a worker", "decline a held request", "remove an MCP server", "change integrations", "explicit words", "manage the network", "workers", "settings", "ui_show", "When a worker profile is unavailable, tell the owner and offer to repoint it to the closest model with worker_profile_add under the same name.", "Write charters as short rules for other agents, not as a technical spec", "under about 200 characters", "Put detail in the project's AGENTS.md"} {
		if !strings.Contains(ctoBrief, want) {
			t.Errorf("ctoBrief lacks %q", want)
		}
	}
}

func TestCTORuntime(t *testing.T) {
	t.Setenv("FAKE_OC_AUTH_RECORD", "1")
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	cli := cliClient(t, n)
	cli.mustCall("cto_events", nil)
	cli.mustCall("cto_new", nil)
	if _, err := os.Stat(filepath.Join(n.cfg.StateDir, "cto")); !os.IsNotExist(err) {
		t.Fatalf("server started before first send: %v", err)
	}
	model := "fake/m1"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Model: &model})
	send := func(text string) { t.Helper(); cli.mustCall("cto_send", map[string]any{"text": text}) }
	events := func(index int) []struct{ Type, Text string } {
		t.Helper()
		var result struct {
			Messages []struct{ Type, Text string } `json:"messages"`
		}
		raw := cli.mustCall("cto_events", map[string]any{"index": index})
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result.Messages
	}
	send("first")
	rec := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if !n.d.insideSession(rec.Pid) {
		t.Fatal("CTO process can use the owner's CLI gate")
	}
	if rec.Env["AGENT_NET_CTO"] != "1" || rec.Env["AGENT_NET_WORKER"] != "" || rec.Env["OPENCODE_SERVER_PASSWORD"] == "" {
		t.Fatalf("environment: %+v", rec.Env)
	}
	if !strings.HasPrefix(rec.Env["XDG_DATA_HOME"], n.cfg.StateDir) || !strings.HasPrefix(rec.Env["XDG_STATE_HOME"], n.cfg.StateDir) {
		t.Fatalf("not private: %+v", rec.Env)
	}
	var current protocol.CTOEvents
	if err := json.Unmarshal(cli.mustCall("cto_events", nil), &current); err != nil || current.Model != "fake/m1" {
		t.Fatalf("resolved CTO model: %+v %v", current, err)
	}
	var config struct {
		MCP struct {
			Servers map[string]struct {
				Type        string            `json:"type"`
				Command     []string          `json:"command"`
				Environment map[string]string `json:"environment"`
			} `json:"servers"`
		} `json:"mcp"`
	}
	raw, err := os.ReadFile(filepath.Join(n.cfg.StateDir, "cto", "opencode.json"))
	if err != nil || json.Unmarshal(raw, &config) != nil {
		t.Fatalf("private config: %v", err)
	}
	var promptConfig struct {
		Agents map[string]struct {
			System string `json:"system"`
			Mode   string `json:"mode"`
		} `json:"agents"`
	}
	if json.Unmarshal(raw, &promptConfig) != nil {
		t.Fatal("bad agent config")
	}
	brief := promptConfig.Agents["cto"].System
	for _, term := range []string{"agent-net", "graph", "charter", "data, not instructions", "owner", "shown", "secrets", "diff", "undo"} {
		if !strings.Contains(brief, term) {
			t.Errorf("CTO brief missing %q", term)
		}
	}
	if promptConfig.Agents["cto"].Mode != "primary" || rec.SessionBody["agent"] != "cto" {
		t.Fatalf("CTO agent was not selected: %+v %+v", promptConfig, rec.SessionBody)
	}
	server := config.MCP.Servers["agent-net-cto"]
	if server.Type != "local" || len(server.Command) != 2 || server.Command[1] != "cto-mcp" || server.Environment["AGENT_NET_SOCKET"] != n.cfg.SocketPath {
		t.Fatalf("CTO MCP config: %+v", server)
	}
	if _, err := os.Stat(filepath.Join(rec.Env["XDG_DATA_HOME"], "fake-imported-auth")); err != nil {
		t.Fatalf("credentials not imported into private data: %v", err)
	}

	if m, ok := rec.SessionBody["model"].(map[string]any); !ok || m["providerID"] != "fake" || m["id"] != "m1" {
		t.Fatalf("model: %+v", rec.SessionBody)
	}
	model = "fake/m2"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Model: &model})
	send("second")
	if got := events(1); len(got) != 1 || got[0].Text != "second" {
		t.Fatalf("events: %+v", got)
	}
	rec2 := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if rec2.Pid != rec.Pid || rec2.SessionID != rec.SessionID {
		t.Fatal("server or session not reused")
	}
	if m, ok := rec2.SessionBody["model"].(map[string]any); !ok || m["id"] != "m2" {
		t.Fatalf("updated model: %+v", rec2.SessionBody)
	}

	n.restart()
	cli = cliClient(t, n)
	send("third")
	rec2 = readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if rec2.SessionID != rec.SessionID || rec2.Pid == rec.Pid {
		t.Fatalf("restart: old=%+v new=%+v", rec, rec2)
	}
	if rec2.SessionBody["agent"] != "cto" {
		t.Fatalf("CTO agent not restored after restart: %+v", rec2.SessionBody)
	}
	if got := events(0); len(got) != 3 || got[0].Text != "first" || got[2].Text != "third" {
		t.Fatalf("restart events: %+v", got)
	}
	cli.mustCall("cto_new", nil)
	send("new")
	rec3 := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if rec3.SessionID == rec.SessionID {
		t.Fatal("new conversation reused old session")
	}
	if got := events(0); len(got) != 1 || got[0].Text != "new" {
		t.Fatalf("new events: %+v", got)
	}
	n.d.wmu.Lock()
	count := len(n.d.workers)
	n.d.wmu.Unlock()
	if count != 0 {
		t.Fatalf("CTO listed as worker: %d", count)
	}
	n.d.mu.Lock()
	for _, session := range n.d.sessions {
		if session.info.NativeID == rec.SessionID || session.info.NativeID == rec3.SessionID {
			n.d.mu.Unlock()
			t.Fatal("CTO registered as a network session")
		}
	}
	n.d.mu.Unlock()
}

func TestCTOAdoptsModelSelectedInOpenCode(t *testing.T) {
	t.Setenv("FAKE_OC_AUTH_RECORD", "1")
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	cli := cliClient(t, n)
	model := "fake/m1"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Model: &model})
	cli.mustCall("cto_send", map[string]string{"text": "first"})

	rec := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if _, err := n.d.cto.api(context.Background(), "POST", "/api/session/"+rec.SessionID+"/model", map[string]any{"model": map[string]string{"providerID": "fake", "id": "m2"}}); err != nil {
		t.Fatal(err)
	}
	var events protocol.CTOEvents
	if err := json.Unmarshal(cli.mustCall("cto_events", nil), &events); err != nil || events.Model != "fake/m2" {
		t.Fatalf("events did not report the live model: %+v %v", events, err)
	}
	cli.mustCall("cto_send", map[string]string{"text": "use my selected model"})
	var settings protocol.CTOSettings
	if err := json.Unmarshal(cli.mustCall("cto_settings", nil), &settings); err != nil || settings.Model != "fake/m2" {
		t.Fatalf("full-screen model was not saved: %+v %v", settings, err)
	}
	if err := json.Unmarshal(cli.mustCall("cto_events", nil), &events); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, action := range events.Actions {
		if action.Text == "model set to fake/m2 (picked in the full screen)" {
			found = true
		}
	}
	if !found {
		t.Fatalf("model adoption action missing: %+v", events.Actions)
	}
	rec = readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if rec.ModelCalls != 1 {
		t.Fatalf("adopted model was redundantly set by agent-net: %+v", rec)
	}

	// An explicit model setting survives a runtime reset even though the
	// OpenCode session still reports the previous model.
	model = "fake/m1"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Model: &model})
	n.d.cto.mu.Lock()
	n.d.cto.session, n.d.cto.model = "", ""
	n.d.cto.mu.Unlock()
	cli.mustCall("cto_send", map[string]string{"text": "use the saved setting"})
	if err := json.Unmarshal(cli.mustCall("cto_settings", nil), &settings); err != nil || settings.Model != "fake/m1" {
		t.Fatalf("explicit model setting was replaced: %+v %v", settings, err)
	}
	rec = readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if got := rec.SessionBody["model"].(map[string]any)["id"]; got != "m1" {
		t.Fatalf("explicit model was not applied after restart: %+v", rec.SessionBody)
	}
	if rec.ModelCalls != 2 {
		t.Fatalf("changed saved model was not applied exactly once: %+v", rec)
	}
}

func TestCTOModelsAreSavedPerHarnessAndUsedByEachRuntime(t *testing.T) {
	t.Setenv("FAKE_OC_AUTH_RECORD", "1")
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	sessions, _ := useFakeClaude(t)
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.Adapters = append(c.Adapters, &fsClaudeAdapter{dir: sessions})
		c.HarnessPaths = map[string]string{"claude": os.Args[0], "opencode": os.Args[0]}
	}})
	cli := cliClient(t, n)
	opencodeModel, claudeModel := "fake/m1", "sonnet"
	opencode, claude := "opencode", "claude"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &opencode, Model: &opencodeModel})
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &claude, Model: &claudeModel})

	var settings struct {
		Model         string `json:"model"`
		Harness       string `json:"harness"`
		OpenCodeModel string `json:"opencode_model"`
		ClaudeModel   string `json:"claude_model"`
	}
	if err := json.Unmarshal(cli.mustCall("cto_settings", nil), &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Harness != "claude" || settings.Model != claudeModel || settings.OpenCodeModel != opencodeModel || settings.ClaudeModel != claudeModel {
		t.Fatalf("settings after changing Claude model: %+v", settings)
	}

	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &opencode})
	cli.mustCall("cto_send", map[string]string{"text": "OpenCode model"})
	openRec := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if got := openRec.SessionBody["model"].(map[string]any)["id"]; got != "m1" {
		t.Fatalf("OpenCode started with model %v", got)
	}

	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &claude})
	cli.mustCall("cto_send", map[string]string{"text": "Claude model"})
	claudeRec := readClaude(t, filepath.Join(n.cfg.StateDir, "cto", "claude"))
	if got := argValue(claudeRec.Args, "--model"); got != claudeModel {
		t.Fatalf("Claude started with model %q", got)
	}
}

func TestCTOLegacyModelMigratesToMatchingHarnessSlot(t *testing.T) {
	for _, tc := range []struct {
		model, harness, wantOpenCode, wantClaude string
	}{
		{model: "opencode-go/deepseek-v4.1-flash", harness: "opencode", wantOpenCode: "opencode-go/deepseek-v4.1-flash"},
		{model: "sonnet", harness: "claude", wantClaude: "sonnet"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
			g := startGateway(t, t.TempDir(), "")
			n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour})
			c, ok := n.d.integrationsClient()
			if !ok {
				t.Fatal("integrations client unavailable")
			}
			body, err := json.Marshal(protocol.CTOSettings{Model: tc.model, Harness: tc.harness})
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequest(http.MethodPut, c.URL+"/v1/cto-settings", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+c.Token)
			req.Header.Set("Content-Type", "application/json")
			resp, err := c.HTTP.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode/100 != 2 {
				t.Fatalf("seed legacy setting: %s", resp.Status)
			}

			var settings struct {
				OpenCodeModel string `json:"opencode_model"`
				ClaudeModel   string `json:"claude_model"`
			}
			if err := json.Unmarshal(cliClient(t, n).mustCall("cto_settings", nil), &settings); err != nil {
				t.Fatal(err)
			}
			if settings.OpenCodeModel != tc.wantOpenCode || settings.ClaudeModel != tc.wantClaude {
				t.Fatalf("migrated settings: %+v", settings)
			}
		})
	}
}

func TestCTOSkipsAgentSwitchWhenSessionIsAlreadyCTO(t *testing.T) {
	t.Setenv("FAKE_OC_AUTH_RECORD", "1")
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	cli := cliClient(t, n)
	cli.mustCall("cto_send", map[string]string{"text": "first"})
	rec := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if rec.AgentCalls != 0 {
		t.Fatalf("new CTO session was redundantly switched: %+v", rec)
	}
	n.d.cto.mu.Lock()
	n.d.cto.session, n.d.cto.model = "", ""
	n.d.cto.mu.Unlock()
	cli.mustCall("cto_send", map[string]string{"text": "after restart"})
	rec = readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if rec.AgentCalls != 0 {
		t.Fatalf("already selected CTO agent was switched after restart: %+v", rec)
	}
	if _, err := n.d.cto.api(context.Background(), "POST", "/api/session/"+rec.SessionID+"/agent", map[string]string{"agent": "build"}); err != nil {
		t.Fatal(err)
	}
	callsBefore := readFake(t, filepath.Join(n.cfg.StateDir, "cto")).AgentCalls
	cli.mustCall("cto_send", map[string]string{"text": "restore the CTO agent"})
	rec = readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if rec.AgentCalls != callsBefore+1 || rec.SessionBody["agent"] != "cto" {
		t.Fatalf("non-CTO agent was not switched once: %+v", rec)
	}
}

func TestCTOEnterAppliesSavedModelAndAgent(t *testing.T) {
	t.Setenv("FAKE_OC_AUTH_RECORD", "1")
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	cli := cliClient(t, n)
	model := "fake/m1"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Model: &model})
	cli.mustCall("cto_send", map[string]string{"text": "start CTO session"})

	model = "fake/m2"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Model: &model})
	rec := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if _, err := n.d.cto.api(context.Background(), "POST", "/api/session/"+rec.SessionID+"/agent", map[string]string{"agent": "build"}); err != nil {
		t.Fatal(err)
	}

	var plan protocol.EnterPlan
	if err := json.Unmarshal(cli.mustCall("cto_enter", nil), &plan); err != nil {
		t.Fatal(err)
	}
	rec = readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if got := rec.SessionBody["model"].(map[string]any)["id"]; got != "m2" {
		t.Fatalf("cto_enter left the old model selected: %+v", rec.SessionBody)
	}
	if rec.SessionBody["agent"] != "cto" {
		t.Fatalf("cto_enter left the wrong agent selected: %+v", rec.SessionBody)
	}
	if len(plan.Argv) == 0 || plan.Argv[0] == "" {
		t.Fatalf("cto_enter returned no attach plan: %+v", plan)
	}
}

func TestCTOMethodsOwnerGate(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi", exe: "/opt/pi"})
	pi, _ := helloPi(t, n, "p1", "/w/p", false)
	for _, method := range []string{"cto_send", "cto_new", "cto_events", "cto_settings", "cto_enter"} {
		if _, err := pi.call(method, map[string]any{"text": "x"}); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
			t.Errorf("%s: %v", method, err)
		}
	}
}

func TestCTORecoversAfterServerExit(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	cli := cliClient(t, n)
	cli.mustCall("cto_send", map[string]string{"text": "before"})
	rec := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if err := syscall.Kill(-rec.Pgid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	select {
	case <-n.d.cto.exited:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not exit")
	}
	cli.mustCall("cto_send", map[string]string{"text": "after"})
	rec2 := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if rec2.SessionID != rec.SessionID || rec2.Pid == rec.Pid {
		t.Fatalf("server not resumed: %+v %+v", rec, rec2)
	}
	var result struct {
		Messages []struct{ Text string } `json:"messages"`
	}
	json.Unmarshal(cli.mustCall("cto_events", nil), &result)
	if len(result.Messages) != 2 || result.Messages[0].Text != "before" || result.Messages[1].Text != "after" {
		t.Fatalf("messages: %+v", result.Messages)
	}
}

func TestCTOStartupFailure(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	t.Setenv("FAKE_OC_EXIT", "1")
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	cli := cliClient(t, n)
	if _, err := cli.call("cto_send", map[string]string{"text": "hello"}); err == nil || !strings.Contains(err.Error(), "exited at start") {
		t.Fatalf("startup: %v", err)
	}
}

func TestCTOPrivatePeerGate(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	outsider := dialLocal(t, n.sock)
	if _, err := outsider.call("hello", map[string]string{"client": "cto-mcp"}); err == nil {
		t.Fatal("unrelated process accepted as CTO")
	}
	cli := cliClient(t, n)
	cli.mustCall("cto_send", map[string]string{"text": "start"})
	rec := readFake(t, filepath.Join(n.cfg.StateDir, "cto"))
	if !n.d.isCTOPeer(rec.ChildPid) {
		t.Fatal("CTO child was refused")
	}
	if n.d.isCTOPeer(os.Getpid()) {
		t.Fatal("unrelated process was accepted")
	}
	if _, err := outsider.call("hello", map[string]string{"client": "cto-mcp"}); err == nil {
		t.Fatal("unrelated process accepted after start")
	}
}

func TestCTOToolsThroughFakeOpenCode(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, WorkerSettings{}, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	peer, _ := helloPi(t, w.node, "target", "/w/target", true)
	w.cli.mustCall("cto_send", map[string]string{"text": "start"})
	rec := readFake(t, filepath.Join(w.cfg.StateDir, "cto"))
	base := w.d.cto.base
	password := rec.Env["OPENCODE_SERVER_PASSWORD"]
	call := func(name string, args any) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
		req, _ := http.NewRequest("POST", base+"/fake/cto-tool", bytes.NewReader(b))
		req.SetBasicAuth("opencode", password)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("%s HTTP %d: %s", name, resp.StatusCode, body)
		}
		var result struct {
			IsError bool `json:"isError"`
		}
		if json.Unmarshal(body, &result) != nil || result.IsError {
			t.Fatalf("%s: %s", name, body)
		}
	}
	call("node_add", map[string]any{"name": "architecture"})
	call("node_add", map[string]any{"name": "component"})
	call("charter_set", map[string]any{"node": "architecture", "attr": "purpose", "value": "design"})
	call("charter_history", map[string]any{"node": "architecture"})
	var changes []protocol.CharterChange
	if err := json.Unmarshal(w.cli.mustCall("charter_history", map[string]any{"node": "architecture"}), &changes); err != nil || len(changes) != 1 || changes[0].By != "cto" {
		t.Fatalf("CTO history: %+v %v", changes, err)
	}

	call("edge_set", map[string]any{"from": "architecture", "to": "component", "kind": "depends"})
	call("edge_history", map[string]any{"node": "component"})
	call("edge_remove", map[string]any{"from": "architecture", "to": "component", "kind": "depends"})
	call("graph_undo", map[string]any{})
	call("edge_remove", map[string]any{"from": "architecture", "to": "component", "kind": "depends"})
	call("node_remove", map[string]any{"name": "component"})
	call("graph_get", map[string]any{})
	call("nodes", map[string]any{})
	call("projects", map[string]any{})
	call("messages", map[string]any{})
	call("held_list", map[string]any{})
	call("inbox_list", map[string]any{})
	call("outcome_inbox", map[string]any{})
	call("node_settings_set", map[string]any{"node": "architecture", "setting": "merge_authority", "value": "ask"})
	call("node_settings_get", map[string]any{"node": "architecture"})
	call("playbook_set", map[string]any{"model": "fake/m1", "note": "use tests"})
	call("playbook_get", map[string]any{"model": "fake/m1"})
	call("integrations_get", map[string]any{})
	call("mcp_list", map[string]any{})
	call("cto_settings", map[string]any{})
	call("agents", map[string]any{})
	call("workers", map[string]any{})
	call("needs_you", map[string]any{})
	call("feed", map[string]any{})
	var agents protocol.AgentsResult
	json.Unmarshal(w.cli.mustCall("list", nil), &agents)
	target := ""
	for _, a := range agents.Agents {
		if a.Harness == "pi" {
			target = a.SessionID
		}
	}
	if target == "" {
		t.Fatal("peer missing")
	}
	call("send", map[string]any{"to": target, "body": "hello"})
	select {
	case msg := <-peer.Pushes:
		if !msg.FromCTO {
			t.Fatalf("missing CTO sender: %+v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("message not delivered")
	}
	var incoming protocol.MessageSent
	rawIncoming := peer.mustCall("send", map[string]any{"to": w.d.ownerSessionID(), "body": "question"})
	if err := json.Unmarshal(rawIncoming, &incoming); err != nil || incoming.Message == nil {
		t.Fatalf("incoming message: %s %v", rawIncoming, err)
	}
	call("reply", map[string]any{"message_id": incoming.Message.ID, "body": "answer"})
	select {
	case msg := <-peer.Pushes:
		if !msg.FromCTO || msg.ParentID != incoming.Message.ID || msg.Body != "answer" {
			t.Fatalf("CTO reply: %+v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CTO reply not delivered")
	}
	call("get", map[string]any{"session_id": target})
	call("session_policy", map[string]any{"session_id": target, "scope": "session", "policy": map[string]any{"delegate": "off"}})
	call("learn", map[string]any{"subject_kind": "model", "subject": "fake/m1", "finding": "Needs clear tasks"})
	call("worker_start", map[string]any{"project": "shop", "harness": "opencode", "model": "fake/m1", "task": "test"})
	workers := w.workers(t)
	if len(workers) != 1 {
		t.Fatalf("worker not started: %+v", workers)
	}
	messages := w.messagesOf(t, workers[0].SessionID)
	foundTask := false
	for _, m := range messages {
		if m.Type == protocol.TypeRequest && m.FromCTO {
			foundTask = true
		}
	}
	if !foundTask {
		t.Fatalf("worker task did not carry CTO attribution: %+v", messages)
	}
	var events protocol.CTOEvents
	if err := json.Unmarshal(w.cli.mustCall("cto_events", nil), &events); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, a := range events.Actions {
		lines = append(lines, a.Text)
	}
	joined := strings.Join(lines, "|")
	for _, want := range []string{"read graph", "read agents", "read workers", "read needs", "read feed", "charter architecture: purpose updated", "sent to", "worker started"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in actions: %+v", want, events.Actions)
		}
	}
	if _, err := w.d.runCTOTool(context.Background(), "charter_set", json.RawMessage(`{"node":"missing","attr":"purpose","value":"x"}`)); err == nil {
		t.Fatal("bad graph write succeeded")
	}
	if err := json.Unmarshal(w.cli.mustCall("cto_events", nil), &events); err != nil || !strings.Contains(events.Actions[len(events.Actions)-1].Text, "failed charter_set") {
		t.Fatalf("failed action missing: %+v %v", events.Actions, err)
	}
	var history []protocol.CTOGraphChange
	if err := json.Unmarshal(w.cli.mustCall("cto_graph_history", nil), &history); err != nil || len(history) == 0 {
		t.Fatalf("history: %+v %v", history, err)
	}
	w.cli.mustCall("graph_undo", protocol.GraphUndoRequest{ID: history[len(history)-1].ID, CTOChat: true})
	if err := json.Unmarshal(w.cli.mustCall("cto_events", nil), &events); err != nil || len(events.Actions) == 0 || !strings.Contains(events.Actions[len(events.Actions)-1].Text, "undid graph change") {
		t.Fatalf("undo action: %+v %v", events, err)
	}
	w.cli.mustCall("cto_new", nil)
	if err := json.Unmarshal(w.cli.mustCall("cto_events", nil), &events); err != nil || events.GraphAfter <= 0 || len(events.Actions) != 0 {
		t.Fatalf("new conversation baseline: %+v %v", events, err)
	}

}

func TestCTOQueuedStartPersistsAttribution(t *testing.T) {
	s, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	q := queuedStart{ID: "q_test", Owner: "owner", Project: "p", Path: "/tmp/p", Harness: "opencode", Task: "t", QueuedAt: time.Now(), FromCTO: true}
	if err := s.addQueued(q); err != nil {
		t.Fatal(err)
	}
	rows, err := s.queue()
	if err != nil || len(rows) != 1 || !rows[0].FromCTO {
		t.Fatalf("queue: %+v %v", rows, err)
	}
}

func TestCTOUndoBeforeFirstMessageIsKept(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	cli := cliClient(t, n)
	if _, err := n.d.call(context.Background(), "node.add", map[string]any{"name": "architect", "by": "cto"}); err != nil {
		t.Fatal(err)
	}
	var history []protocol.CTOGraphChange
	if err := json.Unmarshal(cli.mustCall("cto_graph_history", nil), &history); err != nil || len(history) != 1 {
		t.Fatalf("history: %+v %v", history, err)
	}
	cli.mustCall("cto_new", nil)
	cli.mustCall("graph_undo", protocol.GraphUndoRequest{ID: history[0].ID, CTOChat: true})
	var out protocol.CTOEvents
	if err := json.Unmarshal(cli.mustCall("cto_events", nil), &out); err != nil || len(out.Actions) != 1 || !strings.Contains(out.Actions[0].Text, "undid graph change") {
		t.Fatalf("undo: %+v %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(n.cfg.StateDir, "cto")); !os.IsNotExist(err) {
		t.Fatalf("undo started server: %v", err)
	}
	cli.mustCall("cto_send", map[string]string{"text": "hello"})
	if err := json.Unmarshal(cli.mustCall("cto_events", nil), &out); err != nil || len(out.Actions) != 1 {
		t.Fatalf("undo lost after send: %+v %v", out, err)
	}
	cli.mustCall("cto_new", nil)
	if err := json.Unmarshal(cli.mustCall("cto_events", nil), &out); err != nil || len(out.Actions) != 0 {
		t.Fatalf("new conversation retained old undo: %+v %v", out, err)
	}
}

func TestCTOEventsIncludeAcceptedPromptFailure(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	cli := cliClient(t, n)
	cli.mustCall("cto_send", map[string]string{"text": "FAKE:ctofail"})
	var events protocol.CTOEvents
	if err := json.Unmarshal(cli.mustCall("cto_events", nil), &events); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustMarshalCTOEvents(t, events.Messages)), "unknown model") {
		t.Fatalf("failure missing: %s", events.Messages)
	}
}

func mustMarshalCTOEvents(t *testing.T, messages []json.RawMessage) []byte {
	t.Helper()
	b, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCTOEnterOpenCode(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	cli := cliClient(t, n)
	var plan protocol.EnterPlan
	if err := json.Unmarshal(cli.mustCall("cto_enter", nil), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Argv) != 5 || plan.Argv[1] != "--server" || plan.Argv[3] != "--session" || plan.Env["OPENCODE_SERVER_PASSWORD"] == "" {
		t.Fatalf("plan: %+v", plan)
	}
}

func TestCTOBriefProjectResearchAndCharters(t *testing.T) {
	for _, want := range []string{
		"To learn about a project, find it with the projects and agents tools or its graph node (machine, folder, live sessions).",
		"You can read files only on your own machine; for a project elsewhere, or to draft its charter, send its live session a request and use its answer.",
		"Ask the project's own agent to draft its charter with the charter guide, then show the owner the result as a diff.",
	} {
		if !strings.Contains(ctoBrief, want) {
			t.Errorf("CTO brief missing %q", want)
		}
	}
}

func TestCTOEventsCapMergedTimelineToLatestEntries(t *testing.T) {
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	d := &Daemon{st: st}
	events := protocol.CTOEvents{Messages: make([]json.RawMessage, 0, ctoHistoryLimit+5), Actions: []protocol.CTOAction{}}
	for i := 0; i < ctoHistoryLimit+5; i++ {
		events.Messages = append(events.Messages, json.RawMessage(fmt.Sprintf(`{"type":"user","text":"entry-%d","time":{"created":%d}}`, i, i+1)))
	}
	latest, err := d.mergeCTOHistory(events, "opencode", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Messages) != ctoHistoryLimit || latest.Next != 5 {
		t.Fatalf("entry cap: messages=%d next=%d", len(latest.Messages), latest.Next)
	}
	var first, last struct {
		Text string `json:"text"`
	}
	json.Unmarshal(latest.Messages[0], &first)
	json.Unmarshal(latest.Messages[len(latest.Messages)-1], &last)
	if first.Text != "entry-5" || last.Text != "entry-504" {
		t.Fatalf("returned range: first=%q last=%q", first.Text, last.Text)
	}
	page, err := d.mergeCTOHistory(events, "opencode", latest.Next)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(page.Messages[0], &first)
	if len(page.Messages) != 5 || page.Next != 0 || first.Text != "entry-0" {
		t.Fatalf("indexed page: count=%d first=%q", len(page.Messages), first.Text)
	}
}

func TestCTOBriefToolsWorkflow(t *testing.T) {
	for _, want := range []string{"tools_differences", "wait for the owner's explicit words", "pending or failed target with the recovery step"} {
		if !strings.Contains(ctoBrief, want) {
			t.Errorf("CTO brief lacks %q", want)
		}
	}
}

func TestCTOBriefDirectsGraphLocationLookup(t *testing.T) {
	want := "Each graph node lists where its project lives and its live sessions: use that before searching any filesystem."
	if !strings.Contains(ctoBrief, want) {
		t.Fatalf("CTO brief missing %q", want)
	}
}

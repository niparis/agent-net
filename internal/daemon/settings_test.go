package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/agentfiles"
	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/linkkeepalive"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

func TestSettingsSnapshotOfflineAndOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st, err := openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{st: st}
	if err = d.loadSettingsSnapshot(); err != nil {
		t.Fatal(err)
	}
	e, err := d.SettingsSnapshot().Resolve("workers.auto_continue", settings.Where{})
	if err != nil || string(e.Value) != "true" {
		t.Fatalf("default %+v %v", e, err)
	}
	frame := []byte(`{"revision":5,"recipe":"x","values":[{"key":"workers.auto_continue","level":"network","scope":"network","item":"","value":false},{"key":"future","level":"network","scope":"network","value":1}],"maturity":{}}`)
	if err = d.acceptSettingsSnapshot(frame, true); err != nil {
		t.Fatal(err)
	}
	snap := d.SettingsSnapshot()
	if len(snap.Values) != 1 {
		t.Fatalf("unknown key not dropped: %+v", snap)
	}
	snap.Values[0].Value = json.RawMessage("true")
	e, _ = d.SettingsSnapshot().Resolve("workers.auto_continue", settings.Where{})
	if string(e.Value) != "false" {
		t.Fatal("accessor was mutable")
	}
	if err = d.acceptSettingsSnapshot([]byte(`{"revision":4,"values":[],"maturity":{}}`), false); err != nil {
		t.Fatal(err)
	}
	if d.SettingsSnapshot().Revision != 5 {
		t.Fatal("accepted older revision")
	}
	if err = d.acceptSettingsSnapshot([]byte(`{`), false); err == nil {
		t.Fatal("accepted malformed frame")
	}
	st.close()
	st, err = openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	d = &Daemon{st: st}
	if err = d.loadSettingsSnapshot(); err != nil {
		t.Fatal(err)
	}
	if d.SettingsSnapshot().Revision != 5 {
		t.Fatal("lost offline snapshot")
	}
	if err = d.acceptSettingsSnapshot([]byte(`{"revision":2,"values":[],"maturity":{}}`), true); err != nil || d.SettingsSnapshot().Revision != 2 {
		t.Fatalf("reconnect: %v", err)
	}
}

func TestDeliveryPipelineCutoverSurvivesSnapshotValidation(t *testing.T) {
	d := &Daemon{}
	snap, err := d.cleanSettingsSnapshot([]byte(`{"revision":1,"delivery_cutover":true,"delivery_pipeline_cutover":true,"values":[{"key":"delivery.pipeline.merge.approver","level":"repo","scope":"demo","item":"","value":"owner"}],"maturity":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.String("delivery.merge_authority", settings.Where{Repo: "demo"}); got != "ask" {
		t.Fatalf("pipeline shorthand = %q", got)
	}
}

func TestDeliveryAssignmentsSurviveSnapshotValidation(t *testing.T) {
	d := &Daemon{}
	snap, err := d.cleanSettingsSnapshot([]byte(`{"revision":1,"delivery_cutover":true,"delivery_pipeline_cutover":true,"delivery_assignments":[{"project":"demo","step":"approve","for":"merge","node":"reviewer","kind":"approves"}],"values":[],"maturity":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.String("delivery.merge_authority", settings.Where{Repo: "demo"}); got != "custom" {
		t.Fatalf("graph assignment shorthand = %q", got)
	}
}

func TestSettingsSnapshotLinkIgnoresUnknownFrames(t *testing.T) {
	t.Parallel()
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	d := &Daemon{st: st}
	sent := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			sent <- err
			return
		}
		defer ws.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		for _, frame := range []string{`{"type":"settings.sync","revision":9,"values":[{"key":"workers.auto_continue","level":"network","scope":"network","value":false}],"maturity":{}}`, `{"type":"future.unknown"}`, `{"type":"node.name","hostname":"fixture"}`} {
			if err = ws.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
				sent <- err
				return
			}
		}
		sent <- nil
		<-ctx.Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	l := newLink(ws, linkkeepalive.New())
	go l.readLoop(ctx, d)
	if err = <-sent; err != nil {
		t.Fatal(err)
	}
	waitDaemonLink(t, "settings and subsequent frame", func() bool { return d.shownHost.Load() == "fixture" })
	if snap := d.SettingsSnapshot(); snap.Revision != 9 || len(snap.Values) != 1 {
		t.Fatal(snap)
	}
	cancel()
	ws.CloseNow()
	<-l.dead
}

func TestSettingsGuidanceRefreshOnChange(t *testing.T) {
	t.Parallel()
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	calls := 0
	d := &Daemon{st: st, cfg: Config{RefreshAgentFiles: func(_ func(string, ...any), s settings.Snapshot) { calls++ }}}
	frames := []string{
		`{"revision":1,"values":[],"maturity":{}}`,
		`{"revision":2,"values":[{"key":"workers.auto_continue","level":"network","scope":"network","value":false}],"maturity":{}}`,
		`{"revision":3,"values":[{"key":"guidance.peer_help_direct","level":"network","scope":"network","value":"Answer {{reply}}."}],"maturity":{}}`,
		`{"revision":4,"values":[{"key":"guidance.peer_help_direct","level":"network","scope":"network","value":"Answer {{reply}}."}],"maturity":{}}`,
	}
	for _, f := range frames {
		if err := d.acceptSettingsSnapshot([]byte(f), false); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls)
	}
}

func TestSettingsCTOApprovalAndRead(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	advice := json.RawMessage(`{"area":"values_write","changes":[{"key":"guidance.peer_help_direct","level":"network","scope":"network","value":"Answer {{reply}}."}],"reason":"test"}`)
	if _, err := n.d.runCTOToolRaw(ctx, "settings_change", advice); err != nil {
		t.Fatalf("advice: %v", err)
	}
	rule := json.RawMessage(`{"area":"values_write","changes":[{"key":"workers.auto_continue","level":"network","scope":"network","value":false}],"reason":"test"}`)
	if _, err := n.d.runCTOToolRaw(ctx, "settings_change", rule); err != nil {
		t.Fatalf("proposal: %v", err)
	}
	items, err := n.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("proposals: %v %v", items, err)
	}
	var snap settings.Snapshot
	snap, err = g.gw.Store().SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	e, _ := snap.Resolve("workers.auto_continue", settings.Where{})
	if string(e.Value) != "true" {
		t.Fatalf("changed before approval: %s", e.Value)
	}
	if err := n.d.proposalRunner().decide(ctx, items[0].ID, true); err != nil {
		t.Fatal(err)
	}
	snap, _ = g.gw.Store().SettingsSnapshot()
	e, _ = snap.Resolve("workers.auto_continue", settings.Where{})
	if string(e.Value) != "false" {
		t.Fatalf("after approval: %s", e.Value)
	}
	out, err := n.d.readCTOTool(ctx, "agent_settings_read", json.RawMessage(`{"key":"workers.auto_continue"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	if !strings.Contains(string(b), `"source"`) {
		t.Fatalf("missing source: %s", b)
	}
}

func TestSettingsOwnerGateAndUndo(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cli := cliClient(t, n)
	agentNode := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	agent, _ := helloPi(t, agentNode, "settings-reader", "/work/fixture", true)
	write := map[string]any{"changes": []any{map[string]any{"key": "workers.auto_continue", "level": "network", "scope": "network", "value": false}}, "by": "cto", "reason": "owner test"}
	if _, err := agent.call("settings_write", write); err == nil {
		t.Fatal("ordinary agent wrote rule")
	}
	cli.mustCall("settings_write", write)
	hist, err := g.gw.Store().SettingsHistory("", 0, true)
	if err != nil || len(hist) != 1 || hist[0].Actor != "owner" {
		t.Fatalf("owner history %+v %v", hist, err)
	}
	ctx := context.Background()
	if _, err = n.d.runCTOToolRaw(ctx, "settings_change", json.RawMessage(`{"area":"values_undo","revision":1}`)); err != nil {
		t.Fatal(err)
	}
	items, err := n.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("undo proposal %+v %v", items, err)
	}
	s, _ := g.gw.Store().SettingsSnapshot()
	e, _ := s.Resolve("workers.auto_continue", settings.Where{})
	if string(e.Value) != "false" {
		t.Fatal("undo applied without approval")
	}
	if err = n.d.proposalRunner().decide(ctx, items[0].ID, true); err != nil {
		t.Fatal(err)
	}
	s, _ = g.gw.Store().SettingsSnapshot()
	e, _ = s.Resolve("workers.auto_continue", settings.Where{})
	if string(e.Value) != "true" {
		t.Fatal("undo not applied")
	}
	advice := json.RawMessage(`{"area":"values_write","changes":[{"key":"guidance.peer_help_direct","level":"network","scope":"network","value":"Answer {{reply}}."}],"reason":"advice"}`)
	if _, err = n.d.runCTOToolRaw(ctx, "settings_change", advice); err != nil {
		t.Fatal(err)
	}
	if _, err = n.d.runCTOToolRaw(ctx, "settings_change", json.RawMessage(`{"area":"values_undo","revision":3}`)); err != nil {
		t.Fatalf("advice undo: %v", err)
	}
	hist, err = g.gw.Store().SettingsHistory("guidance.peer_help_direct", 0, true)
	if err != nil || len(hist) != 2 || hist[0].Actor != "cto" || hist[0].Cause != "undo" {
		t.Fatalf("advice undo history %+v %v", hist, err)
	}
	// An owner may also undo an advice revision.
	cli.mustCall("settings_undo", map[string]any{"revision": 4})
}

func TestSettingsProposalConflictAndOwnerWindow(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	cli := cliClient(t, n)
	n.d.ctoGraph.mu.Lock()
	n.d.ctoGraph.until = time.Now().Add(time.Hour)
	n.d.ctoGraph.mu.Unlock()
	raw := json.RawMessage(`{"area":"values_write","changes":[{"key":"workers.auto_continue","level":"network","scope":"network","value":false}],"reason":"rule"}`)
	if _, err := n.d.runCTOToolRaw(ctx, "settings_change", raw); err != nil {
		t.Fatal(err)
	}
	items, err := n.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("proposal %+v %v", items, err)
	}
	cli.mustCall("settings_write", map[string]any{"changes": []any{map[string]any{"key": "workers.auto_continue", "level": "network", "scope": "network", "value": true}}, "reason": "intervening"})
	err = n.d.proposalRunner().decide(ctx, items[0].ID, true)
	if err == nil || !strings.Contains(err.Error(), "current true") {
		t.Fatalf("expected conflict: %v", err)
	}
	s, _ := g.gw.Store().SettingsSnapshot()
	e, _ := s.Resolve("workers.auto_continue", settings.Where{})
	if string(e.Value) != "true" {
		t.Fatal("stale proposal changed setting")
	}
}

func TestSettingsDeliveryRuleCTOProposesOwnerApplies(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	raw := json.RawMessage(`{"area":"values_write","changes":[{"key":"delivery.merge_authority","level":"network","scope":"network","value":"ask"}],"reason":"approval"}`)
	if _, err := n.d.runCTOToolRaw(ctx, "settings_change", raw); err != nil {
		t.Fatal(err)
	}
	proposals, err := n.d.proposalRunner().list()
	if err != nil || len(proposals) != 1 {
		t.Fatalf("proposals: %+v %v", proposals, err)
	}
	snap, _ := g.gw.Store().SettingsSnapshot()
	e, _ := snap.Resolve("delivery.merge_authority", settings.Where{})
	if string(e.Value) != `"full"` {
		t.Fatalf("changed without owner: %s", e.Value)
	}
	if err = n.d.proposalRunner().decide(ctx, proposals[0].ID, true); err != nil {
		t.Fatal(err)
	}
	snap, _ = g.gw.Store().SettingsSnapshot()
	e, _ = snap.Resolve("delivery.merge_authority", settings.Where{})
	if string(e.Value) != `"ask"` {
		t.Fatalf("owner approval did not apply: %s", e.Value)
	}
}

func TestSettingsProposalRejectsInvalidChangesBeforeApproval(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	invalid := []string{
		`[{"key":"delivery.merge_authority","level":"team","scope":"alpha","value":"ask"}]`,
		`[{"key":"delivery.merge_authority","level":"repo","scope":"","value":"ask"}]`,
		`[{"key":"delivery.merge_authority","level":"network","scope":"other","value":"ask"}]`,
		`[{"key":"workers.auto_continue","level":"session","scope":"alpha","value":false}]`,
		`[{"key":"delivery.merge_authority","level":"network","scope":"network","item":"x","value":"ask"}]`,
		`[{"key":"delivery.merge_authority","level":"network","scope":"network","delete":true,"value":"ask"}]`,
		`[{"key":"delivery.merge_authority","level":"network","scope":"network"}]`,
		`[{"key":"delivery.merge_authority","level":"network","scope":"network","value":"ask","expect":true}]`,
		`[{"key":"delivery.merge_authority","level":"network","scope":"network","value":"ask"},{"key":"delivery.merge_authority","level":"network","scope":"network","value":"full"}]`,
		`[{"key":"delivery.merge_authority","level":"network","scope":"network","value":"ask"},{"key":"delivery.merge_authority","level":"repo","scope":"alpha","value":"full"}]`,
		`[{"key":"workers.auto_continue","level":"network","scope":"network","value":false,"expect":true}]`,
	}
	for _, changes := range invalid {
		raw := json.RawMessage(`{"area":"values_write","changes":` + changes + `}`)
		if _, err := n.d.runCTOToolRaw(ctx, "settings_change", raw); err == nil {
			t.Errorf("accepted %s", changes)
		}
	}
	items, err := n.d.proposalRunner().list()
	if err != nil || len(items) != 0 {
		t.Fatalf("invalid proposal saved: %+v %v", items, err)
	}
	snap, err := g.gw.Store().SettingsSnapshot()
	if err != nil || snap.Revision != 0 {
		t.Fatalf("validation changed settings: %+v %v", snap, err)
	}
	valid := json.RawMessage(`{"area":"values_write","changes":[{"key":"workers.auto_continue","level":"network","scope":"network","value":false,"expect":null}]}`)
	if _, err := n.d.runCTOToolRaw(ctx, "settings_change", valid); err != nil {
		t.Fatalf("null expected absent: %v", err)
	}
	items, err = n.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("proposal: %+v %v", items, err)
	}
	if err = n.d.proposalRunner().decide(ctx, items[0].ID, true); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsNamedTargetReadAndCTOParity(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	st := g.gw.Store()
	if _, err := st.UpsertSession(n.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "named-target", Repo: protocol.RepoInfo{Project: "shop"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.WriteSettings([]gateway.SettingsChange{{Key: "workers.auto_continue", Level: "repo", Scope: "shop", Value: json.RawMessage("false")}}, "owner", "fixture"); err != nil {
		t.Fatal(err)
	}
	request := protocol.SettingsReadRequest{Key: "workers.auto_continue", Target: &protocol.SettingsTarget{Level: "repo", Name: "shop"}}
	rawArgs, _ := json.Marshal(request)
	value, err := n.d.runCTOToolRaw(ctx, "agent_settings_read", rawArgs)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(value)
	var out struct {
		Settings []protocol.SettingView `json:"settings"`
	}
	if err := json.Unmarshal(encoded, &out); err != nil || len(out.Settings) != 1 {
		t.Fatalf("CTO named read: %s %v", encoded, err)
	}
	row := out.Settings[0]
	if string(row.Value) != "false" || string(row.Explicit) != "false" || row.Source != "repo shop" || row.SourceLevel != "repo" {
		t.Fatalf("repo target fields: %+v", row)
	}
	if row.SourceRef == nil || row.SourceRef.Kind != "repo" || row.SourceRef.ID != "shop" || row.SourceRef.Name != "shop" {
		t.Fatalf("source ref: %+v", row.SourceRef)
	}
	if row.Target == nil || row.Target.Kind != "repo" || row.Target.ID != "shop" || row.Target.Token == "" {
		t.Fatalf("target: %+v", row.Target)
	}
	if row.RecipeDigest == "" || row.ContextRevision == 0 || row.Raised {
		t.Fatalf("read context: %+v", row)
	}

	ordinary, err := n.d.readSettings(ctx, request, false)
	if err != nil {
		t.Fatal(err)
	}
	ordinaryJSON, _ := json.Marshal(ordinary)
	ctoJSON, _ := json.Marshal(out.Settings)
	if string(ordinaryJSON) != string(ctoJSON) {
		t.Fatalf("public and CTO reads differ:\n%s\n%s", ordinaryJSON, ctoJSON)
	}

	agent, err := n.d.readSettings(ctx, protocol.SettingsReadRequest{Key: "workers.auto_continue", Target: &protocol.SettingsTarget{Level: "agent", Name: "shop"}}, false)
	if err != nil || len(agent) != 1 {
		t.Fatalf("agent namesake: %+v %v", agent, err)
	}
	if agent[0].Target == nil || agent[0].Target.Kind != "agent" || agent[0].Target.ID != "shop" || agent[0].Explicit != nil || agent[0].ExplicitNote != "not set here" {
		t.Fatalf("agent namesake read: %+v", agent[0])
	}

	legacy, err := n.d.readSettings(ctx, protocol.SettingsReadRequest{Key: "workers.auto_continue", Repo: "shop"}, false)
	if err != nil || len(legacy) != 1 || string(legacy[0].Value) != "false" || string(legacy[0].Explicit) != "false" || legacy[0].Target != nil {
		t.Fatalf("legacy repo read changed: %+v %v", legacy, err)
	}

	if _, err = n.d.readSettings(ctx, protocol.SettingsReadRequest{Key: "workers.auto_continue", Repo: "shop", Target: request.Target}, false); err == nil {
		t.Fatal("target and repo accepted together")
	}
	if _, err = n.d.readSettings(ctx, protocol.SettingsReadRequest{Key: "workers.auto_continue", Target: &protocol.SettingsTarget{Level: "repo", Name: "missing"}}, false); err == nil {
		t.Fatal("unknown target read")
	}
}

func TestSettingsGuidanceUndoPreservesAntigravityAcrossStart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	if err := os.Mkdir(filepath.Join(home, ".gemini"), 0755); err != nil {
		t.Fatal(err)
	}
	if r := agentfiles.SyncAntigravity("/tmp/agent-net-test"); r.Err != nil {
		t.Fatal(r.Err)
	}
	path := filepath.Join(agentfiles.AntigravityPluginDir(), "rules", "AGENTS.md")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	original = append(original, []byte("\n# Personal\nLeave this alone.\n")...)
	if err = os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.RefreshAgentFiles = agentfiles.SyncAndLogWithSnapshot }})
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("startup lost text: %s %v", got, err)
	}
	val, _ := json.Marshal("Changed intro.")
	result, err := g.gw.Store().WriteSettings([]gateway.SettingsChange{{Key: "guidance.intro_text", Level: "network", Scope: "network", Value: val}}, "cto", "test")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "changed guidance", func() bool {
		b, _ := os.ReadFile(path)
		return strings.Contains(string(b), "Changed intro.") && strings.Contains(string(b), "# Personal")
	})
	if _, err = g.gw.Store().UndoSettings(result.Revision, "cto"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "restored guidance", func() bool { b, _ := os.ReadFile(path); return bytes.Equal(b, original) })
	_ = n
}

func TestSettingsLegacyGraphOverridesBeforeDeliverySync(t *testing.T) {
	d := &Daemon{}
	overrides := map[string]string{protocol.SettingMergeAuthority: protocol.MergeAsk, protocol.SettingDeployRule: protocol.DeployOwner}
	check := func(name string) {
		t.Helper()
		got := protocol.NodeDefaultsWithSettings("growing", overrides, d.SettingsSnapshot(), "repo")
		if got.MergeAuthority != protocol.MergeAsk || got.DeployRule != protocol.DeployOwner {
			t.Errorf("%s: %+v", name, got)
		}
	}
	check("before sync")
	old, err := d.cleanSettingsSnapshot([]byte(`{"revision":4,"recipe":"old-recipe","values":[{"key":"delivery.merge_authority","level":"repo","scope":"repo","value":"full"}],"maturity":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	d.settingsSnapshot.Store(&old)
	check("pre-cutover snapshot")
	modern, err := d.cleanSettingsSnapshot([]byte(`{"revision":5,"recipe":"current","delivery_cutover":true,"values":[],"maturity":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	d.settingsSnapshot.Store(&modern)
	got := protocol.NodeDefaultsWithSettings("growing", overrides, d.SettingsSnapshot(), "repo")
	if got.MergeAuthority != protocol.MergeFull || got.DeployRule != protocol.DeployBrief {
		t.Fatalf("authoritative snapshot: %+v", got)
	}
}

func TestSettingsDeliveryCutoverSurvivesOfflineRestart(t *testing.T) {
	t.Parallel()
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	d := &Daemon{st: st}
	if err = d.acceptSettingsSnapshot([]byte(`{"revision":1,"recipe":"current","delivery_cutover":true,"values":[],"maturity":{}}`), true); err != nil {
		t.Fatal(err)
	}
	restarted := &Daemon{st: st}
	if err = restarted.loadSettingsSnapshot(); err != nil {
		t.Fatal(err)
	}
	if !restarted.SettingsSnapshot().DeliveryCutover {
		t.Fatal("cutover flag lost on restart")
	}
}

func TestSettingsWorkerSandboxCutoverSurvivesOfflineRestart(t *testing.T) {
	t.Parallel()
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	d := &Daemon{st: st}
	raw := []byte(`{"revision":1,"recipe":"current","worker_sandbox_cutover":true,"values":[{"key":"workers.sandbox_level","level":"repo","scope":"repo","value":"read-only"}],"maturity":{}}`)
	if err = d.acceptSettingsSnapshot(raw, true); err != nil {
		t.Fatal(err)
	}
	restarted := &Daemon{st: st}
	if err = restarted.loadSettingsSnapshot(); err != nil {
		t.Fatal(err)
	}
	snap := restarted.SettingsSnapshot()
	if !snap.WorkerSandboxCutover || protocol.NodeDefaultsWithSettings("early", map[string]string{protocol.SettingWorkerSandbox: "no-network"}, snap, "repo").WorkerSandbox != "read-only" {
		t.Fatalf("lost sandbox snapshot: %+v", snap)
	}
}

func TestWorkerBriefRuleCTOProposesOwnerApplies(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	raw := json.RawMessage(`{"area":"values_write","changes":[{"key":"workers.brief_codex_complete","level":"repo","scope":"shop","value":"Report {{complete_yes}} or {{complete_no}}."}],"reason":"approval"}`)
	if _, err := n.d.runCTOToolRaw(ctx, "settings_change", raw); err != nil {
		t.Fatal(err)
	}
	proposals, err := n.d.proposalRunner().list()
	if err != nil || len(proposals) != 1 {
		t.Fatalf("proposal: %+v %v", proposals, err)
	}
	before, err := g.gw.Store().SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := before.String("workers.brief_codex_complete", settings.Where{Repo: "shop"}); strings.Contains(got, "Report ") {
		t.Fatalf("CTO write applied before approval: %q", got)
	}
	if err = n.d.proposalRunner().decide(ctx, proposals[0].ID, true); err != nil {
		t.Fatal(err)
	}
	after, err := g.gw.Store().SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := after.String("workers.brief_codex_complete", settings.Where{Repo: "shop"}); got != "Report {{complete_yes}} or {{complete_no}}." {
		t.Fatalf("approved value: %q", got)
	}
}

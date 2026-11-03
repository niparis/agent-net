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
	"github.com/niparis/agent-net/internal/settings"
)

func TestSettingsSnapshotOfflineAndOrder(t *testing.T) {
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

func TestSettingsSnapshotLinkIgnoresUnknownFrames(t *testing.T) {
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
	out, err := n.d.readCTOTool(ctx, "settings", json.RawMessage(`{"area":"values","key":"workers.auto_continue"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	if !strings.Contains(string(b), `"source"`) {
		t.Fatalf("missing source: %s", b)
	}
}

func TestSettingsOwnerGateAndUndo(t *testing.T) {
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
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	invalid := []string{
		`[{"key":"delivery.merge_authority","level":"team","scope":"alpha","value":"ask"}]`,
		`[{"key":"delivery.merge_authority","level":"repo","scope":"","value":"ask"}]`,
		`[{"key":"delivery.merge_authority","level":"network","scope":"other","value":"ask"}]`,
		`[{"key":"workers.auto_continue","level":"repo","scope":"alpha","value":false}]`,
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

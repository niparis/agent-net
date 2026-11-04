package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/funnel"
)

func TestSettingsTabAndRemovedKeys(t *testing.T) {
	m := newModel(120, 34)
	for _, key := range []string{"S", "t", "p"} {
		n := keys(m, key)
		if n.mode != modeNormal {
			t.Errorf("%s still acts", key)
		}
	}
	m = keys(m, "6")
	if m.tab != 5 {
		t.Fatalf("6: tab = %d", m.tab)
	}
	view := ansi.Strip(m.testView())
	for _, want := range []string{"Connected apps", "Integrations", "Machines", "Notifications", "Tips"} {
		if !strings.Contains(view, want) {
			t.Errorf("settings lacks %q", want)
		}
	}
}

func TestSettingsSessionCells(t *testing.T) {
	src := &ruleSource{fakeSource: fakeSource{snap: fakeSnapshot()}}
	m := newModel(120, 34)
	m.src = src
	m = selectID(m, "s_0000000000000001")
	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if got := ansi.Strip(m.helpLine(120)); !strings.Contains(got, "space") || !strings.Contains(got, "ask") {
		t.Errorf("rule hint: %s", got)
	}
	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeySpace})
	if cmd == nil {
		t.Fatal("space on RULE did not call the request rule operation")
	}
	cmd()
	if len(src.edges) != 1 || src.edges[0].Rule != "ask" {
		t.Errorf("rule calls: %+v", src.edges)
	}
	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyRight})
	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != modePolicy {
		t.Fatalf("enter on DELEGATE: mode %v", m.mode)
	}
	m = keys(m, "esc", "esc")
	if got := ansi.Strip(m.helpLine(120)); strings.Contains(got, "space") {
		t.Errorf("esc did not return to NAME: %s", got)
	}
}

func TestSettingsOtherRoutesAndViewIndicators(t *testing.T) {
	m := newModel(120, 34)
	m = keys(m, "6")
	result := appsResult{op: "apps.public.status", raw: json.RawMessage(`{"on":true,"url":"https://example.test","other_routes":[8443,10000]}`)}
	m, _ = m.onAppsResult(result)
	view := ansi.Strip(m.testView())
	if !strings.Contains(view, "8443") || !strings.Contains(strings.Join(strings.Fields(view), " "), "sign-in cookie") {
		t.Errorf("missing other routes warning:\n%s", view)
	}
	s := newModel(120, 34)
	title := ansi.Strip(s.titleLine(120))
	s = keys(s, "v")
	if got := ansi.Strip(s.titleLine(120)); got != title {
		t.Errorf("session view changed tab bar: %q != %q", got, title)
	}
	if !strings.Contains(ansi.Strip(strings.Join(s.sessionsTable(120, 10), "\n")), "view:") {
		t.Error("missing session view indicator")
	}
	s = keys(s, "4")
	title = ansi.Strip(s.titleLine(120))
	s = keys(s, "l")
	if got := ansi.Strip(s.titleLine(120)); got != title {
		t.Errorf("message filter changed tab bar: %q != %q", got, title)
	}
}

func selectSettingsKind(m Model, kind string) Model {
	for i, row := range m.settingsItems() {
		if row.kind == kind {
			m.settingsRow = i
			return m
		}
	}
	return m
}

func TestSettingsActionsUseExistingOperations(t *testing.T) {
	f := &connectedAppsFake{result: `{"apps":[]}`}
	m := newModel(120, 34)
	m.src = f
	m, cmd := press(m, keyRunes("6"))
	if m.tab != settingsTab || cmd == nil {
		t.Fatal("opening Settings did not load apps")
	}
	m = runAppsCmd(t, m, cmd)
	if f.op != "apps.list" {
		t.Fatalf("load: %s", f.op)
	}
	m.apps.loading = false
	m = selectSettingsKind(m, "public")
	m, cmd = press(m, tea.KeyPressMsg{Code: tea.KeySpace})
	if cmd == nil {
		t.Fatal("public toggle")
	}
	cmd()
	if f.op != "apps.public.on" {
		t.Fatal(f.op)
	}
	m.apps.loading = false
	m.apps.public.Error = funnel.ErrNotInstalled.Error()
	m = selectSettingsKind(m, "url")
	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != modeApps || m.apps.stage != "url" {
		t.Fatal("URL editor")
	}
	m = keys(m, "esc")
	m.apps.public.On = true
	m = selectSettingsKind(m, "add")
	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.apps.stage != "code" {
		t.Fatal("add flow")
	}
	m = keys(m, "esc")
	m.apps.apps = []connectedApp{{ID: "app-1", Name: "client"}}
	m = selectSettingsKind(m, "app")
	m, _ = press(m, keyRunes("x"))
	if m.apps.stage != "confirm" {
		t.Fatal("revoke confirmation")
	}
	m, cmd = press(m, keyRunes("y"))
	if cmd == nil {
		t.Fatal("revoke command")
	}
	cmd()
	if f.op != "apps.revoke" || f.params["id"] != "app-1" {
		t.Fatalf("revoke %s %+v", f.op, f.params)
	}

	base, src := workerModel(120, 34)
	base.tab = settingsTab
	base = selectSettingsKind(base, "integrations")
	base, cmd = press(base, tea.KeyPressMsg{Code: tea.KeyEnter})
	if base.mode != modeIntegrations || cmd == nil {
		t.Fatal("integrations form")
	}
	base.mode = modeNormal
	base = selectSettingsKind(base, "machine")
	machine := base.machineRows()[base.settingsItems()[base.settingsRow].index]
	base, cmd = press(base, tea.KeyPressMsg{Code: tea.KeyEnter})
	if base.mode != modeRename || base.renameNode != machine.nodeID {
		t.Fatal("machine rename")
	}
	base.rename.SetValue("desk")
	base, cmd = press(base, tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = finish(t, base, cmd)
	if len(src.renames) != 1 || src.renames[0].id != machine.nodeID {
		t.Fatalf("rename %+v", src.renames)
	}
}

func TestSettingsSectionNavigationAndHelp(t *testing.T) {
	m := newModel(120, 34)
	m.tab = settingsTab
	for _, want := range []string{"integrations", "machine", "timezone", "notifications", "tips", "setting-repo", "public"} {
		m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyTab})
		if got := m.settingsItems()[m.settingsRow].kind; got != want {
			t.Fatalf("tab: got %s want %s", got, want)
		}
	}
	m, _ = press(m, keyRunes("["))
	if got := m.settingsItems()[m.settingsRow].kind; got != "setting-repo" {
		t.Fatalf("previous section: %s", got)
	}
	m.help = true
	m.helpAll = true
	for _, key := range []string{"N", "T", "R"} {
		if _, cmd := press(m, keyRunes(key)); cmd != nil {
			t.Errorf("help key %s acted", key)
		}
	}
	help := ansi.Strip(strings.Join(m.helpContent(120), "\n"))
	for _, want := range []string{"Settings", "move between NAME, RULE and DELEGATE", "cycle the request rule", "desktop notifications", "restart tips"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

func TestSettingsGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		w, h := size[0], size[1]
		m := newModel(w, h)
		m.tab = settingsTab
		m.apps.public.On = true
		m.apps.public.Port = 8443
		m.apps.public.URL = "https://example.test:8443"
		m.apps.public.OtherRoutes = json.RawMessage(`[10000]`)
		m.apps.apps = []connectedApp{{ID: "a1", Name: "phone", Kind: "owner_app", ClientName: "Safari"}, {ID: "a2", Name: "helper", Kind: "bot", ClientName: "Cursor"}}
		golden(t, fmt.Sprintf("settings_%dx%d", w, h), m.testView())
		m.apps.public = appsPublicStatus{}
		golden(t, fmt.Sprintf("settings_off_%dx%d", w, h), m.testView())
		m.apps.public.Error = "Tailscale is not installed on research-gateway (the gateway machine). Install it there, or use your own tunnel URL (u)."
		golden(t, fmt.Sprintf("settings_public_error_%dx%d", w, h), m.testView())
		s := newModel(w, h)
		s, _ = press(s, tea.KeyPressMsg{Code: tea.KeyRight})
		golden(t, fmt.Sprintf("sessions_cells_%dx%d", w, h), s.testView())
		s.activeOnly = true
		golden(t, fmt.Sprintf("sessions_active_%dx%d", w, h), s.testView())
	}
}

func TestSettingsValuesRenderAndSave(t *testing.T) {
	src := &settingsValuesTestSource{fakeSource: &fakeSource{snap: fakeSnapshot()}}
	m := New(src)
	m.tab = settingsTab
	m.width = 120
	m.height = 50
	m.settingValues = []protocol.SettingView{{Key: "workers.auto_continue", Value: json.RawMessage(`true`), Source: "network", Actor: "owner", History: []protocol.SettingsHistory{{Revision: 1, Actor: "owner", Reason: "test"}}}}
	view := ansi.Strip(strings.Join(m.settingsView(120, 50), "\n"))
	for _, want := range []string{"workers.auto_continue", "true", "set by you"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q: %s", want, view)
		}
	}
	for i, item := range m.settingsItems() {
		if item.kind == "setting-value" {
			m.settingsRow = i
			break
		}
	}
	m2, _ := m.settingsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = m2.(Model)
	view = ansi.Strip(strings.Join(m.settingsView(120, 50), "\n"))
	if !strings.Contains(view, "test") {
		t.Fatal("history missing")
	}
	m.rename.SetValue("false")
	next, cmd := m.settingValueKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = next
	if cmd == nil {
		t.Fatal("no save")
	}
	_ = cmd()
	if src.write.Changes[0].Key != "workers.auto_continue" || string(src.write.Changes[0].Value) != "false" {
		t.Fatalf("write: %+v", src.write)
	}
}

type settingsValuesTestSource struct {
	*fakeSource
	write      protocol.SettingsWrite
	undo       protocol.SettingsUndo
	graph      protocol.Graph
	ownerWrite protocol.DeliveryOwnerSetRequest
}

func (s *settingsValuesTestSource) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	if method == "settings_undo" {
		s.undo = params.(protocol.SettingsUndo)
		return json.RawMessage(`{"revision":8}`), nil
	}
	if method == "settings_write" {
		s.write = params.(protocol.SettingsWrite)
		return json.RawMessage(`{"revision":2}`), nil
	}
	if method == "graph_get" {
		return json.Marshal(s.graph)
	}
	if method == "delivery_owner_set" {
		s.ownerWrite = params.(protocol.DeliveryOwnerSetRequest)
		return json.RawMessage(`{"ok":true}`), nil
	}
	return json.RawMessage(`[]`), nil
}

func TestSettingsDeliveryNodeOwnerEdit(t *testing.T) {
	src := &settingsValuesTestSource{fakeSource: &fakeSource{snap: fakeSnapshot()}, graph: protocol.Graph{Edges: []protocol.Edge{{From: "reviewer", To: "alpha", Kind: "reviews"}, {From: "/delivery/github", To: "alpha", Kind: "tests_after_merge"}}}}
	m := New(src)
	m.settingRepo = "alpha"
	msg := m.loadSettingValues()().(settingValuesMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	m.settingValues = msg.rows
	index := -1
	afterTest := false
	for i, row := range m.settingValues {
		if row.Key == "delivery.owner.test.after_merge" && string(row.Value) == `"@github"` {
			afterTest = true
		}
		if row.Key == "delivery.owner.approve" && !strings.Contains(row.Description, "approval rule") {
			t.Fatal("approval assignment did not explain its existing approval rule")
		}
		if row.Key == "delivery.owner.deploy" && !strings.Contains(row.Description, "when the pipeline has a deploy step") {
			t.Fatal("deploy owner does not explain when it applies")
		}
		if row.Key == "delivery.owner.review" {
			index = i
			if string(row.Value) != `"reviewer"` {
				t.Fatalf("owner = %s", row.Value)
			}
		}
	}
	if index < 0 {
		t.Fatal("review owner is missing from Settings")
	}
	if !afterTest {
		t.Fatal("post-merge test owner is missing from Settings")
	}
	m, _ = m.startSettingValueEdit(index)
	m.rename.SetValue(`true`)
	invalid, invalidCmd := m.settingValueKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if invalidCmd != nil || invalid.(Model).mode != modeSettingValue {
		t.Fatal("invalid owner value did not keep the editor open")
	}
	m = invalid.(Model)
	m.rename.SetValue(`"other"`)
	_, cmd := m.settingValueKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("owner assignment was not sent")
	}
	if msg := cmd().(settingValuesMsg); msg.err != nil {
		t.Fatal(msg.err)
	}
	want := protocol.DeliveryOwnerSetRequest{Project: "alpha", Step: "review", Node: "other", Expect: "reviewer"}
	if src.ownerWrite != want {
		t.Fatalf("owner write = %+v, want %+v", src.ownerWrite, want)
	}
}

func TestSettingsRepoEditUsesRepoAddress(t *testing.T) {
	src := &settingsValuesTestSource{fakeSource: &fakeSource{snap: fakeSnapshot()}}
	m := New(src)
	m.settingRepo = "alpha"
	m.settingValues = []protocol.SettingView{{Key: "delivery.merge_authority", Levels: []string{"network", "repo"}, Value: json.RawMessage(`"full"`), Source: "repo alpha", Explicit: json.RawMessage(`"full"`)}}
	m, _ = m.startSettingValueEdit(0)
	m.rename.SetValue(`"ask"`)
	_, cmd := m.settingValueKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = cmd()
	change := src.write.Changes[0]
	if change.Level != "repo" || change.Scope != "alpha" || string(change.Expect) != `"full"` {
		t.Fatalf("repo write %+v", change)
	}
}

func TestSettingsUndoSelectedRevision(t *testing.T) {
	src := &settingsValuesTestSource{fakeSource: &fakeSource{snap: fakeSnapshot()}}
	m := New(src)
	m.tab = settingsTab
	m.settingValues = []protocol.SettingView{{Key: "workers.auto_continue", Value: json.RawMessage(`false`), History: []protocol.SettingsHistory{{Revision: 7, Actor: "owner"}}}}
	for i, item := range m.settingsItems() {
		if item.kind == "setting-value" {
			m.settingsRow = i
			break
		}
	}
	next, _ := m.settingsKey(keyRunesForSettings("u"))
	m = next.(Model)
	if m.mode != modeSettingUndo {
		t.Fatal("undo confirmation missing")
	}
	_, cmd := m.settingUndoKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("undo not sent")
	}
	_ = cmd()
	if src.undo.Revision != 7 {
		t.Fatalf("undo %+v", src.undo)
	}
}

func TestSettingsWarnsAboutOnlineOldDaemons(t *testing.T) {
	m := newModel(120, 80)
	m.tab = settingsTab
	m.snap.Nodes = []protocol.NodeView{
		{Hostname: "old-a", Version: "v0.4.116", Online: true},
		{Hostname: "old-b", Version: "v0.3.9", Online: true},
		{Hostname: "current", Version: "v0.4.117", Online: true},
		{Hostname: "offline", Version: "v0.4.10", Online: false},
		{Hostname: "unknown", Version: "dev", Online: true},
	}
	m = selectSettingsKind(m, "setting-repo")
	view := ansi.Strip(strings.Join(m.settingsView(120, 80), "\n"))
	if !strings.Contains(view, "Settings updates may not reach old daemons: old-a, old-b") {
		t.Fatalf("missing old machines: %s", view)
	}
	if !strings.Contains(view, "Settings sync support unknown: unknown") {
		t.Fatalf("missing unknown machine: %s", view)
	}
	older, unknown := m.settingsSyncMachines()
	if strings.Join(older, ",") != "old-a,old-b" || strings.Join(unknown, ",") != "unknown" {
		t.Fatalf("older=%v unknown=%v", older, unknown)
	}

}

func TestSettingsSyncVersionBoundary(t *testing.T) {
	for _, tc := range []struct {
		version      string
		old, unknown bool
	}{
		{"v0.4.116", true, false}, {"0.4.116-rc.1", true, false}, {"v0.4.117", false, false}, {"v0.4.117+build", false, false}, {"v0.4.117-rc.1", false, true}, {"v0.5.0", false, false}, {"", false, true}, {"dev", false, true}, {"broken", false, true},
	} {
		old, unknown := settingsSyncVersion(tc.version)
		if old != tc.old || unknown != tc.unknown {
			t.Errorf("%q: old=%v unknown=%v", tc.version, old, unknown)
		}
	}
}

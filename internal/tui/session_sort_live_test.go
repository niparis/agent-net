package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/protocol"
)

// liveSortNow is the moment of the owner's report on 2026-11-08. The activity
// times below are the gateway's sessions.last_active values at that moment.
var liveSortNow = time.Date(2026, 11, 8, 16, 35, 19, 0, time.UTC)

// liveSortRows is the Working group from the owner's screenshot, in the order
// the screen showed it. Two rows sat out of place: wbg-procurement (under
// wbg-worldview) and wbg-flood-wargame (under tenebrae). Both are worker rows
// of those callers, which is why they appeared directly after them.
func liveSortSnapshot() Snapshot {
	type row struct {
		name    string
		at      string
		status  string
		harness string
		mode    string
		owner   string
		lead    bool
	}
	rows := []row{
		{name: "homelab-agent", at: "16:34:56", status: "busy", harness: "claude"},
		{name: "cursor", at: "16:35:19", status: "idle", harness: "bot"},
		{name: "legion-and-legends", at: "16:25:37", status: "idle", harness: "opencode"},
		{name: "tenebrae-strats", at: "16:25:37", status: "idle", harness: "opencode", lead: true},
		{name: "wbg-worldview", at: "16:25:09", status: "idle", harness: "opencode"},
		{name: "wbg-procurement", at: "16:34:27", status: "exited", harness: "claude", mode: protocol.ModeBackground, owner: "wbg-worldview"},
		{name: "ai-arch", at: "16:08:28", status: "idle", harness: "opencode"},
		{name: "wbg-infra-game", at: "15:57:11", status: "idle", harness: "opencode"},
		{name: "wbg-whatif", at: "15:53:29", status: "idle", harness: "opencode"},
		{name: "wbg-housing 2", at: "15:53:17", status: "idle", harness: "opencode"},
		{name: "tenebrae", at: "15:52:41", status: "idle", harness: "opencode", lead: true},
		{name: "wbg-flood-wargame", at: "16:34:24", status: "idle", harness: "opencode", mode: protocol.ModeBackground, owner: "tenebrae"},
		{name: "voicemechs", at: "15:50:36", status: "idle", harness: "opencode"},
	}
	ids := map[string]string{}
	for i, r := range rows {
		ids[r.name] = "s_live_" + itoa(i+1)
	}
	snap := fakeSnapshot()
	snap.Agents = nil
	for i, r := range rows {
		clock, err := time.Parse("15:04:05", r.at)
		if err != nil {
			panic(err)
		}
		at := time.Date(liveSortNow.Year(), liveSortNow.Month(), liveSortNow.Day(), clock.Hour(), clock.Minute(), clock.Second(), 0, time.UTC)
		a := agent(ids[r.name], "n_mac", "Mac", r.harness, r.name, func(a *protocol.AgentView) {
			a.LastActive, a.LastSeen = at.Unix(), at.Unix()
			a.Status = r.status
			a.Mode = r.mode
			a.TeamLead = r.lead
			if r.owner != "" {
				a.Worker = &protocol.WorkerInfo{ID: "w_" + itoa(i+1), Owner: ids[r.owner], Project: strings.TrimSuffix(r.owner, " "), Task: r.name}
			}
		})
		snap.Agents = append(snap.Agents, a)
	}
	return snap
}

// liveSortModel builds the scene at 120 columns. The first refresh happens
// before the report time so the idle margins see the bands the live screen had.
func liveSortModel(all bool) Model {
	src := &fakeSource{snap: liveSortSnapshot()}
	m := New(src)
	m.loc = time.UTC
	m.activeOnly, m.showOffline = !all, all
	m.now = func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = settle(refresh(m, src))
	m.now = func() time.Time { return liveSortNow }
	return settle(refresh(m, src))
}

func TestSessionsLiveSortOrder(t *testing.T) {
	m := liveSortModel(false)
	want := []string{
		"homelab-agent",
		"tenebrae", "wbg-flood-wargame", // the tree rises on its most recent member
		"legion-and-legends",
		"tenebrae-strats",
		"wbg-worldview",
		"ai-arch",
		"wbg-infra-game",
		"wbg-whatif",
		"wbg-housing 2",
		"voicemechs",
		"cursor", // bots have their own group at the bottom
	}
	if got := orderedTableNames(m); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Working order:\n got %v\nwant %v", got, want)
	}
	var hosts []string
	for _, g := range m.groups() {
		hosts = append(hosts, g.host)
	}
	if got := strings.Join(hosts, ","); got != "Working,Bots" {
		t.Fatalf("groups: %s, want Working,Bots", got)
	}
	for _, name := range orderedTableNames(m) {
		if name == "wbg-procurement" {
			t.Fatal("an exited worker stays in Working")
		}
	}
}

func TestExitedSessionLeavesWorking(t *testing.T) {
	snap := fakeSnapshot()
	snap.Agents = []protocol.AgentView{
		agent("s_live_root", "n_mac", "Mac", "opencode", "live-root", func(a *protocol.AgentView) {
			a.LastActive, a.LastSeen = liveSortNow.Add(-2*time.Minute).Unix(), liveSortNow.Add(-2*time.Minute).Unix()
		}),
		agent("s_exited_root", "n_mac", "Mac", "claude", "exited-root", func(a *protocol.AgentView) {
			a.Status, a.Online = "exited", true
			a.LastActive, a.LastSeen = liveSortNow.Add(-time.Minute).Unix(), liveSortNow.Add(-time.Minute).Unix()
		}),
		agent("s_exited_worker", "n_mac", "Mac", "claude", "exited-worker", func(a *protocol.AgentView) {
			a.Status, a.Online = "exited", true
			a.LastActive, a.LastSeen = liveSortNow.Add(-time.Minute).Unix(), liveSortNow.Add(-time.Minute).Unix()
			a.Worker = &protocol.WorkerInfo{ID: "w_exited", Owner: "s_live_root", Task: "exited-worker"}
		}),
		agent("s_bot", "n_app", "app", "bot", "cursor-bot", func(a *protocol.AgentView) {
			a.LastActive, a.LastSeen = liveSortNow.Unix(), liveSortNow.Unix()
		}),
	}
	src := &fakeSource{snap: snap}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return liveSortNow }
	m.activeOnly, m.showOffline = false, true
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = settle(refresh(m, src))
	for _, g := range m.groups() {
		for _, a := range g.agents {
			if a.SessionID == "s_exited_root" && g.host != "Idle" {
				t.Fatalf("exited root is in %s, want Idle", g.host)
			}
			if a.SessionID == "s_exited_worker" {
				t.Fatalf("exited worker is shown in %s", g.host)
			}
			if a.SessionID == "s_bot" && g.host != "Bots" {
				t.Fatalf("bot is in %s, want Bots", g.host)
			}
		}
	}
	var sawExited, sawBot bool
	for _, g := range m.groups() {
		for _, a := range g.agents {
			sawExited = sawExited || a.SessionID == "s_exited_root"
			sawBot = sawBot || a.SessionID == "s_bot"
		}
	}
	if !sawExited || !sawBot {
		t.Fatalf("all view misses the exited root or the bot: exited=%v bot=%v", sawExited, sawBot)
	}
}

func TestSessionsLiveSortScene(t *testing.T) {
	golden(t, "sessions_sort_live_120", liveSortModel(false).testView())
}

func TestSessionsExitedScene(t *testing.T) {
	m := liveSortModel(true)
	snap := m.snap
	snap.Agents = append(snap.Agents, agent("s_exited_root", "n_mac", "Mac", "claude", "wbg-retired", func(a *protocol.AgentView) {
		a.Status, a.Online = "exited", true
		a.LastActive, a.LastSeen = liveSortNow.Add(-3*time.Minute).Unix(), liveSortNow.Add(-3*time.Minute).Unix()
	}))
	m.snap = snap
	m = settle(refresh(m, &fakeSource{snap: snap}))
	golden(t, "sessions_sort_exited_120", m.testView())
}

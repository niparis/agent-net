package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestDisplayTimeUsesTheOwnerZone(t *testing.T) {
	m := Model{loc: mustZone(t, "Europe/Paris")}
	// 2023-11-14 22:13:20 UTC is 23:13 in Paris.
	if got := m.displayTime(1700000000).Format("2006-01-02 15:04"); got != "2023-11-14 23:13" {
		t.Fatalf("display time: %s", got)
	}
	m.loc = mustZone(t, "America/New_York")
	if got := m.displayTime(1700000000).Format("15:04"); got != "17:13" {
		t.Fatalf("display time in New York: %s", got)
	}
}

func TestTimeZoneAutoPrefersTheMacOverAUTCserver(t *testing.T) {
	nodes := []protocol.NodeView{
		{NodeID: "n_server", Hostname: "research", OS: "linux", TimeZone: "Etc/UTC"},
		{NodeID: "n_mac", Hostname: "Nicolas-MBP", OS: "darwin", TimeZone: "Europe/Paris"},
	}
	zone, source, ok := timeZoneChoice("auto", nodes)
	if !ok || zone != "Europe/Paris" || source != "auto, from Nicolas-MBP" {
		t.Fatalf("auto: %q %q %v", zone, source, ok)
	}
	// Node order does not matter: the Mac wins wherever it is listed.
	zone, source, ok = timeZoneChoice("", []protocol.NodeView{nodes[1], nodes[0]})
	if !ok || zone != "Europe/Paris" || !strings.Contains(source, "Nicolas-MBP") {
		t.Fatalf("auto reversed: %q %q %v", zone, source, ok)
	}
}

func TestTimeZoneAutoFallsBackToUTCWhenEveryMachineIsUTC(t *testing.T) {
	zone, source, ok := timeZoneChoice("auto", []protocol.NodeView{{NodeID: "n_1", Hostname: "research", OS: "linux", TimeZone: "Etc/UTC"}})
	if !ok || zone != "Etc/UTC" || source != "auto, from research" {
		t.Fatalf("all UTC: %q %q %v", zone, source, ok)
	}
	if _, _, ok := timeZoneChoice("auto", nil); ok {
		t.Fatal("auto with no machines should resolve nothing")
	}
}

func TestTimeZoneSettingOverridesAuto(t *testing.T) {
	nodes := []protocol.NodeView{{NodeID: "n_mac", Hostname: "Nicolas-MBP", OS: "darwin", TimeZone: "Europe/Paris"}}
	zone, source, ok := timeZoneChoice("America/New_York", nodes)
	if !ok || zone != "America/New_York" || source != "set by you" {
		t.Fatalf("set: %q %q %v", zone, source, ok)
	}
	m := Model{loc: time.UTC, snap: Snapshot{TimeZone: "America/New_York", Nodes: nodes}}
	m.applyTimeZone()
	if m.loc.String() != "America/New_York" || m.zoneName != "America/New_York" || m.zoneSource != "set by you" {
		t.Fatalf("applied: %v %q %q", m.loc, m.zoneName, m.zoneSource)
	}
	if got := m.timeZoneLine(); got != "Time zone: America/New_York (set by you)" {
		t.Fatalf("row: %s", got)
	}
}

func TestTimeZoneAutoKeepsTheDisplayWhenNothingReported(t *testing.T) {
	m := Model{loc: time.UTC, snap: Snapshot{TimeZone: protocol.TimeZoneAuto}}
	m.applyTimeZone()
	if m.loc.String() != "UTC" || m.zoneName != "" {
		t.Fatalf("auto with no machines changed the zone: %v %q", m.loc, m.zoneName)
	}
	if got := m.timeZoneLine(); got != "Time zone: auto" {
		t.Fatalf("row: %s", got)
	}
}

type zoneSource struct {
	*fakeSource
	written *string
}

func (z *zoneSource) TimeZoneSettings(_ context.Context, name *string) (protocol.TimeZoneSettings, error) {
	if name == nil {
		return protocol.TimeZoneSettings{Name: protocol.TimeZoneAuto}, nil
	}
	z.written = name
	return protocol.TimeZoneSettings{Name: *name}, nil
}

func TestTimeZoneSettingRefusesAnUnknownName(t *testing.T) {
	src := &zoneSource{fakeSource: &fakeSource{snap: fakeSnapshot()}}
	m := New(src)
	m.mode = modeTimeZone
	m.rename.SetValue("Mars/Olympus")
	next, cmd := m.timeZoneKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if cmd != nil || m.mode != modeTimeZone || !m.notice.err || !strings.Contains(m.notice.text, "unknown time zone") {
		t.Fatalf("unknown name accepted: mode %v cmd %v notice %q", m.mode, cmd, m.notice.text)
	}
	if src.written != nil {
		t.Fatalf("the daemon was asked to save %q", *src.written)
	}
}

func TestTimeZoneSettingSavesAValidName(t *testing.T) {
	src := &zoneSource{fakeSource: &fakeSource{snap: fakeSnapshot()}}
	m := New(src)
	m.mode = modeTimeZone
	m.rename.SetValue("Europe/Paris")
	next, cmd := m.timeZoneKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil || m.mode != modeNormal {
		t.Fatalf("valid name not saved: mode %v cmd %v", m.mode, cmd)
	}
	if msg, ok := cmd().(actionMsg); !ok || msg.err != nil || !strings.Contains(msg.text, "Europe/Paris") {
		t.Fatalf("save result: %#v", cmd())
	}
	if src.written == nil || *src.written != "Europe/Paris" {
		t.Fatalf("written: %v", src.written)
	}
}

func TestSettingsTimeZoneRowAndEditor(t *testing.T) {
	m := newModel(120, 34)
	m.tab = settingsTab
	m.snap.TimeZone = protocol.TimeZoneAuto
	m.snap.Nodes = []protocol.NodeView{{NodeID: "n_mac", Hostname: "Nicolas-MBP", OS: "darwin", TimeZone: "Europe/Paris"}}
	m.applyTimeZone()
	row := ""
	for _, item := range m.settingsItems() {
		if item.kind == "timezone" {
			row = item.text
		}
	}
	if row != "Time zone: Europe/Paris (auto, from Nicolas-MBP)" {
		t.Fatalf("row: %q", row)
	}
	view := ansi.Strip(strings.Join(m.settingsView(120, 30), "\n"))
	if !strings.Contains(view, "Time zone") || !strings.Contains(view, "Europe/Paris (auto, from Nicolas-MBP)") {
		t.Fatalf("settings view lacks the time zone row: %s", view)
	}
	m = selectSettingsKind(m, "timezone")
	m.src = &zoneSource{fakeSource: &fakeSource{snap: fakeSnapshot()}}
	next, _ := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next
	if m.mode != modeTimeZone || m.rename.Value() != protocol.TimeZoneAuto {
		t.Fatalf("editor: mode %v value %q", m.mode, m.rename.Value())
	}
}

func TestRenderingInTheOwnerZoneLeavesStoredTimesAlone(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.tab = quotasTab
	m.workersLog = true
	m.snap.WorkerLog = []protocol.WorkerLogEntry{{StartedAt: 1700000000, Title: "A run", Model: "m", Status: "done"}}
	m.asks = append(m.asks, askFromHeld(protocol.Message{ID: "m_1", CreatedAt: time.Unix(1700000000, 0).UTC()}))
	m.loc = mustZone(t, "Europe/Paris")
	line := ansi.Strip(strings.Join(m.workersLogView(120, 40), "\n"))
	if !strings.Contains(line, "11-14 23:13") {
		t.Fatalf("render did not use the owner zone: %s", line)
	}
	// Applying a different display zone changes what is drawn, never the
	// stored Unix time it is drawn from.
	m.snap.TimeZone = "America/New_York"
	m.applyTimeZone()
	if got := m.displayTime(m.asks[0].CreatedAt.Unix()).Format("15:04"); got != "17:13" {
		t.Fatalf("display time: %s", got)
	}
	if got := m.snap.WorkerLog[0].StartedAt; got != 1700000000 {
		t.Fatalf("worker run start time changed to %d", got)
	}
	if got := m.asks[0].CreatedAt.Unix(); got != 1700000000 {
		t.Fatalf("stored message time changed to %d", got)
	}
}

func TestWorkersLogShowsTheOwnerZone(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.tab = quotasTab
	m.loc = mustZone(t, "Europe/Paris")
	m.workersLog = true
	m.snap.WorkerLog = []protocol.WorkerLogEntry{{StartedAt: 1700000000, Title: "A run", Model: "m", Status: "done"}}
	got := ansi.Strip(strings.Join(m.workersLogView(120, 40), "\n"))
	if !strings.Contains(got, "11-14 23:13") {
		t.Fatalf("workers log is not in the owner zone: %s", got)
	}
	if strings.Contains(got, "11-14 22:13") {
		t.Fatalf("workers log is still UTC: %s", got)
	}
}

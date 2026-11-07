package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// mineSnapshot marks two sessions pinned and two recently used, one of them
// offline, so the Mine section has every kind of row.
func mineSnapshot() Snapshot {
	snap := fakeSnapshot()
	for i := range snap.Agents {
		switch snap.Agents[i].SessionID {
		case "s_0000000000000002": // strategies
			snap.Agents[i].PinnedAt = t0.Add(-10 * 24 * time.Hour).Unix()
		case "s_0000000000000003": // web
			snap.Agents[i].PinnedAt = t0.Add(-5 * 24 * time.Hour).Unix()
		case "s_0000000000000001": // tenebrae-53
			snap.Agents[i].OwnerUsedAt = t0.Add(-2 * time.Hour).Unix()
			snap.Agents[i].OwnerUses = 6
		case "s_0000000000000006": // old-session, offline
			snap.Agents[i].OwnerUsedAt = t0.Add(-3 * 24 * time.Hour).Unix()
			snap.Agents[i].OwnerUses = 3
		case "s_0000000000000005": // scratch, used too long ago
			snap.Agents[i].OwnerUsedAt = t0.Add(-8 * 24 * time.Hour).Unix()
		}
	}
	return snap
}

func mineModel(w, h int) (Model, *fakeSource) {
	src := &fakeSource{snap: mineSnapshot(), events: fakeEvents()}
	src.usage, src.counts, src.tops = fakeUsage(), fakeCounts(), fakeTops()
	m := New(src)
	m.loc = time.UTC
	m.now = func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	m = settle(refresh(m, src))
	return m, src
}

func countName(m Model, name string) int {
	n := 0
	for _, got := range names(m) {
		if got == name {
			n++
		}
	}
	return n
}

func TestSessionsHaveNoMineOrMachineGroups(t *testing.T) {
	m, _ := mineModel(120, 34)
	for _, g := range m.groups() {
		if g.host != "Needs you" && g.host != "Working" && g.host != "Idle" {
			t.Fatalf("unexpected group %s", g.host)
		}
	}
	if strings.Contains(ansi.Strip(m.testView()), "Mine") {
		t.Fatal("Mine remains")
	}
}

// Owner use and pins outrank activity, but an exact or prefix name match still
// comes first.
func TestRankSessionsRanksPinnedThenOwnerUse(t *testing.T) {
	agents := []protocol.AgentView{
		agent("s_active", "n_mac", "Mac", "claude", "one", func(a *protocol.AgentView) {
			a.LastActive = t0.Add(-time.Minute).Unix()
		}),
		agent("s_pinned", "n_mac", "Mac", "claude", "two", func(a *protocol.AgentView) {
			a.PinnedAt = t0.Add(-3 * 24 * time.Hour).Unix()
			a.LastActive = t0.Add(-20 * time.Hour).Unix()
		}),
		agent("s_often", "n_mac", "Mac", "claude", "three", func(a *protocol.AgentView) {
			a.OwnerUses = 8
			a.OwnerUsedAt = t0.Add(-time.Hour).Unix()
			a.LastActive = t0.Add(-19 * time.Hour).Unix()
		}),
		agent("s_less", "n_mac", "Mac", "claude", "four", func(a *protocol.AgentView) {
			a.OwnerUses = 3
			a.OwnerUsedAt = t0.Add(-2 * time.Hour).Unix()
			a.LastActive = t0.Add(-18 * time.Hour).Unix()
		}),
	}
	got := rankSessions("", agents, t0)
	var gotNames []string
	for _, a := range got {
		gotNames = append(gotNames, rowName(a))
	}
	want := "two,three,four,one"
	if strings.Join(gotNames, ",") != want {
		t.Fatalf("ranked %v, want %s", gotNames, want)
	}

	named := []protocol.AgentView{
		agent("s_exact", "n_mac", "Mac", "claude", "alpha", nil),
		agent("s_pinned_prefix", "n_mac", "Mac", "claude", "alphabet", func(a *protocol.AgentView) {
			a.PinnedAt = t0.Add(-time.Hour).Unix()
		}),
	}
	if got := rankSessions("alpha", named, t0); len(got) != 2 || got[0].SessionID != "s_exact" {
		t.Fatalf("a name match lost to a pin: %+v", got)
	}
}

type pinCall struct {
	id     string
	pinned bool
}

// pinSource records the owner-only pin call and applies it to the snapshot the
// next fetch sees.
type pinSource struct {
	*fakeSource
	pins []pinCall
}

func (s *pinSource) Call(_ context.Context, method string, args any) (json.RawMessage, error) {
	if method != "session_pin" {
		return nil, fmt.Errorf("unexpected call %q", method)
	}
	raw, _ := json.Marshal(args)
	var q struct {
		SessionID string `json:"session_id"`
		Pinned    bool   `json:"pinned"`
	}
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, err
	}
	s.pins = append(s.pins, pinCall{id: q.SessionID, pinned: q.Pinned})
	for i := range s.snap.Agents {
		if s.snap.Agents[i].SessionID != q.SessionID {
			continue
		}
		if q.Pinned {
			s.snap.Agents[i].PinnedAt = t0.Unix()
		} else {
			s.snap.Agents[i].PinnedAt = 0
		}
	}
	return json.Marshal(map[string]bool{"ok": true})
}

func TestPinKeyPinsAndUnpinsTheSelectedSession(t *testing.T) {
	base := newModel(120, 34)
	src := &pinSource{fakeSource: base.src.(*fakeSource)}
	base.src = src
	m := selectID(base, "s_0000000000000003") // web

	m = pressKey(m, "p")
	if len(src.pins) != 1 || src.pins[0].id != "s_0000000000000003" || !src.pins[0].pinned {
		t.Fatalf("pin call %+v", src.pins)
	}
	if a, ok := m.agentByID("s_0000000000000003"); !ok || a.PinnedAt == 0 {
		t.Fatalf("web is not pinned: %+v", a)
	}
	if got := names(m); indexOfID(got, "web") < 0 {
		t.Fatalf("the pinned session is not visible: %v", got)
	}
	if view := ansi.Strip(m.testView()); !strings.Contains(view, "★ web") {
		t.Fatalf("the pinned row has no pin mark:\n%s", view)
	}

	m = pressKey(m, "p")
	if len(src.pins) != 2 || src.pins[1].pinned {
		t.Fatalf("unpin call %+v", src.pins)
	}
	if a, ok := m.agentByID("s_0000000000000003"); !ok || a.PinnedAt != 0 {
		t.Fatalf("web is still pinned: %+v", a)
	}
	if strings.Contains(ansi.Strip(m.testView()), "★ web") {
		t.Fatal("the pin mark stayed after unpinning")
	}
}

func TestPinKeyIsListedAndTheKeyLineFits(t *testing.T) {
	if got := keyAction(contextSessions, "p"); got != "pin" {
		t.Fatalf("p is %q, want pin", got)
	}
	m := keys(newModel(120, 34), "?")
	help := ansi.Strip(strings.Join(m.helpContent(120), "\n"))
	if !strings.Contains(help, "pin") {
		t.Fatalf("help does not list pin:\n%s", help)
	}
	line := ansi.Strip(newModel(120, 34).helpLine(120))
	if !strings.Contains(line, "p pin") {
		t.Fatalf("key line has no pin: %q", line)
	}
	if hints := strings.Split(line, "  "); len(hints) > 8 {
		t.Fatalf("key line has %d hints: %q", len(hints), line)
	}
}

func TestMineSectionGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}, {160, 40}} {
		m, _ := mineModel(size[0], size[1])
		golden(t, fmt.Sprintf("sessions_mine_active_%dx%d", size[0], size[1]), m.testView())
		m = keys(m, "v")
		golden(t, fmt.Sprintf("sessions_mine_all_%dx%d", size[0], size[1]), m.testView())
	}
}

// An offline Mine row keeps STATUS inside its column, leaves CPU and RAM
// blank, and puts the age in TASK.
func TestMineOfflineRowKeepsItsColumns(t *testing.T) {
	m, _ := mineModel(80, 24)
	m = keys(m, "v")
	rows := m.sessionsTable(80, 24)
	header := ""
	for _, row := range rows {
		plain := ansi.Strip(row)
		if strings.Contains(plain, "NAME") && strings.Contains(plain, "STATUS") {
			header = plain
			break
		}
	}
	statusCol := strings.Index(header, "STATUS")
	taskCol := strings.Index(header, "TASK")
	if statusCol < 0 || taskCol < 0 {
		t.Fatalf("no column header: %q", header)
	}
	found := false
	for _, row := range rows {
		plain := ansi.Strip(row)
		if !strings.Contains(plain, "old-box") || strings.Contains(plain, "MACHINE") {
			continue
		}
		found = true
		runes := []rune(plain)
		if got := string(runes[statusCol : statusCol+7]); got != "offline" {
			t.Errorf("STATUS cell %q, want offline: %q", got, plain)
		}
		if got := string(runes[statusCol+7 : taskCol]); strings.TrimSpace(got) != "" {
			t.Errorf("the offline STATUS runs over CPU and RAM: %q", plain)
		}
		if got := string(runes[taskCol:]); !strings.Contains(got, "1d ago") {
			t.Errorf("TASK does not carry the age: %q", plain)
		}
	}
	if !found {
		t.Fatalf("old-session is not on screen:\n%s", ansi.Strip(strings.Join(rows, "\n")))
	}
}

// Every STATUS a Mine row can show fits the column.
func TestMineStatusLabelsFit(t *testing.T) {
	m := New(nil)
	m.now = func() time.Time { return t0 }
	cases := []struct {
		name string
		a    protocol.AgentView
		want string
	}{
		{"offline", protocol.AgentView{Online: false}, "offline"},
		{"dormant", protocol.AgentView{Online: true, Status: protocol.StatusDormant}, "dormant"},
		{"shell", protocol.AgentView{Online: true, Status: "shell"}, "shell"},
		{"busy", protocol.AgentView{Online: true, Status: "busy"}, "busy"},
		{"idle", protocol.AgentView{Online: true, Status: "idle"}, "idle"},
		{"ended", protocol.AgentView{Online: false, LastSeen: t0.Add(-25 * time.Hour).Unix()}, "offline"},
	}
	for _, tc := range cases {
		got := m.sessionStatusCell(tc.a)
		if got != tc.want {
			t.Errorf("%s: STATUS %q, want %q", tc.name, got, tc.want)
		}
		if ansi.StringWidth(got) > 7 {
			t.Errorf("%s: STATUS %q is %d columns, want 7 or less", tc.name, got, ansi.StringWidth(got))
		}
	}
}

func TestSessionsMachineColumn(t *testing.T) {
	for _, w := range []int{80, 120} {
		m, _ := mineModel(w, 34)
		m = keys(m, "v")
		view := ansi.Strip(strings.Join(m.sessionsTable(w, 40), "\n"))
		for _, want := range []string{"MACHINE", "Mac", "old-box"} {
			if !strings.Contains(view, want) {
				t.Errorf("missing %s: %s", want, view)
			}
		}
		if strings.Contains(view, "· Mac") || strings.Contains(view, "· old-box") {
			t.Fatal("machine suffix remains")
		}
		for _, row := range lines(view) {
			if ansi.StringWidth(row) > w {
				t.Fatal("row wraps")
			}
		}
	}
}

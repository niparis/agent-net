package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

// Workers hang under the session that started them (decision 0010, D6).

const (
	callerID = "s_0000000000000002" // "strategies" on the Mac
	kidA     = "s_00000000000000a1"
	kidB     = "s_00000000000000a2"
	kidC     = "s_00000000000000a3"
)

func kid(id, node, host, name string, started time.Duration) protocol.AgentView {
	return agent(id, node, host, "opencode", name, func(a *protocol.AgentView) {
		a.Mode, a.Model = protocol.ModeBackground, "anthropic/claude-haiku-4-5"
		a.Profile.Task = "probe"
		a.Worker = &protocol.WorkerInfo{ID: "w_" + id, Owner: callerID, Project: "strategies", StartedAt: t0.Add(started).Unix(), Task: name}
	})
}

// treeFixture is the worker fixture plus three workers of "strategies": two on
// its own machine and the last one, started later, on the other machine. They
// are listed out of start order on purpose.
func treeFixture() Snapshot {
	snap, _ := workerFixture()
	snap.Agents = append(snap.Agents,
		kid(kidB, "n_mac", "Mac", "backtest-b", -3*time.Minute),
		kid(kidC, "n_vm", "build-vm", "backtest-on-a-remote-machine", -2*time.Minute),
		kid(kidA, "n_mac", "Mac", "backtest-a", -4*time.Minute))
	return snap
}

func treeModel(w, h int, snap Snapshot) Model {
	src := &fakeSource{snap: snap, events: fakeEvents()}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	return settle(refresh(m, src))
}

func ids(m Model) []string {
	var out []string
	for _, a := range m.visible() {
		out = append(out, a.SessionID)
	}
	return out
}

func indexOfID(l []string, id string) int {
	for i, x := range l {
		if x == id {
			return i
		}
	}
	return -1
}

func TestTreeOrder(t *testing.T) {
	m := treeModel(120, 34, treeFixture())
	g := m.groups()
	// The Mac group: strategies and its three workers right after it, by start
	// time (the one on the other machine too), then the rest as before.
	var mac []string
	for _, a := range g[0].agents {
		mac = append(mac, a.Profile.Name)
	}
	want := []string{"strategies", "backtest-a", "backtest-b", "backtest-on-a-remote-machine", "tenebrae", "tenebrae-53", "web"}
	if strings.Join(mac, ",") != strings.Join(want, ",") {
		t.Fatalf("mac group %v, want %v", mac, want)
	}
	marks := g[0].marks
	if marks[kidA].prefix != "├─ " || marks[kidB].prefix != "├─ " || marks[kidC].prefix != "└─ " {
		t.Errorf("marks %+v", marks)
	}
	if marks[kidA].host != "" || marks[kidC].host != "build-vm" {
		t.Errorf("hosts %+v", marks)
	}
	if marks[workerID].prefix != "↳ " {
		t.Errorf("owner-started worker: %+v", marks[workerID])
	}
	// Its own machine keeps the other sessions and no worker of the caller.
	for _, a := range g[1].agents {
		if a.Worker != nil {
			t.Errorf("build-vm group holds %s", a.Profile.Name)
		}
	}
	if len(g) != 2 {
		t.Errorf("%d groups", len(g))
	}
}

func TestTreeGolden(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		name := "tree_" + itoa(size.w) + "x" + itoa(size.h)
		m := treeModel(size.w, size.h, treeFixture())
		golden(t, name, selectID(m, kidB).View())
		// The caller hides: the filter matches the workers only.
		f := m
		f.filter = "probe"
		f.ensureSelection()
		f.fixScroll()
		golden(t, name+"_hidden_caller", f.View())
	}
}

func TestTreeNested(t *testing.T) {
	snap := treeFixture()
	// A worker of a worker, and a loop of two that must not vanish.
	deep := kid("s_00000000000000b1", "n_mac", "Mac", "deep", -1*time.Minute)
	deep.Worker.Owner = kidA
	snap.Agents = append(snap.Agents, deep)
	m := treeModel(120, 34, snap)
	g := m.groups()[0]
	if p := g.marks[deep.SessionID].prefix; p != "│  └─ " {
		t.Errorf("nested prefix %q", p)
	}
	loopA, loopB := kid("s_00000000000000c1", "n_mac", "Mac", "loop-a", 0), kid("s_00000000000000c2", "n_mac", "Mac", "loop-b", 0)
	loopA.Worker.Owner, loopB.Worker.Owner = loopB.SessionID, loopA.SessionID
	snap.Agents = append(snap.Agents, loopA, loopB)
	m = treeModel(120, 34, snap)
	if got := len(m.visible()); got != len(snap.Agents)-2 { // no row for the owner or the offline session
		t.Errorf("%d rows for %d sessions", got, len(snap.Agents)-2)
	}
}

func TestTreeSelectionInDisplayOrder(t *testing.T) {
	m := treeModel(120, 34, treeFixture())
	order := ids(m)
	at := indexOfID(order, callerID)
	m = selectID(m, callerID)
	for i := 1; i <= 4; i++ {
		m = keys(m, "down")
		if m.selected != order[at+i] {
			t.Fatalf("down %d selected %s, order %v", i, m.selected, order)
		}
	}
	// The caller's neighbours are its workers, in start order.
	if order[at+1] != kidA || order[at+3] != kidC {
		t.Errorf("order %v", order)
	}
	// The cursor stays on the same session when rows move: kidC now starts
	// first, so it moves up under its caller.
	m = selectID(m, kidB)
	snap := treeFixture()
	for i := range snap.Agents {
		if snap.Agents[i].SessionID == kidC {
			snap.Agents[i].Worker.StartedAt = t0.Add(-10 * time.Minute).Unix()
		}
	}
	m = refresh(m, &fakeSource{snap: snap, events: fakeEvents()})
	if m.selected != kidB {
		t.Errorf("selected %s", m.selected)
	}
	if o := ids(m); o[indexOfID(o, callerID)+1] != kidC {
		t.Errorf("order %v", o)
	}
	// Keys act on the worker, and c opens its conversation.
	m = keys(m, "c")
	if m.conv == nil || m.conv.id != kidB {
		t.Errorf("conv %+v", m.conv)
	}
}

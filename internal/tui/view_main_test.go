package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/niparis/agent-net/internal/protocol"
)

// headerModel is the fake screen with an owner-actionable header: three busy
// sessions, one session waiting, four online workers and one queued start on
// the build VM.
func headerModel(w, h int) Model {
	snap := fakeSnapshot()
	for i := range snap.Agents {
		switch snap.Agents[i].SessionID {
		case "s_0000000000000001", "s_0000000000000002", "s_0000000000000003":
			snap.Agents[i].Status = "busy"
		case "s_0000000000000004":
			waiting(&snap.Agents[i], -3*time.Minute, protocol.WaitingPermission, "Bash(git push origin main)")
		}
	}
	for i := 0; i < 4; i++ {
		snap.Agents = append(snap.Agents, agent(
			[]string{"s_0000000000000101", "s_0000000000000102", "s_0000000000000103", "s_0000000000000104"}[i],
			"n_mac", "Mac", "opencode", "worker", func(a *protocol.AgentView) {
				a.Worker = &protocol.WorkerInfo{Owner: "s_0000000000000002"}
			}))
	}
	u := fakeUsage()
	u.Gateway.Nodes[0].Usage.Workers = &protocol.WorkerLoad{Running: 0, Queued: 1, Room: protocol.RoomCPU}
	src := &fakeSource{snap: snap, events: fakeEvents(), usage: u, counts: fakeCounts(), tops: fakeTops()}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	return settle(refresh(m, src))
}

func TestHeaderCountsOnlineBusyNeedsAndWorkers(t *testing.T) {
	head := lines(headerModel(160, 30).View())[0]
	want := "12 online · 3 busy · 1 needs you · workers 4 running, 1 queued"
	if !strings.Contains(head, want) {
		t.Fatalf("header %q lacks %q", head, want)
	}
	if strings.Contains(head, "offline") {
		t.Fatalf("the offline count is still in the header: %q", head)
	}
}

func TestHeaderHidesNeedsYouAtZero(t *testing.T) {
	head := lines(newModel(160, 30).View())[0]
	if strings.Contains(head, "needs you") {
		t.Fatalf("header shows needs you with nobody waiting: %q", head)
	}
	if !strings.Contains(head, "12 online · 1 busy") {
		t.Fatalf("header lacks the busy count: %q", head)
	}
}

func TestHeaderNeedsYouUsesTheWarningColour(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)
	head := lines(headerModel(160, 30).View())[0]
	if !strings.Contains(head, sNeeds.Render("1 needs you")) {
		t.Fatalf("needs you is not in the warning colour: %q", head)
	}
}

// A machine's report reaches the screen twice: live from the local daemon and
// again in the gateway's copy. The queue must count it once.
func TestHeaderQueuedWorkersCountsEachMachineOnce(t *testing.T) {
	u := fakeUsage()
	u.Local.Workers = &protocol.WorkerLoad{Queued: 2, Room: protocol.RoomOK}
	u.Gateway.Nodes = append(u.Gateway.Nodes, protocol.UsageNode{
		NodeID: "n_mac", Hostname: "Mac", AgeSeconds: 2,
		Usage: protocol.UsageReport{Workers: &protocol.WorkerLoad{Queued: 2, Room: protocol.RoomOK}},
	})
	u.Gateway.Nodes[0].Usage.Workers = &protocol.WorkerLoad{Queued: 1, Room: protocol.RoomCPU}
	head := lines(modelWith(fakeSnapshot(), u, 160, 30).View())[0]
	if !strings.Contains(head, "workers 0 running, 3 queued") {
		t.Fatalf("header %q lacks the queue of both machines", head)
	}
}

func TestMachineCPUAt100Fits(t *testing.T) {
	for _, w := range []int{50, 60, 70, 80, 100, 120} {
		u := fakeUsage()
		u.Local.Machine.CPU, u.Local.Machine.CPUAvg = 100, 100
		m := modelWith(fakeSnapshot(), u, w, 24)
		var line string
		for _, l := range lines(m.View()) {
			if strings.Contains(l, "cpu ") {
				line = l
				break
			}
		}
		if line == "" {
			t.Fatalf("%d columns: no machine line:\n%s", w, m.View())
		}
		if strings.Contains(line, "10…") {
			t.Fatalf("%d columns: the CPU cell cuts 100%%: %q", w, line)
		}
		if !strings.Contains(line, "100% of 10") {
			t.Fatalf("%d columns: the CPU cell lacks capacity: %q", w, line)
		}
	}
}

func TestMachineRowsShowAgentNetVersions(t *testing.T) {
	snap := fakeSnapshot()
	snap.Nodes[0].Version = "v0.4.30"
	snap.Nodes[1].Version = "v0.4.29"
	snap.Nodes[2].Version = "v0.4.1"
	m := modelWith(snap, fakeUsage(), 120, 34)
	rows := m.machineRows()
	for _, tc := range []struct{ node, name, version string }{
		{"n_mac", "Mac (here)", "v0.4.30"},
		{"n_vm", "build-vm", "v0.4.29"},
		{"n_old", "old-box", "v0.4.1"},
	} {
		var row string
		for _, r := range rows {
			if r.nodeID == tc.node {
				row = ansi.Strip(m.machineLine(r, 116, machineNameWidth(rows, m.snap.NodeID)))
				break
			}
		}
		nameAt, versionAt, cpuAt := strings.Index(row, tc.name), strings.Index(row, tc.version), strings.Index(row, "cpu ")
		if row == "" || nameAt < 0 || versionAt <= nameAt || cpuAt <= versionAt {
			t.Errorf("%s row does not show %s after its name and before CPU: %q", tc.node, tc.version, row)
		}
	}
}

func TestMachineVersionColorsCompareOnlineNodes(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	snap := fakeSnapshot()
	snap.Nodes[0].Version = "v0.4.30"
	snap.Nodes[1].Version = "v0.4.29"
	snap.Nodes[2].Version = "v99.0.0"
	m := modelWith(snap, fakeUsage(), 120, 34)
	rows := m.machineRows()
	for _, tc := range []struct {
		node, version string
		style         lipgloss.Style
	}{{"n_mac", "v0.4.30", sDim}, {"n_vm", "v0.4.29", sNeeds}, {"n_old", "v99.0.0", sDim}} {
		for _, r := range rows {
			if r.nodeID != tc.node {
				continue
			}
			line := m.machineLine(r, 116, machineNameWidth(rows, m.snap.NodeID))
			if !strings.Contains(line, tc.style.Render(cell(tc.version, 7))) {
				t.Errorf("%s version lacks expected color: %q", tc.node, line)
			}
		}
	}
}

func TestMachineVersionFitsAt80Columns(t *testing.T) {
	snap := fakeSnapshot()
	snap.Nodes[0].Version = "v0.4.30"
	m := modelWith(snap, fakeUsage(), 80, 24)
	rows := m.machineRows()
	for _, r := range rows {
		if r.nodeID != "n_mac" {
			continue
		}
		line := ansi.Strip(m.machineLine(r, 76, machineNameWidth(rows, m.snap.NodeID)))
		if !strings.Contains(line, "v0.4.30") {
			t.Fatalf("80-column machine row lost its version: %q", line)
		}
		return
	}
	t.Fatal("local machine missing")
}

func TestOnlineMachinesShowOnlyMissingHarnessNotes(t *testing.T) {
	m := newModel(160, 24)
	for _, r := range m.machineRows() {
		if r.nodeID == "n_vm" {
			line := ansi.Strip(m.machineLine(r, 156, machineNameWidth(m.machineRows(), m.snap.NodeID)))
			if !strings.Contains(line, "no claude") || !strings.Contains(line, "no opencode") {
				t.Fatalf("missing harness notes: %s", line)
			}
			if strings.Contains(line, "pi") {
				t.Fatalf("installed harness was listed: %s", line)
			}
			return
		}
	}
	t.Fatal("machine missing")
}

func TestMachineRowsShowCapacityDiskWorkersAndTop(t *testing.T) {
	snap := fakeSnapshot()
	var workerTrees []protocol.TreeUsage
	for i, cpu := range []float64{5, 8, 12, 106, 9} {
		id := fmt.Sprintf("s_worker_%02d", i)
		snap.Agents = append(snap.Agents, agent(id, "n_mac", "Mac", "codex", fmt.Sprintf("a2a-ocs worker %d", i), func(a *protocol.AgentView) {
			a.Worker = &protocol.WorkerInfo{ID: fmt.Sprintf("worker-%d", i), Project: "a2a-ocs"}
		}))
		workerTrees = append(workerTrees, tree(6000+i, "codex", cpu, cpu, gib*21/50, 4, id))
	}
	u := fakeUsage()
	u.Local.Machine = protocol.MachineUsage{Cores: 16, CPU: 72, MemUsed: 24 * gib, MemTotal: 32 * gib, DiskFree: 31 * gib, Samples: 60}
	u.Local.Workers = &protocol.WorkerLoad{Running: 5, Room: protocol.RoomOK}
	u.Local.Trees = append(workerTrees,
		tree(7000, "claude", 20, 20, gib, 4, "s_0000000000000001"),
		tree(7001, "claude", 30, 30, gib, 5, "s_0000000000000002"),
	)
	m := modelWith(snap, u, 160, 34)
	var local string
	for _, r := range m.machineRows() {
		if r.nodeID == "n_mac" {
			local = ansi.Strip(m.machineLine(r, 156, machineNameWidth(m.machineRows(), m.snap.NodeID)))
			break
		}
	}
	for _, want := range []string{"cpu ", "72% of 16", "mem ", "24/32G", "disk 31G free", "5 workers · 2.1G", "top: a2a-ocs worker 3 106%"} {
		if !strings.Contains(local, want) {
			t.Errorf("machine row lacks %q: %s", want, local)
		}
	}
	for _, unwanted := range []string{"opencode service", "procs", "claude  opencode", "codex  "} {
		if strings.Contains(local, unwanted) {
			t.Errorf("machine row still shows %q: %s", unwanted, local)
		}
	}
}

func TestMachineRowsHaveNoLoneDashCells(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := newModel(size[0], size[1])
		for _, line := range lines(m.View()) {
			plain := strings.TrimSpace(ansi.Strip(line))
			if !strings.Contains(plain, "cpu ") {
				continue
			}
			plain = strings.TrimSpace(strings.Trim(plain, "│"))
			for _, cell := range strings.Fields(plain) {
				if cell == "-" {
					t.Errorf("%dx%d Machines row has a lone dash cell: %s", size[0], size[1], plain)
				}
			}
		}
	}
}

func TestMachineColumnsAlignAndDropInOrderAtNarrowWidths(t *testing.T) {
	snap := fakeSnapshot()
	snap.Nodes[1].Hostname = "東京"
	m := modelWith(snap, fakeUsage(), 160, 34)
	var linesByNode = map[string]string{}
	for _, r := range m.machineRows() {
		linesByNode[r.nodeID] = ansi.Strip(m.machineLine(r, 156, machineNameWidth(m.machineRows(), m.snap.NodeID)))
	}
	for _, field := range []string{"v0.4.0", "cpu ", "mem ", "disk ", "top:"} {
		start := func(line string) int {
			i := strings.Index(line, field)
			if i < 0 {
				return -1
			}
			return ansi.StringWidth(line[:i])
		}
		mac, vm := start(linesByNode["n_mac"]), start(linesByNode["n_vm"])
		if mac < 0 || mac != vm {
			t.Errorf("%q column starts differ: Mac %d, build-vm %d\n%s\n%s", field, mac, vm, linesByNode["n_mac"], linesByNode["n_vm"])
		}
	}
	for _, tc := range []struct {
		width        int
		want, absent []string
	}{
		{width: 110, want: []string{"disk "}, absent: []string{"top:", "workers"}},
		{width: 60, want: []string{"cpu ", "mem "}, absent: []string{"disk ", "workers", "top:"}},
	} {
		line := ansi.Strip(m.machineLine(m.machineRows()[0], tc.width, machineNameWidth(m.machineRows(), m.snap.NodeID)))
		for _, s := range tc.want {
			if !strings.Contains(line, s) {
				t.Errorf("width %d lacks %q: %s", tc.width, s, line)
			}
		}
		for _, s := range tc.absent {
			if strings.Contains(line, s) {
				t.Errorf("width %d retains %q: %s", tc.width, s, line)
			}
		}
	}
}

func TestMachineOptionalColumnsDropTopThenWorkersThenDisk(t *testing.T) {
	rows := []machineRow{{nodeID: "n", version: "v0.4.30"}}
	figures := map[string]machineFigures{"n": {
		cpu: "cpu ████████ 100% of 16", mem: "mem ████ 32/32G", disk: "disk 31G free",
		workers: "5 workers · 2.1G", top: "top: a2a-ocs worker 4 106%",
	}}
	for _, tc := range []struct {
		width              int
		top, workers, disk bool
	}{{98, false, true, true}, {80, false, false, true}, {65, false, false, false}} {
		got := machineRowWidths(rows, figures, tc.width, 8)
		if got.showTop != tc.top || got.showWorkers != tc.workers || got.showDisk != tc.disk {
			t.Errorf("width %d: top/workers/disk = %v/%v/%v, want %v/%v/%v", tc.width, got.showTop, got.showWorkers, got.showDisk, tc.top, tc.workers, tc.disk)
		}
	}
}

func TestMachineHarnessNoteShowsOnlyMissingOrOutdatedSharedHarnesses(t *testing.T) {
	snap := fakeSnapshot()
	snap.Nodes[0].Harnesses = append(snap.Nodes[0].Harnesses, protocol.HarnessInfo{Name: "codex", Version: "2.1.0"})
	snap.Nodes[1].Harnesses = append(snap.Nodes[1].Harnesses, protocol.HarnessInfo{Name: "codex", Version: "1.9.0"})
	snap.Nodes[2].Harnesses = []protocol.HarnessInfo{}
	m := modelWith(snap, fakeUsage(), 180, 34)
	rows := m.machineRows()
	for _, tc := range []struct{ node, want string }{{"n_vm", "old codex"}, {"n_old", "no codex"}} {
		for _, r := range rows {
			if r.nodeID == tc.node {
				line := ansi.Strip(m.machineLine(r, 176, machineNameWidth(rows, m.snap.NodeID)))
				if !strings.Contains(line, tc.want) {
					t.Errorf("%s row lacks %q: %s", tc.node, tc.want, line)
				}
				break
			}
		}
	}
	for _, r := range rows {
		if r.nodeID == "n_mac" && strings.Contains(ansi.Strip(m.machineLine(r, 176, machineNameWidth(rows, m.snap.NodeID))), "codex") {
			t.Fatal("current harness version should not get a note")
		}
	}
}

func TestMachineDiskFreeUsesLowAndWarningColors(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)
	m := newModel(160, 34)
	for _, tc := range []struct {
		free uint64
		want lipgloss.Style
	}{{9 << 30, sBad}, {15 << 30, sNeeds}} {
		f := machineFigures{disk: "disk free", diskFree: tc.free}
		if got, want := m.machineDiskCell(f), tc.want.Render(f.disk); got != want {
			t.Errorf("%d GiB free: got %q, want %q", tc.free>>30, got, want)
		}
	}
	if got := m.machineDiskCell(machineFigures{disk: "disk free", diskFree: 20 << 30}); got != "disk free" {
		t.Errorf("20 GiB free should be uncolored: %q", got)
	}
}

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/protocol"
)

// newTwoMachineChoice is the startsFixture with project "shop" on both
// machines, both accepting workers with the same harnesses, and a worker
// profile the picker can choose on either. The owner session a lives on nodeA.
func newTwoMachineChoice(t *testing.T) *startsFixture {
	t.Helper()
	f := newStartsFixture(t)
	f.g.opts.TestNoBriefChecks = true
	launch := true
	harnesses := []protocol.HarnessInfo{
		{Name: "claude", Models: []string{"claude-opus-5-5"}},
		{Name: "opencode", Models: []string{"deepseek/deepseek-v4.1-flash"}},
	}
	for _, node := range []string{f.nodeA, f.nodeB} {
		if err := f.s.SetNodeHello(node, f.s.hostname(node), "1"); err != nil {
			t.Fatal(err)
		}
		if err := f.s.SetNodeLaunch(node, &launch); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.SetNodeHarnesses(node, harnesses); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.db.Exec(`INSERT INTO projects(node_id,path,name,added_at,checked_at) VALUES(?,?,?,0,0)`, node, "/repo/shop", "shop"); err != nil {
			t.Fatal(err)
		}
		client, received := connectTestNode(t, f.g, node)
		serveLaunches(t, client, received)
	}
	if err := f.s.saveWorkerProfiles([]protocol.WorkerProfile{
		{Name: "opus", Harness: "claude", Model: "claude-opus-5-5", Effort: "high"},
		{Name: "deepseek", Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash", Effort: "high"},
	}); err != nil {
		t.Fatal(err)
	}
	scoresJSON := `[{"match":"claude-opus-5-5","score":60},{"match":"deepseek/deepseek-v4.1-flash","score":50}]`
	if _, err := f.s.db.Exec(`INSERT INTO setting_values(key,level,scope,item,value,revision) VALUES (?,'network','network','',?,0) ON CONFLICT (key,level,scope,item) DO UPDATE SET value=excluded.value`, scoresKey, scoresJSON); err != nil {
		t.Fatal(err)
	}
	return f
}

// serveLaunches answers the gateway's worker.launch for a node with a running
// worker, so a start completes without a harness process.
func serveLaunches(t *testing.T, client *websocket.Conn, received <-chan []byte) {
	t.Helper()
	ctx := t.Context()
	go func() {
		for n := 1; ; n++ {
			select {
			case <-ctx.Done():
				return
			case raw := <-received:
				var h protocol.Header
				if json.Unmarshal(raw, &h) != nil || h.ID == "" || h.Type != "worker.launch" {
					continue
				}
				res, err := protocol.EncodeResult(h.ID, protocol.WorkerLaunched{SessionID: fmt.Sprintf("s_room_%d", n)}, nil)
				if err == nil {
					_ = client.Write(ctx, websocket.MessageText, res)
				}
			}
		}
	}()
}

// reportRoom records a fresh load report for a machine.
func reportRoom(t *testing.T, f *startsFixture, node string, load protocol.WorkerLoad) {
	t.Helper()
	f.g.usage.set(node, f.clock.now(), protocol.UsageReport{Workers: &load})
}

// An automatic start names no harness. It goes to the machine with the most
// room, and the note says that is why (decision 0018).
func TestAutomaticStartPicksTheRoomiestMachine(t *testing.T) {
	f := newTwoMachineChoice(t)
	reportRoom(t, f, f.nodeA, protocol.WorkerLoad{Running: 4, Room: protocol.RoomOK, Headroom: 0.08})
	reportRoom(t, f, f.nodeB, protocol.WorkerLoad{Running: 0, Room: protocol.RoomOK, Headroom: 0.62})

	ans, err := f.g.workerStart(context.Background(), &nodeConn{id: f.nodeA}, protocol.WorkerStartFrame{Owner: f.a, Level: "normal",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "shop", Task: "test", Model: "auto"}})
	if err != nil {
		t.Fatalf("workerStart: %v", err)
	}
	if ans.NodeID != f.nodeB {
		t.Fatalf("automatic start landed on %s, want the machine with more room (note %q)", ans.Hostname, ans.MachineNote)
	}
	if !strings.Contains(ans.MachineNote, "picked h2") || !strings.Contains(ans.MachineNote, "most room for a worker") {
		t.Fatalf("machine_note %q does not say the roomiest machine won", ans.MachineNote)
	}
}

// A start that names a harness keeps the most-room rule (decision 0018).
func TestNamedHarnessStartPicksTheRoomiestMachine(t *testing.T) {
	f := newTwoMachineChoice(t)
	reportRoom(t, f, f.nodeA, protocol.WorkerLoad{Running: 4, Room: protocol.RoomOK, Headroom: 0.08})
	reportRoom(t, f, f.nodeB, protocol.WorkerLoad{Running: 0, Room: protocol.RoomOK, Headroom: 0.62})

	ans, err := f.g.workerStart(context.Background(), &nodeConn{id: f.nodeA}, protocol.WorkerStartFrame{Owner: f.a, Level: "normal",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "shop", Task: "test", Harness: "claude", Model: "auto"}})
	if err != nil {
		t.Fatalf("workerStart: %v", err)
	}
	if ans.NodeID != f.nodeB {
		t.Fatalf("named harness start landed on %s, want the machine with more room (note %q)", ans.Hostname, ans.MachineNote)
	}
	if !strings.Contains(ans.MachineNote, "picked h2") || !strings.Contains(ans.MachineNote, "most room for a worker") {
		t.Fatalf("machine_note %q does not say the roomiest machine won", ans.MachineNote)
	}
}

// The fallback note may say the load reports are incomplete only when one is
// really missing or stale. With complete reports it must not claim they are.
func TestMachineChoiceNoteNamesMissingReportsOnly(t *testing.T) {
	f := newTwoMachineChoice(t)
	stale := f.clock.now().Add(-time.Duration(protocol.UsageStaleSeconds+1) * time.Second)
	f.g.usage.set(f.nodeB, stale, protocol.UsageReport{Workers: &protocol.WorkerLoad{Room: protocol.RoomOK, Headroom: 0.62}})
	reportRoom(t, f, f.nodeA, protocol.WorkerLoad{Room: protocol.RoomOK, Headroom: 0.08})

	ans, err := f.g.workerStart(context.Background(), &nodeConn{id: f.nodeA}, protocol.WorkerStartFrame{Owner: f.a, Level: "normal",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "shop", Task: "test", Model: "auto"}})
	if err != nil {
		t.Fatalf("workerStart with a stale report: %v", err)
	}
	if !strings.Contains(ans.MachineNote, "without complete load reports") {
		t.Fatalf("machine_note %q does not say a load report is missing", ans.MachineNote)
	}

	// Fresh reports again, but only nodeA can run the named harness, so there
	// is no room rule to apply and no report is missing.
	reportRoom(t, f, f.nodeB, protocol.WorkerLoad{Room: protocol.RoomOK, Headroom: 0.62})
	if _, err := f.s.SetNodeHarnesses(f.nodeB, []protocol.HarnessInfo{{Name: "opencode", Models: []string{"deepseek/deepseek-v4.1-flash"}}}); err != nil {
		t.Fatal(err)
	}
	ans, err = f.g.workerStart(context.Background(), &nodeConn{id: f.nodeA}, protocol.WorkerStartFrame{Owner: f.a, Level: "normal",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "shop", Task: "test", Harness: "claude", Model: "auto"}})
	if err != nil {
		t.Fatalf("workerStart with one capable machine: %v", err)
	}
	if strings.Contains(ans.MachineNote, "without complete load reports") {
		t.Fatalf("machine_note %q claims a report is missing, but both machines reported", ans.MachineNote)
	}
}

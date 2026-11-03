package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func workerTaskID(t *testing.T, row workerRow) string {
	t.Helper()
	f := reflect.ValueOf(row).FieldByName("TaskID")
	if !f.IsValid() || f.Kind() != reflect.String {
		t.Fatal("worker row has no task ID")
	}
	return f.String()
}

func TestWorkerContinueLinksTaskAndCountsRework(t *testing.T) {
	for _, tc := range []struct {
		name, task string
		partial    bool
	}{
		{"stopped", "FAKE:ignore build it", false},
		{"partial", "FAKE:handback FAKE:partial build it", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			w := startWorkerNode(t, g, shortSet, nil)
			repo := newRepo(t, "shop")
			w.register(t, repo)
			old := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: tc.task, Isolation: protocol.IsolationNone, AutoContinue: autoContinueOff()})
			if tc.partial {
				w.workerStopped(t, g, protocol.StopHandback)
			} else {
				w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: old.SessionID})
				w.workerStopped(t, g, protocol.StopRequested)
			}
			first, err := w.startDefault(protocol.WorkerStartRequest{Continue: old.SessionID, Task: "FAKE:ignore"})
			if err != nil {
				t.Fatal(err)
			}
			row := reworkRow(t, w, first.SessionID)
			if got := workerTaskID(t, row); got != old.SessionID {
				t.Fatalf("continued root %q, want %q", got, old.SessionID)
			}
			wantCont, wantFix := 0, 1
			if tc.partial {
				wantCont, wantFix = 1, 0
			}
			if row.ContinueRun != wantCont || row.FixRun != wantFix {
				t.Fatalf("continuation rework: %+v", row)
			}
			w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: first.SessionID})
			eventually(t, "continued worker ends", func() bool { r := reworkRow(t, w, first.SessionID); return r.EndedReason != "" })
			second, err := w.startDefault(protocol.WorkerStartRequest{Continue: first.SessionID, Task: "FAKE:ignore"})
			if err != nil {
				t.Fatal(err)
			}
			if got := workerTaskID(t, reworkRow(t, w, second.SessionID)); got != old.SessionID {
				t.Fatalf("chain root %q, want %q", got, old.SessionID)
			}
		})
	}
}

func TestWorkerMidrunFollowUpsReachGateway(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "FAKE:ignore build it", Isolation: protocol.IsolationNone, AutoContinue: autoContinueOff()})
	eventually(t, "brief recorded", func() bool { return reworkRow(t, w, res.SessionID).TaskMsg != "" })
	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "FAKE:ignore clarification"}), &sent)
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "FAKE:handback finish"}), &sent)
	w.workerStopped(t, g, protocol.StopHandback)
	eventually(t, "midrun count reaches gateway", func() bool {
		q := g.gw.Store().QualityAnalysis()
		b, _ := json.Marshal(q.Levels)
		var levels []struct {
			Families []struct {
				Midrun int `json:"midrun"`
			} `json:"families"`
		}
		_ = json.Unmarshal(b, &levels)
		for _, l := range levels {
			for _, f := range l.Families {
				if f.Midrun == 2 {
					return true
				}
			}
		}
		return false
	})
}

// A daemon upgraded after the gateway backfilled historical links has an old
// local row without a task ID. The gateway's stored root must win in that case.
func TestWorkerContinueUsesBackfilledGatewayRoot(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	old := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "FAKE:ignore old continuation", Isolation: protocol.IsolationNone})
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: old.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	if err := g.gw.Store().RecordWorkerRun(protocol.WorkerRun{NodeID: "node", SessionID: old.SessionID, TaskID: "s_original_root", Harness: "opencode", Outcome: protocol.OutcomeStopped}); err != nil {
		t.Fatal(err)
	}
	next, err := w.startDefault(protocol.WorkerStartRequest{Continue: old.SessionID, Task: "FAKE:ignore"})
	if err != nil {
		t.Fatal(err)
	}
	if got := workerTaskID(t, reworkRow(t, w, next.SessionID)); got != "s_original_root" {
		t.Fatalf("continued backfilled root = %q", got)
	}
}

func TestWorkerMidrunAfterNoticeSurvivesRestart(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "FAKE:handback first", Isolation: protocol.IsolationNone})
	w.workerStopped(t, g, protocol.StopHandback)
	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "FAKE:ignore first follow-up"}), &sent)
	eventually(t, "first request after notice", func() bool { r := reworkRow(t, w, res.SessionID); return r.Fix == 1 && r.MidrunCount == 0 })
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "FAKE:ignore second follow-up"}), &sent)
	eventually(t, "second request after notice", func() bool { r := reworkRow(t, w, res.SessionID); return r.Fix == 2 && r.MidrunCount == 1 })
	// Arrival updates the counters before delivery is recorded. Let the
	// follow-up finish so restart does not replay an in-flight request.
	eventually(t, "second follow-up delivered", func() bool {
		return w.d.st.isDelivered(sent.Message.ID)
	})
	w.d.Stop()
	w.node.restart()
	w.cli = cliClient(t, w.node)
	row := reworkRow(t, w, res.SessionID)
	if row.MidrunCount != 1 || row.MidrunRun != 1 || row.NoticeSinceRequest {
		t.Fatalf("restart lost mid-run state: %+v", row)
	}
}

func TestWorkerMidrunCountsQueuedRequestOnArrival(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "FAKE:wait-handback", Isolation: protocol.IsolationNone})
	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "FAKE:queued-handback"}), &sent)
	eventually(t, "queued request delivered", func() bool { return w.d.st.isDelivered(sent.Message.ID) })
	if row := reworkRow(t, w, res.SessionID); row.MidrunCount != 1 {
		t.Fatalf("queued request did not count on arrival: %+v", row)
	}
	if err := os.WriteFile(filepath.Join(repo, "fake-opencode-finish"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	w.workerStopped(t, g, protocol.StopHandback)
	if row := reworkRow(t, w, res.SessionID); row.MidrunCount != 1 {
		t.Fatalf("queued request counted twice: %+v", row)
	}
}

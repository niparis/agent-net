package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerModelFailureDetailPersists(t *testing.T) {
	st, err := openState(seededStateDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	var row workerRow
	if err := json.Unmarshal([]byte(`{"ID":"w_test","SessionID":"s_test","Failure":"Request failed: 401"}`), &row); err != nil {
		t.Fatal(err)
	}
	if err := st.saveWorker(row); err != nil {
		t.Fatal(err)
	}
	rows, err := st.workers()
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %v: %v", rows, err)
	}
	single, ok, err := st.workerBySession("s_test")
	if err != nil || !ok {
		t.Fatalf("single %v: %v", ok, err)
	}
	for _, got := range []workerRow{rows[0], single} {
		b, _ := json.Marshal(got)
		var fields map[string]any
		json.Unmarshal(b, &fields)
		if fields["Failure"] != "Request failed: 401" {
			t.Fatalf("failure lost: %s", b)
		}
	}
}

func TestWorkerProfileNameStartAndModelNote(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	if err := g.gw.Store().AddWorkerProfile(protocol.WorkerProfile{Name: "Test Profile", Harness: "opencode", Model: "fake/m1", Effort: "medium"}); err != nil {
		t.Fatal(err)
	}
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Model: "tEST pROFILE", Task: "FAKE:ignore test"})
	if res.Harness != "opencode" || res.Model != "fake/m1:medium" || !strings.Contains(res.ModelNote, "Test Profile") || !strings.Contains(res.ModelNote, "selected opencode") {
		t.Fatalf("start: %+v", res)
	}
}

func TestWorkerModelAuthFailureReport(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNodeWithDetect(t, g, shortSet, inventoryWithModels("opencode", []string{"openai/gpt-6.1-sol"}), nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "openai/gpt-6.1-sol", Task: "FAKE:ignore test"})
	var worker *worker
	w.d.wmu.Lock()
	for _, running := range w.d.workers {
		if running.sessionID == res.SessionID {
			worker = running
			worker.failure = "Request failed: 401"
			break
		}
	}
	w.d.wmu.Unlock()
	if worker == nil {
		t.Fatal("worker missing")
	}
	w.d.endWorker(worker, protocol.StopModelFails)
	w.workerStopped(t, g, protocol.StopModelFails)
	blocked, err := g.gw.Store().ModelAuthBlocked(w.d.NodeID(), "opencode", "openai/gpt-6-luna")
	if err != nil || !blocked {
		t.Fatalf("auth failure not reported: %v, %v", blocked, err)
	}
}

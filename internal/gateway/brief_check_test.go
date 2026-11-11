package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// a complete brief for the overlap tests: the path is the one a worker or a
// change can share.
func gatewayBrief(path string) string {
	return "Goal: The owner asked: \"fix the parser crash\" (message m_86b8d8cd77f796f0).\n" +
		"Files or areas: " + path + " and docs/reference.md.\n" +
		"Limits: tests use temp dirs and fakes only, never the real setup.\n" +
		"Definition of done: the new test fails first and passes after the fix.\n" +
		"How you are graded: the caller reruns the test and reads the diff.\n" +
		"Evidence:\n- the owner's words\n- the failing test\n- the diff\n"
}

func briefFrame(task string) protocol.WorkerStartFrame {
	return protocol.WorkerStartFrame{Owner: "s_caller", WorkerStartRequest: protocol.WorkerStartRequest{Project: "shop", Task: task, Harness: "opencode", Model: "fake/m1"}}
}

func TestWorkerBriefPassesACompleteBrief(t *testing.T) {
	s, _ := newTestStore(t)
	failures, err := s.checkWorkerBrief(briefFrame(gatewayBrief("internal/gateway/brief.go")), "shop", "normal")
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Fatalf("complete brief failed: %v", failures)
	}
}

func TestWorkerBriefOverlapsRunningWorker(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	worker, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "w1", Worker: &protocol.WorkerInfo{
		ID: "w_1", Owner: "s_owner", Project: "shop", Task: "Fix internal/gateway/brief.go and its tests."}})
	if err != nil {
		t.Fatal(err)
	}
	failures, err := s.checkWorkerBrief(briefFrame(gatewayBrief("internal/gateway/brief.go")), "shop", "normal")
	if err != nil {
		t.Fatal(err)
	}
	want := "overlaps running worker " + worker + " on internal/gateway/brief.go"
	if !strings.Contains(strings.Join(failures, "\n"), want) {
		t.Fatalf("failures %q do not say %q", failures, want)
	}

	// The same brief passes when it states the overlap and why.
	ok := gatewayBrief("internal/gateway/brief.go")
	ok = strings.Replace(ok, "Evidence:", "Overlap ok: the other worker is only reading the file.\nEvidence:", 1)
	failures, err = s.checkWorkerBrief(briefFrame(ok), "shop", "normal")
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Fatalf("stated overlap failed: %v", failures)
	}
}

func TestWorkerBriefOverlapsOpenChange(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.db.Exec(`INSERT INTO delivery_changes
		(id,project,machine,branch,target_branch,base_commit,head_commit,uncommitted,revision,origin,caller,title,difficulty,state,merged_commit,created_at,updated_at)
		VALUES('ch_open','shop','','agent-net/x','','','` + strings.Repeat("a", 40) + `',0,1,'{}','s_caller','parser','','open','',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO delivery_revision_stats(change_id,revision,diff_stat) VALUES('ch_open',1,?)`,
		" internal/gateway/brief.go | 2 +-\n 1 file changed, 1 insertion(+), 1 deletion(-)"); err != nil {
		t.Fatal(err)
	}
	failures, err := s.checkWorkerBrief(briefFrame(gatewayBrief("internal/gateway/brief.go")), "shop", "normal")
	if err != nil {
		t.Fatal(err)
	}
	want := "overlaps open change ch_open on internal/gateway/brief.go"
	if !strings.Contains(strings.Join(failures, "\n"), want) {
		t.Fatalf("failures %q do not say %q", failures, want)
	}
}

func TestWorkerBriefSimpleTaskNeedsAModel(t *testing.T) {
	s, _ := newTestStore(t)
	frame := briefFrame(gatewayBrief("internal/gateway/brief.go"))
	frame.Model = ""
	failures, err := s.checkWorkerBrief(frame, "shop", "simple")
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0] != protocol.BriefFailModel {
		t.Fatalf("failures = %v", failures)
	}
	frame.NotFamily = []string{"anthropic"}
	if failures, err = s.checkWorkerBrief(frame, "shop", "simple"); err != nil || len(failures) != 0 {
		t.Fatalf("not_family did not pass: %v %v", failures, err)
	}
}

func TestBriefRejectionsAreCountedPerTaskFor24Hours(t *testing.T) {
	s, c := newTestStore(t)
	key := protocol.BriefTaskKey("s_caller", "shop", "", "Goal: fix it")
	if err := s.recordBriefRejection("s_caller", key, []string{"Limits: missing"}); err != nil {
		t.Fatal(err)
	}
	n, err := s.briefRejectionCount("s_caller", key, s.now().Add(-24*time.Hour).Unix())
	if err != nil || n != 1 {
		t.Fatalf("count = %d, %v", n, err)
	}
	c.t = c.t.Add(25 * time.Hour)
	n, err = s.briefRejectionCount("s_caller", key, s.now().Add(-24*time.Hour).Unix())
	if err != nil || n != 0 {
		t.Fatalf("old rejection counted: %d, %v", n, err)
	}
	counts, err := s.BriefRejectionCountsByCaller(0)
	if err != nil || counts["s_caller"] != 1 {
		t.Fatalf("weekly counts = %v, %v", counts, err)
	}
}

// The first two attempts are refused; the third starts anyway and records one
// brief defect against the caller.
func TestThirdBriefAttemptStartsAndRecordsADefect(t *testing.T) {
	a := newAgyGate(t)
	task := gatewayBrief("internal/gateway/brief.go")
	task = strings.Replace(task, "Evidence:\n- the owner's words\n- the failing test\n- the diff\n", "", 1)
	start := func() error {
		_, err := a.g.workerStart(context.Background(), a.nc, protocol.WorkerStartFrame{Owner: a.owner, Level: "normal",
			WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: task, Harness: "agy", Model: "gemini-3.8-flash-low"}})
		return err
	}
	for i := 1; i <= 2; i++ {
		err := start()
		if err == nil || !strings.Contains(err.Error(), "brief refused") {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	err := start()
	if err == nil || strings.Contains(err.Error(), "brief refused") {
		t.Fatalf("third attempt was still refused: %v", err)
	}
	var defects int
	if err := a.g.store.db.QueryRow(`SELECT COUNT(*) FROM worker_brief_defects WHERE caller=?`, a.owner).Scan(&defects); err != nil {
		t.Fatal(err)
	}
	if defects != 1 {
		t.Fatalf("brief defects = %d, want 1", defects)
	}
	var note string
	if err := a.g.store.db.QueryRow(`SELECT note FROM worker_brief_defects WHERE caller=?`, a.owner).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "Evidence:") {
		t.Fatalf("defect note = %q", note)
	}
}

// mode check returns the failures, or an empty list, and starts nothing.
func TestWorkerStartCheckModeStartsNothing(t *testing.T) {
	a := newAgyGate(t)
	answer, err := a.g.workerStart(context.Background(), a.nc, protocol.WorkerStartFrame{Owner: a.owner, Level: "normal",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: gatewayBrief("internal/gateway/brief.go"), Harness: "agy", Model: "gemini-3.8-flash-low", Mode: protocol.WorkerStartModeCheck}})
	if err != nil {
		t.Fatal(err)
	}
	if !answer.Checked || len(answer.BriefFailures) != 0 {
		t.Fatalf("answer = %+v", answer)
	}
	var starts int
	if err := a.g.store.db.QueryRow(`SELECT COUNT(*) FROM worker_starts`).Scan(&starts); err != nil {
		t.Fatal(err)
	}
	if starts != 0 {
		t.Fatalf("check mode tracked %d starts", starts)
	}

	answer, err = a.g.workerStart(context.Background(), a.nc, protocol.WorkerStartFrame{Owner: a.owner, Level: "normal",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "Goal: no owner citation\n", Harness: "agy", Model: "gemini-3.8-flash-low", Mode: protocol.WorkerStartModeCheck}})
	if err != nil {
		t.Fatal(err)
	}
	if !answer.Checked || len(answer.BriefFailures) == 0 {
		t.Fatalf("answer = %+v", answer)
	}
}

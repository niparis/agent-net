package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func setupShadowJudge(t *testing.T, reply string, gate bool) (string, string) {
	t.Helper()
	dir := t.TempDir()
	replyFile := filepath.Join(dir, "reply")
	if err := os.WriteFile(replyFile, []byte(reply), 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "marker")
	t.Setenv(envFakeOpenCode, "1")
	t.Setenv(envFakeCodex, "1")
	t.Setenv(envFakeCodexExec, t.TempDir())
	t.Setenv(envFakeOpenCodeReply, replyFile)
	t.Setenv("FAKE_DIFFICULTY_MARKER", marker)
	path := filepath.Join(dir, "gate")
	if gate {
		t.Setenv("FAKE_DIFFICULTY_GATE", path)
	}
	return marker, path
}

const shadowReply = `{"reasoning":2,"scope":1,"coupling":1,"uncertainty":1,"verification":1,"risk":1,"confidence":"high","reasons":{"reasoning":"logic","scope":"local","coupling":"isolated","uncertainty":"known","verification":"test","risk":"safe"}}`

func TestWorkerJudgeRunsBeforeStartAndIsStored(t *testing.T) {
	marker, _ := setupShadowJudge(t, shadowReply, false)
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, withDifficulty(nil))
	w.register(t, newRepo(t, "shop"))
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "Evidence: local"})
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the judge did not run before the start returned")
	}
	if want := "Tell your user: worker on fake/m1, difficulty trivial (D 1.20; R2 S1 C1 U1 V1 K1)."; res.OwnerLine != want {
		t.Fatalf("owner line = %q, want %q", res.OwnerLine, want)
	}
	a := storedAssessment(t, g, res.SessionID)
	if a.Reasoning != 2 || a.Scope != 1 || a.Coupling != 1 || a.Uncertainty != 1 || a.Verification != 1 || a.Risk != 1 || a.D < 1.199 || a.D > 1.201 || a.Label != "trivial" || a.PickerLabel != "trivial" || a.RulesVersion != "2026-11-02.1" || a.JudgeHarness != "opencode" || a.JudgeModel == "" || len(a.Reasons) != 6 || a.Confidence != "high" || a.AssessedAt == 0 {
		t.Fatalf("assessment: %+v", a)
	}
	for _, m := range w.messagesOf(t, w.ownerID()) {
		if m.From == res.SessionID && strings.Contains(m.Body, "status: difficulty") {
			t.Fatalf("a separate difficulty notice was sent: %s", m.Body)
		}
	}
	if len(w.workers(t)) != 1 {
		t.Fatal("the assessment ended the worker")
	}
}

func TestWorkerJudgeFailureIsStoredAndNotANotice(t *testing.T) {
	setupShadowJudge(t, "not json", false)
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, withDifficulty(nil))
	w.register(t, newRepo(t, "shop"))
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "test"})
	a := storedAssessment(t, g, res.SessionID)
	if a.Label != "normal" || a.PickerLabel != "normal" || !strings.Contains(a.Error, "parse") {
		t.Fatalf("assessment: %+v", a)
	}
	for _, m := range w.messagesOf(t, w.ownerID()) {
		if strings.Contains(m.Body, "status: difficulty") {
			t.Fatalf("a separate difficulty notice was sent: %s", m.Body)
		}
	}
}

func TestWorkerResumedRunGetsNewAssessment(t *testing.T) {
	setupShadowJudge(t, shadowReply, false)
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, withDifficulty(nil))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "FAKE:handback first"})
	var run string
	eventually(t, "first assessment", func() bool {
		a, ok, err := g.gw.Store().Agent(res.SessionID)
		if err != nil || !ok || a.Worker.Assessment == nil {
			return false
		}
		run = a.Worker.Assessment.RunID
		return true
	})
	eventually(t, "worker ended and reported", func() bool {
		rows, _ := w.d.st.workers()
		return w.d.workerCount() == 0 && len(rows) == 1 && rows[0].Reported && rows[0].Resumable
	})
	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "continue"}), &sent)
	eventually(t, "resumed run assessed", func() bool {
		a, ok, err := g.gw.Store().Agent(res.SessionID)
		return err == nil && ok && a.Worker.Assessment != nil && a.Worker.Assessment.RunID != run
	})
}

func TestWorkerAssessmentQueueSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	st, err := openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := protocol.WorkerAssessment{RunID: "k_test", SessionID: "s_test", Error: "judge failed"}
	if err := st.enqueueWorkerAssessment(a); err != nil {
		t.Fatal(err)
	}
	st.close()
	st, err = openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	rows, err := st.pendingWorkerAssessments()
	if err != nil || len(rows) != 1 || rows[0].RunID != a.RunID || rows[0].Error != a.Error {
		t.Fatalf("pending: %+v, %v", rows, err)
	}
}

func TestWorkerAssessmentQueueFlush(t *testing.T) {
	_, gate := setupShadowJudge(t, shadowReply, true)
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "test"})
	a := protocol.WorkerAssessment{RunID: "k_replay", SessionID: res.SessionID, StartedAt: time.Now().UnixNano(), AssessedAt: time.Now().Unix(), Error: "judge failed", PickerLabel: "normal"}
	if err := w.d.st.enqueueWorkerAssessment(a); err != nil {
		t.Fatal(err)
	}
	w.d.flushWorkerAssessments(context.Background())
	rows, err := w.d.st.pendingWorkerAssessments()
	if err != nil || len(rows) != 0 {
		t.Fatalf("outbox: %+v %v", rows, err)
	}
	got, ok, err := g.gw.Store().Agent(res.SessionID)
	if err != nil || !ok || got.Worker.Assessment == nil || got.Worker.Assessment.RunID != a.RunID {
		t.Fatalf("stored: %+v %v", got, err)
	}
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerJudgeRetryStoresFirstFailure(t *testing.T) {
	setupShadowJudge(t, "not json", false)
	fakeDifficultyRun(t, map[string][]string{"judge": {difficultyScoresJSON}})
	d := &Daemon{cfg: Config{HarnessPaths: map[string]string{"opencode": os.Args[0], "codex": os.Args[0]}}}
	a := d.assessWorkerStart(context.Background(), t.TempDir(), "brief")
	if a.Error != "" || a.JudgeHarness != "codex" || a.JudgeModel != "gpt-6.1-sol" || !strings.Contains(a.Reasons["judge_note"], "parse") || a.PickerLabel != "hard" {
		t.Fatalf("assessment: %+v", a)
	}
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var stored protocol.WorkerAssessment
	if err := json.Unmarshal(raw, &stored); err != nil || stored.Reasons["judge_note"] != a.Reasons["judge_note"] {
		t.Fatalf("stored: %+v, %v", stored, err)
	}
}

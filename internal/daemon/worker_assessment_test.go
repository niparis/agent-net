package daemon

import (
	"context"
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
	t.Setenv(envFakeOpenCodeReply, replyFile)
	t.Setenv("FAKE_DIFFICULTY_MARKER", marker)
	path := filepath.Join(dir, "gate")
	if gate {
		t.Setenv("FAKE_DIFFICULTY_GATE", path)
	}
	return marker, path
}

const shadowReply = `{"reasoning":2,"scope":1,"coupling":1,"uncertainty":1,"verification":1,"risk":1,"confidence":"high","reasons":{"reasoning":"logic","scope":"local","coupling":"isolated","uncertainty":"known","verification":"test","risk":"safe"}}`

func TestWorkerShadowAssessmentAsyncAndStored(t *testing.T) {
	marker, gate := setupShadowJudge(t, shadowReply, true)
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, withDifficulty(nil))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "Evidence: local", Difficulty: "normal"})
	eventually(t, "judge starts while worker start already returned", func() bool { _, err := os.Stat(marker); return err == nil })
	a, ok, err := g.gw.Store().Agent(res.SessionID)
	if err != nil || !ok || a.Worker.Assessment != nil {
		t.Fatalf("assessment completed before gate: %+v %v", a, err)
	}
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "assessment stored", func() bool {
		a, ok, err = g.gw.Store().Agent(res.SessionID)
		return err == nil && ok && a.Worker.Assessment != nil
	})
	got := a.Worker.Assessment
	if got.Reasoning != 2 || got.Scope != 1 || got.Coupling != 1 || got.Uncertainty != 1 || got.Verification != 1 || got.Risk != 1 || got.D < 1.199 || got.D > 1.201 || got.Label != "trivial" || got.PickerLabel != "normal" || got.RulesVersion != "2026-11-02.1" || got.JudgeHarness != "opencode" || got.JudgeModel == "" || len(got.Reasons) != 6 || got.Confidence != "high" || got.AssessedAt == 0 {
		t.Fatalf("assessment: %+v", got)
	}
	eventually(t, "non-final assessment notice", func() bool {
		for _, m := range w.messagesOf(t, w.ownerID()) {
			if m.From == res.SessionID && strings.Contains(m.Body, "status: difficulty") && strings.Contains(m.Body, "R2 S1 C1 U1 V1 K1 · D 1.20 · trivial (picker used: normal)") && m.Type == protocol.TypeNotification {
				return true
			}
		}
		return false
	})
	if len(w.workers(t)) != 1 {
		t.Fatal("notice ended the worker")
	}
}

func TestWorkerShadowAssessmentFailure(t *testing.T) {
	setupShadowJudge(t, "not json", false)
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, withDifficulty(nil))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "test"})
	eventually(t, "failure stored", func() bool {
		a, ok, err := g.gw.Store().Agent(res.SessionID)
		return err == nil && ok && a.Worker.Assessment != nil && strings.Contains(a.Worker.Assessment.Error, "parse")
	})
	eventually(t, "failure notice", func() bool {
		for _, m := range w.messagesOf(t, w.ownerID()) {
			if strings.Contains(m.Body, "difficulty: no assessment (") {
				return true
			}
		}
		return false
	})
}

func TestWorkerShadowQueuedStartsOnlyWhenDequeued(t *testing.T) {
	marker, _ := setupShadowJudge(t, shadowReply, false)
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: cpuBusy()}
	w := startWorkerNode(t, g, queueSet, withDifficulty(withLoad(fl)))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, _ := helloPi(t, w.node, "caller", "/work/caller", true)
	res := startFrom(t, pi, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "queued"})
	if res.Status != "queued" {
		t.Fatalf("not queued: %+v", res)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("judge ran while queued")
	}
	fl.set(roomyLoad())
	eventually(t, "judge runs after dequeuing", func() bool { _, err := os.Stat(marker); return err == nil })
	eventually(t, "queued assessment stored", func() bool {
		for _, a := range w.agents(t, true) {
			if a.Worker != nil && a.Worker.Assessment != nil && a.Worker.Assessment.Label == "trivial" {
				return true
			}
		}
		return false
	})
}

func TestWorkerShadowResumedRunGetsNewAssessment(t *testing.T) {
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

func TestWorkerShadowAssessmentQueueSurvivesRestart(t *testing.T) {
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

func TestWorkerShadowAssessmentQueueFlush(t *testing.T) {
	_, gate := setupShadowJudge(t, shadowReply, true)
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, withDifficulty(nil))
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

// withDifficulty turns the difficulty judge back on for a test daemon.
func withDifficulty(next func(*Config)) func(*Config) {
	return func(c *Config) {
		c.testNoDifficulty = false
		if next != nil {
			next(c)
		}
	}
}

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Delivery pipeline build step 1, end to end: a worker's complete hand-back
// records a change in the real gateway, the caller reports steps on it with
// the daemon method step_report, and a continue moves the same change on.
// The workers are fake Claude Code workers; the caller is a Pi session.

// deliveryHandbackNode starts a real gateway and a worker node with the fake
// Claude harness. cfg may change the node's config.
func deliveryHandbackNode(t *testing.T, cfg func(*Config)) (*claudeNode, *testGateway, string) {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.Idle = time.Hour
	sessions, stopLog := useFakeClaude(t)
	adapter := &fsClaudeAdapter{dir: sessions}
	w := startWorkerNode(t, g, set, func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.Adapters = append(c.Adapters, adapter)
		c.HarnessPaths = map[string]string{"claude": os.Args[0]}
		if cfg != nil {
			cfg(c)
		}
	})
	c := &claudeNode{workerNode: w, sessions: sessions, stopLog: stopLog, adapter: adapter}
	repo := newRepo(t, "shop")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".gitignore")
	gitIn(t, repo, "commit", "-q", "-m", "ignore file")
	c.register(t, repo)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("daemon log: %v", c.logs.lines())
		}
	})
	return c, g, repo
}

// deliveryCaller is a Pi session on the node that runs a Claude model, so
// its reviews of a Claude worker are by the same model maker.
func deliveryCaller(t *testing.T, c *claudeNode) (*localClient, string) {
	t.Helper()
	return helloDelivery(t, c.node, "caller", "/work/caller", "claude-opus-4")
}

// startDeliveryWorker starts a worker from the caller and waits until it has
// its task.
func startDeliveryWorker(t *testing.T, c *claudeNode, caller *localClient, callerID string, req protocol.WorkerStartRequest) protocol.WorkerStartResult {
	t.Helper()
	var res protocol.WorkerStartResult
	if err := json.Unmarshal(caller.mustCall("worker_start", req), &res); err != nil || res.SessionID == "" {
		t.Fatalf("worker_start: %+v %v", res, err)
	}
	eventually(t, "the worker has its task", func() bool {
		for _, m := range c.messagesOf(t, callerID) {
			if m.To == res.SessionID && m.Type == protocol.TypeRequest {
				return true
			}
		}
		return false
	})
	if res.Worktree == "" || res.Branch == "" {
		if row, ok, _ := c.d.st.workerBySession(res.SessionID); ok {
			res.Worktree, res.Branch = row.Worktree, row.Branch
		}
	}
	return res
}

// handbackNotice waits for the hand-back notice of worker session to the
// caller.
func handbackNotice(t *testing.T, c *claudeNode, callerID, session string) protocol.Message {
	t.Helper()
	var got protocol.Message
	eventually(t, "the hand-back notice of "+session, func() bool {
		for _, m := range c.messagesOf(t, callerID) {
			if m.From == session && m.To == callerID && strings.HasPrefix(m.Body, "[agent-net worker notice]") &&
				strings.Contains(m.Body, protocol.ReportMarker) {
				got = m
				return true
			}
		}
		return false
	})
	return got
}

func findChanges(t *testing.T, lc *localClient, args protocol.ChangesFindArgs) protocol.ChangesFindResult {
	t.Helper()
	var out protocol.ChangesFindResult
	if err := json.Unmarshal(lc.mustCall(protocol.OpChangesFind, args), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func reportStep(t *testing.T, lc *localClient, args protocol.StepReportArgs) protocol.StepReportResult {
	t.Helper()
	var out protocol.StepReportResult
	if err := json.Unmarshal(lc.mustCall(protocol.OpStepReport, args), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func hasStep(refs []protocol.StepRef, step string) bool {
	for _, r := range refs {
		if r.Step == step && r.For == "" && !r.AfterMerge {
			return true
		}
	}
	return false
}

func TestCompleteHandbackRecordsAChange(t *testing.T) {
	c, g, repo := deliveryHandbackNode(t, nil)
	caller, callerID := deliveryCaller(t, c)
	base := gitOutT(t, repo, "rev-parse", "HEAD")
	res := startDeliveryWorker(t, c, caller, callerID, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude",
		Title: "Add the dirty file", Task: "FAKE:dirty FAKE:handback", Isolation: protocol.IsolationWorktree})
	n := handbackNotice(t, c, callerID, res.SessionID)
	head := gitOutT(t, repo, "rev-parse", res.Branch)
	if head == base {
		t.Fatal("the hand-back made no commit")
	}

	found := findChanges(t, caller, protocol.ChangesFindArgs{Project: "shop"})
	if len(found.Items) != 1 {
		t.Fatalf("changes_find: %+v", found)
	}
	ch := found.Items[0].Change
	if !strings.HasPrefix(ch.ID, protocol.ChangeIDPrefix) || ch.Project != "shop" || ch.Branch != res.Branch || ch.HeadCommit != head ||
		ch.BaseCommit != base || ch.Revision != 1 || ch.Uncommitted || ch.State != protocol.ChangeOpen {
		t.Errorf("change: %+v", ch)
	}
	if ch.Caller != callerID || ch.Title != "Add the dirty file" || ch.Machine != c.d.NodeID() {
		t.Errorf("caller, title or machine: %+v", ch)
	}
	if ch.Origin.Kind != protocol.OriginWorker || ch.Origin.WorkerID != workerIDOf(res) || ch.Origin.SessionID != res.SessionID ||
		ch.Origin.ModelFamily != "anthropic" {
		t.Errorf("origin: %+v", ch.Origin)
	}
	if !strings.Contains(n.Body, "\nchange: "+ch.ID) {
		t.Errorf("the notice does not name the change %s:\n%s", ch.ID, n.Body)
	}
	if strings.Contains(n.Body, "delivery record:") {
		t.Errorf("the notice has a delivery error:\n%s", n.Body)
	}
	// The gateway publishes the notice before the local acknowledgement is saved.
	eventually(t, "the worker row keeps the change", func() bool {
		row, ok, err := c.d.st.workerBySession(res.SessionID)
		return err == nil && ok && row.ChangeID == ch.ID
	})
	stored, err := g.gw.Store().GetChange(ch.ID)
	if err != nil || len(stored.Revisions) != 1 || stored.Revisions[0].Commit != head {
		t.Errorf("gateway revisions: %+v %v", stored.Revisions, err)
	}
	mine := findChanges(t, caller, protocol.ChangesFindArgs{Caller: protocol.ChangesCallerMe})
	if len(mine.Items) != 1 || mine.Items[0].Change.ID != ch.ID {
		t.Errorf("changes_find caller me: %+v", mine)
	}
	one := findChanges(t, caller, protocol.ChangesFindArgs{Change: ch.ID})
	if len(one.Items) != 1 || len(one.Items[0].Revisions) != 1 {
		t.Errorf("changes_find one change: %+v", one)
	}

	// The caller accepts and reviews. Its model is a Claude model too, so the
	// review is by the same model maker: recorded, but not independent by
	// family.
	acc := reportStep(t, caller, protocol.StepReportArgs{Change: ch.ID, StepRef: protocol.StepRef{Step: protocol.StepAccept},
		Result: protocol.StepResultPass, Commit: head, Note: "read the diff"})
	if acc.Evidence.By != callerID || acc.Evidence.Revision != 1 || acc.Evidence.Commit != head {
		t.Errorf("accept evidence: %+v", acc.Evidence)
	}
	if hasStep(acc.Change.Next, protocol.StepAccept) {
		t.Errorf("accept is still next after its pass: %+v", acc.Change.Next)
	}
	rev := reportStep(t, caller, protocol.StepReportArgs{Change: ch.ID, StepRef: protocol.StepRef{Step: protocol.StepReview},
		Result: protocol.StepResultPass, Commit: head, Fields: map[string]string{protocol.FieldFindingsOpen: "0"}})
	if got := rev.Evidence.Fields[protocol.FieldIndependent]; got != protocol.IndependentSession {
		t.Errorf("a review by the same model maker is independent=%q, want %q", got, protocol.IndependentSession)
	}
	if rev.Evidence.ByModelFamily != "anthropic" {
		t.Errorf("review model family %q", rev.Evidence.ByModelFamily)
	}
	stored, err = g.gw.Store().GetChange(ch.ID)
	if err != nil || len(stored.Evidence) != 2 {
		t.Fatalf("stored evidence: %+v %v", stored.Evidence, err)
	}

	// The worker ended after its hand-back. A continue makes a new branch;
	// its hand-back moves the same change there as revision 2.
	eventually(t, "the worker ended and the gateway acknowledged it", func() bool {
		row, ok, err := c.d.st.workerBySession(res.SessionID)
		return c.d.workerCount() == 0 && err == nil && ok && row.EndedReason == protocol.StopHandback && row.Reported && row.Resumable
	})
	var next protocol.WorkerStartResult
	if err := json.Unmarshal(caller.mustCall("worker_start", protocol.WorkerStartRequest{Continue: res.SessionID, Task: "FAKE:edit"}), &next); err != nil || next.SessionID == "" {
		t.Fatalf("continue: %+v %v", next, err)
	}
	n2 := handbackNotice(t, c, callerID, next.SessionID)
	row, ok, err := c.d.st.workerBySession(next.SessionID)
	if err != nil || !ok {
		t.Fatalf("continued worker row: %v", err)
	}
	if row.Branch == res.Branch {
		t.Fatalf("continue kept the branch %s", row.Branch)
	}
	head2 := gitOutT(t, repo, "rev-parse", row.Branch)
	if head2 == head {
		t.Fatal("the continued worker made no commit")
	}
	if row.ChangeID != ch.ID {
		t.Errorf("continued worker row change %q, want %q", row.ChangeID, ch.ID)
	}
	if !strings.Contains(n2.Body, "\nchange: "+ch.ID) {
		t.Errorf("the second notice does not name change %s:\n%s", ch.ID, n2.Body)
	}
	all := findChanges(t, caller, protocol.ChangesFindArgs{Project: "shop", State: protocol.ChangeListAny})
	if len(all.Items) != 1 {
		t.Fatalf("the continue made another change: %+v", all.Items)
	}
	moved := all.Items[0]
	if moved.Change.ID != ch.ID || moved.Change.Branch != row.Branch || moved.Change.HeadCommit != head2 || moved.Change.Revision != 2 {
		t.Errorf("change after continue: %+v", moved.Change)
	}
	if moved.Change.Origin.SessionID != next.SessionID || moved.Change.BaseCommit != base {
		t.Errorf("revision 2 origin or base: %+v", moved.Change)
	}
	// Revision 1's accept no longer counts: accept is next again.
	if !hasStep(moved.Next, protocol.StepAccept) {
		t.Errorf("accept of revision 1 still counts: next %+v, steps %+v", moved.Next, moved.Steps)
	}
	stored, err = g.gw.Store().GetChange(ch.ID)
	if err != nil || len(stored.Revisions) != 2 || stored.Revisions[1].Commit != head2 || len(stored.Evidence) != 2 {
		t.Errorf("gateway after continue: revisions %+v evidence %d %v", stored.Revisions, len(stored.Evidence), err)
	}
}

func TestPartialHandbackRecordsNoChange(t *testing.T) {
	c, g, _ := deliveryHandbackNode(t, nil)
	caller, callerID := deliveryCaller(t, c)
	res := startDeliveryWorker(t, c, caller, callerID, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude",
		Title: "Half done", Task: "FAKE:dirty FAKE:partial FAKE:handback", Isolation: protocol.IsolationWorktree, AutoContinue: autoContinueOff()})
	n := handbackNotice(t, c, callerID, res.SessionID)
	if strings.Contains(n.Body, "\nchange: ") || strings.Contains(n.Body, "delivery record:") {
		t.Errorf("a partial hand-back names a change:\n%s", n.Body)
	}
	list, err := g.gw.Store().ListChanges(protocol.ChangeListRequest{State: protocol.ChangeListAny})
	if err != nil || len(list.Changes) != 0 {
		t.Errorf("a partial hand-back recorded a change: %+v %v", list.Changes, err)
	}
	if found := findChanges(t, caller, protocol.ChangesFindArgs{}); len(found.Items) != 0 {
		t.Errorf("changes_find: %+v", found)
	}
}

// A gateway that does not know change.submit (an older one) is skipped: the
// hand-back works and the notice says nothing about delivery.
func TestHandbackWithAnOldGatewaySkipsTheRecord(t *testing.T) {
	f := &fakeDeliveryGateway{sent: map[string][]json.RawMessage{}, answers: map[string]func(json.RawMessage) (any, error){}}
	f.answer(protocol.FrameChangeSubmit, func(json.RawMessage) (any, error) {
		return nil, errors.New(`unknown frame type "change.submit"`)
	})
	c, _, _ := deliveryHandbackNode(t, func(cfg *Config) { cfg.testGatewayCall = f.call })
	caller, callerID := deliveryCaller(t, c)
	res := startDeliveryWorker(t, c, caller, callerID, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude",
		Title: "Old gateway", Task: "FAKE:dirty FAKE:handback", Isolation: protocol.IsolationWorktree})
	n := handbackNotice(t, c, callerID, res.SessionID)
	if len(f.frames(protocol.FrameChangeSubmit)) != 1 {
		t.Errorf("change.submit frames: %d", len(f.frames(protocol.FrameChangeSubmit)))
	}
	if strings.Contains(n.Body, "\nchange: ") || strings.Contains(n.Body, "delivery record:") {
		t.Errorf("notice with an old gateway:\n%s", n.Body)
	}
}

// Any other error does not fail the hand-back: the notice carries one line.
func TestHandbackDeliveryErrorIsOneNoticeLine(t *testing.T) {
	f := &fakeDeliveryGateway{sent: map[string][]json.RawMessage{}, answers: map[string]func(json.RawMessage) (any, error){}}
	f.answer(protocol.FrameChangeSubmit, func(json.RawMessage) (any, error) {
		return nil, errors.New("the store is full")
	})
	c, _, repo := deliveryHandbackNode(t, func(cfg *Config) { cfg.testGatewayCall = f.call })
	caller, callerID := deliveryCaller(t, c)
	res := startDeliveryWorker(t, c, caller, callerID, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude",
		Title: "Store full", Task: "FAKE:dirty FAKE:handback", Isolation: protocol.IsolationWorktree})
	n := handbackNotice(t, c, callerID, res.SessionID)
	if !strings.Contains(n.Body, "\ndelivery record: the store is full\n") {
		t.Errorf("notice lacks the delivery line:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "status: done") {
		t.Errorf("the hand-back did not go through:\n%s", n.Body)
	}
	// What the daemon sent: the worker's branch, the full head, its origin.
	frames := f.frames(protocol.FrameChangeSubmit)
	if len(frames) != 1 {
		t.Fatalf("change.submit frames: %d", len(frames))
	}
	var q protocol.ChangeSubmitRequest
	if err := json.Unmarshal(frames[0], &q); err != nil {
		t.Fatal(err)
	}
	if q.SessionID != callerID || q.Project != "shop" || q.Branch != res.Branch || q.HeadCommit != gitOutT(t, repo, "rev-parse", res.Branch) ||
		q.Uncommitted || q.Title != "Store full" || q.ChangeID != "" {
		t.Errorf("change.submit: %+v", q)
	}
	if q.Origin.Kind != protocol.OriginWorker || q.Origin.WorkerID != workerIDOf(res) || q.Origin.SessionID != res.SessionID || q.Origin.Model != "sonnet" {
		t.Errorf("origin: %+v", q.Origin)
	}
}

// The record waits for the gateway at most a short time: a gateway that does
// not answer does not hold the hand-back.
func TestHandbackDeliveryRecordHasATimeLimit(t *testing.T) {
	f := &fakeDeliveryGateway{sent: map[string][]json.RawMessage{}, answers: map[string]func(json.RawMessage) (any, error){}}
	c, _, _ := deliveryHandbackNode(t, func(cfg *Config) {
		cfg.testDeliveryRecordTimeout = 200 * time.Millisecond
		cfg.testGatewayCall = func(ctx context.Context, typ string, params any) (json.RawMessage, bool, error) {
			if typ != protocol.FrameChangeSubmit {
				return f.call(ctx, typ, params)
			}
			<-ctx.Done()
			return nil, true, ctx.Err()
		}
	})
	caller, callerID := deliveryCaller(t, c)
	res := startDeliveryWorker(t, c, caller, callerID, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude",
		Title: "Slow gateway", Task: "FAKE:dirty FAKE:handback", Isolation: protocol.IsolationWorktree})
	n := handbackNotice(t, c, callerID, res.SessionID)
	if !strings.Contains(n.Body, "\ndelivery record: ") {
		t.Errorf("notice lacks the delivery line:\n%s", n.Body)
	}
}

// A fix by message: the caller messages the worker after its hand-back, the
// same session starts again on the same branch, and its next complete
// hand-back is revision 2 of the same change.
func TestFixByMessageIsTheNextRevision(t *testing.T) {
	c, _, repo := deliveryHandbackNode(t, nil)
	caller, callerID := deliveryCaller(t, c)
	res := startDeliveryWorker(t, c, caller, callerID, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude",
		Title: "Add the dirty file", Task: "FAKE:dirty FAKE:handback", Isolation: protocol.IsolationWorktree})
	handbackNotice(t, c, callerID, res.SessionID)
	head := gitOutT(t, repo, "rev-parse", res.Branch)
	first := findChanges(t, caller, protocol.ChangesFindArgs{})
	if len(first.Items) != 1 || first.Items[0].Change.HeadCommit != head {
		t.Fatalf("first record: %+v", first.Items)
	}
	id := first.Items[0].Change.ID
	reportStep(t, caller, protocol.StepReportArgs{Change: id, StepRef: protocol.StepRef{Step: protocol.StepAccept}, Result: protocol.StepResultFail,
		Commit: head, Note: "add work.txt too"})
	eventually(t, "the worker ended", func() bool { return c.d.workerCount() == 0 })

	caller.mustCall("send", map[string]any{"to": res.SessionID, "body": "FAKE:edit FAKE:handback"})
	var notices []protocol.Message
	eventually(t, "the second hand-back", func() bool {
		notices = notices[:0]
		for _, m := range c.messagesOf(t, callerID) {
			if m.From == res.SessionID && strings.Contains(m.Body, protocol.ReportMarker) {
				notices = append(notices, m)
			}
		}
		return len(notices) >= 2
	})
	head2 := gitOutT(t, repo, "rev-parse", res.Branch)
	if head2 == head {
		t.Fatal("the fix made no commit")
	}
	if !strings.Contains(notices[0].Body, "\nchange: "+id) {
		t.Errorf("the second notice does not name change %s:\n%s", id, notices[0].Body)
	}
	all := findChanges(t, caller, protocol.ChangesFindArgs{State: protocol.ChangeListAny})
	if len(all.Items) != 1 {
		t.Fatalf("the fix made another change: %+v", all.Items)
	}
	ch := all.Items[0]
	if ch.Change.ID != id || ch.Change.Branch != res.Branch || ch.Change.HeadCommit != head2 || ch.Change.Revision != 2 {
		t.Errorf("change after the fix: %+v", ch.Change)
	}
	// The failed accept was on revision 1: accept is ready again.
	if !hasStep(ch.Next, protocol.StepAccept) {
		t.Errorf("next after the fix: %+v", ch.Next)
	}
}

// The change a continue carries is kept through the start queue and on the
// worker record.
func TestChangeIDIsKeptInTheQueueAndTheWorkerRow(t *testing.T) {
	t.Parallel()
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	if err := st.addQueued(queuedStart{ID: "q_test", Owner: "owner", Project: "shop", Path: "/x", Harness: "claude", ChangeID: "ch_kept", QueuedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	q, err := st.queue()
	if err != nil || len(q) != 1 || q[0].ChangeID != "ch_kept" {
		t.Errorf("queue: %+v %v", q, err)
	}
	if err := st.saveWorker(workerRow{ID: "w_test", Harness: "claude", Project: "shop", Path: "/x", Owner: "owner", SessionID: "s_test",
		StartToken: "t", StartedAt: time.Now(), LastActive: time.Now(), ChangeID: "ch_kept"}); err != nil {
		t.Fatal(err)
	}
	row, ok, err := st.workerBySession("s_test")
	if err != nil || !ok || row.ChangeID != "ch_kept" {
		t.Errorf("worker row: %+v %v", row.ChangeID, err)
	}
	rows, err := st.workers()
	if err != nil || len(rows) != 1 || rows[0].ChangeID != "ch_kept" {
		t.Errorf("workers: %v", err)
	}
}

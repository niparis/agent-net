package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/niparis/agent-net/internal/protocol"
)

// fakeDeliveryGateway answers the frames the test names and lets every other
// frame go to the real test gateway. It records what the daemon sent.
type fakeDeliveryGateway struct {
	mu      sync.Mutex
	sent    map[string][]json.RawMessage
	answers map[string]func(raw json.RawMessage) (any, error)
}

func (f *fakeDeliveryGateway) call(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
	f.mu.Lock()
	answer, ok := f.answers[typ]
	if !ok {
		f.mu.Unlock()
		return nil, false, nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		f.mu.Unlock()
		return nil, true, err
	}
	f.sent[typ] = append(f.sent[typ], raw)
	f.mu.Unlock()
	res, err := answer(raw)
	if err != nil {
		return nil, true, err
	}
	out, err := json.Marshal(res)
	return out, true, err
}

func (f *fakeDeliveryGateway) answer(typ string, fn func(raw json.RawMessage) (any, error)) {
	f.mu.Lock()
	f.answers[typ] = fn
	f.mu.Unlock()
}

func (f *fakeDeliveryGateway) frames(typ string) []json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]json.RawMessage(nil), f.sent[typ]...)
}

func deliveryTestView(c protocol.Change) protocol.ChangeView {
	return protocol.ChangeView{Change: c, Steps: protocol.PipelineToday().Status(c, nil)}
}

func TestDeliveryDiffStatBoundedAndSafe(t *testing.T) {
	in := "good\x00name\n" + strings.Repeat("é", protocol.MaxDiffStatBytes)
	got := deliveryDiffStat(in)
	if len(got) > protocol.MaxDiffStatBytes || !utf8.ValidString(got) || strings.ContainsRune(got, '\x00') || !strings.Contains(got, "truncated") {
		t.Fatalf("unsafe diff stat: bytes=%d valid=%v", len(got), utf8.ValidString(got))
	}
}

func TestDeliverySubmitSummarizesTargetBranchDiff(t *testing.T) {
	n, f := startDeliveryNode(t)
	_, wt := deliveryWorktree(t)
	lc, _ := helloDelivery(t, n, "stat-submitter", wt, "claude-opus-5-5")
	lc.mustCall(protocol.OpChangeSubmit, protocol.ChangeSubmitArgs{Branch: "feat/x", TargetBranch: "main"})
	sent := f.frames(protocol.FrameChangeSubmit)
	if len(sent) != 1 {
		t.Fatalf("submit frames: %d", len(sent))
	}
	var q protocol.ChangeSubmitRequest
	if err := json.Unmarshal(sent[0], &q); err != nil {
		t.Fatal(err)
	}
	if !protocol.ValidCommit(q.BaseCommit) || !strings.Contains(q.DiffStat, "a.txt") {
		t.Fatalf("missing target diff: %+v", q)
	}
}

func TestDeliveryDirectOwnerReportRoutes(t *testing.T) {
	n, f := startDeliveryNode(t)
	_, wt := deliveryWorktree(t)
	lc, sid := helloDelivery(t, n, "owner-reporter", wt, "claude-opus-5-5")
	f.answer(protocol.FrameStepReport, func(raw json.RawMessage) (any, error) {
		return protocol.StepReportResult{Change: deliveryTestView(protocol.Change{ID: "ch_1"})}, nil
	})
	q := protocol.StepReportArgs{Change: "ch_1", StepRef: protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: strings.Repeat("a", 40), DoneBy: protocol.EvidenceByOwner}
	lc.mustCall(protocol.OpStepReport, q)
	frames := f.frames(protocol.FrameStepReport)
	if len(frames) != 1 {
		t.Fatalf("report frames: %d", len(frames))
	}
	var sent protocol.StepReportRequest
	if err := json.Unmarshal(frames[0], &sent); err != nil || sent.SessionID != sid || sent.DoneBy != protocol.EvidenceByOwner {
		t.Fatalf("owner report: %+v, %v", sent, err)
	}
	q.Step, q.For = protocol.StepTest, ""
	if _, err := lc.call(protocol.OpStepReport, q); err == nil {
		t.Fatal("owner attribution accepted for a non-approval step")
	}
}

// startDeliveryNode starts a daemon whose delivery frames go to a fake
// gateway. The fake answers the delivery frames with a stored change built
// from the request.
func startDeliveryNode(t *testing.T) (*node, *fakeDeliveryGateway) {
	t.Helper()
	f := &fakeDeliveryGateway{sent: map[string][]json.RawMessage{}, answers: map[string]func(json.RawMessage) (any, error){}}
	f.answer(protocol.FrameChangeSubmit, func(raw json.RawMessage) (any, error) {
		var q protocol.ChangeSubmitRequest
		_ = json.Unmarshal(raw, &q)
		c := protocol.Change{ID: "ch_1", Project: q.Project, Branch: q.Branch, HeadCommit: q.HeadCommit, Uncommitted: q.Uncommitted,
			Revision: 1, Origin: q.Origin, Caller: q.SessionID, Title: q.Title, State: protocol.ChangeOpen, CreatedAt: 1}
		return protocol.ChangeSubmitResult{Change: deliveryTestView(c)}, nil
	})
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", cfg: func(c *Config) { c.testGatewayCall = f.call }})
	return n, f
}

// helloDelivery connects a Pi session in cwd that runs model, and returns the
// client and its session ID.
func helloDelivery(t *testing.T, n *node, nativeID, cwd, model string) (*localClient, string) {
	t.Helper()
	lc := dialLocal(t, n.sock)
	lc.AutoAck = true
	res := lc.mustCall("hello", map[string]any{"client": "pi", "harness": "pi", "native_id": nativeID, "cwd": cwd, "status": "idle", "model": model})
	var r struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(res, &r)
	if r.SessionID == "" {
		t.Fatal("hello returned no session_id")
	}
	return lc, r.SessionID
}

func gitHead(t *testing.T, dir, rev string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", rev).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// deliveryWorktree makes a repository demo with a linked worktree on branch
// feat/x that has one commit more than main.
func deliveryWorktree(t *testing.T) (repo, wt string) {
	t.Helper()
	repo = newRepo(t, "demo")
	wt = filepath.Join(filepath.Dir(repo), "demo-feat")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "feat/x", wt)
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "a.txt")
	gitIn(t, wt, "commit", "-q", "-m", "feat")
	return repo, wt
}

// A submit from a session in a linked worktree sends the session's project,
// the branch, the branch head as a full hash, and whether the worktree has
// changes that are not committed. The session and its model come from the
// daemon, never from the arguments.
func TestChangeSubmitFromWorktreeSession(t *testing.T) {
	n, f := startDeliveryNode(t)
	_, wt := deliveryWorktree(t)
	lc, sid := helloDelivery(t, n, "np-submit", wt, "claude-opus-5-5")
	head := gitHead(t, wt, "HEAD")

	res := lc.mustCall(protocol.OpChangeSubmit, protocol.ChangeSubmitArgs{Branch: "feat/x", Title: "Add a"})
	var out protocol.ChangeSubmitResult
	if err := json.Unmarshal(res, &out); err != nil || out.Change.Change.ID != "ch_1" {
		t.Fatalf("submit result %s: %v", res, err)
	}
	sent := f.frames(protocol.FrameChangeSubmit)
	if len(sent) != 1 {
		t.Fatalf("change.submit sent %d times", len(sent))
	}
	var q protocol.ChangeSubmitRequest
	_ = json.Unmarshal(sent[0], &q)
	want := protocol.ChangeSubmitRequest{SessionID: sid, Project: "demo", Branch: "feat/x", HeadCommit: head, Title: "Add a", SensitivePath: "diff unavailable",
		Origin: protocol.ChangeOrigin{Kind: protocol.OriginSession, SessionID: sid, Model: "claude-opus-5-5", ModelFamily: "anthropic"}}
	if deliveryJSON(q) != deliveryJSON(want) {
		t.Fatalf("change.submit\n got %s\nwant %s", deliveryJSON(q), deliveryJSON(want))
	}
	if len(q.HeadCommit) != 40 {
		t.Fatalf("head commit %q is not a full hash", q.HeadCommit)
	}

	// A changed file in the worktree makes the submit uncommitted.
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lc.mustCall(protocol.OpChangeSubmit, protocol.ChangeSubmitArgs{Branch: "feat/x", Change: "ch_1"})
	sent = f.frames(protocol.FrameChangeSubmit)
	q = protocol.ChangeSubmitRequest{}
	_ = json.Unmarshal(sent[len(sent)-1], &q)
	if !q.Uncommitted || q.HeadCommit != head || q.ChangeID != "ch_1" {
		t.Fatalf("submit with a changed file: %s", sent[len(sent)-1])
	}

	// Without a branch, the session's own branch is the change's branch.
	lc.mustCall(protocol.OpChangeSubmit, protocol.ChangeSubmitArgs{})
	sent = f.frames(protocol.FrameChangeSubmit)
	q = protocol.ChangeSubmitRequest{}
	_ = json.Unmarshal(sent[len(sent)-1], &q)
	if q.Branch != "feat/x" || q.HeadCommit != head {
		t.Fatalf("submit without a branch: %s", sent[len(sent)-1])
	}
}

// A submit for a branch checked out in another worktree takes that branch's
// head, and the changes of that worktree, not of the session's folder.
func TestChangeSubmitOtherBranchOfTheRepository(t *testing.T) {
	n, f := startDeliveryNode(t)
	repo, wt := deliveryWorktree(t)
	if err := os.WriteFile(filepath.Join(wt, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lc, _ := helloDelivery(t, n, "np-other", repo, "deepseek/deepseek-v4-pro")
	lc.mustCall(protocol.OpChangeSubmit, protocol.ChangeSubmitArgs{Branch: "feat/x"})
	var q protocol.ChangeSubmitRequest
	_ = json.Unmarshal(f.frames(protocol.FrameChangeSubmit)[0], &q)
	if q.HeadCommit != gitHead(t, repo, "feat/x") || !q.Uncommitted || q.Origin.ModelFamily != "deepseek" {
		t.Fatalf("submit of feat/x from the main checkout: %s", deliveryJSON(q))
	}
}

// A submit that git cannot back is refused before the gateway sees it, with a
// reason the agent can act on.
func TestChangeSubmitRefusals(t *testing.T) {
	n, f := startDeliveryNode(t)
	repo, wt := deliveryWorktree(t)
	lc, _ := helloDelivery(t, n, "np-refuse", wt, "claude-opus-5-5")
	plain := t.TempDir()
	lcPlain, _ := helloDelivery(t, n, "np-plain", plain, "claude-opus-5-5")
	unknown := strings.Repeat("b", 40)
	for _, tc := range []struct {
		lc   *localClient
		args protocol.ChangeSubmitArgs
		want string
	}{
		{lc, protocol.ChangeSubmitArgs{Branch: "no/such"}, `branch "no/such"`},
		{lc, protocol.ChangeSubmitArgs{Branch: "feat/x", Commit: unknown}, unknown},
		{lc, protocol.ChangeSubmitArgs{Branch: "feat/x", Commit: gitHead(t, repo, "main~0") + "x"}, "full hash"},
		{lc, protocol.ChangeSubmitArgs{Branch: "feat/x", Project: "other"}, `project "other"`},
		{lcPlain, protocol.ChangeSubmitArgs{Branch: "main"}, "not in a git repository"},
	} {
		_, err := tc.lc.call(protocol.OpChangeSubmit, tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("submit %+v: %v, want an error with %q", tc.args, err, tc.want)
		}
	}
	// A commit on the branch that is not its head is not the head either.
	_, err := lc.call(protocol.OpChangeSubmit, protocol.ChangeSubmitArgs{Branch: "main", Commit: gitHead(t, repo, "feat/x")})
	if err == nil || !strings.Contains(err.Error(), "is not on branch") {
		t.Errorf("commit of feat/x submitted as main: %v", err)
	}
	if got := f.frames(protocol.FrameChangeSubmit); len(got) != 0 {
		t.Fatalf("refused submits reached the gateway: %s", got)
	}
}

// A worker does not submit changes itself: its hand-back does.
func TestChangeSubmitRefusedForWorkers(t *testing.T) {
	n, f := startDeliveryNode(t)
	_, wt := deliveryWorktree(t)
	lc, _ := helloDelivery(t, n, "np-worker", wt, "claude-opus-5-5")
	n.d.mu.Lock()
	for _, ls := range n.d.sessions {
		if ls.info.NativeID == "np-worker" {
			ls.info.Worker = &protocol.WorkerInfo{ID: "w_1", Owner: "s_caller"}
		}
	}
	n.d.mu.Unlock()
	for _, op := range []string{protocol.OpChangeSubmit, protocol.OpStepClaim, protocol.OpStepReport} {
		_, err := lc.call(op, map[string]any{"branch": "feat/x"})
		if err == nil || !strings.Contains(err.Error(), "worker") {
			t.Errorf("%s from a worker: %v", op, err)
		}
	}
	if got := f.frames(protocol.FrameChangeSubmit); len(got) != 0 {
		t.Fatalf("a worker's submit reached the gateway: %s", got)
	}
}

// A report names the reporting session; done_by is accepted only for a worker
// that session started, as the gateway's session record says.
func TestStepReportDoneBy(t *testing.T) {
	n, f := startDeliveryNode(t)
	_, wt := deliveryWorktree(t)
	lc, sid := helloDelivery(t, n, "np-report", wt, "claude-opus-5-5")
	commit := gitHead(t, wt, "HEAD")
	agents := map[string]protocol.AgentView{
		"s_mine":    {SessionID: "s_mine", Worker: &protocol.WorkerInfo{ID: "w_1", Owner: sid}},
		"s_foreign": {SessionID: "s_foreign", Worker: &protocol.WorkerInfo{ID: "w_2", Owner: "s_someone_else"}},
		"s_plain":   {SessionID: "s_plain"},
	}
	f.answer("query.agent", func(raw json.RawMessage) (any, error) {
		var q protocol.QueryAgent
		_ = json.Unmarshal(raw, &q)
		a, ok := agents[q.SessionID]
		if !ok {
			return nil, errors.New("unknown session " + q.SessionID)
		}
		return map[string]any{"agent": a}, nil
	})
	f.answer(protocol.FrameStepReport, func(raw json.RawMessage) (any, error) {
		var q protocol.StepReportRequest
		_ = json.Unmarshal(raw, &q)
		c := protocol.Change{ID: q.ChangeID, Project: "demo", Branch: "feat/x", HeadCommit: q.Commit, Revision: 1,
			Origin: protocol.ChangeOrigin{Kind: protocol.OriginSession, SessionID: sid}, State: protocol.ChangeOpen, CreatedAt: 1}
		by := q.SessionID
		if q.DoneBy != "" {
			by = q.DoneBy
		}
		ev := protocol.Evidence{ChangeID: q.ChangeID, Revision: 1, StepRef: q.StepRef, Result: q.Result, Commit: q.Commit, Fields: q.Fields, By: by, ReportedBy: q.SessionID, At: 2}
		return protocol.StepReportResult{Evidence: ev, Change: deliveryTestView(c)}, nil
	})
	report := func(doneBy string) (json.RawMessage, error) {
		return lc.call(protocol.OpStepReport, protocol.StepReportArgs{Change: "ch_1", StepRef: protocol.StepRef{Step: protocol.StepReview},
			Result: protocol.StepResultPass, Commit: commit, Fields: map[string]string{protocol.FieldFindingsOpen: "0"}, DoneBy: doneBy, Note: "ok"})
	}

	for doneBy, want := range map[string]string{
		"s_foreign": "s_foreign is not a worker you started",
		"s_plain":   "s_plain is not a worker you started",
		"s_gone":    "unknown session s_gone",
	} {
		if _, err := report(doneBy); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("done_by %s: %v, want %q", doneBy, err, want)
		}
	}
	if got := f.frames(protocol.FrameStepReport); len(got) != 0 {
		t.Fatalf("refused reports reached the gateway: %s", got)
	}

	res, err := report("s_mine")
	if err != nil {
		t.Fatalf("done_by of my worker: %v", err)
	}
	var out protocol.StepReportResult
	if err := json.Unmarshal(res, &out); err != nil || out.Evidence.By != "s_mine" {
		t.Fatalf("report result %s: %v", res, err)
	}
	var q protocol.StepReportRequest
	_ = json.Unmarshal(f.frames(protocol.FrameStepReport)[0], &q)
	want := protocol.StepReportRequest{SessionID: sid, ChangeID: "ch_1", StepRef: protocol.StepRef{Step: protocol.StepReview}, Result: protocol.StepResultPass,
		Commit: commit, Fields: map[string]string{protocol.FieldFindingsOpen: "0"}, DoneBy: "s_mine", Note: "ok"}
	if deliveryJSON(q) != deliveryJSON(want) {
		t.Fatalf("step.report\n got %s\nwant %s", deliveryJSON(q), deliveryJSON(want))
	}

	// Without done_by, or with the reporter itself, the reporter did the work.
	for _, doneBy := range []string{"", sid} {
		if _, err := report(doneBy); err != nil {
			t.Fatalf("done_by %q: %v", doneBy, err)
		}
		sent := f.frames(protocol.FrameStepReport)
		q = protocol.StepReportRequest{}
		_ = json.Unmarshal(sent[len(sent)-1], &q)
		if q.DoneBy != "" || q.SessionID != sid {
			t.Fatalf("done_by %q sent %s", doneBy, sent[len(sent)-1])
		}
	}
}

// changes_find passes its filters to change.list, with caller "me" as the
// caller's own session, and reads one change with change.get.
func TestChangesFindPassesFilters(t *testing.T) {
	n, f := startDeliveryNode(t)
	_, wt := deliveryWorktree(t)
	lc, sid := helloDelivery(t, n, "np-find", wt, "claude-opus-5-5")
	c := protocol.Change{ID: "ch_2", Project: "demo", Branch: "feat/x", HeadCommit: strings.Repeat("a", 40), Revision: 1,
		Origin: protocol.ChangeOrigin{Kind: protocol.OriginSession, SessionID: sid}, State: protocol.ChangeMerged, CreatedAt: 1}
	f.answer(protocol.FrameChangeList, func(json.RawMessage) (any, error) {
		return protocol.ChangeListResult{Changes: []protocol.ChangeView{{Change: c}}, Next: "ch_2"}, nil
	})
	f.answer(protocol.FrameChangeGet, func(json.RawMessage) (any, error) {
		return deliveryTestView(c), nil
	})

	res := lc.mustCall(protocol.OpChangesFind, protocol.ChangesFindArgs{Project: "demo", State: protocol.ChangeMerged, Caller: protocol.ChangesCallerMe, Limit: 5, Cursor: "ch_9"})
	var q protocol.ChangeListRequest
	_ = json.Unmarshal(f.frames(protocol.FrameChangeList)[0], &q)
	if want := (protocol.ChangeListRequest{Project: "demo", State: protocol.ChangeMerged, Caller: sid, Limit: 5, After: "ch_9"}); q != want {
		t.Fatalf("change.list %+v, want %+v", q, want)
	}
	var out protocol.ChangesFindResult
	if err := json.Unmarshal(res, &out); err != nil || len(out.Items) != 1 || out.Items[0].Change.ID != "ch_2" || out.NextCursor != "ch_2" || out.Total != 1 {
		t.Fatalf("changes_find result %s: %v", res, err)
	}
	if out.Items[0].Steps == nil {
		t.Fatalf("steps must be a list, even an empty one: %s", res)
	}

	// caller anyone sends no caller; the default state is the gateway's.
	lc.mustCall(protocol.OpChangesFind, protocol.ChangesFindArgs{Caller: protocol.ChangesCallerAnyone})
	sent := f.frames(protocol.FrameChangeList)
	q = protocol.ChangeListRequest{}
	_ = json.Unmarshal(sent[len(sent)-1], &q)
	if q != (protocol.ChangeListRequest{}) {
		t.Fatalf("change.list for anyone: %+v", q)
	}

	res = lc.mustCall(protocol.OpChangesFind, protocol.ChangesFindArgs{Change: "ch_2"})
	var g protocol.ChangeGetRequest
	_ = json.Unmarshal(f.frames(protocol.FrameChangeGet)[0], &g)
	out = protocol.ChangesFindResult{}
	if err := json.Unmarshal(res, &out); err != nil || g.ChangeID != "ch_2" || len(out.Items) != 1 || out.Total != 1 || out.NextCursor != "" {
		t.Fatalf("changes_find of one change: sent %+v, got %s: %v", g, res, err)
	}

	if _, err := lc.call(protocol.OpChangesFind, protocol.ChangesFindArgs{Caller: "you"}); err == nil || !strings.Contains(err.Error(), "caller") {
		t.Fatalf("bad caller: %v", err)
	}
}

// An error of the gateway reaches the agent as it is, and a gateway without
// the delivery frames is named as too old.
func TestDeliveryGatewayErrorsReachTheAgent(t *testing.T) {
	n, f := startDeliveryNode(t)
	_, wt := deliveryWorktree(t)
	lc, _ := helloDelivery(t, n, "np-errors", wt, "claude-opus-5-5")
	commit := gitHead(t, wt, "HEAD")
	f.answer(protocol.FrameStepReport, func(json.RawMessage) (any, error) {
		return nil, errors.New("commit " + commit[:8] + " is not the head of change ch_1 (revision 2 is at bbbbbbbb)")
	})
	f.answer(protocol.FrameChangeSubmit, func(json.RawMessage) (any, error) {
		return nil, errors.New(`unknown frame type "change.submit"`)
	})
	_, err := lc.call(protocol.OpStepReport, protocol.StepReportArgs{Change: "ch_1", StepRef: protocol.StepRef{Step: protocol.StepTest}, Result: protocol.StepResultPass, Commit: commit})
	if err == nil || !strings.Contains(err.Error(), "is not the head of change ch_1 (revision 2 is at bbbbbbbb)") {
		t.Fatalf("gateway refusal: %v", err)
	}
	_, err = lc.call(protocol.OpChangeSubmit, protocol.ChangeSubmitArgs{Branch: "feat/x"})
	if err == nil || !strings.Contains(err.Error(), "the gateway does not know the delivery pipeline yet") {
		t.Fatalf("old gateway: %v", err)
	}
	// A report the contract refuses never reaches the gateway.
	_, err = lc.call(protocol.OpStepReport, protocol.StepReportArgs{Change: "ch_1", StepRef: protocol.StepRef{Step: protocol.StepTest}, Result: protocol.StepResultPass, Commit: commit,
		Fields: map[string]string{protocol.FieldIndependent: "family"}})
	if err == nil || !strings.Contains(err.Error(), "independent is set by agent-net") {
		t.Fatalf("report with independent: %v", err)
	}
	if got := f.frames(protocol.FrameStepReport); len(got) != 1 {
		t.Fatalf("step.report sent %d times, want 1", len(got))
	}
}

func deliveryJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// The owner's TUI reads changes through changes_owner_find as the "cli"
// client, without a session. It sees every change (caller anyone), reads one change,
// and may not ask for "me" (the owner has no session whose changes those are).
// The write methods stay closed to it.
func TestChangesFindForTheOwnersTUI(t *testing.T) {
	n, f := startDeliveryNode(t)
	c := protocol.Change{ID: "ch_7", Project: "demo", Branch: "feat/x", HeadCommit: strings.Repeat("a", 40), Revision: 1,
		Origin: protocol.ChangeOrigin{Kind: protocol.OriginSession, SessionID: "s_other"}, State: protocol.ChangeOpen, CreatedAt: 1}
	f.answer(protocol.FrameChangeList, func(json.RawMessage) (any, error) {
		return protocol.ChangeListResult{Changes: []protocol.ChangeView{{Change: c}}, Next: "ch_7"}, nil
	})
	f.answer(protocol.FrameChangeGet, func(json.RawMessage) (any, error) { return deliveryTestView(c), nil })

	cli := dialLocal(t, n.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	res := cli.mustCall(protocol.OpChangesOwnerFind, protocol.ChangesFindArgs{State: protocol.ChangeListActive, Limit: 50, Cursor: "ch_9"})
	var out protocol.ChangesFindResult
	if err := json.Unmarshal(res, &out); err != nil || len(out.Items) != 1 || out.Items[0].Change.ID != "ch_7" || out.NextCursor != "ch_7" {
		t.Fatalf("owner list %s: %v", res, err)
	}
	var q protocol.ChangeListRequest
	_ = json.Unmarshal(f.frames(protocol.FrameChangeList)[0], &q)
	if want := (protocol.ChangeListRequest{State: protocol.ChangeListActive, Limit: 50, After: "ch_9"}); q != want {
		t.Fatalf("change.list %+v, want %+v", q, want)
	}
	res = cli.mustCall(protocol.OpChangesOwnerFind, protocol.ChangesFindArgs{Change: "ch_7"})
	out = protocol.ChangesFindResult{}
	if err := json.Unmarshal(res, &out); err != nil || len(out.Items) != 1 || out.Items[0].Change.ID != "ch_7" {
		t.Fatalf("owner get %s: %v", res, err)
	}
	if _, err := cli.call(protocol.OpChangesOwnerFind, protocol.ChangesFindArgs{Caller: protocol.ChangesCallerMe}); err == nil || !strings.Contains(err.Error(), "caller") {
		t.Fatalf("owner asked for me: %v", err)
	}
	// The owner method is not for agents, and the agent method stays closed
	// to the TUI's client.
	lc, _ := helloDelivery(t, n, "np-owner-find", t.TempDir(), "claude-opus-5-5")
	if _, err := lc.call(protocol.OpChangesOwnerFind, protocol.ChangesFindArgs{}); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
		t.Fatalf("an agent session called the owner read: %v", err)
	}
	for _, method := range []string{protocol.OpChangeSubmit, protocol.OpStepClaim, protocol.OpStepReport, protocol.OpChangesFind} {
		if _, err := cli.call(method, map[string]any{}); err == nil || !strings.Contains(err.Error(), "not available to cli clients") {
			t.Fatalf("%s from the owner's TUI: %v", method, err)
		}
	}
}

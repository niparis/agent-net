package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

var (
	commitBase   = strings.Repeat("0", 40)
	commitA      = strings.Repeat("a", 40)
	commitB      = strings.Repeat("b", 40)
	commitC      = strings.Repeat("c", 40)
	commitMerged = strings.Repeat("d", 40)
)

// deliveryRig is a gateway with two machines. On machine a: the caller
// (Opus), the worker it started (Opus), a Sonnet session. On machine b: a
// DeepSeek session and a GPT review worker the caller started there.
type deliveryRig struct {
	t                      *testing.T
	s                      *Store
	g                      *Gateway
	a, b                   string
	caller, worker, sonnet string
	deepseek, reviewer     string
}

func newDeliveryRig(t *testing.T) *deliveryRig {
	t.Helper()
	s, _ := newTestStore(t)
	return deliveryRigOn(t, s)
}

func deliveryRigOn(t *testing.T, s *Store) *deliveryRig {
	t.Helper()
	r := &deliveryRig{t: t, s: s, g: &Gateway{store: s}}
	var err error
	if r.a, _, err = s.CreateNode("ha", "linux"); err != nil {
		t.Fatal(err)
	}
	if r.b, _, err = s.CreateNode("hb", "linux"); err != nil {
		t.Fatal(err)
	}
	demo := protocol.RepoInfo{Root: "/code/demo", Project: "demo"}
	session := func(node string, info protocol.SessionInfo) string {
		id, err := s.UpsertSession(node, info)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	r.caller = session(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "caller", Model: "claude-opus-5-5", Repo: demo})
	r.worker = session(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "worker", Model: "claude-opus-5-5", Repo: demo,
		Worker: &protocol.WorkerInfo{ID: "w_1", Owner: r.caller}})
	r.sonnet = session(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "sonnet", Model: "claude-sonnet-5-5", Repo: demo})
	r.deepseek = session(r.b, protocol.SessionInfo{Harness: "opencode", NativeID: "deepseek", Model: "deepseek/deepseek-v4-pro", Repo: demo})
	r.reviewer = session(r.b, protocol.SessionInfo{Harness: "codex", NativeID: "reviewer", Model: "gpt-5.5", Repo: demo,
		Worker: &protocol.WorkerInfo{ID: "w_2", Owner: r.caller}})
	return r
}

// call sends one frame from node and decodes the answer as the daemon would.
func deliveryCall[T any](r *deliveryRig, node, typ string, req any) (T, error) {
	r.t.Helper()
	var out T
	raw, err := json.Marshal(req)
	if err != nil {
		r.t.Fatal(err)
	}
	data, err := r.g.handle(context.Background(), &nodeConn{id: node}, typ, raw)
	if err != nil {
		return out, err
	}
	b, err := json.Marshal(data)
	if err != nil {
		r.t.Fatal(err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		r.t.Fatal(err)
	}
	return out, nil
}

// workerSubmit is the daemon's submit at the worker's complete hand-back.
func (r *deliveryRig) workerSubmit(branch, head string) protocol.ChangeSubmitRequest {
	return protocol.ChangeSubmitRequest{SessionID: r.caller, Project: "demo", Branch: branch, TargetBranch: "main", BaseCommit: commitBase, HeadCommit: head,
		Origin: protocol.ChangeOrigin{Kind: protocol.OriginWorker, WorkerID: "w_1", SessionID: r.worker}, Title: "Fix the login"}
}

func (r *deliveryRig) submit(node string, q protocol.ChangeSubmitRequest) protocol.ChangeSubmitResult {
	r.t.Helper()
	res, err := deliveryCall[protocol.ChangeSubmitResult](r, node, protocol.FrameChangeSubmit, q)
	if err != nil {
		r.t.Fatalf("submit %s %s: %v", q.Branch, q.HeadCommit[:4], err)
	}
	return res
}

func (r *deliveryRig) report(node string, q protocol.StepReportRequest) (protocol.StepReportResult, error) {
	r.t.Helper()
	return deliveryCall[protocol.StepReportResult](r, node, protocol.FrameStepReport, q)
}

func (r *deliveryRig) mustReport(node string, q protocol.StepReportRequest) protocol.StepReportResult {
	r.t.Helper()
	res, err := r.report(node, q)
	if err != nil {
		r.t.Fatalf("report %s %s: %v", q.StepRef, q.Result, err)
	}
	return res
}

func (r *deliveryRig) get(id string) protocol.ChangeView {
	r.t.Helper()
	v, err := deliveryCall[protocol.ChangeView](r, r.a, protocol.FrameChangeGet, protocol.ChangeGetRequest{ChangeID: id})
	if err != nil {
		r.t.Fatalf("get %s: %v", id, err)
	}
	return v
}

func (r *deliveryRig) list(q protocol.ChangeListRequest) protocol.ChangeListResult {
	r.t.Helper()
	res, err := deliveryCall[protocol.ChangeListResult](r, r.a, protocol.FrameChangeList, q)
	if err != nil {
		r.t.Fatalf("list %+v: %v", q, err)
	}
	return res
}

var stepAccept = protocol.StepRef{Step: protocol.StepAccept}

func TestChangeSubmitCreatesChange(t *testing.T) {
	r := newDeliveryRig(t)
	q := r.workerSubmit("agent-net/w_1", commitA)
	q.Uncommitted = true
	q.Difficulty = "hard"
	res := r.submit(r.a, q)
	c := res.Change.Change
	if !strings.HasPrefix(c.ID, protocol.ChangeIDPrefix) || len(c.ID) <= len(protocol.ChangeIDPrefix) {
		t.Fatalf("id %q", c.ID)
	}
	if res.Bumped || c.Revision != 1 || c.State != protocol.ChangeOpen || c.HeadCommit != commitA || c.Branch != "agent-net/w_1" {
		t.Fatalf("change %+v bumped %v", c, res.Bumped)
	}
	if c.Machine != r.a || c.Caller != r.caller || !c.Uncommitted || c.Difficulty != "hard" || c.TargetBranch != "main" || c.BaseCommit != commitBase || c.Title != "Fix the login" || c.CreatedAt == 0 {
		t.Fatalf("change fields %+v", c)
	}
	// The gateway fills the author's model and maker from the worker's session.
	if c.Origin.Model != "claude-opus-5-5" || c.Origin.ModelFamily != "anthropic" || c.Origin.WorkerID != "w_1" || c.Origin.SessionID != r.worker {
		t.Fatalf("origin %+v", c.Origin)
	}
	if len(res.Change.Steps) == 0 || res.Change.Pipeline != nil || res.Change.Evidence != nil {
		t.Fatalf("submit view %+v", res.Change)
	}
	v := r.get(c.ID)
	if v.Change != c {
		t.Fatalf("get %+v, submit %+v", v.Change, c)
	}
	if len(v.Revisions) != 1 || v.Revisions[0] != (protocol.Revision{ChangeID: c.ID, Number: 1, Branch: "agent-net/w_1", Commit: commitA, Uncommitted: true, At: c.CreatedAt}) {
		t.Fatalf("revisions %+v", v.Revisions)
	}
	// With the default settings the pipeline is the owner's way today.
	want, _ := json.Marshal(protocol.PipelineToday())
	got, _ := json.Marshal(v.Pipeline)
	if string(got) != string(want) {
		t.Fatalf("pipeline\n got %s\nwant %s", got, want)
	}
	// The change is hard, so review is required (decision 2) and may start too.
	if len(v.Next) != 2 || v.Next[0] != stepAccept || v.Next[1] != (protocol.StepRef{Step: protocol.StepReview}) {
		t.Fatalf("next %+v", v.Next)
	}
	if _, err := deliveryCall[protocol.ChangeView](r, r.a, protocol.FrameChangeGet, protocol.ChangeGetRequest{ChangeID: "ch_missing"}); err == nil {
		t.Fatal("got an unknown change")
	}
}

// Until step owners are stored, a change's pipeline is the one its project's
// delivery settings describe, with the owner's steps after merge.
func TestChangePipelineFollowsDeliverySettings(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.db.Exec(`INSERT INTO graph_nodes(name,kind,created_at) VALUES('demo','component',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.WriteSettings([]SettingsChange{
		{Key: "delivery.merge_authority", Level: "network", Scope: "network", Value: settingValue(`"delegated"`)},
		{Key: "delivery.deploy_rule", Level: "repo", Scope: "demo", Value: settingValue(`"owner"`)},
	}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	demo := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	web := r.workerSubmit("agent-net/w_1", commitA)
	web.Project = "web"
	webID := r.submit(r.a, web).Change.Change.ID

	want, err := protocol.PipelineFromShorthand(protocol.TestsSuite, protocol.MergeDelegated, protocol.DeployOwner)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range protocol.PipelineToday().Steps {
		if s.AfterMerge {
			want.Steps = append(want.Steps, s)
		}
	}
	got := r.get(demo).Pipeline
	if a, b := string(mustJSON(t, got)), string(mustJSON(t, want)); a != b {
		t.Fatalf("demo pipeline\n got %s\nwant %s", a, b)
	}
	if tests, merge, deploy := got.Shorthand(); tests != protocol.TestsSuite || merge != protocol.MergeDelegated || deploy != protocol.DeployOwner {
		t.Fatalf("shorthand %s %s %s", tests, merge, deploy)
	}
	if _, has := r.get(webID).Pipeline.Find(protocol.StepRef{Step: protocol.StepDeploy, AfterMerge: true}); has {
		t.Fatal("a project with no graph node got the repo's deploy step")
	}
	// The owner can approve directly in the caller's session under delegated.
	approve := protocol.StepReportRequest{SessionID: r.caller, ChangeID: demo, StepRef: protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: commitA, DoneBy: protocol.EvidenceByOwner}
	if res := r.mustReport(r.a, approve); res.Evidence.Step != protocol.StepApprove || res.Evidence.For != protocol.StepMerge {
		t.Fatalf("approve %+v", res.Evidence)
	}
}

func TestChangeSubmitChecksTheMachine(t *testing.T) {
	r := newDeliveryRig(t)
	// The caller lives on machine a: machine b cannot submit for it.
	q := protocol.ChangeSubmitRequest{SessionID: r.caller, Project: "demo", Branch: "fix", HeadCommit: commitA, Origin: protocol.ChangeOrigin{Kind: protocol.OriginSession, SessionID: r.caller}}
	if _, err := deliveryCall[protocol.ChangeSubmitResult](r, r.b, protocol.FrameChangeSubmit, q); err == nil {
		t.Fatal("machine b submitted for a session of machine a")
	}
	// A session submits its own work only.
	q.Origin.SessionID = r.sonnet
	if _, err := deliveryCall[protocol.ChangeSubmitResult](r, r.a, protocol.FrameChangeSubmit, q); err == nil {
		t.Fatal("a session submitted work as another session")
	}
	// A worker on machine b, started by the caller on machine a, hands back:
	// the change is on machine b, where the branch is.
	w := protocol.ChangeSubmitRequest{SessionID: r.caller, Project: "demo", Branch: "agent-net/w_2", HeadCommit: commitA, Origin: protocol.ChangeOrigin{Kind: protocol.OriginWorker, WorkerID: "w_2", SessionID: r.reviewer}}
	res := r.submit(r.b, w)
	if res.Change.Change.Machine != r.b || res.Change.Change.Caller != r.caller || res.Change.Change.Origin.ModelFamily != "openai" {
		t.Fatalf("change %+v", res.Change.Change)
	}
	// Machine b cannot claim a worker that the caller did not start.
	w.Origin = protocol.ChangeOrigin{Kind: protocol.OriginWorker, WorkerID: "w_9", SessionID: r.deepseek}
	w.Branch = "agent-net/w_9"
	if _, err := deliveryCall[protocol.ChangeSubmitResult](r, r.b, protocol.FrameChangeSubmit, w); err == nil {
		t.Fatal("machine b submitted for the caller with a worker the caller did not start")
	}
	// A bad request is refused before anything is stored.
	bad := r.workerSubmit("agent-net/w_1", "abc")
	if _, err := deliveryCall[protocol.ChangeSubmitResult](r, r.a, protocol.FrameChangeSubmit, bad); err == nil {
		t.Fatal("accepted a short commit")
	}
	if got := r.list(protocol.ChangeListRequest{State: protocol.ChangeListAny}); len(got.Changes) != 1 {
		t.Fatalf("stored %d changes, want 1", len(got.Changes))
	}
}

func TestChangeSubmitSameBranchReusesChange(t *testing.T) {
	r := newDeliveryRig(t)
	first := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change
	again := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitB)).Change.Change
	if again.ID != first.ID {
		t.Fatalf("same branch made a new change: %s then %s", first.ID, again.ID)
	}
	other := r.submit(r.a, r.workerSubmit("agent-net/w_3", commitC)).Change.Change
	if other.ID == first.ID {
		t.Fatal("another branch reused the change")
	}
	// The same project and branch name in another project is another change.
	web := r.workerSubmit("agent-net/w_1", commitA)
	web.Project = "web"
	if r.submit(r.a, web).Change.Change.ID == first.ID {
		t.Fatal("another project reused the change")
	}
	// change_id moves the change to a new branch (continue); then that branch
	// holds it, and the old branch is free for a new change.
	cont := r.workerSubmit("agent-net/w_4", commitB)
	cont.ChangeID = first.ID
	moved := r.submit(r.a, cont).Change.Change
	if moved.ID != first.ID || moved.Branch != "agent-net/w_4" {
		t.Fatalf("continue %+v", moved)
	}
	if r.submit(r.a, r.workerSubmit("agent-net/w_4", commitC)).Change.Change.ID != first.ID {
		t.Fatal("the new branch does not hold the change")
	}
	if r.submit(r.a, r.workerSubmit("agent-net/w_1", commitC)).Change.Change.ID == first.ID {
		t.Fatal("the old branch still holds the change")
	}
	// change_id must name a known change of the same project, and may not take
	// a branch that another open change holds.
	for _, edit := range []func(*protocol.ChangeSubmitRequest){
		func(q *protocol.ChangeSubmitRequest) { q.ChangeID = "ch_missing" },
		func(q *protocol.ChangeSubmitRequest) { q.ChangeID = first.ID; q.Project = "web" },
		func(q *protocol.ChangeSubmitRequest) { q.ChangeID = first.ID; q.Branch = "agent-net/w_3" },
	} {
		q := r.workerSubmit("agent-net/w_5", commitA)
		edit(&q)
		if _, err := deliveryCall[protocol.ChangeSubmitResult](r, r.a, protocol.FrameChangeSubmit, q); err == nil {
			t.Errorf("accepted %+v", q)
		}
	}
}

func TestChangeSubmitNewHeadBumpsRevision(t *testing.T) {
	r := newDeliveryRig(t)
	first := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change
	q := r.workerSubmit("agent-net/w_1", commitB)
	q.Uncommitted = true
	res := r.submit(r.a, q)
	c := res.Change.Change
	if !res.Bumped || c.Revision != 2 || c.HeadCommit != commitB || !c.Uncommitted || c.ID != first.ID {
		t.Fatalf("bump %+v bumped %v", c, res.Bumped)
	}
	v := r.get(c.ID)
	if len(v.Revisions) != 2 || v.Revisions[0].Commit != commitA || v.Revisions[0].Uncommitted || v.Revisions[1] != (protocol.Revision{ChangeID: c.ID, Number: 2, Branch: "agent-net/w_1", Commit: commitB, Uncommitted: true, At: c.UpdatedAt}) {
		t.Fatalf("revisions %+v", v.Revisions)
	}
	// A continue by another worker makes it the author of the new revision.
	cont := protocol.ChangeSubmitRequest{SessionID: r.caller, ChangeID: c.ID, Project: "demo", Branch: "agent-net/w_2", HeadCommit: commitC,
		Origin: protocol.ChangeOrigin{Kind: protocol.OriginWorker, WorkerID: "w_2", SessionID: r.reviewer}}
	c = r.submit(r.b, cont).Change.Change
	if c.Revision != 3 || c.Origin.SessionID != r.reviewer || c.Origin.ModelFamily != "openai" || c.Machine != r.b || c.Caller != r.caller || c.Title != "Fix the login" || c.BaseCommit != commitBase {
		t.Fatalf("continue %+v", c)
	}
}

func TestChangeSubmitSameHeadKeepsRevision(t *testing.T) {
	r := newDeliveryRig(t)
	first := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change
	res := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA))
	if res.Bumped || res.Change.Change.Revision != 1 {
		t.Fatalf("same head bumped: %+v", res)
	}
	// A continue with no new commit moves the branch but keeps the revision,
	// so its evidence still counts.
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: first.ID, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	cont := r.workerSubmit("agent-net/w_4", commitA)
	cont.ChangeID = first.ID
	res = r.submit(r.a, cont)
	c := res.Change.Change
	if res.Bumped || c.Revision != 1 || c.Branch != "agent-net/w_4" {
		t.Fatalf("continue %+v bumped %v", c, res.Bumped)
	}
	if res.Change.Steps[0].State != protocol.StepStatePassed {
		t.Fatalf("accept is %s after a continue with the same head", res.Change.Steps[0].State)
	}
	if v := r.get(c.ID); len(v.Revisions) != 1 || v.Revisions[0].Branch != "agent-net/w_1" {
		t.Fatalf("revisions %+v", v.Revisions)
	}
}

func TestStepReportOnOldRevisionRefused(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	r.submit(r.a, r.workerSubmit("agent-net/w_1", commitB))
	_, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	if err == nil || !strings.Contains(err.Error(), "not the head") {
		t.Fatalf("report on revision 1 after revision 2: %v", err)
	}
	v := r.get(id)
	if len(v.Evidence) != 1 || v.Evidence[0].Revision != 1 {
		t.Fatalf("evidence %+v", v.Evidence)
	}
	// The old pass does not count for revision 2.
	if v.Steps[0].State != protocol.StepStateReady {
		t.Fatalf("accept is %s on revision 2", v.Steps[0].State)
	}
	res := r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitB})
	if res.Evidence.Revision != 2 || res.Change.Steps[0].State != protocol.StepStatePassed {
		t.Fatalf("report on revision 2: %+v", res)
	}
}

func TestStepReportOnAnotherCommitRefused(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	for name, q := range map[string]protocol.StepReportRequest{
		"another commit":       {SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitC},
		"a short commit":       {SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: "aaaa"},
		"a step left out":      {SessionID: r.caller, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: commitA},
		"an after-merge step":  {SessionID: r.caller, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepRelease, AfterMerge: true}, Result: protocol.StepResultPass, Commit: commitA},
		"an unknown change":    {SessionID: r.caller, ChangeID: "ch_missing", StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA},
		"independent claimed":  {SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA, Fields: map[string]string{protocol.FieldIndependent: "family"}},
		"a merge pass no hash": {SessionID: r.caller, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: commitA},
	} {
		if _, err := r.report(r.a, q); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The reporting session must be on the sending machine.
	if _, err := r.report(r.b, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA}); err == nil {
		t.Error("machine b reported for a session of machine a")
	}
	if v := r.get(id); len(v.Evidence) != 0 {
		t.Fatalf("stored refused evidence: %+v", v.Evidence)
	}
}

func TestStepReportIndependenceByMaker(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	report := func(node, session, doneBy string) protocol.Evidence {
		t.Helper()
		return r.mustReport(node, protocol.StepReportRequest{SessionID: session, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepReview}, Result: protocol.StepResultPass, Commit: commitA,
			Fields: map[string]string{protocol.FieldFindingsOpen: "0"}, DoneBy: doneBy, Note: "looks right"}).Evidence
	}
	for _, tc := range []struct {
		name, node, session, doneBy string
		by, reportedBy, model       string
		family, independent         string
	}{
		{"the author", r.a, r.caller, r.worker, r.worker, r.caller, "claude-opus-5-5", "anthropic", protocol.IndependentNone},
		{"the reporter itself as done_by", r.a, r.caller, r.caller, r.caller, "", "claude-opus-5-5", "anthropic", protocol.IndependentSession},
		{"a review worker of the caller", r.a, r.caller, r.reviewer, r.reviewer, r.caller, "gpt-5.5", "openai", protocol.IndependentFamily},
	} {
		e := report(tc.node, tc.session, tc.doneBy)
		if e.By != tc.by || e.ReportedBy != tc.reportedBy || e.ByModel != tc.model || e.ByModelFamily != tc.family || e.Fields[protocol.FieldIndependent] != tc.independent {
			t.Errorf("%s: evidence %+v", tc.name, e)
		}
		if e.Revision != 1 || e.Commit != commitA || e.Note != "looks right" || e.Fields[protocol.FieldFindingsOpen] != "0" || e.At == 0 {
			t.Errorf("%s: evidence %+v", tc.name, e)
		}
	}
	// Unrelated sessions cannot be named as done_by; test their classification
	// with the same author and session metadata directly.
	origin := r.get(id).Change.Origin
	for _, tc := range []struct{ name, session, model, family, independent string }{
		{"another Anthropic model", r.sonnet, "claude-sonnet-5-5", "anthropic", protocol.IndependentSession},
		{"another maker", r.deepseek, "deepseek/deepseek-v4-pro", "deepseek", protocol.IndependentFamily},
	} {
		maker, ok, err := r.s.deliverySession(tc.session)
		if err != nil || !ok || maker.model != tc.model || maker.family() != tc.family {
			t.Fatalf("%s: maker %+v, %v", tc.name, maker, err)
		}
		if got := protocol.IndependenceOf(origin, tc.session, maker.family()); got != tc.independent {
			t.Errorf("%s: independence %s, want %s", tc.name, got, tc.independent)
		}
	}
	// done_by must be a worker the reporter started.
	for _, doneBy := range []string{r.deepseek, r.worker + "x"} {
		if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.sonnet, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepReview}, Result: protocol.StepResultPass, Commit: commitA, DoneBy: doneBy}); err == nil {
			t.Errorf("done_by %s accepted for a worker the reporter did not start", doneBy)
		}
	}
	// The review step needs another maker: only the latest such pass counts.
	v := r.get(id)
	if len(v.Evidence) != 3 {
		t.Fatalf("evidence %d, want 3", len(v.Evidence))
	}
	for _, st := range v.Steps {
		if st.Step == protocol.StepReview && (st.State != protocol.StepStatePassed || st.Evidence == nil || st.Evidence.Fields[protocol.FieldIndependent] != protocol.IndependentFamily) {
			t.Fatalf("review %+v", st)
		}
	}
}

func TestStepReportMovesChangeThroughMerge(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	pass := func(ref protocol.StepRef, commit string, fields map[string]string) protocol.Change {
		t.Helper()
		return r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: ref, Result: protocol.StepResultPass, Commit: commit, Fields: fields}).Change.Change
	}
	merge := protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: commitA, Fields: map[string]string{protocol.FieldMergedCommit: commitMerged}}
	if _, err := r.report(r.a, merge); err == nil || !strings.Contains(err.Error(), "accept: no pass") || !strings.Contains(err.Error(), "test(suite=*): no pass") {
		t.Fatalf("premature merge: %v", err)
	}
	if got := r.get(id).Change.State; got != protocol.ChangeOpen {
		t.Fatalf("premature merge changed state to %s", got)
	}
	pass(stepAccept, commitA, nil)
	pass(protocol.StepRef{Step: protocol.StepTest}, commitA, map[string]string{protocol.FieldSuite: protocol.SuiteChanged})
	c := pass(protocol.StepRef{Step: protocol.StepMerge}, commitA, map[string]string{protocol.FieldMergedCommit: commitMerged})
	if c.State != protocol.ChangeMerged || c.MergedCommit != commitMerged {
		t.Fatalf("after merge %+v", c)
	}
	// A merged change takes no new head: the branch starts a new change.
	if again := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitB)).Change.Change; again.ID == id {
		t.Fatal("a merged change took a new head")
	}
	ci := protocol.StepRef{Step: protocol.StepTest, AfterMerge: true}
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: ci, Result: protocol.StepResultPass, Commit: commitA, Fields: map[string]string{protocol.FieldSuite: protocol.SuiteFull}}); err == nil {
		t.Fatal("an after-merge test on the head, not the merged commit, was accepted")
	}
	pass(ci, commitMerged, map[string]string{protocol.FieldSuite: protocol.SuiteFull})
	if c = pass(protocol.StepRef{Step: protocol.StepRelease, AfterMerge: true}, commitMerged, map[string]string{protocol.FieldTag: "v1.2.3"}); c.State != protocol.ChangeMerged {
		t.Fatalf("after release %+v", c)
	}
	if c = pass(protocol.StepRef{Step: protocol.StepVerify, AfterMerge: true}, commitMerged, nil); c.State != protocol.ChangeDone {
		t.Fatalf("after verify %+v", c)
	}
}

func TestStepReportCloseStopsChange(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	res := r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultClose, Commit: commitA, Note: "not wanted"})
	if res.Change.Change.State != protocol.ChangeStopped || len(res.Change.Next) != 0 {
		t.Fatalf("after close %+v", res.Change)
	}
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA}); err == nil {
		t.Fatal("a stopped change took a report")
	}
	if again := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change; again.ID == id || again.Revision != 1 {
		t.Fatalf("submit after close %+v", again)
	}
}

func TestChangeListFiltersAndPages(t *testing.T) {
	r := newDeliveryRig(t)
	sub := func(project, branch string) string {
		q := r.workerSubmit(branch, commitA)
		q.Project = project
		return r.submit(r.a, q).Change.Change.ID
	}
	c1 := sub("demo", "b1")
	c2 := sub("demo", "b2")
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c2, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c2, StepRef: protocol.StepRef{Step: protocol.StepTest}, Result: protocol.StepResultPass, Commit: commitA, Fields: map[string]string{protocol.FieldSuite: protocol.SuiteChanged}})
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c2, StepRef: protocol.StepRef{Step: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: commitA, Fields: map[string]string{protocol.FieldMergedCommit: commitMerged}})
	c3 := sub("demo", "b3")
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c3, StepRef: stepAccept, Result: protocol.StepResultClose, Commit: commitA})
	c4 := r.submit(r.b, protocol.ChangeSubmitRequest{SessionID: r.deepseek, Project: "web", Branch: "b1", HeadCommit: commitA, Origin: protocol.ChangeOrigin{Kind: protocol.OriginSession, SessionID: r.deepseek}}).Change.Change.ID
	c5 := sub("demo", "b5")

	ids := func(res protocol.ChangeListResult) string {
		var out []string
		for _, v := range res.Changes {
			out = append(out, v.Change.ID)
		}
		return strings.Join(out, " ")
	}
	for _, tc := range []struct {
		q    protocol.ChangeListRequest
		want []string
	}{
		{protocol.ChangeListRequest{}, []string{c5, c4, c2, c1}},
		{protocol.ChangeListRequest{State: protocol.ChangeListActive}, []string{c5, c4, c2, c1}},
		{protocol.ChangeListRequest{State: protocol.ChangeOpen}, []string{c5, c4, c1}},
		{protocol.ChangeListRequest{State: protocol.ChangeMerged}, []string{c2}},
		{protocol.ChangeListRequest{State: protocol.ChangeStopped}, []string{c3}},
		{protocol.ChangeListRequest{State: protocol.ChangeDone}, nil},
		{protocol.ChangeListRequest{State: protocol.ChangeListAny}, []string{c5, c4, c3, c2, c1}},
		{protocol.ChangeListRequest{Project: "demo"}, []string{c5, c2, c1}},
		{protocol.ChangeListRequest{Project: "web", State: protocol.ChangeOpen}, []string{c4}},
		{protocol.ChangeListRequest{Caller: r.deepseek, State: protocol.ChangeListAny}, []string{c4}},
		{protocol.ChangeListRequest{Project: "none"}, nil},
	} {
		res := r.list(tc.q)
		if got := ids(res); got != strings.Join(tc.want, " ") || res.Next != "" {
			t.Errorf("list %+v: %q next %q, want %q", tc.q, got, res.Next, strings.Join(tc.want, " "))
		}
		if tc.want == nil && res.Changes == nil {
			t.Errorf("list %+v: changes is null, want []", tc.q)
		}
	}
	// A page holds views with steps, not the full change.
	page := r.list(protocol.ChangeListRequest{State: protocol.ChangeListAny, Limit: 2})
	if ids(page) != c5+" "+c4 || page.Next != c4 {
		t.Fatalf("page 1: %q next %q", ids(page), page.Next)
	}
	if v := page.Changes[0]; len(v.Steps) == 0 || v.Pipeline != nil || v.Revisions != nil || v.Evidence != nil {
		t.Fatalf("list view %+v", v)
	}
	page = r.list(protocol.ChangeListRequest{State: protocol.ChangeListAny, Limit: 2, After: page.Next})
	if ids(page) != c3+" "+c2 || page.Next != c2 {
		t.Fatalf("page 2: %q next %q", ids(page), page.Next)
	}
	page = r.list(protocol.ChangeListRequest{State: protocol.ChangeListAny, Limit: 2, After: page.Next})
	if ids(page) != c1 || page.Next != "" {
		t.Fatalf("page 3: %q next %q", ids(page), page.Next)
	}
	// A page is never larger than 100.
	for i := 0; i < 102; i++ {
		sub("many", "b"+strings.Repeat("x", i+1))
	}
	if page = r.list(protocol.ChangeListRequest{Project: "many", Limit: 1000}); len(page.Changes) != 100 || page.Next == "" {
		t.Fatalf("limit 1000 gave %d, next %q", len(page.Changes), page.Next)
	}
	if page = r.list(protocol.ChangeListRequest{Project: "many"}); len(page.Changes) != 25 {
		t.Fatalf("default page %d, want 25", len(page.Changes))
	}
	for _, q := range []protocol.ChangeListRequest{{State: "closed"}, {After: "ch_missing"}} {
		if _, err := deliveryCall[protocol.ChangeListResult](r, r.a, protocol.FrameChangeList, q); err == nil {
			t.Errorf("list %+v accepted", q)
		}
	}
}

func TestDeliveryChangesSurviveRestart(t *testing.T) {
	path := seededStorePath(t, "gw.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.tempFolders = []string{}
	r := deliveryRigOn(t, s)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultFail, Commit: commitA, Note: "missing test"})
	r.submit(r.a, r.workerSubmit("agent-net/w_1", commitB))
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepReview}, Result: protocol.StepResultPass, Commit: commitB, Fields: map[string]string{protocol.FieldFindingsOpen: "0"}})
	before, _ := json.Marshal(r.get(id))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	r.s, r.g = s2, &Gateway{store: s2}
	after, _ := json.Marshal(r.get(id))
	if string(before) != string(after) {
		t.Fatalf("after restart\n got %s\nwant %s", after, before)
	}
	// The open change on the branch is still found after the restart.
	if res := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitC)); res.Change.Change.ID != id || res.Change.Change.Revision != 3 {
		t.Fatalf("submit after restart %+v", res.Change.Change)
	}
}

// The migration is safe to run again on a database that has it (old upgrade
// tests replay later migrations), and it keeps the stored changes.
func TestDeliveryChangesMigrationReplays(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	if _, err := r.s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "delivery changes, revisions and evidence")); err != nil {
		t.Fatal(err)
	}
	if err := migrateOldFixture(t, r.s); err != nil {
		t.Fatal(err)
	}
	if v := r.get(id); v.Change.HeadCommit != commitA {
		t.Fatalf("change after replay %+v", v.Change)
	}
}

func TestDeliveryEvidenceIsAppendOnly(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultFail, Commit: commitA})
	for _, q := range []string{
		`UPDATE delivery_evidence SET result='pass'`,
		`UPDATE delivery_evidence SET fields='{"independent":"family"}'`,
		`DELETE FROM delivery_evidence`,
	} {
		_, err := r.s.db.Exec(q)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: %v", q, err)
		}
	}
	if v := r.get(id); len(v.Evidence) != 1 || v.Evidence[0].Result != protocol.StepResultFail {
		t.Fatalf("evidence %+v", v.Evidence)
	}
}

func TestChangeReviewRiskPersistsAndCannotBeDropped(t *testing.T) {
	r := newDeliveryRig(t)
	q := r.workerSubmit("feat/risk", commitA)
	q.Risk, q.SensitivePath = 4, "migrations"
	c := r.submit(r.a, q).Change.Change
	view, err := r.s.GetChange(c.ID)
	if err != nil || view.Change.Risk != 4 || view.Change.SensitivePath != "migrations" {
		t.Fatalf("stored change=%+v err=%v", view.Change, err)
	}
	// An older daemon or a plain submit must not drop an existing review trigger.
	q.Risk, q.SensitivePath = 0, ""
	q.HeadCommit = commitB
	c = r.submit(r.a, q).Change.Change
	if c.Risk != 4 || c.SensitivePath != "migrations" {
		t.Fatalf("resubmit dropped review triggers: %+v", c)
	}
	for _, step := range []string{protocol.StepAccept, protocol.StepTest} {
		fields := map[string]string{}
		if step == protocol.StepTest {
			fields[protocol.FieldSuite] = protocol.SuiteFull
		}
		r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: protocol.StepRef{Step: step}, Result: protocol.StepResultPass, Commit: commitB, Fields: fields})
	}
	_, err = r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: protocol.StepRef{Step: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: commitB, Fields: map[string]string{protocol.FieldMergedCommit: commitMerged}})
	if err == nil || !strings.Contains(err.Error(), "review") {
		t.Fatalf("merge without review: %v", err)
	}
}

func TestChangeReviewRiskMigrationUpgrade(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("feat/old", commitA)).Change.Change.ID
	// Simulate the schema of the previous release while retaining its change.
	for _, column := range []string{"risk", "sensitive_path"} {
		if _, err := r.s.db.Exec(`ALTER TABLE delivery_changes DROP COLUMN ` + column); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "delivery review risk columns")); err != nil {
		t.Fatal(err)
	}
	if err := migrateOldFixture(t, r.s); err != nil {
		t.Fatal(err)
	}
	view := r.get(id)
	if view.Change.Risk != 0 || view.Change.SensitivePath != "" || view.Change.HeadCommit != commitA {
		t.Fatalf("upgrade changed existing work: %+v", view.Change)
	}
}

func TestChangeReviewReasonSurvivesOfflineOwner(t *testing.T) {
	r := newDeliveryRig(t)
	q := r.workerSubmit("feat/reason", commitA)
	q.Risk = 4
	c := r.submit(r.a, q).Change.Change
	p := protocol.PipelineToday()
	for i := range p.Steps {
		if p.Steps[i].Step == protocol.StepReview {
			p.Steps[i].Owner = protocol.StepOwner{Kind: protocol.OwnerNode, Node: "offline-reviewer"}
		}
	}
	view, err := r.s.changeView(c, p, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range view.Steps {
		if st.Step == protocol.StepReview && !strings.Contains(st.Note, "required: risk 4") {
			t.Fatalf("offline owner hid review reason: %+v", st)
		}
	}
}

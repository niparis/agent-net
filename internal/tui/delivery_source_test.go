package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

func hash(c byte) string { return strings.Repeat(string(c), 40) }

func pev(change string, rev int, ref protocol.StepRef, result, commit, by, model string, at time.Time, fields map[string]string) protocol.Evidence {
	return protocol.Evidence{ChangeID: change, Revision: rev, StepRef: ref, Result: result, Commit: commit, By: by, ByModel: model, Fields: fields, At: at.Unix()}
}

func ref(step string) protocol.StepRef { return protocol.StepRef{Step: step} }

func status(r protocol.StepRef, owner protocol.StepOwner, state string, e *protocol.Evidence) protocol.StepStatus {
	return protocol.StepStatus{StepRef: r, Owner: owner, State: state, Evidence: e}
}

var (
	ownCaller = protocol.StepOwner{Kind: protocol.OwnerCaller}
	ownGitHub = protocol.StepOwner{Kind: protocol.OwnerGitHub}
	ownPerson = protocol.StepOwner{Kind: protocol.OwnerPerson}
	ownNone   = protocol.StepOwner{Kind: protocol.OwnerNone}
	ownNode   = protocol.StepOwner{Kind: protocol.OwnerNode, Node: "merger"}
)

// Each state of a step is one mark of the strip.
func TestDeliveryMarksMapEveryStepState(t *testing.T) {
	steps := []protocol.StepStatus{
		status(ref(protocol.StepAccept), ownCaller, protocol.StepStatePassed, nil),
		status(ref(protocol.StepReview), ownNode, protocol.StepStateFailed, nil),
		status(ref(protocol.StepTest), ownGitHub, protocol.StepStateReady, nil),
		status(protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}, ownNone, protocol.StepStateSkipped, nil),
		status(ref(protocol.StepMerge), ownCaller, protocol.StepStateWaiting, nil),
		status(protocol.StepRef{Step: protocol.StepRelease, AfterMerge: true}, ownCaller, protocol.StepStateNeedsOwner, nil),
		status(protocol.StepRef{Step: protocol.StepDeploy, AfterMerge: true}, ownCaller, protocol.StepStateClosed, nil),
	}
	got := deliveryMarks(steps)
	want := map[string]stripState{
		"accept": stripPassed, "review": stripFailed, "test": stripWaiting, "approve": stripNotNeeded,
		"merge": stripWaiting, "release": stripWaiting, "deploy": stripFailed, "verify": stripWaiting,
	}
	if len(got) != len(deliverySteps) {
		t.Fatalf("%d marks", len(got))
	}
	for i, step := range deliverySteps {
		if got[i] != want[step] {
			t.Errorf("%s: mark %v, want %v", step, got[i], want[step])
		}
	}
}

// A step name that sits on both sides of merge, or twice for approve, is one
// mark: a failure shows, all passed passes, some passed is under way.
func TestDeliveryMarksJoinStepsWithTheSameName(t *testing.T) {
	post := func(s string) protocol.StepRef { return protocol.StepRef{Step: s, AfterMerge: true} }
	steps := []protocol.StepStatus{
		status(ref(protocol.StepTest), ownGitHub, protocol.StepStatePassed, nil),
		status(post(protocol.StepTest), ownCaller, protocol.StepStateWaiting, nil),
		status(protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}, ownPerson, protocol.StepStatePassed, nil),
		status(protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepDeploy}, ownPerson, protocol.StepStatePassed, nil),
		status(ref(protocol.StepAccept), ownCaller, protocol.StepStatePassed, nil),
		status(ref(protocol.StepReview), ownNode, protocol.StepStateSkipped, nil),
	}
	got := deliveryMarks(steps)
	if got[2] != stripPassed || got[5] != stripWaiting || got[3] != stripPassed || got[0] != stripPassed || got[1] != stripNotNeeded {
		t.Errorf("marks %v", got)
	}
}

func TestDeliveryStripUsesMarksWhenTheSourceGivesThem(t *testing.T) {
	c := deliveryChange{Head: "abc", Marks: deliveryMarks([]protocol.StepStatus{status(ref(protocol.StepAccept), ownCaller, protocol.StepStatePassed, nil)}),
		Evidence: []deliveryEvidence{dEv("review", "passed", "abc", "x", "", t0, nil)}}
	got := deliveryStripStates(c)
	if got[0] != stripPassed || got[1] != stripWaiting {
		t.Errorf("the strip followed the evidence list, not the step states: %v", got)
	}
}

func TestDeliveryResultWords(t *testing.T) {
	for in, want := range map[string]string{
		protocol.StepResultPass: "passed", protocol.StepResultFail: "failed",
		protocol.StepResultNeedsOwner: "needs you", protocol.StepResultClose: "closed",
	} {
		if got := deliveryResultText(in); got != want {
			t.Errorf("deliveryResultText(%q) = %q, want %q", in, got, want)
		}
	}
	// The words the strip knows still map to a mark.
	if normalResult("needs you") != stripRunning || normalResult("closed") != stripFailed || normalResult("passed") != stripPassed {
		t.Error("normalResult does not know the mapped words")
	}
}

func TestDeliveryOwnerLabels(t *testing.T) {
	for in, want := range map[protocol.StepOwner]string{
		ownCaller: "caller", ownGitHub: "github", ownPerson: "owner", ownNode: "merger", ownNone: "none",
		{Kind: protocol.OwnerDelegate}: "delegate", {Kind: protocol.OwnerNode}: "node",
	} {
		if got := deliveryOwnerLabel(in); got != want {
			t.Errorf("deliveryOwnerLabel(%+v) = %q, want %q", in, got, want)
		}
	}
}

// listView is a change as change.list type answers it: steps and next only.
func listView(now time.Time) protocol.ChangeView {
	head := hash('a')
	c := protocol.Change{ID: "ch_5", Project: "agent-net", Branch: "agent-net/w_0123456789abcdef", HeadCommit: head, Revision: 2,
		Title: "Show the provider in each quota row", State: protocol.ChangeOpen, Caller: "s_caller",
		CreatedAt: now.Add(-5 * time.Hour).Unix(), UpdatedAt: now.Add(-3 * time.Hour).Unix()}
	acc := pev("ch_5", 2, ref(protocol.StepAccept), protocol.StepResultPass, head, "s_caller", "", now.Add(-2*time.Hour), nil)
	return protocol.ChangeView{Change: c,
		Steps: []protocol.StepStatus{
			status(ref(protocol.StepAccept), ownCaller, protocol.StepStatePassed, &acc),
			status(ref(protocol.StepReview), ownNode, protocol.StepStateReady, nil),
			status(ref(protocol.StepTest), ownGitHub, protocol.StepStateReady, nil),
			status(ref(protocol.StepMerge), ownCaller, protocol.StepStateWaiting, nil),
		},
		Next: []protocol.StepRef{ref(protocol.StepReview), ref(protocol.StepTest)}}
}

func TestDeliveryChangeFromAListView(t *testing.T) {
	got := deliveryChangeFromView(listView(t0), t0)
	if got.ID != "ch_5" || got.Repo != "agent-net" || got.Branch != "agent-net/w_0123456789abcdef" || got.Head != hash('a') || got.Revision != 2 {
		t.Errorf("identity %+v", got)
	}
	// The current step is the first of next, owned by that step's owner.
	if got.Step != "review" || got.StepOwner != "merger" || got.OwnerKind != protocol.OwnerNode || got.StepState != protocol.StepStateReady {
		t.Errorf("step %q owner %q kind %q state %q", got.Step, got.StepOwner, got.OwnerKind, got.StepState)
	}
	if got.Title != "Show the provider in each quota row" || got.State != protocol.ChangeOpen || got.Caller != "s_caller" {
		t.Errorf("identity for the list: title %q state %q caller %q", got.Title, got.State, got.Caller)
	}
	// Waiting counts from the last evidence (2 h ago), which is newer than updated_at.
	if got.Waiting != 2*time.Hour {
		t.Errorf("waiting %v", got.Waiting)
	}
	if got.Full {
		t.Error("a list view is not the full change")
	}
	if len(got.Evidence) != 1 || got.Evidence[0].Step != "accept" || got.Evidence[0].Result != "passed" || got.Evidence[0].By != "caller" {
		t.Errorf("evidence %+v", got.Evidence)
	}
	if st := deliveryStripStates(got); st[0] != stripPassed || st[1] != stripWaiting {
		t.Errorf("strip %v", st)
	}
}

func TestDeliveryMergedUnrecordedPreMergeSteps(t *testing.T) {
	v := listView(t0)
	v.Change.State = protocol.ChangeDone
	v.Change.MergedCommit = hash('f')
	v.Next = []protocol.StepRef{ref(protocol.StepTest), ref(protocol.StepMerge)}
	for i := range v.Steps {
		if v.Steps[i].Step == "accept" || v.Steps[i].Step == "test" {
			v.Steps[i].State = protocol.StepStateReady
		}
	}
	got := deliveryChangeFromView(v, t0)
	if got.Step == "accept" || got.Step == "test" {
		t.Errorf("pre-merge step still current: %s", got.Step)
	}
	for _, step := range got.Steps {
		if step.Name == "accept" || step.Name == "test" {
			if step.State != "unrecorded" || step.Note != "not recorded" {
				t.Errorf("%s remains open: %+v", step.Name, step)
			}
		}
	}
	for _, state := range got.Marks {
		if state == stripRunning {
			t.Error("merged change has an open progress mark")
		}
	}
}

func TestDeliveryClaimedSession(t *testing.T) {
	if got := deliveryClaimedSession("claimed by s_session abc"); got != "s_session" {
		t.Fatalf("claim %q", got)
	}
	if got := deliveryClaimedSession("waiting for input"); got != "" {
		t.Fatalf("unexpected claim %q", got)
	}
}

func TestDeliveryWaitingStartsAtTheNewerOfEvidenceAndUpdate(t *testing.T) {
	v := listView(t0)
	v.Change.UpdatedAt = t0.Add(-10 * time.Minute).Unix() // a new revision came after the evidence
	if got := deliveryChangeFromView(v, t0).Waiting; got != 10*time.Minute {
		t.Errorf("waiting %v, want 10m", got)
	}
	v.Change.UpdatedAt = 0
	v.Steps[0].Evidence = nil
	v.Change.CreatedAt = t0.Add(-90 * time.Minute).Unix()
	if got := deliveryChangeFromView(v, t0).Waiting; got != 90*time.Minute {
		t.Errorf("waiting %v, want 90m", got)
	}
	v.Change.CreatedAt = t0.Add(time.Hour).Unix() // a clock that is behind never gives a negative wait
	if got := deliveryChangeFromView(v, t0).Waiting; got != 0 {
		t.Errorf("waiting %v, want 0", got)
	}
}

func TestDeliveryCurrentStepFallbacks(t *testing.T) {
	v := listView(t0)
	// No next step: a step that needs the owner comes first, then a failed one, then a waiting one.
	v.Next = nil
	v.Steps[1] = status(ref(protocol.StepReview), ownNode, protocol.StepStateFailed, nil)
	v.Steps[2] = status(ref(protocol.StepTest), ownGitHub, protocol.StepStatePassed, nil)
	if got := deliveryChangeFromView(v, t0); got.Step != "review" || got.StepOwner != "merger" {
		t.Errorf("failed: %q %q", got.Step, got.StepOwner)
	}
	v.Steps[3] = status(ref(protocol.StepMerge), ownPerson, protocol.StepStateNeedsOwner, nil)
	if got := deliveryChangeFromView(v, t0); got.Step != "merge" || got.StepOwner != "owner" {
		t.Errorf("needs owner: %q %q", got.Step, got.StepOwner)
	}
	v.Steps[1] = status(ref(protocol.StepReview), ownNode, protocol.StepStatePassed, nil)
	v.Steps[3] = status(ref(protocol.StepMerge), ownCaller, protocol.StepStateWaiting, nil)
	if got := deliveryChangeFromView(v, t0); got.Step != "merge" || got.StepOwner != "caller" {
		t.Errorf("waiting: %q %q", got.Step, got.StepOwner)
	}
	v.Steps = v.Steps[:3]
	if got := deliveryChangeFromView(v, t0); got.Step != "" || got.StepOwner != "" {
		t.Errorf("nothing left: %q %q", got.Step, got.StepOwner)
	}
}

// fullView is a change as change.get type answers it: every revision's evidence,
// the pipeline and the revisions.
func fullView(now time.Time) protocol.ChangeView {
	v := listView(now)
	old, head := hash('b'), hash('a')
	p := protocol.PipelineToday()
	v.Pipeline = &p
	v.Revisions = []protocol.Revision{{ChangeID: "ch_5", Number: 1, Branch: v.Change.Branch, Commit: old, At: now.Add(-6 * time.Hour).Unix()}, {ChangeID: "ch_5", Number: 2, Branch: v.Change.Branch, Commit: head, At: now.Add(-3 * time.Hour).Unix()}}
	v.Change.Caller = "s_caller"
	v.Evidence = []protocol.Evidence{
		pev("ch_5", 1, ref(protocol.StepAccept), protocol.StepResultPass, old, "s_caller", "", now.Add(-5*time.Hour), nil),
		pev("ch_5", 1, ref(protocol.StepReview), protocol.StepResultFail, old, "s_rev", "claude-opus-5-5", now.Add(-4*time.Hour), map[string]string{"findings_open": "2"}),
		pev("ch_5", 2, ref(protocol.StepAccept), protocol.StepResultPass, head, "s_caller", "", now.Add(-2*time.Hour), nil),
		pev("ch_5", 2, ref(protocol.StepTest), protocol.StepResultPass, head, protocol.EvidenceByGitHub, "", now.Add(-time.Hour), map[string]string{"suite": "ci"}),
		pev("ch_5", 2, protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}, protocol.StepResultNeedsOwner, head, protocol.EvidenceByOwner, "", now.Add(-30*time.Minute), nil),
		pev("ch_5", 2, protocol.StepRef{Step: protocol.StepTest, AfterMerge: true}, protocol.StepResultPass, head, "s_x", "", now.Add(-20*time.Minute), nil),
	}
	// The step states agree with the evidence: tests passed, the owner is asked to approve.
	v.Steps = []protocol.StepStatus{
		v.Steps[0],
		status(ref(protocol.StepReview), ownNode, protocol.StepStateReady, nil),
		status(ref(protocol.StepTest), ownGitHub, protocol.StepStatePassed, &v.Evidence[3]),
		status(protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}, ownPerson, protocol.StepStateNeedsOwner, &v.Evidence[4]),
		status(ref(protocol.StepMerge), ownCaller, protocol.StepStateWaiting, nil),
	}
	return v
}

func TestDeliveryChangeFromAFullView(t *testing.T) {
	got := deliveryChangeFromView(fullView(t0), t0)
	if !got.Full || len(got.Evidence) != 6 {
		t.Fatalf("full %v, %d evidence", got.Full, len(got.Evidence))
	}
	byKey := map[string]deliveryEvidence{}
	for _, e := range got.Evidence {
		after := ""
		if e.Fields["after_merge"] != "" {
			after = "/after"
		}
		byKey[e.Step+"/"+e.Commit[:1]+"/"+e.Result+after] = e
	}
	// Evidence of revision 1 sits on the old commit, so the detail marks it old.
	oldFail, ok := byKey["review/b/failed"]
	if !ok || oldFail.Commit != hash('b') || oldFail.Model != "claude-opus-5-5" || oldFail.Fields["findings_open"] != "2" || oldFail.By != "s_rev" {
		t.Fatalf("old review %+v (%v)", oldFail, ok)
	}
	if !oldFail.At.Equal(t0.Add(-4 * time.Hour)) {
		t.Errorf("time %v", oldFail.At)
	}
	// The session of the change's caller is named "caller"; owner and github stay as they are.
	if e := byKey["accept/a/passed"]; e.By != "caller" {
		t.Errorf("by %q", e.By)
	}
	if e := byKey["test/a/passed"]; e.By != "github" || e.Fields["suite"] != "ci" {
		t.Errorf("github evidence %+v", e)
	}
	if e := byKey["approve/a/needs you"]; e.By != "owner" || e.Fields["for"] != "merge" {
		t.Errorf("approve %+v", e)
	}
	// Steps after merge say so.
	post := false
	for _, e := range got.Evidence {
		if e.Step == "test" && e.Fields["after_merge"] == "yes" {
			post = true
		}
	}
	if !post {
		t.Error("evidence after merge is not marked")
	}
	// The old review failure does not count against the strip; only the step states do.
	if st := deliveryStripStates(got); st[1] != stripWaiting {
		t.Errorf("review mark %v", st[1])
	}
	// The detail view shows the old commit as old.
	m := deliveryModel(&fakeDelivery{changes: []deliveryChange{got}}, 120, 30)
	m = openDelivery(t, m)
	m.delivery.detail = true
	text := deliveryText(m, 120, 40)
	if !strings.Contains(text, shortCommit(hash('b'))+" old") {
		t.Errorf("old evidence is not marked old: %s", text)
	}
	if strings.Contains(text, shortCommit(hash('a'))+" old") {
		t.Errorf("head evidence is marked old: %s", text)
	}
}

func TestDeliveryDetailShowsEachStepOwnerAndState(t *testing.T) {
	v := fullView(t0)
	v.Steps[1].Note = "waiting for merger (offline)"
	ch := deliveryChangeFromView(v, t0)
	m := deliveryModel(&fakeDelivery{}, 120, 40)
	text := ansi.Strip(strings.Join(m.deliveryDetail(ch, nil, 120, 40), "\n"))
	for _, want := range []string{"accept", "caller", "passed", "review", "merger", "ready", "waiting for merger (offline)", "approve for merge", "owner", "needs_owner"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in detail:\n%s", want, text)
		}
	}
}

func TestDeliveryListShowsOfflineOwner(t *testing.T) {
	v := listView(t0)
	v.Steps[1].Note = "waiting for merger (offline)"
	ch := deliveryChangeFromView(v, t0)
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: []deliveryChange{ch}}, 120, 30))
	got := deliveryText(m, 120, 30)
	if !strings.Contains(got, "waiting for merger (offline)") {
		t.Fatalf("offline owner is not shown: %s", got)
	}
}

func TestDeliveryShowsClaimedStepAsRunning(t *testing.T) {
	v := listView(t0)
	v.Steps[1].State = "running"
	v.Steps[1].Note = "claimed by s_owner"
	ch := deliveryChangeFromView(v, t0)
	if ch.Marks[1] != stripRunning {
		t.Fatalf("claimed review mark = %v", ch.Marks[1])
	}
	m := deliveryModel(&fakeDelivery{}, 120, 40)
	got := ansi.Strip(strings.Join(m.deliveryDetail(ch, nil, 120, 40), "\n"))
	if !strings.Contains(got, "review  merger  running  claimed by s_owner") {
		t.Fatalf("claimed step missing from detail: %s", got)
	}
}

func TestDeliveryCurrentStepUsesRunningStep(t *testing.T) {
	v := listView(t0)
	v.Next = nil
	v.Steps[1].State = "running"
	v.Steps[2].State = protocol.StepStatePassed
	got := deliveryChangeFromView(v, t0)
	if got.Step != "review" || got.StepOwner != "merger" {
		t.Fatalf("current step = %s owned by %s", got.Step, got.StepOwner)
	}
}

func TestDeliveryEvidenceTextIsKeptSafe(t *testing.T) {
	v := fullView(t0)
	v.Change.Project = "pro\x1b[31mject"
	v.Evidence[0].ByModel = "mo\ndel"
	got := deliveryChangeFromView(v, t0)
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: []deliveryChange{got}}, 120, 30))
	m.delivery.detail = true
	for _, l := range append(m.deliveryView(120, 30), m.deliveryView(80, 30)...) {
		if strings.Contains(ansi.Strip(l), "\x1b") || strings.Contains(l, "\n") {
			t.Fatalf("unsafe line %q", l)
		}
	}
}

// ---- the source ----

type deliveryCaller struct {
	mu    sync.Mutex
	calls []protocol.ChangesFindArgs
	ops   []string
	pages []protocol.ChangesFindResult
	// byKey, when set, answers by state and cursor ("active|" is the first
	// page of the active changes), because the states are read together and
	// their calls do not come in a fixed order.
	byKey map[string]protocol.ChangesFindResult
	get   protocol.ChangeView
	err   error
}

func (f *deliveryCaller) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = append(f.ops, method)
	a, _ := params.(protocol.ChangesFindArgs)
	f.calls = append(f.calls, a)
	if f.err != nil {
		return nil, f.err
	}
	if a.Change != "" {
		return json.Marshal(protocol.ChangesFindResult{Items: []protocol.ChangeView{f.get}, Total: 1})
	}
	if f.byKey != nil {
		return json.Marshal(f.byKey[a.State+"|"+a.Cursor])
	}
	page := f.pages[min(len(f.calls)-1, len(f.pages)-1)]
	return json.Marshal(page)
}

func TestGatewayDeliveryPagesThroughEveryState(t *testing.T) {
	a, b := listView(t0), listView(t0)
	b.Change.ID, b.Change.Project = "ch_4", "voicemechs"
	done := listView(t0)
	done.Change.ID, done.Change.State = "ch_3", protocol.ChangeDone
	stopped := listView(t0)
	stopped.Change.ID, stopped.Change.State = "ch_2", protocol.ChangeStopped
	f := &deliveryCaller{byKey: map[string]protocol.ChangesFindResult{
		protocol.ChangeListActive + "|":     {Items: []protocol.ChangeView{a}, Total: 1, NextCursor: "ch_5"},
		protocol.ChangeListActive + "|ch_5": {Items: []protocol.ChangeView{b}, Total: 1},
		protocol.ChangeDone + "|":           {Items: []protocol.ChangeView{done}, Total: 1},
		protocol.ChangeStopped + "|":        {Items: []protocol.ChangeView{stopped}, Total: 1},
	}}
	src := newGatewayDelivery(f.Call, func() time.Time { return t0 })
	got, err := src.Changes(context.Background(), "")
	if err != nil || len(got) != 4 {
		t.Fatalf("changes %+v: %v", got, err)
	}
	if got[0].ID != "ch_5" || got[1].ID != "ch_4" || got[1].Repo != "voicemechs" || got[2].State != protocol.ChangeDone || got[3].State != protocol.ChangeStopped {
		t.Fatalf("changes %+v", got)
	}
	if len(f.calls) != 4 || f.ops[0] != protocol.OpChangesOwnerFind {
		t.Fatalf("calls %+v ops %v", f.calls, f.ops)
	}
	// The first pages are asked together, in any order; the second page of
	// the active changes follows its first.
	asked := map[protocol.ChangesFindArgs]bool{}
	for _, c := range f.calls {
		asked[c] = true
	}
	for _, want := range []protocol.ChangesFindArgs{
		// The first page of each state asks for an ETag to send next time.
		{State: protocol.ChangeListActive, Limit: deliveryPageSize, WantETag: true, Lite: true},
		{State: protocol.ChangeListActive, Limit: deliveryPageSize, Cursor: "ch_5", Lite: true},
		{State: protocol.ChangeDone, Limit: deliveryPageSize, WantETag: true, Lite: true},
		{State: protocol.ChangeStopped, Limit: deliveryPageSize, WantETag: true, Lite: true},
	} {
		if !asked[want] {
			t.Errorf("call %+v was not made: %+v", want, f.calls)
		}
	}
	// A repo filter is the project filter.
	repo := &deliveryCaller{pages: []protocol.ChangesFindResult{{Items: []protocol.ChangeView{b}, Total: 1}}}
	if _, err := newGatewayDelivery(repo.Call, func() time.Time { return t0 }).Changes(context.Background(), "voicemechs"); err != nil || repo.calls[0].Project != "voicemechs" {
		t.Errorf("repo filter %+v: %v", repo.calls, err)
	}
}

func TestGatewayDeliveryStopsAtItsCap(t *testing.T) {
	v := listView(t0)
	items := make([]protocol.ChangeView, deliveryPageSize)
	for i := range items {
		items[i] = v
	}
	f := &deliveryCaller{pages: []protocol.ChangesFindResult{{Items: items, NextCursor: "more"}}}
	src := newGatewayDelivery(f.Call, func() time.Time { return t0 })
	got, err := src.Changes(context.Background(), "")
	// The three states are asked together, and the cap stops the later pages.
	if err != nil || len(got) != deliveryMaxChanges || len(f.calls) != deliveryMaxChanges/deliveryPageSize {
		t.Fatalf("%d changes after %d calls: %v", len(got), len(f.calls), err)
	}
}

func TestGatewayDeliveryReadsOneChangeInFull(t *testing.T) {
	f := &deliveryCaller{get: fullView(t0)}
	src := newGatewayDelivery(f.Call, func() time.Time { return t0 })
	got, err := src.Change(context.Background(), "ch_5")
	if err != nil || !got.Full || len(got.Evidence) != 6 || f.calls[0].Change != "ch_5" || f.ops[0] != protocol.OpChangesOwnerFind {
		t.Fatalf("change %+v: %v (%+v)", got, err, f.calls)
	}
	f.get = protocol.ChangeView{}
	f.pages = nil
}

func TestGatewayDeliveryErrors(t *testing.T) {
	f := &deliveryCaller{err: errors.New(`unknown method "changes_owner_find"`)}
	src := newGatewayDelivery(f.Call, func() time.Time { return t0 })
	_, err := src.Changes(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "restart the daemon") {
		t.Errorf("old daemon: %v", err)
	}
	f.err = errors.New("the gateway did not type answer in time")
	if _, err = src.Change(context.Background(), "ch_1"); err == nil || err.Error() != "the gateway did not type answer in time" {
		t.Errorf("gateway error changed: %v", err)
	}
}

// ---- the view with a live source ----

// detailSource is a fake that also reads one change in full.
type detailSource struct {
	fakeDelivery
	full  map[string]deliveryChange
	reads []string
	err   error
}

func (d *detailSource) Change(_ context.Context, id string) (deliveryChange, error) {
	d.reads = append(d.reads, id)
	if d.err != nil {
		return deliveryChange{}, d.err
	}
	return d.full[id], nil
}

func fullScenes() (*detailSource, []deliveryChange) {
	list := deliveryScenes()
	src := &detailSource{fakeDelivery: fakeDelivery{changes: list}, full: map[string]deliveryChange{}}
	for _, c := range list {
		c.Full = true
		c.Evidence = append([]deliveryEvidence(nil), c.Evidence...)
		c.Evidence = append(c.Evidence, dEv("verify", "passed", c.Head, "caller", "", t0, nil))
		src.full[c.ID] = c
		// the list copy has less evidence
		_ = c
	}
	for i := range src.changes {
		if len(src.changes[i].Evidence) > 1 {
			src.changes[i].Evidence = src.changes[i].Evidence[:1]
		}
	}
	return src, src.changes
}

func dpress(m Model, k tea.KeyPressMsg) (Model, tea.Cmd) {
	next, cmd := m.Update(k)
	return next.(Model), cmd
}

func TestDeliveryEnterFetchesTheFullChange(t *testing.T) {
	src, _ := fullScenes()
	m := openDelivery(t, deliveryModel(src, 120, 30))
	m.delivery.row = visibleRow(m, "c_2")
	m.inboxIndex = m.askRowCount() + m.delivery.row
	m, cmd := dpress(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.delivery.detail || cmd == nil {
		t.Fatal("enter should open the detail and fetch the change")
	}
	// Until the answer comes, the detail says the evidence is loading.
	if got := deliveryText(m, 120, 30); !strings.Contains(got, "Loading") {
		t.Errorf("no loading line: %s", got)
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if len(src.reads) != 1 || src.reads[0] != "c_2" {
		t.Fatalf("reads %v", src.reads)
	}
	if got := m.delivery.changes[1]; !got.Full || len(got.Evidence) != 5 {
		t.Fatalf("the list did not take the full change: %+v", got)
	}
	if got := deliveryText(m, 120, 30); strings.Contains(got, "Loading") || !strings.Contains(got, "verify") {
		t.Errorf("detail after the answer: %s", got)
	}
	// An error shows above the detail and keeps what is there.
	src.err = errors.New("no such change")
	m, cmd = dpress(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	m, cmd = dpress(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	next, _ = m.Update(cmd())
	m = next.(Model)
	if got := deliveryText(m, 120, 30); !strings.Contains(got, "Could not load change: no such change") || !strings.Contains(got, "verify") {
		t.Errorf("detail error: %s", got)
	}
}

func TestDeliveryReloadKeepsTheSelectionAndTheFullDetail(t *testing.T) {
	src, _ := fullScenes()
	m := openDelivery(t, deliveryModel(src, 120, 30))
	m.delivery.row = visibleRow(m, "c_2")
	m.inboxIndex = m.askRowCount() + m.delivery.row
	m, cmd := dpress(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	next, _ := m.Update(cmd())
	m = next.(Model)
	// A newer change arrives: the selected change keeps its place in the list
	// (grouped), and stays selected.
	fresh := deliveryChange{ID: "c_9", Repo: "new", Branch: "b", Head: "f00", Revision: 1, Step: "accept", StepOwner: "caller"}
	list := append([]deliveryChange{fresh}, deliveryScenes()...)
	for i := range list[1:] {
		if len(list[i+1].Evidence) > 1 {
			list[i+1].Evidence = list[i+1].Evidence[:1]
		}
	}
	src.changes = list
	next, cmd = m.Update(m.deliveryLoad()())
	m = next.(Model)
	if sel, ok := m.selectedChange(); !ok || sel.ID != "c_2" || !m.delivery.detail {
		t.Fatalf("row %d detail %v selected %+v", m.delivery.row, m.delivery.detail, sel)
	}
	if got := m.delivery.changes[2]; !got.Full || len(got.Evidence) != 5 {
		t.Errorf("the full evidence flickered away: %+v", got)
	}
	if cmd == nil {
		t.Fatal("an open detail should be fetched again after a reload")
	}
	// The selected change closed: the detail closes.
	src.changes = list[:1]
	next, _ = m.Update(m.deliveryLoad()())
	m = next.(Model)
	if m.delivery.detail || m.delivery.row != 0 {
		t.Errorf("detail %v row %d after the change left the list", m.delivery.detail, m.delivery.row)
	}
}

func TestDeliveryRefreshesEvery30SecondsWhileOpen(t *testing.T) {
	src := &fakeDelivery{changes: deliveryScenes()}
	clock := t0
	m := deliveryModel(src, 120, 30)
	m.now = func() time.Time { return clock }
	m, cmd := m.startDeliveryLoad()
	if cmd == nil || !m.delivery.loading {
		t.Fatal("start did not load")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	m.delivery.on = true
	tick := func() tea.Cmd {
		var c tea.Cmd
		m, c = m.deliveryTick()
		return c
	}
	if tick() != nil {
		t.Fatal("refreshed at once")
	}
	clock = t0.Add(29 * time.Second)
	if tick() != nil {
		t.Fatal("refreshed before 30 s")
	}
	clock = t0.Add(30 * time.Second)
	c := tick()
	if c == nil {
		t.Fatal("no refresh after 30 s")
	}
	// A load that is still out is not asked again.
	clock = t0.Add(70 * time.Second)
	if tick() != nil {
		t.Fatal("asked again while loading")
	}
	next, _ = m.Update(c())
	m = next.(Model)
	if src.calls != 2 {
		t.Fatalf("%d loads", src.calls)
	}

}

func TestDeliveryTickMessageRefreshes(t *testing.T) {
	src := &fakeDelivery{changes: deliveryScenes()}
	clock := t0
	m := openDelivery(t, deliveryModel(src, 120, 30))
	m.now = func() time.Time { return clock }
	m.delivery.loadedAt = t0
	m.fetching = true // the tick must not start a snapshot fetch of its own
	clock = t0.Add(31 * time.Second)
	next, cmd := m.Update(tickMsg{})
	m = next.(Model)
	if cmd == nil || !m.delivery.loading {
		t.Fatalf("the tick did not start a delivery load (loading %v)", m.delivery.loading)
	}
}

func TestDeliveryRefreshKeyAndOpenStartALoad(t *testing.T) {
	src := &fakeDelivery{changes: deliveryScenes()}
	m := deliveryModel(src, 120, 30)
	m, cmd := m.startDeliveryLoad()
	if cmd == nil || !m.delivery.loading {
		t.Fatalf("opening the sub-tab: on %v loading %v cmd %v", m.delivery.on, m.delivery.loading, cmd != nil)
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if m.delivery.loading || !m.delivery.loaded {
		t.Fatal("the answer did not end the load")
	}
	m, cmd = dpress(m, tea.KeyPressMsg{Text: "R", Code: []rune("R")[0]})
	if cmd == nil || !m.delivery.loading {
		t.Fatal("R did not reload")
	}
	// R cannot overlap a load already in flight.
	if _, again := dpress(m, tea.KeyPressMsg{Text: "R", Code: []rune("R")[0]}); again != nil {
		t.Fatal("R overlapped a load")
	}
}

func TestNewWiresTheGatewaySourceOnlyForADaemon(t *testing.T) {
	if m := New(&fakeSource{snap: fakeSnapshot()}); m.deliverySrc != nil {
		t.Error("a source with no Call got a delivery source")
	}
	type callerSource struct {
		*fakeSource
		*deliveryCaller
	}
	m := New(callerSource{&fakeSource{snap: fakeSnapshot()}, &deliveryCaller{}})
	if m.deliverySrc == nil {
		t.Error("a daemon source got no delivery source")
	}
}

// ---- golden scene from real-shaped data ----

func TestDeliveryGoldenFromGatewayData(t *testing.T) {
	now := t0
	a := fullView(now)
	b := listView(now)
	b.Change.ID, b.Change.Project, b.Change.Branch, b.Change.Revision = "ch_6", "voicemechs", "feature/deploy-gate", 1
	b.Change.HeadCommit, b.Change.Title = hash('c'), "Fix the deploy gate"
	b.Steps = []protocol.StepStatus{
		status(ref(protocol.StepAccept), ownCaller, protocol.StepStatePassed, nil),
		status(ref(protocol.StepReview), ownNode, protocol.StepStateFailed, nil),
		status(ref(protocol.StepTest), ownGitHub, protocol.StepStatePassed, nil),
		status(protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}, ownNone, protocol.StepStateSkipped, nil),
		status(ref(protocol.StepMerge), ownCaller, protocol.StepStateWaiting, nil),
	}
	b.Next = nil
	b.Change.UpdatedAt = now.Add(-26*time.Hour - 5*time.Minute).Unix()
	b.Steps[0].Evidence = nil
	c := listView(now)
	c.Change.ID, c.Change.Project, c.Change.Branch, c.Change.Revision = "ch_7", "agent-net", "delivery-pipeline", 1
	c.Change.HeadCommit, c.Change.Title = hash('d'), "Say what a delivery row waits for"
	c.Steps = []protocol.StepStatus{
		status(ref(protocol.StepAccept), ownCaller, protocol.StepStatePassed, nil),
		status(ref(protocol.StepReview), ownNode, protocol.StepStatePassed, nil),
		status(ref(protocol.StepTest), ownGitHub, protocol.StepStatePassed, nil),
		status(protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}, ownPerson, protocol.StepStateNeedsOwner, nil),
		status(ref(protocol.StepMerge), ownCaller, protocol.StepStateWaiting, nil),
	}
	c.Next = nil
	c.Change.UpdatedAt = now.Add(-6 * time.Minute).Unix()
	c.Steps[0].Evidence = nil
	var list []deliveryChange
	for _, v := range []protocol.ChangeView{c, b, listView(now)} {
		list = append(list, deliveryChangeFromView(v, now))
	}
	full := deliveryChangeFromView(a, now)
	for _, size := range [][2]int{{80, 24}, {120, 24}} {
		m := openDelivery(t, deliveryModel(&fakeDelivery{changes: list}, size[0], size[1]))
		golden(t, fmt.Sprintf("delivery_gateway_list_%dx%d", size[0], size[1]), m.testView())
		list2 := append([]deliveryChange(nil), list...)
		list2[2] = full
		m = openDelivery(t, deliveryModel(&fakeDelivery{changes: list2}, size[0], size[1]))
		m.delivery.row, m.delivery.detail = 2, true
		golden(t, fmt.Sprintf("delivery_gateway_detail_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestDeliveryMarksSeparateAfterMerge(t *testing.T) {
	steps := []protocol.StepStatus{
		{StepRef: protocol.StepRef{Step: protocol.StepTest}, State: protocol.StepStatePassed},
		{StepRef: protocol.StepRef{Step: protocol.StepTest, AfterMerge: true}, State: protocol.StepStateWaiting},
	}
	marks := deliveryMarks(steps)
	pre, post := -1, -1
	for i, name := range deliverySteps {
		if name == "test" {
			pre = i
		}
		if name == "test after merge" {
			post = i
		}
	}
	if pre < 0 || post < 0 || marks[pre] != stripPassed || marks[post] != stripWaiting {
		t.Fatalf("marks: %v steps: %v", marks, deliverySteps)
	}
}

func TestDeliveryOutcomeFromEvidence(t *testing.T) {
	v := protocol.ChangeView{Change: protocol.Change{MergedCommit: "abcdef1234567890"}, Steps: []protocol.StepStatus{
		{StepRef: protocol.StepRef{Step: protocol.StepTest}, Evidence: &protocol.Evidence{Step: protocol.StepTest, Fields: map[string]string{protocol.FieldRun: "https://ci.example/run/1"}}},
		{StepRef: protocol.StepRef{Step: protocol.StepRelease}, Evidence: &protocol.Evidence{Step: protocol.StepRelease, Fields: map[string]string{protocol.FieldTag: "v1.2.3"}}},
	}}
	got := deliveryOutcome(deliveryChangeFromView(v, t0))
	for _, want := range []string{"abcdef", "https://ci.example/run/1", "v1.2.3"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestDeliveryRefreshUpdatesSessionsAndDoesNotOverlap(t *testing.T) {
	src := &fakeDelivery{}
	clock := t0
	m := treeModel(80, 24, mergerFixture())
	m.deliverySrc, m.now, m.tab = src, func() time.Time { return clock }, sessionsTab
	m, cmd := m.startDeliveryLoad()
	if cmd == nil {
		t.Fatal("initial load missing")
	}
	m, again := m.deliveryTick()
	if again != nil {
		t.Fatal("overlapping load")
	}
	m, _ = m.deliveryLoaded(cmd().(deliveryLoadedMsg))
	a, _ := m.agentByID(mainID)
	if m.sessionTaskText(a) != "" {
		t.Fatal("unexpected task")
	}
	src.changes = []deliveryChange{{ID: "ch_ed5f0000", Steps: []deliveryStep{{Name: "test", State: protocol.StepStateRunning, Note: "claimed by " + mainID}}}}
	clock = clock.Add(30 * time.Second)
	m, cmd = m.deliveryTick()
	if cmd == nil {
		t.Fatal("refresh missing on Sessions")
	}
	m, _ = m.deliveryLoaded(cmd().(deliveryLoadedMsg))
	if got := m.sessionTaskText(a); got != "test ch_ed5f" {
		t.Fatalf("task %q", got)
	}
	src.changes = nil
	clock = clock.Add(30 * time.Second)
	m, cmd = m.deliveryTick()
	if cmd == nil {
		t.Fatal("second refresh missing")
	}
	m, _ = m.deliveryLoaded(cmd().(deliveryLoadedMsg))
	if got := m.sessionTaskText(a); got != "" {
		t.Fatalf("stale task %q", got)
	}
	m.tab = inboxTab
	m, cmd = m.startDeliveryLoad()
	if cmd == nil {
		t.Fatal("load missing")
	}
	_, again = m.deliveryKey(tea.KeyPressMsg{Text: "R", Code: 'R'})
	if again != nil {
		t.Fatal("refresh key started overlapping load")
	}
}

func TestDeliveryStartupUsesInflightGuard(t *testing.T) {
	m := deliveryModel(&fakeDelivery{}, 80, 24)
	next, cmd := m.Update(deliveryStartMsg{})
	m = next.(Model)
	if cmd == nil || !m.delivery.loading {
		t.Fatal("startup did not mark load in flight")
	}
	next, again := m.Update(deliveryStartMsg{})
	if again != nil || !next.(Model).delivery.loading {
		t.Fatal("startup overlapped its load")
	}
}

func TestDeliveryOwnerAndFailureTakePriority(t *testing.T) {
	for _, state := range []string{protocol.StepStateNeedsOwner, protocol.StepStateFailed} {
		t.Run(state, func(t *testing.T) {
			step := protocol.StepTest
			if state == protocol.StepStateNeedsOwner {
				step = protocol.StepVerify
			}
			v := protocol.ChangeView{Change: protocol.Change{ID: "ch_action", Title: "Fix session entry", State: protocol.ChangeOpen, HeadCommit: hash('a'), Caller: "s_caller", Origin: protocol.ChangeOrigin{Kind: protocol.OriginWorker, SessionID: "s_author"}},
				Next: []protocol.StepRef{ref(protocol.StepAccept)}, Steps: []protocol.StepStatus{
					status(ref(protocol.StepAccept), ownCaller, protocol.StepStateReady, nil),
					status(ref(step), ownCaller, state, nil),
				}}
			e := pev("ch_action", 1, ref(step), protocol.StepResultFail, hash('a'), "s_merger", "", t0, nil)
			e.Note = "entry test failed"
			v.Steps[1].Evidence = &e
			ch := deliveryChangeFromView(v, t0)
			m := deliveryModel(nil, 120, 34)
			m.snap.Agents = []protocol.AgentView{{SessionID: "s_author", Profile: protocol.Profile{Name: "entry worker"}, Online: true}, {SessionID: "s_caller", Profile: protocol.Profile{Name: "caller"}, Online: true}}
			m.delivery.changes = []deliveryChange{ch}
			if ch.Step != step {
				t.Errorf("current step = %q, want %s", ch.Step, step)
			}
			if state == protocol.StepStateNeedsOwner {
				if got := deliveryText(m, 120, 34); !strings.Contains(got, "Needs you · 1") || !strings.Contains(got, "waiting for you") {
					t.Fatal(got)
				}
			} else {
				if got, _ := m.deliveryStatusText(ch); got != "back to entry worker (fix)" {
					t.Errorf("status: %q", got)
				}
				if got := deliveryText(m, 120, 34); !strings.Contains(got, "entry test failed") {
					t.Fatal(got)
				}
				m.snap.Agents[0].Online = false
				if got, _ := m.deliveryStatusText(ch); got != "back to caller to restart the worker" {
					t.Errorf("ended worker status: %q", got)
				}
				if got := deliveryText(m, 120, 34); !strings.Contains(got, "back to caller to restart the worker") {
					t.Fatal(got)
				}
			}
		})
	}
}

func TestDeliveryStoppedFailureHidden(t *testing.T) {
	m := deliveryModel(nil, 120, 34)
	m.delivery.changes = []deliveryChange{{ID: "ch_stopped", Title: "Stopped work", State: protocol.ChangeStopped, StepState: protocol.StepStateFailed}}
	if got := m.deliveryVisible(); len(got) != 0 {
		t.Errorf("stopped is visible: %+v", got)
	}
	if done, stale := m.deliveryHidden(); done+stale != 1 {
		t.Errorf("hidden: %d done %d stale", done, stale)
	}
	m = keys(m, "v")
	if got := m.deliveryVisible(); len(got) != 1 || got[0].Group == groupMoving {
		t.Errorf("show all: %+v", got)
	}
}

func TestDeliveryLatestFailedStepFinding(t *testing.T) {
	old := pev("ch_latest", 1, ref(protocol.StepReview), protocol.StepResultFail, hash('a'), "s_reviewer", "", t0.Add(-time.Hour), nil)
	old.Note = "old review finding"
	latest := pev("ch_latest", 1, ref(protocol.StepTest), protocol.StepResultFail, hash('a'), "s_merger", "", t0, nil)
	latest.Note = "new test finding"
	v := protocol.ChangeView{Change: protocol.Change{State: protocol.ChangeOpen, HeadCommit: hash('a')}, Next: []protocol.StepRef{ref(protocol.StepAccept)}, Steps: []protocol.StepStatus{
		status(ref(protocol.StepReview), ownNode, protocol.StepStateFailed, &old),
		status(ref(protocol.StepTest), ownNode, protocol.StepStateFailed, &latest),
	}}
	ch := deliveryChangeFromView(v, t0)
	if ch.Step != protocol.StepTest || deliveryRowFinding(ch) != latest.Note {
		t.Fatalf("latest failure: %+v", ch)
	}
}

func TestGatewayDeliveryAsksForItsVersionAndStaysOnNotModified(t *testing.T) {
	f := &deliveryCaller{pages: []protocol.ChangesFindResult{{NotModified: true}}}
	src := newGatewayDelivery(f.Call, func() time.Time { return t0 })
	src.SetIfVersion(17)
	if _, err := src.Changes(context.Background(), ""); !errors.Is(err, errDeliveryNotModified) {
		t.Fatalf("unchanged list: %v", err)
	}
	// The three states are asked together, so each call carries the version.
	if len(f.calls) == 0 {
		t.Fatal("no call was made")
	}
	for _, c := range f.calls {
		if c.IfVersion != 17 || !c.Lite {
			t.Fatalf("call %+v, want if_version 17 and lite: %+v", c, f.calls)
		}
	}
}

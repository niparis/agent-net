package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

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
	got := deliveryChangeFromView(listView(t0))
	if got.ID != "ch_5" || got.Repo != "agent-net" || got.State != protocol.ChangeOpen {
		t.Errorf("identity %+v", got)
	}
	// The current step is the first of next, with that step's owner kind.
	if got.Step != "review" || got.OwnerKind != protocol.OwnerNode || got.StepState != protocol.StepStateReady {
		t.Errorf("step %q kind %q state %q", got.Step, got.OwnerKind, got.StepState)
	}
	if len(got.Steps) != 4 || got.Steps[0].Name != "accept" || got.Steps[0].State != protocol.StepStatePassed {
		t.Errorf("steps %+v", got.Steps)
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
	got := deliveryChangeFromView(v)
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
}

func TestDeliveryClaimedSession(t *testing.T) {
	if got := deliveryClaimedSession("claimed by s_session abc"); got != "s_session" {
		t.Fatalf("claim %q", got)
	}
	if got := deliveryClaimedSession("waiting for input"); got != "" {
		t.Fatalf("unexpected claim %q", got)
	}
}

func TestDeliveryCurrentStepFallbacks(t *testing.T) {
	v := listView(t0)
	// No next step: a step that needs the owner comes first, then a failed one, then a waiting one.
	v.Next = nil
	v.Steps[1] = status(ref(protocol.StepReview), ownNode, protocol.StepStateFailed, nil)
	v.Steps[2] = status(ref(protocol.StepTest), ownGitHub, protocol.StepStatePassed, nil)
	if got := deliveryChangeFromView(v); got.Step != "review" || got.OwnerKind != protocol.OwnerNode {
		t.Errorf("failed: %q %q", got.Step, got.OwnerKind)
	}
	v.Steps[3] = status(ref(protocol.StepMerge), ownPerson, protocol.StepStateNeedsOwner, nil)
	if got := deliveryChangeFromView(v); got.Step != "merge" || got.OwnerKind != protocol.OwnerPerson {
		t.Errorf("needs owner: %q %q", got.Step, got.OwnerKind)
	}
	v.Steps[1] = status(ref(protocol.StepReview), ownNode, protocol.StepStatePassed, nil)
	v.Steps[3] = status(ref(protocol.StepMerge), ownCaller, protocol.StepStateWaiting, nil)
	if got := deliveryChangeFromView(v); got.Step != "merge" || got.OwnerKind != protocol.OwnerCaller {
		t.Errorf("waiting: %q %q", got.Step, got.OwnerKind)
	}
	v.Steps = v.Steps[:3]
	if got := deliveryChangeFromView(v); got.Step != "" || got.OwnerKind != "" {
		t.Errorf("nothing left: %q %q", got.Step, got.OwnerKind)
	}
}

func TestDeliveryCurrentStepUsesRunningStep(t *testing.T) {
	v := listView(t0)
	v.Next = nil
	v.Steps[1].State = "running"
	v.Steps[2].State = protocol.StepStatePassed
	got := deliveryChangeFromView(v)
	if got.Step != "review" || got.OwnerKind != protocol.OwnerNode {
		t.Fatalf("current step = %s owned by %s", got.Step, got.OwnerKind)
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
	src := newGatewayDelivery(f.Call)
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
	if _, err := newGatewayDelivery(repo.Call).Changes(context.Background(), "voicemechs"); err != nil || repo.calls[0].Project != "voicemechs" {
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
	src := newGatewayDelivery(f.Call)
	got, err := src.Changes(context.Background(), "")
	// The three states are asked together, and the cap stops the later pages.
	if err != nil || len(got) != deliveryMaxChanges || len(f.calls) != deliveryMaxChanges/deliveryPageSize {
		t.Fatalf("%d changes after %d calls: %v", len(got), len(f.calls), err)
	}
}

func TestGatewayDeliveryErrors(t *testing.T) {
	f := &deliveryCaller{err: errors.New(`unknown method "changes_owner_find"`)}
	src := newGatewayDelivery(f.Call)
	_, err := src.Changes(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "restart the daemon") {
		t.Errorf("old daemon: %v", err)
	}
	f.err = errors.New("the gateway did not type answer in time")
	if _, err = src.Changes(context.Background(), ""); err == nil || err.Error() != "the gateway did not type answer in time" {
		t.Errorf("gateway error changed: %v", err)
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
	m := deliveryModel(src, 120, 30)
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

func TestDeliveryStartLoadsOnce(t *testing.T) {
	src := &fakeDelivery{changes: deliveryScenes()}
	m := deliveryModel(src, 120, 30)
	m, cmd := m.startDeliveryLoad()
	if cmd == nil || !m.delivery.loading {
		t.Fatalf("start: loading %v cmd %v", m.delivery.loading, cmd != nil)
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if m.delivery.loading || !m.delivery.loaded {
		t.Fatal("the answer did not end the load")
	}
	// A second start cannot overlap a load already in flight.
	m, _ = m.startDeliveryLoad()
	if _, again := m.startDeliveryLoad(); again != nil {
		t.Fatal("start overlapped a load")
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

func TestGatewayDeliveryAsksForItsVersionAndStaysOnNotModified(t *testing.T) {
	f := &deliveryCaller{pages: []protocol.ChangesFindResult{{NotModified: true}}}
	src := newGatewayDelivery(f.Call)
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

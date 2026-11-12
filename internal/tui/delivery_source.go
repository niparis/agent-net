package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Delivery view reads the gateway's changes through the daemon, with the
// owner's read changes_owner_find (the same data as the agent tool
// agent_changes_find, for every change and without a session). This file maps
// the protocol's ChangeView and Evidence onto the view's own types.

const (
	// deliveryPageSize is how many changes one call asks for, and
	// deliveryMaxChanges is how many the list holds at most: three pages.
	deliveryPageSize   = 100
	deliveryMaxChanges = 300
)

// errDeliveryNotModified means the gateway's changes topic has not moved since
// the list was read, so the view keeps its copy.
var errDeliveryNotModified = errors.New("delivery list unchanged")

// gatewayDelivery is the deliverySource that reads from the daemon.
type gatewayDelivery struct {
	call func(context.Context, string, any) (json.RawMessage, error)
	now  func() time.Time
	// ifVersion is the changes topic version the held list was read at.
	ifVersion uint64

	mu sync.Mutex
	// held is what the last read of each state got: its first page with the
	// ETag the gateway gave it, and the later pages. full says the next read
	// ignores them. The changes topic moves with every message and event, but
	// the list rarely changes, so a page that the gateway says is the same is
	// not sent again.
	held map[string]heldChanges
	full bool
}

// heldChanges is one state's list as the gateway sent it. The rows are the
// gateway's own, not the view's: how long a change has waited depends on the
// time of each draw.
type heldChanges struct {
	etag  string
	first []protocol.ChangeView
	next  string
	more  []protocol.ChangeView
}

// SetIfVersion is how the view says which changes version it already holds.
func (g *gatewayDelivery) SetIfVersion(v uint64) { g.ifVersion = v }

// SetFullRead says the next read asks for every page again. The view sets it
// now and then, so a change on a later page is never missed for long.
func (g *gatewayDelivery) SetFullRead(full bool) {
	g.mu.Lock()
	g.full = full
	g.mu.Unlock()
}

func newGatewayDelivery(call func(context.Context, string, any) (json.RawMessage, error), now func() time.Time) *gatewayDelivery {
	return &gatewayDelivery{call: call, now: now, held: map[string]heldChanges{}}
}

// find sends one changes_owner_find and decodes the page.
func (g *gatewayDelivery) find(ctx context.Context, a protocol.ChangesFindArgs) (protocol.ChangesFindResult, error) {
	var out protocol.ChangesFindResult
	raw, err := g.call(ctx, protocol.OpChangesOwnerFind, a)
	if err != nil {
		if strings.Contains(err.Error(), "unknown method") || strings.Contains(err.Error(), "not available to cli") {
			return out, errors.New("this daemon does not know the delivery pipeline yet: restart the daemon after you upgrade it")
		}
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("bad answer for the delivery pipeline: %v", err)
	}
	return out, nil
}

// Changes lists the changes the view can show. Active (open and merged) comes
// first, so a long history of done changes never pushes live work off the
// list; done and stopped follow for the Done band, which is hidden by default.
// It follows the pages of each state up to deliveryMaxChanges.
func (g *gatewayDelivery) Changes(ctx context.Context, repo string) ([]deliveryChange, error) {
	// The first page of each state is read at the same time, so a slow one does
	// not delay the others and the load takes the time of the slowest page, not
	// the sum. Later pages are rare and follow in order, until the cap.
	states := []string{protocol.ChangeListActive, protocol.ChangeDone, protocol.ChangeStopped}
	g.mu.Lock()
	full := g.full
	g.mu.Unlock()
	type first struct {
		views     []protocol.ChangeView
		cursor    string
		etag      string
		unchanged bool
		err       error
	}
	firsts := make([]first, len(states))
	var wg sync.WaitGroup
	for i, state := range states {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := &firsts[i]
			key := repo + "\x00" + state
			ifETag := ""
			g.mu.Lock()
			held, ok := g.held[key]
			g.mu.Unlock()
			if ok && !full {
				ifETag = held.etag
			}
			page, err := g.find(ctx, protocol.ChangesFindArgs{Project: repo, State: state, Limit: deliveryPageSize, IfVersion: g.ifVersion, IfETag: ifETag, WantETag: true, Lite: true})
			switch {
			case err != nil:
				f.err = err
			case page.NotModified && page.ETag == "":
				// The topic has not moved: the view keeps its list.
				f.err = errDeliveryNotModified
			case page.NotModified && ok:
				// The page is the one the view holds, though the topic moved.
				f.views, f.cursor, f.etag, f.unchanged = held.first, held.next, held.etag, true
			default:
				f.views, f.etag = page.Items, page.ETag
				if len(page.Items) > 0 {
					f.cursor = page.NextCursor
				}
			}
		}()
	}
	wg.Wait()
	have := 0 // changes in the first pages of all states
	for _, f := range firsts {
		if f.err != nil {
			return nil, f.err
		}
		have += len(f.views)
	}
	var out []deliveryChange
	now := g.now()
	add := func(views []protocol.ChangeView) {
		for _, v := range views {
			out = append(out, deliveryChangeFromView(v, now))
		}
	}
	for i, state := range states {
		key := repo + "\x00" + state
		f := firsts[i]
		add(f.views)
		var more []protocol.ChangeView
		if f.unchanged {
			// The first page is the same, so the pages after it are the ones
			// the last full read got.
			g.mu.Lock()
			more = g.held[key].more
			g.mu.Unlock()
			add(more)
			have += len(more)
		} else {
			// More pages only while the cap is not reached by what is already
			// read.
			cursor := f.cursor
			for cursor != "" && have < deliveryMaxChanges {
				page, err := g.find(ctx, protocol.ChangesFindArgs{Project: repo, State: state, Limit: deliveryPageSize, Cursor: cursor, Lite: true})
				if err != nil {
					return nil, err
				}
				add(page.Items)
				more = append(more, page.Items...)
				have += len(page.Items)
				cursor = page.NextCursor
				if len(page.Items) == 0 {
					break
				}
			}
			g.mu.Lock()
			g.held[key] = heldChanges{etag: f.etag, first: f.views, next: f.cursor, more: more}
			g.mu.Unlock()
		}
	}
	return out[:min(len(out), deliveryMaxChanges)], nil
}

// Change reads one change in full: every revision's evidence.
func (g *gatewayDelivery) Change(ctx context.Context, id string) (deliveryChange, error) {
	page, err := g.find(ctx, protocol.ChangesFindArgs{Change: id})
	if err != nil {
		return deliveryChange{}, err
	}
	if len(page.Items) == 0 {
		return deliveryChange{}, fmt.Errorf("the gateway has no change %s", id)
	}
	return deliveryChangeFromView(page.Items[0], g.now()), nil
}

// ReportTry sends the owner's try of a released change: pass when it worked,
// fail with the owner's line when it did not.
func (g *gatewayDelivery) ReportTry(ctx context.Context, changeID, result, note string) error {
	_, err := g.call(ctx, protocol.OpChangesOwnerReport, protocol.ChangesOwnerReportArgs{Change: changeID, Result: result, Note: note})
	return err
}

// deliveryOwnerLabel is who owns a step, as the list shows it: the node's
// name for a node, else the kind (caller, github, owner, delegate, none).
func deliveryOwnerLabel(o protocol.StepOwner) string {
	if o.Kind == protocol.OwnerNode {
		return firstNonEmpty(o.Node, "node")
	}
	return firstNonEmpty(o.Kind, "-")
}

// deliveryResultText is a step result in the words the view shows.
func deliveryResultText(result string) string {
	switch result {
	case protocol.StepResultPass:
		return "passed"
	case protocol.StepResultFail:
		return "failed"
	case protocol.StepResultNeedsOwner:
		return "needs you"
	case protocol.StepResultClose:
		return "closed"
	}
	return result
}

// deliveryMarks gives the strip: one mark for each of the eight step names,
// from the states of the change's steps. A name that sits in more than one
// place (test before and after merge, approve for merge and for deploy) is one
// mark: a failed or closed step shows as failed, a step that needs the owner or
// a name that is only partly done shows as running, a name whose steps are all
// passed is passed, and one whose steps are all left out is not needed.
func deliveryMarks(steps []protocol.StepStatus) []stripState {
	marks := make([]stripState, len(deliverySteps))
	for i, name := range deliverySteps {
		var active, passed int
		failed, running := false, false
		for _, st := range steps {
			want := name
			if name == "test after merge" {
				want = "test"
			}
			if st.Step != want || (st.AfterMerge && st.Step == "test") != (name == "test after merge") {
				continue
			}
			switch st.State {
			case protocol.StepStateSkipped:
				continue
			case "unrecorded":
				continue
			case protocol.StepStatePassed:
				passed++
			case protocol.StepStateFailed, protocol.StepStateClosed:
				failed = true
			case protocol.StepStateRunning:
				running = true
			}
			active++
		}
		switch {
		case failed:
			marks[i] = stripFailed
		case running:
			// Only a claimed step task shows as running. A ready step waits
			// for its owner; it is not work in progress.
			marks[i] = stripRunning
		case active == 0 && hasDeliveryStep(steps, name):
			marks[i] = stripNotNeeded
		case active > 0 && passed == active:
			marks[i] = stripPassed
		}
	}
	return marks
}

func hasDeliveryStep(steps []protocol.StepStatus, name string) bool {
	for _, st := range steps {
		if name == "test after merge" && st.Step == "test" && st.AfterMerge {
			return true
		}
		if st.Step == name && !(name == "test" && st.AfterMerge) {
			return true
		}
	}
	return false
}

func hasStep(steps []protocol.StepStatus, name string) bool {
	for _, st := range steps {
		if st.Step == name {
			return true
		}
	}
	return false
}

// currentStep prioritizes owner intervention and failed work over ready steps.
// A failure sends the revision back to its author even when accept is ready.
func currentStep(v protocol.ChangeView) (protocol.StepRef, protocol.StepOwner, string, bool) {
	ownerOf := func(r protocol.StepRef) (protocol.StepOwner, string) {
		for _, st := range v.Steps {
			if st.StepRef == r {
				return st.Owner, st.State
			}
		}
		return protocol.StepOwner{}, ""
	}
	for _, state := range []string{protocol.StepStateNeedsOwner, protocol.StepStateFailed} {
		var latest *protocol.StepStatus
		for i := range v.Steps {
			st := &v.Steps[i]
			if st.State == state && (latest == nil || (st.Evidence != nil && (latest.Evidence == nil || st.Evidence.At > latest.Evidence.At))) {
				latest = st
			}
		}
		if latest != nil {
			return latest.StepRef, latest.Owner, latest.State, true
		}
	}
	if len(v.Next) > 0 {
		owner, state := ownerOf(v.Next[0])
		return v.Next[0], owner, state, true
	}
	for _, state := range []string{protocol.StepStateRunning, protocol.StepStateWaiting} {
		for _, st := range v.Steps {
			if st.State == state {
				return st.StepRef, st.Owner, st.State, true
			}
		}
	}
	return protocol.StepRef{}, protocol.StepOwner{}, "", false
}

// deliveryChangeFromView maps one change of the gateway. A view that has a
// pipeline is the full change (change.get): its evidence covers every revision.
// A list view has only the latest counted result of each step.
func deliveryChangeFromView(v protocol.ChangeView, now time.Time) deliveryChange {
	c := v.Change
	steps := append([]protocol.StepStatus(nil), v.Steps...)
	if c.State == protocol.ChangeDone || c.State == protocol.ChangeMerged || c.MergedCommit != "" {
		for i := range steps {
			if deliveryStepBeforeMerge(steps[i].Step) && !steps[i].AfterMerge && steps[i].State != protocol.StepStatePassed && steps[i].State != protocol.StepStateSkipped {
				steps[i].State = "unrecorded"
				steps[i].Note = "not recorded"
			}
		}
		filtered := make([]protocol.StepRef, 0, len(v.Next))
		for _, next := range v.Next {
			if !deliveryStepBeforeMerge(next.Step) || next.AfterMerge {
				filtered = append(filtered, next)
			}
		}
		v.Next = filtered
	}
	out := deliveryChange{ID: c.ID, Title: c.Title, Repo: c.Project, Branch: c.Branch, Head: c.HeadCommit, Revision: c.Revision,
		State: c.State, Caller: c.Caller, Updated: max(c.UpdatedAt, c.CreatedAt), Origin: c.Origin, MergedCommit: c.MergedCommit, Marks: deliveryMarks(steps), Full: v.Pipeline != nil,
		Signals: append([]protocol.QualitySignal(nil), v.Signals...)}
	for _, st := range steps {
		out.Steps = append(out.Steps, deliveryStep{Name: st.StepRef.String(), Owner: deliveryOwnerLabel(st.Owner), State: st.State, Note: st.Note})
	}
	v.Steps = steps
	if r, owner, state, ok := currentStep(v); ok {
		out.Step, out.StepOwner, out.OwnerKind, out.StepState = r.Step, deliveryOwnerLabel(owner), owner.Kind, state
		for _, st := range steps {
			if st.StepRef == r {
				out.ClaimedBy = deliveryClaimedSession(st.Note)
				if state == protocol.StepStateWaiting && strings.HasPrefix(st.Note, "waiting on ") {
					out.Wait = st.Note
				}
				if st.Evidence != nil {
					switch state {
					case protocol.StepStateFailed:
						out.Finding = st.Evidence.Note
					case protocol.StepStateNeedsOwner:
						out.Check = st.Evidence.Note
					}
				}
				break
			}
		}
	}

	raw := v.Evidence
	if !out.Full {
		for _, st := range steps {
			if st.Evidence != nil && st.Evidence.Fields["reconcile_kind"] == "" {
				raw = append(raw, *st.Evidence)
			}
		}
	}
	last := time.Unix(max(c.UpdatedAt, c.CreatedAt), 0)
	for _, e := range raw {
		out.Evidence = append(out.Evidence, deliveryEvidenceFrom(e, c))
		if at := time.Unix(e.At, 0); at.After(last) {
			last = at
		}
	}
	sort.SliceStable(out.Evidence, func(i, j int) bool { return out.Evidence[i].At.Before(out.Evidence[j].At) })
	out.Waiting = max(now.Sub(last), 0)
	return out
}

func deliveryStepBeforeMerge(step string) bool {
	switch step {
	case "accept", "review", "test", "approve":
		return true
	}
	return false
}

func deliveryClaimedSession(note string) string {
	const prefix = "claimed by "
	if i := strings.Index(note, prefix); i >= 0 {
		fields := strings.Fields(note[i+len(prefix):])
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

// deliveryEvidenceFrom maps one report. The session that is the change's
// caller shows as "caller"; owner, github and other sessions stay as they are.
func deliveryEvidenceFrom(e protocol.Evidence, c protocol.Change) deliveryEvidence {
	by := e.By
	if by != "" && by == c.Caller {
		by = "caller"
	}
	fields := map[string]string{}
	for k, v := range e.Fields {
		fields[k] = v
	}
	if e.For != "" {
		fields["for"] = e.For
	}
	if e.AfterMerge {
		fields["after_merge"] = "yes"
	}
	return deliveryEvidence{Step: e.Step, Result: deliveryResultText(e.Result), Commit: e.Commit, By: by, Model: e.ByModel, Fields: fields, At: time.Unix(e.At, 0)}
}

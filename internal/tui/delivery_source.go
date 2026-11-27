package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Sessions screen reads the gateway's changes through the daemon, with the
// owner's read changes_owner_find (the same data as the agent tool
// agent_changes_find, for every change and without a session). This file maps
// the protocol's ChangeView onto the small view the screen needs.

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

// heldChanges is one state's list as the gateway sent it.
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

func newGatewayDelivery(call func(context.Context, string, any) (json.RawMessage, error)) *gatewayDelivery {
	return &gatewayDelivery{call: call, held: map[string]heldChanges{}}
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

// Changes lists the changes the screen reads. Active (open and merged) comes
// first, so a long history of done changes never pushes live work off the
// list. It follows the pages of each state up to deliveryMaxChanges.
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
	add := func(views []protocol.ChangeView) {
		for _, v := range views {
			out = append(out, deliveryChangeFromView(v))
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

// deliveryChangeFromView maps one change of the gateway.
func deliveryChangeFromView(v protocol.ChangeView) deliveryChange {
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
	out := deliveryChange{ID: c.ID, Repo: c.Project, State: c.State}
	for _, st := range steps {
		out.Steps = append(out.Steps, deliveryStep{Name: st.StepRef.String(), State: st.State, Note: st.Note})
	}
	v.Steps = steps
	if r, owner, state, ok := currentStep(v); ok {
		out.Step, out.OwnerKind, out.StepState = r.Step, owner.Kind, state
		for _, st := range steps {
			if st.StepRef == r {
				out.ClaimedBy = deliveryClaimedSession(st.Note)
				break
			}
		}
	}
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

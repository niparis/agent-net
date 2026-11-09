package phone

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

// The Changes screen reads the delivery changes the TUI Delivery tab reads,
// with changes_owner_find. It is read only: a step is claimed, reported and
// approved from the TUI or by an agent, never from the web.

// changeFilters are the three views of the list, in the order the screen shows.
var changeFilters = []struct{ ID, Label string }{{"open", "open"}, {"mine", "mine to do"}, {"all", "all"}}

// perStateAll is how many changes of each state the "all" filter lists.
const perStateAll = 50

type filterLink struct {
	Label, Href string
	On          bool
}

type stepView struct {
	Name, State, Mark, Owner, Note, By string
}

type changeDetail struct {
	Title, ID, Project, Branch, Head, State, Status string
	Revision, Risk                                  int
	Author, Model, Caller, DiffStat                 string
	Steps                                           []stepView
}

type changesView struct {
	Filter  string
	Filters []filterLink
	Rows    []changeRow
	Detail  *changeDetail
}

// changeFilterID keeps a known filter and falls back to open.
func changeFilterID(f string) string {
	for _, c := range changeFilters {
		if c.ID == f {
			return f
		}
	}
	return "open"
}

// findChanges reads one page of changes. An error leaves the list empty: the
// screen then says nothing is open, and the next tick reads again.
func (a *app) findChanges(ctx context.Context, args protocol.ChangesFindArgs) []protocol.ChangeView {
	raw, err := a.read(ctx, protocol.OpChangesOwnerFind, args)
	if err != nil {
		return nil
	}
	var res protocol.ChangesFindResult
	if json.Unmarshal(raw, &res) != nil {
		return nil
	}
	return res.Items
}

// changeIsMine says a change waits for the owner: its step needs the owner, or
// the step is the owner's, or the caller is the owner's own session. It is the
// TUI's "needs you" group.
func changeIsMine(v protocol.ChangeView, ownerSession string) bool {
	c := v.Change
	if c.State == protocol.ChangeDone || c.State == protocol.ChangeStopped {
		return false
	}
	st, ok := currentStatus(v)
	if !ok {
		return false
	}
	switch st.State {
	case protocol.StepStateClosed, protocol.StepStateFailed:
		return false
	case protocol.StepStateNeedsOwner:
		return true
	case protocol.StepStatePassed, protocol.StepStateSkipped, protocol.StepStateWaiting, stepUnrecorded:
		return false
	}
	return st.Owner.Kind == protocol.OwnerPerson || (st.Owner.Kind == protocol.OwnerCaller && ownerSession != "" && c.Caller == ownerSession)
}

func (a *app) changes(ctx context.Context, snap operator.Snapshot, q query) *changesView {
	now := time.Now()
	filter := changeFilterID(q.Filter)
	v := &changesView{Filter: filter}
	for _, f := range changeFilters {
		href := "/changes?f=" + f.ID
		v.Filters = append(v.Filters, filterLink{Label: f.Label, Href: href, On: f.ID == filter})
	}
	var items []protocol.ChangeView
	if filter == "all" {
		for _, state := range []string{protocol.ChangeListActive, protocol.ChangeDone, protocol.ChangeStopped} {
			items = append(items, a.findChanges(ctx, protocol.ChangesFindArgs{State: state, Limit: perStateAll})...)
		}
	} else {
		items = a.findChanges(ctx, protocol.ChangesFindArgs{State: protocol.ChangeListActive, Limit: 100})
	}
	byID := map[string]protocol.ChangeView{}
	for _, it := range items {
		it.Steps = changeSteps(it)
		if _, seen := byID[it.Change.ID]; seen {
			continue
		}
		byID[it.Change.ID] = it
		if filter == "mine" && !changeIsMine(it, snap.OwnerSession) {
			continue
		}
		c := it.Change
		updated := time.Unix(max(c.UpdatedAt, c.CreatedAt), 0)
		_, state, _ := currentStep(it)
		marks, aria := changeMarks(it.Steps)
		v.Rows = append(v.Rows, changeRow{ID: c.ID, Title: firstNonEmpty(c.Title, c.Branch, c.ID), Project: c.Project, Revision: c.Revision, Risk: c.Risk,
			Marks: marks, Aria: aria, Status: changeStatusLine(it), Age: ago(updated, now), NeedsYou: state == protocol.StepStateNeedsOwner})
	}
	sort.SliceStable(v.Rows, func(i, j int) bool { return v.Rows[i].NeedsYou && !v.Rows[j].NeedsYou })
	chosen := -1
	for i := range v.Rows {
		if v.Rows[i].ID == q.Selected {
			chosen = i
		}
	}
	if chosen < 0 && len(v.Rows) > 0 {
		chosen = 0
	}
	if chosen >= 0 {
		v.Rows[chosen].Selected = true
		v.Detail = a.changeDetail(ctx, snap, byID[v.Rows[chosen].ID])
	}
	return v
}

// changeDetail reads one change in full: every revision's evidence. When the
// full read fails, the list's own view of the change is shown.
func (a *app) changeDetail(ctx context.Context, snap operator.Snapshot, listed protocol.ChangeView) *changeDetail {
	view := listed
	if full := a.findChanges(ctx, protocol.ChangesFindArgs{Change: listed.Change.ID}); len(full) > 0 {
		view = full[0]
	}
	view.Steps = changeSteps(view)
	c := view.Change
	names := map[string]string{}
	for _, ag := range snap.Agents {
		names[ag.SessionID] = sessionName(ag)
	}
	who := func(session string) string {
		if session == "" {
			return ""
		}
		return firstNonEmpty(names[session], "an ended session")
	}
	d := &changeDetail{Title: firstNonEmpty(c.Title, c.Branch, c.ID), ID: c.ID, Project: c.Project, Branch: c.Branch, State: c.State, Revision: c.Revision, Risk: c.Risk,
		Status: changeStatusLine(view), Caller: who(c.Caller), Model: c.Origin.Model}
	if len(c.HeadCommit) >= 8 {
		d.Head = c.HeadCommit[:8]
	}
	switch c.Origin.Kind {
	case protocol.OriginPerson:
		d.Author = firstNonEmpty(c.Origin.Person, "you")
	default:
		d.Author = firstNonEmpty(who(c.Origin.SessionID), c.Origin.Kind)
	}
	for _, st := range view.Steps {
		sv := stepView{Name: st.StepRef.String(), Owner: stepOwnerLabel(st.Owner)}
		switch st.State {
		case protocol.StepStatePassed:
			sv.State, sv.Mark = "passed", "p"
		case protocol.StepStateFailed:
			sv.State, sv.Mark = "failed", "f"
		case protocol.StepStateClosed:
			sv.State, sv.Mark = "closed", "f"
		case protocol.StepStateRunning:
			sv.State, sv.Mark = "running", "r"
		case protocol.StepStateNeedsOwner:
			sv.State, sv.Mark = "needs you", "w"
		case protocol.StepStateSkipped:
			sv.State, sv.Mark = "not needed", "s"
		case stepUnrecorded:
			sv.State, sv.Mark = "not recorded", "s"
		case protocol.StepStateReady:
			sv.State = "ready"
		default:
			sv.State = firstNonEmpty(st.State, "waiting")
		}
		var notes []string
		if st.Note != "" && st.State != stepUnrecorded {
			notes = append(notes, st.Note)
		}
		if e := st.Evidence; e != nil {
			if e.Note != "" {
				notes = append(notes, e.Note)
			}
			by := e.By
			switch {
			case by != "" && by == c.Caller:
				by = "caller"
			case by == protocol.EvidenceByOwner, by == protocol.EvidenceByGitHub:
			default:
				by = who(by)
			}
			sv.By = strings.TrimSpace(by + modelSuffix(e.ByModel))
			if stat := e.Fields["diff_stat"]; stat != "" && d.DiffStat == "" {
				d.DiffStat = stat
			}
		}
		sv.Note = strings.Join(notes, "\n")
		d.Steps = append(d.Steps, sv)
	}
	return d
}

func modelSuffix(model string) string {
	if model == "" {
		return ""
	}
	return " (" + model + ")"
}

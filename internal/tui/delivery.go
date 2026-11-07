package tui

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/protocol"
)

// deliverySteps are the steps of the pipeline, in the order the strip shows
// them.
var deliverySteps = []string{"accept", "review", "test", "approve", "merge", "test after merge", "release", "deploy", "verify"}

// deliveryChange is one change.
type deliveryChange struct {
	ID           string
	Title        string // what the work is, as the owner reads it
	Repo         string
	Branch       string
	MergedCommit string
	Head         string // the commit of the current revision
	Revision     int
	State        string // the change state: open, merged, done or stopped
	Caller       string // the session that gets the final notice
	Step         string // the step the change is in
	// StepOwner is who owns that step, as text: a node name, "caller",
	// "owner", "github". OwnerKind is the protocol kind behind it.
	StepOwner string
	OwnerKind string
	// StepState is the state of Step: ready, running, needs_owner, passed,
	// failed, waiting, skipped or closed. Empty when no step is current.
	StepState string
	ClaimedBy string
	Waiting   time.Duration
	Evidence  []deliveryEvidence
	Steps     []deliveryStep
	// Marks is the strip as the source worked it out from the step states. A
	// source that has no step states leaves it empty, and the strip follows
	// the evidence.
	Marks []stripState
	// Full says the evidence covers every revision, not only the latest
	// counted result of each step.
	Full     bool
	Group    deliveryGroup
	Count    int
	Children []deliveryChange
}

// deliveryStep is one pipeline step, including repeated test and approval steps.
type deliveryStep struct {
	Name, Owner, State, Note string
}

// deliveryEvidence is one step report, tied to one commit.
type deliveryEvidence struct {
	Step   string
	Result string // passed, failed, running, not needed
	Commit string
	By     string
	Model  string
	Fields map[string]string
	At     time.Time
}

// deliverySource gives the view its data. An empty repo means every repo.
type deliverySource interface {
	Changes(ctx context.Context, repo string) ([]deliveryChange, error)
}

// deliveryDetailSource is a source that can read one change in full. The list
// holds only the latest counted result of each step; the detail needs all of
// the evidence.
type deliveryDetailSource interface {
	Change(ctx context.Context, id string) (deliveryChange, error)
}

// deliveryLoadTimeout is how long the view waits for the source.
const deliveryLoadTimeout = 5 * time.Second

// deliveryRefreshEvery is how often changes load again.
const deliveryRefreshEvery = 30 * time.Second

// deliveryState is everything the view keeps. It sits in the Model by value
// and is replaced, never shared.
type deliveryState struct {
	on      bool // retained for compatibility with existing fixtures
	project string
	detail  bool // the evidence of the selected change is showing
	showAll bool // done and stale changes are on screen too
	row     int
	scroll  int // detail scroll, in lines
	loaded  bool
	changes []deliveryChange
	err     string

	loading       bool      // a list load is out
	loadedAt      time.Time // when the last load started
	detailLoading bool      // the full change is out
	detailErr     string
	expanded      map[string]bool
}

// deliveryStartMsg starts the first load through the normal in-flight guard.
type deliveryStartMsg struct{}

// deliveryLoadedMsg is the answer of one load.
type deliveryLoadedMsg struct {
	changes []deliveryChange
	err     error
}

// withDeliverySource sets where the view gets its changes.
func (m Model) withDeliverySource(s deliverySource) Model {
	m.deliverySrc = s
	return m
}

// deliveryLoad asks the source for the changes. With no source it does nothing
// and the view shows its empty state.
func (m Model) deliveryLoad() tea.Cmd {
	src := m.deliverySrc
	if src == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), deliveryLoadTimeout)
		defer cancel()
		changes, err := src.Changes(ctx, "")
		return deliveryLoadedMsg{changes: changes, err: err}
	}
}

// deliveryDetailMsg is the answer of one read of a change in full.
type deliveryDetailMsg struct {
	id     string
	change deliveryChange
	err    error
}

// startDeliveryLoad marks a load as out and returns the command. A load that
// is already out is not asked again.
func (m Model) startDeliveryLoad() (Model, tea.Cmd) {
	if m.deliverySrc == nil || m.delivery.loading {
		return m, nil
	}
	m.delivery.loading, m.delivery.loadedAt = true, m.now()
	return m, m.deliveryLoad()
}

// deliveryTick loads again after deliveryRefreshEvery
// since the last load.
func (m Model) deliveryTick() (Model, tea.Cmd) {
	d := m.delivery
	if d.loading || m.now().Sub(d.loadedAt) < deliveryRefreshEvery {
		return m, nil
	}
	return m.startDeliveryLoad()
}

// deliveryGroup is one band of the list. The bands appear in this order, so a
// change the owner has to act on is never below one that waits on an agent.
type deliveryGroup int

const (
	groupNeedsYou deliveryGroup = iota
	groupMoving
	groupStuck
	groupDone
)

func (g deliveryGroup) title() string {
	switch g {
	case groupNeedsYou:
		return "Needs you"
	case groupMoving:
		return "Moving"
	case groupStuck:
		return "Stuck"
	}
	return "Done"
}

// deliveryGroupOf says who has to act on a change next.
func deliveryGroupOf(ch deliveryChange, ownerSession string) deliveryGroup {
	if ch.State == protocol.ChangeDone || ch.State == protocol.ChangeStopped || ch.StepState == protocol.StepStateClosed {
		return groupDone
	}
	if ch.StepState == protocol.StepStateFailed {
		return groupMoving
	}
	// A passed, skipped or blocked step has no action for anyone yet.
	switch ch.StepState {
	case "", protocol.StepStatePassed, protocol.StepStateSkipped, protocol.StepStateWaiting:
		if ch.Waiting >= 12*time.Hour {
			return groupStuck
		}
		return groupMoving
	}
	switch ch.OwnerKind {
	case protocol.OwnerPerson:
		return groupNeedsYou
	}
	if ch.OwnerKind == protocol.OwnerCaller && ownerSession != "" && ch.Caller == ownerSession {
		return groupNeedsYou
	}
	if ch.Waiting >= 12*time.Hour {
		return groupStuck
	}
	return groupMoving
}

func (m Model) deliveryGroupFor(ch deliveryChange) deliveryGroup {
	if ch.StepState == protocol.StepStateFailed {
		return groupMoving
	}
	if group := deliveryGroupOf(ch, m.snap.OwnerSession); group == groupNeedsYou || group == groupDone {
		return group
	}
	for _, item := range m.inboxItems {
		if item.Status == "open" && item.Kind == "approval" && item.Delivery != nil && item.Delivery.ChangeID == ch.ID {
			return groupNeedsYou
		}
	}
	return deliveryGroupOf(ch, m.snap.OwnerSession)
}

// deliveryStale says nothing happened on a change for a day and it is not
// merged. A merged change may wait for its after-merge steps for as long as it
// takes; an open one that nobody touched is abandoned or superseded.
func deliveryStale(ch deliveryChange) bool {
	switch ch.State {
	case protocol.ChangeMerged, protocol.ChangeDone, protocol.ChangeStopped:
		return false
	}
	if ch.StepState == protocol.StepStateClosed {
		return false
	}
	return ch.Waiting >= 24*time.Hour
}

// deliveryVisible is the list on screen: grouped by who has to act, with done
// and stale rows left out until the owner asks for them.
func (m Model) deliveryVisible() []deliveryChange {
	var out []deliveryChange
	for _, ch := range m.delivery.changes {
		group := m.deliveryGroupFor(ch)
		if m.delivery.project != "" && ch.Repo != m.delivery.project {
			continue
		}
		if !m.delivery.showAll && (group == groupDone || deliveryStale(ch)) {
			continue
		}
		out = append(out, ch)
	}
	sort.SliceStable(out, func(i, j int) bool { return m.deliveryGroupFor(out[i]) < m.deliveryGroupFor(out[j]) })
	var rows []deliveryChange
	stuck := map[string][]deliveryChange{}
	for _, ch := range out {
		g := m.deliveryGroupFor(ch)
		if g != groupStuck {
			ch.Group, ch.Count = g, 1
			rows = append(rows, ch)
			continue
		}
		key := ch.Repo + "\x00" + ch.Step + "\x00" + ch.StepOwner
		stuck[key] = append(stuck[key], ch)
	}
	for _, group := range stuck {
		if len(group) == 1 {
			ch := group[0]
			ch.Group, ch.Count = groupStuck, 1
			rows = append(rows, ch)
			continue
		}
		first := group[0]
		minAge, maxAge := group[0].Waiting, group[0].Waiting
		for _, ch := range group {
			minAge = min(minAge, ch.Waiting)
			maxAge = max(maxAge, ch.Waiting)
		}
		sort.SliceStable(group, func(i, j int) bool { return group[i].ID < group[j].ID })
		first.Group, first.Count, first.Children = groupStuck, len(group), group
		who := ""
		if presence := m.deliveryOwnerPresence(first.StepOwner); presence != "" {
			who = " (" + presence + ")"
		}
		first.Title = first.Repo + " · " + strconv.Itoa(len(group)) + " changes wait for " + first.Step + " by " + first.StepOwner + who + " · " + deliveryHourText(minAge) + "-" + deliveryHourText(maxAge)
		rows = append(rows, first)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Group != rows[j].Group {
			return rows[i].Group < rows[j].Group
		}
		if rows[i].Repo != rows[j].Repo {
			return rows[i].Repo < rows[j].Repo
		}
		if rows[i].Step != rows[j].Step {
			return rows[i].Step < rows[j].Step
		}
		return rows[i].StepOwner < rows[j].StepOwner
	})
	var expanded []deliveryChange
	for _, row := range rows {
		if len(row.Children) > 1 && m.delivery.expanded[row.ID] {
			expanded = append(expanded, row.Children...)
		} else {
			expanded = append(expanded, row)
		}
	}
	return expanded
}

func (m Model) deliveryOwnerPresence(name string) string {
	for _, a := range m.snap.Agents {
		if strings.EqualFold(strings.TrimSpace(a.Profile.Name), strings.TrimSpace(name)) {
			if a.Online {
				return "online"
			}
			return "exited"
		}
	}
	return ""
}

func deliveryHourText(d time.Duration) string {
	h := int(d.Hours())
	if h < 1 {
		return "<1h"
	}
	return strconv.Itoa(h) + "h"
}

// selectedChange is the change under the cursor.
func (m Model) selectedChange() (deliveryChange, bool) {
	visible := m.deliveryVisible()
	if len(visible) == 0 {
		return deliveryChange{}, false
	}
	return visible[min(max(m.delivery.row, 0), len(visible)-1)], true
}

// deliveryDetailCmd reads one change in full. A source that cannot do it
// leaves the list's evidence on screen.
func (m Model) deliveryDetailCmd(id string) tea.Cmd {
	src, ok := m.deliverySrc.(deliveryDetailSource)
	if !ok || id == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), deliveryLoadTimeout)
		defer cancel()
		c, err := src.Change(ctx, id)
		return deliveryDetailMsg{id: id, change: c, err: err}
	}
}

// deliveryLoaded takes an answer. A failed load keeps the last list on screen
// and shows the error above it. The cursor stays on the same change when the
// list changes under it, and an open detail keeps its full evidence until the
// fresh one arrives.
func (m Model) deliveryLoaded(msg deliveryLoadedMsg) (Model, tea.Cmd) {
	d := m.delivery
	prev, had := m.selectedChange()
	d.loaded, d.loading = true, false
	if msg.err != nil {
		d.err = msg.err.Error()
	} else {
		d.err = ""
		changes := msg.changes
		for i := range changes {
			if had && changes[i].ID == prev.ID {
				if prev.Full && !changes[i].Full && prev.Revision == changes[i].Revision && sameCommit(prev.Head, changes[i].Head) {
					changes[i].Evidence, changes[i].Full = prev.Evidence, true
				}
			}
		}
		d.changes = changes
	}
	m.delivery = d
	visible := m.deliveryVisible()
	found := false
	for i := range visible {
		if had && visible[i].ID == prev.ID {
			d.row, found = i, true
		}
	}
	if had && !found {
		d.detail = false
	}
	d.row = min(max(d.row, 0), max(len(visible)-1, 0))
	if len(visible) == 0 {
		d.detail = false
	}
	m.delivery = d
	if sel, ok := m.selectedChange(); ok && d.detail && msg.err == nil {
		m.delivery.detailLoading = true
		return m, m.deliveryDetailCmd(sel.ID)
	}
	return m, nil
}

// deliveryDetailLoaded puts the full change into the list, in the place of the
// change with the same ID.
func (m Model) deliveryDetailLoaded(msg deliveryDetailMsg) Model {
	d := m.delivery
	if sel, ok := m.selectedChange(); ok && sel.ID == msg.id {
		d.detailLoading = false
	}
	if msg.err != nil {
		d.detailErr = msg.err.Error()
		m.delivery = d
		return m
	}
	d.detailErr = ""
	for i := range d.changes {
		if d.changes[i].ID == msg.id {
			d.changes[i] = msg.change
		}
	}
	m.delivery = d
	return m
}

// nextQuotasSubtab toggles the quota views.
func (m Model) nextQuotasSubtab() (Model, tea.Cmd) {
	switch {
	case m.qualityDetail:
		m.qualityDetail = false
	case m.workersLog:
		m.workersLog = false
		m.qualityDetail = true
	default:
		m.workersLog = true
	}
	m.quotaScroll = 0
	if !m.workersLog {
		m.workerLogDetailsFocus = false
	}
	return m, nil
}

func (m Model) deliveryKey(k tea.KeyPressMsg) (Model, tea.Cmd) {
	d := m.delivery
	action := keyAction(contextDelivery, k.String())
	if action == "all" && d.project != "" {
		d.project, d.detail, d.row = "", false, 0
		m.delivery = d
		return m, nil
	}
	if action == "all" {
		d.showAll = !d.showAll
		d.detail = false
		m.delivery = d
		d.row = min(max(d.row, 0), max(len(m.deliveryVisible())-1, 0))
		m.delivery = d
		return m, nil
	}
	last := max(len(m.deliveryVisible())-1, 0)
	switch action {
	case "up":
		if d.detail {
			d.scroll = max(d.scroll-1, 0)
		} else {
			d.row = max(d.row-1, 0)
		}
	case "down":
		if d.detail {
			d.scroll++
		} else {
			d.row = min(d.row+1, last)
		}
	case "page up":
		if d.detail {
			d.scroll = max(d.scroll-10, 0)
		} else {
			d.row = max(d.row-10, 0)
		}
	case "page down":
		if d.detail {
			d.scroll += 10
		} else {
			d.row = min(d.row+10, last)
		}
	case "open":
		if sel, ok := m.selectedChange(); ok && !d.detail && len(sel.Children) > 1 {
			if d.expanded == nil {
				d.expanded = map[string]bool{}
			}
			d.expanded[sel.ID] = !d.expanded[sel.ID]
			m.delivery = d
			return m, nil
		}
		if sel, ok := m.selectedChange(); ok && !d.detail {
			d.detail, d.scroll, d.detailErr = true, 0, ""
			cmd := m.deliveryDetailCmd(sel.ID)
			d.detailLoading = cmd != nil
			m.delivery = d
			return m, cmd
		}
	case "back":
		if !d.detail {
			d.project, d.row = "", 0
		}
		d.detail, d.detailErr, d.detailLoading = false, "", false
	case "refresh":
		// Do not overlap a load that is already in flight.
		m.delivery = d
		return m.startDeliveryLoad()
	case "help":
		m.delivery = d
		m.openHelp()
		return m, nil
	case "quit":
		m.quitting = true
		return m, tea.Quit
	}
	m.delivery = d
	return m, nil
}

// ---- evidence strip ----

type stripState int

const (
	stripWaiting stripState = iota // no evidence for this commit yet
	stripPassed
	stripFailed
	stripRunning
	stripNotNeeded
)

// normalResult maps the words a source may use onto the five states.
func normalResult(result string) stripState {
	switch strings.ToLower(strings.TrimSpace(strings.ReplaceAll(result, "_", " "))) {
	case "passed", "pass", "ok", "accepted", "approved", "done":
		return stripPassed
	case "failed", "fail", "refused", "rejected", "sent back", "closed":
		return stripFailed
	case "running", "started", "in progress", "needs you":
		return stripRunning
	case "not needed", "skipped", "none":
		return stripNotNeeded
	}
	return stripWaiting
}

// sameCommit says whether two commit names are the same commit. One may be a
// short form of the other.
func sameCommit(a, b string) bool {
	a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	return a != "" && b != "" && (strings.HasPrefix(a, b) || strings.HasPrefix(b, a))
}

// deliveryStripStates gives one state per step. A source that worked the marks
// out from the step states gives them as they are. Otherwise only evidence for the
// change's head counts: a pass for an older revision is waiting again. The
// newest evidence for a step wins.
func deliveryStripStates(c deliveryChange) []stripState {
	out := make([]stripState, len(deliverySteps))
	if len(c.Marks) == len(deliverySteps) {
		copy(out, c.Marks)
		return out
	}
	for i, step := range deliverySteps {
		var newest *deliveryEvidence
		for j := range c.Evidence {
			e := &c.Evidence[j]
			wantStep, commit := step, c.Head
			if step == "test after merge" {
				wantStep, commit = "test", c.MergedCommit
			}
			if e.Step != wantStep || (wantStep == "test" && (e.Fields["after_merge"] == "yes") != (step == "test after merge")) || !sameCommit(e.Commit, commit) {
				continue
			}
			if newest == nil || !e.At.Before(newest.At) {
				newest = e
			}
		}
		if newest != nil {
			out[i] = normalResult(newest.Result)
		}
	}
	return out
}

// shortBranch drops the prefixes that every branch of a kind shares, so the
// part that tells branches apart is what the column shows.
func shortBranch(b string) string {
	b = strings.TrimPrefix(sanitize(strings.TrimSpace(b)), "refs/heads/")
	b = strings.TrimPrefix(b, "agent-net/")
	return firstNonEmpty(b, "-")
}

// shortCommit is the first seven characters of a commit.
func shortCommit(c string) string {
	c = sanitize(strings.TrimSpace(c))
	if len(c) > 7 {
		c = c[:7]
	}
	return firstNonEmpty(c, "-")
}

// deliveryWaitText is a compact duration: 6m, 2h14m, 1d2h.
func deliveryWaitText(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h" + strconv.Itoa(int(d%time.Hour/time.Minute)) + "m"
	case d < 100*24*time.Hour:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d" + strconv.Itoa(int(d%(24*time.Hour)/time.Hour)) + "h"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + "d"
}

// deliveryHints is the key line of the Delivery tab.
func (m Model) deliveryHints() []hint {
	help, quit := bindingHint(contextDelivery, "help"), bindingHint(contextDelivery, actQuit)
	if m.delivery.detail {
		scroll := bindingPairHint(contextDelivery, actUp, actDown)
		scroll.label = "scroll"
		return []hint{scroll, bindingHint(contextDelivery, "back"), help, quit}
	}
	return []hint{bindingPairHint(contextDelivery, actUp, actDown), bindingHint(contextDelivery, "open"), bindingHint(contextDelivery, "all"), bindingHint(contextDelivery, "refresh"), help, quit}
}

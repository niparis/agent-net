package tui

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/operator"
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
	Origin       protocol.ChangeOrigin
	Caller       string // the session that gets the final notice
	Updated      int64  // when the change last moved, in Unix seconds
	Step         string // the step the change is in
	// StepOwner is who owns that step, as text: a node name, "caller",
	// "owner", "github". OwnerKind is the protocol kind behind it.
	StepOwner string
	OwnerKind string
	// StepState is the state of Step: ready, running, needs_owner, passed,
	// failed, waiting, skipped or closed. Empty when no step is current.
	StepState string
	Wait      string // the current step's event wait, including its optional due time
	Finding   string // the current failed step's short finding
	Check     string // the current needs_owner step's note: what the owner checks
	ClaimedBy string
	Waiting   time.Duration
	Evidence  []deliveryEvidence
	// Signals are the free quality signals of the current revision. They
	// inform the owner; they never block a step.
	Signals []protocol.QualitySignal
	Steps   []deliveryStep
	// Marks is the strip as the source worked it out from the step states. A
	// source that has no step states leaves it empty, and the strip follows
	// the evidence.
	Marks []stripState
	// Full says the evidence covers every revision, not only the latest
	// counted result of each step.
	Full bool
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

// deliveryFullReadEvery is how often a load asks for every page again, though
// the gateway says the first page of a state is the same. Later pages hold old
// changes; a change on one is shown within this time.
const deliveryFullReadEvery = 2 * time.Minute

// inboxRefreshEvery is how often the Inbox lists are checked on a tab that
// does not show them, so its count is never stale for long.
const inboxRefreshEvery = 10 * time.Second

// frictionRefreshEvery is how often the Work tab reads the friction reports.
// They arrive when a worker hands back, so a minute is enough.
const frictionRefreshEvery = time.Minute

// deliveryState is everything the view keeps. It sits in the Model by value
// and is replaced, never shared.
type deliveryState struct {
	on      bool // retained for compatibility with existing fixtures
	detail  bool // the evidence of the selected change is showing
	row     int  // the index of that change in changes
	scroll  int  // detail scroll, in lines
	loaded  bool
	changes []deliveryChange
	err     string
	// slow is set when the last load ended by its deadline. The view then says
	// "slow, retrying" and keeps the last list. retried is set once the
	// background retry was made, so a second timeout waits for the next
	// regular refresh instead of asking again at once.
	slow, retried bool

	loading      bool      // a list load is out
	loadedAt     time.Time // when the last load started
	fullAt       time.Time // when the last load that asked for every page started
	topicVersion uint64
	topicSeen    bool
	// dataVersion is the changes topic version the held list was read at,
	// and dataAt is when that read happened.
	dataVersion   uint64
	dataAt        time.Time
	detailLoading bool // the full change is out
	detailErr     string
}

// deliveryStartMsg starts the first load through the normal in-flight guard.
type deliveryStartMsg struct{}

// deliveryLoadedMsg is the answer of one load.
type deliveryLoadedMsg struct {
	changes []deliveryChange
	err     error
	// version is the changes topic version at the start of the load.
	version uint64
	// at is when the load started.
	at time.Time
	// notModified means the list on screen is still current.
	notModified bool
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
	version, at := m.topicVersions[protocol.TopicChanges], m.now()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), deliveryLoadTimeout)
		defer cancel()
		changes, err := src.Changes(ctx, "")
		if errors.Is(err, errDeliveryNotModified) {
			return deliveryLoadedMsg{notModified: true, version: version, at: at}
		}
		return deliveryLoadedMsg{changes: changes, err: err, version: version, at: at}
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
	if v, ok := m.topicVersions[protocol.TopicChanges]; ok {
		m.delivery.topicVersion, m.delivery.topicSeen = v, true
	}
	if f, ok := m.deliverySrc.(interface{ SetFullRead(bool) }); ok {
		// Pages the gateway says are unchanged are not asked for again, except
		// that every deliveryFullReadEvery all of them are, so a change on a
		// later page is never missed for long.
		full := m.delivery.fullAt.IsZero() || m.now().Sub(m.delivery.fullAt) >= deliveryFullReadEvery
		if full {
			m.delivery.fullAt = m.now()
		}
		f.SetFullRead(full)
	}
	if v, ok := m.deliverySrc.(interface{ SetIfVersion(uint64) }); ok {
		held := uint64(0)
		// Like the snapshot parts, the list is only current while it was read
		// inside the worker window: rows change with time even when the
		// changes tables do not.
		if m.delivery.dataVersion != 0 && !m.delivery.dataAt.IsZero() && m.now().Sub(m.delivery.dataAt) < protocol.RecentWorkerWindow {
			held = m.delivery.dataVersion
		}
		v.SetIfVersion(held)
	}
	return m, m.deliveryLoad()
}

// deliveryTick loads again after deliveryRefreshEvery
// since the last load. Only the Sessions table shows a change state, so other
// tabs do not ask for it.
func (m Model) deliveryTick() (Model, tea.Cmd) {
	if m.tab != sessionsTab {
		return m, nil
	}
	d := m.delivery
	v, haveVersion := m.topicVersions[protocol.TopicChanges]
	changed := m.topicActive && haveVersion && (!d.topicSeen || v > d.topicVersion)
	// A changed topic loads again at once after a quiet time, and no sooner
	// than topicRefreshGap after the last load on a busy one: the changes
	// topic moves with every message and event.
	since := m.now().Sub(d.loadedAt)
	if d.loading || (changed && since < topicRefreshGap) || (!changed && since < deliveryRefreshEvery) {
		return m, nil
	}
	return m.startDeliveryLoad()
}

// deliveryOwnerPresence says whether an agent with this name is online.
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

// selectedChange is the change whose detail is open.
func (m Model) selectedChange() (deliveryChange, bool) {
	if len(m.delivery.changes) == 0 {
		return deliveryChange{}, false
	}
	return m.delivery.changes[min(max(m.delivery.row, 0), len(m.delivery.changes)-1)], true
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

// deliveryLoaded takes an answer. A failed load keeps the last changes on
// screen and shows the error above them. The open detail stays on the same
// change when the list changes under it, and keeps its full evidence until the
// fresh one arrives.
func (m Model) deliveryLoaded(msg deliveryLoadedMsg) (Model, tea.Cmd) {
	d := m.delivery
	prev, had := m.selectedChange()
	d.loaded, d.loading = true, false
	if msg.notModified {
		// The topic did not move: the list on screen is still current and
		// costs no bytes to keep. The read time does not move, so a list that
		// gets old is read in full again.
		d.err, d.slow, d.retried, d.dataVersion = "", false, false, msg.version
		m.delivery = d
		return m, nil
	}
	retry := false
	switch {
	case msg.err != nil && operator.IsSlow(msg.err):
		d.err, d.slow = "", true
		retry, d.retried = !d.retried, true
	case msg.err != nil:
		d.err, d.slow, d.retried = msg.err.Error(), false, false
	default:
		d.err, d.slow, d.retried = "", false, false
		changes := msg.changes
		for i := range changes {
			if had && changes[i].ID == prev.ID {
				if prev.Full && !changes[i].Full && prev.Revision == changes[i].Revision && sameCommit(prev.Head, changes[i].Head) {
					changes[i].Evidence, changes[i].Full = prev.Evidence, true
				}
			}
		}
		d.changes = changes
		d.dataVersion, d.dataAt = msg.version, msg.at
	}
	found := false
	for i := range d.changes {
		if had && d.changes[i].ID == prev.ID {
			d.row, found = i, true
		}
	}
	if had && !found {
		d.detail = false
	}
	d.row = min(max(d.row, 0), max(len(d.changes)-1, 0))
	if len(d.changes) == 0 {
		d.detail = false
	}
	m.delivery = d
	if retry {
		return m.startDeliveryLoad()
	}
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
	if !m.workersLog {
		// The Quotas panel and the Quality subtab draw the picker table, so
		// they read it when it comes back on screen.
		m.pickerWanted = true
	}
	if m.qualityDetail {
		m.weeklyWanted = true
	}
	return m, nil
}

// deliveryKey handles the keys of an open change detail.
func (m Model) deliveryKey(k tea.KeyPressMsg) (Model, tea.Cmd) {
	action := keyAction(contextDelivery, k.String())
	d := m.delivery
	switch action {
	case "up":
		d.scroll = max(d.scroll-1, 0)
	case "down":
		d.scroll++
	case "page up":
		d.scroll = max(d.scroll-10, 0)
	case "page down":
		d.scroll += 10
	case "back":
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

// deliveryHints is the key line of an open change detail.
func (m Model) deliveryHints() []hint {
	help, quit := bindingHint(contextDelivery, "help"), bindingHint(contextDelivery, actQuit)
	scroll := bindingPairHint(contextDelivery, actUp, actDown)
	scroll.label = "scroll"
	return []hint{scroll, bindingHint(contextDelivery, "back"), help, quit}
}

package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

// deliveryChange is the part of a change the Sessions screen reads: its state,
// the step it is in, and the session that claims that step.
type deliveryChange struct {
	ID        string
	Repo      string
	State     string // the change state: open, merged, done or stopped
	Step      string // the step the change is in
	OwnerKind string // the protocol owner kind behind Step
	StepState string // the state of Step
	ClaimedBy string // the session that claimed a running step
	Steps     []deliveryStep
}

// deliveryStep is one pipeline step, including repeated test and approval steps.
type deliveryStep struct {
	Name, State, Note string
}

// deliverySource gives the Sessions screen its changes. An empty repo means
// every repo.
type deliverySource interface {
	Changes(ctx context.Context, repo string) ([]deliveryChange, error)
}

// deliveryLoadTimeout is how long a load waits for the source.
const deliveryLoadTimeout = 5 * time.Second

// deliveryRefreshEvery is how often changes load again.
const deliveryRefreshEvery = 30 * time.Second

// deliveryFullReadEvery is how often a load asks for every page again, though
// the gateway says the first page of a state is the same. Later pages hold old
// changes; a change on one is shown within this time.
const deliveryFullReadEvery = 2 * time.Minute

// chiefRefreshEvery is how often the chief's status is checked on a tab
// that does not show the CTO box, so it is never stale for long.
const chiefRefreshEvery = 10 * time.Second

// frictionRefreshEvery is how often the Work tab reads the friction reports.
// They arrive when a worker hands back, so a minute is enough.
const frictionRefreshEvery = time.Minute

// deliveryState is everything the loader keeps. It sits in the Model by value
// and is replaced, never shared.
type deliveryState struct {
	loaded  bool
	changes []deliveryChange
	err     string
	// slow is set when the last load ended by its deadline. retried is set
	// once the background retry was made, so a second timeout waits for the
	// next regular refresh instead of asking again at once.
	slow, retried bool

	loading      bool      // a list load is out
	loadedAt     time.Time // when the last load started
	fullAt       time.Time // when the last load that asked for every page started
	topicVersion uint64
	topicSeen    bool
	// dataVersion is the changes topic version the held list was read at,
	// and dataAt is when that read happened.
	dataVersion uint64
	dataAt      time.Time
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

// withDeliverySource sets where the Sessions screen gets its changes.
func (m Model) withDeliverySource(s deliverySource) Model {
	m.deliverySrc = s
	return m
}

// deliveryLoad asks the source for the changes. With no source it does nothing.
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

// deliveryTick loads again after deliveryRefreshEvery since the last load. Only
// the Sessions screen shows a change state, so other tabs do not ask for it.
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

// deliveryLoaded takes an answer. A failed load keeps the last changes and
// shows the error. A load that ended by its deadline is retried once.
func (m Model) deliveryLoaded(msg deliveryLoadedMsg) (Model, tea.Cmd) {
	d := m.delivery
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
		d.changes = msg.changes
		d.dataVersion, d.dataAt = msg.version, msg.at
	}
	m.delivery = d
	if retry {
		return m.startDeliveryLoad()
	}
	return m, nil
}

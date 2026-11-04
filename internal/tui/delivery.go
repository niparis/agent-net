package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// The Delivery view shows the open changes of the delivery pipeline (see
// docs/design/delivery-pipeline.md, "What the owner sees"). It lives as the
// third sub-tab of the Quotas tab, after the Workers log: it is a list of
// things the owner watches, like the log, and a tab of its own would break the
// seven numbered tabs (1-7) that every key hint and the help overlay name.
//
// The view reads a small interface with its own types. A later step maps the
// gateway's real types onto them, so nothing here imports the protocol.

// deliverySteps are the steps of the pipeline, in the order the strip shows
// them.
var deliverySteps = []string{"accept", "review", "test", "approve", "merge", "release", "deploy", "verify"}

// deliveryChange is one open change.
type deliveryChange struct {
	ID        string
	Repo      string
	Branch    string
	Head      string // the commit of the current revision
	Revision  int
	Step      string // the step the change is in
	StepOwner string // who owns that step: a node, "caller", "owner", "github"
	Waiting   time.Duration
	Evidence  []deliveryEvidence
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

// deliveryLoadTimeout is how long the view waits for the source.
const deliveryLoadTimeout = 5 * time.Second

// deliveryState is everything the view keeps. It sits in the Model by value
// and is replaced, never shared.
type deliveryState struct {
	on      bool // the Delivery sub-tab is showing
	detail  bool // the evidence of the selected change is showing
	row     int
	scroll  int // detail scroll, in lines
	loaded  bool
	changes []deliveryChange
	err     string
}

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

// deliveryLoaded takes an answer. A failed load keeps the last list on screen
// and shows the error above it.
func (m Model) deliveryLoaded(msg deliveryLoadedMsg) Model {
	d := m.delivery
	d.loaded = true
	if msg.err != nil {
		d.err = msg.err.Error()
	} else {
		d.err = ""
		d.changes = msg.changes
	}
	d.row = min(max(d.row, 0), max(len(d.changes)-1, 0))
	if len(d.changes) == 0 {
		d.detail = false
	}
	m.delivery = d
	return m
}

// nextQuotasSubtab moves Quotas -> Workers log -> Delivery -> Quotas. It
// returns the command that loads the changes when Delivery opens.
func (m Model) nextQuotasSubtab() (Model, tea.Cmd) {
	switch {
	case m.delivery.on:
		m.delivery.on, m.delivery.detail = false, false
	case m.workersLog:
		m.workersLog = false
		m.delivery.on = true
		return m, m.deliveryLoad()
	default:
		m.workersLog = true
	}
	return m, nil
}

func (m Model) deliveryKey(k tea.KeyMsg) (Model, tea.Cmd) {
	d := m.delivery
	last := max(len(d.changes)-1, 0)
	switch keyAction(contextDelivery, k.String()) {
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
		if !d.detail && len(d.changes) > 0 {
			d.detail, d.scroll = true, 0
		}
	case "back":
		d.detail = false
	case "refresh":
		m.delivery = d
		return m, m.deliveryLoad()
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
	case "failed", "fail", "refused", "rejected", "sent back":
		return stripFailed
	case "running", "started", "in progress":
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

// deliveryStripStates gives one state per step. Only evidence for the
// change's head counts: a pass for an older revision is waiting again. The
// newest evidence for a step wins.
func deliveryStripStates(c deliveryChange) []stripState {
	out := make([]stripState, len(deliverySteps))
	for i, step := range deliverySteps {
		var newest *deliveryEvidence
		for j := range c.Evidence {
			e := &c.Evidence[j]
			if e.Step != step || !sameCommit(e.Commit, c.Head) {
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

// deliveryHints is the key line of the Delivery sub-tab.
func (m Model) deliveryHints() []hint {
	help, quit := bindingHint(contextDelivery, "help"), bindingHint(contextDelivery, actQuit)
	if m.delivery.detail {
		return []hint{bindingPairHint(contextDelivery, actUp, actDown), bindingHint(contextDelivery, "back"), help, quit}
	}
	return []hint{bindingHint(contextDelivery, "workers log"), bindingPairHint(contextDelivery, actUp, actDown), bindingHint(contextDelivery, "open"), bindingHint(contextDelivery, "refresh"), help, quit}
}

package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

// This file is the CPU and memory side of the model: what the daemon told us,
// how fresh it is, and how figures are written. It knows nothing about the
// terminal (decision 0007).

// usageEvery is how often the screen asks for usage and counts. The daemons
// report every 10 seconds, so asking each second would add nothing.
const usageEvery = 5 * time.Second

// topEvery is how long the top processes of a session are kept before they
// are fetched again.
const topEvery = 5 * time.Second

// Thresholds that mark a heavy process tree, and a loaded machine.
const (
	HeavyProcs  = 20
	HeavyCPU    = 200.0   // percent of one core
	HeavyRSS    = 4 << 30 // bytes
	hotMachine  = 85.0    // percent of all cores, or of memory
	warmMachine = 60.0
)

// usageState is how much we know about a machine or a session tree.
type usageState int

const (
	usageNone  usageState = iota // no report (older agent-net, offline, no process)
	usageStale                   // a report exists but it is too old
	usageOK
)

// usageData is the last usage answer, with the time we got it.
type usageData struct {
	at          time.Time
	have        bool
	raw         Usage
	counts      *protocol.SessionCounts
	unavailable string // why there is no usage (an older daemon or gateway)
	noCounts    bool   // counts are not available: do not ask again
}

// topEntry is the cached top processes of one session.
type topEntry struct {
	at      time.Time
	loading bool
	have    bool
	top     protocol.UsageTop
	err     string
}

// nodeUsage returns the report of a machine. This machine's own report is
// read live; it replaces the gateway's copy.
func (m Model) nodeUsage(nodeID string) (protocol.UsageNode, usageState) {
	if !m.usage.have {
		return protocol.UsageNode{}, usageNone
	}
	since := int64(0)
	if d := m.now().Sub(m.usage.at); d > 0 {
		since = int64(d / time.Second)
	}
	var n protocol.UsageNode
	found := false
	if g := m.usage.raw.Gateway; g != nil {
		for _, x := range g.Nodes {
			if x.NodeID == nodeID {
				n, found = x, true
				n.AgeSeconds += since
			}
		}
	}
	if l := m.usage.raw.Local; l != nil && nodeID == m.usage.raw.NodeID {
		n = protocol.UsageNode{NodeID: nodeID, Hostname: m.usage.raw.Hostname, AgeSeconds: since, Usage: *l}
		found = true
	}
	switch {
	case !found:
		return n, usageNone
	case n.AgeSeconds > protocol.UsageStaleSeconds:
		return n, usageStale
	}
	return n, usageOK
}

// treeOf finds the process tree of a session.
func (m Model) treeOf(a protocol.AgentView) (protocol.TreeUsage, usageState) {
	n, st := m.nodeUsage(a.NodeID)
	if st != usageOK {
		return protocol.TreeUsage{}, st
	}
	for _, t := range n.Usage.Trees {
		for _, s := range t.Sessions {
			if s == a.SessionID {
				return t, usageOK
			}
		}
	}
	return protocol.TreeUsage{}, usageNone
}

// sharedTree tells whether the tree is one process several sessions share. A
// tree marked shared with exactly one session is that session's own private
// server: its figures belong on the session's row.
func sharedTree(t protocol.TreeUsage) bool {
	return t.Shared && len(t.Sessions) > 1
}

// sharedService returns the shared OpenCode service of a machine, if it has
// one. Its figures go in the machine heading, not on a session row.
func (m Model) sharedService(nodeID string) (protocol.TreeUsage, usageState) {
	n, st := m.nodeUsage(nodeID)
	if st != usageOK {
		return protocol.TreeUsage{}, st
	}
	for _, t := range n.Usage.Trees {
		if sharedTree(t) {
			return t, usageOK
		}
	}
	return protocol.TreeUsage{}, usageNone
}

// heavy tells whether a tree is worth a warning.
func heavy(t protocol.TreeUsage) bool {
	return t.Procs > HeavyProcs || t.CPU >= HeavyCPU || t.RSS >= HeavyRSS
}

// usageDue tells whether the next refresh should also ask for usage.
func (m Model) usageDue() bool {
	if m.usage.unavailable != "" && m.usage.noCounts {
		return false
	}
	return m.usage.at.IsZero() || m.now().Sub(m.usage.at) >= usageEvery
}

// wantTop returns the command that fetches the top processes of the selected
// session, when they are not cached or are older than topEvery.
func (m *Model) wantTop() tea.Cmd {
	if m.help {
		return nil
	}
	a, ok := m.selectedAgent()
	if !ok || !a.Online {
		return nil
	}
	if _, st := m.treeOf(a); st != usageOK {
		return nil
	}
	e := m.top[a.SessionID]
	if e != nil && (e.loading || m.now().Sub(e.at) < topEvery) {
		return nil
	}
	if e == nil {
		if len(m.top) > 50 {
			m.top = map[string]*topEntry{}
		}
		e = &topEntry{}
		m.top[a.SessionID] = e
	}
	e.loading = true
	return m.topCmd(a.SessionID)
}

// ---- figures ----

// fmtPct writes a CPU figure: whole percent, "<1%" for a trickle.
func fmtPct(v float64) string {
	switch {
	case v <= 0:
		return "0%"
	case v < 1:
		return "<1%"
	}
	return fmt.Sprintf("%.0f%%", v)
}

// fmtMem writes a size in at most 4 characters where it can: 512M, 1.2G, 12G.
func fmtMem(b uint64) string {
	switch {
	case b >= 10<<30:
		return fmt.Sprintf("%.0fG", float64(b)/(1<<30))
	case b >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%dM", b>>20)
	}
	return fmt.Sprintf("%dK", b>>10)
}

// fmtMemPair writes used/total in gigabytes: "9.1/16G".
func fmtMemPair(used, total uint64) string {
	g := func(b uint64) string {
		v := float64(b) / (1 << 30)
		if v >= 10 {
			return fmt.Sprintf("%.0f", v)
		}
		return fmt.Sprintf("%.1f", v)
	}
	return g(used) + "/" + g(total) + "G"
}

// bar draws frac (0 to 1) in n cells, the empty ones light.
func bar(frac float64, n int) string {
	if n <= 0 {
		return ""
	}
	fill := int(frac*float64(n) + 0.5)
	fill = min(max(fill, 0), n)
	if frac > 0 && fill == 0 {
		fill = 1 // a trickle is still visible
	}
	return strings.Repeat("█", fill) + strings.Repeat("░", n-fill)
}

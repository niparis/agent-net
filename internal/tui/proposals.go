package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
)

type proposalDetail struct {
	proposal protocol.CTOProposal
	diff     []string
	loading  bool
	err      string
	scroll   int
}
type proposalDetailMsg struct {
	node, id string
	diff     []string
	err      error
}
type proposalAnswerMsg struct {
	node, id, action string
	items            []protocol.CTOProposal
	err              error
	sessionBefore    uint64
}

func proposalKey(p protocol.CTOProposal) string { return "proposal:" + p.NodeID + ":" + p.ID }
func (m Model) selectedProposal() (protocol.CTOProposal, bool) {
	for _, p := range m.proposals {
		if p.Status == "open" && proposalKey(p) == m.selected {
			return p, true
		}
	}
	return protocol.CTOProposal{}, false
}
func proposalSummary(p protocol.CTOProposal) string {
	var a map[string]any
	_ = json.Unmarshal(p.Args, &a)
	value := func(k string) string { v, _ := a[k].(string); return sanitize(v) }
	if (p.Tool == "session_change" && p.Action == "session_start") || (p.Tool == "project_change" && p.Action == "create") {
		project := value("project")
		if project == "" {
			project = value("path")
		}
		machine := value("node_id")
		if machine == "" {
			machine = "here"
		}
		harness := value("harness")
		if harness == "" {
			harness = "claude"
		}
		folder := value("folder")
		if folder == "" {
			folder = "project folder"
		}
		message := []rune(strings.Join(strings.Fields(value("first_message")), " "))
		first := ""
		if len(message) > 0 {
			if len(message) > 28 {
				message = append(message[:27], '…')
			}
			first = " · " + string(message)
		}
		verb := "start"
		if p.Action == "create" {
			verb = "create"
		}
		return fmt.Sprintf("%s %s @ %s · %s · %s%s", verb, project, machine, harness, folder, first)
	}
	switch p.Action {
	case "charter_set":
		return fmt.Sprintf("charter %s %s", value("node"), value("attr"))
	case "edge_set":
		return fmt.Sprintf("edge %s -> %s rule %s", value("from"), value("to"), value("rule"))
	}
	if p.Tool == "node_rename" || (p.Tool == "settings_change" && p.Action == "machine_name") {
		return "rename machine " + value("node_id") + " to " + value("name")
	}
	if p.Tool == "session_change" {
		return "session " + p.Action + " " + value("session_id")
	}
	return strings.TrimSpace(p.Tool + " " + p.Action)
}

func (m Model) proposalDetailCmd(p protocol.CTOProposal) tea.Cmd {
	c, ok := m.src.(inboxCaller)
	return func() tea.Msg {
		out := proposalDetailMsg{node: p.NodeID, id: p.ID}
		if !ok {
			out.err = fmt.Errorf("proposal details unavailable")
			return out
		}
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		if p.Action == "charter_set" || p.Action == "edge_set" || p.Tool == "charter_set" || p.Tool == "edge_set" {
			raw, err := c.Call(ctx, protocol.OpGraphGet, nil)
			if err != nil {
				out.err = err
				return out
			}
			var g protocol.Graph
			if err = json.Unmarshal(raw, &g); err != nil {
				out.err = err
				return out
			}
			out.diff = proposalGraphDiff(p, g)
		} else if p.Tool == "node_rename" || (p.Tool == "settings_change" && p.Action == "machine_name") {
			var a map[string]string
			_ = json.Unmarshal(p.Args, &a)
			snap, err := m.src.Snapshot(ctx)
			if err != nil {
				out.err = err
				return out
			}
			old := "(machine no longer exists)"
			for _, n := range snap.Nodes {
				if n.NodeID == a["node_id"] {
					old = n.Hostname
					break
				}
			}
			out.diff = []string{"- " + old, "+ " + a["name"]}
		} else {
			out.diff = []string{"Diff unavailable for this call"}
		}
		return out
	}
}
func proposalGraphDiff(p protocol.CTOProposal, g protocol.Graph) []string {
	var a map[string]string
	_ = json.Unmarshal(p.Args, &a)
	if p.Action == "charter_set" || p.Tool == "charter_set" {
		for _, n := range g.Nodes {
			if n.Name == a["node"] {
				b, _ := json.Marshal(n.Charter)
				var old map[string]string
				_ = json.Unmarshal(b, &old)
				return []string{"- " + old[a["attr"]], "+ " + a["value"]}
			}
		}
		return []string{"Current node no longer exists", "+ " + a["value"]}
	}
	for _, e := range g.Edges {
		if e.From == a["from"] && e.To == a["to"] && e.Kind == a["kind"] {
			b, _ := json.Marshal(e)
			return []string{"- " + string(b), "+ " + string(p.Args)}
		}
	}
	return []string{"- (no edge)", "+ " + string(p.Args)}
}
func (m Model) proposalAnswerCmd(p protocol.CTOProposal, action string) tea.Cmd {
	c, ok := m.src.(inboxCaller)
	socket, pane := m.herdrSocket, m.herdrPaneID
	sessionBefore := m.sessionNoticeSerial
	var confirmed []protocol.CTOProposalRef
	if action == "A" {
		confirmed = make([]protocol.CTOProposalRef, 0, len(m.proposalConfirmItems))
		for _, v := range m.proposalConfirmItems {
			confirmed = append(confirmed, protocol.CTOProposalRef{NodeID: v.NodeID, ID: v.ID})
		}
	}
	return func() tea.Msg {
		r := proposalAnswerMsg{node: p.NodeID, id: p.ID, action: action, sessionBefore: sessionBefore}
		if !ok {
			r.err = fmt.Errorf("proposal actions unavailable")
			return r
		}
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		_, r.err = c.Call(ctx, protocol.OpProposalAnswer, protocol.CTOProposalAnswer{NodeID: p.NodeID, ID: p.ID, Action: action, Confirm: confirmed, HerdrSocket: socket, HerdrPaneID: pane})
		raw, err := c.Call(ctx, protocol.OpProposalList, nil)
		if err == nil {
			_ = json.Unmarshal(raw, &r.items)
		}
		return r
	}
}
func (m *Model) openProposalConfirm() {
	m.proposalConfirm = true
	m.proposalConfirmItems = nil
	m.proposalConfirmScroll = 0
	for _, p := range m.proposals {
		if p.Status == "open" {
			m.proposalConfirmItems = append(m.proposalConfirmItems, p)
		}
	}
}
func (m Model) proposalConfirmKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	yes, no := confirmAnswer(k)
	switch {
	case yes:
		m.proposalConfirm = false
		return m, m.proposalAnswerCmd(protocol.CTOProposal{}, "A")
	case no:
		m.proposalConfirm = false
	case k.String() == "up" || k.String() == "k":
		m.proposalConfirmScroll = max(0, m.proposalConfirmScroll-1)
	case k.String() == "down" || k.String() == "j":
		m.proposalConfirmScroll = min(max(0, len(m.proposalConfirmItems)-1), m.proposalConfirmScroll+1)
	}
	return m, nil
}
func (m Model) proposalDetailKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.proposalDetail = nil
	case "up", "k":
		m.proposalDetail.scroll = max(0, m.proposalDetail.scroll-1)
	case "down", "j":
		m.proposalDetail.scroll++
	case "?":
		m.help = true
	case "A":
		m.openProposalConfirm()
	case "a", "x":
		if m.proposalDetail.proposal.Status == "open" {
			return m, m.proposalAnswerCmd(m.proposalDetail.proposal, k.String())
		}
	}
	return m, nil
}

func (m Model) proposalHistoryItems() []protocol.CTOProposal {
	var out []protocol.CTOProposal
	for _, ref := range m.proposalConfirmItems {
		for _, p := range m.proposals {
			if p.ID == ref.ID && p.NodeID == ref.NodeID {
				out = append(out, p)
				break
			}
		}
	}
	return out
}
func (m Model) proposalHistoryKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.proposalDetail != nil {
		return m.proposalDetailKey(k)
	}
	items := m.proposalHistoryItems()
	switch k.String() {
	case "esc":
		m.proposalHistory = false
	case "?":
		m.help = true
	case "up", "k":
		m.proposalHistoryIndex = max(0, m.proposalHistoryIndex-1)
	case "down", "j":
		m.proposalHistoryIndex = min(max(0, len(items)-1), m.proposalHistoryIndex+1)
	case "enter":
		if len(items) > 0 {
			p := items[min(m.proposalHistoryIndex, len(items)-1)]
			m.proposalDetail = &proposalDetail{proposal: p, loading: true}
			return m, m.proposalDetailCmd(p)
		}
	}
	return m, nil
}
func (m Model) proposalHistoryView(w int) []string {
	out := []string{section("Proposal outcomes", w), "  Enter opens the full result.", ""}
	for i, p := range m.proposalHistoryItems() {
		mark := "  "
		if i == m.proposalHistoryIndex {
			mark = "▸ "
		}
		out = append(out, clip(mark+p.ID+"  "+proposalSummary(p)+"  "+p.Note, w))
	}
	return out
}
func (m Model) proposalConfirmView(w int) []string {
	out := []string{section("Approve all CTO proposals?", w), "  These calls will run in order. Stop on the first failure.", ""}
	for _, p := range m.proposalConfirmItems[m.proposalConfirmScroll:] {
		if p.Status == "open" {
			out = append(out, clip("  "+p.ID+"  "+proposalSummary(p), w))
		}
	}
	return append(out, "", "  "+confirmHint)
}
func (m Model) proposalDetailView(w int) []string {
	d := m.proposalDetail
	p := d.proposal
	out := []string{section("CTO proposal "+p.ID, w), "  " + clip(proposalSummary(p), w-2), "", "CTO's words:"}
	for _, line := range wrap(sanitize(p.Reason), w-2) {
		out = append(out, "  "+line)
	}
	out = append(out, "", "Current diff:")
	if d.loading {
		out = append(out, "  Loading...")
	} else if d.err != "" {
		out = append(out, "  "+d.err)
	} else {
		for _, s := range d.diff {
			for _, line := range wrap(sanitize(s), w-2) {
				out = append(out, "  "+line)
			}
		}
	}
	out = append(out, "", "Messages delivered before the proposal:")
	for _, msg := range p.Messages {
		out = append(out, "  "+clip(sanitize(msg.Sender)+"  "+msg.At.Local().Format("2006-01-02 15:04"), w-2))
	}
	if len(p.Messages) == 0 {
		out = append(out, "  None recorded")
	}
	out = append(out, "", "Outcome: "+sanitize(p.Note))
	if len(p.Result) > 0 && string(p.Result) != "null" {
		out = append(out, "Result:")
		var pretty bytes.Buffer
		if json.Indent(&pretty, p.Result, "", "  ") != nil {
			pretty.Write(p.Result)
		}
		for _, line := range strings.Split(pretty.String(), "\n") {
			for _, part := range wrap(sanitize(line), w-2) {
				out = append(out, "  "+part)
			}
		}
	}
	start := min(d.scroll, max(0, len(out)-1))
	return out[start:]
}

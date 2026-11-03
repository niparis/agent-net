package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/niparis/agent-net/internal/protocol"
)

func (m Model) quotasView(w, h int) []string {
	if m.workersLog {
		return m.workersLogView(w, h)
	}
	out := []string{m.titleLine(w), m.quotasSubtabs(w)}
	var subscription []string
	protected := m.snap.ProtectedProviders
	if len(protected.Providers) == 0 {
		subscription = append(subscription, sDim.Render("no provider protected · select a subscription and press p"))
	} else {
		summary := "protected: " + sanitize(strings.Join(protected.Providers, ", "))
		if protected.Reason != "" {
			summary += " · " + sanitize(protected.Reason)
		}
		for _, line := range wrap(summary, w-4) {
			subscription = append(subscription, sDim.Render(line))
		}
	}
	if m.mode == modeQuotaConfirm {
		question := "workers use others first; this provider only when nothing else fits"
		if m.quotaUnprotect {
			question = "workers may use this provider freely"
		}
		for _, line := range wrap(question, w-4) {
			subscription = append(subscription, sBold.Render(line))
		}
	}
	rows := m.quotaDisplayRows()
	nodes, ids := m.quotaNodes()
	var errors []string
	reporting := 0
	singleMachine := ""
	for _, id := range ids {
		n := nodes[id]
		if n.Usage.Quotas != nil {
			reporting++
			singleMachine = firstNonEmpty(n.Hostname, id)
		}
		if n.Usage.QuotaError != "" {
			errors = append(errors, sanitize(firstNonEmpty(n.Hostname, id))+": "+quotaErrorLabel(n.Usage.QuotaError))
		}
	}
	if len(rows) == 0 && len(errors) == 0 {
		empty := "No machine reads quotas yet: quota-axi is not installed on any machine (see docs/setup.md, Quota)."
		for _, id := range ids {
			if note := nodes[id].Usage.QuotaNote; note != "" {
				empty = sanitize(note)
				break
			}
		}
		for _, line := range wrap(empty, w-2) {
			subscription = append(subscription, sDim.Render(line))
		}
	}

	var newest int64
	var configured, liveUnused, unset, unused []quotaRow
	relevant := m.quotaUsableProviders()
	for _, r := range rows {
		provider := strings.ToLower(r.provider.Provider)
		if len(r.provider.Windows) > 0 {
			if relevant[provider] {
				configured = append(configured, r)
			} else {
				liveUnused = append(liveUnused, r)
			}
		} else if quotaNotSetUp(r.provider) {
			unset = append(unset, r)
		} else {
			unused = append(unused, r)
		}
		if r.at > newest {
			newest = r.at
		}
	}
	if m.quotaShowUnset {
		configured = append(configured, liveUnused...)
		configured = append(configured, unset...)
		configured = append(configured, unused...)
	}
	var machines []protocol.UsageNode
	for _, id := range ids {
		if nodes[id].Usage.Quotas != nil {
			machines = append(machines, nodes[id])
		}
	}
	// Wide enough for the longest machine name (and "sign in"), so the header
	// is not cut; narrow terminals keep a short fixed width.
	machineWidth := 10
	for _, n := range machines {
		machineWidth = max(machineWidth, len([]rune(sanitize(firstNonEmpty(n.Hostname, n.NodeID)))))
	}
	machineWidth = min(machineWidth, 14)
	if w < 110 {
		machineWidth = 7
	}
	providerWidth, planWidth, windowWidth := quotaColumnWidths(configured, m, w-len(machines)*(machineWidth+1))
	if len(rows) > 0 {
		header := cell("PROVIDER", providerWidth) + " " + cell("PLAN", planWidth)
		for _, node := range machines {
			header += " " + cell(strings.ToUpper(sanitize(firstNonEmpty(node.Hostname, node.NodeID))), machineWidth)
		}
		header += " " + cell("WINDOW", windowWidth) + " LEFT                 PACE      RESETS"
		subscription = append(subscription, sBold.Render(header))
	}
	for rowIndex, r := range configured {
		if r.at > newest {
			newest = r.at
		}
		p := r.provider
		windows := p.Windows
		if len(windows) == 0 {
			windows = []protocol.QuotaWindow{{}}
		}
		for i, window := range windows {
			provider, plan := "", ""
			if i == 0 {
				provider = sanitize(p.Provider)
				if m.quotaProviderProtected(p.Provider) {
					provider = "🔒 " + provider
				}
				plan = sanitize(p.Plan)
			}
			name := protocol.QuotaWindowName(window)
			if name == "quota" && len(p.Windows) == 0 {
				name = "-"
			}
			pace := strings.ReplaceAll(window.Pace, "_", " ")
			if window.Pace == protocol.QuotaPaceBehind || window.Pace == protocol.QuotaPaceOnPace {
				pace = "on pace"
			}
			if pace == "" || pace == "unknown" {
				pace = "-"
			}
			left := min(max(100-window.PercentUsed, 0), 100)
			pct := fmt.Sprintf("%3.0f%%", left)
			if window.Exhausted || window.PercentUsed >= 100 {
				pct = "exhausted"
			}
			color := roleGood
			if left <= 40 {
				color = roleWarning
			}
			if left < 15 || window.Exhausted {
				color = roleError
			}
			pctText := pct
			if window.Exhausted || window.PercentUsed >= 100 {
				pctText = sBad.Render(pct)
			}
			remaining := "-"
			if len(p.Windows) > 0 {
				remaining = quotaBar(left/100, 10, color) + " " + pctText
			}
			remaining = padStyled(remaining, 20)

			paceStyle := sDim
			if window.Pace == protocol.QuotaPaceAhead {
				paceStyle = lipgloss.NewStyle().Foreground(roleWarning)
			}
			if window.Pace == protocol.QuotaPaceBehind {
				paceStyle = sBusy
			}
			pace = paceStyle.Render(cell(pace, 9))
			reset := "-"
			if window.ResetsAt > 0 {
				t := m.displayTime(window.ResetsAt)
				d := t.Sub(m.now())
				if d > 0 && d < 24*time.Hour {
					minutes := int(d / time.Minute)
					if minutes >= 60 {
						reset = fmt.Sprintf("in %dh%02d", minutes/60, minutes%60)
					} else {
						reset = fmt.Sprintf("in %dm", minutes)
					}
				} else if d >= 0 && d < 7*24*time.Hour {
					reset = t.Format("Mon 15:04")
				} else {
					reset = t.Format("Jan 02 15:04")
				}
			}
			if i == 0 {
				if rowIndex == m.quotaSelected {
					provider = "> " + provider
				} else {
					provider = "  " + provider
				}
			}
			providerCell := cell(provider, providerWidth)
			if rowIndex == m.quotaSelected && i == 0 {
				providerCell = sSel.Render(providerCell)
			}
			line := providerCell + " " + cell(plan, planWidth)
			for _, node := range machines {
				status := ""
				if i == 0 {
					status = quotaMachineStatus(node.Usage.Quotas, p.Provider)
				}
				value := cell(status, machineWidth)
				if status == "live" {
					value = sBusy.Render(value)
				} else {
					value = sDim.Render(value)
				}
				line += " " + value
			}
			line += fmt.Sprintf(" %s %s %s %s", cell(name, windowWidth), remaining, pace, reset)
			subscription = append(subscription, clip(line, w-4))
		}

	}
	if count := len(liveUnused) + len(unset) + len(unused); count > 0 {
		action := "s to show"
		if m.quotaShowUnset {
			action = "s to hide"
		}
		subscription = append(subscription, sDim.Render(fmt.Sprintf("%d other subscriptions · %s", count, action)))
	}
	for _, e := range errors {
		for _, line := range wrap(e, w-4) {
			subscription = append(subscription, sDim.Render(line))
		}
	}
	title := "Subscriptions"
	var copies []string
	for _, id := range ids {
		n := nodes[id]
		c := n.Usage.QuotaCopy
		if c == nil && n.Usage.Quotas != nil {
			c = n.Usage.Quotas.Copy
		}
		if c != nil {
			copies = append(copies, sanitize(firstNonEmpty(n.Hostname, id))+": "+quotaCopyLabel(c))
		}
	}
	if len(copies) == 1 {
		_, label, _ := strings.Cut(copies[0], ": ")
		title += " · " + label
	} else if len(copies) > 1 {
		for _, label := range copies {
			subscription = append(subscription, sDim.Render(label))
		}
	}
	if reporting == 1 {
		title += " · read on " + sanitize(singleMachine)
	}
	if newest > 0 {
		age := m.now().Sub(time.Unix(newest, 0))
		if age < 0 {
			age = 0
		}
		title += fmt.Sprintf(" · read %s ago", age.Truncate(time.Second))
	}
	out = append(out, panel(title, subscription, w, roleAccent)...)
	if calibration := m.calibrationView(w); len(calibration) > 0 {
		out = append(out, calibration...)
	}

	workers := m.quotaModelsView(w)
	out = append(out, workers...)
	out = append(out[:2], quotaPanelViewport(out[2:], w, max(h-2, 0), m.quotaScroll)...)

	for len(out) < h {
		out = append(out, "")
	}
	return out
}

// quotaPanelViewport scrolls the tab's rows while keeping the borders of every
// visible panel. The height comes from View after it reserves the CTO box.
func quotaPanelViewport(rows []string, w, h, scroll int) []string {
	if len(rows) <= h {
		return rows
	}
	// A continued panel repeats its heading and adds a "more above" row.
	start := min(max(scroll, 0), max(len(rows)-h+2, 0))
	var out []string
	hint := func(text string) string {
		return panel("", []string{sDim.Render(text)}, w, roleAccent)[1]
	}
	moreBelow := hint("more below · up/down to scroll")
	for i := 0; i < len(rows); {
		end := i + 1
		framed := strings.HasPrefix(stripStyles(rows[i]), "╭")
		if framed {
			for end < len(rows) && !strings.HasPrefix(stripStyles(rows[end-1]), "╰") {
				end++
			}
		}
		if start >= end {
			i = end
			continue
		}
		room := h - len(out)
		if !framed {
			if room == 0 {
				break
			}
			out = append(out, rows[i])
			i = end
			continue
		}
		bodyStart := max(i+1, start)
		above := bodyStart > i+1
		bodyRoom := room - 2
		if above {
			bodyRoom--
		}
		bodyLeft := end - 1 - bodyStart
		// If the next panel cannot start here, reserve its cue as an
		// extra row. Replacing the last body row would make that row
		// unreachable even after scrolling to the panel's end.
		below := bodyLeft > bodyRoom || (end < len(rows) && bodyRoom-bodyLeft < 4)
		if below {
			bodyRoom--
		}
		if bodyRoom < 0 || (below && bodyRoom == 0) {
			// A terminal too short for a panel must not show half a frame.
			break
		}
		out = append(out, rows[i])
		if above {
			out = append(out, hint("more above · up/down to scroll"))
		}
		bodyEnd := min(bodyStart+bodyRoom, end-1)
		out = append(out, rows[bodyStart:bodyEnd]...)
		if below {
			out = append(out, moreBelow)
		}
		out = append(out, rows[end-1])
		if below {
			break
		}
		i = end
	}
	return out
}

func quotaNotSetUp(p protocol.QuotaProvider) bool {
	if len(p.Windows) > 0 {
		return false
	}
	reason := strings.ToLower(strings.ReplaceAll(p.Error, "_", " "))
	return strings.Contains(reason, "credential unavailable") || strings.Contains(reason, "sign-in required") || strings.Contains(reason, "sign in required") || strings.Contains(reason, "not running") || strings.Contains(reason, "cli unavailable")
}

func quotaMachineStatus(q *protocol.Quotas, provider string) string {
	status := "-"
	for _, p := range q.Providers {
		if !strings.EqualFold(p.Provider, provider) {
			continue
		}
		if len(p.Windows) > 0 {
			return "live"
		}
		err := strings.ToLower(quotaErrorLabel(p.Error))
		switch {
		case strings.Contains(err, "sign-in") || strings.Contains(err, "sign in"):
			status = "sign in"
		case strings.Contains(err, "credential") || strings.Contains(err, "no key"):
			status = "no key"
		case strings.Contains(err, "cli") && (strings.Contains(err, "unavailable") || strings.Contains(err, "not installed")):
			status = "no CLI"
		case err != "":
			status = clip(sanitize(err), 10)
		}
	}
	return status
}

func quotaErrorLabel(err string) string {
	switch {
	case strings.HasSuffix(err, "credential_unavailable"):
		return "no credential found"
	case strings.HasSuffix(err, "cli_unavailable"):
		return "CLI not installed"
	case strings.HasSuffix(err, "sign_in_required"):
		return "sign-in required"
	default:
		return sanitize(err)
	}
}

func quotaColumnWidths(rows []quotaRow, m Model, w int) (int, int, int) {
	provider, plan, window := len("PROVIDER"), len("PLAN"), len("WINDOW")
	for i, r := range rows {
		name := sanitize(r.provider.Provider)
		if m.quotaProviderProtected(r.provider.Provider) {
			name = "🔒 " + name
		}
		if i == m.quotaSelected {
			name = "> " + name
		} else {
			name = "  " + name
		}
		provider = max(provider, lipgloss.Width(name))
		plan = max(plan, lipgloss.Width(sanitize(r.provider.Plan)))
		for _, q := range r.provider.Windows {
			window = max(window, lipgloss.Width(sanitize(protocol.QuotaWindowName(q))))
		}
		if len(r.provider.Windows) == 0 {
			window = max(window, 1)
		}
	}
	// Reserve room for the bar, pace, reset text, and column separators.
	budget := max(w-4-47, 12)
	for provider+plan+window > budget {
		switch {
		case window > 8:
			window--
		case plan > 5:
			plan--
		case provider > 11:
			provider--
		default:
			return max(provider, 1), max(plan, 1), max(window, 1)
		}
	}
	return provider, plan, window
}

func quotaBar(frac float64, n int, color lipgloss.TerminalColor) string {
	n = max(n, 0)
	filled := min(max(int(frac*float64(n)+0.5), 0), n)
	if frac > 0 && n > 0 && filled == 0 {
		filled = 1
	}
	return lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", filled)) + lipgloss.NewStyle().Foreground(roleFaint).Render(strings.Repeat("─", n-filled))
}

func quotaCopyLabel(c *protocol.QuotaCopy) string {
	label := "quota-axi " + sanitize(c.Version)
	if c.Private {
		return label + " · agent-net's copy"
	}
	return label + " · yours (" + sanitize(c.Dir) + ")"
}

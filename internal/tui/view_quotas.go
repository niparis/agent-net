package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/niparis/agent-net/internal/protocol"
)

func (m Model) quotasView(w, h int) []string {
	out := []string{m.titleLine(w)}
	var subscription []string
	rows := m.quotaRows()
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
			errors = append(errors, sanitize(firstNonEmpty(n.Hostname, id))+": "+sanitize(n.Usage.QuotaError))
		}
	}
	if len(rows) > 0 {
		subscription = append(subscription, sBold.Render("PROVIDER    PLAN      WINDOW         LEFT                 PACE      RESETS"))
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
	var configured, unset, unused []quotaRow
	relevant := m.quotaRelevantProviders()
	providersWithQuota := map[string]bool{}
	for _, r := range rows {
		if len(r.provider.Windows) > 0 {
			providersWithQuota[strings.ToLower(r.provider.Provider)] = true
		}
	}
	for _, r := range rows {
		provider := strings.ToLower(r.provider.Provider)
		if quotaNotSetUp(r.provider) && !providersWithQuota[provider] {
			unset = append(unset, r)
		} else if !relevant[provider] {
			unused = append(unused, r)
		} else {
			configured = append(configured, r)
		}
		if r.at > newest {
			newest = r.at
		}
	}
	if m.quotaShowUnset {
		configured = append(configured, unset...)
		configured = append(configured, unused...)
	}
	for _, r := range configured {
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
			remaining := quotaBar(left/100, 10, color) + " " + pctText
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
				t := time.Unix(window.ResetsAt, 0).In(m.loc)
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
			line := fmt.Sprintf("%-11s %-9s %-14s %s %s %s", cell(provider, 11), cell(plan, 9), cell(name, 14), remaining, pace, reset)
			if p.Stale || p.Error != "" {
				line = sDim.Render(stripStyles(line))
			}
			subscription = append(subscription, clip(line, w-4))
		}
		var notes []string
		if r.machine != "" && reporting > 1 {
			notes = append(notes, "read on "+sanitize(r.machine))
		}
		if p.Stale {
			notes = append(notes, "stale reading")
		}
		if p.Error != "" {
			notes = append(notes, sanitize(p.Error))
		}
		if len(notes) > 0 {
			subscription = append(subscription, sDim.Render(clip("  ("+strings.Join(notes, "; ")+")", w-4)))
		}
	}
	if len(unset) > 0 {
		action := "s to show"
		if m.quotaShowUnset {
			action = "s to hide"
		}
		subscription = append(subscription, sDim.Render(fmt.Sprintf("%d providers not set up · %s", len(unset), action)))
	}
	if len(unused) > 0 {
		action := "s to show"
		if m.quotaShowUnset {
			action = "s to hide"
		}
		subscription = append(subscription, sDim.Render(fmt.Sprintf("%s (not used by your agents) · %s", strings.Replace(plural(len(unused), "subscription"), " ", " more ", 1), action)))
	}
	for _, e := range errors {
		for _, line := range wrap(e, w-4) {
			subscription = append(subscription, sDim.Render(line))
		}
	}
	title := "Subscriptions"
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

	workers := m.quotaModelsView(w)
	out = append(out, workers...)
	if len(out) > h {
		body := out[1:]
		room := max(h-1, 0)
		start := min(m.quotaScroll, max(len(body)-room, 0))
		end := min(start+room, len(body))
		workerStart := len(body) - len(workers)
		if start <= workerStart && end > workerStart && end-workerStart < 3 {
			end = workerStart
		}
		out = append(out[:1], body[start:end]...)
	}

	for len(out) < h {
		out = append(out, "")
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

func quotaBar(frac float64, n int, color lipgloss.TerminalColor) string {
	n = max(n, 0)
	filled := min(max(int(frac*float64(n)+0.5), 0), n)
	if frac > 0 && n > 0 && filled == 0 {
		filled = 1
	}
	return lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", filled)) + lipgloss.NewStyle().Foreground(roleFaint).Render(strings.Repeat("─", n-filled))
}

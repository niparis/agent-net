package tui

import (
	"fmt"
	"sort"
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
		subscription = append(subscription, sBold.Render("PROVIDER       PLAN         WINDOW   USAGE                PACE      RESETS"))
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
	for _, e := range errors {
		for _, line := range wrap(e, w-2) {
			subscription = append(subscription, sDim.Render(line))
		}
	}
	var newest int64
	var configured, unset []quotaRow
	for _, r := range rows {
		if quotaNotSetUp(r.provider) {
			unset = append(unset, r)
		} else {
			configured = append(configured, r)
		}
		if r.at > newest {
			newest = r.at
		}
	}
	if m.quotaShowUnset {
		configured = append(configured, unset...)
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
			pct := fmt.Sprintf("%3.0f%%", window.PercentUsed)
			if window.Exhausted || window.PercentUsed >= 100 {
				pct = "exhausted"
			}
			color := roleGood
			if window.PercentUsed >= 60 {
				color = roleWarning
			}
			if window.PercentUsed > 85 || window.Exhausted {
				color = roleError
			}
			pctText := pct
			if window.Exhausted || window.PercentUsed >= 100 {
				pctText = sBad.Render(pct)
			}
			usage := progressBar(window.PercentUsed/100, 10, color) + " " + pctText
			usage = padStyled(usage, 20)

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
			line := fmt.Sprintf("%-14s %-12s %-8s %s %s %s", cell(provider, 14), cell(plan, 12), cell(name, 8), usage, pace, reset)
			if p.Stale || p.Error != "" {
				line = sDim.Render(stripStyles(line))
			}
			subscription = append(subscription, clip(line, w-4))
		}
		var notes []string
		if r.machine != "" && reporting > 1 {
			if r.readers == 1 {
				notes = append(notes, "read on "+sanitize(r.machine))
			} else {
				notes = append(notes, sanitize(r.machine))
			}
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

	counts := map[string]int{}
	for _, a := range m.snap.Agents {
		if a.Online && a.Worker != nil {
			counts[a.Harness]++
		}
	}
	names := make([]string, 0, len(counts))
	for k := range counts {
		names = append(names, k)
	}
	sort.Strings(names)
	var parts []string
	for _, k := range names {
		parts = append(parts, fmt.Sprintf("%s %d", sanitize(k), counts[k]))
	}
	if len(parts) == 0 {
		parts = append(parts, "none")
	}
	bottom := "workers now: " + strings.Join(parts, " · ")
	workers := m.quotaModelsView(w)
	footer := panel("", []string{sDim.Render(bottom)}, w, roleAccent)[1]
	workers = append(workers[:len(workers)-1], footer, workers[len(workers)-1])
	out = append(out, workers...)
	if len(out) > h {
		header := out[:1]
		body := out[1 : len(out)-2]
		room := max(h-3, 0)
		start := min(m.quotaScroll, max(len(body)-room, 0))
		out = append(header, body[start:min(start+room, len(body))]...)
		out = append(out, footer, workers[len(workers)-1])
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

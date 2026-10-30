package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func (m Model) quotasView(w, h int) []string {
	out := []string{m.titleLine(w), ""}
	rows := m.quotaRows()
	nodes, ids := m.quotaNodes()
	var errors []string
	for _, id := range ids {
		n := nodes[id]
		if n.Usage.QuotaError != "" {
			errors = append(errors, sanitize(firstNonEmpty(n.Hostname, id))+": "+sanitize(n.Usage.QuotaError))
		}
	}
	if len(rows) > 0 {
		out = append(out, sBold.Render("  PROVIDER       PLAN         WINDOW   USAGE                PACE      RESETS"))
	}
	if len(rows) == 0 && len(errors) == 0 {
		for _, line := range wrap("No machine reads quotas yet: quota-axi is not installed on any machine (see docs/setup.md, Quota).", w-2) {
			out = append(out, sDim.Render("  "+line))
		}
	}
	for _, e := range errors {
		for _, line := range wrap(e, w-2) {
			out = append(out, sDim.Render("  "+line))
		}
	}
	var newest int64
	for _, r := range rows {
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
			usage := bar(window.PercentUsed/100, 10) + " " + pct
			usage = cell(usage, 20)
			if window.Exhausted || window.PercentUsed >= 100 {
				usage = sBad.Render(usage)
			}
			if window.Pace == protocol.QuotaPaceAhead {
				pace = sBusy.Render(cell(pace, 9))
			}
			if window.Pace != protocol.QuotaPaceAhead {
				pace = cell(pace, 9)
			}
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
			line := fmt.Sprintf("  %-14s %-12s %-8s %s %s %s", cell(provider, 14), cell(plan, 12), cell(name, 8), usage, pace, reset)
			if p.Stale || p.Error != "" {
				line = sDim.Render(stripStyles(line))
			}
			out = append(out, clip(line, w))
		}
		var notes []string
		if r.machine != "" {
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
			out = append(out, sDim.Render(clip("    ("+strings.Join(notes, "; ")+")", w)))
		}
	}
	out = append(out, m.quotaModelsView(w)...)
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
	bottom := "  workers now: " + strings.Join(parts, " · ")
	if m.quotaShowExcluded {
		bottom += "   ·   s hide excluded"
	} else {
		bottom += "   ·   s show excluded"
	}
	if newest > 0 {
		age := m.now().Sub(time.Unix(newest, 0))
		if age < 0 {
			age = 0
		}
		bottom += fmt.Sprintf("   ·   reading %s old", age.Truncate(time.Second))
	}
	if len(out) > h-1 {
		headerRows := 2
		if len(rows) > 0 {
			headerRows++
		}
		header, body := out[:headerRows], out[headerRows:]
		room := max(h-1-headerRows, 0)
		start := min(m.quotaScroll, max(len(body)-room, 0))
		out = append(header, body[start:min(start+room, len(body))]...)
	}
	for len(out) < h-1 {
		out = append(out, "")
	}
	out = append(out, sDim.Render(clip(bottom, w)))
	return out
}

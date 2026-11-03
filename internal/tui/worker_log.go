package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/niparis/agent-net/internal/protocol"
)

func (m Model) workersLogKey(k tea.KeyMsg) (Model, tea.Cmd) {
	before := m.workerLogRow
	switch keyAction(contextWorkersLog, k.String()) {
	case "up":
		m.workerLogRow = max(m.workerLogRow-1, 0)
	case "down":
		m.workerLogRow = min(m.workerLogRow+1, max(len(m.snap.WorkerLog)-1, 0))
	case "page up":
		m.workerLogRow = max(m.workerLogRow-10, 0)
	case "page down":
		m.workerLogRow = min(m.workerLogRow+10, max(len(m.snap.WorkerLog)-1, 0))
	case "scroll details up":
		m.workerLogDetail = max(m.workerLogDetail-1, 0)
	case "scroll details down":
		m.workerLogDetail++
	case "help":
		m.openHelp()
	case "quit":
		m.quitting = true
		return m, tea.Quit
	}
	if before != m.workerLogRow {
		m.workerLogDetail = 0
	}
	return m, nil
}

func (m Model) quotasSubtabs(w int) string {
	quotas, workers := "  Quotas  ", "  Workers log  "
	if m.workersLog {
		quotas = sDim.Render(quotas)
		workers = sActiveTab.Render("[ Workers log ]")
	} else {
		quotas = sActiveTab.Render("[ Quotas ]")
		workers = sDim.Render(workers)
	}
	return clip(quotas+"  "+workers, w)
}

// workerStatusStyle is shared with the worker threads in Messages.
func workerStatusStyle(status string) lipgloss.Style {
	switch status {
	case "done":
		return sBusy
	case "failed", "blocked", "timed out":
		return sBad
	default:
		return lipgloss.NewStyle().Foreground(roleText)
	}
}

func workerLogLine(r protocol.WorkerLogEntry, w int, selected bool) string {
	lead := "  "
	if selected {
		lead = "▸ "
	}
	// The model includes the harness's effort suffix already.
	prefix := time.Unix(r.StartedAt, 0).UTC().Format("01-02 15:04") + "  " + cell(sanitize(firstNonEmpty(r.Model, "-")), min(25, max(w/4, 6))) + "  " + cell(sanitize(firstNonEmpty(r.Difficulty, "-")), min(10, max(w/8, 3))) + "  "
	status := r.Status
	room := max(w-2-len(status)-1, 0)
	line := lead + cell(sanitize(prefix+firstNonEmpty(r.Title, "-")), room) + " " + workerStatusStyle(status).Render(status)
	if selected {
		line = selectedRow(line)
	}
	return clip(line, w)
}

func (m Model) workersLogView(w, h int) []string {
	out := []string{m.titleLine(w), m.quotasSubtabs(w)}
	inner := max(w-4, 1)
	selected := min(m.workerLogRow, max(len(m.snap.WorkerLog)-1, 0))
	list := []string{}
	for i, r := range m.snap.WorkerLog {
		list = append(list, workerLogLine(r, inner, i == selected))
	}
	if len(list) == 0 {
		list = append(list, sDim.Render("No worker runs"))
	}
	detail := []string{sDim.Render("Select a worker run")}
	if len(m.snap.WorkerLog) > 0 {
		r := m.snap.WorkerLog[selected]
		detail = nil
		addTitle := "Title: " + firstNonEmpty(r.Title, "-")
		add := func(s string) { detail = append(detail, wrap(sanitize(s), inner)...) }
		add(addTitle)
		add("Model: " + firstNonEmpty(r.Model, "-"))
		add("Machine: " + firstNonEmpty(r.Hostname, firstNonEmpty(r.NodeID, "-")) + "   Project: " + firstNonEmpty(r.Project, "-") + "   Harness: " + firstNonEmpty(r.Harness, "-"))
		add(fmt.Sprintf("Run time: %s", (time.Duration(r.RunSeconds) * time.Second).String()))
		if a := r.Assessment; a != nil {
			add(a.DifficultyLine())
			add("Judge: " + firstNonEmpty(a.JudgeModel, "-"))
			for _, key := range []string{"reasoning", "scope", "coupling", "uncertainty", "verification", "risk"} {
				if reason := a.Reasons[key]; reason != "" {
					add(strings.ToUpper(key[:1]) + key[1:] + ": " + reason)
				}
			}
			if a.Error != "" {
				add("Assessment note: " + a.Error)
			}
		} else {
			add("Assessment: -")
		}
		add("Picker: " + firstNonEmpty(r.ModelNote, "-"))
		if r.Failure != "" {
			label := "Note: "
			if r.Status == "failed" || r.Status == "timed out" {
				label = "Failure: "
			}
			add(label + r.Failure)
		}
	}
	available := max(h-6, 0)
	dh := min(len(detail), max(available*2/3, 0))
	lh := max(available-dh, 0)
	out = append(out, panel("Workers log", messageFit(list, lh, selected), w, roleAccent)...)
	out = append(out, panel("Difficulty details", messageFit(detail, dh, min(max(dh-1, 0)+m.workerLogDetail, max(len(detail)-1, 0))), w, roleAccent)...)
	return out
}

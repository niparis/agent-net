package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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

func workerLogLine(r protocol.WorkerLogEntry, w int, selected bool, loc *time.Location) string {
	lead := "  "
	if selected {
		lead = "▸ "
	}
	// Keep today's runs compact so the task title has room.
	started := time.Unix(r.StartedAt, 0).In(loc)
	dateFormat := "01-02 15:04"
	if started.Format("2006-01-02") == time.Now().In(loc).Format("2006-01-02") {
		dateFormat = "15:04"
	}
	modelWidth := min(25, max(w/4, 6))
	if w < 100 {
		modelWidth = min(12, max(w/7, 6))
	}
	prefix := started.Format(dateFormat) + "  " + cell(sanitize(firstNonEmpty(r.Model, "-")), modelWidth) + "  " + cell(sanitize(firstNonEmpty(r.Difficulty, "-")), min(10, max(w/8, 3))) + "  " + cell(sanitize(firstNonEmpty(r.Caller, "-")), min(14, max(w/8, 4))) + "  "
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
		list = append(list, workerLogLine(r, inner, i == selected, m.loc))
	}
	if len(list) == 0 {
		list = append(list, sDim.Render("No worker runs"))
	}
	detail := []string{sDim.Render("Select a worker run")}
	if len(m.snap.WorkerLog) > 0 {
		r := m.snap.WorkerLog[selected]
		detail = workerLogDetails(r, inner)
		if r.Failure != "" {
			label := "Note: "
			if r.Status == "failed" || r.Status == "timed out" {
				label = "Failure: "
			}
			detail = append(detail, wrapHanging(label, sanitize(r.Failure), inner)...)
		}
	}
	available := max(h-6, 0)
	dh := min(len(detail), max(available*2/3, 0))
	lh := max(available-dh, 0)
	out = append(out, panel("Workers log", messageFit(list, lh, selected), w, roleAccent)...)
	out = append(out, panel("Difficulty details", messageFit(detail, dh, min(max(dh-1, 0)+m.workerLogDetail, max(len(detail)-1, 0))), w, roleAccent)...)
	return out
}

func workerLogDetails(r protocol.WorkerLogEntry, width int) []string {
	var detail []string
	labels := []string{"Title", "Caller", "Model", "Where", "Run time"}
	labelWidth := 0
	for _, label := range labels {
		labelWidth = max(labelWidth, len(label))
	}
	addFact := func(label, value string) {
		detail = appendWorkerLogFact(detail, label, value, labelWidth, width)
	}
	addFact("Title", r.Title)
	caller := r.Caller
	if r.CallerHost != "" {
		caller += " (" + r.CallerHost + ")"
	}
	addFact("Caller", caller)
	addFact("Model", workerLogModel(r.Harness, r.Model))
	where := strings.Join(nonEmpty(r.Hostname, r.Project), " / ")
	if where == "" {
		where = strings.Join(nonEmpty(r.NodeID, r.Project), " / ")
	}
	addFact("Where", where)
	if r.RunSeconds > 0 {
		addFact("Run time", (time.Duration(r.RunSeconds) * time.Second).String())
	}

	if a := r.Assessment; a != nil {
		if len(detail) > 0 {
			detail = append(detail, "")
		}
		level := strings.TrimSpace(a.Label)
		if level == "" {
			level = protocol.WorkerLevel(a)
		}
		levelText := workerDifficultyStyle(level).Render(level)
		summary := levelText + "  D " + fmt.Sprintf("%.2f", a.D)
		if a.JudgeModel != "" {
			summary += sDim.Render("  ·  " + sanitize(a.JudgeModel))
		}
		detail = append(detail, clip(summary, width))
		if a.Error != "" {
			detail = append(detail, wrapHanging("Judge error: ", sanitize(a.Error), width)...)
		} else {
			detail = append(detail, "")
			dimensions := []struct {
				name, key string
				score     int
			}{{"Reasoning", "reasoning", a.Reasoning}, {"Scope", "scope", a.Scope}, {"Coupling", "coupling", a.Coupling}, {"Uncertainty", "uncertainty", a.Uncertainty}, {"Verification", "verification", a.Verification}, {"Risk", "risk", a.Risk}}
			nameWidth := len("Verification")
			for _, d := range dimensions {
				name := fmt.Sprintf("%-*s", nameWidth, d.name)
				score := fmt.Sprintf("%d/5 %s", d.score, difficultyBar(d.score))
				prefix := name + "  " + score + "  "
				reason := strings.TrimSpace(a.Reasons[d.key])
				if reason == "" {
					reason = "No explanation provided"
				}
				for _, line := range wrapHanging(prefix, sanitize(reason), width) {
					if strings.HasPrefix(line, prefix) {
						detail = append(detail, sDim.Render(name)+"  "+sBold.Render(score)+"  "+strings.TrimPrefix(line, prefix))
					} else {
						detail = append(detail, line)
					}
				}
			}
		}
	}
	picker := r.ModelNote
	if picker == "" && r.Assessment != nil {
		picker = r.Assessment.PickerNote
	}
	picker = workerPickerNote(picker, r.Assessment)
	if strings.TrimSpace(picker) != "" {
		if len(detail) > 0 {
			detail = append(detail, "")
		}
		detail = appendWorkerLogFact(detail, "Picker", picker, labelWidth, width)
	}
	return detail
}

func appendWorkerLogFact(detail []string, label, value string, labelWidth, width int) []string {
	if strings.TrimSpace(value) == "" {
		return detail
	}
	prefix := fmt.Sprintf("%-*s  ", labelWidth, label)
	for _, line := range wrapHanging(prefix, sanitize(value), width) {
		if strings.HasPrefix(line, prefix) {
			detail = append(detail, sDim.Render(prefix)+strings.TrimPrefix(line, prefix))
		} else {
			detail = append(detail, line)
		}
	}
	return detail
}

func workerPickerNote(note string, assessment *protocol.WorkerAssessment) string {
	if assessment == nil {
		return note
	}
	prefix := "difficulty " + assessment.DifficultyLine() + " -> "
	return strings.TrimPrefix(note, prefix)
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func workerLogModel(harness, model string) string {
	if model == "" {
		return harness
	}
	provider := protocol.WorkerProvider(harness, model)
	modelName, effort := model, ""
	if i := strings.LastIndex(model, ":"); i > 0 && i < len(model)-1 {
		modelName, effort = model[:i], model[i+1:]
	}
	if provider == harness {
		provider = ""
	}
	if strings.HasPrefix(modelName, provider+"/") && provider != "" {
		modelName = strings.TrimPrefix(modelName, provider+"/")
	}
	parts := nonEmpty(harness, provider, modelName, effort)
	return strings.Join(parts, " · ")
}

func workerDifficultyStyle(level string) lipgloss.Style {
	switch strings.ToLower(level) {
	case "easy", "trivial", "simple":
		return lipgloss.NewStyle().Foreground(roleGood)
	case "hard":
		return lipgloss.NewStyle().Foreground(roleWarning)
	case "very hard", "extreme", "very_complex":
		return sBad
	default:
		return sAccent
	}
}

func difficultyBar(score int) string {
	score = min(max(score, 0), 5)
	return strings.Repeat("#", score) + strings.Repeat(".", 5-score)
}

func wrapHanging(prefix, text string, width int) []string {
	if width < 8 {
		width = 8
	}
	indent := ansi.StringWidth(prefix)
	if indent > width-8 {
		indent = max(width-8, 1)
	}
	firstWidth := max(width-indent, 1)
	lines := wrap(text, firstWidth)
	if len(lines) == 0 {
		return []string{prefix}
	}
	out := []string{prefix + lines[0]}
	for _, line := range lines[1:] {
		out = append(out, strings.Repeat(" ", indent)+line)
	}
	return out
}

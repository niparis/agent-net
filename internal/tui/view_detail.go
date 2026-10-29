package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// The detail pane of the selected session. It has two columns when the screen
// is wide enough: what the session is on the left, what it costs on the right.
// On a narrow screen they stack, the title and the cost first.

// tailCell fits a path into w columns, cutting the front: the end of a path
// says more than its start.
func tailCell(s string, w int) string {
	s = sanitize(s)
	if w <= 0 {
		return ""
	}
	if n := utf8.RuneCountInString(s); n > w {
		r := []rune(s)
		s = "…" + string(r[n-w+1:])
	}
	return s
}

func (m Model) detailLines(w, n int) []string {
	a, ok := m.selectedAgent()
	if !ok {
		out := []string{sDim.Render("  nothing selected")}
		for len(out) < n {
			out = append(out, "")
		}
		return out
	}
	two := w >= 76
	rightW := 36
	if w >= 100 {
		rightW = min(w*45/100, 64)
	}
	leftW := w - rightW - 3
	if !two {
		leftW = w
	}
	// The first line spans the screen: who, and what it costs.
	title, left := m.detailLeft(a, leftW, n)
	summary, right := m.detailRight(a, rightW, n)
	out := []string{title + summary}
	if two {
		for i := 0; i < n-1; i++ {
			var l, r string
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			out = append(out, padStyled(l, leftW)+sDim.Render(" │ ")+r)
		}
	} else {
		out = append(out, right...)
		out = append(out, left...)
	}
	for i := range out {
		out[i] = clip(out[i], w)
	}
	for len(out) < n {
		out = append(out, "")
	}
	return out[:n]
}

// detailLeft lists what the session is. Lines come in order of use; the pane
// shows as many as it has room for.
func (m Model) detailLeft(a protocol.AgentView, w, n int) (string, []string) {
	const lw = 8
	kv := func(label, value string) string {
		return clip("  "+sDim.Render(cell(label, lw))+" "+sanitize(value), w)
	}
	title := "  " + sBold.Render(clip(sanitize(dashIfEmpty(a.Profile.Name)), 26)) + "  " + sanitize(a.Harness)
	if a.Online {
		title += "  " + statusStyle(a.Status).Render(statusShort(a.Status))
	} else {
		title += "  " + sBad.Render("offline, last seen "+m.clock(time.Unix(a.LastSeen, 0)))
	}
	id := a.SessionID
	if a.PID > 0 {
		id += fmt.Sprintf(" pid %d", a.PID)
	}
	branch := dashIfEmpty(a.Repo.Branch)
	model := a.Model
	switch a.Mode {
	case protocol.ModeForeground:
		model += ", foreground"
	case protocol.ModeBackground:
		model += ", background"
	}
	model = dashIfEmpty(strings.TrimPrefix(model, ", "))
	var out []string
	if a.Worker != nil {
		// Who started it matters most, so it comes first.
		out = append(out, kv("worker", fmt.Sprintf("started by %s, %s, project %s", m.who(a.Worker.Owner),
			m.clock(time.Unix(a.Worker.StartedAt, 0)), dashIfEmpty(a.Worker.Project))))
	}
	out = append(out,
		kv("id", id),
		kv("folder", tailCell(dashIfEmpty(a.Cwd), w-12)),
		kv("branch", branch),
		kv("model", model),
	)
	if t := firstNonEmpty(a.Profile.Function, a.Profile.Task); t != "" {
		task := a.Profile.Function
		if a.Profile.Task != "" {
			task = strings.TrimSuffix(task+" / "+a.Profile.Task, " / ")
		}
		out = append(out, kv("task", task))
	}
	out = append(out, kv("remote", dashIfEmpty(a.Repo.Remote)))
	// A linked git worktree (not the main working tree), or a folder inside
	// one: say which.
	switch {
	case a.Repo.Worktree != "" && a.Repo.Root != "" && a.Repo.Worktree != a.Repo.Root:
		out = append(out, kv("worktree", tailCell(a.Repo.Worktree, w-12)+"  (linked)"))
	case a.Repo.Worktree != "" && a.Repo.Worktree != a.Cwd:
		out = append(out, kv("worktree", tailCell(a.Repo.Worktree, w-12)))
	}
	if src := m.projectSource(a); src != "" {
		out = append(out, kv("project", src))
	}

	msgs := m.feed.messagesOf(a.SessionID, 8)
	if len(out) > n-1 && (len(msgs) > 0 || m.feed.unavailable != "") && n >= 3 {
		// Messages need at least one row; the last lines give way.
		out = out[:max(n-1, 1)]
	}
	switch {
	case m.feed.unavailable != "":
		out = append(out, kv("messages", m.feed.unavailable))
	case len(msgs) == 0:
		out = append(out, kv("messages", "none seen yet"))
	default:
		for i, it := range msgs {
			label := ""
			if i == 0 {
				label = "messages"
			}
			out = append(out, kv(label, m.sessionMessageLine(a.SessionID, it)))
		}
	}
	return title, out
}

// detailRight lists what the session costs: its totals, then the heaviest
// processes by CPU and by memory.
func (m Model) detailRight(a protocol.AgentView, w, n int) (string, []string) {
	if !a.Online {
		return "", []string{sDim.Render("offline: no figures")}
	}
	t, st := m.treeOf(a)
	switch st {
	case usageStale:
		return sDim.Render("    cpu ? ram ? (figures are old)"), nil
	case usageNone:
		if m.usage.unavailable != "" {
			return sDim.Render("    " + m.usage.unavailable), nil
		}
		return sDim.Render("    cpu ? ram ? procs ?"), nil
	}
	sum := "    CPU " + fmtPct(t.CPU) + sDim.Render(" (5m "+fmtPct(t.CPUAvg)+")") + "  RAM " + fmtMem(t.RSS) + sDim.Render(" ("+fmtMem(t.RSSAvg)+")")
	procs := fmt.Sprintf("%d procs", t.Procs)
	if t.Procs > HeavyProcs {
		procs = sBad.Render(procs)
	}
	sum += "  " + procs
	var out []string
	if t.Shared {
		out = append(out, sDim.Render(fmt.Sprintf("shared by %s, not split", plural(len(t.Sessions), sanitize(t.Harness)+" session"))))
	}
	e := m.top[a.SessionID]
	room := n - 1 - len(out)
	switch {
	case e == nil || (!e.have && e.err == ""):
		out = append(out, sDim.Render("loading processes..."))
		return sum, out
	case e.err != "":
		out = append(out, sDim.Render(clip("processes: "+e.err, w)))
		return sum, out
	}
	out = append(out, procList("by CPU", e.top.ByCPU, min(5, max(room-1, 0)), w)...)
	if rest := n - 1 - len(out); rest >= 3 {
		out = append(out, procList("by RAM", e.top.ByRSS, min(5, rest-1), w)...)
	}
	return sum, out
}

// procList writes a heading and up to rows processes.
func procList(title string, ps []protocol.ProcUsage, rows, w int) []string {
	if rows <= 0 {
		return nil
	}
	out := []string{sDim.Render(cellR("PID", 7)+" "+cellR("CPU", 5)+" "+cellR("RAM", 5)+" ") + sBold.Render(title)}
	for i, p := range ps {
		if i >= rows {
			break
		}
		cmd := p.Args
		if strings.TrimSpace(cmd) == "" {
			cmd = p.Cmd
		}
		line := cellR(fmt.Sprint(p.PID), 7) + " " + cellR(fmtPct(p.CPU), 5) + " " + cellR(fmtMem(p.RSS), 5) + " "
		out = append(out, line+cell(cmd, max(w-ansi.StringWidth(line), 0)))
	}
	return out
}

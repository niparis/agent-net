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
	for _, e := range m.snap.Outcomes {
		if m.selected == "outcome:"+e.ReplyID {
			lines := []string{"  Request: " + sanitize(e.Request), "  Outcome: " + sanitize(e.Outcome), "  Reason: " + sanitize(e.Reason), "  Recommendation: " + sanitize(e.Recommendation), "  c: recipient conversation  C: requester conversation", "  s: let it stand  o: overrule"}
			var wrapped []string
			for _, line := range lines {
				wrapped = append(wrapped, wrap(line, w)...)
			}
			lines = wrapped
			for len(lines) < n {
				lines = append(lines, "")
			}
			return lines[:min(n, len(lines))]
		}
	}
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
	// The worker ID is here, not in the table: the table shows its task. It
	// comes before the PID so a narrow pane never cuts it (decision 0017).
	if a.Worker != nil && a.Worker.ID != "" {
		id += " " + a.Worker.ID
	}
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
		// Who started it matters most, so it comes first. Its ID is on the id
		// line: the table shows the task instead (decision 0017).
		out = append(out, kv("worker", fmt.Sprintf("started by %s, %s, project %s", m.who(a.Worker.Owner),
			m.clock(time.Unix(a.Worker.StartedAt, 0)), dashIfEmpty(m.projectName(a.Worker.Project)))))
		context := protocol.ContextText(a.Worker.ContextTokens, a.Worker.ContextLimit)
		tokens := protocol.CompactTokens(a.Worker.Tokens)
		switch {
		case context != "" && tokens != "":
			out = append(out, kv("context", context+"  tokens "+tokens))
		case context != "":
			out = append(out, kv("context", context))
		case tokens != "":
			out = append(out, kv("tokens", tokens))
		}
	}
	out = append(out,
		kv("id", id),
		kv("folder", tailCell(dashIfEmpty(a.Cwd), w-12)),
		kv("branch", branch),
		kv("model", model),
	)
	if t := toolsText(a); t != "" {
		out = append(out, clip("  "+sDim.Render(cell("tools", lw))+" "+sBusy.Render(t), w))
	}
	if f := functionText(a); f != "" {
		if f == noAgentsMD {
			f = "none: add a `# Project: <one line>` line to AGENTS.md"
		}
		out = append(out, kv("function", f))
	}
	if a.Profile.Task != "" {
		out = append(out, kv("task", a.Profile.Task))
	}
	if a.Profile.Expertise != "" {
		out = append(out, kv("expert", a.Profile.Expertise))
	}
	if a.PeerTrust {
		out = append(out, kv("trust", "acts on peer requests without asking (t: off)"))
	}
	if a.Worker != nil && a.Worker.Permissions != "" {
		note := ""
		if a.Harness != "codex" {
			note = " (not enforced yet: Codex only)"
		}
		out = append(out, kv("sandbox", a.Worker.Permissions+note))
	}
	if a.Delegation != nil {
		out = append(out, kv("policy", policySummary(a.Delegation)+"  (p: change)"))
		if a.Delegation.Main() != "" {
			out = append(out, kv("main", a.Delegation.Main()))
		}
		if a.Delegation.CoderError != "" {
			out = append(out, kv("", "coder model: "+a.Delegation.CoderError))
		}
		for _, s := range m.policySupport(a, a.Delegation) {
			out = append(out, kv("", s))
		}
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
		out = append(out, clip("  "+sDim.Render("project source: ")+sourceText(src), w))
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
	if sharedTree(t) {
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

// toolsText is the warning line for a session whose agent-net tools are older
// than its machine's agent-net. It is empty when the tools are current, and for
// a session that has no agent-net tools process.
func toolsText(a protocol.AgentView) string {
	switch {
	case a.ToolsVersion == protocol.ToolsUnknown:
		return "unknown (from before v0.4.7; reconnect agent-net with /mcp)"
	case a.ToolsOld:
		return sanitize(a.ToolsVersion) + " (old: reconnect agent-net with /mcp)"
	}
	return ""
}

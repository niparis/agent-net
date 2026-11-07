package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"path"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/niparis/agent-net/internal/protocol"
)

type sessionsArchiveForm struct {
	stage   string
	fields  []textinput.Model
	field   int
	query   protocol.SessionsArchiveRequest
	entries []protocol.SessionArchiveEntry
	checked map[string]bool
	index   int
	err     string
	loading bool
}
type sessionsArchiveMsg struct {
	result  protocol.SessionsArchiveResult
	err     error
	applied bool
}

func (m Model) startSessionsArchive() (Model, tea.Cmd) {
	f := &sessionsArchiveForm{stage: "menu", checked: map[string]bool{}}
	for range 7 {
		in := textinput.New()
		in.CharLimit = 2048
		in.SetWidth(52)
		f.fields = append(f.fields, in)
	}
	f.fields[2].SetValue("*")
	f.fields[6].SetValue("false")
	m.archive = f
	return m, nil
}

func (m Model) sessionsArchiveCall(method string, q protocol.SessionsArchiveRequest, applied bool) tea.Cmd {
	c, ok := m.src.(inboxCaller)
	return func() tea.Msg {
		if !ok {
			return sessionsArchiveMsg{err: fmt.Errorf("session archive unavailable")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		raw, err := c.Call(ctx, method, q)
		var result protocol.SessionsArchiveResult
		if err == nil {
			err = json.Unmarshal(raw, &result)
		}
		return sessionsArchiveMsg{result: result, err: err, applied: applied}
	}
}
func (m Model) sessionsArchiveResult(r sessionsArchiveMsg) (Model, tea.Cmd) {
	if m.archive == nil {
		return m, nil
	}
	f := m.archive
	f.loading = false
	if r.err != nil {
		f.err = explain(r.err)
		if f.stage == "confirm" {
			f.stage = "preview"
		}
		return m, nil
	}
	if r.applied {
		m.archive = nil
		m.notice = notice{text: fmt.Sprintf("%d sessions updated.", r.result.Count), at: m.now()}
		return m, m.fetchCmd()
	}
	f.entries = r.result.Sessions
	sort.Slice(f.entries, func(i, j int) bool {
		a, b := f.entries[i], f.entries[j]
		if path.Dir(a.Folder) != path.Dir(b.Folder) {
			return path.Dir(a.Folder) < path.Dir(b.Folder)
		}
		if a.LastSeen != b.LastSeen {
			return a.LastSeen < b.LastSeen
		}
		return a.SessionID < b.SessionID
	})
	f.stage = "preview"
	f.index = 0
	f.checked = map[string]bool{}
	for _, a := range f.entries {
		f.checked[a.SessionID] = true
	}
	return m, nil
}
func (m Model) sessionsArchiveKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := m.archive
	if f.loading {
		return m, nil
	}
	if k.String() == "esc" {
		m.archive = nil
		return m, nil
	}
	f.err = ""
	if f.stage == "menu" {
		switch k.String() {
		case "f":
			f.stage = "filters"
			return m, f.fields[0].Focus()
		case "r":
			f.query = protocol.SessionsArchiveRequest{Restore: true, DryRun: true}
			f.loading = true
			return m, m.sessionsArchiveCall("sessions_archived", f.query, false)
		case "enter":
			f.query = protocol.SessionsArchiveRequest{DryRun: true}
			f.loading = true
			return m, m.sessionsArchiveCall("sessions_archive", f.query, false)
		}
		return m, nil
	}
	if f.stage == "filters" {
		switch k.String() {
		case "tab", "down", "shift+tab", "up":
			f.fields[f.field].Blur()
			delta := 1
			if k.String() == "shift+tab" || k.String() == "up" {
				delta = -1
			}
			f.field = (f.field + delta + len(f.fields)) % len(f.fields)
			return m, f.fields[f.field].Focus()
		case "enter":
			f.query = protocol.SessionsArchiveRequest{Name: f.fields[0].Value(), Folder: f.fields[1].Value(), Harness: f.fields[3].Value(), CreatedBefore: f.fields[4].Value(), EndedWorkers: f.fields[6].Value() == "true", DryRun: true}
			if p := f.fields[2].Value(); p != "*" {
				f.query.Project = &p
			}
			if ids := strings.Fields(strings.ReplaceAll(f.fields[5].Value(), ",", " ")); len(ids) > 0 {
				f.query.IDs = ids
			}
			if v := f.fields[6].Value(); v != "true" && v != "false" {
				f.err = "Ended workers must be true or false."
				return m, nil
			}
			f.loading = true
			return m, m.sessionsArchiveCall("sessions_archive", f.query, false)
		}
		var cmd tea.Cmd
		f.fields[f.field], cmd = f.fields[f.field].Update(k)
		return m, cmd
	}
	switch k.String() {
	case "up", "k":
		f.index = max(0, f.index-1)
	case "down", "j":
		f.index = min(max(0, len(f.entries)-1), f.index+1)
	case "space":
		if f.stage == "preview" && len(f.entries) > 0 {
			id := f.entries[f.index].SessionID
			f.checked[id] = !f.checked[id]
		}
	case "enter":
		if f.stage == "preview" {
			count := 0
			for _, a := range f.entries {
				if f.checked[a.SessionID] {
					count++
				}
			}
			if count > 0 {
				f.stage = "confirm"
			}
		}
	case "y":
		if f.stage == "confirm" {
			q := protocol.SessionsArchiveRequest{Restore: f.query.Restore, IDs: []string{}, Expected: []string{}}
			for _, a := range f.entries {
				if f.checked[a.SessionID] {
					q.IDs = append(q.IDs, a.SessionID)
				}
			}
			q.Expected = q.IDs
			f.loading = true
			return m, m.sessionsArchiveCall("sessions_archive", q, true)
		}
	case "n":
		if f.stage == "confirm" {
			f.stage = "preview"
		}
	}
	return m, nil
}
func (m Model) sessionsArchiveHints() string {
	switch m.archive.stage {
	case "menu":
		return keyHints("enter", "preview all", "f", "filters", "r", "show archived", "esc", "cancel")
	case "filters":
		return keyHints("tab", "next field", "enter", "preview", "esc", "cancel")
	case "confirm":
		return keyHints("y", "confirm", "n", "back", "↑↓", "review", "esc", "cancel")
	default:
		return keyHints("↑↓", "review", "space", "select", "enter", "confirm", "esc", "cancel")
	}
}

// archiveColumns keeps every decision field visible even at 80 columns.
func archiveColumns(w int) []int {
	if w >= 110 {
		return []int{18, 14, 9, 12, 10, max(1, w-73)}
	}
	return []int{12, 10, 8, 8, 9, max(1, w-57)}
}
func archiveRow(values []string, widths []int) string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = cell(v, widths[i])
	}
	return strings.Join(out, " ")
}
func (m Model) sessionsArchiveView(w, h int) []string {
	f := m.archive
	title := "Archive offline sessions"
	body := []string{}
	switch f.stage {
	case "menu":
		body = append(body, "Choose offline sessions to archive, or restore archived sessions.", "", "Enter  Preview all offline sessions", "f      Filter by name, folder, project, harness, date or IDs", "r      Show archived sessions and restore")
	case "filters":
		title += " · select"
		body = append(body, "Filters combine with AND. Name and folder use globs.", "Project: * means any; empty means no project.", "")
		labels := []string{"Name glob", "Folder glob", "Project", "Harness", "Created before", "IDs (comma-separated)", "Ended workers"}
		for i, label := range labels {
			mark := "  "
			if i == f.field {
				mark = "> "
			}
			body = append(body, mark+fmt.Sprintf("%-22s", label)+f.fields[i].View())
		}
	default:
		verb, kind := "Archive", "offline"
		if f.query.Restore {
			verb, kind = "Restore", "archived"
			title = "Restore archived sessions"
		}
		if f.stage == "confirm" {
			title += " · confirm"
		} else {
			title += " · dry run"
		}
		count := 0
		for _, a := range f.entries {
			if f.checked[a.SessionID] {
				count++
			}
		}
		summary := fmt.Sprintf("%s %s", verb, plural(count, kind+" session"))
		if f.stage == "confirm" {
			summary = "Confirm " + strings.ToLower(verb) + ": " + plural(count, kind+" session")
		}
		body = append(body, summary+" · "+fmt.Sprintf("%d matched", len(f.entries)))
		widths := archiveColumns(w - 4)
		body = append(body, "     "+archiveRow([]string{"NAME", "PROJECT", "HARNESS", "MACHINE", "LAST SEEN", "FOLDER"}, widths))
		room := max(1, h-6)
		start := max(0, f.index-room+1)
		end := min(len(f.entries), start+room)
		for i := start; i < end; i++ {
			a := f.entries[i]
			mark := " "
			if i == f.index {
				mark = ">"
			}
			check := " "
			if f.checked[a.SessionID] {
				check = "x"
			}
			seen := "unknown"
			if a.LastSeen > 0 {
				age := m.now().Sub(time.Unix(a.LastSeen, 0))
				if age >= 24*time.Hour {
					seen = fmt.Sprintf("%dd ago", int(age.Hours()/24))
				} else {
					seen = ago(age) + " ago"
				}
			}
			folder := sanitize(a.Folder)
			if size := ansi.StringWidth(folder); size > widths[5] {
				folder = ansi.TruncateLeft(folder, size-widths[5]+1, "…")
			}
			line := mark + "[" + check + "] " + archiveRow([]string{a.Name, orStr(a.Project, "no project"), a.Harness, orStr(a.Machine, "unknown"), seen, folder}, widths)
			if i == f.index {
				line = selectedRow(line)
			}
			body = append(body, line)
		}
		body = append(body, fmt.Sprintf("Rows %d-%d of %d · folder group, oldest first", min(start+1, len(f.entries)), end, len(f.entries)))
	}
	if f.loading {
		body = append(body, "Loading...")
	}
	if f.err != "" {
		body = append(body, "! "+sanitize(f.err))
	}
	for len(body) < h-2 {
		body = append(body, "")
	}
	return panel(title, body[:min(len(body), max(0, h-2))], w, roleAccent)
}

type removeSessionsMsg struct {
	ids  []string
	name string
	all  bool
	err  error
}

func (m Model) startRemoveOffline() (Model, tea.Cmd) {
	m.removeIDs = nil
	for _, a := range m.visible() {
		if !a.Online {
			m.removeIDs = append(m.removeIDs, a.SessionID)
		}
	}
	if len(m.removeIDs) == 0 {
		m.fail("No offline sessions shown.")
		return m, nil
	}
	m.removeAll = true
	m.mode = modeConfirm
	m.notice = notice{}
	return m, nil
}

func (m Model) removeSessionsCmd(ids []string, name string, all bool) tea.Cmd {
	c, ok := m.src.(inboxCaller)
	return func() tea.Msg {
		result := removeSessionsMsg{ids: ids, name: name, all: all}
		if !ok {
			result.err = fmt.Errorf("session archive unavailable")
			return result
		}
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		_, result.err = c.Call(ctx, "sessions_archive", protocol.SessionsArchiveRequest{IDs: ids, Expected: ids})
		return result
	}
}

func (m Model) removeSessionsResult(r removeSessionsMsg) (Model, tea.Cmd) {
	if r.err != nil {
		m.fail(explain(r.err))
		return m, nil
	}
	ids := make(map[string]bool, len(r.ids))
	for _, id := range r.ids {
		ids[id] = true
	}
	agents := make([]protocol.AgentView, 0, len(m.snap.Agents))
	for _, a := range m.snap.Agents {
		if !ids[a.SessionID] {
			agents = append(agents, a)
		}
	}
	m.snap.Agents = agents
	m.ensureSelection()
	if r.all {
		m.say(fmt.Sprintf("Removed %d offline sessions.", len(r.ids)))
	} else {
		m.say("Removed " + r.name + ".")
	}
	return m, m.fetchCmd()
}

package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
func (m Model) sessionsArchiveView(w, h int) []string {
	f := m.archive
	lines := []string{"  Offline sessions", ""}
	footer := "  esc cancel"
	switch f.stage {
	case "menu":
		lines = append(lines, "  Archive offline...", "  You can restore archived sessions here.", "", "  enter  Preview all offline sessions", "  f      Select by name, folder, project, harness, date or IDs", "  r      Show archived, restore")
	case "filters":
		lines = append(lines, "  Archive offline... Select sessions", "  Filters combine with AND. Name and folder use globs.", "  Project: * means any; empty means no project.", "")
		labels := []string{"Name glob", "Folder glob", "Project", "Harness", "Created before", "IDs (comma-separated)", "Ended workers"}
		for i, label := range labels {
			mark := "  "
			if i == f.field {
				mark = "> "
			}
			lines = append(lines, "  "+mark+fmt.Sprintf("%-22s", label)+f.fields[i].View())
		}
		footer = "  tab next field  enter preview  esc cancel"
	default:
		verb, kind := "Archive", "offline"
		if f.query.Restore {
			verb, kind = "Restore", "archived"
		}
		count := 0
		for _, a := range f.entries {
			if f.checked[a.SessionID] {
				count++
			}
		}
		title := fmt.Sprintf("  %s %d %s session(s) - dry run", verb, count, kind)
		if f.stage == "confirm" {
			title = fmt.Sprintf("  Confirm %s: %d session(s)", strings.ToLower(verb), count)
		}
		lines = append(lines, title, "  Names, IDs and folders below. History keeps its names.", "")
		room := max(1, h-11)
		start := max(0, f.index-room+1)
		for i := start; i < min(len(f.entries), start+room); i++ {
			a := f.entries[i]
			mark := " "
			if i == f.index {
				mark = ">"
			}
			check := " "
			if f.checked[a.SessionID] {
				check = "x"
			}
			lines = append(lines, clip(fmt.Sprintf(" %s [%s] %s  %s  %s", mark, check, sanitize(a.Name), sanitize(a.SessionID), sanitize(a.Folder)), w))
		}
		lines = append(lines, "", fmt.Sprintf("  %d matched; %d selected. Use arrows to review every record.", len(f.entries), count))
		if f.stage == "confirm" {
			footer = "  y confirm  n back  esc cancel"
		} else {
			footer = "  space select  enter confirm  esc cancel"
		}
	}
	if f.loading {
		lines = append(lines, "  Loading...")
	}
	if f.err != "" {
		lines = append(lines, clip("  ! "+sanitize(f.err), w))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	lines[h-1] = footer
	for i := range lines {
		lines[i] = cell(lines[i], w)
	}
	return lines[:min(len(lines), h)]
}

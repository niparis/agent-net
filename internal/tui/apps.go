package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

type connectedApp struct {
	ID, Name, Kind, ClientName string
	RedirectURIs               []string
	CreatedAt                  time.Time
	LastUsedAt, RevokedAt      *time.Time
}
type pendingApp struct{ ID, ClientName, RedirectHost, DefaultKind string }
type appsPublicStatus struct {
	protocol.AppsPublicStatus
	OtherRoutes json.RawMessage `json:"other_routes"`
}

type appsForm struct {
	public         appsPublicStatus
	addAfterPublic bool
	apps           []connectedApp
	index          int
	stage          string
	input          textinput.Model
	pending        pendingApp
	kind           string
	err            string
	loading        bool
	editingName    bool
}
type appsResult struct {
	op  string
	raw json.RawMessage
	err error
}

func (m Model) startApps() (Model, tea.Cmd) {
	m.mode = modeApps
	m.apps = appsForm{loading: true}
	return m, m.appsCall("apps.list", struct{}{})
}
func (m Model) appsCall(op string, params any) tea.Cmd {
	c, ok := m.src.(inboxCaller)
	return func() tea.Msg {
		if !ok {
			return appsResult{op: op, err: fmt.Errorf("connected apps unavailable")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		raw, err := c.Call(ctx, op, params)
		return appsResult{op: op, raw: raw, err: err}
	}
}
func (m Model) onAppsResult(r appsResult) (Model, tea.Cmd) {
	if m.mode != modeApps && m.tab != settingsTab {
		return m, nil
	}
	f := &m.apps
	f.loading = false
	if r.err != nil {
		f.err = r.err.Error()
		if strings.HasPrefix(r.op, "apps.public.") {
			f.public.Error = f.err
			f.err = ""
			f.stage = ""
			return m, nil
		}
		if r.op == "apps.pending" && strings.Contains(f.err, "no rows") {
			f.err = "No request with that code (expired or mistyped)"
		}
		if strings.Contains(f.err, "too many tries") {
			minutes := 10
			if _, suffix, ok := strings.Cut(f.err, "too many tries: "); ok {
				if fields := strings.Fields(suffix); len(fields) > 0 {
					if n, err := strconv.Atoi(fields[0]); err == nil && n > 0 {
						minutes = n
					}
				}
			}
			f.err = fmt.Sprintf("Too many wrong codes. Try again in %d minutes.", minutes)
		}
		return m, nil
	}
	f.err = ""
	switch r.op {
	case "apps.list":
		var v struct {
			Apps []connectedApp `json:"apps"`
		}
		if err := json.Unmarshal(r.raw, &v); err != nil {
			f.err = err.Error()
			return m, nil
		}
		f.apps = nil
		for _, a := range v.Apps {
			if a.RevokedAt == nil {
				f.apps = append(f.apps, a)
			}
		}
		f.index = min(f.index, max(0, len(f.apps)-1))
		return m, m.appsCall("apps.public.status", struct{}{})
	case "apps.public.status", "apps.public.on", "apps.public.off", "apps.public.url":
		var status appsPublicStatus
		if err := json.Unmarshal(r.raw, &status); err != nil {
			f.err = err.Error()
			return m, nil
		}
		f.public = status
		if f.addAfterPublic && f.public.On {
			f.addAfterPublic = false
			f.startCode()
		} else if r.op != "apps.public.status" {
			f.stage = ""
		}
	case "apps.pending":
		var v struct {
			Pending pendingApp `json:"pending"`
		}
		if err := json.Unmarshal(r.raw, &v); err != nil {
			f.err = err.Error()
			return m, nil
		}
		f.pending = v.Pending
		f.editingName = false
		f.kind = "bot"
		if v.Pending.DefaultKind == "owner_app" {
			f.kind = "owner_app"
		}
		f.stage = "approve"
		f.input = newTextInput()
		name := v.Pending.ClientName
		if f.kind != "owner_app" {
			name = strings.Join(strings.Fields(strings.ToLower(name)), "-")
		}
		f.input.SetValue(name)
		f.input.CursorEnd()
		f.input.Focus()
		f.editingName = true
	default:
		f.stage = ""
		f.loading = true
		return m, m.appsCall("apps.list", struct{}{})
	}
	return m, nil
}
func (m Model) appsKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := &m.apps
	if f.loading {
		return m, nil
	}
	if f.stage == "public-confirm" {
		yes, no := confirmAnswer(k)
		if yes {
			f.addAfterPublic = true
			f.loading = true
			return m, m.appsCall("apps.public.on", struct{}{})
		}
		if no {
			f.stage = ""
		}
		return m, nil
	}
	if f.stage == "confirm" {
		yes, no := confirmAnswer(k)
		if yes {
			f.loading = true
			return m, m.appsCall("apps.revoke", map[string]string{"id": f.apps[f.index].ID})
		}
		if no {
			f.stage = ""
		}
		return m, nil
	}
	if f.stage == "rename" {
		if k.Code == tea.KeyEsc {
			f.stage = ""
			f.err = ""
			return m, nil
		}
		if k.Code == tea.KeyEnter {
			name := strings.TrimSpace(f.input.Value())
			if name == "" {
				f.err = "Name cannot be empty"
				return m, nil
			}
			f.loading = true
			return m, m.appsCall(protocol.OpAppsRename, map[string]string{"id": f.apps[f.index].ID, "name": name})
		}
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(k)
		return m, cmd
	}
	if f.stage == "code" || f.stage == "approve" || f.stage == "url" {
		if k.Code == tea.KeyEsc {
			f.stage = ""
			f.err = ""
			return m, nil
		}
		if f.stage == "approve" {
			switch k.String() {
			case "up", "down", "tab":
				f.editingName = !f.editingName
				return m, nil
			case "r":
				if !f.editingName {
					f.loading = true
					return m, m.appsCall("apps.refuse", map[string]string{"id": f.pending.ID})
				}
			case "left", "right":
				if f.editingName {
					break
				}
				if f.kind == "bot" {
					f.kind = "owner_app"
				} else {
					f.kind = "bot"
				}
				return m, nil

			}
		}
		if k.Code == tea.KeyEnter {
			f.loading = true
			if f.stage == "url" {
				return m, m.appsCall("apps.public.url", map[string]string{"url": f.input.Value()})
			}
			if f.stage == "code" {
				return m, m.appsCall("apps.pending", map[string]string{"code": f.input.Value()})
			}
			return m, m.appsCall("apps.approve", map[string]string{"id": f.pending.ID, "kind": f.kind, "name": f.input.Value()})
		}
		if f.stage == "approve" && !f.editingName {
			return m, nil
		}
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(k)
		return m, cmd
	}
	switch keyAction(contextApps, k.String()) {
	case "back":
		m.mode = modeNormal
	case actUp:
		f.index = max(0, f.index-1)
	case actDown:
		f.index = min(max(0, len(f.apps)-1), f.index+1)
	case "public":
		op := "apps.public.on"
		if f.public.On {
			op = "apps.public.off"
		}
		f.loading = true
		return m, m.appsCall(op, struct{}{})
	case "url":
		f.stage = "url"
		f.input = newTextInput()
		f.input.CharLimit = 2048
		f.input.SetWidth(60)
		f.input.Focus()
	case "add":
		if !f.public.On {
			f.stage = "public-confirm"
		} else {
			f.startCode()
		}
	case "revoke":
		if len(f.apps) > 0 {
			f.stage = "confirm"
		}
	}
	if k.Code == tea.KeyEnter && len(f.apps) > 0 {
		f.stage = "rename"
		f.input = newTextInput()
		f.input.CharLimit = protocol.MaxNodeName
		f.input.Prompt = "name for " + sanitize(f.apps[f.index].Name) + "> "
		f.input.SetValue(f.apps[f.index].Name)
		f.input.CursorEnd()
		f.input.Focus()
		f.err = ""
	}
	return m, nil
}
func (m Model) appsDate(t time.Time) string {
	t = m.displayTime(t.Unix())
	now := m.now().In(m.loc)
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return "today " + t.Format("15:04")
	}
	return t.Format("Jan 02")
}

func (m Model) appsView(w, rows int) []string {
	f := m.apps
	out := []string{section("Connected apps", w)}
	status := "off"
	if f.public.On {
		phone := strings.TrimRight(f.public.URL, "/") + "/"
		bots := strings.TrimRight(f.public.URL, "/") + "/mcp"
		status = "on   phone " + phone + "   bots " + bots
		if ansi.StringWidth("Reachable from the internet: "+status) > w-2 {
			status = "on   " + phone + "   " + bots
		}
		if ansi.StringWidth("Reachable from the internet: "+status) > w-2 {
			available := w - 2 - ansi.StringWidth("Reachable from the internet: on      ")
			left := available / 2
			status = "on   " + strings.TrimRight(cellDots(phone, left), " ") + "   " + strings.TrimRight(cellDots(bots, available-left), " ")
		}
	}
	for _, line := range wrap("Reachable from the internet: "+status, w-2) {
		out = append(out, "  "+line)
	}
	if warning := otherRoutesWarning(f.public.OtherRoutes); warning != "" {
		for _, line := range wrap(warning, w-2) {
			out = append(out, "  "+lipgloss.NewStyle().Foreground(roleWarning).Render(line))
		}
	}
	if f.public.Error != "" {
		for _, line := range wrap(sanitize(f.public.Error), w-2) {
			out = append(out, "  "+lipgloss.NewStyle().Foreground(roleWarning).Render(line))
		}
	}
	if strings.Contains(strings.ToLower(firstNonEmpty(f.public.Error, f.err)), "tailscale is not installed") {
		out = append(out, "  u: paste your own tunnel URL")
	}
	out = append(out, "")
	switch f.stage {
	case "public-confirm":
		out = append(out, "  Turn on internet access? y/n")
	case "url":
		in := f.input
		in.Prompt = ""
		out = append(out, "  Public tunnel URL (HTTPS):", "  "+in.View())
	case "code":
		in := f.input
		in.Prompt = ""
		out = append(out, "  Code shown in your browser:", "  "+in.View())
	case "rename":
		in := f.input
		in.Prompt = ""
		out = append(out, "  "+f.input.Prompt+in.View())
		if f.err != "" {
			out = append(out, "  "+sBad.Render(f.err))
		}
	case "approve":
		kind := "bot"
		if f.kind == "owner_app" {
			kind = "owner app"
		}
		name := sanitize(f.input.Value())
		if f.editingName {
			in := f.input
			in.Prompt = ""
			name = in.View()
		}
		kindFocus := "  "
		nameFocus := "  "
		if !f.editingName {
			kindFocus = "▸ "
		} else {
			nameFocus = "▸ "
		}
		out = append(out, "  Approve this app?", "", "    Client       "+sanitize(f.pending.ClientName), "    Return host  "+sBold.Render(sanitize(f.pending.RedirectHost)), "  "+kindFocus+"Kind         ‹ "+kind+" ›   "+sDim.Render("(left/right: bot or owner app)"), "  "+nameFocus+"Name         "+name, "")
		first := "Approve only if you just pressed connect in this app and the return host is the one you expect."
		second := "A bot acts under its own name; an owner app acts as you."
		for _, line := range wrap(first, w-4) {
			out = append(out, "  "+sDim.Render(line))
		}
		for _, line := range wrap(second, w-4) {
			if f.kind == "owner_app" {
				out = append(out, "  "+sBad.Render(line))
			} else {
				out = append(out, "  "+sDim.Render(line))
			}
		}
	default:
		if len(f.apps) == 0 {
			for _, line := range wrap("No connected apps. Press a to add one: open the app's connect page, then type the code it shows.", w-4) {
				out = append(out, "  "+line)
			}
		} else {
			headers := []string{"NAME", "KIND", "CLIENT", "RETURN HOST", "CONNECTED", "LAST USED"}
			cells := make([][]string, len(f.apps))
			widths := make([]int, len(headers))
			for i, h := range headers {
				widths[i] = ansi.StringWidth(h)
			}
			for i, a := range f.apps {
				kind := a.Kind
				if kind == "owner_app" {
					kind = "owner app"
				}
				host := "-"
				if len(a.RedirectURIs) > 0 {
					if u, e := url.Parse(a.RedirectURIs[0]); e == nil {
						host = u.Hostname()
					}
				}
				used := "never"
				if a.LastUsedAt != nil {
					used = m.appsDate(*a.LastUsedAt)
				}
				cells[i] = []string{a.Name, kind, a.ClientName, host, m.appsDate(a.CreatedAt), used}
				for j, v := range cells[i] {
					widths[j] = max(widths[j], ansi.StringWidth(sanitize(v)))
				}
			}
			total := 2 + 2*(len(headers)-1)
			for _, n := range widths {
				total += n
			}
			for total > w {
				biggest := -1
				for i, n := range widths {
					if n > ansi.StringWidth(headers[i]) && (biggest < 0 || n > widths[biggest]) {
						biggest = i
					}
				}
				if biggest < 0 {
					break
				}
				widths[biggest]--
				total--
			}
			row := func(parts []string) string {
				var cols []string
				for i, v := range parts {
					cols = append(cols, cell(v, widths[i]))
				}
				return strings.Join(cols, "  ")
			}
			out = append(out, sDim.Render(clip("  "+row(headers), w)))
			visible := max(1, rows-len(out)-2)
			start := max(0, f.index-visible+1)
			end := min(len(f.apps), start+visible)
			for i := start; i < end; i++ {
				line := "  " + row(cells[i])
				if i == f.index {
					line = lipgloss.NewStyle().Foreground(roleAccent).Render("▸") + " " + row(cells[i])
					line = selectedRow(clip(line, w))
				}
				out = append(out, clip(line, w))
			}
		}
		if f.stage == "confirm" && len(f.apps) > 0 {
			out = append(out, fmt.Sprintf("  Revoke %s? Its tokens stop working at once. (y/n)", sanitize(f.apps[f.index].Name)))
		}
	}
	if f.loading {
		out = append(out, "  Loading...")
	}
	if f.err != "" {
		out = append(out, "  "+lipgloss.NewStyle().Foreground(roleWarning).Render(sanitize(f.err)))
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out
}

func (f *appsForm) startCode() {
	f.stage = "code"
	f.input = newTextInput()
	f.input.CharLimit = 7
	f.input.Focus()
	f.err = ""
}

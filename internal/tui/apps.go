package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type connectedApp struct {
	ID, Name, Kind, ClientName string
	RedirectURIs               []string
	CreatedAt                  time.Time
	LastUsedAt, RevokedAt      *time.Time
}
type pendingApp struct{ ID, ClientName, RedirectHost string }
type appsForm struct {
	apps        []connectedApp
	index       int
	stage       string
	input       textinput.Model
	pending     pendingApp
	kind        string
	err         string
	loading     bool
	editingName bool
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
	if m.mode != modeApps {
		return m, nil
	}
	f := &m.apps
	f.loading = false
	if r.err != nil {
		f.err = r.err.Error()
		if r.op == "apps.pending" && strings.Contains(f.err, "no rows") {
			f.err = "No request with that code (expired or mistyped)"
		}
		if strings.Contains(f.err, "too many tries") {
			f.err = "Too many wrong codes. Try again in 10 minutes."
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
		f.stage = "approve"
		f.input = textinput.New()
		f.input.SetValue(strings.Join(strings.Fields(strings.ToLower(v.Pending.ClientName)), "-"))
		f.input.Focus()
	default:
		f.stage = ""
		f.loading = true
		return m, m.appsCall("apps.list", struct{}{})
	}
	return m, nil
}
func (m Model) appsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.apps
	if f.loading {
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
	if f.stage == "code" || f.stage == "approve" {
		if k.Type == tea.KeyEsc {
			f.stage = ""
			f.err = ""
			return m, nil
		}
		if f.stage == "approve" {
			switch k.String() {
			case "tab":
				f.editingName = !f.editingName
				return m, nil
			case "r":
				if !f.editingName {
					f.loading = true
					return m, m.appsCall("apps.refuse", map[string]string{"id": f.pending.ID})
				}
			case "left", "right":
				if f.kind == "bot" {
					f.kind = "owner_app"
				} else {
					f.kind = "bot"
				}
				return m, nil

			}
		}
		if k.Type == tea.KeyEnter {
			f.loading = true
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
	case "add":
		f.stage = "code"
		f.input = textinput.New()
		f.input.CharLimit = 7
		f.input.Focus()
		f.err = ""
	case "revoke":
		if len(f.apps) > 0 {
			f.stage = "confirm"
		}
	}
	return m, nil
}
func (m Model) appsView(w, rows int) []string {
	f := m.apps
	out := []string{section("Connected apps", w)}
	switch f.stage {
	case "code":
		out = append(out, "  Code shown in your browser:", "  "+f.input.View())
	case "approve":
		kind := "bot"
		if f.kind != "bot" {
			kind = "owner app"
		}
		out = append(out, "  Client: "+sanitize(f.pending.ClientName), "  Redirect host: "+sBold.Render(sanitize(f.pending.RedirectHost)), "  Kind: "+kind+"  (left/right)", "  Name: "+f.input.View(), "  Tab edits name; Enter approves; r refuses; Esc cancels.")
	default:
		if len(f.apps) == 0 {
			out = append(out, "  No connected apps.")
		}
		visible := max(1, (rows-3)/2)
		start := max(0, f.index-visible+1)
		end := min(len(f.apps), start+visible)
		for i := start; i < end; i++ {
			a := f.apps[i]
			mark := "  "
			if i == f.index {
				mark = "> "
			}
			kind := a.Kind
			if kind == "owner_app" {
				kind = "owner app"
			}
			host := ""
			if len(a.RedirectURIs) > 0 {
				if u, e := url.Parse(a.RedirectURIs[0]); e == nil {
					host = u.Hostname()
				}
			}
			used := "never"
			if a.LastUsedAt != nil {
				used = a.LastUsedAt.Format("2006-01-02")
			}
			out = append(out, clip(mark+sanitize(a.Name)+"  "+kind+"  "+sanitize(a.ClientName)+"  "+sanitize(host), w))
			out = append(out, clip("    connected "+a.CreatedAt.Format("2006-01-02")+"  last used "+used, w))
		}
		if f.stage == "confirm" && len(f.apps) > 0 {
			out = append(out, fmt.Sprintf("  Revoke %s? Its tokens stop working at once. (y/n)", sanitize(f.apps[f.index].Name)))
		}
	}
	if f.loading {
		out = append(out, "  Loading...")
	}
	if f.err != "" {
		out = append(out, "  "+sanitize(f.err))
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out
}

package tui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

// The delegation policy form (decision 0014): key p on the selected session.
// The owner picks how the session handles code changes (do it itself, a native
// subagent, a worker), the coder model, an optional main model and whether it
// is for this session or for the whole project. The gateway resolves the
// loose model names and the form shows the result before it saves.

type policyField int

const (
	pfDelegate policyField = iota
	pfCoder
	pfMain
	pfWorkers
	pfScope
	nPolicyFields
)

var policyLabels = [nPolicyFields]string{"Delegate", "Coder", "Main", "Workers", "Scope"}

// delegateChoices are the values of the Delegate field, in the order the
// left and right keys cycle through them. "" clears the policy.
var delegateChoices = []string{"", protocol.DelegateOff, protocol.DelegateNative, protocol.DelegateWorkers}

// workersChoices are the values of the Workers field: the permission level of
// unattended workers started in the project (decision 0018). "" is the
// default, sandboxed.
var workersChoices = append([]string{""}, protocol.WorkersLevels...)

type policyForm struct {
	sessionID string
	name      string // "name (host)"
	harness   string
	project   string
	delegate  int // index in delegateChoices
	workers   int // index in workersChoices
	scope     string
	coder     textinput.Model
	main      textinput.Model
	cur       policyField
	err       string
	// preview is the last answer of the gateway for the values in the form.
	preview    *protocol.DelegationView
	previewErr string
	seq        int // the preview that is current
}

// policyPreviewMsg is the gateway's answer to a dry run.
type policyPreviewMsg struct {
	seq int
	res protocol.SessionPolicyResult
	err error
}

// policySavedMsg is the end of a save.
type policySavedMsg struct {
	res protocol.SessionPolicyResult
	err error
}

func newPolicyInput(v string) textinput.Model {
	t := textinput.New()
	t.Prompt = ""
	t.CharLimit = protocol.MaxModelName
	t.Cursor.SetMode(cursor.CursorHide)
	t.SetValue(v)
	return t
}

func (f policyForm) delegateValue() string { return delegateChoices[f.delegate] }

// policy builds the policy the form describes.
func (f policyForm) policy() protocol.DelegationPolicy {
	p := protocol.DelegationPolicy{Delegate: f.delegateValue(), MainModel: strings.TrimSpace(f.main.Value()),
		Workers: workersChoices[f.workers]}
	if p.Delegate == protocol.DelegateNative || p.Delegate == protocol.DelegateWorkers {
		p.CoderModel = strings.TrimSpace(f.coder.Value())
	}
	return p
}

func (f *policyForm) focus(to policyField) tea.Cmd {
	f.coder.Blur()
	f.main.Blur()
	f.cur = to
	switch to {
	case pfCoder:
		f.coder.CursorEnd()
		return f.coder.Focus()
	case pfMain:
		f.main.CursorEnd()
		return f.main.Focus()
	}
	return nil
}

// startPolicy opens the form for the selected session, filled with its
// current effective policy.
func (m Model) startPolicy() (Model, tea.Cmd) {
	a, ok := m.selectedAgent()
	switch {
	case !ok:
		m.fail("Nothing is selected: pick a session first.")
		return m, nil
	case protocol.IsOwner(a.Harness):
		m.fail("That is you, not an agent session.")
		return m, nil
	}
	f := policyForm{sessionID: a.SessionID, harness: a.Harness, scope: protocol.ScopeSession,
		name:  sanitize(dashIfEmpty(a.Profile.Name)) + " (" + sanitize(a.Hostname) + ")",
		coder: newPolicyInput(""), main: newPolicyInput("")}
	f.project = firstNonEmpty(a.Repo.Project, a.Profile.Project)
	if d := a.Delegation; d != nil {
		for i, c := range delegateChoices {
			if c == d.Delegate {
				f.delegate = i
			}
		}
		for i, c := range workersChoices {
			if c == d.Workers {
				f.workers = i
			}
		}
		f.coder.SetValue(d.CoderModel)
		f.main.SetValue(d.MainModel)
		f.scope = d.Scope
	}
	m.pol = f
	m.mode = modePolicy
	m.notice = notice{}
	return m, m.policyPreviewCmd()
}

// policyPreviewCmd asks the gateway what the values in the form resolve to.
func (m *Model) policyPreviewCmd() tea.Cmd {
	m.pol.seq++
	seq, f, src := m.pol.seq, m.pol, m.src
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		res, err := src.SetSessionPolicy(ctx, protocol.SessionPolicyRequest{SessionID: f.sessionID,
			Policy: f.policy(), Scope: f.scope, DryRun: true})
		return policyPreviewMsg{seq: seq, res: res, err: err}
	}
}

func (m Model) onPolicyPreview(r policyPreviewMsg) (Model, tea.Cmd) {
	if m.mode != modePolicy || r.seq != m.pol.seq {
		return m, nil // an old answer
	}
	if r.err != nil {
		m.pol.preview, m.pol.previewErr = nil, explain(r.err)
		return m, nil
	}
	m.pol.preview, m.pol.previewErr = r.res.Effective, ""
	return m, nil
}

func (m Model) onPolicySaved(r policySavedMsg) (Model, tea.Cmd) {
	if r.err != nil {
		m.pol.err = explain(r.err)
		return m, nil
	}
	m.mode = modeNormal
	text := "Policy saved for " + m.pol.name + ". It applies on the session's next turn."
	if m.pol.scope == protocol.ScopeProject && m.pol.project != "" {
		text = "Policy saved for project " + sanitize(m.pol.project) + " on that machine. Sessions of it follow it on their next turn."
	}
	if m.pol.policy().Empty() {
		text = "Policy cleared."
	}
	return m.onAction(actionMsg{text: text})
}

func (m Model) policyKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.pol
	changed := false
	switch k.Type {
	case tea.KeyEsc:
		m.mode = modeNormal
		m.say("Policy unchanged.")
		return m, nil
	case tea.KeyTab, tea.KeyDown:
		if f.cur < pfScope {
			return m, tea.Batch(f.focus(f.cur+1), m.policyPreviewCmd())
		}
		return m, nil
	case tea.KeyShiftTab, tea.KeyUp:
		if f.cur > 0 {
			return m, tea.Batch(f.focus(f.cur-1), m.policyPreviewCmd())
		}
		return m, nil
	case tea.KeyEnter:
		if f.cur < pfScope {
			return m, tea.Batch(f.focus(f.cur+1), m.policyPreviewCmd())
		}
		return m.savePolicy()
	case tea.KeyLeft, tea.KeyRight:
		step := 1
		if k.Type == tea.KeyLeft {
			step = -1
		}
		switch f.cur {
		case pfDelegate:
			f.delegate = (f.delegate + step + len(delegateChoices)) % len(delegateChoices)
			changed = true
		case pfWorkers:
			f.workers = (f.workers + step + len(workersChoices)) % len(workersChoices)
			if workersChoices[f.workers] != "" {
				f.scope = protocol.ScopeProject // the level is a project setting
			}
			changed = true
		case pfScope:
			f.scope = map[string]string{protocol.ScopeSession: protocol.ScopeProject, protocol.ScopeProject: protocol.ScopeSession}[f.scope]
			changed = true
		}
		if changed {
			f.err = ""
			return m, m.policyPreviewCmd()
		}
	}
	if f.cur == pfDelegate || f.cur == pfWorkers || f.cur == pfScope {
		if k.String() == " " {
			return m.policyKey(tea.KeyMsg{Type: tea.KeyRight})
		}
		return m, nil
	}
	f.err = ""
	var cmd tea.Cmd
	if f.cur == pfCoder {
		f.coder, cmd = f.coder.Update(k)
	} else {
		f.main, cmd = f.main.Update(k)
	}
	return m, cmd
}

func (m Model) savePolicy() (tea.Model, tea.Cmd) {
	f := m.pol
	pol := f.policy()
	if (pol.Delegate == protocol.DelegateNative || pol.Delegate == protocol.DelegateWorkers) && pol.CoderModel == "" {
		m.pol.err = "Name the coder model."
		return m, m.pol.focus(pfCoder)
	}
	if f.workers != 0 && f.scope != protocol.ScopeProject {
		m.pol.err = "The workers level is set per project: use scope project."
		return m, nil
	}
	if f.scope == protocol.ScopeProject && f.project == "" {
		m.pol.err = "This session has no project: use scope session."
		return m, nil
	}
	src := m.src
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := src.SetSessionPolicy(ctx, protocol.SessionPolicyRequest{SessionID: f.sessionID, Policy: pol, Scope: f.scope})
		return policySavedMsg{res: res, err: err}
	}
}

// policySupport lists what the harness of a session will not do with a policy,
// in plain words, using what the machine reported.
func (m Model) policySupport(a protocol.AgentView, d *protocol.DelegationView) []string {
	sup := protocol.SupportFor(a.Harness)
	var out []string
	if !sup.Applied {
		return []string{sup.Note}
	}
	if a.Harness == "claude" {
		for _, n := range m.snap.Nodes {
			if n.NodeID != a.NodeID {
				continue
			}
			for _, h := range n.Harnesses {
				if h.Name == "claude" && !h.PolicyReady {
					out = append(out, "not applied yet: "+sup.Note)
				}
			}
		}
	}
	if d == nil {
		return out
	}
	if d.Delegate == protocol.DelegateNative && !sup.Native && sup.NativeNote != "" {
		out = append(out, sup.NativeNote)
	}
	if d.MainModel != "" && !sup.MainModel && sup.MainNote != "" {
		out = append(out, sup.MainNote)
	}
	return out
}

// policySummary is the "policy" line of the detail pane: the mode and the
// coder. The main model has its own line.
func policySummary(d *protocol.DelegationView) string {
	if d == nil {
		return ""
	}
	s := d.Delegate
	if s == "" && d.Main() == "" && d.Workers != "" {
		s = "none (workers " + d.Workers + ")"
	} else if s == "" {
		s = "main model only"
	}
	if d.Coder() != "" && d.Delegate != "" {
		s += " -> " + d.Coder()
	}
	return s + " (" + d.Scope + ")"
}

// policyBadge is the short mark of the sessions table.
func policyBadge(d *protocol.DelegationView) string {
	switch {
	case d == nil || d.Delegate == "":
		return ""
	case d.Delegate == protocol.DelegateOff:
		return "own"
	case d.Delegate == protocol.DelegateNative:
		return "sub"
	}
	return "wrk"
}

// policyView draws the form in the place of the detail pane and the feed.
func (m Model) policyView(w, rows int) []string {
	f := m.pol
	out := []string{section("Delegation policy: "+f.name+", "+sanitize(f.harness), w)}
	const lw = 9
	value := func(i policyField) string {
		switch i {
		case pfDelegate:
			c := f.delegateValue()
			if c == "" {
				c = "none (no policy)"
			}
			if i == f.cur {
				return "< " + c + " >"
			}
			return c
		case pfWorkers:
			c := workersChoices[f.workers]
			if c == "" {
				c = protocol.WorkersSandboxed + " (default)"
			}
			if i == f.cur {
				return "< " + c + " >"
			}
			return c
		case pfScope:
			s := f.scope
			if f.scope == protocol.ScopeProject {
				s += " (" + sanitize(dashIfEmpty(f.project)) + ", every session of it on this machine)"
			}
			if i == f.cur {
				return "< " + s + " >"
			}
			return s
		case pfCoder:
			if i == f.cur {
				return f.coder.View()
			}
			return sanitize(f.coder.Value())
		}
		if i == f.cur {
			return f.main.View()
		}
		return sanitize(f.main.Value())
	}
	for i := policyField(0); i < nPolicyFields; i++ {
		marker := "  "
		if i == f.cur {
			marker = "> "
		}
		out = append(out, clip(marker+sBold.Render(cell(policyLabels[i], lw))+" "+value(i), w))
		if note := m.policyResolveLine(i); note != "" {
			out = append(out, clip("  "+cell("", lw)+" "+sDim.Render(note), w))
		}
	}
	if f.err != "" {
		out = append(out, sBad.Render(clip("  ! "+f.err, w)))
	} else {
		out = append(out, "")
	}
	a, _ := m.selectedAgent()
	for _, s := range m.policySupport(a, f.preview) {
		out = append(out, sDim.Render(clip("  note: "+s, w)))
	}
	out = append(out, sDim.Render(clip("  Workers: "+workersHelp(workersChoices[f.workers]), w)))
	out = append(out, sDim.Render(clip("  left/right changes a choice. Enter on Scope saves. The policy is added to the session's context on every turn.", w)))
	for len(out) < rows {
		out = append(out, "")
	}
	return out[:rows]
}

// workersHelp says in plain words what a workers level lets a worker do.
func workersHelp(level string) string {
	switch level {
	case protocol.WorkersNoNetwork:
		return "no-network: writes only in its worktree, network off, no approval prompts (Codex only for now)"
	case protocol.WorkersReadOnly:
		return "read-only: no writes at all, network on (Codex only for now)"
	}
	return "sandboxed: writes only in its worktree, network on, no approval prompts (Codex only for now)"
}

// policyResolveLine says what a model field resolves to.
func (m Model) policyResolveLine(i policyField) string {
	f := m.pol
	if f.previewErr != "" && i == pfDelegate {
		return "cannot check: " + f.previewErr
	}
	v := f.preview
	if v == nil {
		return ""
	}
	note := func(resolved, errText string) string {
		switch {
		case errText != "":
			return "! " + errText
		case resolved != "":
			return "-> " + resolved
		case !v.ModelList:
			return "no model list for this harness: used as typed"
		}
		return ""
	}
	switch i {
	case pfCoder:
		if f.coder.Value() == "" || f.delegateValue() == "" || f.delegateValue() == protocol.DelegateOff {
			return ""
		}
		return note(v.CoderResolved, v.CoderError)
	case pfMain:
		if f.main.Value() == "" {
			return ""
		}
		return note(v.MainResolved, v.MainError)
	}
	return ""
}

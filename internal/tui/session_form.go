package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

// The "New session" form of the Sessions tab and the Graph card. It starts an
// owner session, not a worker: project, harness, model, a fixed project folder
// and an optional first message. See docs/design/start-sessions.md.

type sessionFormField int

const (
	sProject sessionFormField = iota
	sHarness
	sModel
	sFolder
	sFirstMessage
	sFields
)

var sessionFieldLabels = [sFields]string{"Project", "Harness", "Model", "Folder", "First message"}

// sessionValueCol is the column where a value starts: two markers and the
// label column. The lines under First message line up with it.
const sessionValueCol = 17

type sessionForm struct {
	in  [sFields]textinput.Model
	cur sessionFormField
	// What the form was filled from.
	nodeID   string // the machine of the selected row or node
	hostname string
	path     string
	// initialProject is the project value the form opened with; the note
	// shows the selected folder only while the field still names that project.
	initialProject string
	err            string
}

func newSessionForm() sessionForm {
	var f sessionForm
	for i := range f.in {
		t := textinput.New()
		t.Prompt = ""
		t.CharLimit = 4000
		t.Cursor.SetMode(cursor.CursorHide)
		f.in[i] = t
	}
	f.in[sFolder].SetValue("project folder")
	f.in[sFirstMessage].Focus()
	f.cur = sFirstMessage
	return f
}

func (f sessionForm) value(i sessionFormField) string { return strings.TrimSpace(f.in[i].Value()) }

func (f *sessionForm) focus(to sessionFormField) tea.Cmd {
	f.in[f.cur].Blur()
	f.cur = to
	f.in[f.cur].CursorEnd()
	return f.in[f.cur].Focus()
}

// nextSessionField is the next field that can take the focus. Folder is fixed
// text in this version, so it is shown but not visited.
func nextSessionField(cur sessionFormField) sessionFormField {
	switch cur {
	case sProject:
		return sHarness
	case sHarness:
		return sModel
	}
	return sFirstMessage
}

func prevSessionField(cur sessionFormField) sessionFormField {
	switch cur {
	case sFirstMessage:
		return sModel
	case sModel:
		return sHarness
	}
	return sProject
}

// localHost is the name this machine is shown by.
func (m Model) localHost() string {
	return m.nodeName(m.snap.NodeID, m.snap.Hostname)
}

// sessionProjectValue adds the machine when the same project runs on more than
// one machine, and for a row on another machine.
func (m Model) sessionProjectValue(project, nodeID, host string) string {
	if project == "" {
		return ""
	}
	if nodeID != "" && nodeID != m.snap.NodeID {
		if host == "" {
			host = nodeID
		}
		return project + "@" + host
	}
	for _, p := range m.projectRows() {
		if strings.EqualFold(p.Name, project) && p.NodeID != nodeID {
			if host == "" {
				host = m.localHost()
			}
			return project + "@" + host
		}
	}
	return project
}

// sessionHarnessFor picks the harness of the newest session in the project,
// then the first supported harness the machine has, then Claude.
func (m Model) sessionHarnessFor(project, nodeID string) string {
	newest, harness := int64(-1), ""
	for _, a := range m.snap.Agents {
		if protocol.IsOwner(a.Harness) || firstNonEmpty(a.Repo.Project, a.Profile.Project) != project {
			continue
		}
		if nodeID != "" && a.NodeID != nodeID {
			continue
		}
		if a.LastSeen > newest {
			newest, harness = a.LastSeen, a.Harness
		}
	}
	if harness != "" {
		return harness
	}
	for _, n := range m.snap.Nodes {
		if nodeID != "" && n.NodeID != nodeID {
			continue
		}
		for _, want := range []string{"claude", "codex", "opencode", "pi"} {
			for _, hi := range n.Harnesses {
				if hi.Name == want {
					return want
				}
			}
		}
	}
	return "claude"
}

// startSessionForm opens the form filled from the selected row.
func (m Model) startSessionForm() (Model, tea.Cmd) {
	a, ok := m.selectedAgent()
	if !ok {
		m.fail("Nothing is selected: pick a session first.")
		return m, nil
	}
	project := firstNonEmpty(a.Repo.Project, a.Profile.Project)
	if project == "" {
		m.fail("That session has no project: pick a project row or a node on the Graph.")
		return m, nil
	}
	host := m.nodeName(a.NodeID, a.Hostname)
	m.sform = newSessionForm()
	m.sform.nodeID, m.sform.hostname = a.NodeID, host
	m.sform.path = firstNonEmpty(a.Repo.Root, a.Cwd)
	m.sform.in[sProject].SetValue(m.sessionProjectValue(project, a.NodeID, host))
	m.sform.initialProject = m.sform.value(sProject)
	harness := a.Harness
	if harness == "" {
		harness = "claude"
	}
	m.sform.in[sHarness].SetValue(harness)
	m.mode = modeSessionForm
	m.notice = notice{}
	return m, textinput.Blink
}

// startSessionFormFromNode opens the same form filled from the Graph card.
func (m Model) startSessionFormFromNode() (Model, tea.Cmd) {
	node, ok := m.mapNode(m.mp.selected)
	if !ok || node.Kind == "architect" {
		m.fail("Pick a project to start a session in.")
		return m, nil
	}
	var hit *protocol.ProjectView
	for _, p := range m.projectRows() {
		if !strings.EqualFold(p.Name, node.Name) {
			continue
		}
		copy := p
		if p.NodeID == m.snap.NodeID {
			hit = &copy
			break
		}
		if hit == nil {
			hit = &copy
		}
	}
	nodeID, host, path := "", "", ""
	if hit != nil {
		nodeID, host, path = hit.NodeID, hit.Hostname, hit.Path
	}
	if nodeID == "" {
		for _, a := range m.snap.Agents {
			if firstNonEmpty(a.Repo.Project, a.Profile.Project) == node.Name {
				nodeID, host = a.NodeID, m.nodeName(a.NodeID, a.Hostname)
				path = firstNonEmpty(a.Repo.Root, a.Cwd)
				break
			}
		}
	}
	m.sform = newSessionForm()
	m.sform.nodeID, m.sform.hostname, m.sform.path = nodeID, host, path
	m.sform.in[sProject].SetValue(m.sessionProjectValue(node.Name, nodeID, host))
	m.sform.initialProject = m.sform.value(sProject)
	m.sform.in[sHarness].SetValue(m.sessionHarnessFor(node.Name, nodeID))
	m.mode = modeSessionForm
	m.notice = notice{}
	return m, textinput.Blink
}

// splitProjectMachine splits "name@machine". A path may contain an @ of its
// own, so a path stays whole.
func splitProjectMachine(value string) (project, machine string) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~") {
		return value, ""
	}
	if i := strings.LastIndex(value, "@"); i > 0 {
		return strings.TrimSpace(value[:i]), strings.TrimSpace(value[i+1:])
	}
	return value, ""
}

// sessionFormRefusal says why this version cannot start the form's session.
// Local Claude and OpenCode starts are built (build plan part 4).
func (m Model) sessionFormRefusal(f sessionForm) string {
	const only = "only Claude and OpenCode on this machine for now"
	if f.nodeID != "" && f.nodeID != m.snap.NodeID {
		return only
	}
	if h := strings.ToLower(f.value(sHarness)); h != "" && h != "claude" && h != "opencode" {
		return only
	}
	project, machine := splitProjectMachine(f.value(sProject))
	if machine != "" {
		local := m.localHost()
		if !strings.EqualFold(machine, local) && !strings.EqualFold(machine, m.snap.NodeID) {
			return only
		}
	}
	if _, _, node := m.resolveProject(project); node != nil && node.NodeID != m.snap.NodeID {
		return only
	}
	return ""
}

// sessionFormProject resolves the project field to the name the daemon knows.
func (m Model) sessionFormProject(f sessionForm) string {
	project, _ := splitProjectMachine(f.value(sProject))
	return project
}

func (m Model) sessionFormKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.sform
	switch k.Type {
	case tea.KeyEsc:
		m.mode = modeNormal
		m.say("Start cancelled.")
		return m, nil
	case tea.KeyTab:
		if f.cur < sFirstMessage {
			return m, f.focus(nextSessionField(f.cur))
		}
		return m, nil
	case tea.KeyShiftTab, tea.KeyUp:
		if f.cur > sProject {
			return m, f.focus(prevSessionField(f.cur))
		}
		return m, nil
	case tea.KeyDown:
		if f.cur < sFirstMessage {
			return m, f.focus(nextSessionField(f.cur))
		}
		return m, nil
	case tea.KeyEnter:
		if f.cur < sFirstMessage {
			return m, f.focus(nextSessionField(f.cur))
		}
		return m.submitSessionForm()
	}
	f.err = ""
	var cmd tea.Cmd
	f.in[f.cur], cmd = f.in[f.cur].Update(k)
	return m, cmd
}

func (m Model) submitSessionForm() (tea.Model, tea.Cmd) {
	f := &m.sform
	project := f.value(sProject)
	if project == "" {
		f.err = "Name the project."
		return m, f.focus(sProject)
	}
	if why := m.sessionFormRefusal(*f); why != "" {
		f.err = strings.ToUpper(why[:1]) + why[1:] + "."
		if f.nodeID != "" && f.nodeID != m.snap.NodeID {
			return m, f.focus(sProject)
		}
		return m, f.focus(sHarness)
	}
	harness := strings.ToLower(f.value(sHarness))
	if harness == "" {
		harness = "claude"
	}
	req := protocol.SessionStartRequest{Project: m.sessionFormProject(*f), Harness: harness,
		Model: f.value(sModel), FirstMessage: f.value(sFirstMessage)}
	m.mode = modeNormal
	m.say("Starting " + sanitize(m.sessionFormProject(*f)) + " on " + sanitize(m.localHost()) + "...")
	return m, m.sessionStartCmd(req)
}

// sessionStartedMsg is the end of a session_start, after the session
// registered or the start failed.
type sessionStartedMsg struct {
	req protocol.SessionStartRequest
	res protocol.StartedSession
	err error
}

func (m Model) sessionStartCmd(req protocol.SessionStartRequest) tea.Cmd {
	src := m.src
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), operator.StartTimeout)
		defer cancel()
		res, err := src.SessionStart(ctx, req)
		if err != nil {
			return sessionStartedMsg{req: req, err: err}
		}
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		for res.Status == "starting" {
			select {
			case <-ctx.Done():
				return sessionStartedMsg{req: req, err: ctx.Err()}
			case <-ticker.C:
			}
			res, err = src.SessionStartGet(ctx, res.ID)
			if err != nil {
				return sessionStartedMsg{req: req, err: err}
			}
		}
		return sessionStartedMsg{req: req, res: res}
	}
}

func (m Model) onSessionStarted(r sessionStartedMsg) (Model, tea.Cmd) {
	refresh := func() tea.Cmd {
		if m.fetching {
			return nil
		}
		m.fetching = true
		return m.fetchCmd()
	}
	if r.err != nil {
		m.fail("Session not started: " + explain(r.err))
		return m, refresh()
	}
	if r.res.Status == "failed" {
		m.fail(firstNonEmpty(sanitize(r.res.Error), "The session did not start."))
		return m, refresh()
	}
	if r.res.Status != "started" {
		m.fail("The start did not finish.")
		return m, refresh()
	}
	host := m.nodeName(r.res.NodeID, m.sform.hostname)
	project := sanitize(firstNonEmpty(r.res.Project, m.sessionFormProject(m.sform)))
	harness := firstNonEmpty(r.res.Harness, r.req.Harness)
	if harness == "" {
		harness = "claude"
	}
	if r.req.FirstMessage != "" {
		m.say(fmt.Sprintf("started %s (%s) on %s: first message sent. Enter opens it.", project, harness, sanitize(host)))
		return m, refresh()
	}
	if r.res.SessionID == "" {
		m.fail("The session started but the daemon did not register it yet: refresh and press enter on its row.")
		return m, refresh()
	}
	name := project + " (" + sanitize(host) + ")"
	m.say("Starting " + name + "...")
	return m, m.enterCmd(r.res.SessionID, name)
}

// sessionFieldNote is the right-hand note of a field.
func (m Model) sessionFieldNote(i sessionFormField) string {
	f := m.sform
	switch i {
	case sProject:
		if f.value(sProject) == "" {
			return ""
		}
		host := f.hostname
		if _, machine, node := m.resolveProject(f.value(sProject)); node != nil {
			host = machine
		}
		if host == "" {
			host = m.localHost()
		}
		note := "on " + host
		if f.path != "" && f.value(sProject) == f.initialProject && f.nodeID == m.snap.NodeID {
			note += ", " + sanitize(f.path)
		}
		return note
	case sHarness:
		if f.value(sHarness) == "" {
			return "automatic choice: claude"
		}
	case sModel:
		if f.value(sModel) == "" {
			return ""
		}
	case sFolder:
		return "no worktree yet"
	}
	return ""
}

// sessionFormView draws the form in the place of the session list, like the
// worker form. rows is the room it has, including its heading.
func (m Model) sessionFormView(w, rows int) []string {
	f := m.sform
	out := []string{section("New session", w)}
	const lw = 15
	for i := sessionFormField(0); i < sFields; i++ {
		marker := "  "
		if i == f.cur {
			marker = "> "
		}
		var val string
		switch {
		case i == sModel && f.value(sModel) == "":
			val = sDim.Render("default")
		case i == f.cur && i != sFolder:
			val = f.in[i].View()
		default:
			val = sanitize(f.in[i].Value())
		}
		line := marker + sBold.Render(cell(sessionFieldLabels[i], lw)) + val
		if note := m.sessionFieldNote(i); note != "" {
			if room := w - ansi.StringWidth(line) - 3; room > 6 {
				line += "   " + sDim.Render(clip(note, room))
			}
		}
		out = append(out, clip(line, w))
	}
	why := m.sessionFormRefusal(f)
	indent := strings.Repeat(" ", sessionValueCol)
	if strings.TrimSpace(f.in[sFirstMessage].Value()) == "" {
		out = append(out, sDim.Render(clip(indent+"empty: start it and enter it", w)))
		out = append(out, sDim.Render(clip(indent+"text: start it in the background and send this", w)))
	} else {
		out = append(out, sDim.Render(clip(indent+"enter: start in the background and send this", w)))
	}
	switch {
	case why != "":
		out = append(out, sBad.Render(clip("  ! "+why+".", w)))
	case f.err != "":
		out = append(out, sBad.Render(clip("  ! "+f.err, w)))
	}
	for len(out) < rows {
		out = append(out, "")
	}
	if len(out) > rows {
		out = out[:rows]
	}
	return out
}

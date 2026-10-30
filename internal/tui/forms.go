package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

// The actions of the screen (decision 0008): send a message as the owner,
// start a worker, stop a worker. Each one calls only the operator interface,
// shows a prompt in the help row or a form in the middle of the screen, and
// ends with one result line, green or red, under the title.

type inputMode int

const (
	modeNormal  inputMode = iota
	modeCompose           // typing a message to the selected session
	modeForm              // the start-a-worker form
	modeConfirm           // asking y/n before stopping a worker
	modeTrust             // asking y/n before trusting a session with peer requests
	modePolicy            // the delegation policy form (policy.go)
	modeRename            // typing a display name for the selected session's machine
)

// noticeTTL is how long a result line stays when no key clears it.
const noticeTTL = 30 * time.Second

// notice is the result of the last action.
type notice struct {
	text string
	err  bool
	at   time.Time
}

// actionMsg is the end of an action that ran in the background.
type actionMsg struct {
	text string
	err  error
}

// ---- notices ----

func (m *Model) say(text string) { m.notice = notice{text: text, at: m.now()} }
func (m *Model) fail(text string) {
	m.notice = notice{text: text, err: true, at: m.now()}
}

// activeNotice returns the notice to show, or nil when there is none or it is old.
func (m Model) activeNotice() *notice {
	if m.notice.text == "" || m.now().Sub(m.notice.at) > noticeTTL {
		return nil
	}
	return &m.notice
}

// explain turns an error of an action into one plain line.
func explain(err error) string {
	switch ClassifyError(err) {
	case ProblemDaemon:
		return "The daemon cannot be reached."
	case ProblemGateway:
		return "The gateway cannot be reached."
	}
	return strings.TrimSpace(sanitize(err.Error()))
}

// ---- send a message ----

// startCompose opens the message prompt for the selected session.
func (m Model) startCompose() (Model, tea.Cmd) {
	a, ok := m.selectedAgent()
	switch {
	case !ok:
		m.fail("Nothing is selected: pick a session first.")
		return m, nil
	case !a.Online:
		m.fail("That session is offline: a message would wait until it comes back.")
		return m, nil
	}
	m.mode = modeCompose
	m.compose = textinput.New()
	m.compose.Prompt = "to " + sanitize(dashIfEmpty(a.Profile.Name)) + "> "
	m.compose.CharLimit = 4000
	m.compose.Cursor.SetMode(cursor.CursorHide)
	m.composeTo = a.SessionID
	m.composeName = sanitize(dashIfEmpty(a.Profile.Name)) + " (" + sanitize(a.Hostname) + ")"
	m.notice = notice{}
	return m, m.compose.Focus()
}

func (m Model) composeKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEsc:
		m.mode = modeNormal
		m.say("Message cancelled.")
		return m, nil
	case tea.KeyEnter:
		body := strings.TrimSpace(m.compose.Value())
		if body == "" {
			m.fail("The message is empty. Type something, or press esc to cancel.")
			return m, nil
		}
		m.mode = modeNormal
		m.say("Sending...")
		return m, m.sendCmd(m.composeTo, m.composeName, body)
	}
	var cmd tea.Cmd
	m.compose, cmd = m.compose.Update(k)
	return m, cmd
}

func (m Model) sendCmd(to, name, body string) tea.Cmd {
	src := m.src
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := src.SendAsOwner(ctx, to, body)
		if err != nil {
			return actionMsg{err: fmt.Errorf("Not sent to %s: %s", name, explain(err))}
		}
		if res.Queued {
			return actionMsg{text: "Queued for " + name + ": the gateway is not reachable, it is sent when the link is back."}
		}
		id := ""
		if res.Message != nil {
			id = " (" + res.Message.ID + ")"
		}
		return actionMsg{text: "Sent to " + name + id + " as you. The reply will show in the messages."}
	}
}

// ---- stop a worker ----

func (m Model) startConfirm() (Model, tea.Cmd) {
	a, ok := m.selectedAgent()
	switch {
	case !ok:
		m.fail("Nothing is selected: pick a worker first.")
	case a.Worker == nil:
		m.fail("That session is not a worker. x stops workers only.")
	default:
		m.mode = modeConfirm
		m.confirmID = a.SessionID
		m.confirmName = sanitize(dashIfEmpty(a.Profile.Name)) + " (" + sanitize(a.Hostname) + ")"
		m.notice = notice{}
	}
	return m, nil
}

func (m Model) confirmKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "y", "Y":
		m.mode = modeNormal
		m.say("Stopping...")
		return m, m.stopCmd(m.confirmID, m.confirmName)
	case "n", "N", "esc":
		m.mode = modeNormal
		m.say("Not stopped.")
	}
	return m, nil
}

func (m Model) stopCmd(id, name string) tea.Cmd {
	src := m.src
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := src.StopWorker(ctx, id); err != nil {
			return actionMsg{err: fmt.Errorf("Not stopped: %s", explain(err))}
		}
		return actionMsg{text: "Stopped worker " + name + "."}
	}
}

// ---- machine name ----

// startRename opens the name prompt for the machine of the selected session
// (decision 0015). The name is kept on the gateway, so every machine sees it.
func (m Model) startRename() (Model, tea.Cmd) {
	a, ok := m.selectedAgent()
	if !ok {
		m.fail("Nothing is selected: pick a session on the machine first.")
		return m, nil
	}
	shown, set := a.Hostname, false
	for _, n := range m.snap.Nodes {
		if n.NodeID == a.NodeID {
			shown, set = n.Hostname, n.DisplayName != ""
		}
	}
	m.mode = modeRename
	m.rename = textinput.New()
	m.rename.Prompt = "name for " + sanitize(shown) + "> "
	m.rename.CharLimit = protocol.MaxNodeName
	m.rename.Cursor.SetMode(cursor.CursorHide)
	m.rename.SetValue(sanitize(shown))
	m.rename.CursorEnd()
	m.renameNode, m.renameShown, m.renameSet = a.NodeID, shown, set
	m.notice = notice{}
	return m, m.rename.Focus()
}

func (m Model) renameKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEsc:
		m.mode = modeNormal
		m.say("Machine name unchanged.")
		return m, nil
	case tea.KeyEnter:
		name, err := protocol.CleanNodeName(m.rename.Value())
		if err != nil {
			m.fail(err.Error() + ".")
			return m, nil
		}
		if name == m.renameShown && !m.renameSet || name == "" && !m.renameSet {
			m.mode = modeNormal
			m.say("Machine name unchanged.")
			return m, nil
		}
		m.mode = modeNormal
		m.say("Saving the machine name...")
		return m, m.renameCmd(m.renameNode, name)
	}
	var cmd tea.Cmd
	m.rename, cmd = m.rename.Update(k)
	return m, cmd
}

func (m Model) renameCmd(id, name string) tea.Cmd {
	src := m.src
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := src.RenameNode(ctx, id, name)
		if err != nil {
			return actionMsg{err: fmt.Errorf("Machine name not changed: %s", explain(err))}
		}
		if name == "" {
			return actionMsg{text: "Machine name cleared: it is " + sanitize(res.Hostname) + " again, on every machine."}
		}
		return actionMsg{text: "Machine named " + sanitize(res.Hostname) + ". Every machine shows it."}
	}
}

// ---- peer trust ----

// startTrust toggles peer trust on the selected session (decision 0012). Turning
// it off is immediate; turning it on asks first, because the session then acts
// on requests from other agents without asking its user.
func (m Model) startTrust() (Model, tea.Cmd) {
	a, ok := m.selectedAgent()
	switch {
	case !ok:
		m.fail("Nothing is selected: pick a session first.")
		return m, nil
	case protocol.IsOwner(a.Harness):
		m.fail("That is you, not an agent session.")
		return m, nil
	}
	name := sanitize(dashIfEmpty(a.Profile.Name)) + " (" + sanitize(a.Hostname) + ")"
	if a.PeerTrust {
		m.say("Turning peer trust off...")
		return m, m.trustCmd(a.SessionID, name, false)
	}
	m.mode = modeTrust
	m.confirmID, m.confirmName = a.SessionID, name
	m.notice = notice{}
	return m, nil
}

func (m Model) trustKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "y", "Y":
		m.mode = modeNormal
		m.say("Turning peer trust on...")
		return m, m.trustCmd(m.confirmID, m.confirmName, true)
	case "n", "N", "esc":
		m.mode = modeNormal
		m.say("Peer trust unchanged.")
	}
	return m, nil
}

func (m Model) trustCmd(id, name string, on bool) tea.Cmd {
	src := m.src
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := src.SetPeerTrust(ctx, id, on); err != nil {
			return actionMsg{err: fmt.Errorf("Peer trust not changed: %s", explain(err))}
		}
		if on {
			return actionMsg{text: name + " now acts on requests from your other agents without asking. Press t on it to turn that off."}
		}
		return actionMsg{text: name + " asks you before acting on requests from other agents."}
	}
}

// ---- start a worker ----

type formField int

const (
	fProject formField = iota
	fHarness
	fModel
	fTask
	nFields
)

var fieldLabels = [nFields]string{"Project", "Harness", "Model", "Task"}

// workerForm is the start-a-worker form. Project and model complete from the
// project list and the models that machine reports.
type workerForm struct {
	in  [nFields]textinput.Model
	cur formField
	err string
}

// candidate is one completion: the text it puts in the field and a note.
type candidate struct {
	value string
	note  string
}

func newWorkerForm() workerForm {
	var f workerForm
	for i := range f.in {
		t := textinput.New()
		t.Prompt = ""
		t.CharLimit = 4000
		t.Cursor.SetMode(cursor.CursorHide)
		f.in[i] = t
	}
	f.in[fHarness].SetValue(protocol.WorkerHarnesses[0])
	f.in[fProject].Focus()
	return f
}

func (f *workerForm) focus(to formField) tea.Cmd {
	f.in[f.cur].Blur()
	f.cur = to
	f.in[f.cur].CursorEnd()
	return f.in[f.cur].Focus()
}

func (f workerForm) value(i formField) string { return strings.TrimSpace(f.in[i].Value()) }

// startForm opens the form, with the project of the selected session filled in.
func (m Model) startForm() (Model, tea.Cmd) {
	m.form = newWorkerForm()
	if a, ok := m.selectedAgent(); ok && !protocol.IsOwner(a.Harness) {
		if p := firstNonEmpty(a.Repo.Project, a.Profile.Project); p != "" && len(m.projectCandidates(p, true)) > 0 {
			m.form.in[fProject].SetValue(p)
			m.form.cur = fTask
			m.form.in[fProject].Blur()
			m.form.in[fTask].Focus()
		}
	}
	m.mode = modeForm
	m.notice = notice{}
	return m, textinput.Blink
}

// projectRows lists the known projects once per name and machine.
func (m Model) projectRows() []protocol.ProjectView {
	seen := map[string]bool{}
	var out []protocol.ProjectView
	for _, p := range m.snap.Projects {
		if p.Hostname == "" { // the machine list has it too
			for _, n := range m.snap.Nodes {
				if n.NodeID == p.NodeID {
					p.Hostname = n.Hostname
				}
			}
		}
		k := strings.ToLower(p.Name) + "\x00" + p.NodeID
		if !seen[k] {
			seen[k] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name); a != b {
			return a < b
		}
		return out[i].Hostname < out[j].Hostname
	})
	return out
}

// projectCandidates are the project names that match text. A name that is on
// more than one machine is offered as name@machine. exact keeps only names
// equal to text.
func (m Model) projectCandidates(text string, exact bool) []candidate {
	rows := m.projectRows()
	machines := map[string]int{}
	for _, p := range rows {
		machines[strings.ToLower(p.Name)]++
	}
	text = strings.ToLower(strings.TrimSpace(text))
	var out []candidate
	for _, p := range rows {
		name := p.Name
		value := name
		if machines[strings.ToLower(name)] > 1 {
			value = name + "@" + p.Hostname
		}
		hay := strings.ToLower(value)
		if exact && strings.ToLower(name) != text && hay != text {
			continue
		}
		if !exact && !strings.Contains(hay, text) {
			continue
		}
		src := p.Source
		if src == "" {
			src = protocol.SourceSeen
		}
		out = append(out, candidate{value: value, note: src + " on " + p.Hostname})
	}
	return out
}

// resolveProject splits "name" or "name@machine" and finds the machine that
// holds the project, when there is exactly one.
func (m Model) resolveProject(text string) (name, machine string, node *protocol.NodeView) {
	name = strings.TrimSpace(text)
	if i := strings.LastIndex(name, "@"); i > 0 {
		machine, name = strings.TrimSpace(name[i+1:]), strings.TrimSpace(name[:i])
	}
	var hit *protocol.ProjectView
	n := 0
	for _, p := range m.projectRows() {
		if strings.EqualFold(p.Name, name) && (machine == "" || strings.EqualFold(p.Hostname, machine) || p.NodeID == machine) {
			hit = &p
			n++
		}
	}
	if n != 1 {
		return name, machine, nil
	}
	for i := range m.snap.Nodes {
		if m.snap.Nodes[i].NodeID == hit.NodeID {
			return name, hit.Hostname, &m.snap.Nodes[i]
		}
	}
	return name, hit.Hostname, nil
}

// harnessCandidates are the worker harnesses installed on the project's machine
// (all supported ones when the machine is not known).
func (m Model) harnessCandidates(text string) []candidate {
	_, _, node := m.resolveProject(m.form.value(fProject))
	text = strings.ToLower(strings.TrimSpace(text))
	var out []candidate
	for _, h := range protocol.WorkerHarnesses {
		note := ""
		if node != nil && node.Harnesses != nil {
			found := false
			for _, hi := range node.Harnesses {
				found = found || hi.Name == h
			}
			if !found {
				continue
			}
			note = "installed on " + node.Hostname
		}
		if strings.Contains(h, text) {
			out = append(out, candidate{value: h, note: note})
		}
	}
	return out
}

// modelCandidates are the models the project's machine reports for the harness.
func (m Model) modelCandidates(text string) []candidate {
	_, _, node := m.resolveProject(m.form.value(fProject))
	if node == nil {
		return nil
	}
	harness := m.form.value(fHarness)
	text = strings.ToLower(strings.TrimSpace(text))
	var pre, rest []candidate
	for _, hi := range node.Harnesses {
		if hi.Name != harness {
			continue
		}
		for _, mod := range hi.Models {
			l := strings.ToLower(mod)
			switch {
			case strings.HasPrefix(l, text):
				pre = append(pre, candidate{value: mod})
			case strings.Contains(l, text):
				rest = append(rest, candidate{value: mod})
			}
		}
	}
	return append(pre, rest...)
}

// candidates are the completions of the field that has the focus.
func (m Model) candidates() []candidate {
	text := m.form.in[m.form.cur].Value()
	switch m.form.cur {
	case fProject:
		return m.projectCandidates(text, false)
	case fHarness:
		return m.harnessCandidates(text)
	case fModel:
		return m.modelCandidates(text)
	}
	return nil
}

// complete puts the longest common start of the candidates in the field. It
// reports whether the field changed.
func (m *Model) complete() bool {
	cands := m.candidates()
	if len(cands) == 0 {
		return false
	}
	prefix := cands[0].value
	for _, c := range cands[1:] {
		for !strings.HasPrefix(strings.ToLower(c.value), strings.ToLower(prefix)) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	cur := m.form.in[m.form.cur].Value()
	if len(cands) == 1 {
		prefix = cands[0].value
	}
	if len(prefix) <= len(cur) && !(len(cands) == 1 && prefix != cur) {
		return false
	}
	m.form.in[m.form.cur].SetValue(prefix)
	m.form.in[m.form.cur].CursorEnd()
	return true
}

func (m Model) formKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.form
	switch k.Type {
	case tea.KeyEsc:
		m.mode = modeNormal
		m.say("Start cancelled.")
		return m, nil
	case tea.KeyTab:
		if !m.complete() && f.cur < fTask {
			return m, f.focus(f.cur + 1)
		}
		return m, nil
	case tea.KeyShiftTab, tea.KeyUp:
		if f.cur > 0 {
			return m, f.focus(f.cur - 1)
		}
		return m, nil
	case tea.KeyDown:
		if f.cur < fTask {
			return m, f.focus(f.cur + 1)
		}
		return m, nil
	case tea.KeyEnter:
		if f.cur < fTask {
			return m, f.focus(f.cur + 1)
		}
		return m.submitForm()
	}
	f.err = ""
	var cmd tea.Cmd
	f.in[f.cur], cmd = f.in[f.cur].Update(k)
	return m, cmd
}

func (m Model) submitForm() (tea.Model, tea.Cmd) {
	f := &m.form
	name, machine, _ := m.resolveProject(f.value(fProject))
	if !strings.Contains(f.value(fProject), "@") {
		machine = "" // only a machine the person named; the gateway finds the rest
	}
	switch {
	case name == "":
		f.err = "Name the project."
		return m, f.focus(fProject)
	case f.value(fHarness) == "":
		f.err = "Name the harness."
		return m, f.focus(fHarness)
	case f.value(fTask) == "":
		f.err = "Write the task."
		return m, f.focus(fTask)
	}
	req := protocol.WorkerStartRequest{Project: name, Machine: machine, Harness: f.value(fHarness),
		Model: f.value(fModel), Task: f.value(fTask)}
	m.mode = modeNormal
	m.say("Starting a worker in " + sanitize(name) + "... (this can take half a minute)")
	return m, m.startCmd(req)
}

func (m Model) startCmd(req protocol.WorkerStartRequest) tea.Cmd {
	src := m.src
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), operator.StartTimeout)
		defer cancel()
		res, err := src.StartWorker(ctx, req)
		if err != nil {
			return actionMsg{err: fmt.Errorf("Worker not started: %s", explain(err))}
		}
		text := fmt.Sprintf("Started worker %s (%s, %s on %s). Its reply will show in the messages.",
			res.SessionID, res.Harness, sanitize(res.Project), sanitize(res.Hostname))
		if res.Queued {
			text = fmt.Sprintf("Started worker %s, but the task waits: the gateway link dropped. It is sent when the link is back.", res.SessionID)
		}
		return actionMsg{text: text}
	}
}

// onAction ends an action: the result line, and an immediate refresh so the
// new worker or message shows.
func (m Model) onAction(r actionMsg) (Model, tea.Cmd) {
	if r.err != nil {
		m.fail(r.err.Error())
	} else {
		m.say(r.text)
	}
	if !m.fetching {
		m.fetching = true
		return m, m.fetchCmd()
	}
	return m, nil
}

// firstNames returns up to n distinct project names.
func firstNames(rows []protocol.ProjectView, n int) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range rows {
		if !seen[p.Name] && len(out) < n {
			seen[p.Name] = true
			out = append(out, p.Name)
		}
	}
	return out
}

package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
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
	sBranch
	sFirstMessage
	sFields
)

var sessionFieldLabels = [sFields]string{"Project", "Harness", "Model", "Folder", "Branch", "First message"}

// sessionValueCol is the column where a value starts: two markers and the
// label column. The lines under First message line up with it.
const sessionValueCol = 17

type sessionForm struct {
	in  [sFields]textinput.Model
	cur sessionFormField
	// folder is protocol.FolderProject or protocol.FolderWorktree. Branch is
	// shown and sent only for a worktree.
	folder string
	// busy is the one-line reason the folder is not offered first, empty when
	// no live session works in the project folder.
	busy string
	// folderWhy says why this project or path has no worktree choice, such as
	// a new folder or a folder that is not a git repository.
	folderWhy string
	// base is the branch the new worktree starts from, for the branch note.
	base string
	// branchAuto is true while the branch field still holds the automatic
	// name, so a project change may replace it.
	branchAuto bool
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
	f.folder = protocol.FolderProject
	f.in[sFirstMessage].Focus()
	f.cur = sFirstMessage
	return f
}

func (f sessionForm) value(i sessionFormField) string { return strings.TrimSpace(f.in[i].Value()) }

// worktree tells whether the form starts in a new git worktree.
func (f sessionForm) worktree() bool { return f.folder == protocol.FolderWorktree }

// folderLabel is the switch value shown on the Folder line.
func (f sessionForm) folderLabel() string {
	if f.worktree() {
		return "new worktree"
	}
	return "project folder"
}

func (f *sessionForm) focus(to sessionFormField) tea.Cmd {
	f.in[f.cur].Blur()
	f.cur = to
	f.in[f.cur].CursorEnd()
	return f.in[f.cur].Focus()
}

// nextSessionField is the next field that can take the focus. Branch is
// skipped unless the form starts in a worktree.
func (f sessionForm) nextSessionField(cur sessionFormField) sessionFormField {
	switch cur {
	case sProject:
		return sHarness
	case sHarness:
		return sModel
	case sModel:
		return sFolder
	case sFolder:
		if f.worktree() {
			return sBranch
		}
	}
	return sFirstMessage
}

func (f sessionForm) prevSessionField(cur sessionFormField) sessionFormField {
	switch cur {
	case sFirstMessage:
		if f.worktree() {
			return sBranch
		}
		return sFolder
	case sBranch:
		return sFolder
	case sFolder:
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

// sessionDefaultsFor picks the most-used harness and model on the project's
// machine. Ties go to the most recently seen session.
func (m Model) sessionDefaultsFor(project, nodeID string) (string, string) {
	type count struct {
		n      int
		newest int64
	}
	harnesses := map[string]count{}
	for _, a := range m.snap.Agents {
		if protocol.IsOwner(a.Harness) || a.Harness == "" || !strings.EqualFold(firstNonEmpty(a.Repo.Project, a.Profile.Project), project) || (nodeID != "" && a.NodeID != nodeID) {
			continue
		}
		c := harnesses[a.Harness]
		c.n++
		if a.LastSeen > c.newest {
			c.newest = a.LastSeen
		}
		harnesses[a.Harness] = c
	}
	choose := func(counts map[string]count) string {
		best := ""
		var top count
		for name, c := range counts {
			if c.n > top.n || (c.n == top.n && (c.newest > top.newest || (c.newest == top.newest && name < best))) {
				best, top = name, c
			}
		}
		return best
	}
	harness := choose(harnesses)
	if harness != "" {
		models := map[string]count{}
		for _, a := range m.snap.Agents {
			if a.Harness != harness || a.Model == "" || !strings.EqualFold(firstNonEmpty(a.Repo.Project, a.Profile.Project), project) || (nodeID != "" && a.NodeID != nodeID) {
				continue
			}
			c := models[a.Model]
			c.n++
			if a.LastSeen > c.newest {
				c.newest = a.LastSeen
			}
			models[a.Model] = c
		}
		return harness, choose(models)
	}
	for _, n := range m.snap.Nodes {
		if nodeID != "" && n.NodeID != nodeID {
			continue
		}
		for _, want := range []string{"claude", "codex", "opencode", "pi"} {
			for _, hi := range n.Harnesses {
				if hi.Name == want {
					return want, ""
				}
			}
		}
	}
	return "claude", ""
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
	m.resetSessionFolder()
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
	harness, model := m.sessionDefaultsFor(node.Name, nodeID)
	m.sform.in[sHarness].SetValue(harness)
	m.sform.in[sModel].SetValue(model)
	m.resetSessionFolder()
	m.mode = modeSessionForm
	m.notice = notice{}
	return m, textinput.Blink
}

// sessionProjectFolder is the main working tree of the form's project on its
// machine, or "" when the project is not known yet.
func (m Model) sessionProjectFolder(project, nodeID string) string {
	for _, p := range m.projectRows() {
		if strings.EqualFold(p.Name, project) && (nodeID == "" || p.NodeID == nodeID) {
			return p.Path
		}
	}
	return ""
}

// sessionProjectBase is the branch the project folder has now, for the "from
// <branch> (HEAD)" note. It is not the branch of a session in a linked
// worktree: the new worktree starts from the project's main tree.
func (m Model) sessionProjectBase(project, nodeID, folder string) string {
	for _, p := range m.projectRows() {
		if strings.EqualFold(p.Name, project) && (nodeID == "" || p.NodeID == nodeID) && p.DefaultBranch != "" {
			return p.DefaultBranch
		}
	}
	for _, a := range m.snap.Agents {
		if !strings.EqualFold(firstNonEmpty(a.Repo.Project, a.Profile.Project), project) {
			continue
		}
		if nodeID != "" && a.NodeID != nodeID {
			continue
		}
		if folder != "" && firstNonEmpty(a.Repo.Worktree, a.Cwd) != folder {
			continue
		}
		if a.Repo.Branch != "" {
			return a.Repo.Branch
		}
	}
	return ""
}

// sessionFolderBusy tells whether a live session is working in the project
// folder. The reason is the project name: "agent-net is busy in the project
// folder" (docs/design/start-sessions.md).
func (m Model) sessionFolderBusy(project, nodeID, folder string) bool {
	if folder == "" {
		return false
	}
	for _, a := range m.snap.Agents {
		if !a.Online || a.Status != "busy" {
			continue
		}
		if !strings.EqualFold(firstNonEmpty(a.Repo.Project, a.Profile.Project), project) {
			continue
		}
		if nodeID != "" && a.NodeID != nodeID {
			continue
		}
		if firstNonEmpty(a.Repo.Worktree, a.Cwd) == folder {
			return true
		}
	}
	return false
}

// ownerWorktreeBranch is the lowest free agent-net/<project>-<n>. Numbers
// already used by the project's sessions and its known worktrees are taken.
func (m Model) ownerWorktreeBranch(project, nodeID string) string {
	prefix := protocol.OwnerWorktreeBranchPrefix(project)
	taken := map[int]bool{}
	take := func(branch string) {
		if !strings.HasPrefix(branch, prefix) {
			return
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(branch, prefix)); err == nil && n > 0 {
			taken[n] = true
		}
	}
	projectPart := protocol.OwnerWorktreeProject(project) + "-"
	for _, a := range m.snap.Agents {
		if !strings.EqualFold(firstNonEmpty(a.Repo.Project, a.Profile.Project), project) {
			continue
		}
		if nodeID != "" && a.NodeID != nodeID {
			continue
		}
		take(a.Repo.Branch)
	}
	for _, p := range m.projectRows() {
		if !strings.EqualFold(p.Name, project) || (nodeID != "" && p.NodeID != nodeID) {
			continue
		}
		for _, wt := range p.Worktrees {
			base := strings.TrimRight(wt, "/")
			if i := strings.LastIndex(base, "/"); i >= 0 {
				base = base[i+1:]
			}
			if strings.HasPrefix(base, projectPart) {
				if n, err := strconv.Atoi(strings.TrimPrefix(base, projectPart)); err == nil && n > 0 {
					taken[n] = true
				}
			}
		}
	}
	n := 1
	for taken[n] {
		n++
	}
	return protocol.OwnerWorktreeBranch(project, n)
}

// resetSessionFolder applies the folder default of the form's project: the
// project folder, or a new worktree when a live session is busy there.
func (m *Model) resetSessionFolder() {
	f := &m.sform
	project, _ := splitProjectMachine(f.value(sProject))
	nodeID := f.nodeID
	if node := m.sessionFormNode(*f); node != nil {
		nodeID = node.NodeID
	}
	folder := m.sessionProjectFolder(project, nodeID)
	f.base = m.sessionProjectBase(project, nodeID, folder)
	f.busy = ""
	f.folder = protocol.FolderProject
	f.folderWhy = ""
	st := m.sessionPathState(*f)
	if st.isPath {
		f.path = st.candidate
		switch {
		case st.why != "":
		case !st.exists:
			f.folderWhy = "a new folder has one tree"
		case !st.isGit:
			f.folderWhy = "not a git repository: no worktrees"
		}
	} else if m.sessionFolderBusy(project, nodeID, folder) {
		f.busy = project + " is busy in the project folder"
		f.folder = protocol.FolderWorktree
	}
	f.branchAuto = true
	f.in[sBranch].SetValue(m.ownerWorktreeBranch(project, nodeID))
}

// setSessionFolder switches between the project folder and a new worktree,
// keeping the automatic branch name up to date.
func (m *Model) setSessionFolder(folder string) {
	f := &m.sform
	if folder != protocol.FolderWorktree {
		folder = protocol.FolderProject
	}
	if folder == protocol.FolderWorktree && f.folderWhy != "" {
		return
	}
	if f.folder == folder {
		return
	}
	f.folder = folder
	if folder == protocol.FolderWorktree && (f.branchAuto || f.value(sBranch) == "") {
		project, _ := splitProjectMachine(f.value(sProject))
		nodeID := f.nodeID
		if node := m.sessionFormNode(*f); node != nil {
			nodeID = node.NodeID
		}
		f.in[sBranch].SetValue(m.ownerWorktreeBranch(project, nodeID))
		f.branchAuto = true
	}
}

// splitProjectMachine splits "name@machine" and "path@machine". A path may
// contain an @ of its own, so an @ only starts a machine when what follows it
// has no folder separator.
func splitProjectMachine(value string) (project, machine string) {
	value = strings.TrimSpace(value)
	if i := strings.LastIndex(value, "@"); i > 0 {
		suffix := strings.TrimSpace(value[i+1:])
		if suffix != "" && !strings.ContainsAny(suffix, `/\`) && !strings.HasPrefix(suffix, "~") {
			return strings.TrimSpace(value[:i]), suffix
		}
	}
	return value, ""
}

// nodeByIDOrHost finds a machine by its gateway ID or its host name.
func (m Model) nodeByIDOrHost(s string) *protocol.NodeView {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for i := range m.snap.Nodes {
		if m.snap.Nodes[i].NodeID == s || strings.EqualFold(m.snap.Nodes[i].Hostname, s) {
			return &m.snap.Nodes[i]
		}
	}
	return nil
}

// sessionFormNode is the machine the form's project is on, or nil when it is
// not known yet. The typed value wins; the selected row fills in the rest.
func (m Model) sessionFormNode(f sessionForm) *protocol.NodeView {
	if _, _, node := m.resolveProject(f.value(sProject)); node != nil {
		return node
	}
	_, machine := splitProjectMachine(f.value(sProject))
	if node := m.nodeByIDOrHost(machine); node != nil {
		return node
	}
	return m.nodeByIDOrHost(f.nodeID)
}

// sessionRemoteHost is the other machine the form starts on, or "" for this
// machine.
func (m Model) sessionRemoteHost(f sessionForm) string {
	node := m.sessionFormNode(f)
	if node == nil || node.NodeID == m.snap.NodeID {
		return ""
	}
	return firstNonEmpty(node.Hostname, node.NodeID)
}

// sessionFormRefusal says why this version cannot start the form's session.
func (m Model) sessionFormRefusal(f sessionForm) string {
	const only = "only Claude, OpenCode, Codex and Pi for now"
	if why := m.sessionPathState(f).why; why != "" {
		return why
	}
	if h := strings.ToLower(f.value(sHarness)); h != "" && h != "claude" && h != "opencode" && h != "codex" && h != "pi" {
		return only
	}
	if _, machine := splitProjectMachine(f.value(sProject)); machine != "" && m.nodeByIDOrHost(machine) == nil {
		return "no machine named " + machine
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
			return m, f.focus(f.nextSessionField(f.cur))
		}
		return m, nil
	case tea.KeyShiftTab, tea.KeyUp:
		if f.cur > sProject {
			return m, f.focus(f.prevSessionField(f.cur))
		}
		return m, nil
	case tea.KeyDown:
		if f.cur < sFirstMessage {
			return m, f.focus(f.nextSessionField(f.cur))
		}
		return m, nil
	case tea.KeyLeft, tea.KeyRight:
		// The folder switch owns the arrows everywhere in this form, as the
		// key line says. Text fields still take home, end and editing keys.
		if f.worktree() {
			m.setSessionFolder(protocol.FolderProject)
			if m.sform.cur == sBranch {
				return m, m.sform.focus(sFolder)
			}
		} else {
			m.setSessionFolder(protocol.FolderWorktree)
		}
		return m, nil
	case tea.KeyEnter:
		if f.cur < sFirstMessage {
			return m, f.focus(f.nextSessionField(f.cur))
		}
		return m.submitSessionForm()
	}
	f.err = ""
	if f.cur == sBranch {
		f.branchAuto = false
	}
	var cmd tea.Cmd
	f.in[f.cur], cmd = f.in[f.cur].Update(k)
	if f.cur == sProject {
		m.refreshSessionPathFolder()
		if f.worktree() && f.branchAuto {
			project, _ := splitProjectMachine(f.value(sProject))
			nodeID := f.nodeID
			if node := m.sessionFormNode(*f); node != nil {
				nodeID = node.NodeID
			}
			f.in[sBranch].SetValue(m.ownerWorktreeBranch(project, nodeID))
		}
	}
	return m, cmd
}

// refreshSessionPathFolder keeps the Folder line honest while the owner types
// a path. A new folder, or one that is not a repository, has one tree.
func (m *Model) refreshSessionPathFolder() {
	f := &m.sform
	st := m.sessionPathState(*f)
	if !st.isPath || st.why != "" {
		f.folderWhy = ""
		return
	}
	f.path = st.candidate
	switch {
	case !st.exists:
		f.folderWhy = "a new folder has one tree"
	case !st.isGit:
		f.folderWhy = "not a git repository: no worktrees"
	default:
		f.folderWhy = ""
	}
	if f.folderWhy != "" && f.folder == protocol.FolderWorktree {
		f.folder = protocol.FolderProject
		if f.cur == sBranch {
			m.sform.focus(sFolder)
		}
	}
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
		if m.sessionPathState(*f).why != "" || (f.nodeID != "" && f.nodeID != m.snap.NodeID) {
			return m, f.focus(sProject)
		}
		return m, f.focus(sHarness)
	}
	harness := strings.ToLower(f.value(sHarness))
	if harness == "" {
		harness = "claude"
	}
	folder := f.folder
	if folder != protocol.FolderWorktree {
		folder = protocol.FolderProject
	}
	branch := ""
	if folder == protocol.FolderWorktree {
		branch = f.value(sBranch)
		if branch == "" {
			project, _ := splitProjectMachine(f.value(sProject))
			nodeID := f.nodeID
			if node := m.sessionFormNode(*f); node != nil {
				nodeID = node.NodeID
			}
			branch = m.ownerWorktreeBranch(project, nodeID)
			f.in[sBranch].SetValue(branch)
		}
	}
	st := m.sessionPathState(*f)
	req := protocol.SessionStartRequest{Harness: harness,
		Model: f.value(sModel), FirstMessage: f.value(sFirstMessage), Folder: folder, Branch: branch}
	project = m.sessionFormProject(*f)
	host := m.localHost()
	if st.isPath {
		req.Path = st.candidate
		project = filepath.Base(st.candidate)
	} else {
		req.Project = project
	}
	if node := m.sessionFormNode(*f); node != nil {
		if node.NodeID != m.snap.NodeID {
			req.NodeID = node.NodeID
		}
		host = firstNonEmpty(node.Hostname, host)
	}
	if st.create {
		display := m.tildePath(st.candidate)
		m.sessionPending = &sessionAsk{kind: askCreateFolder, req: req, display: display, host: host}
		m.notice = notice{}
		return m, nil
	}
	m.mode = modeNormal
	what := project
	if st.isPath {
		what = m.tildePath(st.candidate)
	}
	m.say("Starting " + sanitize(what) + " on " + sanitize(host) + "...")
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
	host := m.nodeName(r.res.NodeID, firstNonEmpty(r.res.Hostname, m.localHost()))
	if r.res.Status == "needs_create" || r.res.Status == "needs_trust" {
		display := firstNonEmpty(r.res.Path, r.req.Path)
		if r.res.NodeID == "" || r.res.NodeID == m.snap.NodeID {
			display = m.tildePath(display)
		}
		kind := askCreateFolder
		if r.res.Status == "needs_trust" {
			kind = askTrustClaude
		}
		m.mode = modeNormal
		m.sessionPending = &sessionAsk{kind: kind, req: r.req, display: display, host: host}
		m.notice = notice{}
		return m, nil
	}
	if r.res.Status == "failed" {
		m.fail(firstNonEmpty(sanitize(r.res.Error), "The session did not start."))
		return m, refresh()
	}
	if r.res.Status != "started" {
		m.fail("The start did not finish.")
		return m, refresh()
	}
	host = m.nodeName(r.res.NodeID, firstNonEmpty(m.sform.hostname, m.localHost()))
	project := sanitize(firstNonEmpty(r.res.Project, m.sessionFormProject(m.sform)))
	harness := firstNonEmpty(r.res.Harness, r.req.Harness)
	if harness == "" {
		harness = "claude"
	}
	if harness == "codex" {
		if r.res.EnterPlan == nil {
			m.fail("The daemon did not provide a Codex enter plan.")
			return m, refresh()
		}
		name := project + " (" + sanitize(host) + ")"
		plan := *r.res.EnterPlan
		return m, func() tea.Msg { return enterMsg{name: name, plan: plan} }
	}
	if r.req.FirstMessage != "" {
		enter := firstNonEmpty(sanitize(r.res.EnterNote), "Enter opens it.")
		m.say(fmt.Sprintf("started %s (%s) on %s: first message sent. %s", project, harness, sanitize(host), enter))
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
		host, nodeID := f.hostname, f.nodeID
		node := m.sessionFormNode(f)
		if node != nil {
			host, nodeID = node.Hostname, node.NodeID
		}
		if host == "" {
			host = m.localHost()
		}
		note := "on " + host
		if f.path != "" && f.value(sProject) == f.initialProject && f.nodeID == nodeID {
			note += ", " + sanitize(f.path)
		}
		return note
	case sHarness:
		if f.value(sHarness) == "" {
			return "automatic choice: claude"
		}
		if node := m.sessionFormNode(f); node != nil && node.NodeID != m.snap.NodeID && len(node.Harnesses) > 0 {
			names := make([]string, 0, len(node.Harnesses))
			for _, hi := range node.Harnesses {
				names = append(names, hi.Name)
			}
			return node.Hostname + " has: " + strings.Join(names, ", ")
		}
	case sModel:
		if f.value(sModel) == "" {
			return ""
		}
	case sFolder:
		if f.folderWhy != "" {
			return f.folderWhy
		}
		if f.busy != "" {
			return f.busy
		}
		return "no other session here"
	case sBranch:
		if !f.worktree() {
			return ""
		}
		if f.base == "" {
			return "from HEAD"
		}
		return "from " + f.base + " (HEAD)"
	}
	return ""
}

// sessionFormView draws the form in the place of the session list. rows is the
// room it has, including its heading.
func (m Model) sessionFormView(w, rows int) []string {
	f := m.sform
	out := []string{section("New session", w)}
	const lw = 15
	// The notes line up in one column, after the longest visible value. This
	// keeps the folder reason and the branch note readable at 80 columns, and
	// at the wider column of the design mockup when there is room.
	noteCol := sessionValueCol + 19
	if w >= 100 {
		noteCol = 49
	}
	for i := sessionFormField(0); i < sFields; i++ {
		if i == sBranch && !f.worktree() {
			continue
		}
		var val string
		switch {
		case i == sFolder:
			val = "◂ " + f.folderLabel() + " ▸"
		case i == sModel && f.value(sModel) == "":
			val = "default"
		default:
			val = f.in[i].Value()
		}
		if end := sessionValueCol + ansi.StringWidth(val) + 3; end > noteCol {
			noteCol = end
		}
	}
	indent := strings.Repeat(" ", sessionValueCol)
	for i := sessionFormField(0); i < sFields; i++ {
		if i == sBranch && !f.worktree() {
			continue
		}
		marker := "  "
		if i == f.cur {
			marker = "> "
		}
		var val string
		switch {
		case i == sFolder:
			val = sBold.Render("◂ " + f.folderLabel() + " ▸")
		case i == sModel && f.value(sModel) == "":
			val = sDim.Render("default")
		case i == f.cur:
			val = f.in[i].View()
		default:
			val = sanitize(f.in[i].Value())
		}
		line := marker + sBold.Render(cell(sessionFieldLabels[i], lw)) + val
		if note := m.sessionFieldNote(i); note != "" {
			pad := noteCol - ansi.StringWidth(line)
			if pad < 3 {
				pad = 3
			}
			if room := w - ansi.StringWidth(line) - pad; room > 6 {
				line += strings.Repeat(" ", pad) + sDim.Render(clip(note, room))
			}
		}
		out = append(out, clip(line, w))
		if i == sProject {
			st := m.sessionPathState(f)
			for _, extra := range []string{st.note, m.sessionPathTrustNote(st, f.value(sHarness))} {
				if extra != "" {
					out = append(out, sDim.Render(clip(indent+extra, w)))
				}
			}
		}
	}
	why := m.sessionFormRefusal(f)
	remote := m.sessionRemoteHost(f)
	enterHow := "empty: start it and enter it"
	if remote != "" {
		enterHow += " (through ssh to " + remote + ")"
	}
	if strings.EqualFold(f.value(sHarness), "codex") {
		out = append(out, sDim.Render(clip(indent+enterHow, w)))
		if remote != "" {
			out = append(out, sDim.Render(clip(indent+"codex: opens it now through ssh to "+remote, w)))
		} else {
			out = append(out, sDim.Render(clip(indent+"codex: opens it now", w)))
		}
	} else if strings.TrimSpace(f.in[sFirstMessage].Value()) == "" {
		out = append(out, sDim.Render(clip(indent+enterHow, w)))
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

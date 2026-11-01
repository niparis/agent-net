package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

// The actions of the screen (decision 0008): send a message as the owner and
// stop a worker. Each one calls only the operator interface, shows a prompt in
// the help row, and ends with one result line, green or red, under the title.

type inputMode int

const (
	modeNormal      inputMode = iota
	modeCompose               // typing a message to the selected session
	modeSessionForm           // the new-session form
	modeConfirm               // asking before stopping a session
	modeDefectNote            // typing the one-line defect found later
	modeQuotaConfirm
	modeTrust         // asking before trusting a session with peer requests
	modePolicy        // the delegation policy form (policy.go)
	modeRename        // typing a display name for the selected session's machine
	modeProjectRename // typing the selected project display name
	modeIntegrations  // the setup integrations of the whole network (integrations.go)
)

// A result is shown in the key line at full brightness for its hold, then
// fades through three colour steps and gives the hints back. No key is needed.
const (
	noticeHold     = 4 * time.Second
	noticeHoldErr  = 8 * time.Second
	noticeFade     = time.Second
	noticeFadeStep = noticeFade / 3
	noticeAnimStep = 250 * time.Millisecond
)

// notice is the result of the last action.
type notice struct {
	text string
	err  bool
	at   time.Time
}

func (n notice) hold() time.Duration {
	if n.err {
		return noticeHoldErr
	}
	return noticeHold
}

func (n notice) life() time.Duration { return n.hold() + noticeFade }

// actionMsg is the end of an action that ran in the background.
type actionMsg struct {
	onboarding string
	text       string
	err        error
}

// ---- notices ----

func (m *Model) say(text string) { m.notice = notice{text: text, at: m.now()} }
func (m *Model) fail(text string) {
	m.notice = notice{text: text, err: true, at: m.now()}
}

// activeNotice returns the notice to show, or nil when there is none or its
// hold and fade are over.
func (m Model) activeNotice() *notice {
	if a := m.sessionPending; a != nil {
		return &notice{text: a.question() + confirmSuffix, at: m.now()}
	}
	if a := m.resumeConfirm; a != nil {
		return &notice{text: fmt.Sprintf("Resume %s (claude, last active %s ago) and enter it?", sanitize(a.Profile.Name), ago(m.now().Sub(time.Unix(a.LastSeen, 0)))) + confirmSuffix, at: m.now()}
	}
	if m.notice.text == "" || m.now().Sub(m.notice.at) >= m.notice.life() {
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
			return actionMsg{onboarding: "talk", text: "Queued for " + name + ": the gateway is not reachable, it is sent when the link is back."}
		}
		id := ""
		if res.Message != nil {
			id = " (" + res.Message.ID + ")"
		}
		return actionMsg{onboarding: "talk", text: "Sent to " + name + id + " as you. The reply will show in the messages."}
	}
}

// ---- stop a session ----

func (m Model) startConfirm() (Model, tea.Cmd) {
	a, ok := m.selectedAgent()
	switch {
	case !ok:
		m.fail("Nothing is selected: pick a session first.")
	case !a.Online && a.Worker == nil:
		m.fail("That session is offline already.")
	default:
		m.mode = modeConfirm
		m.confirmID = a.SessionID
		m.confirmOwner = a.Worker == nil
		m.confirmName = sanitize(dashIfEmpty(a.Profile.Name)) + " (" + sanitize(a.Hostname) + ")"
		if m.confirmOwner {
			m.confirmName = sanitize(dashIfEmpty(a.Profile.Name))
		}
		m.notice = notice{}
	}
	return m, nil
}

func (m Model) confirmKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	yes, no := confirmAnswer(k)
	switch {
	case yes:
		m.mode = modeNormal
		if m.confirmDefect {
			worker, name := m.defectWorker, m.defectName
			note := strings.TrimSpace(m.defect.Value())
			m.confirmDefect = false
			m.say("Reporting the defect...")
			return m, m.defectCmd(worker, name, note)
		}
		m.say("Stopping...")
		if m.confirmOwner {
			return m, m.stopSessionCmd(m.confirmID)
		}
		return m, m.stopCmd(m.confirmID, m.confirmName)
	case no:
		m.mode = modeNormal
		if m.confirmDefect {
			m.confirmDefect = false
			m.say("Defect not reported.")
		} else {
			m.say("Not stopped.")
		}
	}
	return m, nil
}

// ---- defect found later ----

// startDefect opens the one-line note for a defect found later on the selected
// worker conversation. The owner files it through the owner path; the worker's
// caller can do the same with agent_rate kind defect_found_later.
func (m Model) startDefect() (Model, tea.Cmd) {
	if m.tab != messagesTab {
		return m, nil
	}
	rows := m.messageConversations()
	if len(rows) == 0 {
		m.fail("Pick a worker conversation first.")
		return m, nil
	}
	row := rows[m.selectedConversationIndex(rows)]
	if !row.worker || row.workerSession == "" {
		m.fail("Defects are reported on a worker conversation.")
		return m, nil
	}
	m.mode = modeDefectNote
	m.defect = textinput.New()
	name := sanitize(strings.TrimSpace(m.conversationName(row.workerSession)))
	if name == "" || name == "unknown session" {
		name = shortID(row.workerSession)
	}
	m.defect.Prompt = "defect later for " + name + "> "
	m.defect.CharLimit = protocol.RateNoteMax
	m.defect.Cursor.SetMode(cursor.CursorHide)
	m.defectWorker, m.defectName = row.workerSession, name
	m.notice = notice{}
	return m, m.defect.Focus()
}

func (m Model) defectKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEsc:
		m.mode = modeNormal
		m.say("Defect not reported.")
		return m, nil
	case tea.KeyEnter:
		note := strings.TrimSpace(m.defect.Value())
		if note == "" {
			m.fail("Write one line about the defect.")
			return m, nil
		}
		m.mode = modeConfirm
		m.confirmDefect = true
		m.notice = notice{}
		return m, nil
	}
	var cmd tea.Cmd
	m.defect, cmd = m.defect.Update(k)
	return m, cmd
}

func (m Model) defectCmd(worker, name, note string) tea.Cmd {
	src := m.src
	return func() tea.Msg {
		reporter, ok := src.(interface {
			ReportDefect(context.Context, string, string) (protocol.RateResult, error)
		})
		if !ok {
			return actionMsg{err: fmt.Errorf("Defect reports need a newer daemon and gateway.")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := reporter.ReportDefect(ctx, worker, note)
		if err != nil {
			return actionMsg{err: fmt.Errorf("Defect not reported: %s", explain(err))}
		}
		if !res.Accepted {
			reason := strings.TrimSpace(res.Reason)
			if reason == "" {
				reason = "the gateway refused it"
			}
			return actionMsg{err: fmt.Errorf("Defect not reported: %s", explain(fmt.Errorf("%s", reason)))}
		}
		return actionMsg{text: "Defect found later recorded for " + name + "."}
	}
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
	if !ok || protocol.IsOwner(a.Harness) {
		m.fail("Pick an agent session.")
		return m, nil
	}
	project := firstNonEmpty(a.Repo.Project, a.Profile.Project)
	if project == "" {
		m.fail("This session has no project node.")
		return m, nil
	}
	rule := m.wildcardRule(project)
	next := "ask"
	if rule == "ask" {
		next = "auto"
	} else if rule == "auto" {
		next = "normal"
	}
	setter, ok := m.src.(interface {
		SetRequestRule(context.Context, protocol.Edge) error
	})
	if !ok {
		m.fail("Request rules are unavailable.")
		return m, nil
	}
	e := protocol.Edge{From: "*", To: project, Kind: "depends", Rule: next}
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := setter.SetRequestRule(ctx, e); err != nil {
			return actionMsg{err: fmt.Errorf("Request rule not changed: %s", explain(err))}
		}
		return actionMsg{text: "Request rule for " + project + " is " + next + "."}
	}
}

func (m Model) trustKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	yes, no := confirmAnswer(k)
	switch {
	case yes:
		m.mode = modeNormal
		m.say("Turning peer trust on...")
		return m, m.trustCmd(m.confirmID, m.confirmName, true)
	case no:
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

// onAction ends an action: the result line, and an immediate refresh so the
// updated data is visible.
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

func (m Model) startProjectRename() (Model, tea.Cmd) {
	node, ok := m.mapNode(m.mp.selected)
	if !ok || node.Kind != "component" {
		m.fail("Pick a project to name.")
		return m, nil
	}
	m.mode = modeProjectRename
	m.rename = textinput.New()
	m.rename.Prompt = "name for " + sanitize(node.Name) + "> "
	m.rename.CharLimit = protocol.ProjectMaxFieldLen
	m.rename.Cursor.SetMode(cursor.CursorHide)
	shown := m.projectName(node.Name)
	if shown != node.Name {
		m.rename.SetValue(shown)
	}
	m.rename.CursorEnd()
	m.renameNode = node.Name
	m.notice = notice{}
	return m, m.rename.Focus()
}

func (m Model) projectRenameKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEsc:
		m.mode = modeNormal
		m.say("Project name unchanged.")
		return m, nil
	case tea.KeyEnter:
		project, name := m.renameNode, strings.TrimSpace(m.rename.Value())
		m.mode = modeNormal
		m.say("Saving the project name...")
		src := m.src
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, err := src.SetProjectDisplayName(ctx, project, name)
			if err != nil {
				return actionMsg{err: fmt.Errorf("Project name not changed: %s", explain(err))}
			}
			return actionMsg{text: "Project name saved."}
		}
	}
	var cmd tea.Cmd
	m.rename, cmd = m.rename.Update(k)
	return m, cmd
}

func (m Model) stopSessionCmd(id string) tea.Cmd {
	src := m.src
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		note, err := src.SessionStop(ctx, id)
		if err != nil {
			return actionMsg{err: fmt.Errorf("Not stopped: %s", explain(err))}
		}
		return actionMsg{text: note}
	}
}

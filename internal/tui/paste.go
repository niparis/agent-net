package tui

import (
	tea "charm.land/bubbletea/v2"
)

// paste routes bracketed input only to the active editor. It must never be
// interpreted as shortcuts, even when it contains q, enter, or other commands.
func (m Model) paste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.mode {
	case modeCompose:
		m.compose, cmd = m.compose.Update(msg)
	case modeSessionForm:
		return m.sessionFormInput(msg)
	case modeDefectNote:
		m.defect, cmd = m.defect.Update(msg)
	case modeDefectSeverity:
		m.defectSeverity, cmd = m.defectSeverity.Update(msg)
	case modeRename, modeProjectRename, modeTimeZone, modeSettingValue, modeSettingRepo:
		m.rename, cmd = m.rename.Update(msg)
	case modePolicy:
		{
			m.pol.err = ""
			switch m.pol.cur {
			case pfCoder:
				m.pol.coder, cmd = m.pol.coder.Update(msg)
			case pfMain:
				m.pol.main, cmd = m.pol.main.Update(msg)
			}
		}
	case modeApps:
		if !m.apps.loading && (m.apps.stage == "rename" || m.apps.stage == "code" || m.apps.stage == "url" || (m.apps.stage == "approve" && m.apps.editingName)) {
			m.apps.input, cmd = m.apps.input.Update(msg)
		}
	case modeNormal:
		switch {
		case m.help || m.proposalConfirm || m.proposalHistory || m.proposalDetail != nil:
		case m.focus == focusCTO && m.cto != nil && m.cto.settingsForm != nil:
			f := m.cto.settingsForm
			switch f.field {
			case 1:
				f.titleInput, cmd = f.titleInput.Update(msg)
			case 2:
				f.input, cmd = f.input.Update(msg)
			}
		case m.tab == inboxTab && m.inboxEditing:
			m.inboxInput, cmd = m.inboxInput.Update(msg)
		case m.filtering:
			m.input, cmd = m.input.Update(msg)
			m.filter = m.input.Value()
			m.rebuildConversations()
			m.ensureSelection()
			m.fixScroll()
		case m.tab == graphTab && m.mp.finding:
			m.mp.findQuery += sanitize(msg.Content)
		case m.tab == toolsTab && m.tl.searching:
			m.tl.search += sanitize(msg.Content)
			m.tl.row, m.tl.scroll = 0, 0
			m.toolsClamp()
		case m.tab == toolsTab && m.tl.agentSearching:
			m.tl.agentFilter += sanitize(msg.Content)
			m.tl.skillRow = 0
		case m.tab == toolsTab && m.tl.modal != nil && m.tl.modal.picker != nil && m.tl.modal.picker.filtering:
			m.tl.modal.picker.filter += sanitize(msg.Content)
			m.tl.modal.picker.cur = 0
			pickerFoot(m.tl.modal, m.tl.modal.picker)
		case m.tab == toolsTab && m.tl.modal != nil && m.tl.modal.form != nil:
			f := m.tl.modal.form
			if f.field < len(f.values) {
				f.values[f.field] += sanitize(msg.Content)
			}
		}
	}
	return m, cmd
}

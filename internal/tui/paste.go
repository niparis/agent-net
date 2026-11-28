package tui

import tea "charm.land/bubbletea/v2"

// paste routes bracketed input only to the active editor. It must never be
// interpreted as shortcuts, even when it contains q, enter, or other commands.
func (m Model) paste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.archive != nil || m.resumeConfirm != nil || m.moveConfirm != nil || m.sessionPending != nil {
		return m, nil
	}
	var cmd tea.Cmd
	switch m.mode {
	case modeCompose:
		m.compose, cmd = m.compose.Update(msg)
	case modeSessionForm:
		return m.sessionFormInput(msg)
	case modeCharterForm:
		m.charterForm.input, cmd = m.charterForm.input.Update(msg)
		return m, cmd
	case modeTeamForm:
		f := &m.teamForm
		f.in[f.cur], cmd = f.in[f.cur].Update(msg)
		return m, cmd
	case modeDefectNote:
		m.defect, cmd = m.defect.Update(msg)
	case modeDefectSeverity:
		m.defectSeverity, cmd = m.defectSeverity.Update(msg)
	case modeRename, modeProjectRename, modeTimeZone, modeStandingAdd, modeSettingValue, modeSettingRepo, modeQuotaFloor:
		m.rename, cmd = m.rename.Update(msg)
	case modeApps:
		return m.pasteApps(msg)
	case modeNormal:
		switch {
		case m.help || (m.qualityDetail && m.tab == quotasTab):
		case m.focus == focusCTO && m.cto != nil:
			if f := m.cto.settingsForm; f != nil {
				switch f.field {
				case 0:
					// The harness is a switch, not a text field.
				case 1:
					f.titleInput, cmd = f.titleInput.Update(msg)
				case 2:
					f.input, cmd = f.input.Update(msg)
				}
			}
		case m.tab == graphTab && m.mp.finding:
			m.mp.findQuery += sanitize(msg.Content)
		case m.mapOn:
		case m.filtering:
			m.input, cmd = m.input.Update(msg)
			m.filter = m.input.Value()
			m.rebuildConversations()
			m.ensureSelection()
			m.fixScroll()
		case m.open != nil:
		case m.tab == settingsTab && m.apps.stage == "rename":
			return m.pasteApps(msg)
		}
	}
	return m, cmd
}

func (m Model) pasteApps(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	f := &m.apps
	if f.loading || (f.stage != "rename" && f.stage != "code" && f.stage != "url" && (f.stage != "approve" || !f.editingName)) {
		return m, nil
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return m, cmd
}

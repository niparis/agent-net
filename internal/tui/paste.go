package tui

import tea "charm.land/bubbletea/v2"

// paste routes bracketed input only to the active editor. It must never be
// interpreted as shortcuts, even when it contains q, enter, or other commands.
func (m Model) paste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.archive != nil || m.resumeConfirm != nil || m.sessionPending != nil || m.proposalConfirm || (m.proposalHistory && !m.help) || (m.proposalDetail != nil && !m.help) {
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
	case modePolicy:
		m.pol.err = ""
		switch m.pol.cur {
		case pfCoder:
			m.pol.coder, cmd = m.pol.coder.Update(msg)
		case pfMain:
			m.pol.main, cmd = m.pol.main.Update(msg)
		}
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
		case m.tab == toolsTab && !m.mapOn && m.tl.capturing():
			return m.pasteTools(msg)
		case m.mapOn:
		case m.filtering:
			m.input, cmd = m.input.Update(msg)
			m.filter = m.input.Value()
			m.rebuildConversations()
			m.ensureSelection()
			m.fixScroll()
		case m.open != nil:
		case m.tab == inboxTab && m.inboxEditing:
			m.inboxInput, cmd = m.inboxInput.Update(msg)
		case m.tab == deliveryTab && m.delivery.reporting:
			m.delivery.reportInput, cmd = m.delivery.reportInput.Update(msg)
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

func (m Model) pasteTools(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	t := &m.tl
	text := sanitize(msg.Content)
	if md := t.modal; md != nil {
		switch {
		case md.menu != nil:
		case md.form != nil:
			f := md.form
			if f.field < len(f.values) {
				f.values[f.field] += text
			}
		case md.picker != nil && !md.loading && md.picker.filtering:
			md.picker.filter += text
			md.picker.cur = 0
			pickerFoot(md, md.picker)
		}
	} else if !t.allTools && t.agentSearching {
		t.agentFilter += text
		t.skillRow = 0
	} else if t.allTools && !t.projectsOpen && t.searching {
		t.search += text
		t.row, t.scroll = 0, 0
		m.toolsClamp()
	}
	return m, nil
}

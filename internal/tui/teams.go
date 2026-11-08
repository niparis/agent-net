package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

// A Team has an authority mode and a held back text (decision 0088). The Graph
// can show one Team at a time, and the owner can create, rename, fill, archive
// and set the mode and held back text of Teams from the Graph tab. The owner
// can undo the last mode or held back change with u.

// teamEntry is one active Team as the Graph reads it.
type teamEntry struct {
	ID          string
	Name        string
	Purpose     string
	Mode        string
	HeldBack    string
	MemberCount int
}

// teamsOf reads the active Teams from a raw typed Graph read. Names are still
// display names here, before prepareGraph turns typed keys into node names.
func teamsOf(v protocol.GraphView) []teamEntry {
	var out []teamEntry
	index := map[string]int{}
	for _, n := range v.Nodes {
		if n.Ref == nil || n.Ref.Kind != "team" {
			continue
		}
		if n.Active != nil && !*n.Active {
			continue
		}
		index[n.Ref.ID] = len(out)
		out = append(out, teamEntry{ID: n.Ref.ID, Name: n.Name, Purpose: n.Charter.Purpose, Mode: protocol.NormalAuthorityMode(n.AuthorityMode), HeldBack: n.HeldBack})
	}
	for _, e := range v.Edges {
		if e.Kind != "member_of" || e.ToRef == nil || e.ToRef.Kind != "team" {
			continue
		}
		if i, ok := index[e.ToRef.ID]; ok {
			out[i].MemberCount++
		}
	}
	sortTeams(out)
	return out
}

func sortTeams(teams []teamEntry) {
	sort.Slice(teams, func(i, j int) bool {
		if teams[i].Name != teams[j].Name {
			return teams[i].Name < teams[j].Name
		}
		return teams[i].ID < teams[j].ID
	})
}

func (m Model) activeTeams() []teamEntry { return m.mp.teams }

func (m Model) teamByID(id string) (teamEntry, bool) {
	for _, t := range m.mp.teams {
		if t.ID == id {
			return t, true
		}
	}
	return teamEntry{}, false
}

func (m Model) teamName(id string) string {
	if t, ok := m.teamByID(id); ok {
		return t.Name
	}
	return id
}

// teamFilterName is the Team the Graph shows, or "all".
func (m Model) teamFilterName() string {
	if m.teamFilter == unassignedTeam {
		return "unassigned"
	}
	if m.teamFilter == "" {
		return "all"
	}
	return sanitize(m.teamName(m.teamFilter))
}

// applyTeamView builds the display projection while retaining full card data.
func (m *Model) applyTeamView(fit bool) {
	full := m.graphProjection()
	m.mp.nodes, m.mp.edges = full.Nodes, full.Edges
	view := m.graphDisplayView(full)
	first := m.graphState.Graph == nil
	m.graphState, _ = graphview.Update(m.graphState, graphview.GraphChanged{Graph: view})
	if first || fit {
		w, h := m.graphSize()
		m.graphState, _ = graphview.Update(m.graphState, graphview.Fit{Viewport: graphview.Viewport{Width: float64(w), Height: float64(h)}})
	}
	// Cards retain the complete merged view.
	m.mapEnsureSelection()
}

func (m *Model) setTeamFilter(id string) {
	m.teamFilter = id
	m.mp.focusNode = ""
	m.mp.teamInside = false
	m.mp.levels = nil
	if m.mp.full.Nodes == nil {
		return
	}
	m.applyTeamView(true)
	if id != "" {
		m.selectGraphInterior()
	}
	m.smoothCamera()
}

// cycleTeamFilter moves All -> each active Team -> All.
func (m Model) cycleTeamFilter() (Model, tea.Cmd) {
	teams := m.activeTeams()
	if len(teams) == 0 {
		m.say("No teams yet. Press T to make one.")
		return m, nil
	}
	next := 1
	for i, t := range teams {
		if t.ID == m.teamFilter {
			next = i + 2
			break
		}
	}
	if next > len(teams) {
		next = 0
	}
	id := ""
	if next > 0 {
		id = teams[next-1].ID
	}
	m.setTeamFilter(id)
	return m, nil
}

// selectedRef is the agent or repo under the selection, on the Graph or its
// card. Nothing else can become a Team member.
func (m Model) selectedRef() *protocol.GraphRef {
	name := m.mp.selected
	if !m.mapOn && m.graphState.Graph != nil {
		name = m.graphState.Graph.Selected()
	}
	n, ok := m.mapNode(name)
	if !ok || n.Ref == nil {
		return nil
	}
	if n.Ref.Kind != "agent" && n.Ref.Kind != "repo" {
		return nil
	}
	ref := *n.Ref
	return &ref
}

// memberTeam is the Team the ref currently belongs to, or "".
func (m Model) memberTeam(ref protocol.GraphRef) string {
	for _, e := range m.mp.full.Edges {
		if e.Kind != "member_of" || e.FromRef == nil || e.ToRef == nil {
			continue
		}
		if e.FromRef.Kind == ref.Kind && e.FromRef.ID == ref.ID && e.ToRef.Kind == "team" {
			return e.ToRef.ID
		}
	}
	return ""
}

// ---- Team writes ----

type teamWriteSource interface {
	WriteTeam(context.Context, protocol.TeamWrite) error
}

type teamWriteMsg struct {
	q    protocol.TeamWrite
	done string
	err  error
}

func (m Model) teamWriteCmd(q protocol.TeamWrite, done string) tea.Cmd {
	src := m.src
	return func() tea.Msg {
		s, ok := src.(teamWriteSource)
		if !ok {
			return teamWriteMsg{q: q, done: done, err: ErrTooOld}
		}
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		return teamWriteMsg{q: q, done: done, err: s.WriteTeam(ctx, q)}
	}
}

func (m Model) onTeamWrite(r teamWriteMsg) (Model, tea.Cmd) {
	if r.err != nil {
		m.teamUndo = nil
		m.fail(teamWriteError(r.err))
	} else {
		m.say(r.done)
	}
	// Refresh even after a refusal: a stale membership or name is then ready
	// for the next try.
	m.mp.loading = true
	return m, m.mapCmd()
}

func teamWriteError(err error) string {
	if errors.Is(err, ErrTooOld) || isUnavailable(err) {
		return "Teams need a newer daemon and gateway."
	}
	text := explain(err)
	switch {
	case strings.Contains(text, "typed Graph cutover is waiting for all machines to upgrade"):
		return "Teams are waiting for all machines to upgrade before they can be saved."
	case strings.Contains(text, "UNIQUE constraint failed: teams"):
		return "A team with that name already exists, active or archived."
	}
	return text
}

// ---- the Team list ----

func (m Model) startTeamMenu() (Model, tea.Cmd) {
	m.mode = modeTeams
	m.teamIndex = 0
	for i, t := range m.activeTeams() {
		if t.ID == m.teamFilter {
			m.teamIndex = i
			break
		}
	}
	m.notice = notice{}
	return m, nil
}

func (m Model) currentTeam() (teamEntry, bool) {
	teams := m.activeTeams()
	if len(teams) == 0 {
		return teamEntry{}, false
	}
	i := min(max(m.teamIndex, 0), len(teams)-1)
	return teams[i], true
}

func (m Model) teamMenuKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := keyAction(contextTeams, k.String())
	switch key {
	case actQuit:
		m.quitting = true
		return m, tea.Quit
	case actClose:
		m.mode = modeNormal
		return m, nil
	case "help":
		m.openHelp()
		return m, nil
	case actUp:
		m.teamIndex = max(m.teamIndex-1, 0)
		return m, nil
	case actDown:
		m.teamIndex = min(m.teamIndex+1, max(len(m.activeTeams())-1, 0))
		return m, nil
	}
	team, ok := m.currentTeam()
	switch key {
	case "lead":
		if ok {
			return m.startLeadPicker(protocol.GraphRef{Kind: "team", ID: team.ID}, team.Name), nil
		}
	case "show team":
		if !ok {
			m.fail("No team yet. Press c to make one.")
			return m, nil
		}
		m.setTeamFilter(team.ID)
		m.mode = modeNormal
		return m, nil
	case "create team":
		m.teamForm = newTeamForm(false, "", "")
		m.mode = modeTeamForm
		return m, m.teamForm.in[0].Focus()
	case "rename team":
		if !ok {
			m.fail("No team yet. Press c to make one.")
			return m, nil
		}
		m.teamForm = newTeamForm(true, team.ID, team.Name)
		m.mode = modeTeamForm
		return m, m.teamForm.in[0].Focus()
	case "edit purpose":
		if !ok {
			m.fail("No team yet. Press c to make one.")
			return m, nil
		}
		m.teamForm = newTeamPurposeForm(team.ID, team.Name, team.Purpose)
		m.mode = modeTeamForm
		return m, m.teamForm.in[1].Focus()
	case "archive team":
		if !ok {
			m.fail("No team yet. Press c to make one.")
			return m, nil
		}
		return m, m.teamWriteCmd(protocol.TeamWrite{Action: "archive", ID: team.ID},
			"Archived team "+sanitize(team.Name)+".")
	case "member":
		if !ok {
			m.fail("No team yet. Press c to make one.")
			return m, nil
		}
		return m.toggleTeamMember(team)
	case "authority mode":
		if !ok {
			m.fail("No team yet. Press c to make one.")
			return m, nil
		}
		next := protocol.NextAuthorityMode(team.Mode)
		m.teamUndo = &teamUndoStep{q: protocol.TeamWrite{Action: "mode", ID: team.ID, Mode: team.Mode},
			done: "Put the mode of team " + sanitize(team.Name) + " back to " + protocol.AuthorityModeLabel(team.Mode) + "."}
		return m, m.teamWriteCmd(protocol.TeamWrite{Action: "mode", ID: team.ID, Mode: next},
			"Set the mode of team "+sanitize(team.Name)+" to "+protocol.AuthorityModeLabel(next)+". u undoes it.")
	case "edit held back":
		if !ok {
			m.fail("No team yet. Press c to make one.")
			return m, nil
		}
		m.teamForm = newTeamHeldBackForm(team.ID, team.Name, team.HeldBack, false)
		m.mode = modeTeamForm
		return m, m.teamForm.in[1].Focus()
	case "repo held back":
		ref := m.selectedRef()
		if ref == nil || ref.Kind != "repo" {
			m.fail("Select a repo on the Graph first.")
			return m, nil
		}
		name := m.graphName(graphRefKey(ref))
		held := ""
		if n, found := m.mapNode(graphRefKey(ref)); found {
			held = n.HeldBack
		}
		m.teamForm = newTeamHeldBackForm(ref.ID, name, held, true)
		m.mode = modeTeamForm
		return m, m.teamForm.in[1].Focus()
	case "undo team setting":
		if m.teamUndo == nil {
			m.fail("Nothing to undo.")
			return m, nil
		}
		step := *m.teamUndo
		m.teamUndo = nil
		return m, m.teamWriteCmd(step.q, step.done)
	}
	return m, nil
}

// teamUndoStep is the write that puts the last mode or held back change back.
type teamUndoStep struct {
	q    protocol.TeamWrite
	done string
}

func (m Model) toggleTeamMember(team teamEntry) (Model, tea.Cmd) {
	ref := m.selectedRef()
	if ref == nil {
		m.fail("Select a repo or agent on the Graph first.")
		return m, nil
	}
	old := m.memberTeam(*ref)
	label := graphRefKey(ref)
	if name := m.graphName(label); name != "" {
		label = name
	}
	q := protocol.TeamWrite{Action: "membership", ID: team.ID, Member: ref, ExpectTeam: old}
	done := "Added " + label + " to " + sanitize(team.Name) + "."
	switch {
	case old == team.ID:
		q.ID = ""
		done = "Removed " + label + " from " + sanitize(team.Name) + "."
	case old != "":
		done = "Moved " + label + " from " + sanitize(m.teamName(old)) + " to " + sanitize(team.Name) + "."
	}
	return m, m.teamWriteCmd(q, done)
}

func (m Model) teamsView(w, rows int) []string {
	teams := m.activeTeams()
	out := []string{section("Teams", w)}
	const nameW, memberW, modeW = 18, 9, 15
	if len(teams) == 0 {
		out = append(out, sDim.Render(clip("  No teams yet. c creates one. Team writes wait for all machines to upgrade.", w)))
	} else {
		out = append(out, sDim.Render(clip("  "+cell("name", nameW)+cell("members", memberW)+cell("mode", modeW)+"purpose", w)))
		i := min(max(m.teamIndex, 0), len(teams)-1)
		visible := max(rows-9, 1)
		start := 0
		if i >= visible {
			start = i - visible + 1
		}
		for j := start; j < len(teams) && j < start+visible; j++ {
			t := teams[j]
			marker := "  "
			if j == i {
				marker = "> "
			}
			line := marker + cell(t.Name, nameW) + cell(fmt.Sprintf("%d", t.MemberCount), memberW) + cell(protocol.AuthorityModeLabel(t.Mode), modeW) + sanitize(t.Purpose)
			if j == i {
				line = graphSelectionStyle.Render(line)
			}
			out = append(out, clip(line, w))
		}
	}
	if team, ok := m.currentTeam(); ok {
		out = append(out, sDim.Render(clip("  mode: "+protocol.AuthorityModeLabel(team.Mode)+" - "+protocol.AuthorityModeMeaning(team.Mode), w)))
		held := "nothing beyond the always list"
		if h := strings.TrimSpace(team.HeldBack); h != "" {
			held = sanitize(h)
		}
		out = append(out, sDim.Render(clip("  held back: "+held+" (always: "+protocol.AlwaysHeldBack+")", w)))
	}
	out = append(out, "")
	filter := "  graph: all"
	if m.teamFilter != "" {
		filter = "  graph: " + sanitize(m.teamFilterName())
	}
	out = append(out, sDim.Render(clip(filter, w)))
	if ref := m.selectedRef(); ref != nil {
		label := m.graphName(graphRefKey(ref))
		old := m.memberTeam(*ref)
		line := "  member: " + label
		if old == "" {
			line += " (no team)"
		} else {
			line += " in " + sanitize(m.teamName(old))
		}
		out = append(out, clip(line, w))
		out = append(out, sDim.Render(clip("  "+m.teamMemberAction(label, old), w)))
	} else {
		out = append(out, sDim.Render(clip("  member: no repo or agent selected", w)))
	}
	for len(out) < rows {
		out = append(out, "")
	}
	if len(out) > rows {
		out = out[:rows]
	}
	for i := range out {
		out[i] = clip(out[i], w)
	}
	return out
}

// teamMemberAction says what space will do to the selected member.
func (m Model) teamMemberAction(label, old string) string {
	team, ok := m.currentTeam()
	if !ok {
		return "space needs a team"
	}
	switch old {
	case "":
		return "space adds " + label + " to " + sanitize(team.Name)
	case team.ID:
		return "space removes " + label + " from " + sanitize(team.Name)
	}
	return "space moves " + label + " from " + sanitize(m.teamName(old)) + " to " + sanitize(team.Name)
}

// ---- the Team form ----

type teamForm struct {
	rename      bool
	purposeOnly bool
	heldOnly    bool // sets held back text; repo says id is a Repo, not a Team
	repo        bool
	old         string // the held back text when the form opened
	id          string
	expectName  string
	in          [2]textinput.Model
	cur         int
	err         string
}

func newTeamPurposeForm(id, name, purpose string) teamForm {
	f := newTeamForm(false, "", "")
	f.purposeOnly = true
	f.id = id
	f.expectName = name
	f.in[0].SetValue(name)
	f.in[0].CursorEnd()
	f.in[1].SetValue(purpose)
	f.in[1].CursorEnd()
	f.cur = 1
	return f
}

func newTeamHeldBackForm(id, name, held string, repo bool) teamForm {
	f := newTeamForm(false, "", "")
	f.heldOnly, f.repo, f.old = true, repo, held
	f.id = id
	f.expectName = name
	f.in[0].SetValue(name)
	f.in[1].SetValue(held)
	f.in[1].CursorEnd()
	f.cur = 1
	return f
}

func newTeamForm(rename bool, id, name string) teamForm {
	var f teamForm
	f.rename, f.id = rename, id
	for i := range f.in {
		t := newTextInput()
		t.Prompt = ""
		t.CharLimit = 4000
		t.SetVirtualCursor(false)
		f.in[i] = t
	}
	f.in[0].SetValue(name)
	f.in[0].CursorEnd()
	f.expectName = name
	return f
}

func (f *teamForm) focus(i int) tea.Cmd {
	f.in[f.cur].Blur()
	f.cur = i
	f.in[f.cur].CursorEnd()
	return f.in[f.cur].Focus()
}

func (m Model) teamFormKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k = normalizeKey(k)
	f := &m.teamForm
	switch keyAction(contextForm, k.String()) {
	case "cancel":
		m.mode = modeTeams
		m.say("Team change cancelled.")
		return m, nil
	case "tab field", "next field":
		if !f.rename && !f.purposeOnly && !f.heldOnly && f.cur == 0 {
			return m, f.focus(1)
		}
		return m, nil
	case "previous field":
		if !f.purposeOnly && !f.heldOnly && f.cur > 0 {
			return m, f.focus(0)
		}
		return m, nil
	case "submit":
		if !f.rename && !f.purposeOnly && !f.heldOnly && f.cur == 0 {
			return m, f.focus(1)
		}
		return m.submitTeamForm()
	}
	var cmd tea.Cmd
	f.err = ""
	f.in[f.cur], cmd = f.in[f.cur].Update(k)
	return m, cmd
}

func (m Model) submitTeamForm() (tea.Model, tea.Cmd) {
	f := &m.teamForm
	if f.heldOnly {
		held := sanitize(strings.TrimSpace(f.in[1].Value()))
		q := protocol.TeamWrite{Action: "held_back", ID: f.id, HeldBack: held}
		done := "Updated what team " + sanitize(f.expectName) + " may not do."
		if f.repo {
			q.Action = "repo_held_back"
			done = "Updated what repo " + sanitize(f.expectName) + " may not do."
		}
		m.teamUndo = &teamUndoStep{q: protocol.TeamWrite{Action: q.Action, ID: f.id, HeldBack: f.old},
			done: "Put back what " + sanitize(f.expectName) + " may not do."}
		m.mode = modeTeams
		return m, m.teamWriteCmd(q, done)
	}
	if f.purposeOnly {
		q := protocol.TeamWrite{Action: "purpose", ID: f.id, Purpose: sanitize(strings.TrimSpace(f.in[1].Value()))}
		done := "Updated the purpose of team " + sanitize(f.expectName) + "."
		m.mode = modeTeams
		return m, m.teamWriteCmd(q, done)
	}
	name := sanitize(strings.TrimSpace(f.in[0].Value()))
	if name == "" {
		f.err = "Name the team."
		return m, f.focus(0)
	}
	q := protocol.TeamWrite{Action: "create", ID: name, Name: name, Purpose: sanitize(strings.TrimSpace(f.in[1].Value()))}
	done := "Created team " + name + "."
	if f.rename {
		q = protocol.TeamWrite{Action: "rename", ID: f.id, Name: name, ExpectName: f.expectName}
		done = "Renamed team " + sanitize(f.expectName) + " to " + name + "."
	}
	m.mode = modeTeams
	return m, m.teamWriteCmd(q, done)
}

func (m Model) teamFormView(w, rows int) []string {
	f := m.teamForm
	title := "New team"
	note := "  Teams are labels: they change no rules, settings or delivery."
	if f.rename {
		title = "Rename " + sanitize(f.expectName)
		note = "  The Team ID and its history stay the same."
	}
	if f.purposeOnly {
		title = "Purpose of " + sanitize(f.expectName)
		note = "  The Team ID, name and history stay the same."
	}
	if f.heldOnly {
		title = "Held back from " + sanitize(f.expectName)
		note = "  Free text. The owner always holds back " + protocol.AlwaysHeldBack + "."
	}
	out := []string{section(title, w)}
	const labelW = 9
	labels := [2]string{"Name", "Purpose"}
	if f.heldOnly {
		labels[1] = "Held back"
	}
	for i := range labels {
		if f.rename && i == 1 || (f.purposeOnly || f.heldOnly) && i == 0 {
			continue
		}
		marker := "  "
		if i == f.cur {
			marker = "> "
		}
		val := sanitize(f.in[i].Value())
		if i == f.cur {
			val = f.in[i].View()
		}
		out = append(out, clip(marker+sBold.Render(cell(labels[i], labelW))+val, w))
	}
	out = append(out, sDim.Render(clip(note, w)))
	if f.err != "" {
		out = append(out, sBad.Render(clip("  ! "+f.err, w)))
	}
	for len(out) < rows {
		out = append(out, "")
	}
	if len(out) > rows {
		out = out[:rows]
	}
	for i := range out {
		out[i] = clip(out[i], w)
	}
	return out
}

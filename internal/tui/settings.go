package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

type settingsItem struct {
	section int
	kind    string
	index   int
	text    string
}

func settingsAppRow(width int, fields []string) string {
	headers := []string{"NAME", "KIND", "CLIENT", "RETURN HOST", "CONNECTED", "LAST USED"}
	widths := []int{16, 9, 12, 20, 12, 9}
	total := 2 + 2*(len(widths)-1)
	for _, n := range widths {
		total += n
	}
	for total > width {
		largest := -1
		for i, n := range widths {
			if n > len(headers[i]) && (largest < 0 || n > widths[largest]) {
				largest = i
			}
		}
		if largest < 0 {
			break
		}
		widths[largest]--
		total--
	}
	parts := make([]string, len(widths))
	for i := range widths {
		parts[i] = cellDots(fields[i], widths[i])
	}
	return strings.Join(parts, "  ")
}

func (m Model) settingsItems() []settingsItem {
	state := "off"
	narrowURLs := false
	base := ""
	if m.apps.public.On {
		state = "on"
		if m.apps.public.URL != "" {
			base = strings.TrimRight(m.apps.public.URL, "/")
			narrowURLs = m.width > 0 && m.width < 100
			if narrowURLs {
				// The two addresses do not fit beside the toggle at 80
				// columns. They sit under it as notes instead.
			} else {
				state += "   phone " + base + "/   bots " + base + "/mcp"
			}
		}
	}
	items := []settingsItem{{0, "public", 0, "Internet access: " + state}}
	if narrowURLs {
		items = append(items,
			settingsItem{0, "public-phone", 0, "  phone  " + base + "/"},
			settingsItem{0, "public-bots", 0, "  bots   " + base + "/mcp"})
	}
	if !m.apps.public.On {
		items = append(items, settingsItem{0, "url", 0, "Use your own tunnel URL"})
	} else if m.apps.public.Port == 0 && m.apps.public.URL != "" {
		items = append(items, settingsItem{0, "url", 0, "Tunnel URL: " + m.apps.public.URL})
	}
	items = append(items, settingsItem{0, "add", 0, "Add an app"})
	for i, a := range m.apps.apps {
		kind := a.Kind
		if kind == "owner_app" {
			kind = "owner app"
		}
		host := "-"
		if len(a.RedirectURIs) > 0 {
			if u, err := url.Parse(a.RedirectURIs[0]); err == nil {
				host = u.Hostname()
			}
		}
		used := "never"
		if a.LastUsedAt != nil {
			used = m.appsDate(*a.LastUsedAt)
		}
		items = append(items, settingsItem{0, "app", i, settingsAppRow(max(m.width, 40), []string{a.Name, kind, a.ClientName, host, m.appsDate(a.CreatedAt), used})})
	}
	items = append(items, settingsItem{1, "integrations", 0, "Configure integrations"})
	if len(m.machineRows()) == 0 {
		items = append(items, settingsItem{2, "no machines", 0, "No machines yet"})
	}
	for i, n := range m.machineRows() {
		host := n.host
		for _, node := range m.snap.Nodes {
			if node.NodeID == n.nodeID {
				host = firstNonEmpty(node.Reported, node.Hostname)
				break
			}
		}
		online := "no"
		if n.online {
			online = "yes"
		}
		items = append(items, settingsItem{2, "machine", i, fmt.Sprintf("%-20s  %-20s  %-12s  %s", cellDots(n.host, 20), cellDots(host, 20), cellDots(dashIfEmpty(n.version), 12), online)})
	}
	notifications := "on"
	if m.snap.NotificationsDisabled {
		notifications = "off"
	}
	tips := "on"
	if m.onboarding.Disabled {
		tips = "off"
	}
	items = append(items,
		settingsItem{3, "timezone", 0, m.timeZoneLine()},
		settingsItem{4, "notifications", 0, "Desktop notifications: " + notifications},
		settingsItem{5, "tips", 0, "Tips: " + tips},
		settingsItem{5, "restart", 0, "Restart tips"})
	scope := "network"
	if m.settingRepo != "" {
		scope = "repo " + m.settingRepo
	}
	items = append(items, settingsItem{6, "setting-repo", 0, "Show settings for: " + scope})
	if len(m.settingValues) == 0 {
		items = append(items, settingsItem{6, "settings-empty", 0, "Settings unavailable or loading"})
	}
	for i, v := range m.settingValues {
		label := v.Key + ": " + sanitize(string(v.Value)) + " (" + settingSource(v) + ")"
		if v.Type == "pipeline_view" {
			label = v.Key + ": resolved steps (" + settingSource(v) + ")"
		}
		if !v.Live {
			label += " [not live yet]"
		}
		items = append(items, settingsItem{6, "setting-value", i, label})
	}
	return items
}

func (m Model) settingsKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.apps.stage == "rename" {
		next, cmd := m.appsKey(k)
		return next, cmd
	}
	items := m.settingsItems()
	m.settingsRow = min(max(m.settingsRow, 0), len(items)-1)
	item := items[m.settingsRow]
	switch keyAction(contextSettings, k.String()) {
	case actQuit:
		m.quitting = true
		return m, tea.Quit
	case "help":
		m.openHelp()
		return m, nil
	case actUp:
		m.settingsRow = settingsStep(items, m.settingsRow, -1)
	case actDown:
		m.settingsRow = settingsStep(items, m.settingsRow, 1)
	case "next section", "previous section":
		step := 1
		if keyAction(contextSettings, k.String()) == "previous section" {
			step = -1
		}
		section := (item.section + step + 7) % 7
		if i, ok := settingsSection(items, section); ok {
			m.settingsRow = i
		}
	case "rotate token":
		if item.kind == "machine" {
			n := m.machineRows()[item.index]
			if !n.online {
				m.fail("Machine is offline; offline machines can only be removed, not rotated.")
				return m, nil
			}
			m.mode = modeConfirm
			m.confirmMachine = protocol.OpMachineRotate
			m.confirmID = n.nodeID
			m.confirmName = n.host
		}
		return m, nil
	case "public", "url", "add", "revoke":
		action := keyAction(contextSettings, k.String())
		if action == "url" && item.kind == "setting-value" {
			row := m.settingValues[item.index]
			if len(row.History) == 0 {
				m.fail("No settings history to undo.")
				return m, nil
			}
			m.settingUndoRevision = row.History[0].Revision
			m.mode = modeSettingUndo
			return m, nil
		}
		if action == "revoke" && item.kind == "machine" {
			n := m.machineRows()[item.index]
			m.mode = modeConfirm
			m.confirmMachine = protocol.OpMachineRemove
			m.confirmID = n.nodeID
			m.confirmName = n.host
			return m, nil
		}
		if item.section != 0 || (action == "revoke" && item.kind != "app") {
			return m, nil
		}
		if item.kind == "app" {
			m.apps.index = item.index
		}
		return m.settingsAppsKey(k)
	case "open", "toggle":
		open := keyAction(contextSettings, k.String()) == "open"
		switch item.kind {
		case "setting-repo":
			if open {
				m.mode = modeSettingRepo
				m.rename = newTextInput()
				m.rename.Prompt = "repo (empty for network)> "
				m.rename.SetValue(m.settingRepo)
				return m, m.rename.Focus()
			}
		case "setting-value":
			if open {
				return m.startSettingValueEdit(item.index)
			}
		case "public":
			return m.settingsAppsKey(keyRunesForSettings("p"))
		case "url":
			if open {
				return m.settingsAppsKey(keyRunesForSettings("u"))
			}
		case "add":
			if open {
				return m.settingsAppsKey(keyRunesForSettings("a"))
			}
		case "app":
			if open {
				m.apps.index = item.index
				return m.appsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			}
		case "integrations":
			if open {
				return m.startIntegrations()
			}
		case "machine":
			if open {
				n := m.machineRows()[item.index]
				return m.startRenameMachine(n.nodeID, n.host)
			}
		case "timezone":
			if open {
				return m.startTimeZoneEdit()
			}
		case "notifications":
			src, ok := m.src.(interface {
				NotificationSettings(context.Context, *bool) (bool, error)
			})
			if !ok {
				m.fail("Desktop notification settings are unavailable.")
				return m, nil
			}
			disabled := !m.snap.NotificationsDisabled
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), Timeout)
				defer cancel()
				value, err := src.NotificationSettings(ctx, &disabled)
				return notificationSettingMsg{disabled: value, err: err}
			}
		case "tips":
			disabled := !m.onboarding.Disabled
			return m.onboardingWrite("onboarding_set", protocol.OnboardingUpdate{Disabled: &disabled})
		case "restart":
			if open {
				return m.onboardingWrite("onboarding_restart", nil)
			}
		}
	}
	return m, nil
}

func keyRunesForSettings(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Text: s, Code: []rune(s)[0]}
}

// settingsSelectable hides the URL notes under the internet toggle from the
// row cursor: they are text, not actions.
func settingsSelectable(item settingsItem) bool {
	return item.kind != "public-phone" && item.kind != "public-bots"
}

// settingsSection finds the first selectable row of a section.
func settingsSection(items []settingsItem, section int) (int, bool) {
	for i, item := range items {
		if item.section == section && settingsSelectable(item) {
			return i, true
		}
	}
	return 0, false
}

// settingsStep moves the row cursor to the next or previous selectable row.
func settingsStep(items []settingsItem, row, step int) int {
	row = min(max(row, 0), len(items)-1)
	for next := row + step; next >= 0 && next < len(items); next += step {
		if settingsSelectable(items[next]) {
			return next
		}
	}
	return row
}

// startTimeZoneEdit asks for the display time zone: auto, or a name the zone
// database knows.
func (m Model) startTimeZoneEdit() (Model, tea.Cmd) {
	if _, ok := m.src.(interface {
		TimeZoneSettings(context.Context, *string) (protocol.TimeZoneSettings, error)
	}); !ok {
		m.fail("The time zone setting is unavailable.")
		return m, nil
	}
	m.mode = modeTimeZone
	m.rename = newTextInput()
	m.rename.Prompt = "time zone (auto or a name like Europe/Paris)> "
	m.rename.CharLimit = 64
	m.rename.SetVirtualCursor(false)
	shown := m.snap.TimeZone
	if shown == "" {
		shown = protocol.TimeZoneAuto
	}
	m.rename.SetValue(shown)
	m.rename.CursorEnd()
	m.notice = notice{}
	return m, m.rename.Focus()
}

func (m Model) timeZoneKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch keyAction(contextForm, k.String()) {
	case "cancel":
		m.mode = modeNormal
		m.say("Time zone unchanged.")
		return m, nil
	case "submit":
		name, err := protocol.NormalizeTimeZone(m.rename.Value())
		if err != nil {
			m.fail(err.Error())
			return m, nil
		}
		if name == m.snap.TimeZone || name == protocol.TimeZoneAuto && m.snap.TimeZone == "" {
			m.mode = modeNormal
			m.say("Time zone unchanged.")
			return m, nil
		}
		m.mode = modeNormal
		m.say("Saving the time zone...")
		src, ok := m.src.(interface {
			TimeZoneSettings(context.Context, *string) (protocol.TimeZoneSettings, error)
		})
		if !ok {
			m.fail("The time zone setting is unavailable.")
			return m, nil
		}
		write := name
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			out, err := src.TimeZoneSettings(ctx, &write)
			if err != nil {
				return actionMsg{err: fmt.Errorf("Time zone not changed: %s", explain(err))}
			}
			if out.Name == protocol.TimeZoneAuto {
				return actionMsg{text: "Time zone is auto: the zone of your own machine."}
			}
			return actionMsg{text: "Time zone set to " + sanitize(out.Name) + ", for every machine."}
		}
	}
	var cmd tea.Cmd
	m.rename, cmd = m.rename.Update(k)
	return m, cmd
}

func (m Model) settingsAppsKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	next, cmd := m.appsKey(k)
	m = next.(Model)
	if m.apps.stage != "" && m.apps.stage != "rename" {
		m.mode = modeApps
	}
	return m, cmd
}

// otherRoutePorts accepts the port list sent by the gateway. A structured
// route from a newer gateway can also provide a port field.
func otherRoutePorts(raw json.RawMessage) []string {
	var routes []json.RawMessage
	if json.Unmarshal(raw, &routes) != nil {
		return nil
	}
	var ports []string
	for _, route := range routes {
		var port int
		if json.Unmarshal(route, &port) == nil && port > 0 {
			ports = append(ports, strconv.Itoa(port))
			continue
		}
		var text string
		if json.Unmarshal(route, &text) == nil && text != "" {
			ports = append(ports, sanitize(text))
			continue
		}
		var object struct {
			Port int `json:"port"`
		}
		if json.Unmarshal(route, &object) == nil && object.Port > 0 {
			ports = append(ports, strconv.Itoa(object.Port))
		}
	}
	sort.SliceStable(ports, func(i, j int) bool {
		a, errA := strconv.Atoi(ports[i])
		b, errB := strconv.Atoi(ports[j])
		if errA == nil && errB == nil {
			return a < b
		}
		if errA == nil {
			return true
		}
		if errB == nil {
			return false
		}
		return ports[i] < ports[j]
	})
	return ports
}

func otherRoutesWarning(raw json.RawMessage) string {
	ports := otherRoutePorts(raw)
	if len(ports) == 0 {
		return ""
	}
	label := "ports"
	if len(ports) == 1 {
		label = "port"
	}
	return "Other services are served on this machine's Tailscale name (" + label + " " + strings.Join(ports, ", ") + "). They receive the phone's sign-in cookie. Remove them or accept the risk."
}

func (m Model) settingsView(w, h int) []string {
	out := []string{m.titleLine(w)}
	switch m.mode {
	case modeIntegrations:
		return append(out, m.integrationsView(w, max(h-1, 1))...)
	case modeApps:
		return append(out, m.appsView(w, max(h-1, 1))...)
	}
	names := []string{"Connected apps", "Integrations", "Machines", "Time zone", "Notifications", "Tips", "Working rules and guidance"}
	descriptions := []string{"Control internet access and the apps connected to your network.", "Choose integrations for every machine in your network.", "The name every machine in your network shows for each machine.", "The clock every time on this screen uses, wherever you run it.", "Choose whether this machine shows desktop notifications.", "Show or restart the onboarding tips.", "Change network values and review their history."}
	items := m.settingsItems()
	selected := min(max(m.settingsRow, 0), len(items)-1)
	if !settingsSelectable(items[selected]) {
		selected = settingsStep(items, selected, 1)
		if !settingsSelectable(items[selected]) {
			selected = settingsStep(items, selected, -1)
		}
	}
	if items[selected].section == 6 {
		old, unknown := m.settingsSyncMachines()
		if len(old) > 0 {
			for _, line := range wrap("Settings updates may not reach old daemons: "+strings.Join(old, ", "), max(w-2, 1)) {
				out = append(out, lipgloss.NewStyle().Foreground(roleWarning).Render(clip("  "+line, w)))
			}
		}
		if len(unknown) > 0 {
			for _, line := range wrap("Settings sync support unknown: "+strings.Join(unknown, ", "), max(w-2, 1)) {
				out = append(out, lipgloss.NewStyle().Foreground(roleWarning).Render(clip("  "+line, w)))
			}
		}
	}
	var body []string
	selectedLine := 0
	selectedEnd := 0
	section := -1
	for i, item := range items {
		if item.section != section {
			if section >= 0 {
				body = append(body, "")
			}
			section = item.section
			body = append(body, sBold.Render(names[section]), sDim.Render(clip("  "+descriptions[section], w)))
			if section == 0 {
				if warning := otherRoutesWarning(m.apps.public.OtherRoutes); warning != "" {
					for _, line := range wrap(warning, w-2) {
						body = append(body, lipgloss.NewStyle().Foreground(roleWarning).Render("  "+line))
					}
				}
				if m.apps.public.Error != "" {
					for _, line := range wrap(sanitize(m.apps.public.Error), w-2) {
						body = append(body, lipgloss.NewStyle().Foreground(roleWarning).Render("  "+line))
					}
				}
			}
			if section == 2 {
				body = append(body, sDim.Render(clip(fmt.Sprintf("  %-20s  %-20s  %-12s  %s", "NAME", "HOST NAME", "VERSION", "ONLINE"), w)))
			}
		}
		if item.kind == "app" && item.index == 0 {
			body = append(body, sDim.Render(clip("  "+settingsAppRow(w, []string{"NAME", "KIND", "CLIENT", "RETURN HOST", "CONNECTED", "LAST USED"}), w)))
		}
		if i == selected {
			selectedLine = len(body)
		}
		line := "  " + item.text
		if i == selected {
			line = selectedRow(clip("▸ "+item.text, w))
		}
		body = append(body, clip(line, w))
		if item.kind == "setting-value" && i == selected {
			v := m.settingValues[item.index]
			body = append(body, sDim.Render(clip("    "+v.Description, w)))
			if len(v.History) == 0 {
				body = append(body, sDim.Render("    No changes yet"))
			}
			for _, history := range v.History {
				body = append(body, sDim.Render(clip(fmt.Sprintf("    #%d %s %s %s: %s -> %s (%s)", history.Revision, history.Actor, history.Level, history.Scope, settingHistoryValue(history.Old), settingHistoryValue(history.New), sanitize(history.Reason)), w)))
			}
		}

		if i == selected {
			selectedEnd = len(body) - 1
		}
		if item.kind == "app" && m.apps.stage == "rename" && item.index == m.apps.index {
			in := m.apps.input
			in.Prompt = ""
			body = append(body, clip("    name for "+sanitize(m.apps.apps[item.index].Name)+"> "+in.View(), w))
			if m.apps.err != "" {
				body = append(body, sBad.Render(clip("    "+m.apps.err, w)))
			}
		}
	}
	if m.apps.loading {
		body = append(body, sDim.Render("  Loading connected apps..."))
	}
	if m.apps.err != "" {
		body = append(body, lipgloss.NewStyle().Foreground(roleWarning).Render(clip("  "+sanitize(m.apps.err), w)))
	}
	room := max(h-len(out), 1)
	start := 0
	if selectedEnd >= room {
		start = min(selectedLine, selectedEnd-room+1)
	}
	if start > 0 && start+room > len(body) {
		start = max(len(body)-room, 0)
	}
	out = append(out, body[start:min(len(body), start+room)]...)
	for len(out) < h {
		out = append(out, "")
	}
	for i := range out {
		out[i] = ansi.Truncate(out[i], w, "")
	}
	return out
}

type settingValuesMsg struct {
	rows   []protocol.SettingView
	notice string
	err    error
}

func settingHistoryValue(v json.RawMessage) string {
	if v == nil {
		return "inherited"
	}
	return sanitize(string(v))
}
func settingSource(v protocol.SettingView) string {
	if v.Source == "network" {
		if strings.HasPrefix(v.Actor, "owner") {
			return "set by you"
		}
		if strings.HasPrefix(strings.ToLower(v.Actor), "cto") {
			return "CTO"
		}
	}
	return v.Source
}
func (m Model) loadSettingValues() tea.Cmd {
	src, ok := m.src.(inboxCaller)
	repo := m.settingRepo
	return func() tea.Msg {
		if !ok {
			return settingValuesMsg{err: fmt.Errorf("unavailable")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		raw, err := src.Call(ctx, "settings_owner_read", protocol.SettingsReadRequest{Repo: repo})
		if err != nil {
			return settingValuesMsg{err: err}
		}
		var rows []protocol.SettingView
		if err = json.Unmarshal(raw, &rows); err != nil {
			return settingValuesMsg{err: err}
		}
		if repo != "" {
			raw, err = src.Call(ctx, protocol.OpGraphGet, map[string]any{})
			if err != nil {
				return settingValuesMsg{err: err}
			}
			var graph protocol.Graph
			if err = json.Unmarshal(raw, &graph); err != nil {
				return settingValuesMsg{err: err}
			}
			rows = append(rows, deliveryOwnerViews(graph, repo)...)
		}
		return settingValuesMsg{rows: rows}
	}
}

// deliveryOwnerViews adds the graph-backed assignment controls for a repo.
// An empty explicit node returns to the step's default owner.
func deliveryOwnerViews(graph protocol.Graph, repo string) []protocol.SettingView {
	var rows []protocol.SettingView
	refs := []protocol.StepRef{{Step: protocol.StepAccept}, {Step: protocol.StepReview}, {Step: protocol.StepTest},
		{Step: protocol.StepTest, AfterMerge: true}, {Step: protocol.StepApprove, For: protocol.StepMerge},
		{Step: protocol.StepApprove, For: protocol.StepDeploy, AfterMerge: true}, {Step: protocol.StepMerge},
		{Step: protocol.StepRelease, AfterMerge: true}, {Step: protocol.StepDeploy, AfterMerge: true}, {Step: protocol.StepVerify, AfterMerge: true}}
	for _, ref := range refs {
		step := ref.Step
		kind := protocol.DeliveryRefEdge(step, ref.For, ref.AfterMerge)
		owner, defaultDeploy := "", ""
		for _, edge := range graph.Edges {
			if edge.To != repo {
				continue
			}
			if edge.Kind == kind {
				owner = protocol.DeliveryOwnerSetting(edge.From)
			}
			if step == protocol.StepDeploy && edge.Kind == "deploys" && defaultDeploy == "" {
				defaultDeploy = edge.From
			}
		}
		source := "caller by default"
		if step == protocol.StepApprove {
			source = "approval rule by default"
		} else if defaultDeploy != "" {
			source = "default deployer " + defaultDeploy
		}
		if owner != "" {
			source = "graph assignment"
		}
		value, _ := json.Marshal(owner)
		description := "Enter a node name or @caller, @owner, @github, @delegate or @none as a JSON string; empty uses the default."
		if step == protocol.StepDeploy {
			description += " Takes effect when the pipeline has a deploy step."
		}
		if step == protocol.StepApprove {
			description += " A written or delegated approval rule keeps its approver."
		}
		key := "delivery.owner." + step
		if step == protocol.StepTest && ref.AfterMerge {
			key += ".after_merge"
		} else if step == protocol.StepApprove && ref.For == protocol.StepDeploy {
			key += ".deploy"
		}
		rows = append(rows, protocol.SettingView{Key: key, Type: "delivery_owner", Kind: "rule", Live: true, DeliveryStep: &ref,
			Description: description,
			Value:       value, Explicit: value, Source: source})
	}
	return rows
}
func (m Model) startSettingValueEdit(i int) (Model, tea.Cmd) {
	if m.settingValues[i].Type == "pipeline_view" {
		m.fail("Edit the pipeline rule settings and owner rows below.")
		return m, nil
	}
	if _, ok := m.src.(inboxCaller); !ok {
		m.fail("Settings unavailable.")
		return m, nil
	}
	m.settingEdit = i
	m.mode = modeSettingValue
	m.rename = newTextInput()
	m.rename.Prompt = "JSON value> "
	m.rename.CharLimit = 0
	m.rename.SetValue(string(m.settingValues[i].Value))
	m.rename.CursorEnd()
	return m, m.rename.Focus()
}
func (m Model) settingValueKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch keyAction(contextForm, k.String()) {
	case "cancel":
		m.mode = modeNormal
		return m, nil
	case "submit":
		value := json.RawMessage(strings.TrimSpace(m.rename.Value()))
		if !json.Valid(value) || string(value) == "null" {
			m.fail("Enter a JSON value.")
			return m, nil
		}
		row := m.settingValues[m.settingEdit]
		src, ok := m.src.(inboxCaller)
		if !ok {
			m.fail("Settings unavailable.")
			return m, nil
		}
		if row.Type == "delivery_owner" {
			var node, previous string
			if json.Unmarshal(value, &node) != nil || json.Unmarshal(row.Explicit, &previous) != nil {
				m.fail("Enter the node name as a JSON string, or an empty string for the default.")
				return m, nil
			}
			m.mode = modeNormal
			if row.DeliveryStep == nil {
				m.fail("The delivery step is unavailable; reload Settings.")
				return m, nil
			}
			q := protocol.DeliveryOwnerSetRequest{Project: m.settingRepo, Step: row.DeliveryStep.Step, For: row.DeliveryStep.For, AfterMerge: row.DeliveryStep.AfterMerge, Node: node, Expect: previous}
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), Timeout)
				defer cancel()
				if _, err := src.Call(ctx, protocol.OpDeliveryOwnerSet, q); err != nil {
					return settingValuesMsg{err: err}
				}
				return settingValuesMsg{notice: "Saved " + row.Key + "."}
			}
		}
		m.mode = modeNormal
		level, scope := "network", "network"
		if m.settingRepo != "" {
			for _, l := range row.Levels {
				if l == "repo" {
					level, scope = "repo", m.settingRepo
					break
				}
			}
		}
		q := protocol.SettingsWrite{Changes: []protocol.SettingsChange{{Key: row.Key, Level: level, Scope: scope, Value: value, Expect: row.Explicit}}, Reason: "Changed in Settings"}
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()
			raw, err := src.Call(ctx, "settings_write", q)
			if err != nil {
				return settingValuesMsg{err: err}
			}
			var result protocol.SettingsResult
			if err = json.Unmarshal(raw, &result); err != nil {
				return settingValuesMsg{err: err}
			}
			notice := fmt.Sprintf("Saved %s (revision %d).", row.Key, result.Revision)
			for _, move := range result.Moved {
				notice += fmt.Sprintf(" Repo %s: %s -> %s.", move.Scope, move.Old, move.New)
			}
			return settingValuesMsg{notice: notice}
		}
	}
	var cmd tea.Cmd
	m.rename, cmd = m.rename.Update(k)
	return m, cmd
}

func (m Model) settingRepoKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch keyAction(contextForm, k.String()) {
	case "cancel":
		m.mode = modeNormal
		return m, nil
	case "submit":
		m.settingRepo = strings.TrimSpace(m.rename.Value())
		m.settingValues = nil
		m.mode = modeNormal
		return m, m.loadSettingValues()
	}
	var cmd tea.Cmd
	m.rename, cmd = m.rename.Update(k)
	return m, cmd
}

func (m Model) settingUndoKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch keyAction(contextForm, k.String()) {
	case "cancel":
		m.mode = modeNormal
		return m, nil
	case "submit":
		src, ok := m.src.(inboxCaller)
		if !ok {
			m.mode = modeNormal
			m.fail("Settings unavailable.")
			return m, nil
		}
		rev := m.settingUndoRevision
		m.mode = modeNormal
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()
			raw, err := src.Call(ctx, "settings_undo", protocol.SettingsUndo{Revision: rev})
			if err != nil {
				return settingValuesMsg{err: err}
			}
			var result protocol.SettingsResult
			if err = json.Unmarshal(raw, &result); err != nil {
				return settingValuesMsg{err: err}
			}
			notice := fmt.Sprintf("Undid revision %d as revision %d.", rev, result.Revision)
			for _, move := range result.Moved {
				notice += fmt.Sprintf(" Repo %s: %s -> %s.", move.Scope, move.Old, move.New)
			}
			return settingValuesMsg{notice: notice}
		}
	}
	return m, nil
}

// The settings snapshot first shipped in v0.4.117. Unparseable and development
// versions do not prove support and are shown separately from older releases.
func settingsSyncVersion(v string) (old, unknown bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" || v == "dev" {
		return false, true
	}
	base := v
	pre := false
	if i := strings.IndexByte(base, '+'); i >= 0 {
		base = base[:i]
	}
	if i := strings.IndexByte(base, '-'); i >= 0 {
		base = base[:i]
		pre = true
	}
	parts := strings.Split(base, ".")
	if len(parts) != 3 {
		return false, true
	}
	numbers := [3]int{}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || part == "" {
			return false, true
		}
		numbers[i] = n
	}
	first := [3]int{0, 4, 117}
	for i := range first {
		if numbers[i] != first[i] {
			return numbers[i] < first[i], false
		}
	}
	return false, pre
}
func (m Model) settingsSyncMachines() (old, unknown []string) {
	for _, n := range m.snap.Nodes {
		if !n.Online {
			continue
		}
		older, unsure := settingsSyncVersion(n.Version)
		name := sanitize(firstNonEmpty(n.Hostname, firstNonEmpty(n.Reported, n.NodeID)))
		if older {
			old = append(old, name)
		} else if unsure {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(old)
	sort.Strings(unknown)
	return old, unknown
}

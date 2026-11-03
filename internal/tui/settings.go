package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	if m.apps.public.On {
		state = "on"
		if m.apps.public.URL != "" {
			base := strings.TrimRight(m.apps.public.URL, "/")
			state += "   phone " + base + "/   bots " + base + "/mcp"
		}
	}
	items := []settingsItem{{0, "public", 0, "Internet access: " + state}}
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
	return append(items, settingsItem{3, "notifications", 0, "Desktop notifications: " + notifications}, settingsItem{4, "tips", 0, "Tips: " + tips}, settingsItem{4, "restart", 0, "Restart tips"})
}

func (m Model) settingsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		m.settingsRow = max(0, m.settingsRow-1)
	case actDown:
		m.settingsRow = min(len(items)-1, m.settingsRow+1)
	case "next section", "previous section":
		step := 1
		if keyAction(contextSettings, k.String()) == "previous section" {
			step = -1
		}
		section := (item.section + step + 5) % 5
		for i, row := range items {
			if row.section == section {
				m.settingsRow = i
				break
			}
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
				return m.appsKey(tea.KeyMsg{Type: tea.KeyEnter})
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

func keyRunesForSettings(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func (m Model) settingsAppsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	names := []string{"Connected apps", "Integrations", "Machines", "Notifications", "Tips"}
	descriptions := []string{"Control internet access and the apps connected to your network.", "Choose integrations for every machine in your network.", "The name every machine in your network shows for each machine.", "Choose whether this machine shows desktop notifications.", "Show or restart the onboarding tips."}
	items := m.settingsItems()
	selected := min(max(m.settingsRow, 0), len(items)-1)
	var body []string
	selectedLine := 0
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
	room := max(h-1, 1)
	start := 0
	if selectedLine >= room {
		start = selectedLine - room + 1
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

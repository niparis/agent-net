package tui

import (
	"fmt"
	"strings"
)

// Tools keeps every local binding visible instead of using the seven-hint
// shortlist shared by the other tabs. Equal labels share one group of keys.
func toolsBindingParts(ctx keyContext, include func(keyBinding) bool) []string {
	var parts []string
	positions := map[string]int{}
	for _, b := range keyBindings {
		if b.context != ctx || b.action == actQuit || include != nil && !include(b) {
			continue
		}
		keys := strings.Join(b.keys, "/")
		if i, ok := positions[b.label]; ok {
			parts[i] += "/" + keys
			continue
		}
		positions[b.label] = len(parts)
		parts = append(parts, keys, b.label)
	}
	return parts
}

func (m Model) toolsKeyLines(w int) []string {
	var parts []string
	switch {
	case m.tl.agentDiscard:
		parts = toolsBindingParts(contextToolsDiscard, nil)
	case m.tl.agentSearching || m.tl.searching:
		parts = toolsBindingParts(contextToolsFilter, nil)
		parts = append(parts, "text", "filter")
	case m.tl.modal != nil:
		md := m.tl.modal
		switch {
		case md.menu != nil:
			parts = toolsBindingParts(contextToolsMenu, nil)
		case md.form != nil:
			parts = toolsBindingParts(contextToolsAdd, nil)
			parts = append(parts, "text", "type")
		case md.picker != nil && !md.loading:
			if md.picker.filtering {
				parts = toolsBindingParts(contextToolsFilter, nil)
				parts = append(parts, "text", "filter")
			} else {
				parts = toolsBindingParts(contextToolsPicker, nil)
			}
		default:
			parts = toolsBindingParts(contextToolsPlan, func(b keyBinding) bool {
				if md.loading {
					return b.action == "help"
				} // Esc can close a loading preview.
				if b.action == "exact folders" {
					return len(md.paths) > 0
				}
				if b.action == "apply" {
					_, ok := md.actions["enter"]
					return ok || md.done
				}
				return true
			})
			if md.loading {
				parts = append(parts, "esc", "cancel")
			}
			// Use the current plan's verb (import, apply, close or back).
			for i := 0; i+1 < len(parts); i += 2 {
				for j := 0; j+1 < len(md.foot); j += 2 {
					if strings.HasPrefix(parts[i], "enter/") && strings.HasPrefix(md.foot[j], "enter") || strings.HasPrefix(parts[i], "esc/") && strings.HasPrefix(md.foot[j], "esc") {
						parts[i+1] = md.foot[j+1]
					}
				}
			}
			// Compare and other plans can add actions such as a/b to keep a copy.
			for i := 0; i+1 < len(md.foot); i += 2 {
				key := md.foot[i]
				if key == "enter" || strings.Contains(key, "enter/") || key == "esc" || strings.HasPrefix(key, "esc/") || (key == "d" && md.actions[key] == nil) || key == "j/k" || key == "up/down" {
					continue
				}
				if _, ok := md.actions[key]; ok {
					parts = append(parts, key, md.foot[i+1])
				}
			}
		}
	case m.tl.projectsOpen && m.tl.allTools:
		parts = toolsBindingParts(contextToolsCosts, nil)
	case !m.tl.allTools:
		parts = toolsBindingParts(m.agentToolsContext(), nil)
		if m.tl.skillsFocus {
			for i := 0; i+1 < len(parts); i += 2 {
				if parts[i] == "enter" {
					parts[i+1] = fmt.Sprintf("review and apply (%d)", len(m.tl.agentPending[m.agentProject()]))
				}
			}
		}
	default:
		parts = toolsBindingParts(contextTools, nil)
	}
	lines := wrapHints(parts, w)
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

package tui

import "github.com/niparis/agent-net/internal/protocol"

// A skill, a prompt (Claude Code slash command) and an agent (subagent) can
// share a name. The project view keys its rows and pending changes by kind and
// name, so they never mix. A skill's key is its plain name.
func rowKey(kind, name string) string {
	if kind == "" || kind == protocol.InventorySkill {
		return name
	}
	return kind + ":" + name
}

// rowLabel is how the project view names a row: a prompt as the command the
// owner types, an agent with its kind.
func rowLabel(kind, name string) string {
	switch kind {
	case protocol.InventoryPrompt:
		return "/" + name
	case protocol.InventoryAgent:
		return name + " (agent)"
	}
	return name
}

// skillFileOf is the file to read for an item: SKILL.md of a skill, or the
// one Markdown file of a prompt or an agent.
func skillFileOf(kind, name string) string {
	if protocol.IsFileKind(kind) {
		return protocol.BundleFileName(name)
	}
	return "SKILL.md"
}

// kindRank orders the rows of the project view: skills, prompts, agents.
func kindRank(kind string) int {
	switch kind {
	case protocol.InventoryPrompt:
		return 1
	case protocol.InventoryAgent:
		return 2
	}
	return 0
}

package protocol

import "strings"

// Kinds of managed items that travel as a bundle through the skill store.
// A skill is a folder. A prompt (a Claude Code slash command) and an agent
// (a Claude Code subagent) are one Markdown file each.
const (
	InventoryPrompt = "prompt"
	InventoryAgent  = "agent"
)

// InventoryBundles is a tools filter value that selects every kind that is
// stored and switched on per project: skills, prompts and agents.
const InventoryBundles = "bundles"

// KindMatches says whether an item of kind matches the Kind of a tools filter.
func KindMatches(filter, kind string) bool {
	return filter == "" || filter == kind || (filter == InventoryBundles && IsBundleKind(kind))
}

// IsBundleKind says whether items of this kind are stored, versioned and
// switched on per project like skills.
func IsBundleKind(kind string) bool {
	return kind == InventorySkill || kind == InventoryPrompt || kind == InventoryAgent
}

// IsFileKind says whether an item of this kind is one Markdown file instead of
// a folder.
func IsFileKind(kind string) bool {
	return kind == InventoryPrompt || kind == InventoryAgent
}

// BundleFileName is the name of the single file inside the bundle of a file
// kind. It is also the file name in the harness folder.
func BundleFileName(name string) string { return name + ".md" }

// BundleEntryName is the name of the entry a copy has in its harness folder:
// the folder of a skill, the Markdown file of a prompt or an agent.
func BundleEntryName(kind, name string) string {
	if IsFileKind(kind) {
		return BundleFileName(name)
	}
	return name
}

// FileKindRoots are the folders, relative to the user folder or a project
// checkout, that hold items of a file kind for each harness. Only Claude Code
// is supported so far.
func FileKindRoots(kind string) []string {
	switch kind {
	case InventoryPrompt:
		return []string{".claude/commands"}
	case InventoryAgent:
		return []string{".claude/agents"}
	}
	return nil
}

// FileKindName returns the item name for a Markdown file name, or "" when the
// file is not a candidate.
func FileKindName(file string) string {
	if !strings.HasSuffix(file, ".md") || strings.HasPrefix(file, ".") {
		return ""
	}
	name := strings.TrimSuffix(file, ".md")
	if name == "" || strings.ContainsAny(name, "/\\\x00\r\n") {
		return ""
	}
	return name
}

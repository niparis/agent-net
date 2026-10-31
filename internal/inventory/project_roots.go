package inventory

// ProjectSkillRoot chooses one native or shared compatibility directory for
// each harness. A conflict means the installed harnesses would see two copies.
func ProjectSkillRoot(harness string, installed []string) (string, bool) {
	has := func(name string) bool {
		for _, v := range installed {
			if v == name {
				return true
			}
		}
		return false
	}
	if has("opencode") && has("claude") && (has("codex") || has("pi")) {
		return "", true
	}
	switch harness {
	case "claude":
		return ".claude/skills", false
	case "codex":
		return ".agents/skills", false
	case "opencode":
		if has("codex") || has("pi") {
			return ".agents/skills", false
		}
		if has("claude") {
			return ".claude/skills", false
		}
		return ".opencode/skills", false
	case "pi":
		if has("codex") || has("opencode") {
			return ".agents/skills", false
		}
		return ".pi/skills", false
	default:
		return "", true
	}
}

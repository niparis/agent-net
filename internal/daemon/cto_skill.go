package daemon

import (
	"path/filepath"

	"github.com/niparis/agent-net/internal/daemon/ctoskill"
)

// ctoSkillDir names the folder a CTO harness loads the setup skill from. A
// Claude CTO runs in <state>/cto/claude, and the private OpenCode server runs
// in <state>/cto. OpenCode 2.3 lists a project skill under .claude/skills from
// its working folder (checked with the /api/skill route of a private server),
// so the same relative path serves both. Pi cannot be the CTO, so no other
// harness has a path.
func ctoSkillDir(stateDir, harness string) (string, bool) {
	switch harness {
	case "claude":
		return filepath.Join(stateDir, "cto", "claude", ".claude", "skills", ctoskill.Name), true
	case "opencode":
		return filepath.Join(stateDir, "cto", ".claude", "skills", ctoskill.Name), true
	}
	return "", false
}

// writeCTOSkill prepares the CTO's own skill. It runs for the two harnesses a
// CTO can use and does nothing for any other session.
func (d *Daemon) writeCTOSkill(harness string) error {
	dir, ok := ctoSkillDir(d.cfg.StateDir, harness)
	if !ok {
		return nil
	}
	return ctoskill.Write(dir)
}

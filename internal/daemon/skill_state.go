package daemon

import (
	"os"
	"path/filepath"
)

// RemoveSkillState deletes the files that the old Tools rollout left in the
// state folder (removed by decision 0169, task 5): its manifest, journal and
// backups. Git exclude lines it added to checkouts stay: they only hide
// folders, and nothing here changes a repo. Call only after the daemon has
// stopped.
func RemoveSkillState(state string) error {
	for _, name := range []string{"skill-manifest.json", "skill-journal", "skill-backups", "mcp-backups"} {
		if err := os.RemoveAll(filepath.Join(state, name)); err != nil {
			return err
		}
	}
	for _, prefix := range []string{".skill-manifest-", ".project-verify-", ".project-ignore-", "mcp-coverage-undo-"} {
		left, _ := filepath.Glob(filepath.Join(state, prefix+"*"))
		for _, p := range left {
			if err := os.RemoveAll(p); err != nil {
				return err
			}
		}
	}
	return nil
}

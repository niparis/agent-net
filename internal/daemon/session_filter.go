package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/temppath"
)

// IgnoreEnv keeps a session off the network. When a session's process has
// AGENT_NET_IGNORE=1, the daemon does not register it: it has no session and
// no project. A session that was registered before is removed.
const IgnoreEnv = "AGENT_NET_IGNORE"

// isJudgeDir reports whether dir or any parent directory is a temporary folder
// created for a difficulty judge.
func isJudgeDir(dir string) bool {
	if dir == "" {
		return false
	}
	clean := filepath.Clean(dir)
	for d := clean; d != "/" && d != "." && d != ""; d = filepath.Dir(d) {
		if strings.HasPrefix(filepath.Base(d), "agent-net-difficulty-") {
			return true
		}
	}
	return false
}

// processCwd returns the working directory of pid if accessible.
func processCwd(pid int) string {
	if cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil {
		return cwd
	}
	return ""
}

// sessionIgnored reads the flag from the session process's environment. A
// process that the daemon cannot read (another user, or gone) is not ignored.
// It also ignores sessions whose process runs in a judge directory.
func (d *Daemon) sessionIgnored(pid int) bool {
	if pid <= 0 {
		return false
	}
	if strings.TrimSpace(processEnvVar(pid, IgnoreEnv)) == "1" {
		return true
	}
	if isJudgeDir(processEnvVar(pid, "PWD")) {
		return true
	}
	if isJudgeDir(processCwd(pid)) {
		return true
	}
	return false
}

// tempFolders is the list of temporary folders. Nil means the system list,
// which follows TMPDIR; an empty list turns the rule off.
func (d *Daemon) tempFolders() []string {
	if d.cfg.TempFolders != nil {
		return d.cfg.TempFolders
	}
	return temppath.Default()
}

// sessionRepo returns the repository info of a session's folder and whether
// the folder is temporary. A temporary folder gets no repository info, so it
// cannot name a project.
func (d *Daemon) sessionRepo(cwd string, fresh bool) (protocol.RepoInfo, bool) {
	if temppath.Under(cwd, d.tempFolders()) {
		return protocol.RepoInfo{}, true
	}
	if fresh {
		return d.repo.Refresh(cwd), false
	}
	return d.repo.Lookup(cwd), false
}

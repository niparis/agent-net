package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/niparis/agent-net/internal/agentfiles"
	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/guidance"
)

// stopWorkers is a variable so a test can look at the call.
var stopWorkers = daemon.StopRecordedWorkers

// Uninstall undoes Setup. It leaves the binary and the gateway data alone.
func Uninstall(o Options) error {
	r, err := newRunner(o)
	if err != nil {
		return err
	}
	if r.NoService {
		r.step("services", "skipped (no service)", nil)
	} else {
		for _, s := range []Service{{Name: "daemon"}, {Name: "gateway"}} {
			res, err := r.removeService(s)
			r.step(s.Name+" service", res, err)
		}
	}
	// Workers outlive the daemon on purpose (a restart keeps them), so with
	// the service gone nobody else would stop them.
	n, err := stopWorkers(daemon.StateDir(), 3*time.Second)
	switch {
	case err != nil:
		r.step("workers", "", err)
	case n == 0:
		r.step("workers", "skipped (none running)", nil)
	default:
		r.step("workers", fmt.Sprintf("ok (stopped %d)", n), nil)
	}
	r.removeClaudeMCP()
	r.removePeerMessages()
	r.removeCodexMCP()
	r.removeHintHook()
	r.removePolicyHook()
	r.removeNeedsHook()
	r.removeClaudeWorkersMod()
	r.removeQuotaAXI()
	r.removePiExtension()
	r.removeOpenCodePlugin()
	r.removeGuidance()
	manifest := filepath.Join(daemon.StateDir(), "skill-manifest.json")
	if err := os.Remove(manifest); err != nil && !os.IsNotExist(err) {
		r.step("skill manifest", "", err)
	}

	if r.Purge {
		for _, dir := range []string{daemon.ConfigDir(), daemon.StateDir()} {
			res := "skipped (not found)"
			if exists(dir) {
				if err := os.RemoveAll(dir); err != nil {
					r.step("purge", "", err)
					continue
				}
				res = "ok (removed)"
			}
			r.step("purge "+displayPath(dir), res, nil)
		}
		if gw := filepath.Join(home(), ".local", "state", "agent-net", "gateway"); exists(gw) {
			r.hint("gateway data is kept in %s (delete it by hand if you want it gone)", displayPath(gw))
		}
	}
	return r.failed
}

func (r *runner) removeClaudeMCP() {
	claude := claudePath()
	if claude == "" {
		r.step("claude code", "skipped (claude not found)", nil)
		return
	}
	if _, err := runCmd(home(), claude, "mcp", "get", "agent-net"); err != nil {
		r.step("claude code", "skipped (not registered)", nil)
		return
	}
	if out, err := runCmd(home(), claude, "mcp", "remove", "-s", "user", "agent-net"); err != nil {
		r.step("claude code", "", errors.New("claude mcp remove: "+firstLine(out, err)))
		return
	}
	r.step("claude code", "ok (removed)", nil)
}

func (r *runner) removePiExtension() {
	path := agentfiles.PiExtensionPath()
	st, err := os.Lstat(path)
	switch {
	case err != nil:
		r.step("pi extension", "skipped (not installed)", nil)
	case st.Mode()&os.ModeSymlink != 0:
		if !agentfiles.SymlinkTargetIsAgentNet(path) {
			r.step("pi extension", "skipped (symlink, left as is)", nil)
			return
		}
		if err := os.Remove(path); err != nil {
			r.step("pi extension", "", err)
			return
		}
		r.step("pi extension", "ok (removed an agent-net symlink)", nil)
	default:
		if err := os.Remove(path); err != nil {
			r.step("pi extension", "", err)
			return
		}
		r.step("pi extension", "ok (removed)", nil)
	}
}

func (r *runner) removeQuotaAXI() {
	removed, err := removeQuotaAXI()
	switch {
	case err != nil:
		r.step(quotaStepName, "", err)
	case removed:
		r.step(quotaStepName, "ok (removed)", nil)
	default:
		r.step(quotaStepName, "skipped (not installed)", nil)
	}
}

func (r *runner) removeOpenCodePlugin() {
	path := agentfiles.OpencodePluginPath()
	st, err := os.Lstat(path)
	switch {
	case err != nil:
		r.step("opencode plugin", "skipped (not installed)", nil)
	case st.Mode()&os.ModeSymlink != 0:
		if !agentfiles.SymlinkTargetIsAgentNet(path) {
			r.step("opencode plugin", "skipped (symlink, left as is)", nil)
			return
		}
		if err := os.Remove(path); err != nil {
			r.step("opencode plugin", "", err)
			return
		}
		r.step("opencode plugin", "ok (removed an agent-net symlink)", nil)
	default:
		if err := os.Remove(path); err != nil {
			r.step("opencode plugin", "", err)
			return
		}
		r.step("opencode plugin", "ok (removed)", nil)
	}
}

func (r *runner) removeGuidance() {
	for _, f := range agentfiles.GuidanceFiles() {
		old, err := os.ReadFile(f.Path)
		if err != nil {
			r.step(f.Name, "skipped (no file)", nil)
			continue
		}
		out, changed := guidance.Remove(string(old))
		if !changed {
			r.step(f.Name, "skipped (no agent-net block)", nil)
			continue
		}
		if err := os.WriteFile(f.Path, []byte(out), 0o644); err != nil {
			r.step(f.Name, "", err)
			continue
		}
		r.step(f.Name, "ok (removed)", nil)
	}
}

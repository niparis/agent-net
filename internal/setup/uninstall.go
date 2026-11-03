package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/agentfiles"
	"github.com/niparis/agent-net/internal/claudetrust"
	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/funnel"
	"github.com/niparis/agent-net/internal/guidance"
	"github.com/niparis/agent-net/internal/quotaaxi"
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
	// Only remove a route whose current target still matches our record.
	data := r.Data
	if data == "" {
		data = os.Getenv("AGENT_NET_GATEWAY_DATA")
	}
	if data == "" {
		data = filepath.Join(home(), ".local", "state", "agent-net", "gateway")
	}
	runner := r.FunnelRunner
	if runner == nil {
		runner = funnel.ExecRunner{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	removed, routeErr := funnel.Cleanup(ctx, runner, data)
	cancel()
	switch {
	case routeErr != nil:
		r.step("Funnel route", "", routeErr)
	case removed:
		r.step("Funnel route", "ok (removed)", nil)
	default:
		r.step("Funnel route", "skipped (no owned route)", nil)
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
	if err := claudetrust.Remove(filepath.Join(daemon.StateDir(), "cto", "claude")); err != nil {
		r.step("Claude CTO trust", "", err)
	}
	r.removeClaudeMCP()
	r.removePeerMessages()
	r.removeCodexMCP()
	r.removeHintHook()
	r.removePolicyHook()
	r.removeNeedsHook()
	r.removeClaudeWorkersMod()
	r.removeQuotaAXI()
	if err := quotaaxi.RemoveAuth(daemon.ConfigDir()); err != nil {
		r.step("quota-axi auth", "", err)
	}
	if err := quotaaxi.RemoveChoice(daemon.ConfigDir()); err != nil {
		r.step("quota-axi choice", "", err)
	}
	r.removePiExtension()
	r.removeOpenCodePlugin()
	r.removeGuidance()
	if err := daemon.RemoveSkillState(daemon.StateDir()); err != nil {
		r.step("skill state", "", err)
	}

	if r.Purge {
		worktrees, _ := os.ReadDir(filepath.Join(daemon.StateDir(), "worktrees"))
		for _, dir := range []string{daemon.ConfigDir(), daemon.StateDir()} {
			res := "skipped (not found)"
			if exists(dir) {
				if err := os.RemoveAll(dir); err != nil {
					r.step("purge", "", err)
					continue
				}
				if dir == daemon.StateDir() {
					for _, entry := range worktrees {
						if entry.IsDir() {
							if err := claudetrust.Remove(filepath.Join(dir, "worktrees", entry.Name())); err != nil {
								r.step("Claude worktree trust", "", err)
							}
						}
					}
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
	if err := agentfiles.RemoveLegacyPi(); err != nil {
		r.step("legacy pi files", "", err)
	}
	removeGeneratedCatalog(r, "pi tool catalog", agentfiles.PiToolsPath())
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
	removeGeneratedCatalog(r, "opencode tool catalog", agentfiles.OpencodeToolsPath())
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

func removeGeneratedCatalog(r *runner, name, path string) {
	if st, statErr := os.Lstat(path); statErr == nil && st.Mode()&os.ModeSymlink != 0 {
		targetData, readErr := os.ReadFile(path)
		if readErr != nil || !strings.HasPrefix(string(targetData), "// Code generated by go generate ./internal/toolcatalog; DO NOT EDIT.\n") {
			r.step(name, "skipped (symlink, left as is)", nil)
			return
		}
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		r.step(name, "skipped (not installed)", nil)
		return
	}
	if err != nil {
		r.step(name, "", err)
		return
	}
	if !strings.HasPrefix(string(data), "// Code generated by go generate ./internal/toolcatalog; DO NOT EDIT.\n") {
		r.step(name, "skipped (not an agent-net generated file)", nil)
		return
	}
	if err := os.Remove(path); err != nil {
		r.step(name, "", err)
		return
	}
	r.step(name, "ok (removed)", nil)
}

func (r *runner) removeGuidance() {
	for _, f := range agentfiles.GuidanceFiles() {
		old, err := os.ReadFile(f.Path)
		if err != nil {
			r.step(f.Name, "skipped (no file)", nil)
			continue
		}
		// Remove by block markers so older guidance is removed too when its
		// wording changes, including the merge authority clause.
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

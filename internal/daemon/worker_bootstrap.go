package daemon

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// setWorktreeUpstream records origin/<branch> as the upstream of a worker's
// branch, so a plain `git push` works. The worker's sandbox keeps the shared
// .git read-only, and `git push -u` writes the upstream into its config, so
// the daemon, which can write it, does it at the start. Nothing is written
// when the project has no origin.
func setWorktreeUpstream(ctx context.Context, main, branch string) error {
	if _, err := gitOut(ctx, main, "config", "--get", "remote.origin.url"); err != nil {
		return nil
	}
	if _, err := gitOut(ctx, main, "config", "branch."+branch+".remote", "origin"); err != nil {
		return err
	}
	_, err := gitOut(ctx, main, "config", "branch."+branch+".merge", "refs/heads/"+branch)
	return err
}

// bootstrapTail is how much of the command's output goes in the notice.
const bootstrapTail = 1500

// runWorkerBootstrap runs the project's workers.bootstrap_command in the new
// worktree before the harness starts, in the same OS sandbox a Pi or OpenCode
// worker gets (the project's node.json switch applies: with the sandbox off
// for the project it runs without). A failure does not stop the worker; it is
// kept for the start notice (workerBootstrapLine).
func (d *Daemon) runWorkerBootstrap(ctx context.Context, w *worker) {
	d.wmu.Lock()
	project, worktree, level := w.project, w.worktree, w.permissions
	d.wmu.Unlock()
	cmdText := strings.TrimSpace(d.SettingsSnapshot().String("workers.bootstrap_command", settings.Where{Repo: project}))
	if cmdText == "" || worktree == "" {
		return
	}
	note := func(format string, a ...any) {
		line := fmt.Sprintf(format, a...)
		d.wmu.Lock()
		w.bootstrap = line
		d.wmu.Unlock()
		d.Logf("worker %s: %s", w.id, line)
	}
	if protocol.WorkerPermissionsOrDefault(level) == protocol.WorkersReadOnly {
		note("The bootstrap command was not run: this worker is read-only.")
		return
	}
	begin := time.Now()
	out, err := d.bootstrapOutput(ctx, w, cmdText)
	if err != nil {
		tail := strings.TrimSpace(out)
		if len(tail) > bootstrapTail {
			tail = "..." + tail[len(tail)-bootstrapTail:]
		}
		line := fmt.Sprintf("The bootstrap command failed (%v): %s", err, cmdText)
		if tail != "" {
			line += "\nOutput:\n" + tail
		}
		note("%s", line)
		return
	}
	d.Logf("worker %s: bootstrap command ok in %s", w.id, time.Since(begin).Round(time.Millisecond))
}

// bootstrapOutput runs the command with sh -c and returns its output.
func (d *Daemon) bootstrapOutput(ctx context.Context, w *worker, cmdText string) (string, error) {
	d.wmu.Lock()
	project, worktree := w.project, w.worktree
	d.wmu.Unlock()
	bin, args := "/bin/sh", []string{"-c", cmdText}
	env := workerEnv(os.Environ(), d.workerToolEnv(w))
	if d.cfg.Workers.osSandboxMode(project) != osSandboxOff {
		tool, err := osSandboxTool()
		if err == nil {
			err = osSandboxReadyFor(d)
		}
		if err != nil {
			return "", fmt.Errorf("the OS sandbox is on for this project but cannot run here, so the command was not started: %v", err)
		}
		in, _, err := d.osSandboxInputFor(w, bin)
		if err != nil {
			return "", err
		}
		tmp := osSandboxTmpEnv(filepath.Join(in.WorkerDir, "tmp"))
		set := map[string]string{"TMPDIR": tmp}
		for k, v := range workerCacheVars(filepath.Join(tmp, "cache")) {
			set[k] = v
		}
		env = workerEnv(env, set)
		args = osSandboxCommand(buildOSSandboxPolicy(in), bin, args)
		bin = tool
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir, cmd.Env = worktree, env
	cmd.WaitDelay = 2 * time.Second
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("stopped by the start timeout: %w", ctx.Err())
	}
	return buf.String(), err
}

// osSandboxReadyFor is osSandboxReady, or the test replacement.
func osSandboxReadyFor(d *Daemon) error {
	if d.cfg.testOSSandboxReady != nil {
		return d.cfg.testOSSandboxReady()
	}
	return osSandboxReady()
}

// workerBootstrapLine is the line a start notice adds when the project's
// bootstrap command failed or was skipped, or "".
func (d *Daemon) workerBootstrapLine(w *worker) string {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if w.bootstrap == "" {
		return ""
	}
	return w.bootstrap + "\n"
}

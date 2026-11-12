package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/niparis/agent-net/internal/claudetrust"
	"github.com/niparis/agent-net/internal/protocol"
)

// A session role is a set of settings the one start path applies to an
// ordinary agent-net session: its name, system prompt, tool list, MCP servers
// and model (decision 0156). The CTO is the one role today. The chief of staff
// is an ordinary agent session and needs none.
type sessionRole string

const roleCTO sessionRole = "cto"

const ctoClaudeJobName = "agent-net-cto"

// claudeRoleDir is the private folder a role's Claude session runs in. It is
// also where its conversation transcript lives, so it must not change.
func (d *Daemon) claudeRoleDir(role sessionRole) string {
	return filepath.Join(d.cfg.StateDir, string(role), "claude")
}

// claudeStartInput is everything one Claude start needs that is not fixed by
// the launcher. Owner starts, resumes, wakes and role starts all build their
// claudeBackground here.
type claudeStartInput struct {
	Role                                        sessionRole
	Name, Resume, Model, Settings, FirstMessage string
}

// claudeStartSpec is the one builder of a Claude start. A role replaces the
// settings and adds its flags and MCP config; a coding session has none.
func (d *Daemon) claudeStartSpec(ctx context.Context, in claudeStartInput) claudeBackground {
	spec := claudeBackground{Name: in.Name, Settings: in.Settings, Resume: in.Resume, FirstMessage: in.FirstMessage}
	if in.Model != "" {
		spec.ModelArgs = []string{"--model", in.Model}
	}
	if in.Role == roleCTO {
		d.applyCTOClaudeRole(ctx, &spec)
	}
	return spec
}

// applyCTOClaudeRole is the CTO's Claude settings. It has no built-in tools
// and reads none of the owner's Claude settings: only its own MCP server.
func (d *Daemon) applyCTOClaudeRole(ctx context.Context, spec *claudeBackground) {
	// CLAUDE_CODE_DISABLE_MOUSE is read inside the session, from its settings env
	// and from the environment of Claude's background service. The command that
	// starts the job and the attach client are ignored, so "0" here is the only
	// way to keep the wheel working for a long CTO reply whatever started the
	// service. See docs/decisions/0128-cto-keeps-mouse-tracking-on.md.
	// The CTO's message tool is allowed, so Claude Code's auto-mode check does not
	// block it. The name is the cto-mcp server's tool: mcp__<server>__<tool>.
	launch := map[string]any{"crossSessionInbound": "accept", "disableAllHooks": true, "enabledPlugins": map[string]bool{}, "permissions": map[string]any{"allow": []string{"mcp__agent-net-cto__agent_message_send"}}, "env": map[string]string{"AGENT_NET_CTO": "1", "AGENT_NET_SOCKET": d.cfg.SocketPath, "AGENT_NET_WORKER": "", "CLAUDE_CODE_DISABLE_MOUSE": "0", "DISABLE_AUTOUPDATER": "1"}}
	// "fullscreen" is what gives every other session scrollback; the CTO reads
	// no owner settings, so this one is passed on (see ownerClaudeRenderer).
	if tui := ownerClaudeRenderer(d.cfg.ClaudeSessionsDir); tui != "" {
		launch["tui"] = tui
	}
	settings, _ := json.Marshal(launch)
	mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{ctoClaudeJobName: map[string]any{"command": d.cfg.Exe, "args": []string{"cto-mcp"}}}})
	spec.Name, spec.Settings = ctoClaudeJobName, string(settings)
	spec.Flags = []string{"--tools", "", "--setting-sources", "", "--system-prompt", d.ctoSystemPrompt(ctx), "--mcp-config", string(mcp), "--strict-mcp-config"}
	// Every session that Claude's service starts later inherits the environment
	// of the first launch: do not hand it a variable that turns the wheel off.
	spec.DropEnv = []string{"CLAUDE_CODE_DISABLE_MOUSE"}
}

// startClaudeRole starts, or keeps, the Claude session of a role. It is
// serialized with CTO mutations by ctoDispatchMu. Only inspecting and
// publishing runtime state takes cto.mu. Launching Claude, capability checks
// and waiting for its session file run without it.
//
// The launch is startBackground, as for every Claude start. What stays here is
// the role's runtime record (which job is the CTO, adoption after a daemon
// restart, crash monitoring), which part B moves into the ordinary session
// record.
func (d *Daemon) startClaudeRole(ctx context.Context, role sessionRole, id, model string) error {
	d.setClaudeCTORestarted(false)
	start := func(id string) error {
		err := d.startClaudeRoleOnce(ctx, role, id, model)
		var failure *claudeJobFailure
		if errors.As(err, &failure) && failure.ended {
			d.Logf("cto.crash harness=claude reason=startup_job_failed detail=%q", failure.detail)
		}
		return err
	}
	err := start(id)
	var failure *claudeJobFailure
	if id == "" || !errors.As(err, &failure) || !failure.recoverable {
		return err
	}
	if err = start(""); err != nil {
		return err
	}
	d.setClaudeCTORestarted(true)
	if d.log != nil {
		d.log.Print(claudeCTORestartNotice)
	}
	return nil
}

func (d *Daemon) startClaudeRoleOnce(ctx context.Context, role sessionRole, id, model string) error {
	d.cto.mu.Lock()
	c := &d.claudeCTO
	dir := d.claudeRoleDir(role)
	canAdopt := c.ref == ""
	if c.ref != "" {
		sf, ok := findClaudeCTOBg(d.cfg.ClaudeSessionsDir, dir, c.ref)
		root := c.root.Load()
		tok, alive := "", false
		if root != nil {
			tok, alive = procToken(root.pid)
		}
		status, _ := claudeCTOJobState(d.cfg.ClaudeSessionsDir, c.ref)
		if ok && !claudeJobEnded(status) && root != nil && alive && tok == c.token && sf.PID == root.pid && c.session == id && c.model == model {
			d.cto.mu.Unlock()
			return nil
		}
		reason := "configuration_change"
		if !alive || root == nil || tok != c.token || claudeJobEnded(status) {
			reason = claudeCTOEndReason(status)
		}
		d.stopClaudeCTOLocked(reason)
	}
	d.cto.mu.Unlock()
	// A daemon restart loses the in-memory job ref, but the private background
	// session may still be alive. Bind it before launching another copy.
	if sf, ok := findClaudeCTOBg(d.cfg.ClaudeSessionsDir, dir, ""); canAdopt && ok && func() bool {
		status, _ := claudeCTOJobState(d.cfg.ClaudeSessionsDir, sf.JobID)
		return !claudeJobEnded(status)
	}() && d.bindClaudeCTO(sf, model, id, "adopted") {
		return nil
	}
	bin, err := d.harnessBinary("claude")
	if err != nil {
		return err
	}
	if err := d.checkClaudeCTOCapabilities(ctx, bin); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := claudetrust.Set(dir); err != nil {
		return fmt.Errorf("trust CTO Claude directory: %w", err)
	}
	spec := d.claudeStartSpec(ctx, claudeStartInput{Role: role, Resume: id, Model: model})
	c.starting.Store(true)
	defer c.starting.Store(false)
	// The launch is bounded like every start. The old private path bounded only
	// the wait for the session file, so a slow first launch of Claude's service
	// is allowed the same time as for any other session.
	launch, cancel := context.WithTimeout(ctx, d.workerSettings("").StartTimeout)
	defer cancel()
	sf, err := d.startBackground(launch, bin, dir, spec, "", nil, nil)
	if err != nil {
		var failure *claudeJobFailure
		if errors.As(err, &failure) || strings.Contains(err.Error(), "does not trust") {
			return err
		}
		return fmt.Errorf("start CTO Claude: %w", err)
	}
	if !d.bindClaudeCTO(sf, model, id, "started") {
		return errors.New("CTO Claude session did not start: its process is gone")
	}
	return nil
}

// startOpenCodeRole starts, or keeps, the private OpenCode server of a role.
// The caller holds cto.mu. A role needs its own server: its agent, system
// prompt, permissions and MCP server are OpenCode configuration, which the
// owner's shared service must not carry. It is the same launcher that starts a
// worker's private server (serveOpenCodePrivate).
func (d *Daemon) startOpenCodeRole(ctx context.Context, role sessionRole) error {
	c := &d.cto
	if c.cmd != nil {
		select {
		case <-c.exited:
			d.stopOpenCodeCTOLocked("crash")
		default:
			return nil
		}
	}
	bin, err := d.harnessBinary("opencode")
	if err != nil {
		return err
	}
	dir := filepath.Join(d.cfg.StateDir, string(role))
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, state := filepath.Join(dir, "data"), filepath.Join(dir, "state")
	if err = copyOpenCodeAuth(ctx, bin, data, state); err != nil {
		return err
	}
	config, cache := filepath.Join(dir, "config"), filepath.Join(dir, "cache")
	for _, path := range []string{state, config, cache} {
		if err = os.MkdirAll(path, 0700); err != nil {
			return err
		}
	}
	// This project configuration belongs only to the role's private server.
	configData, err := d.ctoConfigData(config)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "opencode.json"), configData, 0600); err != nil {
		return err
	}
	pass := protocol.NewToken()
	logf, err := os.OpenFile(filepath.Join(dir, "opencode.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer logf.Close()
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var run *ctoProcess
	_, err = d.serveOpenCodePrivate(startup, openCodeServe{
		label: "CTO OpenCode", password: pass,
		spawn: func(attempt, port int) (string, string, string, error) {
			cmd := exec.Command(bin, "serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(port))
			cmd.Dir = dir
			cmd.Env = workerEnv(os.Environ(), map[string]string{"XDG_DATA_HOME": data, "XDG_STATE_HOME": state, "XDG_CONFIG_HOME": config, "OPENCODE_CONFIG": "", "OPENCODE_CONFIG_CONTENT": "", "OPENCODE_CONFIG_DIR": "", "XDG_CACHE_HOME": cache, "OPENCODE_SERVER_PASSWORD": pass, "AGENT_NET_CTO": "1", "AGENT_NET_WORKER": "", "AGENT_NET_WORKER_SESSION_FILE": "", "AGENT_NET_SOCKET": ""})
			cmd.Stdout, cmd.Stderr = logf, logf
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := cmd.Start(); err != nil {
				return "", "", "", fmt.Errorf("start CTO OpenCode: %w", err)
			}
			c.cmd = cmd
			c.pid.Store(int64(cmd.Process.Pid))
			c.exited = make(chan struct{})
			c.base, c.password, c.version = portURL(port), pass, d.openCodeVersion()
			c.identity = fmt.Sprintf("cto:%d:%d", cmd.Process.Pid, attempt)
			exited := c.exited
			run = &ctoProcess{}
			c.run = run
			go func(run *ctoProcess) {
				err := cmd.Wait()
				c.pid.CompareAndSwap(int64(cmd.Process.Pid), 0)
				if !run.expected.Load() {
					// Reap any children left in the private process group.
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					d.Logf("cto.crash harness=opencode error=%v", err)
					if run.started.Load() {
						d.Logf("cto.stop harness=opencode reason=crash")
					}
				}
				close(exited)
				d.scheduleCTOPresence()
			}(run)
			return c.base, c.version, c.identity, nil
		},
		setVersion: func(v string) { c.version = v },
		gone: func() bool {
			select {
			case <-c.exited:
				return true
			default:
				return false
			}
		},
		kill: d.stopOpenCodeCTOLocked,
		ready: func(info []byte) error {
			return d.checkOpenCodeCTOTools(startup, info)
		},
	})
	if err != nil {
		return err
	}
	run.started.Store(true)
	d.Logf("cto.start harness=opencode")
	d.scheduleCTOPresence()
	d.startCTOCompactionLoop()
	return nil
}

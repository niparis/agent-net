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
	NonOwnerFirstMessage                        bool
	// Skills is the graph node this start takes its skills from. The zero
	// value gets no folder.
	Skills skillNode
}

// claudeStartSpec is the one builder of a Claude start. A role replaces the
// settings and adds its flags and MCP config; a coding session has none.
func (d *Daemon) claudeStartSpec(ctx context.Context, in claudeStartInput) claudeBackground {
	spec := claudeBackground{Name: in.Name, Settings: in.Settings, Resume: in.Resume, FirstMessage: in.FirstMessage, NonOwnerFirstMessage: in.NonOwnerFirstMessage}
	if in.Model != "" {
		spec.ModelArgs = []string{"--model", in.Model}
	}
	if in.Role == roleCTO {
		d.applyCTOClaudeRole(ctx, &spec)
	}
	// Claude Code takes a whole skill set as one plugin folder (decision 0169,
	// task 4). A set that could not be built leaves the launch as it was.
	if !in.Skills.empty() {
		if folder, ok := d.skillsForStart(ctx, in.Skills); ok {
			spec.Flags = append(spec.Flags, "--plugin-dir", folder.Dir)
		}
	}
	return spec
}

// ctoHookMatcher is the CTO's cto_op tool as Claude Code names it:
// mcp__<server>__<tool>. The server is the CTO's own MCP server.
const ctoHookMatcher = "mcp__" + ctoClaudeJobName + "__cto_op"

// applyCTOClaudeRole is the CTO's Claude settings. The CTO has full power
// (decision 0157): Claude Code's built-in tools and the owner's own Claude
// settings, as in any coding session. What the role adds is its prompt, its
// own MCP server and the tool-decide hook, which lets it run the ops its scopes allow at once.
func (d *Daemon) applyCTOClaudeRole(ctx context.Context, spec *claudeBackground) {
	// CLAUDE_CODE_DISABLE_MOUSE is read inside the session, from its settings env
	// and from the environment of Claude's background service. The command that
	// starts the job and the attach client are ignored, so "0" here is the only
	// way to keep the wheel working for a long CTO reply whatever started the
	// service. See docs/decisions/0128-cto-keeps-mouse-tracking-on.md.
	// The CTO's message tool is allowed, so Claude Code's auto-mode check does not
	// block it. The name is the cto-mcp server's tool: mcp__<server>__<tool>.
	hook := map[string]any{"type": "command", "command": shellQuote(d.cfg.Exe) + " tool-decide"}
	// autoMemoryEnabled false turns Claude Code's auto memory off for this launch
	// only (owner decision 2026-11-13: the CTO's memory is gateway notes, which
	// the owner sees and deletes in the TUI). A coding session does not get it.
	launch := map[string]any{"crossSessionInbound": "accept", "autoMemoryEnabled": false,
		"hooks":       map[string]any{"PreToolUse": []any{map[string]any{"matcher": ctoHookMatcher, "hooks": []any{hook}}}},
		"permissions": map[string]any{"allow": []string{"mcp__agent-net-cto__agent_message_send"}},
		"env":         map[string]string{"AGENT_NET_CTO": "1", "AGENT_NET_SOCKET": d.cfg.SocketPath, "AGENT_NET_WORKER": "", "CLAUDE_CODE_DISABLE_MOUSE": "0", "DISABLE_AUTOUPDATER": "1"}}
	settings, _ := json.Marshal(launch)
	mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{ctoClaudeJobName: map[string]any{"command": d.cfg.Exe, "args": []string{"cto-mcp"}}}})
	spec.Name, spec.Settings = ctoClaudeJobName, string(settings)
	// --strict-mcp-config keeps the CTO's identity: its one agent-net server
	// is cto-mcp, never the owner's ordinary agent-net server.
	// The prompt is appended, never a replacement: --system-prompt would drop
	// Claude Code's default prompt and with it the built-in tool guidance. The
	// CTO has full tools (decision 0157), so it needs that guidance.
	spec.Flags = []string{"--append-system-prompt", d.ctoSystemPrompt(ctx), "--mcp-config", string(mcp), "--strict-mcp-config"}
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
	return d.startClaudeRoleWith(ctx, role, id, model, "")
}

// startClaudeRoleWith is startClaudeRole with a first prompt. A start with a
// prompt always launches: an adopted job would never get the prompt.
func (d *Daemon) startClaudeRoleWith(ctx context.Context, role sessionRole, id, model, first string) error {
	d.setClaudeCTORestarted(false)
	start := func(id string) error {
		err := d.startClaudeRoleOnce(ctx, role, id, model, first)
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

func (d *Daemon) startClaudeRoleOnce(ctx context.Context, role sessionRole, id, model, first string) error {
	dir := d.claudeRoleDir(role)
	c := &d.claudeCTO
	var startGen uint64
	var canAdopt bool
	for {
		d.cto.mu.Lock()
		ref := c.ref
		root := c.root.Load()
		cToken, cSession, cModel := c.token, c.sessionID(), c.model
		startGen = c.gen
		d.cto.mu.Unlock()
		canAdopt = ref == "" && first == ""
		if ref == "" {
			break
		}
		sf, ok := findClaudeCTOBg(d.cfg.ClaudeSessionsDir, dir, ref)
		tok, alive := "", false
		if root != nil {
			tok, alive = procToken(root.pid)
		}
		status, _ := claudeCTOJobState(d.cfg.ClaudeSessionsDir, ref)
		keep := first == "" && ok && !claudeJobEnded(status) && root != nil && alive && tok == cToken && sf.PID == root.pid && cSession == id && cModel == model

		d.cto.mu.Lock()
		if c.ref != ref || c.root.Load() != root || c.token != cToken || c.sessionID() != cSession || c.model != cModel {
			d.cto.mu.Unlock()
			continue
		}
		if keep {
			d.cto.mu.Unlock()
			return nil
		}
		reason := "configuration_change"
		if !alive || root == nil || tok != cToken || claudeJobEnded(status) {
			reason = claudeCTOEndReason(status)
		}
		d.stopClaudeCTOLocked(reason)
		startGen = c.gen
		d.cto.mu.Unlock()
		break
	}
	// A daemon restart loses the in-memory job ref, but the private background
	// session may still be alive. Bind it before launching another copy.
	if sf, ok := findClaudeCTOBg(d.cfg.ClaudeSessionsDir, dir, ""); canAdopt && ok && func() bool {
		status, _ := claudeCTOJobState(d.cfg.ClaudeSessionsDir, sf.JobID)
		return !claudeJobEnded(status)
	}() && d.bindClaudeCTO(sf, model, id, "adopted", startGen) {
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
	spec := d.claudeStartSpec(ctx, claudeStartInput{Role: role, Resume: id, Model: model, FirstMessage: first, NonOwnerFirstMessage: first != "", Skills: skillNode{Agent: "cto"}})
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
	if !d.bindClaudeCTO(sf, model, id, "started", startGen) {
		return errors.New("CTO Claude session did not start: its process is gone")
	}
	return nil
}

// startOpenCodeRole starts, or keeps, the private OpenCode server of a role.
// The caller must not hold cto.mu: spawning and polling run without the lock,
// and the server is published only if the generation did not change in between.
// A role needs its own server: its agent, system prompt, permissions and MCP
// server are OpenCode configuration, which the owner's shared service must not
// carry. It is the same launcher that starts a worker's private server
// (serveOpenCodePrivate).
func (d *Daemon) startOpenCodeRole(ctx context.Context, role sessionRole) error {
	c := &d.cto
	c.mu.Lock()
	if c.cmd != nil {
		select {
		case <-c.exited:
			d.stopOpenCodeCTOLocked("crash")
		default:
			c.mu.Unlock()
			return nil
		}
	}
	startGen := c.gen
	c.mu.Unlock()
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
	// The role's node takes its skills in its per-process config (decision
	// 0169, task 4), which adds to the file above. A set that could not be
	// built leaves the process config empty.
	skillsConfig := d.openCodeSkillsConfig(ctx, skillNode{Agent: "cto"}, "")
	pass := protocol.NewToken()
	logf, err := os.OpenFile(filepath.Join(dir, "opencode.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer logf.Close()
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var (
		localCmd                         *exec.Cmd
		localExited                      chan struct{}
		localRun                         *ctoProcess
		localBase, localVersion, localID string
	)
	killLocal := func() {
		if localCmd == nil {
			return
		}
		select {
		case <-localExited:
		default:
			localRun.expected.Store(true)
			_ = syscall.Kill(-localCmd.Process.Pid, syscall.SIGKILL)
			<-localExited
		}
		localCmd = nil
		localExited = nil
		localRun = nil
	}
	_, err = d.serveOpenCodePrivate(startup, openCodeServe{
		label: "CTO OpenCode", password: pass,
		spawn: func(attempt, port int) (string, string, string, error) {
			cmd := exec.Command(bin, "serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(port))
			cmd.Dir = dir
			cmd.Env = workerEnv(os.Environ(), map[string]string{"XDG_DATA_HOME": data, "XDG_STATE_HOME": state, "XDG_CONFIG_HOME": config, "OPENCODE_CONFIG": "", "OPENCODE_CONFIG_CONTENT": skillsConfig, "OPENCODE_CONFIG_DIR": "", "XDG_CACHE_HOME": cache, "OPENCODE_SERVER_PASSWORD": pass, "AGENT_NET_CTO": "1", "AGENT_NET_WORKER": "", "AGENT_NET_WORKER_SESSION_FILE": "", "AGENT_NET_SOCKET": ""})
			cmd.Stdout, cmd.Stderr = logf, logf
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := cmd.Start(); err != nil {
				return "", "", "", fmt.Errorf("start CTO OpenCode: %w", err)
			}
			localCmd = cmd
			localExited = make(chan struct{})
			localBase, localVersion = portURL(port), d.openCodeVersion()
			localID = fmt.Sprintf("cto:%d:%d", cmd.Process.Pid, attempt)
			exited := localExited
			run := &ctoProcess{}
			localRun = run
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
			return localBase, localVersion, localID, nil
		},
		setVersion: func(v string) { localVersion = v },
		gone: func() bool {
			c.mu.Lock()
			stopped := c.gen != startGen
			c.mu.Unlock()
			if stopped {
				cancel()
				return true
			}
			select {
			case <-localExited:
				return true
			default:
				return false
			}
		},
		kill: func(string) { killLocal() },
		ready: func(info []byte) error {
			return d.checkOpenCodeCTOAgentAt(startup, localBase, pass, &localVersion, localID, info)
		},
	})
	if err != nil {
		killLocal()
		return err
	}
	c.mu.Lock()
	if c.gen != startGen {
		c.mu.Unlock()
		killLocal()
		return errCTONotRunning
	}
	if c.cmd != nil {
		c.mu.Unlock()
		killLocal()
		return nil
	}
	c.cmd = localCmd
	c.pid.Store(int64(localCmd.Process.Pid))
	c.exited = localExited
	c.base, c.password, c.version, c.identity = localBase, pass, localVersion, localID
	c.run = localRun
	c.mu.Unlock()
	localRun.started.Store(true)
	d.Logf("cto.start harness=opencode")
	d.scheduleCTOPresence()
	d.startCTOCompactionLoop()
	return nil
}

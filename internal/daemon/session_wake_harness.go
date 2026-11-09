package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/niparis/agent-net/internal/daemon/opencodeapi"
	"github.com/niparis/agent-net/internal/protocol"
)

// wakeSession resumes an offline owner session so queued messages can be
// delivered. Claude keeps its own rules (session_wake.go). Codex and OpenCode
// are woken only when the gateway says the agent's autonomy is wake. Pi cannot
// be woken: its rpc mode cannot open an existing session. A wake never starts
// a new session, never opens a view or a herdr pane, and never touches workers.
func (d *Daemon) wakeSession(ctx context.Context, id, token string, autonomy bool) error {
	a, err := d.agentView(ctx, id)
	if err != nil {
		return err
	}
	switch a.Harness {
	case "claude":
		return d.wakeClaude(ctx, id, token, autonomy)
	case "codex", "opencode":
		if !autonomy {
			return errors.New("this session is woken only when its agent's autonomy is wake")
		}
		if err := d.wakeGuard(a); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(ctx, 110*time.Second)
		defer cancel()
		if a.Harness == "codex" {
			return d.wakeCodex(ctx, a)
		}
		return d.wakeOpenCode(ctx, a)
	}
	return fmt.Errorf("%s sessions cannot be woken", a.Harness)
}

// wakeGuard holds the checks every wake shares: a known, offline, local owner
// session in a folder that still exists.
func (d *Daemon) wakeGuard(a protocol.AgentView) error {
	if a.NodeID != d.cfg.NodeID || a.Worker != nil || a.NativeID == "" || a.Online {
		return fmt.Errorf("not an offline local %s owner session", a.Harness)
	}
	if d.workerBySession(a.SessionID) != nil {
		return errors.New("workers cannot be woken")
	}
	if _, ok, err := d.st.workerBySession(a.SessionID); err != nil || ok {
		if err != nil {
			return err
		}
		return errors.New("workers cannot be woken")
	}
	info, err := os.Stat(a.Cwd)
	if err != nil || !info.IsDir() {
		return errors.New("session folder is gone")
	}
	return nil
}

// wakeCodex reopens the saved thread on the shared app server with
// thread/resume, in the session's folder. Codex then lists it as loaded, the
// daemon registers it, and the gateway delivers the queued messages with
// thread/queue/add. No turn starts here and no window opens.
func (d *Daemon) wakeCodex(ctx context.Context, a protocol.AgentView) error {
	r := protocol.StartedSession{ID: protocol.NewID("wake"), Status: "starting", NodeID: d.cfg.NodeID, Hostname: d.hostName(), Harness: "codex", Path: a.Cwd, NativeID: a.NativeID}
	r, err := d.prepareOwnerCodexThread(ctx, r, true)
	if codexServerDown(err) {
		// After a reboot nothing has started the shared app server, so its
		// control socket is missing (or left behind with no one listening).
		if err = d.startCodexAppServer(ctx); err != nil {
			return fmt.Errorf("cannot start the Codex app server: %w", err)
		}
		r, err = d.prepareOwnerCodexThread(ctx, r, true)
	}
	if err != nil {
		return fmt.Errorf("cannot resume the Codex thread: %w", err)
	}
	if r.NativeID != a.NativeID {
		return errors.New("Codex resumed a different thread")
	}
	return nil
}

// codexServerDown reports a failed dial of the control socket because the
// socket is missing or has no listener.
func codexServerDown(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
}

// startCodexAppServer starts the shared Codex app server with Codex's own
// command, `codex app-server daemon start`, which does nothing when the daemon
// already runs, and then waits for its control socket. It runs in the owner's
// Codex home and changes no Codex file or setting by itself. The command
// output goes to a file, not a pipe, because the daemon it starts outlives it.
func (d *Daemon) startCodexAppServer(ctx context.Context) error {
	bin, err := d.harnessBinary("codex")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, d.workerSettings("").StartTimeout)
	defer cancel()
	out, err := os.CreateTemp("", "agent-net-codex-start-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	cmd := exec.CommandContext(ctx, bin, "app-server", "daemon", "start")
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = 5 * time.Second
	if home, def := d.cfg.CodexHome, defaultCodexHome(); home != "" && home != def {
		cmd.Env = workerEnv(os.Environ(), map[string]string{"CODEX_HOME": home})
	}
	if err := cmd.Run(); err != nil {
		text, _ := os.ReadFile(out.Name())
		if reason := cleanFailure(string(text)); len(text) > 0 {
			return fmt.Errorf("%s: %w", reason, err)
		}
		return err
	}
	sock := codexSocketPath(d.cfg.CodexHome)
	for {
		if target, err := filepath.EvalSymlinks(sock); err == nil {
			if fi, err := os.Lstat(target); err == nil && fi.Mode()&os.ModeSocket != 0 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the Codex app server started but its control socket %s did not appear", sock)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// wakeOpenCode loads the saved session in the owner's OpenCode service, in the
// session's folder, so the plugin registers it again. It never creates a
// session and never opens a viewer.
func (d *Daemon) wakeOpenCode(ctx context.Context, a protocol.AgentView) error {
	service, err := d.ownerOpenCodeService()
	if err != nil {
		return fmt.Errorf("the OpenCode service is not running on %s", d.cfg.Hostname)
	}
	routes, err := d.discoverOpenCodeRoutes(ctx, service.URL, service.Password, "", service.identity)
	if err != nil {
		return fmt.Errorf("cannot reach the OpenCode service: %w", err)
	}
	api := openCodeWorkerAPI(ctx, service.URL, service.Password)
	directory := strings.ReplaceAll(url.QueryEscape(a.Cwd), "+", "%20")
	for _, step := range []struct {
		op opencodeapi.Operation
		id string
	}{{opencodeapi.GetSession, a.NativeID}, {opencodeapi.Location, ""}} {
		path, err := routes.Path(step.op, step.id)
		if err != nil {
			return err
		}
		_, code, err := api(step.op.Method(), path, nil, "x-opencode-directory", directory)
		if err != nil {
			return errors.New("cannot reach the OpenCode service")
		}
		if code != http.StatusOK {
			return fmt.Errorf("OpenCode refused to load the session (HTTP %d)", code)
		}
	}
	return d.waitWokenSession(ctx, "opencode", a)
}

// waitWokenSession waits until discovery has registered the resumed session
// under the same gateway session ID.
func (d *Daemon) waitWokenSession(ctx context.Context, harness string, a protocol.AgentView) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if d.SessionID(harness, a.NativeID) == a.SessionID {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the resumed %s session did not register: %w", harness, ctx.Err())
		case <-ticker.C:
		}
	}
}

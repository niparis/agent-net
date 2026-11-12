package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// agent-net's own herdr server (decision 0154).
//
// A herdr session the owner runs himself is read-only for agent-net: the
// daemon lists its panes and reads their process info, and nothing else. It
// never creates a tab, runs a command, focuses, types into or closes a pane
// there. Everything agent-net hosts in herdr (a Pi session, a viewer asked
// from another machine, a sign-in asked from another machine) goes into the
// named herdr session "agent-net". A named session is a separate herdr server
// with its own socket, and `herdr --remote <host> --session agent-net`
// reaches it from another machine.

const agentNetHerdrSession = "agent-net"

// herdrRemoteArgv opens a herdr session on another machine through herdr's
// remote client, the path that carries the clipboard (decision 0046). Without
// a valid session name it opens that machine's default herdr session.
func herdrRemoteArgv(bin, target, session string) []string {
	argv := []string{bin, "--remote", target}
	if herdrSessionRe.MatchString(session) {
		argv = append(argv, "--session", session)
	}
	return argv
}

// herdrCleanEnv is the daemon's environment without the variables that would
// point a herdr command at the herdr the daemon happens to run in.
func herdrCleanEnv() []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HERDR_SESSION=") && !strings.HasPrefix(kv, "HERDR_SOCKET_PATH=") {
			env = append(env, kv)
		}
	}
	return env
}

// agentNetHerdrSocket asks herdr where the agent-net session's socket is and
// whether its server runs. `session list` reads herdr's session folders; it
// sends nothing to a server.
func (d *Daemon) agentNetHerdrSocket(bin string) (socket string, running bool, err error) {
	out, err := d.herdrRun(bin, "", "session", "list", "--json")
	if err != nil {
		return "", false, err
	}
	var r struct {
		Sessions []struct {
			Name    string `json:"name"`
			Running bool   `json:"running"`
			Socket  string `json:"socket_path"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", false, err
	}
	for _, s := range r.Sessions {
		if s.Name == agentNetHerdrSession && filepath.IsAbs(s.Socket) {
			d.herdrOwnMu.Lock()
			d.herdrOwnSocket = filepath.Clean(s.Socket)
			d.herdrOwnMu.Unlock()
			return filepath.Clean(s.Socket), s.Running, nil
		}
	}
	return "", false, nil
}

// agentNetHerdr returns the socket of agent-net's herdr server, and starts the
// server when it does not answer. The server is in its own process session so
// that it, and what runs in it, outlives a daemon restart.
func (d *Daemon) agentNetHerdr(bin string) (string, error) {
	ready := func() (string, bool) {
		socket, running, err := d.agentNetHerdrSocket(bin)
		if err != nil || !running || socket == "" {
			return "", false
		}
		_, err = d.herdrPanes(bin, socket)
		return socket, err == nil
	}
	if socket, ok := ready(); ok {
		return socket, nil
	}
	cmd := exec.Command(bin, "--session", agentNetHerdrSession, "server")
	cmd.Env = herdrCleanEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	var waitErr error
	done := make(chan struct{})
	go func() { waitErr = cmd.Wait(); close(done) }()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			if socket, ok := ready(); ok {
				return socket, nil
			}
			return "", fmt.Errorf("the agent-net herdr server exited: %v", firstNonNil(waitErr, errors.New("it did not answer")))
		case <-deadline.C:
			return "", fmt.Errorf("the agent-net herdr server did not become ready within 3 seconds")
		case <-ticker.C:
			if socket, ok := ready(); ok {
				return socket, nil
			}
		}
	}
}

// isAgentNetHerdr says whether socket is agent-net's own herdr server. Any
// other herdr is the owner's and is read-only.
func (d *Daemon) isAgentNetHerdr(bin, socket string) bool {
	if !filepath.IsAbs(socket) {
		return false
	}
	d.herdrOwnMu.Lock()
	own := d.herdrOwnSocket
	d.herdrOwnMu.Unlock()
	if own == "" && bin != "" {
		own, _, _ = d.agentNetHerdrSocket(bin)
	}
	return own != "" && filepath.Clean(socket) == own
}

// herdrSessionOf names the herdr session a server belongs to, for
// `herdr --remote <host> --session <name>`. It is "" for the default session.
func (d *Daemon) herdrSessionOf(bin string, h herdrPane) string {
	if d.isAgentNetHerdr(bin, h.socket) {
		return agentNetHerdrSession
	}
	if h.session != "" {
		return h.session
	}
	// herdr keeps a named session's socket at <config>/herdr/sessions/<name>/herdr.sock.
	dir := filepath.Dir(h.socket)
	if filepath.IsAbs(h.socket) && filepath.Base(filepath.Dir(dir)) == "sessions" && herdrSessionRe.MatchString(filepath.Base(dir)) {
		return filepath.Base(dir)
	}
	return ""
}

// herdrSessionAttach is the command that attaches to a pane of a named herdr
// session on the machine that runs it. It names the session, not a socket,
// because it may run on another machine through ssh.
func herdrSessionAttach(session, pane string) []string {
	argv := []string{"env", "-u", "HERDR_SOCKET_PATH"}
	if session != "" {
		argv = append(argv, "HERDR_SESSION="+session)
	}
	return append(argv, "herdr", "agent", "attach", pane)
}

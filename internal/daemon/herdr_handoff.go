package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// A herdr server reads its config once, when it starts, and the sidebar
// setting takes effect only then. agent-net's server may run without
// agent-net's config: it started before agent-net had one (v0.5.36), or a
// herdr client started it. Such a server keeps showing its sidebar, to local
// and --remote clients alike, because the server's config decides it, not the
// client's.
//
// `herdr server reload-config` rereads only the file the server started with,
// which is the owner's. herdr's live handoff instead moves every pane, with
// its running program, into a new server process. agent-net starts that
// process through a wrapper that sets HERDR_CONFIG_PATH, so the new server
// reads agent-net's config and no pane is closed. The handoff ends every
// attached client, so agent-net runs it only while no client is attached.
// Otherwise it tells the owner once and tries again at the next open.

// herdrSidebarWaitNote and herdrSidebarRestartNote are the one line the owner
// sees when the sidebar cannot go away yet. %s is the key that hides it now.
// The key comes first: the TUI shows a notice on one line and cuts the rest.
const (
	herdrSidebarWaitNote    = "Press %s to hide the herdr sidebar. It goes away for good at the next open with no herdr window attached."
	herdrSidebarRestartNote = "Press %s to hide the herdr sidebar. It goes away for good when agent-net's herdr server restarts."
)

// herdrAPI sends one request to a herdr server's API socket and decodes the
// result into result, when result is not nil.
func herdrAPI(socket, method string, params, result any) error {
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	req, err := json.Marshal(map[string]any{"id": "agent-net", "method": method, "params": params})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("herdr %s: %s: %s", method, resp.Error.Code, resp.Error.Message)
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(resp.Result, result)
}

// herdrServerPID is the process that listens on a herdr API socket.
func herdrServerPID(socket string) (int, error) {
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	pid, err := peerPID(conn)
	if err == nil && pid <= 0 {
		err = errors.New("herdr server pid unknown")
	}
	return pid, err
}

// herdrServerConfigured says whether a herdr server process runs with
// agent-net's config, and that config hides the sidebar.
func herdrServerConfigured(pid int) bool {
	path := agentNetHerdrConfigPath()
	return processEnvVar(pid, "HERDR_CONFIG_PATH") == path && herdrSidebarHidden(path)
}

// herdrUI is the part of a herdr config that agent-net reads back.
type herdrUI struct {
	Keys struct {
		Toggle string `toml:"toggle_sidebar"`
	} `toml:"keys"`
	UI struct {
		StartCollapsed bool   `toml:"sidebar_start_collapsed"`
		Collapsed      string `toml:"sidebar_collapsed_mode"`
	} `toml:"ui"`
}

func readHerdrUI(path string) (herdrUI, error) {
	var cfg herdrUI
	raw, err := os.ReadFile(path)
	if err == nil {
		err = toml.Unmarshal(raw, &cfg)
	}
	return cfg, err
}

func herdrSidebarHidden(path string) bool {
	cfg, err := readHerdrUI(path)
	return err == nil && cfg.UI.StartCollapsed && cfg.UI.Collapsed == "hidden"
}

// herdrSidebarKey is the key that shows or hides the sidebar in agent-net's
// herdr, as the owner's config binds it.
func herdrSidebarKey(path string) string {
	if cfg, err := readHerdrUI(path); err == nil && cfg.Keys.Toggle != "" {
		return cfg.Keys.Toggle
	}
	return "prefix+b"
}

// herdrHandoffWrapper writes the program that herdr's live handoff runs as
// the new server: herdr itself, with agent-net's config.
func herdrHandoffWrapper(bin, config string) (string, error) {
	path := filepath.Join(filepath.Dir(config), "herdr-server")
	body := "#!/bin/sh\n# agent-net's herdr server, with agent-net's config.\nHERDR_CONFIG_PATH=" + shellQuote(config) + " exec " + shellQuote(bin) + " \"$@\"\n"
	return path, writeHerdrStateFile(path, []byte(body), 0o700)
}

// adoptAgentNetHerdr brings agent-net's running herdr server, at socket,
// under agent-net's config. It returns a line for the owner when the sidebar
// stays for now, once for each server process.
func (d *Daemon) adoptAgentNetHerdr(bin, socket string) string {
	pid, err := herdrServerPID(socket)
	if err != nil {
		return "" // no server: the one agent-net starts gets the config
	}
	d.herdrOwnMu.Lock()
	known := d.herdrOwnConfPID == pid
	d.herdrOwnMu.Unlock()
	if known {
		return ""
	}
	if herdrServerConfigured(pid) {
		d.setHerdrOwnConfigured(pid)
		return ""
	}
	config := ""
	for _, kv := range d.agentNetHerdrServerEnv(bin) {
		if v, ok := strings.CutPrefix(kv, "HERDR_CONFIG_PATH="); ok {
			config = v
		}
	}
	if config == "" {
		// This herdr has no sidebar setting; agentNetHerdrServerEnv logged it.
		d.setHerdrOwnConfigured(pid)
		return ""
	}
	key := herdrSidebarKey(config)
	var ping struct {
		Capabilities struct {
			LiveHandoff bool `json:"live_handoff"`
		} `json:"capabilities"`
	}
	if err := herdrAPI(socket, "ping", struct{}{}, &ping); err != nil {
		d.herdrLogf("agent-net's herdr server did not answer: %v", err)
		return ""
	}
	if !ping.Capabilities.LiveHandoff {
		d.herdrLogf("agent-net's herdr server (pid %d) shows its sidebar until it restarts: this herdr has no live handoff", pid)
		return d.herdrOwnNote(pid, fmt.Sprintf(herdrSidebarRestartNote, key))
	}
	// herdr tells whether a client is attached only through the answer to a
	// window title request. Clearing a title that agent-net never set changes
	// nothing the owner sees.
	var title struct {
		Reason string `json:"reason"`
	}
	if err := herdrAPI(socket, "client.window_title.clear", struct{}{}, &title); err != nil {
		d.herdrLogf("agent-net's herdr server did not say whether a client is attached: %v", err)
		return ""
	}
	if title.Reason != "no_foreground_client" {
		return d.herdrOwnNote(pid, fmt.Sprintf(herdrSidebarWaitNote, key))
	}
	wrapper, err := herdrHandoffWrapper(bin, config)
	if err == nil {
		err = herdrAPI(socket, "server.live_handoff", map[string]string{"import_exe": wrapper}, nil)
	}
	if err == nil {
		err = waitHerdrConfigured(socket, 3*time.Second)
	}
	if err != nil {
		d.herdrLogf("agent-net's herdr server (pid %d) keeps its sidebar: the handoff to agent-net's config failed: %v", pid, err)
		return d.herdrOwnNote(pid, fmt.Sprintf(herdrSidebarRestartNote, key))
	}
	newPID, _ := herdrServerPID(socket)
	d.setHerdrOwnConfigured(newPID)
	d.herdrLogf("agent-net's herdr server moved its panes from pid %d to pid %d, which hides the sidebar", pid, newPID)
	return ""
}

// waitHerdrConfigured waits until the server that answers on socket runs with
// agent-net's config. The old server answers until the new one takes over.
func waitHerdrConfigured(socket string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		if pid, err := herdrServerPID(socket); err == nil && herdrServerConfigured(pid) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no server with agent-net's config within %s", limit)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (d *Daemon) setHerdrOwnConfigured(pid int) {
	d.herdrOwnMu.Lock()
	d.herdrOwnConfPID = pid
	d.herdrOwnMu.Unlock()
}

// herdrOwnNote returns line the first time for server pid, then "".
func (d *Daemon) herdrOwnNote(pid int, line string) string {
	d.herdrOwnMu.Lock()
	defer d.herdrOwnMu.Unlock()
	if d.herdrOwnNotePID == pid {
		return ""
	}
	d.herdrOwnNotePID = pid
	return line
}

func (d *Daemon) herdrLogf(format string, args ...any) {
	if d.log != nil {
		d.Logf(format, args...)
	}
}

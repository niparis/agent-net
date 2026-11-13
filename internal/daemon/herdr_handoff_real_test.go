package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestHerdrHandoffRealHerdr runs the daemon's adoption of agent-net's herdr
// server against a real herdr, in a temporary config folder of its own. It
// never reaches the owner's herdr servers: herdr keeps every session under
// XDG_CONFIG_HOME, which the test points at its own folder. A tmux server on
// its own socket plays the owner's terminal. scripts/herdr-sidebar-proof.sh
// runs it and keeps the frames.
func TestHerdrHandoffRealHerdr(t *testing.T) {
	// TestMain hides the machine's herdr from every test; this one gets it
	// only when asked by name.
	bin := os.Getenv("AGENT_NET_HERDR_BIN")
	if !filepath.IsAbs(bin) || !isExecutable(bin) {
		t.Skip("set AGENT_NET_HERDR_BIN to a herdr binary to run against a real herdr (scripts/herdr-sidebar-proof.sh)")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}
	// Socket paths must stay short, so not under t.TempDir.
	root, err := os.MkdirTemp("", "hsp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	t.Setenv("AGENT_NET_STATE_DIR", filepath.Join(root, "state"))
	t.Setenv("SHELL", "/bin/sh") // TestMain sets a fake shell; herdr panes run SHELL
	t.Setenv("PS1", "$ ")
	for _, k := range []string{"HERDR_CONFIG_PATH", "HERDR_SESSION", "HERDR_SOCKET_PATH", "TMUX"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	if err := os.MkdirAll(filepath.Join(root, "home"), 0o700); err != nil {
		t.Fatal(err)
	}
	ownerConfig := filepath.Join(root, "xdg", "herdr", "config.toml")
	if err := writeHerdrStateFile(ownerConfig, []byte("onboarding = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The test reads the real environment of the herdr server.
	orig := processEnviron
	processEnviron = osProcessEnviron
	t.Cleanup(func() { processEnviron = orig })

	// A server started the way agent-net started it before v0.5.36: the
	// owner's config, so the sidebar shows.
	server := exec.Command(bin, "--session", agentNetHerdrSession, "server")
	server.Env = herdrCleanEnv()
	server.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	go server.Wait()
	t.Cleanup(func() {
		stop := exec.Command(bin, "--session", agentNetHerdrSession, "server", "stop")
		stop.Env = herdrCleanEnv()
		_ = stop.Run()
	})
	d := &Daemon{}
	var socket string
	waitFor(t, "agent-net's herdr server", 10*time.Second, func() bool {
		s, running, err := d.agentNetHerdrSocket(bin)
		socket = s
		return err == nil && running
	})
	oldPID, err := herdrServerPID(socket)
	if err != nil {
		t.Fatal(err)
	}

	tmuxSocket := filepath.Join(root, "tmux.sock")
	tm := func(args ...string) string {
		out, _ := exec.Command(tmux, append([]string{"-S", tmuxSocket}, args...)...).CombinedOutput()
		return string(out)
	}
	t.Cleanup(func() { tm("kill-server") })
	screen := func() string { return tm("capture-pane", "-p", "-t", "owner") }
	// attach opens herdr in a fresh terminal of the given size, the way the
	// owner opens agent-net's herdr, and waits until the pane shows want.
	attach := func(w, h int, want string) string {
		tm("new-session", "-d", "-x", fmt.Sprint(w), "-y", fmt.Sprint(h), "-s", "owner",
			"env -u HERDR_SESSION -u HERDR_SOCKET_PATH -u HERDR_CONFIG_PATH "+shellQuote(bin)+" --session agent-net")
		var frame string
		waitFor(t, fmt.Sprintf("%q on the %dx%d screen", want, w, h), 10*time.Second, func() bool {
			frame = screen()
			return strings.Contains(frame, want)
		})
		return frame
	}
	detach := func() {
		tm("kill-session", "-t", "owner")
		waitFor(t, "the herdr client to leave", 10*time.Second, func() bool {
			var title struct {
				Reason string `json:"reason"`
			}
			return herdrAPI(socket, "client.window_title.clear", struct{}{}, &title) == nil && title.Reason == "no_foreground_client"
		})
	}
	const sidebar = "agents" // the sidebar's agent list heading

	// Step 1: the owner opens agent-net's herdr and runs a command in its pane.
	attach(120, 34, "$")
	tm("send-keys", "-t", "owner", "echo MARKER-$$", "Enter")
	frame := attach2(t, screen, "MARKER-")
	if !strings.Contains(frame, sidebar) {
		t.Fatalf("the old server shows no sidebar:\n%s", frame)
	}
	writeSidebarProof(t, 120, 34, "before", "open agent-net's herdr, served by a server without agent-net's config, and run echo MARKER-$$", "the sidebar shows on the left", frame)

	// Step 2: the daemon checks the server while the owner is attached.
	note := d.adoptAgentNetHerdr(bin, socket)
	if !strings.HasPrefix(note, "Press prefix+b to hide the herdr sidebar.") {
		t.Fatalf("note %q", note)
	}
	if pid, _ := herdrServerPID(socket); pid != oldPID {
		t.Fatalf("the server changed while a client was attached: %d -> %d", oldPID, pid)
	}
	frame = screen()
	if !strings.Contains(frame, "MARKER-") || !strings.Contains(frame, sidebar) {
		t.Fatalf("the attached client changed:\n%s", frame)
	}
	writeSidebarProof(t, 120, 34, "attached", "open a session in the TUI while this herdr window stays open",
		"nothing changes in herdr; the TUI shows once: "+note, frame)
	detach()
	frame = attach(80, 24, "MARKER-")
	writeSidebarProof(t, 80, 24, "before", "open agent-net's herdr at 80x24, before the daemon moved the server", "the sidebar shows on the left", frame)
	detach()

	// Step 3: with no herdr window open, the next open moves the panes to a
	// server with agent-net's config.
	if note := d.adoptAgentNetHerdr(bin, socket); note != "" {
		t.Fatalf("note %q after the clients left", note)
	}
	newPID, err := herdrServerPID(socket)
	if err != nil || newPID == oldPID || !herdrServerConfigured(newPID) {
		t.Fatalf("server pid %d -> %d (%v), configured %v", oldPID, newPID, err, herdrServerConfigured(newPID))
	}
	for _, size := range []struct{ w, h int }{{120, 34}, {80, 24}} {
		frame = attach(size.w, size.h, "MARKER-")
		if strings.Contains(frame, sidebar) {
			t.Fatalf("%dx%d: the sidebar still shows:\n%s", size.w, size.h, frame)
		}
		writeSidebarProof(t, size.w, size.h, "after", "close the herdr window, open a session in the TUI, then open agent-net's herdr again",
			fmt.Sprintf("no sidebar; the pane and its shell (with MARKER) are still there; server pid %d -> %d", oldPID, newPID), frame)
		detach()
	}
}

// attach2 waits until the screen shows want and returns it.
func attach2(t *testing.T, screen func() string, want string) string {
	t.Helper()
	var frame string
	waitFor(t, fmt.Sprintf("%q on the screen", want), 10*time.Second, func() bool {
		frame = screen()
		return strings.Count(frame, want) >= 2 // the command and its output
	})
	return frame
}

func writeSidebarProof(t *testing.T, w, h int, step, action, expected, frame string) {
	t.Helper()
	dir := os.Getenv("AGENT_NET_PROOF_DIR")
	if dir == "" {
		return
	}
	capture := fmt.Sprintf("build: %s\nsize: %dx%d\nstep: %s\naction: %s\nexpected: %s\nerrors: none\n\n%s", os.Getenv("AGENT_NET_PROOF_BUILD"), w, h, step, action, expected, frame)
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("herdr-sidebar-%s-%dx%d.txt", step, w, h)), []byte(capture), 0o600); err != nil {
		t.Fatal(err)
	}
}

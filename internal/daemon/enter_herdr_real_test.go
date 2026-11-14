package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// TestEnterHerdrTakesOverRealAttach runs a real herdr, in a temporary config
// folder of its own, and shows that Enter of a Pi session takes the terminal
// over from a stale client instead of failing with "already has an attached
// client". A tmux server on its own socket plays the owner's terminals: the
// first client stays attached, and the second one presses Enter (the same
// plan argv). The first client must end and the second must show the pane.
// scripts/herdr-takeover-proof.sh runs it and keeps the frames at 80x24 and
// 120x34.
func TestEnterHerdrTakesOverRealAttach(t *testing.T) {
	// TestMain hides the machine's herdr from every test; this one gets it
	// only when asked by name.
	bin := os.Getenv("AGENT_NET_HERDR_BIN")
	if !filepath.IsAbs(bin) || !isExecutable(bin) {
		t.Skip("set AGENT_NET_HERDR_BIN to a herdr binary to run against a real herdr (scripts/herdr-takeover-proof.sh)")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}
	// Socket paths must stay short, so not under t.TempDir.
	root, err := os.MkdirTemp("", "hto")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	home, xdg := filepath.Join(root, "home"), filepath.Join(root, "xdg")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("SHELL", "/bin/sh") // TestMain sets a fake shell; herdr panes run SHELL
	t.Setenv("PS1", "$ ")
	for _, k := range []string{"HERDR_CONFIG_PATH", "HERDR_SESSION", "HERDR_SOCKET_PATH", "HERDR_ENV", "HERDR_PANE_ID", "HERDR_TAB_ID", "HERDR_WORKSPACE_ID", "TMUX"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeHerdrStateFile(filepath.Join(xdg, "herdr", "config.toml"), []byte("onboarding = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The test reads the real environment of the pane's process.
	orig := processEnviron
	processEnviron = osProcessEnviron
	t.Cleanup(func() { processEnviron = orig })

	const session = "agent-net-enter-test"
	server := exec.Command(bin, "--session", session, "server")
	server.Env = herdrCleanEnv()
	server.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	go server.Wait()
	t.Cleanup(func() {
		stop := exec.Command(bin, "--session", session, "server", "stop")
		stop.Env = herdrCleanEnv()
		_ = stop.Run()
	})
	// Every herdr call uses the test's own server and none of the agent
	// variables of the process that runs the test.
	herdr := func(args ...string) (string, error) {
		cmd := exec.Command(bin, append([]string{"--session", session}, args...)...)
		cmd.Env = herdrCleanEnv()
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	waitFor(t, "the test herdr server", 10*time.Second, func() bool {
		_, err := herdr("pane", "list")
		return err == nil
	})

	// A workspace with one pane, which runs a process that stays alive so the
	// pane has a live terminal to attach.
	out, err := herdr("workspace", "create", "--label", "pi")
	if err != nil {
		t.Fatalf("workspace create: %v\n%s", err, out)
	}
	var created struct {
		Result struct {
			RootPane struct {
				PaneID     string `json:"pane_id"`
				TerminalID string `json:"terminal_id"`
				TabID      string `json:"tab_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.Result.RootPane.PaneID == "" {
		t.Fatalf("workspace create answer %q: %v", out, err)
	}
	pane := created.Result.RootPane.PaneID
	// A script keeps the pane's command free of quoting, which herdr's
	// `pane run` would pass through the pane's shell.
	script := filepath.Join(root, "pi-session.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'ATTACH-MARKER\\n'\nsleep 600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := herdr("pane", "run", pane, script); err != nil {
		t.Fatalf("pane run: %v\n%s", err, out)
	}

	// The pane's foreground process plays the Pi session. Its environment
	// carries HERDR_ENV, so the daemon finds the pane from the process alone.
	d := &Daemon{cfg: Config{HarnessPaths: map[string]string{"herdr": bin}}}
	var plan protocol.EnterPlan
	waitFor(t, "the pane's process", 10*time.Second, func() bool {
		raw, err := herdr("pane", "process-info", "--pane", pane)
		if err != nil {
			return false
		}
		var info struct {
			Result struct {
				Info struct {
					Procs []struct {
						PID int `json:"pid"`
					} `json:"foreground_processes"`
				} `json:"process_info"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(raw), &info) != nil || len(info.Result.Info.Procs) == 0 {
			return false
		}
		pid := info.Result.Info.Procs[len(info.Result.Info.Procs)-1].PID
		var ok bool
		plan, ok = d.herdrPlan(protocol.SessionInfo{PID: pid}, protocol.EnterRequest{}, protocol.EnterPlan{Host: "test-host"}, "pi session")
		return ok && len(plan.Argv) > 0
	})
	if got := plan.Argv[len(plan.Argv)-1]; got != "--takeover" {
		t.Fatalf("Enter does not take the terminal over: %v", plan.Argv)
	}

	tmuxSock := filepath.Join(root, "tmux.sock")
	tm := func(args ...string) string {
		out, _ := exec.Command(tmux, append([]string{"-S", tmuxSock}, args...)...).CombinedOutput()
		return string(out)
	}
	t.Cleanup(func() { tm("kill-server") })
	screen := func(name string) string { return tm("capture-pane", "-p", "-t", name) }
	sessions := func() string {
		return strings.TrimSpace(tm("list-sessions", "-F", "#{session_name}"))
	}
	clientCommand := func() string {
		q := make([]string, len(plan.Argv))
		for i, a := range plan.Argv {
			q[i] = shellQuote(a)
		}
		return "exec env -i HOME=" + shellQuote(home) + " XDG_CONFIG_HOME=" + shellQuote(xdg) +
			" PATH=" + shellQuote(os.Getenv("PATH")) + " TERM=xterm-256color LANG=C.UTF-8 " + strings.Join(q, " ")
	}
	attach := func(name string, w, h int) {
		tm("new-session", "-d", "-x", fmt.Sprint(w), "-y", fmt.Sprint(h), "-s", name, clientCommand())
		waitFor(t, name+" shows the pane", 10*time.Second, func() bool {
			return strings.Contains(screen(name), "ATTACH-MARKER")
		})
	}
	gones := func(name string) bool {
		return !strings.Contains(tm("list-sessions", "-F", "#{session_name}"), name+"\n")
	}

	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		// Step 1: a client is already attached, from an earlier Enter.
		attach("first", size.w, size.h)
		writeTakeoverProof(t, size.w, size.h, "stale-client",
			"leave a client attached to the Pi session's terminal, as an earlier Enter or a closed window does",
			"the TUI shows the pane while the stale client holds it",
			"tmux sessions: "+sessions()+"\n\n"+screen("first"))

		// Step 2: Enter runs the daemon's plan. It takes the terminal over:
		// the stale client ends and this one shows the session.
		tm("new-session", "-d", "-x", fmt.Sprint(size.w), "-y", fmt.Sprint(size.h), "-s", "second", clientCommand())
		waitFor(t, "the stale client to end", 10*time.Second, func() bool { return gones("first") })
		waitFor(t, "the new client to show the pane", 10*time.Second, func() bool {
			return strings.Contains(screen("second"), "ATTACH-MARKER")
		})
		writeTakeoverProof(t, size.w, size.h, "enter-takes-over",
			"press Enter on the Pi session while the stale client is still attached",
			"the newest client shows the session and the stale client has ended",
			"tmux sessions: "+sessions()+"\n\n"+screen("second"))

		tm("kill-session", "-t", "second")
	}
}

func writeTakeoverProof(t *testing.T, w, h int, step, action, expected, frame string) {
	t.Helper()
	dir := os.Getenv("AGENT_NET_PROOF_DIR")
	if dir == "" {
		return
	}
	capture := fmt.Sprintf("build: %s\nsize: %dx%d\nstep: %s\naction: %s\nexpected: %s\nerrors: none\n\n%s",
		os.Getenv("AGENT_NET_PROOF_BUILD"), w, h, step, action, expected, frame)
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("herdr-takeover-%s-%dx%d.txt", step, w, h)), []byte(capture), 0o600); err != nil {
		t.Fatal(err)
	}
}

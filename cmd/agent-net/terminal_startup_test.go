package main

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/creack/pty"
)

func TestNonTUIStartupDoesNotQueryTerminalBackground(t *testing.T) {
	if os.Getenv("AGENT_NET_TEA_INIT_HELPER") == "1" {
		os.Exit(0)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestNonTUIStartupDoesNotQueryTerminalBackground$")
	cmd.Env = childEnv("AGENT_NET_TEA_INIT_HELPER=1", "TERM=xterm-256color")
	terminal, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	// Answer any OSC and cursor-position queries promptly so the regression
	// test does not wait for termenv's five-second timeout.
	if _, err := terminal.Write([]byte("\x1b]11;rgb:0000/0000/0000\x1b\\\x1b[1;1R")); err != nil {
		t.Fatal(err)
	}
	output, readErr := io.ReadAll(terminal)
	terminal.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child test process: %v; output %q", err, output)
	}
	if readErr != nil && !strings.Contains(readErr.Error(), "input/output error") {
		t.Fatalf("read terminal: %v", readErr)
	}
	if strings.Contains(string(output), "\x1b]11;?") {
		t.Fatalf("non-TUI startup queried the terminal background: %q", output)
	}
}

func childEnv(values ...string) []string {
	env := os.Environ()
	for i := 0; i < len(env); {
		if strings.HasPrefix(env[i], "CI=") || strings.HasPrefix(env[i], "NO_COLOR=") {
			env = append(env[:i], env[i+1:]...)
			continue
		}
		i++
	}
	for _, value := range values {
		key, _, _ := strings.Cut(value, "=")
		prefix := key + "="
		for i, current := range env {
			if strings.HasPrefix(current, prefix) {
				env[i] = value
				goto next
			}
		}
		env = append(env, value)
	next:
	}
	return env
}

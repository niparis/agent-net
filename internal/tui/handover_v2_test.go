package tui

import (
	"bytes"
	"io"
	"os"
	osexec "os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/creack/pty"
	"github.com/niparis/agent-net/internal/protocol"
)

// Exercise the real Bubble Tea release/restore path, not a mocked tea.Exec.
func TestV2TerminalHandoverReturnsOnCtrlBracket(t *testing.T) {
	if os.Getenv("AGENT_NET_HANDOVER_TEST_CHILD") == "1" {
		m, src := workerModel(80, 24)
		m = selectID(m, workerID)
		src.enterPlan = protocol.EnterPlan{Argv: []string{"sh", "-c", "printf HANDOVER_READY; exec cat"}}
		p := tea.NewProgram(m, tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
		if _, err := p.Run(); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	cmd := osexec.Command(os.Args[0], "-test.run=^TestV2TerminalHandoverReturnsOnCtrlBracket$")
	cmd.Env = append(os.Environ(), "AGENT_NET_HANDOVER_TEST_CHILD=1", "TERM=xterm-256color", "GORACE=atexit_sleep_ms=0")
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	var mu sync.Mutex
	var out bytes.Buffer
	doneReading := make(chan struct{})
	go func() {
		defer close(doneReading)
		b := make([]byte, 4096)
		for {
			n, e := terminal.Read(b)
			if n > 0 {
				mu.Lock()
				_, _ = out.Write(b[:n])
				mu.Unlock()
			}
			if e != nil {
				return
			}
		}
	}()
	snapshot := func() string { mu.Lock(); defer mu.Unlock(); return out.String() }
	waitFor := func(label string, pred func(string) bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if pred(snapshot()) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s; terminal output %q", label, snapshot())
	}
	waitFor("TUI", func(s string) bool { return strings.Contains(s, "Sessions") })
	if _, err := io.WriteString(terminal, "\r"); err != nil {
		t.Fatal(err)
	}
	waitFor("session", func(s string) bool { return strings.Contains(s, "HANDOVER_READY") })
	before := strings.Count(snapshot(), "Sessions")
	if _, err := terminal.Write([]byte{0x1d}); err != nil {
		t.Fatal(err)
	}
	waitFor("restored TUI", func(s string) bool { return strings.Count(s, "Sessions") > before })
	if _, err := io.WriteString(terminal, "q"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("TUI exit: %v; output %q", err, snapshot())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TUI did not respond after handover")
	}
	_ = terminal.Close()
	<-doneReading
}

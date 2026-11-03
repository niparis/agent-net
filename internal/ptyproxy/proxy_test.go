package ptyproxy

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
)

func TestProxy(t *testing.T) {
	if os.Getenv("PTY_PROXY_CHILD") == "1" {
		b := make([]byte, 3)
		_, _ = io.ReadFull(os.Stdin, b)
		_, _ = os.Stdout.Write(append([]byte("child:"), b...))
		select {}
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestProxy")
	cmd.Env = append(os.Environ(), "PTY_PROXY_CHILD=1", "GORACE=atexit_sleep_ms=0")
	input, keys := io.Pipe()
	defer input.Close()
	defer keys.Close()
	output, screen := io.Pipe()
	defer output.Close()
	defer screen.Close()
	p := New(cmd)
	p.SetStdin(input)
	p.SetStdout(screen)
	done := make(chan error, 1)
	go func() { done <- p.Run() }()
	go func() { _, _ = keys.Write([]byte("abc\n")) }()
	reader := bufio.NewReader(output)
	var seen strings.Builder
	for !strings.Contains(seen.String(), "child:abc") {
		b, err := reader.ReadByte()
		if err != nil {
			t.Fatal(err)
		}
		seen.WriteByte(b)
	}
	go func() { _, _ = io.Copy(io.Discard, reader) }()
	_, _ = keys.Write([]byte{0x1d})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxy did not return")
	}
}

func TestTitleRestored(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cmd     *exec.Cmd
		input   string
		wantErr bool
	}{
		{"normal", exec.Command("sh", "-c", "exit 0"), "", false},
		{"escape", exec.Command("sh", "-c", "exec sleep 30"), "\x1d", false},
		{"start error", exec.Command("/no-such-agent-net-command"), "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			p := New(tc.cmd)
			p.Title = "session - ctrl+] to come back"
			p.SetStdin(strings.NewReader(tc.input))
			p.SetStdout(&out)
			err := p.Run()
			if (err != nil) != tc.wantErr {
				t.Fatalf("error %v", err)
			}
			got := out.String()
			prefix := "\x1b[22;0t\x1b]0;session - ctrl+] to come back\a"
			suffix := "\x1b[<u\x1b[>4;0m\x1b[23;0t"
			if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
				t.Fatalf("title sequences: %q", got)
			}
		})
	}
}

func TestEntryNoticeBeforeChildStarts(t *testing.T) {
	var out bytes.Buffer
	p := New(exec.Command("/no-such-agent-net-command"))
	p.Name = "session"
	p.SetStdin(strings.NewReader(""))
	p.SetStdout(&out)
	if err := p.Run(); err == nil {
		t.Fatal("expected start error")
	}
	if !strings.Contains(out.String(), "agent-net: entering session. Press ctrl+] to come back here.\r\n") {
		t.Fatalf("output %q", out.String())
	}
}

func TestEscapeMatcher(t *testing.T) {
	// Every form of ctrl+] a terminal may send, including the kitty keyboard
	// protocol with Caps Lock and Num Lock bits (64 and 128) added to the
	// modifier value, alternate keys, associated text, and the control code 29.
	for _, seq := range []string{
		"\x1d",
		"\x1b[93;5u",
		"\x1b[93;5:1u",
		"\x1b[27;5;93~",
		"\x1b[93;69u",  // Caps Lock
		"\x1b[93;133u", // Num Lock
		"\x1b[93;197u", // both
		"\x1b[93;69:1u",
		"\x1b[93;133:1u",
		"\x1b[93;197:1u",
		"\x1b[27;69;93~",
		"\x1b[27;133;93~",
		"\x1b[27;197;93~",
		"\x1b[93:125;5u",    // shifted alternate key
		"\x1b[93::93;5u",    // base-layout alternate key
		"\x1b[93:125;69u",   // alternate key and Caps Lock
		"\x1b[93;5;29u",     // associated text
		"\x1b[93;5;93u",     // associated text for the key itself
		"\x1b[93;5;29:93u",  // several text codepoints
		"\x1b[93:125;5;29u", // alternate key and text
		"\x1b[29;5u",        // the control code instead of the key
		"\x1b[29;69u",
		"\x1b[29;5:1u",
	} {
		for split := 0; split <= len(seq); split++ {
			t.Run(fmt.Sprintf("%q/split%d", seq, split), func(t *testing.T) {
				var m escapeMatcher
				var out bytes.Buffer
				if m.write(&out, []byte("before"+seq[:split])) {
					if split != len(seq) {
						t.Fatal("escaped early")
					}
				} else if !m.write(&out, []byte(seq[split:]+"after")) {
					t.Fatal("did not escape")
				}
				if out.String() != "before" {
					t.Fatalf("forwarded %q", out.String())
				}
			})
		}
	}
}

func TestEscapeMatcherSwallowsRepeatAndRelease(t *testing.T) {
	// Repeat (2) and release (3) events of ctrl+] must not reach the child and
	// must not trigger the escape.
	for _, seq := range []string{
		"\x1b[93;5:3u",
		"\x1b[93;5:2u",
		"\x1b[93;69:3u",
		"\x1b[93;133:2u",
		"\x1b[93;197:3u",
		"\x1b[93;5:3;29u",
		"\x1b[93;5:2;29u",
		"\x1b[93:125;5:3u",
		"\x1b[29;5:3u",
		"\x1b[29;69:2u",
	} {
		for split := 0; split <= len(seq); split++ {
			t.Run(fmt.Sprintf("%q/split%d", seq, split), func(t *testing.T) {
				var m escapeMatcher
				var out bytes.Buffer
				if m.write(&out, []byte("before"+seq[:split])) {
					t.Fatal("escaped on a repeat or release")
				}
				if m.write(&out, []byte(seq[split:]+"after")) {
					t.Fatal("escaped on a repeat or release")
				}
				if got, want := out.String(), "beforeafter"; got != want {
					t.Fatalf("forwarded %q, want %q", got, want)
				}
			})
		}
	}
}

func TestEscapeMatcherOtherInput(t *testing.T) {
	// Everything that is not ctrl+] passes through byte for byte.
	for _, seq := range []string{
		"]",
		"\x1b[93;6u",    // ctrl+shift+]
		"\x1b[93;70u",   // ctrl+shift+] with Caps Lock
		"\x1b[27;6;93~", // modifyOtherKeys ctrl+shift+]
		"\x1b[97;5u",    // kitty ctrl+a
		"\x1b[97;69u",   // kitty ctrl+a with Caps Lock
		"\x1b[27;5;97~", // modifyOtherKeys ctrl+a
		"\x1b[A",        // up arrow
		"\x1b[93;5:4u",  // unknown event type
		"\x1b[93;5x",    // malformed sequence
	} {
		for split := 0; split <= len(seq); split++ {
			t.Run(fmt.Sprintf("%q/split%d", seq, split), func(t *testing.T) {
				var m escapeMatcher
				var out bytes.Buffer
				if m.write(&out, []byte("before"+seq[:split])) {
					t.Fatal("unexpected escape")
				}
				if m.write(&out, []byte(seq[split:]+"after")) {
					t.Fatal("unexpected escape")
				}
				if got, want := out.String(), "before"+seq+"after"; got != want {
					t.Fatalf("got %q, want %q", got, want)
				}
			})
		}
	}
}

func TestEscapeMatcherSequenceBeforeEscape(t *testing.T) {
	var m escapeMatcher
	var out bytes.Buffer
	if !m.write(&out, []byte("a\x1b[93;5:3ub\x1d")) {
		t.Fatal("did not escape")
	}
	if got, want := out.String(), "ab"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEscapeMatcherFlushPartial(t *testing.T) {
	for _, partial := range []string{"\x1b", "\x1b[93;5", "\x1b[27;5;"} {
		var m escapeMatcher
		var out bytes.Buffer
		if m.write(&out, []byte(partial)) {
			t.Fatalf("%q triggered early", partial)
		}
		m.flush(&out)
		if got := out.String(); got != partial {
			t.Fatalf("got %q, want %q", got, partial)
		}
	}
}

func TestProxyEscapeTiming(t *testing.T) {
	if os.Getenv("PTY_PROXY_TIMING_CHILD") == "1" {
		_, _ = term.MakeRaw(os.Stdin.Fd())
		_, _ = os.Stdout.WriteString("ready")
		b := make([]byte, 1)
		if _, err := os.Stdin.Read(b); err == nil {
			_, _ = os.Stdout.Write(append([]byte("received:"), b...))
		}
		select {}
	}
	for _, split := range []bool{false, true} {
		t.Run(fmt.Sprintf("split=%v", split), func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestProxyEscapeTiming$")
			cmd.Env = append(os.Environ(), "PTY_PROXY_TIMING_CHILD=1", "GORACE=atexit_sleep_ms=0")
			input, keys := io.Pipe()
			output, screen := io.Pipe()
			defer input.Close()
			defer keys.Close()
			defer output.Close()
			defer screen.Close()
			p := New(cmd)
			p.SetStdin(input)
			p.SetStdout(screen)
			done := make(chan error, 1)
			go func() { done <- p.Run() }()
			seen := make(chan string, 1)
			go func() {
				reader := bufio.NewReader(output)
				var text strings.Builder
				for {
					b, err := reader.ReadByte()
					if err != nil {
						return
					}
					text.WriteByte(b)
					if strings.HasSuffix(text.String(), "ready") || strings.HasSuffix(text.String(), "received:\x1b") {
						seen <- text.String()
					}
				}
			}()
			select {
			case <-seen:
			case <-time.After(2 * time.Second):
				t.Fatal("child not ready")
			}
			_, _ = keys.Write([]byte("\x1b"))
			if split {
				time.Sleep(5 * time.Millisecond)
				_, _ = keys.Write([]byte("[93;5u"))
			} else {
				select {
				case <-seen:
				case <-time.After(100 * time.Millisecond):
					t.Error("escape not delivered")
				}
				_, _ = keys.Write([]byte("\x1d"))
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("proxy did not return")
			}
		})
	}
}

// A tmux attach client is left with ctrl+] the same way as any other client.
// The private server keeps its session, so returning to the TUI does not end
// what the owner was watching.
func TestTmuxAttachCtrlPlusReturnsAndKeepsSession(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}
	dir, err := os.MkdirTemp("", "antmux")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	name := "agent-net-proxy-" + fmt.Sprint(os.Getpid())
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_TMPDIR=") {
			continue // never reach the tmux server of the person running the tests
		}
		env = append(env, kv)
	}
	env = append(env, "TMUX_TMPDIR="+dir, "TERM=xterm-256color")
	command := func(args ...string) *exec.Cmd {
		c := exec.Command(tmux, args...)
		c.Env = env
		return c
	}
	if out, err := command("-L", name, "new-session", "-d", "-c", dir, "--", "sleep", "30").CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = command("-L", name, "kill-server").Run() })
	input, keys := io.Pipe()
	defer input.Close()
	defer keys.Close()
	output, screen := io.Pipe()
	defer output.Close()
	defer screen.Close()
	go func() { _, _ = io.Copy(io.Discard, output) }()
	p := New(command("-L", name, "attach-session", "-t", "0"))
	p.SetStdin(input)
	p.SetStdout(screen)
	done := make(chan error, 1)
	go func() { done <- p.Run() }()
	time.Sleep(200 * time.Millisecond)
	if _, err := keys.Write([]byte{0x1d}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxy did not return from the tmux attach")
	}
	if out, err := command("-L", name, "has-session").CombinedOutput(); err != nil {
		t.Fatalf("the attach client ended the tmux session: %v\n%s", err, out)
	}
}

// TestBarChild is the fake session of the bar tests. It reports its own PTY
// size whenever it changes. With PTY_PROXY_BAR_CHILD=clear it clears the
// screen once, as a full-screen child does, and the bar must come back.
func TestBarChild(t *testing.T) {
	mode := os.Getenv("PTY_PROXY_BAR_CHILD")
	if mode == "" {
		t.Skip("only runs as the child of the bar tests")
	}
	if mode == "clear" {
		_, _ = os.Stdout.WriteString("\x1b[2J")
	}
	last := ""
	for {
		cols, rows, err := term.GetSize(os.Stdout.Fd())
		if err != nil {
			os.Exit(1)
		}
		now := fmt.Sprintf("size:%dx%d\n", cols, rows)
		if now != last {
			_, _ = os.Stdout.WriteString(now)
			last = now
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// lockedOutput collects what the proxy writes to the real terminal.
type lockedOutput struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (o *lockedOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.Write(p)
}

func (o *lockedOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.String()
}

func (o *lockedOutput) count(want string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return strings.Count(o.b.String(), want)
}

func waitForOutput(t *testing.T, out *lockedOutput, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no %q in the terminal output %q", want, out.String())
}

func waitForCount(t *testing.T, out *lockedOutput, want string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if out.count(want) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%q appears %d times, want %d, in %q", want, out.count(want), n, out.String())
}

type barRun struct {
	master *os.File
	out    *lockedOutput
	done   chan error
}

// startBarProxy runs a proxy on a real PTY of rows by cols. Never touches the
// terminal of the person running the tests.
func startBarProxy(t *testing.T, rows, cols int, name, mode string) *barRun {
	t.Helper()
	master, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	if err := pty.Setsize(master, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestBarChild$")
	cmd.Env = append(os.Environ(), "PTY_PROXY_BAR_CHILD="+mode, "GORACE=atexit_sleep_ms=0")
	p := New(cmd)
	p.Name = name
	p.SetStdin(tty)
	p.SetStdout(tty)
	r := &barRun{master: master, out: &lockedOutput{}, done: make(chan error, 1)}
	go func() { r.done <- p.Run() }()
	go func() { _, _ = io.Copy(r.out, master) }()
	t.Cleanup(func() {
		_ = master.Close()
		_ = tty.Close()
	})
	return r
}

func (r *barRun) resize(t *testing.T, rows, cols int) {
	t.Helper()
	if err := pty.Setsize(r.master, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
}

func (r *barRun) escape(t *testing.T) {
	t.Helper()
	if _, err := r.master.Write([]byte{0x1d}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-r.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxy did not return from ctrl+]")
	}
}

// The child gets one row less than the real terminal, at start and after a
// resize, and the bar sits on the row the child cannot reach.
func TestBarReservesTheBottomRow(t *testing.T) {
	r := startBarProxy(t, 30, 100, "session", "size")
	waitForOutput(t, r.out, "size:100x29")
	waitForOutput(t, r.out, "\x1b[30;1H\x1b[2m"+barText("session"))

	r.resize(t, 24, 90)
	waitForOutput(t, r.out, "size:90x23")
	waitForOutput(t, r.out, "\x1b[24;1H\x1b[2m"+barText("session"))

	r.escape(t)
	waitForOutput(t, r.out, "\x1b[24;1H\x1b[2K")
}

// A clear-screen from the child wipes the last row, so the bar is drawn again
// after the child has repainted.
func TestBarComesBackAfterAClearScreen(t *testing.T) {
	r := startBarProxy(t, 30, 100, "session", "clear")
	drawn := "\x1b[30;1H\x1b[2m" + barText("session")
	waitForCount(t, r.out, drawn, 2)
	r.escape(t)
}

// AGENT_NET_NO_BAR=1 leaves the plain terminal alone: the child keeps every
// row and no bar is ever drawn.
func TestBarOffWithEnv(t *testing.T) {
	t.Setenv("AGENT_NET_NO_BAR", "1")
	r := startBarProxy(t, 20, 80, "session", "size")
	waitForOutput(t, r.out, "size:80x20")
	r.escape(t)
	if got := r.out.String(); strings.Contains(got, "agent-net · ctrl+] back") || strings.Contains(got, "\x1b[2K") {
		t.Fatalf("bar drawn with AGENT_NET_NO_BAR=1: %q", got)
	}
}

func TestClearsScreen(t *testing.T) {
	for _, seq := range []string{"\x1b[2J", "\x1b[3J", "\x1b[J", "\x1b[?1049h", "\x1b[?1049l", "\x1bc"} {
		if !clearsScreen([]byte("before" + seq + "after")) {
			t.Errorf("%q not detected", seq)
		}
	}
	for _, seq := range []string{"\x1b[H", "\x1b[K", "plain text", "\x1b[1;1H"} {
		if clearsScreen([]byte(seq)) {
			t.Errorf("%q wrongly detected as a clear", seq)
		}
	}
}

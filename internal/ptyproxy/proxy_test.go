package ptyproxy

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
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

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
	cmd.Env = append(os.Environ(), "PTY_PROXY_CHILD=1")
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
	for _, seq := range []string{"\x1d", "\x1b[93;5u", "\x1b[93;5:1u", "\x1b[27;5;93~"} {
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

func TestEscapeMatcherOtherInput(t *testing.T) {
	var m escapeMatcher
	var out bytes.Buffer
	for _, part := range []string{"a\x1b[93;5:", "3ub\x1b[97;5u\x1b[<0;12;4M", "paste"} {
		if m.write(&out, []byte(part)) {
			t.Fatal("unexpected escape")
		}
	}
	if got, want := out.String(), "ab\x1b[97;5u\x1b[<0;12;4Mpaste"; got != want {
		t.Fatalf("got %q, want %q", got, want)
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
			cmd.Env = append(os.Environ(), "PTY_PROXY_TIMING_CHILD=1")
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

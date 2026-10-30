// Package ptyproxy runs an interactive attach client with a common escape key.
package ptyproxy

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"github.com/muesli/cancelreader"
)

type Proxy struct {
	Cmd   *exec.Cmd
	Title string
	Name  string
	in    io.Reader
	out   io.Writer
}

func New(cmd *exec.Cmd) *Proxy         { return &Proxy{Cmd: cmd} }
func (p *Proxy) SetStdin(r io.Reader)  { p.in = r }
func (p *Proxy) SetStdout(w io.Writer) { p.out = w }
func (p *Proxy) SetStderr(w io.Writer) {}
func (p *Proxy) Run() error {
	_, _ = io.WriteString(p.out, "\x1b[22;0t\x1b]0;"+p.Title+"\a")
	defer io.WriteString(p.out, "\x1b[<u\x1b[>4;0m\x1b[23;0t")
	_, _ = io.WriteString(p.out, "agent-net: entering "+p.Name+". Press ctrl+] to come back here.\r\n")
	input, err := cancelreader.NewReader(p.in)
	if err != nil {
		return err
	}
	defer input.Close()
	defer input.Cancel()
	pt, err := pty.Start(p.Cmd)
	if err != nil {
		return err
	}
	defer pt.Close()
	if f, ok := p.in.(*os.File); ok {
		if state, e := term.MakeRaw(f.Fd()); e == nil {
			defer term.Restore(f.Fd(), state)
		}
		resize := func() { _ = pty.InheritSize(f, pt) }
		resize()
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGWINCH)
		defer signal.Stop(ch)
		done := make(chan struct{})
		defer close(done)
		go func() {
			for {
				select {
				case <-ch:
					resize()
				case <-done:
					return
				}
			}
		}()
	}
	outputDone := make(chan struct{})
	go func() { _, _ = io.Copy(p.out, pt); close(outputDone) }()
	escaped := make(chan struct{}, 1)
	go func() {
		b := make([]byte, 4096)
		var matcher escapeMatcher
		for {
			n, e := input.Read(b)
			if n > 0 && matcher.write(pt, b[:n]) {
				escaped <- struct{}{}
				return
			}
			if e != nil {
				_, _ = pt.Write(matcher.pending)
				return
			}
		}
	}()
	wait := make(chan error, 1)
	go func() { wait <- p.Cmd.Wait() }()
	select {
	case err = <-wait:
	case <-escaped:
		_ = p.Cmd.Process.Signal(syscall.SIGHUP)
		select {
		case <-wait:
		case <-time.After(time.Second):
			_ = p.Cmd.Process.Kill()
			<-wait
		}
		err = nil
	}
	_ = pt.Close()
	<-outputDone
	return err
}

// escapeMatcher buffers only prefixes of the escape and release sequences.
type escapeMatcher struct{ pending []byte }

func (m *escapeMatcher) write(out io.Writer, input []byte) bool {
	sequences := []string{"\x1d", "\x1b[93;5u", "\x1b[93;5:1u", "\x1b[27;5;93~", "\x1b[93;5:3u"}
	var forward []byte
	defer func() { _, _ = out.Write(forward) }()
	for _, c := range input {
		m.pending = append(m.pending, c)
		for len(m.pending) > 0 {
			prefix := false
			for i, seq := range sequences {
				if bytes.Equal(m.pending, []byte(seq)) {
					m.pending = m.pending[:0]
					if i != len(sequences)-1 {
						return true
					}
					prefix = true
					break
				}
				if bytes.HasPrefix([]byte(seq), m.pending) {
					prefix = true
				}
			}
			if prefix {
				break
			}
			forward = append(forward, m.pending[0])
			m.pending = m.pending[1:]
		}
	}
	return false
}

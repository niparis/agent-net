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
	in    io.Reader
	out   io.Writer
}

func New(cmd *exec.Cmd) *Proxy         { return &Proxy{Cmd: cmd} }
func (p *Proxy) SetStdin(r io.Reader)  { p.in = r }
func (p *Proxy) SetStdout(w io.Writer) { p.out = w }
func (p *Proxy) SetStderr(w io.Writer) {}
func (p *Proxy) Run() error {
	_, _ = io.WriteString(p.out, "\x1b[22;0t\x1b]0;"+p.Title+"\a")
	defer io.WriteString(p.out, "\x1b[23;0t")
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
		for {
			n, e := input.Read(b)
			if n > 0 {
				i := bytes.IndexByte(b[:n], 0x1d)
				if i >= 0 {
					_, _ = pt.Write(b[:i])
					escaped <- struct{}{}
					return
				}
				_, _ = pt.Write(b[:n])
			}
			if e != nil {
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

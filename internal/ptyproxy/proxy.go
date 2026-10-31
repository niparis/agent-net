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
	inputDone := make(chan struct{})
	defer close(inputDone)
	type readResult struct {
		data []byte
		err  error
	}
	reads := make(chan readResult)
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := input.Read(b)
			result := readResult{data: append([]byte(nil), b[:n]...), err: err}
			select {
			case reads <- result:
			case <-inputDone:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		var matcher escapeMatcher
		var timer *time.Timer
		var timeout <-chan time.Time
		defer func() {
			if timer != nil {
				timer.Stop()
			}
		}()
		for {
			select {
			case result := <-reads:
				if matcher.write(pt, result.data) {
					escaped <- struct{}{}
					return
				}
				if result.err != nil {
					matcher.flush(pt)
					return
				}
				if timer != nil {
					timer.Stop()
				}
				timeout = nil
				if len(matcher.pending) > 0 {
					if timer == nil {
						timer = time.NewTimer(25 * time.Millisecond)
					} else {
						timer.Reset(25 * time.Millisecond)
					}
					timeout = timer.C
				}
			case <-timeout:
				matcher.flush(pt)
				timeout = nil
			case <-inputDone:
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

// escapeMatcher watches for ctrl+] and drops the repeat and release events of
// that key. It buffers only bytes that could still become one of those
// sequences, so every other byte is forwarded unchanged and in order.
type escapeMatcher struct{ pending []byte }

// escapeScan is what the buffered bytes at the start of the input are.
type escapeScan int

const (
	escapeIncomplete escapeScan = iota // a longer sequence may still match
	escapeOther                        // not ctrl+]
	escapeTrigger                      // ctrl+] was pressed
	escapeSwallow                      // a repeat or release of ctrl+] to drop
)

func (m *escapeMatcher) write(out io.Writer, input []byte) bool {
	m.pending = append(m.pending, input...)
	var forward []byte
	defer func() {
		if len(forward) > 0 {
			_, _ = out.Write(forward)
		}
	}()
	for len(m.pending) > 0 {
		scan, n := scanEscape(m.pending)
		switch scan {
		case escapeIncomplete:
			return false
		case escapeOther:
			forward = append(forward, m.pending[0])
			m.pending = m.pending[1:]
		case escapeSwallow:
			m.pending = m.pending[n:]
		case escapeTrigger:
			m.pending = m.pending[:0]
			return true
		}
	}
	return false
}

func (m *escapeMatcher) flush(out io.Writer) {
	_, _ = out.Write(m.pending)
	m.pending = nil
}

// scanEscape reads the start of p. It reports a complete sequence and its
// length, whether p is a prefix of one, or that it is something else.
func scanEscape(p []byte) (escapeScan, int) {
	if len(p) == 0 {
		return escapeIncomplete, 0
	}
	if p[0] == 0x1d {
		return escapeTrigger, 1
	}
	if p[0] != 0x1b {
		return escapeOther, 0
	}
	if len(p) < 2 {
		return escapeIncomplete, 0
	}
	if p[1] != '[' {
		return escapeOther, 0
	}
	kitty, kn := scanKitty(p)
	xterm, xn := scanModifyOtherKeys(p)
	switch {
	case kitty == escapeTrigger:
		return escapeTrigger, kn
	case xterm == escapeTrigger:
		return escapeTrigger, xn
	case kitty == escapeSwallow:
		return escapeSwallow, kn
	case xterm == escapeSwallow:
		return escapeSwallow, xn
	case kitty == escapeIncomplete || xterm == escapeIncomplete:
		return escapeIncomplete, 0
	default:
		return escapeOther, 0
	}
}

// scanKitty reads a kitty keyboard protocol CSI-u sequence:
//
//	ESC [ key[:alternate...] ; modifiers[:event] [; text] u
//
// The key may also be 29, the control code for ctrl+].
func scanKitty(p []byte) (escapeScan, int) {
	i := 2
	keyStart := i
	for i < len(p) && isDigit(p[i]) {
		i++
	}
	if i == keyStart {
		return escapeOther, 0
	}
	if !keyPrefix(p[keyStart:i]) {
		return escapeOther, 0
	}
	keyEnd := i
	// Alternate keys may be empty, for example 93::93.
	for i < len(p) && p[i] == ':' {
		i++
		for i < len(p) && isDigit(p[i]) {
			i++
		}
	}
	if i == len(p) {
		return escapeIncomplete, 0
	}
	if p[i] != ';' {
		return escapeOther, 0
	}
	i++
	modStart := i
	for i < len(p) && isDigit(p[i]) {
		i++
	}
	if i == modStart {
		if i == len(p) {
			return escapeIncomplete, 0
		}
		return escapeOther, 0
	}
	modEnd := i
	if i == len(p) {
		return escapeIncomplete, 0
	}
	event := 1
	if p[i] == ':' {
		i++
		eventStart := i
		for i < len(p) && isDigit(p[i]) {
			i++
		}
		if i == eventStart {
			if i == len(p) {
				return escapeIncomplete, 0
			}
			return escapeOther, 0
		}
		event = smallInt(p[eventStart:i])
	}
	if i == len(p) {
		return escapeIncomplete, 0
	}
	// Associated text is an optional third field of codepoints.
	if p[i] == ';' {
		i++
		textStart := i
		for i < len(p) && (isDigit(p[i]) || p[i] == ':') {
			i++
		}
		if i == textStart {
			if i == len(p) {
				return escapeIncomplete, 0
			}
			return escapeOther, 0
		}
	}
	if i == len(p) {
		return escapeIncomplete, 0
	}
	if p[i] != 'u' {
		return escapeOther, 0
	}
	i++
	key := smallInt(p[keyStart:keyEnd])
	if key != 93 && key != 29 {
		return escapeOther, 0
	}
	if !ctrlOnly(smallInt(p[modStart:modEnd])) {
		return escapeOther, 0
	}
	switch event {
	case 1:
		return escapeTrigger, i
	case 2, 3:
		return escapeSwallow, i
	default:
		return escapeOther, 0
	}
}

var modifyOtherKeysPrefix = []byte("\x1b[27;")

// scanModifyOtherKeys reads an xterm modifyOtherKeys sequence for ctrl+]:
//
//	ESC [ 27 ; modifiers ; 93 ~
func scanModifyOtherKeys(p []byte) (escapeScan, int) {
	if len(p) < len(modifyOtherKeysPrefix) {
		if bytes.HasPrefix(modifyOtherKeysPrefix, p) {
			return escapeIncomplete, 0
		}
		return escapeOther, 0
	}
	if !bytes.HasPrefix(p, modifyOtherKeysPrefix) {
		return escapeOther, 0
	}
	i := len(modifyOtherKeysPrefix)
	modStart := i
	for i < len(p) && isDigit(p[i]) {
		i++
	}
	if i == modStart {
		if i == len(p) {
			return escapeIncomplete, 0
		}
		return escapeOther, 0
	}
	modEnd := i
	if i == len(p) {
		return escapeIncomplete, 0
	}
	if p[i] != ';' {
		return escapeOther, 0
	}
	i++
	keyStart := i
	for i < len(p) && isDigit(p[i]) {
		i++
	}
	if i == keyStart {
		if i == len(p) {
			return escapeIncomplete, 0
		}
		return escapeOther, 0
	}
	keyEnd := i
	if i == len(p) {
		return escapeIncomplete, 0
	}
	if p[i] != '~' {
		return escapeOther, 0
	}
	i++
	if smallInt(p[keyStart:keyEnd]) != 93 {
		return escapeOther, 0
	}
	if !ctrlOnly(smallInt(p[modStart:modEnd])) {
		return escapeOther, 0
	}
	return escapeTrigger, i
}

// keyPrefix reports whether digits could still be 93 or 29.
func keyPrefix(digits []byte) bool {
	return bytes.HasPrefix([]byte("93"), digits) || bytes.HasPrefix([]byte("29"), digits)
}

// ctrlOnly reports whether the kitty modifier value, after the Caps Lock (64)
// and Num Lock (128) bits are ignored, is ctrl (4) and nothing else.
func ctrlOnly(mod int) bool {
	if mod < 1 {
		return false
	}
	return (mod-1)&^(64|128) == 4
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// smallInt reads decimal digits, stopping at a value no sequence can use.
func smallInt(b []byte) int {
	const limit = 1 << 20
	n := 0
	for _, c := range b {
		if n > limit/10 {
			return limit
		}
		n = n*10 + int(c-'0')
	}
	return n
}

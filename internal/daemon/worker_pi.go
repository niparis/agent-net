package daemon

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// The Pi worker (decision 0008).
//
// A worker is `pi --mode rpc`: Pi's long-lived mode for programs. Facts from Pi
// 1.4.0 (`pi --help`, docs/rpc.md in its package):
//
//   - `--mode rpc` reads JSON commands on stdin and writes events on stdout. It
//     ends when stdin closes. We send no command: the task arrives as a normal
//     agent-net message, through our Pi extension, which loads in this mode
//     (ctx.mode is "rpc", which the extension reports as a background session).
//   - `--session-id <id>` makes Pi use that session ID. The daemon picks it, so
//     the worker is bound to its session before Pi starts, and no other process
//     may connect as it.
//   - `--model` takes provider/id (and optional :thinking). Pi can list models
//     (`pi --list-models`), but only as a table, so the model is not checked
//     here: a model Pi does not know ends the process, and the start fails with
//     its output.
//
// Stdin must never close. The daemon gives Pi a named pipe that it opened for
// reading and writing itself, so Pi never sees the end of the file, also when
// the daemon restarts.
//
// Pi has no permission prompts: its tools run without asking. A Pi worker
// therefore has the full reach of Pi's tools in the project folder. Use
// isolation worktree for tasks that change code (docs/security.md).
//
// Pi has no resume: the daemon picks a new session ID for every start, and
// `pi --mode rpc --session-id` starts a session with that ID rather than
// opening a stored one. So piLauncher has no resume method, and a follow-up to
// a Pi worker that handed back is refused with that reason
// (cannotResumeReason). Read its old session with `agent-net show`, or open its
// session file in Pi.
type piLauncher struct{}

func (piLauncher) start(ctx context.Context, d *Daemon, w *worker) error {
	bin, err := d.harnessBinary("pi")
	if err != nil {
		return err
	}
	native, err := newUUID()
	if err != nil {
		return err
	}
	if err := d.bindWorkerNative(w, native); err != nil {
		return err
	}
	dir := d.setWorkerDir(w)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fifo := filepath.Join(dir, "stdin")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		return fmt.Errorf("cannot make the stdin pipe: %w", err)
	}
	in, err := os.OpenFile(fifo, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	// The daemon keeps this end open, so Pi never sees the end of its input.
	// It is also how the daemon asks for the session statistics.
	w.piIn = in
	pr, pw, err := os.Pipe()
	if err != nil {
		in.Close()
		return err
	}
	w.stdout = pw
	w.piUsage = &piUsage{}
	env := workerEnv(os.Environ(), map[string]string{
		"AGENT_NET_WORKER": w.id,
		"AGENT_NET_SOCKET": d.cfg.SocketPath,
	})
	args := []string{"--mode", "rpc", "--session-id", native, "--name", "worker " + w.project}
	if w.model != "" {
		args = append(args, "--model", w.model)
	}
	if err := d.spawnWorkerStdin(w, bin, args, env, in); err != nil {
		in.Close()
		pr.Close()
		pw.Close()
		return err
	}
	pw.Close()
	go readPiUsage(w, pr)
	// The extension registers the session (the daemon binds it by process
	// group). If it does not, it is not installed and the worker cannot answer.
	for {
		select {
		case <-w.ready:
			return nil
		case <-ctx.Done():
			return errors.New("the agent-net extension did not register the worker session in time: is the Pi extension installed? Run `agent-net setup` on that machine")
		case <-time.After(50 * time.Millisecond):
		}
		if d.gone(w) {
			return errors.New("pi exited at start")
		}
	}
}

// piUsage holds the answer to the last get_session_stats command, and the
// channel a waiting stats call listens on.
type piUsage struct {
	mu      sync.Mutex
	last    workerStats
	waiting chan struct{}
}

// readPiUsage reads Pi's rpc event stream. The only response it needs is
// get_session_stats; the rest of the stream is ignored.
func readPiUsage(w *worker, r *os.File) {
	defer r.Close()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<16), 8<<20)
	for sc.Scan() {
		var frame struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Success bool   `json:"success"`
			Data    struct {
				Tokens struct {
					Input      float64 `json:"input"`
					Output     float64 `json:"output"`
					CacheRead  float64 `json:"cacheRead"`
					CacheWrite float64 `json:"cacheWrite"`
				} `json:"tokens"`
				ContextUsage *struct {
					Tokens        *float64 `json:"tokens"`
					ContextWindow float64  `json:"contextWindow"`
				} `json:"contextUsage"`
			} `json:"data"`
		}
		if json.Unmarshal(sc.Bytes(), &frame) != nil || frame.Type != "response" || frame.Command != "get_session_stats" || !frame.Success {
			continue
		}
		t := frame.Data.Tokens
		st := workerStats{Tokens: tokenText(t.Input, t.Output, 0, t.CacheRead, t.CacheWrite),
			TotalTokens: int64(t.Input + t.Output)}
		if c := frame.Data.ContextUsage; c != nil && c.Tokens != nil {
			st.ContextTokens, st.ContextLimit = int64(*c.Tokens), int64(c.ContextWindow)
		}
		u := w.piUsage
		if u == nil {
			return
		}
		u.mu.Lock()
		u.last = st
		if u.waiting != nil {
			close(u.waiting)
			u.waiting = nil
		}
		u.mu.Unlock()
	}
}

// stats asks Pi's rpc mode for its session statistics. Pi reports the tokens,
// cost, and the context usage of the active model (get_session_stats in
// docs/rpc-commands.md). The answer is short and usually there at once; a slow
// or busy Pi keeps the last answer.
func (piLauncher) stats(ctx context.Context, d *Daemon, w *worker) workerStats {
	d.wmu.Lock()
	in, u := w.piIn, w.piUsage
	d.wmu.Unlock()
	if in == nil || u == nil {
		return workerStats{}
	}
	u.mu.Lock()
	ch := make(chan struct{})
	u.waiting = ch
	u.mu.Unlock()
	if _, err := in.Write([]byte("{\"type\":\"get_session_stats\"}\n")); err != nil {
		u.mu.Lock()
		if u.waiting == ch {
			u.waiting = nil
		}
		last := u.last
		u.mu.Unlock()
		return last
	}
	select {
	case <-ch:
	case <-time.After(500 * time.Millisecond):
	case <-ctx.Done():
	}
	u.mu.Lock()
	if u.waiting == ch {
		u.waiting = nil
	}
	last := u.last
	u.mu.Unlock()
	return last
}

// end closes the pipes of a Pi worker. The process is killed by the worker
// end; closing its input here lets the reader finish too.
func (piLauncher) end(d *Daemon, w *worker) {
	d.wmu.Lock()
	in := w.piIn
	w.piIn = nil
	d.wmu.Unlock()
	if in != nil {
		in.Close()
	}
}

// newUUID returns a random version 4 UUID.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

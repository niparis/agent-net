package daemon

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	w.dir = filepath.Join(d.cfg.StateDir, "workers", w.id)
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		return err
	}
	fifo := filepath.Join(w.dir, "stdin")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		return fmt.Errorf("cannot make the stdin pipe: %w", err)
	}
	in, err := os.OpenFile(fifo, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer in.Close() // the process has its own copy
	env := workerEnv(os.Environ(), map[string]string{
		"AGENT_NET_WORKER": w.id,
		"AGENT_NET_SOCKET": d.cfg.SocketPath,
	})
	args := []string{"--mode", "rpc", "--session-id", native, "--name", "worker " + w.project}
	if w.model != "" {
		args = append(args, "--model", w.model)
	}
	if err := d.spawnWorkerStdin(w, bin, args, env, in); err != nil {
		return err
	}
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

package daemon

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	defer in.Close() // the process has its own copy, so its input never closes
	env := workerEnv(os.Environ(), map[string]string{
		"AGENT_NET_WORKER": w.id,
		"AGENT_NET_SOCKET": d.cfg.SocketPath,
	})
	args := []string{"--mode", "rpc", "--session-id", native, "--name", "worker " + w.project}
	if w.model != "" {
		args = append(args, "--model", w.model)
	}
	w.discardStdout = true // the rpc event stream; stderr (with the errors) goes to the log
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

// stats reads the usage of the Pi worker's session file. Pi writes the session
// as JSONL under its session folder, named with the session ID the daemon
// picked, so this works while the worker runs and after a daemon restart. The
// context window comes from `pi --list-models`, once per worker and model.
func (piLauncher) stats(ctx context.Context, d *Daemon, w *worker) workerStats {
	d.wmu.Lock()
	native := w.nativeID
	d.wmu.Unlock()
	if native == "" {
		return workerStats{}
	}
	st := piSessionStats(d.cfg.PiSessionsDir, native)
	if st.ContextTokens > 0 && st.Model != "" {
		st.ContextLimit = d.piModelLimit(ctx, w, st.Model)
	}
	return st
}

// piSessionStats reads the assistant usage of a Pi session file. Context is the
// last assistant message (input plus cache read and cache write). Tokens are
// the session's input plus output; Pi counts reasoning inside output, so it is
// not added again.
func piSessionStats(dir, session string) workerStats {
	path := piSessionFile(dir, session)
	if path == "" {
		return workerStats{}
	}
	f, err := os.Open(path)
	if err != nil {
		return workerStats{}
	}
	defer f.Close()
	type usage struct{ in, out, cr, cw float64 }
	var total, last usage
	model := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 16<<20)
	for sc.Scan() {
		var line struct {
			Type    string `json:"type"`
			Message struct {
				Role     string `json:"role"`
				Provider string `json:"provider"`
				Model    string `json:"model"`
				Usage    struct {
					Input      float64 `json:"input"`
					Output     float64 `json:"output"`
					CacheRead  float64 `json:"cacheRead"`
					CacheWrite float64 `json:"cacheWrite"`
				} `json:"usage"`
			} `json:"message"`
			Usage struct {
				Input      float64 `json:"input"`
				Output     float64 `json:"output"`
				CacheRead  float64 `json:"cacheRead"`
				CacheWrite float64 `json:"cacheWrite"`
			} `json:"usage"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		switch {
		case line.Type == "message" && line.Message.Role == "assistant":
			u := usage{line.Message.Usage.Input, line.Message.Usage.Output, line.Message.Usage.CacheRead, line.Message.Usage.CacheWrite}
			total.in, total.out, total.cr, total.cw = total.in+u.in, total.out+u.out, total.cr+u.cr, total.cw+u.cw
			last = u
			if line.Message.Model != "" {
				model = line.Message.Model
				if line.Message.Provider != "" {
					model = line.Message.Provider + "/" + model
				}
			}
		case line.Type == "usage" || line.Type == "compaction":
			total.in += line.Usage.Input
			total.out += line.Usage.Output
			total.cr += line.Usage.CacheRead
			total.cw += line.Usage.CacheWrite
		}
	}
	return workerStats{Tokens: tokenText(total.in, total.out, 0, total.cr, total.cw), Model: model,
		ContextTokens: int64(last.in + last.cr + last.cw), TotalTokens: int64(total.in + total.out)}
}

// piSessionFile finds the JSONL file of a session: Pi groups sessions by
// working folder under the session folder, in
// <timestamp>_<session-id>.jsonl.
func piSessionFile(dir, session string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, "*", "*_"+session+".jsonl"))
	more, _ := filepath.Glob(filepath.Join(dir, "*_"+session+".jsonl"))
	matches = append(matches, more...)
	if len(matches) == 0 {
		return ""
	}
	best, bestTime := matches[0], time.Time{}
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && fi.ModTime().After(bestTime) {
			best, bestTime = m, fi.ModTime()
		}
	}
	return best
}

// piModelLimit reads the context window of a model from `pi --list-models`.
// The table is small, and the worker keeps the answer, so Pi is asked once per
// worker and model.
func (d *Daemon) piModelLimit(ctx context.Context, w *worker, model string) int64 {
	d.wmu.Lock()
	if w.piLimitModel == model {
		limit := w.piLimit
		d.wmu.Unlock()
		return limit
	}
	d.wmu.Unlock()
	bin, err := d.harnessBinary("pi")
	if err != nil {
		return 0
	}
	out, err := exec.CommandContext(ctx, bin, "--list-models").Output()
	if err != nil {
		return 0
	}
	limit := piModelLimitFromTable(string(out), model)
	d.wmu.Lock()
	w.piLimitModel, w.piLimit = model, limit
	d.wmu.Unlock()
	return limit
}

// piModelLimitFromTable picks the context column of the model in the table of
// `pi --list-models`. The model is "provider/id"; a ":thinking" suffix is not
// part of the id in the table.
func piModelLimitFromTable(table, model string) int64 {
	provider, id, ok := strings.Cut(model, "/")
	if !ok {
		return 0
	}
	if i := strings.LastIndexByte(id, ':'); i >= 0 {
		id = id[:i] // a thinking level such as :low is not part of the id
	}
	for _, line := range strings.Split(table, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[0] != provider || f[1] != id {
			continue
		}
		return parseTokenSize(f[2])
	}
	return 0
}

// parseTokenSize reads a size from `pi --list-models`: 200K, 1.0M, 8.2K.
func parseTokenSize(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	mult := 1.0
	switch s[len(s)-1] {
	case 'k', 'K':
		mult, s = 1e3, s[:len(s)-1]
	case 'm', 'M':
		mult, s = 1e6, s[:len(s)-1]
	case 'g', 'G':
		mult, s = 1e9, s[:len(s)-1]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(v*mult + 0.5)
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

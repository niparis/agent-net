package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/protocol"
)

// Helpers shared by the harness adapters.

// workerDir makes and returns the private folder of the worker.
func (d *Daemon) workerDir(w *worker) (string, error) {
	dir := d.setWorkerDir(w)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// openWorkerFIFO makes a named pipe at path and opens it read-write, so the
// harness never sees end of file, also when the daemon restarts. The caller
// closes it.
func openWorkerFIFO(path string) (*os.File, error) {
	_ = os.Remove(path)
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_RDWR, 0)
}

// nativeSessionID returns the worker's harness session ID, or an error that
// names the thing the worker is missing.
func (w *worker) nativeSessionID(what string) (string, error) {
	if w.nativeID == "" {
		return "", fmt.Errorf("the %s of the worker is not known", what)
	}
	return w.nativeID, nil
}

// waitWorkerReady polls ready until it is true, the context ends, or the
// worker's process exits. timeout names the reason when the context ends;
// exited, when not nil, names it when the process goes away.
func (d *Daemon) waitWorkerReady(ctx context.Context, w *worker, ready func() bool, timeout func() error, exited error) error {
	for {
		if ready() {
			return nil
		}
		select {
		case <-ctx.Done():
			return timeout()
		case <-time.After(50 * time.Millisecond):
		}
		if exited != nil && d.gone(w) {
			return exited
		}
	}
}

// registerWorkerSession makes the worker's native session a session of the
// daemon, with the worker flag. At a start it waits until the gateway has given
// it an ID. After a restart it does not wait: the link may not be up yet, and
// the daemon sends all its sessions when it connects.
func (d *Daemon) registerWorkerSession(ctx context.Context, w *worker, info protocol.SessionInfo, wait bool) error {
	d.wmu.Lock()
	sessionID := w.sessionID
	d.wmu.Unlock()
	ls := &localSession{info: info, sessionID: sessionID, owned: true}
	d.mu.Lock()
	ls.lastActive = d.activity[key(info.Harness, info.NativeID)]
	d.sessions[key(info.Harness, info.NativeID)] = ls
	d.mu.Unlock()
	if !wait {
		go d.pushSession(ls)
		return nil
	}
	for {
		d.pushSession(ls)
		if sid := d.currentSessionID(ls); sid != "" {
			d.workerSessionReady(w, sid)
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the gateway did not register the %s worker session in time", info.Harness)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// setWorkerStatus changes the status of the worker's session (idle or busy).
func (d *Daemon) setWorkerStatus(w *worker, status string) {
	if ls := d.changeWorkerStatus(w, status); ls != nil {
		d.persistActivity(ls)
		go d.pushSession(ls)
	}
}

// changeWorkerStatus changes the status under the locks and returns the
// session when it changed, for the caller to persist and push itself.
func (d *Daemon) changeWorkerStatus(w *worker, status string) *localSession {
	d.wmu.Lock()
	harness, native := w.harness, w.nativeID
	d.wmu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	ls := d.sessions[key(harness, native)]
	if ls == nil || ls.info.Status == status {
		return nil
	}
	ls.info.Status = status
	d.recordActivityLocked(ls, time.Now())
	return ls
}

// workerWriteRoots are the folders a worker's commands may write beyond its own
// path: the git folders of a commit in its worktree, its package caches, and
// the Go build and temp folders. A folder that cannot be made is left out.
func (d *Daemon) workerWriteRoots(ctx context.Context, w *worker) []string {
	return d.workerWriteRootsThen(ctx, w.path, d.workerCacheRoot(w), d.workerGoCacheDir(), d.workerSandboxGoTmpDir())
}

// workerWriteRootsThen is workerWriteRoots for a harness that needs the folders
// after the git roots in its own order: agy keeps the package cache last.
func (d *Daemon) workerWriteRootsThen(ctx context.Context, path string, then ...string) []string {
	paths := workerGitRoots(ctx, path)
	for _, p := range then {
		if err := os.MkdirAll(p, 0o700); err == nil {
			paths = append(paths, p)
		}
	}
	return paths
}

// harnessBinary returns the path of an installed harness, from the inventory
// or from the test overrides. When detection has not finished, or a run missed
// the harness, it looks the harness up the same way detection does.
func (d *Daemon) harnessBinary(name string) (string, error) {
	if p := d.cfg.HarnessPaths[name]; p != "" {
		return p, nil
	}
	list, _ := d.inventory()
	for _, h := range list {
		if h.Name == name && h.Path != "" {
			return h.Path, nil
		}
	}
	search := harnesses.Options{}
	if d.cfg.HarnessSearch != nil {
		search = *d.cfg.HarnessSearch
	}
	if p := harnesses.Locate(context.Background(), name, search); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("%s is not installed on this machine", name)
}

// workerEnv returns env with the given variables set and any inherited value
// of them removed.
func workerEnv(env []string, set map[string]string) []string {
	out := make([]string, 0, len(env)+len(set))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if _, replaced := set[k]; !replaced {
			out = append(out, kv)
		}
	}
	for k, v := range set {
		out = append(out, k+"="+v)
	}
	return out
}

func (d *Daemon) workerFreePort() (int, error) {
	if d.cfg.testFreePort != nil {
		return d.cfg.testFreePort()
	}
	return freePort()
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

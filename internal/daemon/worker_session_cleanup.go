package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// deleteWorkerHarnessSession only accepts retained, ended worker records. A
// failed deletion leaves the record available to the next retention pass.
func (d *Daemon) deleteWorkerHarnessSession(ctx context.Context, r workerRow) error {
	if err := d.workerSessionRefusal(r); err != nil {
		return err
	}
	if r.NativeID == "" {
		return nil
	} // launch failed before a session existed
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	switch r.Harness {
	case "codex":
		// --force only accepts a UUID, so a thread name can never select an owner
		// session. Let Codex remove its own rollout and database entries.
		home := d.cfg.CodexHome
		if home == "" {
			home = os.Getenv("CODEX_HOME")
		}
		if home == "" {
			home = defaultCodexHome()
		}
		before := codexWorkerBytes(home, r.NativeID)
		bin, err := d.harnessBinary("codex")
		if err != nil {
			return &codexWorkerDeleteError{cause: err}
		}
		cmd := exec.CommandContext(ctx, bin, "delete", "--force", r.NativeID)
		cmd.Env = workerEnv(os.Environ(), map[string]string{"CODEX_HOME": home})
		var output bytes.Buffer
		captured := &limitedBuffer{b: &output}
		cmd.Stdout, cmd.Stderr = captured, captured
		if err := cmd.Run(); err != nil {
			text := strings.ToLower(output.String())
			if codexWorkerThreadNotFound(text, r.NativeID) {
				return nil
			}
			return &codexWorkerDeleteError{cause: err, unsupported: codexWorkerDeleteUnsupported(text)}
		}
		if d.log != nil {
			d.Logf("worker.session.deleted worker=%s harness=codex session=%s bytes_freed=%d", r.ID, r.NativeID, before-codexWorkerBytes(home, r.NativeID))
		}
	case "opencode":
		dir := filepath.Join(d.cfg.StateDir, "workers", r.ID)
		data := filepath.Join(dir, "data")
		if _, err := os.Stat(data); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return err
		}
		for _, path := range []string{dir, data, filepath.Join(dir, "state")} {
			info, err := os.Lstat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return workerSessionSafetyError("invalid worker data directory: " + path)
			}
		}
		bin, err := d.harnessBinary("opencode")
		if err != nil {
			return err
		}
		port, err := d.workerFreePort()
		if err != nil {
			return err
		}
		password := "cleanup-" + uuid.NewString()
		cmd := exec.CommandContext(ctx, bin, "serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(port))
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Dir = dir
		cmd.Env = workerEnv(os.Environ(), map[string]string{"XDG_DATA_HOME": data, "XDG_STATE_HOME": filepath.Join(dir, "state"), "OPENCODE_SERVER_PASSWORD": password, "AGENT_NET_WORKER": "", "AGENT_NET_WORKER_SESSION_FILE": "", "AGENT_NET_SOCKET": filepath.Join(dir, "cleanup-disabled.sock")})
		if err := cmd.Start(); err != nil {
			return err
		}
		defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() }()
		api := openCodeWorkerAPI(ctx, "http://127.0.0.1:"+strconv.Itoa(port), password)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, code, err := api("GET", "/api/info", nil); err == nil && code == http.StatusOK {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
		_, code, err := api("DELETE", "/api/session/"+url.PathEscape(r.NativeID), nil)
		if err != nil {
			return err
		}
		if code != http.StatusOK && code != http.StatusNoContent && code != http.StatusNotFound {
			return fmt.Errorf("opencode delete HTTP %d", code)
		}
	}
	return nil
}

// workerFolderBytes counts only the private worker folder. Shared harness
// database free pages cannot be reported as reclaimed filesystem space.
func workerFolderBytes(dir string) int64 {
	var size int64
	_ = filepath.WalkDir(dir, func(_ string, e fs.DirEntry, err error) error {
		if err == nil && e.Type().IsRegular() {
			if info, err := e.Info(); err == nil {
				size += info.Size()
			}
		}
		return nil
	})
	return size
}

// Count matching rollout files only; shared database pages may remain allocated.
func codexWorkerBytes(home, id string) int64 {
	var size int64
	for _, sub := range []string{"sessions", "archived_sessions"} {
		_ = filepath.WalkDir(filepath.Join(home, sub), func(path string, e fs.DirEntry, err error) error {
			if err == nil && e.Type().IsRegular() && strings.HasSuffix(e.Name(), "-"+id+".jsonl") {
				if info, err := e.Info(); err == nil {
					size += info.Size()
				}
			}
			return nil
		})
	}
	return size
}

// Do not expose harness stderr, which may contain paths or credentials, in logs.
// Keep the classification separate from safety errors such as a live worker.
type codexWorkerDeleteError struct {
	cause       error
	unsupported bool
}

func (e *codexWorkerDeleteError) Error() string { return fmt.Sprintf("codex delete: %v", e.cause) }
func (e *codexWorkerDeleteError) Unwrap() error { return e.cause }

func codexWorkerThreadNotFound(text, id string) bool {
	for _, subject := range []string{"thread", "session"} {
		if strings.Contains(text, subject+" not found") || strings.Contains(text, subject+" "+id+" not found") || strings.Contains(text, "no "+subject+" found") {
			return true
		}
	}
	return false
}

func codexWorkerDeleteUnsupported(text string) bool {
	text = strings.NewReplacer("\"", "'", "`", "'").Replace(text)
	for _, prefix := range []string{"unrecognized subcommand", "unknown subcommand", "unknown command"} {
		if strings.Contains(text, prefix+" 'delete'") {
			return true
		}
	}
	return false
}

func (d *Daemon) workerSessionRefusal(r workerRow) error {
	if r.ID == "" || filepath.Base(r.ID) != r.ID || r.ID == "." || r.ID == ".." {
		return workerSessionSafetyError("invalid worker ID")
	}
	if r.EndedReason == "" {
		return workerSessionSafetyError("worker is still live")
	}
	d.wmu.Lock()
	live := d.workers[r.ID] != nil
	d.wmu.Unlock()
	if live {
		return workerSessionSafetyError("worker is still registered as running")
	}
	if r.Harness == "codex" && r.NativeID != "" {
		if _, err := uuid.Parse(r.NativeID); err != nil {
			return workerSessionSafetyError("invalid worker thread ID: " + err.Error())
		}
	}
	return nil
}

// Safety refusals must never expire into permission to drop a live or invalid row.
type workerSessionSafetyError string

func (e workerSessionSafetyError) Error() string { return string(e) }

// This loop is started once by Daemon.Start, independently of gateway link-up
// and the worker health tick. No link path starts harness deletion.
func (d *Daemon) workerSessionCleanupLoop(ctx context.Context) {
	timer := time.NewTicker(d.workerSettings("").Cleanup)
	defer timer.Stop()
	for {
		d.cleanupWorkerSessions(ctx)
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

// A bounded pass, also guarded against overlap. Move past the last attempted
// row so a backlog of failed deletions cannot starve later workers.
func (d *Daemon) cleanupWorkerSessions(ctx context.Context) {
	if !d.workerSessionCleanupRunning.CompareAndSwap(false, true) {
		return
	}
	defer d.workerSessionCleanupRunning.Store(false)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	rows, err := d.st.workers()
	if err != nil {
		return
	}
	start := 0
	for i, r := range rows {
		if r.ID == d.workerSessionCleanupCursor {
			start = i + 1
			break
		}
	}
	set := d.workerSettings("")
	attempted := 0
	for i := 0; i < len(rows) && attempted < 10 && ctx.Err() == nil; i++ {
		r := rows[(start+i)%len(rows)]
		if r.EndedReason == "" || !r.Reported || r.StartedAt.Add(set.MaxAge).After(time.Now()) {
			continue
		}
		attempted++
		d.workerSessionCleanupCursor = r.ID
		if d.cleanupWorkerSessionRow(ctx, r) && r.Resumable {
			d.Logf("worker.cleaned worker=%s session=%s (ended more than %s ago)", r.ID, r.SessionID, set.MaxAge)
		}
	}
}

// Only this retention pass may authorize dropping a row after harness deletion.
func (d *Daemon) cleanupWorkerSessionRow(ctx context.Context, r workerRow) bool {
	if err := d.workerSessionRefusal(r); err != nil {
		d.Logf("worker.cleanup refused worker=%s harness=%s: %v", r.ID, r.Harness, err)
		return false
	}
	set := d.workerSettings("")
	if set.DeleteSessions == nil || *set.DeleteSessions {
		if err := d.deleteWorkerHarnessSession(ctx, r); err != nil {
			var safety workerSessionSafetyError
			if errors.As(err, &safety) {
				d.Logf("worker.cleanup refused worker=%s harness=%s: %v", r.ID, r.Harness, err)
				return false
			}
			var failure *codexWorkerDeleteError
			reason := ""
			if errors.As(err, &failure) && failure.unsupported {
				reason = "delete_unsupported"
			} else if !time.Now().Before(r.StartedAt.Add(set.MaxAge).Add(7 * 24 * time.Hour)) {
				reason = "retry_expired"
			}
			if reason == "" {
				d.Logf("worker.cleanup retry worker=%s harness=%s: %v", r.ID, r.Harness, err)
				return false
			}
			d.Logf("worker.session.delete_skipped worker=%s harness=%s session=%s reason=%s", r.ID, r.Harness, r.NativeID, reason)
		}
	}
	return d.dropWorkerRow(r)
}

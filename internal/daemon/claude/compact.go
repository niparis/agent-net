package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

type idleCompactState struct {
	attempted, running, awaitingIdle bool
	at                               int64
}

// The daemon supplies the live recipe. Other hosts (including adapter tests)
// need not implement settings to discover and deliver sessions.
type compactSettingsHost interface{ SettingsSnapshot() settings.Snapshot }

// Called on every discovery, so even a short busy turn resets the idle stretch.
// Our own /compact turn must not reset the attempt that is still in flight.
func (a *Adapter) observeCompactStateLocked(info protocol.SessionInfo) {
	s := a.compactStates[info.NativeID]
	if info.Status == "idle" && !s.running && s.awaitingIdle {
		s.awaitingIdle = false
		a.compactStates[info.NativeID] = s
	}
	if info.Status == "busy" && !s.running && !s.awaitingIdle {
		delete(a.compactStates, info.NativeID)
	}
}

func (a *Adapter) compactLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.compactOnce(ctx)
		}
	}
}

func (a *Adapter) compactOnce(ctx context.Context) {
	snap := settings.Snapshot{}
	if h, ok := a.host.(compactSettingsHost); ok {
		snap = h.SettingsSnapshot()
	}
	if !snap.Bool("sessions.idle_compaction", settings.Where{}) {
		return
	}
	now := time.Now()
	if a.compactNow != nil {
		now = a.compactNow()
	}
	attached := a.compactAttached
	if attached == nil {
		attached = claudeAttachedJobs
	}
	jobs, err := attached(ctx)
	if err != nil {
		a.host.Logf("claude idle compaction: cannot check attached clients: %v", err)
		return
	}
	a.mu.Lock()
	var candidates []found
	for _, f := range a.byNative {
		s := a.compactStates[f.info.NativeID]
		if f.kind == "bg" && f.job != "" && f.info.Status == "idle" && !s.attempted && !jobs[f.job] && f.info.LastActive > 0 && now.Sub(time.Unix(f.info.LastActive, 0)) >= snap.Duration("sessions.idle_compaction_after", settings.Where{}) {
			candidates = append(candidates, f)
		}
	}
	a.mu.Unlock()
	for _, f := range candidates {
		if ctx.Err() != nil {
			return
		}
		path := a.models.transcript(f.info.NativeID, f.info.Cwd)
		if contextTokens(path) < int64(snap.Int("sessions.idle_compaction_min_tokens", settings.Where{})) {
			continue
		}
		// Discovery may have seen a new turn since the candidate list was built.
		a.mu.Lock()
		current, ok := a.byNative[f.info.NativeID]
		s := a.compactStates[f.info.NativeID]
		if !ok || current.info.Status != "idle" || current.info.LastActive != f.info.LastActive || s.attempted {
			a.mu.Unlock()
			continue
		}
		s.attempted = true
		s.running = true
		a.compactStates[f.info.NativeID] = s
		a.mu.Unlock()
		err := a.compactSession(ctx, f, path)
		a.mu.Lock()
		s = a.compactStates[f.info.NativeID]
		s.running = false
		if err == nil {
			completed := time.Now()
			if a.compactNow != nil {
				completed = a.compactNow()
			}
			s.at = completed.Unix()
			s.awaitingIdle = true
		}
		a.compactStates[f.info.NativeID] = s
		a.mu.Unlock()
		if err != nil {
			a.host.Logf("claude idle compaction %s: %v", f.info.NativeID, err)
		} else {
			a.host.Logf("claude idle compaction %s: compacted while idle", f.info.NativeID)
		}
	}
}

// ps is available on Linux and macOS. Match argv words, including the complete
// job ID, rather than a substring in a shell command. Failure skips this scan.
func claudeAttachedJobs(ctx context.Context) (map[string]bool, error) {
	out, err := exec.CommandContext(ctx, "ps", "-axww", "-o", "pid=,args=").Output()
	if err != nil {
		return nil, err
	}
	return attachedJobs(out), nil
}
func attachedJobs(out []byte) map[string]bool {
	return attachedJobsExcept(out, 0)
}
func attachedJobsExcept(out []byte, exceptPID int) map[string]bool {
	jobs := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		args := strings.Fields(line)
		if len(args) >= 4 && args[0] != strconv.Itoa(exceptPID) && filepath.Base(args[1]) == "claude" && args[2] == "attach" {
			jobs[args[3]] = true
		}
	}
	return jobs
}

// Same definition as claudeTranscriptStats: the last assistant's input plus
// cache read and cache creation, without output tokens or cumulative usage.
func contextTokens(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	var tokens int64
	scanTail(path, fi.Size(), func(line []byte) bool {
		var v struct {
			Type    string `json:"type"`
			Message struct {
				Usage struct {
					Input    int64 `json:"input_tokens"`
					Read     int64 `json:"cache_read_input_tokens"`
					Creation int64 `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &v) != nil || v.Type != "assistant" {
			return false
		}
		tokens = v.Message.Usage.Input + v.Message.Usage.Read + v.Message.Usage.Creation
		return true
	})
	return tokens
}

func compactBoundaryAfter(path string, offset int64) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return false
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 65536), 16<<20)
	for sc.Scan() {
		var v struct {
			Subtype string `json:"subtype"`
		}
		if json.Unmarshal(sc.Bytes(), &v) == nil && v.Subtype == "compact_boundary" {
			return true
		}
	}
	return false
}

func (a *Adapter) compactSession(ctx context.Context, f found, path string) (result error) {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	offset := fi.Size()
	bin := a.compactBinary
	if bin == "" {
		bin = "claude"
	}
	cmd := exec.Command(bin, "attach", f.job)
	cmd.Dir = f.info.Cwd
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 160, Rows: 40})
	if err != nil {
		return fmt.Errorf("attach: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		terminal.Close()
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		if !pidAlive(f.pid) {
			result = fmt.Errorf("background process exited after detach")
		}
	}()
	prompt := make(chan struct{}, 1)
	go func() {
		buf := make([]byte, 4096)
		tail := ""
		for {
			n, err := terminal.Read(buf)
			if n > 0 {
				tail += string(buf[:n])
				if strings.Contains(tail, "❯") {
					select {
					case prompt <- struct{}{}:
					default:
					}
				}
				if len(tail) > 4096 {
					tail = tail[len(tail)-4096:]
				}
			}
			if err != nil {
				return
			}
		}
	}()
	ready := a.compactPromptTimeout
	if ready == 0 {
		ready = 30 * time.Second
	}
	timer := time.NewTimer(ready)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("attach prompt timeout")
	case err := <-done:
		done <- err
		return fmt.Errorf("attach exited before prompt: %v", err)
	case <-prompt:
	}
	// Recheck immediately before typing, excluding our own attach client.
	var jobs map[string]bool
	if a.compactAttached != nil {
		jobs, err = a.compactAttached(ctx)
	} else {
		var out []byte
		out, err = exec.CommandContext(ctx, "ps", "-axww", "-o", "pid=,args=").Output()
		jobs = attachedJobsExcept(out, cmd.Process.Pid)
	}
	if err != nil {
		return fmt.Errorf("attached clients: %w", err)
	}
	if jobs[f.job] {
		return fmt.Errorf("another client attached before compact")
	}
	raw, err := os.ReadFile(filepath.Join(a.cfg.SessionsDir, fmt.Sprintf("%d.json", f.pid)))
	var sf sessionFile
	if err != nil || json.Unmarshal(raw, &sf) != nil || sf.Status != "idle" || sf.SessionID != f.info.NativeID || sf.JobID != f.job {
		return fmt.Errorf("session changed before compact")
	}
	if _, err := terminal.Write([]byte("/compact\r")); err != nil {
		return err
	}
	timeout := a.compactTimeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	wait, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if compactBoundaryAfter(path, offset) {
			if !pidAlive(f.pid) {
				return fmt.Errorf("background process exited after compact")
			}
			return nil
		}
		select {
		case <-wait.Done():
			return fmt.Errorf("compact boundary: %w", wait.Err())
		case err := <-done:
			done <- err
			return fmt.Errorf("attach exited before compact boundary: %v", err)
		case <-tick.C:
		}
	}
}

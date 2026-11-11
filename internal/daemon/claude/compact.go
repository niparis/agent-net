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
	"github.com/niparis/agent-net/internal/claudelaunch"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

type idleCompactState struct {
	attempted, running, awaitingIdle bool
	at                               int64
	skipLogged                       string // last skip reason logged in this idle stretch
}

// The daemon supplies the live recipe. Other hosts (including adapter tests)
// need not implement settings to discover and deliver sessions.
type compactSettingsHost interface{ SettingsSnapshot() settings.Snapshot }

// AttachClientsViewing says, for each `claude attach` client pid, whether the
// owner is looking at it: it runs outside herdr, or in a herdr pane that herdr
// reports as focused. A client in an unfocused herdr pane is not a viewer. A
// pid missing from the answer, and a host without herdr support, count as
// viewers. One call is one scan, so it asks herdr once for all clients.
type compactViewHost interface {
	AttachClientsViewing(pids []int) map[int]bool
}

// viewedJobs returns a function that says whether the owner is viewing a job.
// exceptPID is our own attach client, which never counts.
func (a *Adapter) viewedJobs(ctx context.Context, exceptPID int) (func(job string) bool, error) {
	if a.compactAttached != nil {
		jobs, err := a.compactAttached(ctx)
		if err != nil {
			return nil, err
		}
		return func(job string) bool { return jobs[job] }, nil
	}
	var out []byte
	var err error
	if a.compactPS != nil {
		out, err = a.compactPS(ctx)
	} else {
		out, err = exec.CommandContext(ctx, "ps", "-axww", "-o", "pid=,args=").Output()
	}
	if err != nil {
		return nil, err
	}
	clients := attachClients(out, exceptPID)
	view, _ := a.host.(compactViewHost)
	// Built on first use, so a scan that finds no candidate past the token
	// check does not ask herdr at all.
	var snapshot map[int]bool
	return func(job string) bool {
		if len(clients[job]) == 0 {
			return false
		}
		if view == nil {
			return true
		}
		if snapshot == nil {
			var pids []int
			for _, list := range clients {
				pids = append(pids, list...)
			}
			snapshot = view.AttachClientsViewing(pids)
		}
		for _, pid := range clients[job] {
			if v, ok := snapshot[pid]; !ok || v {
				return true
			}
		}
		return false
	}, nil
}

// compactEvent logs one line per attempt or skip decision. A skip reason is
// logged once per session in an idle stretch.
func (a *Adapter) compactEvent(id, result string) {
	a.host.Logf("event=idle_compact session=%s result=%s", id, result)
}

func (a *Adapter) compactSkip(id, reason string) {
	a.mu.Lock()
	s := a.compactStates[id]
	same := s.skipLogged == reason
	s.skipLogged = reason
	a.compactStates[id] = s
	a.mu.Unlock()
	if !same {
		a.compactEvent(id, "skipped:"+reason)
	}
}

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
	viewed, err := a.viewedJobs(ctx, 0)
	if err != nil {
		a.host.Logf("claude idle compaction: cannot check attached clients: %v", err)
		return
	}
	a.mu.Lock()
	var candidates []found
	for _, f := range a.byNative {
		s := a.compactStates[f.info.NativeID]
		if f.kind == "bg" && f.job != "" && f.info.Status == "idle" && !s.attempted && f.info.LastActive > 0 && now.Sub(time.Unix(f.info.LastActive, 0)) >= snap.Duration("sessions.idle_compaction_after", settings.Where{}) {
			candidates = append(candidates, f)
		}
	}
	a.mu.Unlock()
	for _, f := range candidates {
		if ctx.Err() != nil {
			return
		}
		path := a.models.transcript(f.info.NativeID, f.info.Cwd)
		if noTurnSinceCompact(path) {
			a.compactSkip(f.info.NativeID, "no_activity_since_compact")
			continue
		}
		if contextTokens(path) < int64(snap.Int("sessions.idle_compaction_min_tokens", settings.Where{})) {
			a.compactSkip(f.info.NativeID, "small_context")
			continue
		}
		if viewed(f.job) {
			a.compactSkip(f.info.NativeID, "viewing")
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
			a.compactEvent(f.info.NativeID, "error")
		} else {
			a.host.Logf("claude idle compaction %s: compacted while idle", f.info.NativeID)
			a.compactEvent(f.info.NativeID, "ok")
		}
	}
}

// ps is available on Linux and macOS. Match argv words, including the complete
// job ID, rather than a substring in a shell command. Failure skips this scan.
func attachedJobs(out []byte) map[string]bool {
	return attachedJobsExcept(out, 0)
}
func attachedJobsExcept(out []byte, exceptPID int) map[string]bool {
	jobs := map[string]bool{}
	for job := range attachClients(out, exceptPID) {
		jobs[job] = true
	}
	return jobs
}

// attachClients maps each job ID to the pids of its `claude attach` clients.
func attachClients(out []byte, exceptPID int) map[string][]int {
	clients := map[string][]int{}
	for _, line := range strings.Split(string(out), "\n") {
		args := strings.Fields(line)
		if len(args) >= 4 && args[0] != strconv.Itoa(exceptPID) && filepath.Base(args[1]) == "claude" && args[2] == "attach" {
			if pid, err := strconv.Atoi(args[0]); err == nil {
				clients[args[3]] = append(clients[args[3]], pid)
			}
		}
	}
	return clients
}

// localCommandPrefixes are the string contents Claude Code writes for a local
// command such as /compact and its output. They follow the compaction and are
// not turns.
var localCommandPrefixes = []string{"<command-name>", "<local-command-stdout>", "<local-command-stderr>", "<local-command-caveat>"}

// isLocalCommandContent says a user entry carries only local command
// bookkeeping, not something the owner or the model said.
func isLocalCommandContent(raw json.RawMessage) bool {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return false
	}
	text = strings.TrimSpace(text)
	for _, p := range localCommandPrefixes {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}

// noTurnSinceCompact says the session's newest user or assistant turn is the
// compaction Claude Code wrote, so it has done nothing since. It reads the
// transcript, so the answer survives a daemon restart. Non-turn entries
// (system, attachment and other bookkeeping), user entries that only carry
// command output (isMeta) and the entries of a local command such as /compact
// are skipped.
func noTurnSinceCompact(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false // unknown, so do not skip
	}
	compacted := false
	scanTail(path, fi.Size(), func(line []byte) bool {
		var v struct {
			Type             string `json:"type"`
			Subtype          string `json:"subtype"`
			IsMeta           bool   `json:"isMeta"`
			IsSidechain      bool   `json:"isSidechain"`
			IsCompactSummary bool   `json:"isCompactSummary"`
			Message          struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &v) != nil || v.IsSidechain {
			return false
		}
		// Claude Code writes a system compact_boundary and then a user entry
		// with isCompactSummary for the summary that replaces the conversation.
		if (v.Type == "system" && v.Subtype == "compact_boundary") || (v.Type == "user" && v.IsCompactSummary) {
			compacted = true
			return true
		}
		if v.Type == "assistant" {
			return true // a real turn, so there was activity
		}
		if v.Type == "user" && !v.IsMeta && !isLocalCommandContent(v.Message.Content) {
			return true // a real user turn
		}
		return false
	})
	return compacted
}

// Same definition as claudeTranscriptStats: the last assistant's input plus
// cache read and cache creation, without output tokens or cumulative usage.
// After a compaction the context is the compact boundary's postTokens, not the
// usage of the assistant turn that was before it.
func contextTokens(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	var tokens int64
	scanTail(path, fi.Size(), func(line []byte) bool {
		var v struct {
			Type             string `json:"type"`
			Subtype          string `json:"subtype"`
			IsCompactSummary bool   `json:"isCompactSummary"`
			Message          struct {
				Usage struct {
					Input    int64 `json:"input_tokens"`
					Read     int64 `json:"cache_read_input_tokens"`
					Creation int64 `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			CompactMetadata struct {
				PostTokens int64 `json:"postTokens"`
			} `json:"compactMetadata"`
		}
		if json.Unmarshal(line, &v) != nil {
			return false
		}
		switch {
		case v.Type == "assistant":
			tokens = v.Message.Usage.Input + v.Message.Usage.Read + v.Message.Usage.Creation
			return true
		case v.Type == "system" && v.Subtype == "compact_boundary":
			tokens = v.CompactMetadata.PostTokens
			return true
		case v.Type == "user" && v.IsCompactSummary:
			// The summary follows the boundary, so keep looking for it.
			return false
		}
		return false
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

// compactInstructions is the free text the daemon types after /compact. It tells
// Claude what the summary must keep for an agent-net session to keep working.
// One line, no newlines: the text is typed into the terminal as one command.
const compactInstructions = "Keep: the current task and its goal; open questions to the owner and to other agents; every agent-net ID (sessions s_, workers, changes ch_, messages, asks); branches, worktree paths and full commit shas; decisions made; the next step."

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
	cmd.Env = claudelaunch.Env(append(os.Environ(), "TERM=xterm-256color"))
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
	viewed, err := a.viewedJobs(ctx, cmd.Process.Pid)
	if err != nil {
		return fmt.Errorf("attached clients: %w", err)
	}
	if viewed(f.job) {
		return fmt.Errorf("another client attached before compact")
	}
	raw, err := os.ReadFile(filepath.Join(a.cfg.SessionsDir, fmt.Sprintf("%d.json", f.pid)))
	var sf sessionFile
	if err != nil || json.Unmarshal(raw, &sf) != nil || sf.Status != "idle" || sf.SessionID != f.info.NativeID || sf.JobID != f.job {
		return fmt.Errorf("session changed before compact")
	}
	// Bracketed paste wraps the typed command so Claude Code's terminal paste
	// sanitizer does not strip the trailing \r as an invisible character.
	if _, err := terminal.Write([]byte("\x1b[200~/compact " + compactInstructions + "\x1b[201~\r")); err != nil {
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

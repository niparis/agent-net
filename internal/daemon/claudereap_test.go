package daemon

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/settings"
)

// The Claude background reaper ends bg-pty-host trees whose job has settled.
// These tests use a fake process table and fake job and session files, so no
// real Claude process is touched.

type fakeSignal struct {
	pid int
	sig syscall.Signal
}

// fakeClaudeProcs is a process table that a signal can change.
type fakeClaudeProcs struct {
	mu       sync.Mutex
	procs    []claudeBgProc
	signals  []fakeSignal
	ignoring map[int]bool
	reads    int
	// onRead runs with the number of the read that is about to happen.
	onRead func(read int)
}

func (f *fakeClaudeProcs) add(p claudeBgProc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.procs = append(f.procs, p)
}

func (f *fakeClaudeProcs) table() []claudeBgProc {
	f.mu.Lock()
	f.reads++
	read := f.reads
	f.mu.Unlock()
	if f.onRead != nil {
		f.onRead(read)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]claudeBgProc(nil), f.procs...)
}

func (f *fakeClaudeProcs) signal(pid int, sig syscall.Signal) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signals = append(f.signals, fakeSignal{pid: pid, sig: sig})
	if sig == syscall.SIGTERM && f.ignoring[pid] {
		return nil
	}
	for i, p := range f.procs {
		if p.PID == pid {
			f.procs = append(f.procs[:i], f.procs[i+1:]...)
			return nil
		}
	}
	return syscall.ESRCH
}

func (f *fakeClaudeProcs) signalled() []fakeSignal {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeSignal(nil), f.signals...)
}

func (f *fakeClaudeProcs) alive(pid int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.procs {
		if p.PID == pid {
			return true
		}
	}
	return false
}

func (f *fakeClaudeProcs) setToken(pid int, token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.procs {
		if f.procs[i].PID == pid {
			f.procs[i].Token = token
		}
	}
}

func reapTestDaemon(t *testing.T, root string) (*Daemon, *fakeClaudeProcs, *bytes.Buffer) {
	t.Helper()
	sessions := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	f := &fakeClaudeProcs{ignoring: map[int]bool{}}
	var logs bytes.Buffer
	d := &Daemon{
		log:      log.New(&logs, "", 0),
		sessions: map[string]*localSession{},
		workers:  map[string]*worker{},
		cfg: Config{
			ClaudeSessionsDir:     sessions,
			testClaudeProcs:       f.table,
			testProcessSignal:     f.signal,
			testProcessStopGrace:  50 * time.Millisecond,
			testClaudeStopCheckAt: 5 * time.Millisecond,
		},
	}
	return d, f, &logs
}

func writeClaudeJobState(t *testing.T, root, job, state string) {
	t.Helper()
	dir := filepath.Join(root, "jobs", job)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"state": state})
	if err := os.WriteFile(filepath.Join(dir, "state.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeClaudeSessionFile(t *testing.T, dir string, pid int, job string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"pid": pid, "sessionId": job + "-0000", "jobId": job, "kind": "bg", "status": "idle"})
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(pid)+".json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// claudeTreePids adds one pty host and its bg-spare child, started age ago.
func claudeTreePids(f *fakeClaudeProcs, host, spare int, age time.Duration) {
	started := time.Now().Add(-age)
	f.add(claudeBgProc{PID: host, PPID: 1, UID: os.Getuid(), EUID: os.Getuid(), Token: "host-" + strconv.Itoa(host),
		Started: started, RSSKB: 40000,
		Command: "claude bg-pty-host --bg-pty-host /tmp/cc-daemon-1000/x/spare/" + strconv.Itoa(host) + ".pty.sock 200 50 -- /usr/bin/claude --bg-spare /tmp/cc-daemon-1000/x/spare/" + strconv.Itoa(host) + ".claim.sock"})
	f.add(claudeBgProc{PID: spare, PPID: host, UID: os.Getuid(), EUID: os.Getuid(), Token: "spare-" + strconv.Itoa(spare),
		Started: started, RSSKB: 300000,
		Command: "claude bg-spare --bg-spare /tmp/cc-daemon-1000/x/spare/" + strconv.Itoa(host) + ".claim.sock"})
}

func TestClaudeReapEndsASettledJobTree(t *testing.T) {
	root := t.TempDir()
	d, f, logs := reapTestDaemon(t, root)
	writeClaudeJobState(t, root, "abcd1234", "stopped")
	writeClaudeSessionFile(t, d.cfg.ClaudeSessionsDir, 4101, "abcd1234")
	claudeTreePids(f, 4100, 4101, 2*time.Hour)

	d.reapSettledClaude()

	if f.alive(4100) || f.alive(4101) {
		t.Fatalf("the settled tree is still there: host=%v spare=%v signals=%v", f.alive(4100), f.alive(4101), f.signalled())
	}
	var termed bool
	for _, s := range f.signalled() {
		if s.pid == 4100 && s.sig == syscall.SIGTERM {
			termed = true
		}
	}
	if !termed {
		t.Fatalf("the pty host was not sent TERM: %v", f.signalled())
	}
	line := logs.String()
	for _, want := range []string{"job=abcd1234", "pid=4100", "rss="} {
		if !bytes.Contains([]byte(line), []byte(want)) {
			t.Errorf("the log line %q is missing %q", line, want)
		}
	}
}

func TestClaudeReapKeepsAWorkingJobTree(t *testing.T) {
	root := t.TempDir()
	d, f, _ := reapTestDaemon(t, root)
	writeClaudeJobState(t, root, "abcd1234", "working")
	writeClaudeSessionFile(t, d.cfg.ClaudeSessionsDir, 4101, "abcd1234")
	claudeTreePids(f, 4100, 4101, 2*time.Hour)

	d.reapSettledClaude()

	if !f.alive(4100) || !f.alive(4101) {
		t.Fatalf("a working job's tree was ended: %v", f.signalled())
	}
	if got := f.signalled(); len(got) != 0 {
		t.Fatalf("a working job's tree was signalled: %v", got)
	}
}

func TestClaudeReapKeepsTheIdleSpare(t *testing.T) {
	root := t.TempDir()
	d, f, _ := reapTestDaemon(t, root)
	claudeTreePids(f, 4100, 4101, 2*time.Hour) // no session file: no job claimed it

	d.reapSettledClaude()

	if !f.alive(4100) || !f.alive(4101) {
		t.Fatalf("the idle spare was ended: %v", f.signalled())
	}
	if got := f.signalled(); len(got) != 0 {
		t.Fatalf("the idle spare was signalled: %v", got)
	}
}

func TestClaudeReapKeepsAReusedPID(t *testing.T) {
	root := t.TempDir()
	d, f, _ := reapTestDaemon(t, root)
	writeClaudeJobState(t, root, "abcd1234", "done")
	writeClaudeSessionFile(t, d.cfg.ClaudeSessionsDir, 4101, "abcd1234")
	claudeTreePids(f, 4100, 4101, 2*time.Hour)
	// The process numbers were reused by other programs after the tree was
	// read: the start time no longer matches.
	f.onRead = func(read int) {
		if read >= 2 {
			f.setToken(4100, "other")
			f.setToken(4101, "other")
		}
	}

	d.reapSettledClaude()

	if got := f.signalled(); len(got) != 0 {
		t.Fatalf("a reused process number was signalled: %v", got)
	}
}

func TestClaudeReapKeepsAYoungSettledTree(t *testing.T) {
	root := t.TempDir()
	d, f, _ := reapTestDaemon(t, root)
	writeClaudeJobState(t, root, "abcd1234", "stopped")
	writeClaudeSessionFile(t, d.cfg.ClaudeSessionsDir, 4101, "abcd1234")
	claudeTreePids(f, 4100, 4101, 2*time.Minute)

	d.reapSettledClaude()

	if got := f.signalled(); len(got) != 0 {
		t.Fatalf("a tree younger than reap_after was signalled: %v", got)
	}
}

func TestClaudeReapSettingOffKeepsEverything(t *testing.T) {
	root := t.TempDir()
	d, f, _ := reapTestDaemon(t, root)
	writeClaudeJobState(t, root, "abcd1234", "failed")
	writeClaudeSessionFile(t, d.cfg.ClaudeSessionsDir, 4101, "abcd1234")
	claudeTreePids(f, 4100, 4101, 2*time.Hour)
	snap := settings.Snapshot{Values: []settings.Value{{
		Key: "claude.reap_settled", Level: "network", Scope: "network", Value: json.RawMessage("false"),
	}}}
	d.settingsSnapshot.Store(&snap)

	d.reapSettledClaude()

	if got := f.signalled(); len(got) != 0 {
		t.Fatalf("reaping was off but a tree was signalled: %v", got)
	}
}

func TestClaudeReapDefaults(t *testing.T) {
	snap := (&Daemon{}).SettingsSnapshot()
	if !snap.Bool("claude.reap_settled", settings.Where{}) {
		t.Fatal("claude.reap_settled is not on by default")
	}
	if got := snap.Duration("claude.reap_after", settings.Where{}); got != 30*time.Minute {
		t.Fatalf("claude.reap_after = %v, want 30m", got)
	}
}

func TestClaudeStopCheckEndsALingeringTree(t *testing.T) {
	root := t.TempDir()
	d, f, _ := reapTestDaemon(t, root)
	writeClaudeJobState(t, root, "abcd1234", "stopped")
	writeClaudeSessionFile(t, d.cfg.ClaudeSessionsDir, 4101, "abcd1234")
	claudeTreePids(f, 4100, 4101, 2*time.Minute)

	d.afterClaudeStop("abcd1234")

	eventually(t, "the stopped job's tree was ended", func() bool {
		return !f.alive(4100) && !f.alive(4101)
	})
}

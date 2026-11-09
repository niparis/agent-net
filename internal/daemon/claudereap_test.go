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
)

// The Claude end-of-job check ends a bg-pty-host tree that agent-net stopped,
// but only after the supervisor's fresh roster no longer lists it. These tests
// use a fake process table, a fake roster and fake job and session files, so no
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

func treeTestDaemon(t *testing.T, root string, now time.Time) (*Daemon, *fakeClaudeProcs, *bytes.Buffer) {
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
			testClaudeNow:         func() time.Time { return now },
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

// writeClaudeRoster writes ~/.claude/daemon/roster.json as the supervisor
// does. Workers are keyed by job id; each holds the pty host pid and the
// claimed process pid.
func writeClaudeRoster(t *testing.T, root string, updatedAt time.Time, workers map[string][2]int) {
	t.Helper()
	dir := filepath.Join(root, "daemon")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"proto": 1, "supervisorPid": 1, "updatedAt": updatedAt.UnixMilli(), "workers": map[string]any{}}
	list := body["workers"].(map[string]any)
	for job, pids := range workers {
		list[job] = map[string]any{"pid": pids[0], "procStart": "100", "replPid": pids[1], "replProcStart": "101"}
	}
	raw, _ := json.Marshal(body)
	if err := os.WriteFile(filepath.Join(dir, "roster.json"), raw, 0o600); err != nil {
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

func TestClaudeStopEndsATreeThatLeftTheRoster(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	d, f, logs := treeTestDaemon(t, root, now)
	writeClaudeRoster(t, root, now.Add(-5*time.Second), nil)
	writeClaudeJobState(t, root, "abcd1234", "stopped")
	writeClaudeSessionFile(t, d.cfg.ClaudeSessionsDir, 4101, "abcd1234")
	claudeTreePids(f, 4100, 4101, 2*time.Minute)

	before := d.claudeBgPIDsForJob("abcd1234")
	d.finishClaudeStop("abcd1234", before)

	if f.alive(4100) || f.alive(4101) {
		t.Fatalf("the tree left the roster but was not ended: host=%v spare=%v signals=%v", f.alive(4100), f.alive(4101), f.signalled())
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

// A job in the roster is live, whatever its state.json says.
func TestClaudeStopKeepsATreeInTheRoster(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	d, f, _ := treeTestDaemon(t, root, now)
	writeClaudeRoster(t, root, now.Add(-5*time.Second), map[string][2]int{"abcd1234": {4100, 4101}})
	writeClaudeJobState(t, root, "abcd1234", "done")
	writeClaudeSessionFile(t, d.cfg.ClaudeSessionsDir, 4101, "abcd1234")
	claudeTreePids(f, 4100, 4101, 2*time.Hour)

	d.finishClaudeStop("abcd1234", d.claudeBgPIDsForJob("abcd1234"))

	if got := f.signalled(); len(got) != 0 {
		t.Fatalf("a tree in the roster was signalled: %v", got)
	}
	if !f.alive(4100) || !f.alive(4101) {
		t.Fatal("a tree in the roster was ended")
	}
}

func TestClaudeStopKeepsATreeWithAStaleRoster(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	d, f, _ := treeTestDaemon(t, root, now)
	writeClaudeRoster(t, root, now.Add(-10*time.Minute), nil)
	writeClaudeJobState(t, root, "abcd1234", "stopped")
	writeClaudeSessionFile(t, d.cfg.ClaudeSessionsDir, 4101, "abcd1234")
	claudeTreePids(f, 4100, 4101, 2*time.Hour)

	d.finishClaudeStop("abcd1234", d.claudeBgPIDsForJob("abcd1234"))

	if got := f.signalled(); len(got) != 0 {
		t.Fatalf("a stale roster was used to end a tree: %v", got)
	}
}

func TestClaudeStopKeepsAWorkingJobTree(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	d, f, _ := treeTestDaemon(t, root, now)
	writeClaudeRoster(t, root, now.Add(-5*time.Second), nil)
	writeClaudeJobState(t, root, "abcd1234", "working")
	writeClaudeSessionFile(t, d.cfg.ClaudeSessionsDir, 4101, "abcd1234")
	claudeTreePids(f, 4100, 4101, 2*time.Hour)

	d.finishClaudeStop("abcd1234", d.claudeBgPIDsForJob("abcd1234"))

	if got := f.signalled(); len(got) != 0 {
		t.Fatalf("a working job's tree was signalled: %v", got)
	}
}

func TestClaudeStopKeepsAReusedPID(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	d, f, _ := treeTestDaemon(t, root, now)
	writeClaudeRoster(t, root, now.Add(-5*time.Second), nil)
	writeClaudeJobState(t, root, "abcd1234", "done")
	writeClaudeSessionFile(t, d.cfg.ClaudeSessionsDir, 4101, "abcd1234")
	claudeTreePids(f, 4100, 4101, 2*time.Hour)
	// The process numbers were reused by other programs after the tree was
	// captured: the start time no longer matches.
	f.onRead = func(read int) {
		if read >= 3 {
			f.setToken(4100, "other")
			f.setToken(4101, "other")
		}
	}

	d.finishClaudeStop("abcd1234", d.claudeBgPIDsForJob("abcd1234"))

	if got := f.signalled(); len(got) != 0 {
		t.Fatalf("a reused process number was signalled: %v", got)
	}
}

func TestClaudeStopCheckRunsAfterTheDelay(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	d, f, _ := treeTestDaemon(t, root, now)
	writeClaudeRoster(t, root, now.Add(-5*time.Second), nil)
	writeClaudeJobState(t, root, "abcd1234", "stopped")
	writeClaudeSessionFile(t, d.cfg.ClaudeSessionsDir, 4101, "abcd1234")
	claudeTreePids(f, 4100, 4101, 2*time.Minute)

	d.afterClaudeStop("abcd1234")

	eventually(t, "the stopped job's tree was ended", func() bool {
		return !f.alive(4100) && !f.alive(4101)
	})
}

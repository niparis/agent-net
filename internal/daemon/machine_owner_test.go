package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/usage"
)

// claudeOwnerFixture is ownerFixture plus extra processes, each with a command
// line. Every process sits under init, so no process tree names its owner:
// only the process's own facts can.
func claudeOwnerFixture(end time.Time, extra map[int]string) *scriptReader {
	r := ownerFixture(end)
	for i := range r.snaps {
		for pid := range extra {
			// The most CPU of all, so every extra process is in the top list.
			r.snaps[i].Procs[pid] = usage.Proc{PID: pid, PPID: 1, Start: uint64(pid), CPU: 10 * float64(i*5+1), RSS: uint64(pid) << 20, Comm: "claude", Elapsed: time.Minute}
		}
	}
	for pid, args := range extra {
		r.args[pid] = args
	}
	return r
}

// claudeOwnerDaemon builds a daemon over a fixture sampler. Its state folder
// and roster file are temporary, and cwds is what the machine says each
// process's working directory is.
func claudeOwnerDaemon(t *testing.T, extra map[int]string, cwds map[int]string, roster string) *Daemon {
	t.Helper()
	old := processCWDs
	processCWDs = func(pids []int) map[int]string {
		out := map[int]string{}
		for _, pid := range pids {
			if c, ok := cwds[pid]; ok {
				out[pid] = c
			}
		}
		return out
	}
	t.Cleanup(func() { processCWDs = old })
	s := usage.NewWith(claudeOwnerFixture(time.Now(), extra), time.Hour, 5*time.Minute, nil)
	ctx := context.Background()
	s.Sample(ctx)
	s.Sample(ctx)
	if roster == "" {
		roster = os.DevNull
	}
	return &Daemon{cfg: Config{UsageSampler: s, StateDir: t.TempDir(), claudeRosterPath: roster}, sessions: map[string]*localSession{}, workers: map[string]*worker{}}
}

func topRow(t *testing.T, rows []protocol.MachineProcRow, pid int) protocol.MachineProcRow {
	t.Helper()
	for _, r := range rows {
		if r.PID == pid {
			return r
		}
	}
	t.Fatalf("pid %d is not in the top list %+v", pid, rows)
	return protocol.MachineProcRow{}
}

// A "claude bg-spare" process is not a "claude --resume" process: nothing in
// its command line names a session. Its working directory is the worktree of
// the worker it serves, and the operating system says so.
func TestMachineUsageAttributesBgSpareByWorktreeCWD(t *testing.T) {
	d := claudeOwnerDaemon(t,
		map[int]string{600: "claude bg-spare --bg-spare /tmp/cc-daemon/spare.sock", 610: "claude bg-spare --bg-spare /tmp/cc-daemon/other.sock", 620: "claude bg-spare --bg-spare /tmp/cc-daemon/old.sock"},
		nil, "")
	wt := d.worktreesDir()
	d.workers["w_abc"] = &worker{id: "w_abc", title: "Fix the thing"}
	processCWDs = func(pids []int) map[int]string {
		return map[int]string{
			600: filepath.Join(wt, "w_abc"),
			610: filepath.Join(filepath.Dir(wt), "worktrees-old", "w_abc"), // a sibling folder, not the worktrees folder
			620: filepath.Join(wt, "w_gone", "internal"),                   // below the worktree of a worker that is gone
		}
	}
	got := d.machineUsageNow(context.Background())

	row := topRow(t, got.Top, 600)
	if row.OwnerKind != protocol.ProcOwnerWorker || row.OwnerID != "w_abc" || row.Owner != "Fix the thing" || row.OwnerRule != "cwd in worker worktree" {
		t.Errorf("bg-spare in a worker worktree: %+v", row)
	}
	if row := topRow(t, got.Top, 610); row.OwnerKind != protocol.ProcOwnerOther || row.OwnerRule != "" {
		t.Errorf("a folder next to the worktrees folder must not count: %+v", row)
	}
	if row := topRow(t, got.Top, 620); row.OwnerKind != protocol.ProcOwnerWorker || row.OwnerID != "w_gone" {
		t.Errorf("a process below a worktree belongs to that worker: %+v", row)
	}
}

// A process the Claude supervisor lists in its roster belongs to that
// background session, with no working directory read at all.
func TestMachineUsageAttributesRosterProcesses(t *testing.T) {
	stateDir := t.TempDir()
	wt := filepath.Join(stateDir, "worktrees", "w_roster")
	roster := writeClaudeRoster(t, 0, time.Now(), map[string]any{
		"aaaa1111": map[string]any{"pid": 700, "replPid": 701, "sessionId": "native-1", "cwd": wt},
		"bbbb2222": map[string]any{"pid": 710, "replPid": 711, "sessionId": "native-2", "cwd": "/home/x/repo"},
		"cccc3333": map[string]any{"pid": 720, "replPid": 721, "sessionId": "native-3", "cwd": "/home/x/other"},
	})
	d := claudeOwnerDaemon(t, map[int]string{
		700: "claude bg-pty-host", 701: "claude bg-spare --bg-spare /tmp/s.sock",
		711: "claude bg-spare --bg-spare /tmp/t.sock", 721: "claude bg-spare --bg-spare /tmp/u.sock",
	}, nil, roster)
	d.cfg.StateDir = stateDir
	d.sessions["s_native"] = &localSession{sessionID: "s_native", info: protocol.SessionInfo{Harness: "claude", NativeID: "native-2", Name: "my session"}}
	got := d.machineUsageNow(context.Background())

	if row := topRow(t, got.Top, 701); row.OwnerKind != protocol.ProcOwnerWorker || row.OwnerID != "w_roster" || row.OwnerRule != "Claude roster pid" {
		t.Errorf("roster process in a worker worktree: %+v", row)
	}
	if row := topRow(t, got.Top, 711); row.OwnerKind != protocol.ProcOwnerSession || row.OwnerID != "s_native" || row.Owner != "my session" || row.OwnerRule != "Claude roster pid" {
		t.Errorf("roster process of a registered session: %+v", row)
	}
	if row := topRow(t, got.Top, 721); row.OwnerKind != protocol.ProcOwnerSession || row.OwnerID != "cccc3333" || row.OwnerRule != "Claude roster pid" {
		t.Errorf("roster process of a session agent-net does not know: %+v", row)
	}
}

// The CTO checks the roster before it cleans up, so agent_machine_get shows
// whether it is fresh and how old it is.
func TestMachineUsageShowsClaudeRosterFreshness(t *testing.T) {
	now := time.Now()
	sessions := map[string]any{"s1": map[string]any{"pid": 1001}, "s2": map[string]any{"pid": 1002}}
	cases := []struct {
		name       string
		path       string
		state      string
		minAge     int64
		maxAge     int64
		supervisor int
	}{
		{"legacy roster written seconds ago", writeClaudeRoster(t, 0, now.Add(-20*time.Second), sessions), protocol.ClaudeRosterFresh, 20, 24, 0},
		{"legacy roster written minutes ago", writeClaudeRoster(t, 0, now.Add(-5*time.Minute), sessions), protocol.ClaudeRosterStale, 300, 304, 0},
		{"supervisor alive", writeClaudeRoster(t, os.Getpid(), now, sessions), protocol.ClaudeRosterFresh, 0, 4, os.Getpid()},
		{"supervisor gone", writeClaudeRoster(t, 2147483000, now, sessions), protocol.ClaudeRosterUnverified, 0, 4, 2147483000},
		{"no roster file", filepath.Join(t.TempDir(), "none.json"), protocol.ClaudeRosterMissing, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := claudeOwnerDaemon(t, nil, nil, tc.path)
			r := d.machineUsageNow(context.Background()).Roster
			if r == nil {
				t.Fatal("the usage block has no roster status")
			}
			if r.State != tc.state || r.Note == "" || r.SupervisorPID != tc.supervisor {
				t.Errorf("status = %+v, want state %s", r, tc.state)
			}
			if r.AgeSeconds < tc.minAge || r.AgeSeconds > tc.maxAge {
				t.Errorf("age = %d s, want %d to %d", r.AgeSeconds, tc.minAge, tc.maxAge)
			}
			if tc.state == protocol.ClaudeRosterMissing {
				if r.UpdatedAt != "" || r.Sessions != 0 {
					t.Errorf("a missing roster has no time and no sessions: %+v", r)
				}
				return
			}
			if _, err := time.Parse(time.RFC3339, r.UpdatedAt); err != nil || r.Sessions != 2 {
				t.Errorf("status = %+v", r)
			}
		})
	}
}

// The process list gives the same attribution as the usage block, and the same
// roster status.
func TestMachineProcessesShowRosterAndCWDOwner(t *testing.T) {
	d := claudeOwnerDaemon(t, map[int]string{600: "claude bg-spare --bg-spare /tmp/s.sock"}, nil, "")
	processCWDs = func(pids []int) map[int]string { return map[int]string{600: filepath.Join(d.worktreesDir(), "w_abc")} }
	now := time.Now()
	fixture := claudeOwnerFixture(now, map[int]string{600: ""})
	rows, roster, err := d.machineProcesses(context.Background(), "rss", fixtureProcessTable(now, fixture))
	if err != nil {
		t.Fatal(err)
	}
	if roster == nil || roster.State != protocol.ClaudeRosterMissing {
		t.Errorf("roster = %+v", roster)
	}
	for _, r := range rows {
		if r.PID == 600 {
			if r.OwnerKind != protocol.ProcOwnerWorker || r.OwnerID != "w_abc" || r.OwnerRule != "cwd in worker worktree" {
				t.Errorf("row = %+v", r)
			}
			return
		}
	}
	t.Fatal("pid 600 is not in the process list")
}

func TestParseLsofCWD(t *testing.T) {
	out := "p101\nfcwd\nn/Users/x/code/a\np202\nfcwd\nn/tmp/with space\np303\nfcwd\n"
	got := parseLsofCWD([]byte(out))
	if len(got) != 2 || got[101] != "/Users/x/code/a" || got[202] != "/tmp/with space" {
		t.Errorf("parsed %v", got)
	}
}

func TestOSProcessCWDsReadsOwnDirectory(t *testing.T) {
	want, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got := osProcessCWDs([]int{os.Getpid(), 2147483000})
	if got[os.Getpid()] != want || len(got) != 1 {
		t.Errorf("got %v, want only this process at %s", got, want)
	}
}

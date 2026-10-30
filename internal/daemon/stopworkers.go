package daemon

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// StopRecordedWorkers stops the workers that the daemon in stateDir left
// running, and says how many it stopped. Workers outlive the daemon on
// purpose (the next daemon adopts them), so when agent-net is removed for good
// nobody else stops them. It reads the worker rows from the daemon database,
// so it works with the daemon down. A row counts only if the process is still
// the same run (same start time), so a reused process ID is left alone. Each
// process group gets SIGTERM, then SIGKILL after grace.
func StopRecordedWorkers(stateDir string, grace time.Duration) (int, error) {
	if _, err := os.Stat(filepath.Join(stateDir, "daemon.db")); err != nil {
		return 0, nil // no daemon ever ran here
	}
	st, err := openState(stateDir)
	if err != nil {
		return 0, err
	}
	rows, err := st.workers()
	st.close()
	if err != nil {
		return 0, err
	}
	var groups []workerRow
	for _, r := range rows {
		if r.EndedReason != "" || r.PID <= 0 {
			continue
		}
		if tok, alive := procToken(r.PID); alive && tok == r.StartToken {
			groups = append(groups, r)
			pgid := r.PGID
			if pgid <= 0 {
				pgid = r.PID
			}
			syscall.Kill(-pgid, syscall.SIGTERM)
		}
	}
	deadline := time.Now().Add(grace)
	for _, r := range groups {
		for time.Now().Before(deadline) {
			if tok, alive := procToken(r.PID); !alive || tok != r.StartToken {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		pgid := r.PGID
		if pgid <= 0 {
			pgid = r.PID
		}
		syscall.Kill(-pgid, syscall.SIGKILL) // also stragglers in the group
	}
	return len(groups), nil
}

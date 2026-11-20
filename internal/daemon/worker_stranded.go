package daemon

import "github.com/niparis/agent-net/internal/protocol"

// A Claude job that a cut-short worker launch left behind is up, with no
// worker record, in the folder of a worker. If it were listed it would be an
// ordinary session of the owner that nobody started ("agent-net 3", idle, no
// task). A session in that folder that no worker record names is never listed,
// and its job is stopped once.
//
// SetSessions calls this with claudeStartMu held, so a launch that is still
// binding its session to its worker has finished before this looks.
func (d *Daemon) strandedWorkerSession(info protocol.SessionInfo) bool {
	if info.Worker != nil || !d.inWorktreesDir(info.Cwd) {
		return false
	}
	if d.adopted != nil {
		select {
		case <-d.adopted:
		default:
			// The workers of the last run are not taken back yet, so this may
			// be one of them. It is listed when they are.
			return true
		}
	}
	if d.st.workerNativeKnown(info.Harness, info.NativeID) {
		return false
	}
	if _, done := d.strandedStopped.LoadOrStore(info.NativeID, true); !done {
		job := claudeSessionJobs(d.cfg.ClaudeSessionsDir)[info.PID]
		d.Logf("worker.stranded session=%s job=%s cwd=%s: a Claude job in a worker folder has no worker; it is stopped and not listed", info.NativeID, job, info.Cwd)
		if job != "" {
			d.goBackground(func() { claudeLauncher{}.stopID(d, job) })
		}
	}
	return true
}

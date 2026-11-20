package daemon

import "github.com/niparis/agent-net/internal/protocol"

// A Claude job that a cut-short worker launch left behind is up, with no
// worker record, in the folder of a worker. If it were listed it would be an
// ordinary session of the owner that nobody started ("agent-net 3", idle, no
// task). A background job in that folder that no worker record names is never
// listed, and it is stopped once.
//
// Only background jobs (they carry a job id) are held to this. The owner may
// open an interactive session in a worker folder to look at its work; that
// session is listed and never stopped.
//
// SetSessions calls this with claudeStartMu held, so a launch that is still
// binding its session to its worker has finished before this looks.
func (d *Daemon) strandedWorkerSession(info protocol.SessionInfo) bool {
	if info.Worker != nil || info.Mode != protocol.ModeBackground || !d.inWorktreesDir(info.Cwd) {
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
	if _, done := d.strandedStopped.Load(info.NativeID); done {
		return true
	}
	job := claudeSessionJobs(d.cfg.ClaudeSessionsDir)[info.PID]
	if job == "" {
		return false
	}
	d.strandedStopped.Store(info.NativeID, true)
	d.Logf("worker.stranded session=%s job=%s cwd=%s: a Claude job in a worker folder has no worker; it is stopped and not listed", info.NativeID, job, info.Cwd)
	d.goBackground(func() { claudeLauncher{}.stopID(d, job) })
	return true
}

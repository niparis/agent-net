package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/claudesettings"
	"github.com/niparis/agent-net/internal/protocol"
)

// One mechanism decides whether Claude Code sessions have mouse tracking on:
// agent-net writes CLAUDE_CODE_DISABLE_MOUSE=0 to the user's Claude settings
// file, which Claude Code applies inside every session (decision 0128). The
// daemon ensures that value at start and every minute, reports live sessions
// that did not see it, and restarts a background service that carries the old
// value once it hosts no live sessions.

const (
	// claudeMouseNoticeEvery is how long one session or service stays quiet
	// after a report.
	claudeMouseNoticeEvery = 24 * time.Hour
	// claudeDaemonStopTimeout bounds the supported `claude daemon stop` call.
	claudeDaemonStopTimeout = 30 * time.Second
)

// mouseProcView reads a process's start time. Tests replace it so a session
// can look older or newer than the settings file.
var mouseProcView = readProcView

// claudeDaemonStop stops Claude Code's background service. It is a variable so
// tests never run the real command. The supported command is
// `claude daemon stop --any --keep-workers` (see `claude daemon --help`).
// `stop` also ends the background sessions; `--keep-workers` leaves a detached
// session running, so even a session that started between the roster read and
// the stop is never killed.
var claudeDaemonStop = func(ctx context.Context, bin string) error {
	cctx, cancel := context.WithTimeout(ctx, claudeDaemonStopTimeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, bin, "daemon", "stop", "--any", "--keep-workers").CombinedOutput()
	if err != nil {
		if detail := strings.TrimSpace(string(out)); detail != "" {
			return fmt.Errorf("%v: %s", err, detail)
		}
		return err
	}
	return nil
}

// claudeSettingsFile is the user settings file agent-net owns for the mouse
// choice. It follows $CLAUDE_CONFIG_DIR, or ~/.claude/settings.json.
func (d *Daemon) claudeSettingsFile() string {
	if d.cfg.claudeSettingsPath != "" {
		return d.cfg.claudeSettingsPath
	}
	return claudesettings.File()
}

// claudeMouseMaintenance is one pass of the Claude user settings and session
// work. The daemon runs it at start and from the integrations loop every
// minute: no timer of its own.
func (d *Daemon) claudeMouseMaintenance(ctx context.Context) {
	now := time.Now()
	d.ensureClaudeMouseSetting(ctx)
	d.ensureClaudeAutoUpdateSetting(ctx)
	d.checkClaudeMouseSessions(ctx, now)
	d.checkClaudeMouseService(ctx, now)
}

// ensureClaudeMouseSetting writes the choice when the file does not carry it.
// It never touches another key, and never writes a file that is not valid
// JSON: that file is reported instead.
func (d *Daemon) ensureClaudeMouseSetting(ctx context.Context) {
	path := d.claudeSettingsFile()
	if path == "" {
		return
	}
	changed, err := claudesettings.EnsureMouseOn(path)
	if err != nil {
		if claudesettings.IsNotJSON(err) {
			d.reportMouseNotice(ctx, "settings:"+path, fmt.Sprintf(
				"Claude settings %s is not valid JSON; the wheel setting was not applied", path))
			return
		}
		d.Logf("claude mouse: %v", err)
		return
	}
	if changed {
		d.Logf("claude mouse: set %s=%s in %s", claudesettings.MouseKey, claudesettings.MouseOn, path)
	}
}

// checkClaudeMouseSessions reports every live Claude session that does not see
// the choice. The user settings file applies from the moment Claude reads it,
// which is when the session starts; a file written after that did not reach a
// running session, so its inherited environment still decides.
func (d *Daemon) checkClaudeMouseSessions(ctx context.Context, now time.Time) {
	d.mu.Lock()
	sessions := make([]protocol.SessionInfo, 0, len(d.sessions))
	for _, ls := range d.sessions {
		if ls.info.Harness == "claude" && ls.info.PID > 1 {
			sessions = append(sessions, ls.info)
		}
	}
	d.mu.Unlock()
	// The CTO keeps its own deliberate exception in its launch settings, so
	// its inherited environment does not decide anything for it.
	d.cto.mu.Lock()
	ctoSession := d.claudeCTO.session
	d.cto.mu.Unlock()
	ctoDir := ""
	if d.cfg.StateDir != "" {
		ctoDir = filepath.Join(d.cfg.StateDir, "cto", "claude")
	}
	path := d.claudeSettingsFile()
	for _, s := range sessions {
		if ctoSession != "" && s.NativeID == ctoSession {
			continue
		}
		if ctoDir != "" && filepath.Clean(s.Cwd) == filepath.Clean(ctoDir) {
			continue
		}
		if _, alive := procToken(s.PID); !alive {
			continue
		}
		if !d.claudeSessionMouseOff(s, path) {
			continue
		}
		name := s.Name
		if name == "" {
			name = s.NativeID
		}
		d.reportMouseNotice(ctx, "session:"+key(s.Harness, s.NativeID), fmt.Sprintf(
			"%s on %s: wheel off (mouse tracking disabled); it scrolls after its next restart",
			name, d.hostName()))
	}
}

// claudeSessionMouseOff says whether s runs with mouse tracking off. The
// process environment is the base; the user settings value applies when the
// file was written before the process started, because Claude Code reads it at
// start only.
func (d *Daemon) claudeSessionMouseOff(s protocol.SessionInfo, path string) bool {
	value := processEnvVar(s.PID, claudesettings.MouseKey)
	started := time.Time{}
	if v, err := mouseProcView(s.PID); err == nil {
		started = v.Started
	}
	if path != "" && !started.IsZero() {
		if info, statErr := os.Stat(path); statErr == nil {
			// Process start times are whole seconds, so a write in the same
			// second still counts as read by the session.
			if valueAt, present, valueErr := claudesettings.MouseValue(path); valueErr == nil && present && !info.ModTime().After(started.Add(2*time.Second)) {
				value = valueAt
			}
		}
	}
	return mouseValueOff(value)
}

// mouseValueOff tells whether a value of CLAUDE_CODE_DISABLE_MOUSE turns mouse
// tracking off. Unset and "0" keep it on; every other value is not the choice
// agent-net makes (decision 0128).
func mouseValueOff(value string) bool {
	return value != "" && value != "0"
}

// ---- Claude Code's background service ----

type claudeMouseAction int

const (
	claudeMouseNothing claudeMouseAction = iota
	claudeMouseRestartService
	claudeMouseWaitService
)

// claudeMouseServiceAction decides what to do about a service whose
// environment carries the old value. It restarts it only when no live session
// would be ended; otherwise it waits.
func claudeMouseServiceAction(value string, liveSessions int) claudeMouseAction {
	if !mouseValueOff(value) {
		return claudeMouseNothing
	}
	if liveSessions > 0 {
		return claudeMouseWaitService
	}
	return claudeMouseRestartService
}

// claudeDaemonServicePIDs lists Claude Code background supervisors, the
// `claude daemon run ...` processes. It never matches `claude daemon stop`.
func claudeDaemonServicePIDs() []int {
	var out []int
	for _, pid := range processList() {
		args := processArgs(pid)
		if len(args) < 3 {
			continue
		}
		if claudeProgram(args[0]) && args[1] == "daemon" && args[2] == "run" {
			out = append(out, pid)
		}
	}
	return out
}

// checkClaudeMouseService finds a background service that still carries the
// old value. A service with live sessions is reported once and left alone; a
// service with no live sessions is stopped, and the next launch starts a new
// one with the current environment. The two cases keep their own once-a-day
// record, so a service reported as busy is still stopped when it later has no
// live session, without waiting for the next report window.
func (d *Daemon) checkClaudeMouseService(ctx context.Context, now time.Time) {
	for _, pid := range claudeDaemonServicePIDs() {
		value := processEnvVar(pid, claudesettings.MouseKey)
		live := d.claudeServiceLiveSessions(now)
		// A roster that cannot be read means live sessions cannot be ruled
		// out, so it is never a restart.
		if live < 0 {
			live = 1
		}
		switch claudeMouseServiceAction(value, live) {
		case claudeMouseRestartService:
			// One stop per service per day, even when the stop keeps failing.
			if !d.mouseNoticeDue("stop:"+fmt.Sprint(pid), now) {
				continue
			}
			bin, err := d.harnessBinary("claude")
			if err != nil {
				d.Logf("claude mouse: background service %d: %v", pid, err)
				continue
			}
			if err := claudeDaemonStop(ctx, bin); err != nil {
				d.Logf("claude mouse: stop background service %d: %v", pid, err)
				continue
			}
			d.markMouseNotice("stop:"+fmt.Sprint(pid), now)
			d.Logf("claude mouse: stopped background service %d with no live sessions; the next launch starts a clean one", pid)
			d.reportMouseNotice(ctx, "service-stop:"+fmt.Sprint(pid), fmt.Sprintf(
				"%s: Claude's background service had mouse tracking off and no live sessions; it was stopped, and the next launch starts a clean one",
				d.hostName()))
		case claudeMouseWaitService:
			d.reportMouseNotice(ctx, "service:"+fmt.Sprint(pid), fmt.Sprintf(
				"%s: Claude's background service has mouse tracking off; it will be restarted when it has no live sessions",
				d.hostName()))
		}
	}
}

// claudeServiceLiveSessions counts the live sessions in Claude's roster, or
// returns -1 when the roster is missing or stale and nothing can be counted.
func (d *Daemon) claudeServiceLiveSessions(now time.Time) int {
	roster := d.readClaudeRoster(now)
	if roster == nil {
		return -1
	}
	live := 0
	for _, w := range roster.Workers {
		for _, pid := range []int{w.PID, w.ReplPID} {
			if pid > 1 {
				if _, alive := procToken(pid); alive {
					live++
					break
				}
			}
		}
	}
	return live
}

// reportMouseNotice shows one line in the owner's TUI and writes it to the
// Desk log. It keeps to one report per key per day.
func (d *Daemon) reportMouseNotice(ctx context.Context, key, text string) {
	now := time.Now()
	if !d.mouseNoticeDue(key, now) {
		return
	}
	d.markMouseNotice(key, now)
	d.showMouseNotice(ctx, key, text)
}

// showMouseNotice sends the line to the local TUIs and the Desk log. If the
// gateway link is not connected yet, it retries the Desk note with backoff.
func (d *Daemon) showMouseNotice(ctx context.Context, key, text string) {
	d.mouseMu.Lock()
	if d.mouseNotices == nil {
		d.mouseNotices = map[string]time.Time{}
	}
	pendingKey := "\x00pending:" + key
	if _, pending := d.mouseNotices[pendingKey]; pending {
		d.mouseMu.Unlock()
		return
	}
	d.mouseNotices[pendingKey] = time.Time{}
	d.mouseMu.Unlock()
	clearPending := func() {
		d.mouseMu.Lock()
		delete(d.mouseNotices, pendingKey)
		d.mouseMu.Unlock()
	}
	d.broadcastCTOUI(protocol.UIRequest{Text: text, DaemonNotice: true})
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, err := d.call(cctx, "desk.note", protocol.DeskNote{Text: text})
	cancel()
	if err == nil {
		clearPending()
		return
	}
	if !isMouseNoticeOffline(err) {
		d.Logf("claude mouse: desk log: %v", err)
		clearPending()
		return
	}
	bctx := ctx
	if d.ctoCtx != nil {
		bctx = d.ctoCtx
	}
	if bctx == nil {
		bctx = context.Background()
	}
	d.goBackground(func() {
		defer clearPending()
		delay := time.Second
		for {
			timer := time.NewTimer(delay)
			select {
			case <-bctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			cctx, cancel := context.WithTimeout(bctx, 10*time.Second)
			_, err := d.call(cctx, "desk.note", protocol.DeskNote{Text: text})
			cancel()
			if err == nil {
				return
			}
			if !isMouseNoticeOffline(err) {
				d.Logf("claude mouse: desk log: %v", err)
				return
			}
			delay *= 2
			if delay > time.Minute {
				delay = time.Minute
			}
		}
	})
}

func isMouseNoticeOffline(err error) bool {
	return errors.Is(err, errNotConnected) || (err != nil && strings.Contains(err.Error(), "not connected to the gateway"))
}

// mouseNoticeDue says whether key may be reported. The record survives a
// daemon restart through the state database; a daemon without one keeps it in
// memory.
func (d *Daemon) mouseNoticeDue(key string, now time.Time) bool {
	if d.st != nil {
		if at, ok, err := d.st.mouseNoticeAt(key); err == nil && ok && now.Sub(at) < claudeMouseNoticeEvery {
			return false
		}
	}
	d.mouseMu.Lock()
	defer d.mouseMu.Unlock()
	at, ok := d.mouseNotices[key]
	return !ok || now.Sub(at) >= claudeMouseNoticeEvery
}

// markMouseNotice records a report so the same key stays quiet for a day.
func (d *Daemon) markMouseNotice(key string, now time.Time) {
	if d.st != nil {
		if err := d.st.setMouseNotice(key, now); err == nil {
			return
		}
	}
	d.mouseMu.Lock()
	defer d.mouseMu.Unlock()
	if d.mouseNotices == nil {
		d.mouseNotices = map[string]time.Time{}
	}
	d.mouseNotices[key] = now
}

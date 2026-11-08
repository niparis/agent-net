package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/daemon/opencodeapi"
	"github.com/niparis/agent-net/internal/protocol"
)

// resetIdleNotice starts a new period when the harness starts another turn.
func (d *Daemon) resetIdleNotice(w *worker) {
	d.wmu.Lock()
	if w.idleNoticeCancel != nil {
		w.idleNoticeCancel()
		w.idleNoticeCancel = nil
	}
	w.idleNoticeSince = time.Time{}
	w.idleNoticeSent = false
	w.idleNoticePending = false
	w.idleNoticeReport = ""
	w.idleNoticeKey = ""
	w.idleNoticeGeneration++
	if err := d.st.deleteWorkerIdleNotice(w.id); err != nil {
		d.Logf("worker %s: reset idle notice: %v", w.id, err)
	}
	d.wmu.Unlock()
}

// noteCodexIdle keeps the final text if an automatic hand-back failed, so
// adoption can still report it. The notice itself applies the same size limit.
func (d *Daemon) noteCodexIdle(w *worker, report string) {
	if strings.TrimSpace(report) == "" {
		report = "(Codex ended the turn with no message.)"
	}
	if len(report) > 48*1024 {
		report = strings.ToValidUTF8(report[:48*1024], "") + "\n[report cut at 48 KB]"
	}
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if w.stopping {
		return
	}
	w.idleNoticeReport = report
	w.idleNoticeSince = time.Now()
	w.idleNoticeKey = protocol.NewID("k")
	if err := d.st.saveWorkerIdleNotice(w.id, w.idleNoticeSince, w.idleNoticeKey, report, false); err != nil {
		w.idleNoticeSince, w.idleNoticeKey = time.Time{}, ""
		d.Logf("worker %s: save idle notice: %v", w.id, err)
	}
}

// checkIdleNotice only acts on harnesses with a known end of turn. Codex
// normally hands back automatically; it can remain idle if that send failed.
// Claude reports the same idle status in its session file, and keeps it across
// a daemon restart, so an adopted Claude worker gets the notice too.
func (d *Daemon) checkIdleNotice(ctx context.Context, w *worker, now time.Time, delay time.Duration, stats workerStats) {
	d.wmu.Lock()
	harness, native := w.harness, w.nativeID
	d.wmu.Unlock()
	if harness != "opencode" && harness != "codex" && harness != "agy" && harness != "claude" {
		return
	}
	d.mu.Lock()
	ls := d.sessions[key(harness, native)]
	idle := ls != nil && (ls.info.Status == "idle" || ls.info.Status == "dormant")
	busy := ls != nil && ls.info.Status == "busy"
	d.mu.Unlock()
	d.wmu.Lock()
	if busy && !w.idleNoticeSince.IsZero() {
		if w.idleNoticeCancel != nil {
			w.idleNoticeCancel()
			w.idleNoticeCancel = nil
		}
		w.idleNoticeSince = time.Time{}
		w.idleNoticeSent = false
		w.idleNoticePending = false
		w.idleNoticeReport = ""
		w.idleNoticeKey = ""
		w.idleNoticeGeneration++
		if err := d.st.deleteWorkerIdleNotice(w.id); err != nil {
			d.Logf("worker %s: reset idle notice: %v", w.id, err)
		}
	}
	eligible := idle && !w.stopping && w.doneStatus == "" && w.taskMsg != "" &&
		((harness == "opencode" && !w.turnActive && !w.turnAwaitingStart && len(w.turnQueue) == 0) ||
			harness == "claude" ||
			((harness == "codex" || harness == "agy") && w.idleNoticeReport != ""))
	if !eligible {
		d.wmu.Unlock()
		return
	}
	if w.idleNoticeSince.IsZero() {
		w.idleNoticeSince = now
		w.idleNoticeKey = protocol.NewID("k")
		if err := d.st.saveWorkerIdleNotice(w.id, now, w.idleNoticeKey, w.idleNoticeReport, false); err != nil {
			w.idleNoticeSince, w.idleNoticeKey = time.Time{}, ""
			d.wmu.Unlock()
			d.Logf("worker %s: save idle notice: %v", w.id, err)
			return
		}
	}
	if w.idleNoticeSent || w.idleNoticePending || now.Sub(w.idleNoticeSince) < delay {
		d.wmu.Unlock()
		return
	}
	w.idleNoticePending = true
	generation, report, base, pass, version, identity, noticeKey, cwd := w.idleNoticeGeneration, w.idleNoticeReport, w.apiBase, w.apiPass, w.apiVersion, w.apiIdentity, w.idleNoticeKey, w.path
	noticeCtx, cancelNotice := context.WithCancel(ctx)
	w.idleNoticeCancel = cancelNotice
	d.wmu.Unlock()
	d.goBackground(func() {
		defer cancelNotice()
		if harness == "opencode" {
			readCtx, cancel := context.WithTimeout(noticeCtx, 3*time.Second)
			report = d.openCodeLastAssistant(readCtx, base, pass, version, identity, native)
			cancel()
		}
		if harness == "claude" {
			report = d.claudeWorkerLastAssistant(native, cwd)
		}
		if strings.TrimSpace(report) == "" {
			report = "(No assistant message was available.)"
		}
		d.wmu.Lock()
		valid := d.workers[w.id] == w && !w.stopping && w.doneStatus == "" && w.idleNoticeGeneration == generation && w.idleNoticePending
		d.wmu.Unlock()
		if !valid {
			return
		}
		// The worker stopped early without a hand-back: send it our own
		// continue, up to the limit, instead of telling the caller yet.
		if d.tryAutoContinue(ctx, w) {
			return
		}
		n := d.notice(w, "idle without hand-back", "This is the worker's last assistant message, not a hand-back. The worker is still running.", stats)
		d.wmu.Lock()
		valid = d.workers[w.id] == w && !w.stopping && w.doneStatus == "" && w.idleNoticeGeneration == generation && w.idleNoticePending
		d.wmu.Unlock()
		if !valid {
			return
		}
		_, err := d.sendNoticeToKey(noticeCtx, w, n, report, "", "", noticeKey, false)
		d.wmu.Lock()
		if w.idleNoticeGeneration == generation && !w.stopping {
			w.idleNoticeCancel = nil
			w.idleNoticePending = false
			if err == nil {
				w.idleNoticeSent = true
				if saveErr := d.st.saveWorkerIdleNotice(w.id, w.idleNoticeSince, noticeKey, "", true); saveErr != nil {
					d.Logf("worker %s: save idle notice: %v", w.id, saveErr)
				}
			}
		}
		d.wmu.Unlock()
		if err != nil {
			d.Logf("worker %s: idle notice: %v", w.id, err)
		}
	})
}

func openCodeLastAssistant(ctx context.Context, base, pass, native string) string {
	return (*Daemon)(nil).openCodeLastAssistant(ctx, base, pass, "", "", native)
}

func (d *Daemon) openCodeLastAssistant(ctx context.Context, base, pass, version, identity, native string) string {
	text, _ := d.openCodeLastAssistantErr(ctx, base, pass, version, identity, native)
	return text
}

func (d *Daemon) openCodeLastAssistantErr(ctx context.Context, base, pass, version, identity, native string) (string, error) {
	path, err := d.openCodePath(ctx, base, pass, version, identity, opencodeapi.Messages, native)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, opencodeapi.Messages.Method(), strings.TrimRight(base, "/")+path+"?type=assistant&limit=1", nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth("opencode", pass)
	resp, err := openCodeHTTPClient(10 * time.Second).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var page struct {
		Data []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&page) != nil {
		return "", nil
	}
	for _, message := range page.Data {
		if message.Type != "assistant" {
			continue
		}
		var parts []string
		for _, part := range message.Content {
			if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
				parts = append(parts, part.Text)
			}
		}
		return strings.Join(parts, "\n"), nil
	}
	return "", nil
}

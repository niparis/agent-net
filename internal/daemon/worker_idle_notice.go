package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// resetIdleNotice starts a new period when the harness starts another turn.
func (d *Daemon) resetIdleNotice(w *worker) {
	d.wmu.Lock()
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
func (d *Daemon) checkIdleNotice(ctx context.Context, w *worker, now time.Time, delay time.Duration, stats workerStats) {
	d.wmu.Lock()
	harness, native := w.harness, w.nativeID
	d.wmu.Unlock()
	if harness != "opencode" && harness != "codex" {
		return
	}
	d.mu.Lock()
	ls := d.sessions[key(harness, native)]
	idle := ls != nil && (ls.info.Status == "idle" || ls.info.Status == "dormant")
	busy := ls != nil && ls.info.Status == "busy"
	d.mu.Unlock()
	d.wmu.Lock()
	if busy && !w.idleNoticeSince.IsZero() {
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
			(harness == "codex" && w.idleNoticeReport != ""))
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
	generation, report, base, pass, noticeKey := w.idleNoticeGeneration, w.idleNoticeReport, w.apiBase, w.apiPass, w.idleNoticeKey
	d.wmu.Unlock()
	d.goBackground(func() {
		if harness == "opencode" {
			readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			report = openCodeLastAssistant(readCtx, base, pass, native)
			cancel()
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
		n := d.notice(w, "idle without hand-back", "This is the worker's last assistant message, not a hand-back. The worker is still running.", stats)
		d.wmu.Lock()
		valid = d.workers[w.id] == w && !w.stopping && w.doneStatus == "" && w.idleNoticeGeneration == generation && w.idleNoticePending
		d.wmu.Unlock()
		if !valid {
			return
		}
		_, err := d.sendNoticeToKey(ctx, w, n, report, "", "", noticeKey, false)
		d.wmu.Lock()
		if w.idleNoticeGeneration == generation && !w.stopping {
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
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/api/session/"+url.PathEscape(native)+"/message?type=assistant&limit=1", nil)
	if err != nil {
		return ""
	}
	req.SetBasicAuth("opencode", pass)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return ""
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
		return ""
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
		return strings.Join(parts, "\n")
	}
	return ""
}

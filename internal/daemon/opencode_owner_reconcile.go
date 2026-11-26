package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/daemon/opencodeapi"
)

// reconcileSharedOpenCode checks sessions belonging to OpenCode's shared local
// service. Confirm the session belongs to that server before trusting absence
// from its active map: other OpenCode servers may have their own sessions.
func (d *Daemon) reconcileSharedOpenCode(ctx context.Context) {
	d.mu.Lock()
	var sessions []*localSession
	for _, ls := range d.sessions {
		if ls.info.Harness == "opencode" && ls.info.Worker == nil {
			sessions = append(sessions, ls)
		}
	}
	d.mu.Unlock()
	service, err := d.ownerOpenCodeService()
	if err != nil {
		return
	}
	base := strings.TrimRight(service.URL, "/")
	if len(sessions) == 0 {
		// The service outlives the daemon, but OpenCode can dispose its
		// project contexts (and their plugins) independently. With no plugin
		// running there is nothing left to reconnect to our new socket.
		// Loading a location boots the plugin, whose discovery re-registers
		// sessions across all directories. Keep retrying until it connects;
		// do not invent presence without a real plugin hello.
		check, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		path, err := d.openCodePath(check, base, service.Password, "", service.identity, opencodeapi.Location, "")
		if err == nil {
			api := openCodeWorkerAPI(check, base, service.Password)
			_, _, _ = api(opencodeapi.Location.Method(), path, nil)
		}
		return
	}
	for _, ls := range sessions {
		d.mu.Lock()
		native, rev, status := ls.info.NativeID, ls.statusRevision, ls.info.Status
		d.mu.Unlock()
		if d.workerByNative("opencode", native) != nil {
			continue
		}
		check, cancel := context.WithTimeout(ctx, time.Second)
		busy, known := d.openCodeSessionBusy(check, base, service.Password, "", service.identity, native)
		if !known {
			cancel()
			return
		}
		if busy {
			cancel()
			d.mu.Lock()
			changed := d.sessions[key("opencode", native)] == ls && ls.info.Status == status && ls.statusRevision == rev && status != "busy"
			if changed {
				ls.info.Status = "busy"
				ls.info.Waiting = nil
				d.recordActivityLocked(ls, time.Now())
			}
			d.mu.Unlock()
			if changed {
				d.persistActivity(ls)
				go d.pushSession(ls)
			}
			continue
		}
		if status != "busy" {
			cancel()
			continue
		}
		sessionPath, err := d.openCodePath(check, base, service.Password, "", service.identity, opencodeapi.GetSession, native)
		if err != nil {
			cancel()
			continue
		}
		req, err := http.NewRequestWithContext(check, opencodeapi.GetSession.Method(), base+sessionPath, nil)
		if err != nil {
			cancel()
			continue
		}
		if service.Password != "" {
			req.SetBasicAuth("opencode", service.Password)
		}
		resp, err := openCodeHTTPClient(10 * time.Second).Do(req)
		if err != nil {
			cancel()
			return
		}
		var body struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		ok := resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body) == nil && body.Data.ID == native
		resp.Body.Close()
		cancel()
		if !ok {
			continue
		}
		d.mu.Lock()
		changed := d.sessions[key("opencode", native)] == ls && ls.info.Status == "busy" && ls.statusRevision == rev
		if changed {
			ls.info.Status = "idle"
			d.recordActivityLocked(ls, time.Now())
		}
		d.mu.Unlock()
		if changed {
			d.persistActivity(ls)
			go d.pushSession(ls)
		}
	}
}

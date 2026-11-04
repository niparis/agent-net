package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

type openCodeService struct {
	URL      string `json:"url"`
	Password string `json:"password"`
}

// Only the owner's loopback service may receive its password. Never include
// service.json contents, HTTP response bodies or transport errors in notices.
func ownerOpenCodeService() (openCodeService, error) {
	var service openCodeService
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return service, err
		}
		state = filepath.Join(home, ".local", "state")
	}
	data, err := os.ReadFile(filepath.Join(state, "opencode", "service.json"))
	if err != nil {
		return service, err
	}
	if len(data) > 16384 || json.Unmarshal(data, &service) != nil {
		return service, errors.New("invalid OpenCode service record")
	}
	u, err := url.Parse(service.URL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return service, errors.New("invalid OpenCode service address")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return service, errors.New("OpenCode service address is not loopback")
	}
	service.URL = strings.TrimRight(service.URL, "/")
	return service, nil
}

// relayed says the owner approved the start from another machine; see
// launchOwnerClaude.
func (d *Daemon) launchOwnerOpenCode(ctx context.Context, r protocol.StartedSession, message, socket, pane string, notify, relayed bool) (protocol.StartedSession, error) {
	down := fmt.Errorf("the OpenCode service is not running on %s", d.cfg.Hostname)
	service, err := ownerOpenCodeService()
	if err != nil {
		return protocol.StartedSession{}, down
	}
	request := map[string]any{"title": r.Project}
	if r.Model != "" {
		provider, model, ok := strings.Cut(r.Model, "/")
		if !ok || provider == "" || model == "" {
			return protocol.StartedSession{}, errors.New("OpenCode model must look like provider/model")
		}
		base, level := protocol.SplitModelLevel(model)
		selected := map[string]string{"providerID": provider, "id": base}
		if level != "" {
			selected["variant"] = level
		}
		request["model"] = selected
	}
	directory := strings.ReplaceAll(url.QueryEscape(r.Path), "+", "%20")
	api := openCodeWorkerAPI(ctx, service.URL, service.Password)
	body, code, err := api("POST", "/api/session", request, "x-opencode-directory", directory)
	if err != nil {
		return protocol.StartedSession{}, down
	}
	if code != http.StatusOK && code != http.StatusCreated {
		return protocol.StartedSession{}, fmt.Errorf("OpenCode refused the session (HTTP %d)", code)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &created) != nil || created.Data.ID == "" {
		return protocol.StartedSession{}, errors.New("OpenCode answered without a session ID")
	}
	r.NativeID = created.Data.ID
	d.mu.Lock()
	if d.startedSessions == nil {
		d.startedSessions = make(map[string]protocol.StartedSession)
	}
	d.startedSessions[r.ID] = r
	d.mu.Unlock()
	d.wg.Add(1)
	go func(r protocol.StartedSession) {
		defer d.wg.Done()
		cctx, cancel := context.WithTimeout(d.ctoCtx, d.workerSettings("").StartTimeout)
		defer cancel()
		var startErr error
		if message != "" {
			// OpenCode 2.2 queues user turns through /prompt, as for the CTO.
			api := openCodeWorkerAPI(cctx, service.URL, service.Password)
			_, status, err := api("POST", "/api/session/"+url.PathEscape(r.NativeID)+"/prompt", map[string]string{"text": message, "delivery": "queue"}, "x-opencode-directory", directory)
			if err != nil {
				startErr = errors.New("could not send the first message to OpenCode")
			} else if status < 200 || status >= 300 {
				startErr = fmt.Errorf("OpenCode refused the first message (HTTP %d)", status)
			}
		}
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for startErr == nil {
			d.mu.Lock()
			if ls := d.sessions[key("opencode", r.NativeID)]; ls != nil {
				r.SessionID = ls.sessionID
			}
			d.mu.Unlock()
			if r.SessionID != "" {
				break
			}
			select {
			case <-cctx.Done():
				startErr = errors.New("the daemon did not register the OpenCode session")
			case <-ticker.C:
			}
		}
		if startErr != nil {
			r.Status, r.Error = "failed", startErr.Error()
		} else {
			bin, err := d.harnessBinary("opencode")
			if err == nil {
				plan := protocol.EnterPlan{Argv: []string{bin, "--session", r.NativeID}, Dir: r.Path, Host: r.Hostname,
					Note: "Press ctrl+] to come back here; the session keeps running."}
				opened := d.openForCaller(r.SessionID, r.Project, socket, pane, plan)
				if opened.OpenedHerdr || len(opened.Argv) == 0 || relayed {
					r.EnterPlan = &opened
				} else {
					r.EnterNote = "Enter opens it."
					if d.herdrBinary("") == "" {
						r.EnterNote += " " + herdrInstallNote
					}
				}
			}
			r.Status = "started"
		}
		d.mu.Lock()
		d.startedSessions[r.ID] = r
		d.mu.Unlock()
		if notify && r.Status == "started" {
			d.notifySessionStart(r)
		}
	}(r)
	return r, nil
}

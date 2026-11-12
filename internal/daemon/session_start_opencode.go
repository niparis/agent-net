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

	"github.com/niparis/agent-net/internal/daemon/opencodeapi"
	"github.com/niparis/agent-net/internal/protocol"
)

type openCodeService struct {
	URL      string `json:"url"`
	Password string `json:"password"`
	identity string
}

func ownerOpenCodeServicePath(homeDir string) (string, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home := homeDir
		if home == "" {
			var err error
			home, err = os.UserHomeDir()
			if err != nil {
				return "", err
			}
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "opencode", "service.json"), nil
}

// Only the owner's loopback service may receive its password. Never include
// service.json contents, HTTP response bodies or transport errors in notices.
func ownerOpenCodeService() (openCodeService, error) {
	return ownerOpenCodeServiceForHome("")
}

func (d *Daemon) ownerOpenCodeService() (openCodeService, error) {
	var home string
	if d != nil {
		home = d.cfg.HomeDir
	}
	return ownerOpenCodeServiceForHome(home)
}

func ownerOpenCodeServiceForHome(homeDir string) (openCodeService, error) {
	var service openCodeService
	path, err := ownerOpenCodeServicePath(homeDir)
	if err != nil {
		return service, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return service, err
	}
	if len(data) > 16384 || json.Unmarshal(data, &service) != nil {
		return service, errors.New("invalid OpenCode service record")
	}
	if stat, statErr := os.Stat(path); statErr == nil {
		service.identity = fmt.Sprintf("%d/%d", stat.ModTime().UnixNano(), stat.Size())
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
func (d *Daemon) launchOwnerOpenCode(ctx context.Context, r protocol.StartedSession, message, socket, pane string, notify, relayed bool) (result protocol.StartedSession, startErr error) {
	return d.startOwnerOpenCode(ctx, r, message, socket, pane, notify, relayed, false)
}

func (d *Daemon) resumeOwnerOpenCode(ctx context.Context, r protocol.StartedSession, socket, pane string) (protocol.StartedSession, error) {
	return d.startOwnerOpenCode(ctx, r, "", socket, pane, false, false, true)
}

func (d *Daemon) startOwnerOpenCode(ctx context.Context, r protocol.StartedSession, message, socket, pane string, notify, relayed, resume bool) (result protocol.StartedSession, startErr error) {
	defer func() {
		if startErr != nil {
			d.failStartAgent(r)
		}
	}()
	down := fmt.Errorf("the OpenCode service is not running on %s", d.cfg.Hostname)
	service, err := d.ownerOpenCodeService()
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
	if r.AgentID != "" {
		if err := d.reserveStartAgent(ctx, "opencode", "", r.AgentID, r.ID, r.Path, r.Project); err != nil {
			return protocol.StartedSession{}, err
		}
	}
	routes, err := d.discoverOpenCodeRoutes(ctx, service.URL, service.Password, "", service.identity)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return protocol.StartedSession{}, down
		}
		return protocol.StartedSession{}, err
	}
	api := openCodeWorkerAPI(ctx, service.URL, service.Password)
	op, id := opencodeapi.CreateSession, ""
	if resume {
		op, id = opencodeapi.GetSession, r.NativeID
		request = nil
	}
	endpoint, err := routes.Path(op, id)
	if err != nil {
		return protocol.StartedSession{}, err
	}
	body, code, err := api(op.Method(), endpoint, request, "x-opencode-directory", directory)
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
	if err := d.bindRepoStart(ctx, r); err != nil {
		return protocol.StartedSession{}, err
	}
	if r.AgentID != "" {
		if err := d.reserveStartAgent(ctx, "opencode", r.NativeID, r.AgentID, r.ID, r.Path, r.Project); err != nil {
			return protocol.StartedSession{}, err
		}
	}
	if resume {
		bin, err := d.harnessBinary("opencode")
		if err != nil {
			return protocol.StartedSession{}, err
		}
		plan := protocol.EnterPlan{Argv: []string{bin, "--server", service.URL, "--session", r.NativeID}, Dir: r.Path, Host: r.Hostname, Env: map[string]string{"OPENCODE_SERVER_PASSWORD": service.Password}, Note: "Press ctrl+] to come back here; the session keeps running."}
		opened := d.viewPlan("opencode:"+r.NativeID, r.Project, socket, pane, false, plan)
		r.Status = "started"
		r.EnterPlan = &opened
		d.mu.Lock()
		if d.startedSessions == nil {
			d.startedSessions = make(map[string]protocol.StartedSession)
		}
		d.startedSessions[r.ID] = r
		d.mu.Unlock()
		d.wg.Add(1)
		go func(r protocol.StartedSession) {
			defer d.wg.Done()
			wait, cancel := context.WithTimeout(d.ctoCtx, d.workerSettings("").StartTimeout)
			defer cancel()
			ticker := time.NewTicker(25 * time.Millisecond)
			defer ticker.Stop()
			for {
				d.mu.Lock()
				if ls := d.sessions[key("opencode", r.NativeID)]; ls != nil {
					r.SessionID = ls.sessionID
				}
				if r.SessionID != "" {
					d.startedSessions[r.ID] = r
				}
				d.mu.Unlock()
				if r.SessionID != "" {
					// An OpenCode session that could not be opened under the old
					// native ID is a new conversation: move the old work to it.
					d.adoptResumedSession(wait, r.Resumes, r.SessionID)
					return
				}
				select {
				case <-wait.Done():
					d.Logf("OpenCode resumed viewer opened; session registration is still pending")
					return
				case <-ticker.C:
				}
			}
		}(r)
		return r, nil
	}
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
		sendFirst := func() {
			if message == "" {
				return
			}
			// OpenCode 2.2 queues user turns through /prompt, as for the CTO.
			promptPath, err := routes.Path(opencodeapi.Prompt, r.NativeID)
			if err != nil {
				startErr = err
				return
			}
			api := openCodeWorkerAPI(cctx, service.URL, service.Password)
			_, status, err := api(opencodeapi.Prompt.Method(), promptPath, map[string]string{"text": message, "delivery": "queue"}, "x-opencode-directory", directory)
			if err != nil {
				startErr = errors.New("could not send the first message to OpenCode")
			} else if status < 200 || status >= 300 {
				startErr = fmt.Errorf("OpenCode refused the first message (HTTP %d)", status)
			}
		}
		if r.AgentID == "" {
			sendFirst()
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
		if startErr == nil {
			startErr = d.checkStartAgent(cctx, r)
		}
		if startErr == nil && r.AgentID != "" {
			sendFirst()
		}
		if startErr != nil {
			d.releaseRepoStart(r)
			r.Status, r.Error = "failed", startErr.Error()
			d.failStartAgent(r)
		} else {
			if bin, err := d.harnessBinary("opencode"); err == nil {
				d.showStart(&r, protocol.EnterPlan{Argv: []string{bin, "--server", service.URL, "--session", r.NativeID}, Dir: r.Path, Host: r.Hostname,
					Env:  map[string]string{"OPENCODE_SERVER_PASSWORD": service.Password},
					Note: "Press ctrl+] to come back here; the session keeps running."}, startView{socket: socket, pane: pane, relayed: relayed})
			}
			r.Status = "started"
		}
		// Report a failure before the failed state is visible; see launchOwnerClaude.
		if r.Status == "failed" {
			d.startFailed(r, notify)
		}
		d.mu.Lock()
		d.startedSessions[r.ID] = r
		d.mu.Unlock()
		if r.Status == "started" && notify {
			d.notifySessionStart(r)
		}
	}(r)
	return r, nil
}

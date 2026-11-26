package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/daemon/opencodeapi"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/providererr"
	"github.com/niparis/agent-net/internal/redact"
)

// The OpenCode worker (decision 0007).
//
// A worker is a private `opencode serve` process, not `opencode run`:
//
//   - `opencode run` without --standalone talks to the user's own background
//     service, so the worker would run in that service. We would not own its
//     process and could not kill it. With --standalone it ends after one turn,
//     and then a follow-up message has nowhere to go.
//   - `opencode serve` is a long-lived server of its own. It binds 127.0.0.1 on
//     a free port with a random password, is a child of this daemon in its own
//     process group, and dies with that group.
//
// The daemon creates one session in it through the HTTP API, with the model
// the caller picked. The agent-net plugin loads in this process (it is in the
// user's plugin folder) and registers that session like any other, so the
// worker can use agent_message_reply. The task then arrives as a normal message.
//
// Isolation from the user's service: the worker's state folder
// (XDG_STATE_HOME) is its own, so it never touches the service's service.json.
// Its data folder is private too. Credentials are exported and imported at
// each start; configuration remains shared. The plugin is told it runs in a
// worker (AGENT_NET_WORKER=1). The daemon also refuses any
// connection for a worker's session that does not come from the worker's own
// process group.
//
// A worker that handed back ends. A follow-up from its owner starts a new
// private server on the same data and state folders, tells the plugin the same
// session ID, and prompts that session again: the conversation is continued.

// opencodeLauncher starts workers with `opencode serve`.
type opencodeLauncher struct{}

// opencodeAPI calls the worker's own server.
type opencodeAPI func(method, path string, body any, headers ...string) ([]byte, int, error)

func (opencodeLauncher) start(ctx context.Context, d *Daemon, w *worker) error {
	api, err := d.serveOpenCode(ctx, w)
	if err != nil {
		return err
	}
	var provider, model string
	if w.model != "" {
		var ok bool
		if provider, model, ok = strings.Cut(w.model, "/"); !ok || provider == "" || model == "" {
			return fmt.Errorf("model %q must look like provider/model", w.model)
		}
	}
	// One session, with the model the caller chose.
	req := map[string]any{"title": "agent-net worker " + w.id + ": " + w.project}
	if model != "" {
		selection := protocol.ParseModelSelection("opencode", provider, model, "")
		built, buildErr := selection.Arguments(nil)
		if buildErr != nil {
			return buildErr
		}
		_, base, _ := strings.Cut(built[0], "/")
		effort := built[1]
		selected := map[string]string{"providerID": provider, "id": base}
		if effort != "" {
			selected["variant"] = effort
		}
		req["model"] = selected
	}
	d.wmu.Lock()
	base, pass, version, identity := w.apiBase, w.apiPass, w.apiVersion, w.apiIdentity
	d.wmu.Unlock()
	createPath, err := d.openCodePath(ctx, base, pass, version, identity, opencodeapi.CreateSession, "")
	if err != nil {
		return err
	}
	body, code, err := api(opencodeapi.CreateSession.Method(), createPath, req)
	if err != nil {
		return fmt.Errorf("cannot create the worker session: %w", err)
	}
	if code != http.StatusOK && code != http.StatusCreated {
		return fmt.Errorf("opencode refused the worker session (HTTP %d): %s", code, truncate(string(body), 200))
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &created) != nil || created.Data.ID == "" {
		return fmt.Errorf("opencode answered without a session ID: %s", truncate(string(body), 200))
	}
	if err := d.bindWorkerNative(w, created.Data.ID); err != nil {
		return err
	}
	if err := writeWorkerSessionFile(w, created.Data.ID); err != nil {
		return err
	}
	return d.loadOpenCodeProject(ctx, w, api)
}

// resume starts a new private server for the same session: the state folder
// and data folders are the ones the worker had, so it opens the same conversation.
func (opencodeLauncher) resume(ctx context.Context, d *Daemon, w *worker) error {
	if _, err := w.nativeSessionID("OpenCode session"); err != nil {
		return err
	}
	api, err := d.serveOpenCode(ctx, w)
	if err != nil {
		return err
	}
	if err := writeWorkerSessionFile(w, w.nativeID); err != nil {
		return err
	}
	return d.loadOpenCodeProject(ctx, w, api)
}

// adopt reconnects to the private server and loads its project so the plugin
// can resume its event watch and session delivery after the daemon restart.
func (opencodeLauncher) adopt(ctx context.Context, d *Daemon, w *worker) error {
	password, err := os.ReadFile(filepath.Join(w.dir, "opencode-password"))
	if err != nil || len(password) == 0 || w.ref == "" || w.nativeID == "" {
		return errors.New("the connection details of the OpenCode worker are gone")
	}
	base, pass := "http://"+w.ref, string(password)
	version, identity := d.openCodeVersion(), "adopt:"+w.id+":"+w.ref
	infoPath, err := d.openCodePath(ctx, base, pass, version, identity, opencodeapi.Info, "")
	if err != nil {
		return err
	}
	api := openCodeWorkerAPI(ctx, base, pass)
	info, code, err := api(opencodeapi.Info.Method(), infoPath, nil)
	if err != nil || code != http.StatusOK {
		return fmt.Errorf("cannot reconnect to the OpenCode worker (HTTP %d): %v", code, err)
	}
	if v := parseOpenCodeInfoVersion(info); v != "" {
		version = v
	}
	d.wmu.Lock()
	w.apiBase, w.apiPass, w.apiVersion, w.apiIdentity = base, pass, version, identity
	d.wmu.Unlock()
	if err := writeWorkerSessionFile(w, w.nativeID); err != nil {
		return err
	}
	return d.loadOpenCodeProject(ctx, w, api)
}

// loadOpenCodeProject asks for the project folder, which is how OpenCode
// loads its plugins. The plugin registers the session named in the worker's
// session file, and the daemon waits until it has.
func (d *Daemon) loadOpenCodeProject(ctx context.Context, w *worker, api opencodeAPI) error {
	d.wmu.Lock()
	base, pass, version, identity := w.apiBase, w.apiPass, w.apiVersion, w.apiIdentity
	d.wmu.Unlock()
	locPath, err := d.openCodePath(ctx, base, pass, version, identity, opencodeapi.Location, "")
	if err != nil {
		return err
	}
	if _, code, err := api(opencodeapi.Location.Method(), locPath, nil, "x-opencode-directory", strings.ReplaceAll(url.QueryEscape(w.path), "+", "%20")); err != nil || code != http.StatusOK {
		return fmt.Errorf("cannot load the project in the worker (HTTP %d): %v", code, err)
	}
	// The plugin registers the session. If it does not, the plugin is not
	// installed, and the worker could never answer. Wait on the channel itself:
	// the resume must return as soon as the session is ready, not a poll tick
	// later, or the gateway can deliver the message and its answer first.
	select {
	case <-w.ready:
		return nil
	case <-ctx.Done():
		return errors.New("the agent-net plugin did not register the worker session in time: is the OpenCode plugin installed? Run `agent-net setup` on that machine")
	}
}

// writeWorkerSessionFile tells the plugin which session is the worker's. It
// cannot find it by itself: the session exists before the plugin instance.
func writeWorkerSessionFile(w *worker, id string) error {
	tmp := filepath.Join(w.dir, "session.tmp")
	if err := os.WriteFile(tmp, []byte(id), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(w.dir, "session"))
}

// serveOpenCode starts the private server and waits until it answers.
func (d *Daemon) serveOpenCode(ctx context.Context, w *worker) (opencodeAPI, error) {
	bin, err := d.harnessBinary("opencode")
	if err != nil {
		return nil, err
	}
	password := protocol.NewToken()
	dir, err := d.workerDir(w)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "opencode-password"), []byte(password), 0o600); err != nil {
		return nil, err
	}
	data, state := filepath.Join(dir, "data"), filepath.Join(dir, "state")
	if err := copyOpenCodeAuth(ctx, bin, data, state); err != nil {
		return nil, err
	}
	// The worker's own OpenCode process takes the skill folder of its node in
	// its config (decision 0169, task 4). It adds to the owner's folders.
	vars := map[string]string{
		"XDG_DATA_HOME":                 data,
		"OPENCODE_SERVER_PASSWORD":      password,
		"AGENT_NET_WORKER":              w.id,
		"AGENT_NET_WORKER_SESSION_FILE": filepath.Join(dir, "session"),
		"AGENT_NET_SOCKET":              d.cfg.SocketPath,
		"XDG_STATE_HOME":                state,
	}
	if cfg := d.openCodeSkillsConfig(ctx, workerSkillNode(w), os.Getenv("OPENCODE_CONFIG_CONTENT")); cfg != "" {
		vars["OPENCODE_CONFIG_CONTENT"] = cfg
	}
	env := d.workerGitEnv(w, os.Environ(), vars)
	return d.serveOpenCodePrivate(ctx, openCodeServe{
		label: "opencode serve", password: password,
		spawn: func(attempt, port int) (string, string, string, error) {
			args := []string{"serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(port)}
			if err := d.spawnWorker(w, bin, args, env); err != nil {
				return "", "", "", err
			}
			base, version := portURL(port), d.openCodeVersion()
			identity := fmt.Sprintf("worker:%s:%d", w.id, attempt)
			d.wmu.Lock()
			w.apiBase, w.apiPass, w.apiVersion, w.apiIdentity = base, password, version, identity
			w.ref = "127.0.0.1:" + strconv.Itoa(port)
			d.saveWorkerLocked(w)
			d.wmu.Unlock()
			return base, version, identity, nil
		},
		setVersion: func(v string) {
			d.wmu.Lock()
			w.apiVersion = v
			d.wmu.Unlock()
		},
		gone: func() bool { return d.gone(w) },
		kill: func(string) { d.killGroup(w) },
	})
}

func openCodeWorkerAPI(ctx context.Context, base, password string) opencodeAPI {
	hc := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(method, path string, body any, headers ...string) ([]byte, int, error) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
		if err != nil {
			return nil, 0, err
		}
		req.SetBasicAuth("opencode", password)
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := hc.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return b, resp.StatusCode, nil
	}
}

// copyOpenCodeAuth uses OpenCode's supported credential export and import. In
// 2.2 credentials are in SQLite, not just auth.json. The JSON stays in memory,
// and the worker's database is private and retained across resumes.
func copyOpenCodeAuth(ctx context.Context, bin, data, state string) error {
	if err := os.MkdirAll(data, 0o700); err != nil {
		return err
	}
	export := exec.CommandContext(ctx, bin, "auth", "export", "--standalone")
	export.Env = workerEnv(os.Environ(), map[string]string{"XDG_STATE_HOME": state})
	credentials, err := export.Output()
	if err != nil {
		return fmt.Errorf("cannot export OpenCode credentials: %w", err)
	}
	imp := exec.CommandContext(ctx, bin, "auth", "import", "--standalone")
	imp.Env = workerEnv(os.Environ(), map[string]string{"XDG_DATA_HOME": data, "XDG_STATE_HOME": state})
	imp.Stdin = bytes.NewReader(credentials)
	if err := imp.Run(); err != nil {
		return fmt.Errorf("cannot import OpenCode credentials: %w", err)
	}
	// The directory is private, and the database itself must be private too.
	if err := os.Chmod(filepath.Join(data, "opencode", "opencode.db"), 0o600); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// stats reads the session's tokens, cost and model from the worker's own
// server. Any failure gives an empty result: the notice then omits them.
//
// The context is the tokens of the latest assistant message (input plus cache
// read and cache write), and the model's limit.context from GET /api/model.
func (opencodeLauncher) stats(ctx context.Context, d *Daemon, w *worker) workerStats {
	d.wmu.Lock()
	base, pass, version, identity, nativeID := w.apiBase, w.apiPass, w.apiVersion, w.apiIdentity, w.nativeID
	d.wmu.Unlock()
	if base == "" || nativeID == "" {
		return workerStats{}
	}
	sessionPath, err := d.openCodePath(ctx, base, pass, version, identity, opencodeapi.GetSession, nativeID)
	if err != nil {
		return workerStats{}
	}
	var r struct {
		Data struct {
			Cost   *float64 `json:"cost"`
			Tokens struct {
				Input, Output, Reasoning float64
				Cache                    struct{ Read, Write float64 }
			} `json:"tokens"`
			Model struct {
				ProviderID string `json:"providerID"`
				ID         string `json:"id"`
			} `json:"model"`
		} `json:"data"`
	}
	if !openCodeGet(ctx, base, pass, sessionPath, &r) {
		return workerStats{}
	}
	t := r.Data.Tokens
	st := workerStats{Tokens: tokenText(t.Input, t.Output, t.Reasoning, t.Cache.Read, t.Cache.Write),
		TotalTokens: int64(t.Input + t.Output + t.Reasoning),
		Usage:       protocol.RunTokens{In: t.Input, Out: t.Output, Reasoning: t.Reasoning, CacheRead: t.Cache.Read, CacheWrite: t.Cache.Write}}
	if r.Data.Cost != nil && st.Tokens != "" {
		st.Cost = fmt.Sprintf("%.4f (as OpenCode reports it)", *r.Data.Cost)
	}
	if r.Data.Model.ID != "" {
		st.Model = r.Data.Model.ProviderID + "/" + r.Data.Model.ID
	}
	context := d.openCodeSessionContext(ctx, base, pass, version, identity, nativeID)
	st.ContextTokens, st.ContextLimit = context.Tokens, context.Limit
	return st
}

// opencodeSessionContext reads the conversation's current context size and the
// model's window. The newest assistant message is the last step of the run, so
// a session with no assistant message yet has no context figure. GET /api/model
// lists the models with limit.context; the session names the model it runs, so
// only that entry matters.
type opencodeContextState struct {
	Tokens      int64
	Limit       int64
	Model       string
	AssistantID string
}

func (d *Daemon) openCodeSessionContext(ctx context.Context, base, pass, version, identity, nativeID string) opencodeContextState {
	out := opencodeContextState{}
	routes, err := d.discoverOpenCodeRoutes(ctx, base, pass, version, identity)
	if err != nil {
		return out
	}
	sessionPath, err := routes.Path(opencodeapi.GetSession, nativeID)
	if err != nil {
		return out
	}
	var sess struct {
		Data struct {
			Model struct {
				ProviderID string `json:"providerID"`
				ID         string `json:"id"`
			} `json:"model"`
		} `json:"data"`
	}
	if !openCodeGet(ctx, base, pass, sessionPath, &sess) {
		return out
	}
	if sess.Data.Model.ID != "" {
		out.Model = sess.Data.Model.ProviderID + "/" + sess.Data.Model.ID
	}
	var msgs struct {
		Data []struct {
			ID     string `json:"id"`
			Type   string `json:"type"`
			Tokens struct {
				Input, Output, Reasoning float64
				Cache                    struct{ Read, Write float64 }
			} `json:"tokens"`
		} `json:"data"`
	}
	if messagesPath, merr := routes.Path(opencodeapi.Messages, nativeID); merr == nil && openCodeGet(ctx, base, pass, messagesPath+"?type=assistant&order=desc&limit=1", &msgs) {
		for _, m := range msgs.Data {
			if m.Type != "assistant" {
				continue
			}
			out.AssistantID = m.ID
			out.Tokens = int64(m.Tokens.Input + m.Tokens.Cache.Read + m.Tokens.Cache.Write)
			break
		}
	}
	if out.Tokens > 0 && sess.Data.Model.ID != "" {
		var models struct {
			Data []struct {
				ProviderID string `json:"providerID"`
				ID         string `json:"id"`
				ModelID    string `json:"modelID"`
				Limit      struct {
					Context float64 `json:"context"`
				} `json:"limit"`
			} `json:"data"`
		}
		if modelsPath, merr := routes.Path(opencodeapi.Models, ""); merr == nil && openCodeGet(ctx, base, pass, modelsPath, &models) {
			for _, m := range models.Data {
				if m.ProviderID == sess.Data.Model.ProviderID && (m.ID == sess.Data.Model.ID || m.ModelID == sess.Data.Model.ID) {
					out.Limit = int64(m.Limit.Context)
					break
				}
			}
		}
	}
	return out
}

// failure tells whether the worker's last run failed, and why. OpenCode 2.2
// marks a failed run with outcome "failed" on the session, and the session
// messages then hold an "idle" entry with the error ({type, message}). The
// same error is in the session.execution.failed event. Any failure to read it
// gives "" (nothing known).
func (opencodeLauncher) failure(ctx context.Context, d *Daemon, w *worker) (string, bool) {
	d.wmu.Lock()
	base, pass, version, identity, nativeID := w.apiBase, w.apiPass, w.apiVersion, w.apiIdentity, w.nativeID
	d.wmu.Unlock()
	if base == "" || nativeID == "" {
		return "", false
	}
	routes, err := d.discoverOpenCodeRoutes(ctx, base, pass, version, identity)
	if err != nil {
		return "", false
	}
	sessionPath, err := routes.Path(opencodeapi.GetSession, nativeID)
	if err != nil {
		return "", false
	}
	var sess struct {
		Data struct {
			Outcome string `json:"outcome"`
		} `json:"data"`
	}
	if !openCodeGet(ctx, base, pass, sessionPath, &sess) || sess.Data.Outcome != "failed" {
		return "", false
	}
	var msgs struct {
		Data []struct {
			Type  string `json:"type"`
			Error *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"data"`
	}
	text, typ := "", ""
	if messagesPath, merr := routes.Path(opencodeapi.Messages, nativeID); merr == nil && openCodeGet(ctx, base, pass, messagesPath, &msgs) { // newest first
		for _, m := range msgs.Data {
			if m.Type == "idle" && m.Error != nil {
				text, typ = m.Error.Message, m.Error.Type
				if text == "" {
					text = m.Error.Type
				}
				break
			}
		}
	}
	return cleanFailure(text), providererr.UpstreamFailure(typ + " " + text)
}

// cleanFailure makes a provider error fit a notice: one line, secrets masked,
// cut to 300 characters. An empty error still says that the run failed.
func cleanFailure(s string) string {
	s = strings.Join(strings.Fields(redact.Secrets(s)), " ")
	if s == "" {
		return "the harness reported a failed run with no error text"
	}
	return redact.Cut(s, 300)
}

// reconcileOpenCode refreshes session status against their own server. The
// response may arrive after another status event, so it must not override it.
func (d *Daemon) reconcileOpenCode(ctx context.Context) {
	d.reconcileSharedOpenCode(ctx)
	d.wmu.Lock()
	workers := make([]*worker, 0, len(d.workers))
	for _, w := range d.workers {
		if w.harness == "opencode" && w.apiBase != "" && w.nativeID != "" && !w.stopping {
			workers = append(workers, w)
		}
	}
	d.wmu.Unlock()
	for _, w := range workers {
		d.wmu.Lock()
		base, pass, version, identity, native := w.apiBase, w.apiPass, w.apiVersion, w.apiIdentity, w.nativeID
		d.wmu.Unlock()
		d.mu.Lock()
		ls := d.sessions[key("opencode", native)]
		if ls == nil {
			d.mu.Unlock()
			continue
		}
		rev, status := ls.statusRevision, ls.info.Status
		d.mu.Unlock()
		check, cancel := context.WithTimeout(ctx, time.Second)
		busy, known := d.openCodeSessionBusy(check, base, pass, version, identity, native)
		cancel()
		if !known {
			continue
		}
		if busy {
			d.wmu.Lock()
			d.mu.Lock()
			changed := d.workers[w.id] == w && !w.stopping && d.sessions[key("opencode", native)] == ls && ls.statusRevision == rev && ls.info.Status == status && status != "busy"
			if d.workers[w.id] == w && !w.stopping && !w.turnHandbackIdle && ls.statusRevision == rev {
				w.turnAwaitingStart = false
			}
			if changed {
				ls.info.Status = "busy"
				ls.info.Waiting = nil
				d.recordActivityLocked(ls, time.Now())
			}
			d.mu.Unlock()
			d.wmu.Unlock()
			if changed {
				d.persistActivity(ls)
				go d.pushSession(ls)
			}
			continue
		}
		if status != "busy" {
			continue
		}

		d.wmu.Lock()
		d.mu.Lock()
		if d.workers[w.id] != w || w.stopping || w.doneStatus != "" || (w.turnAwaitingStart && !w.turnHandbackIdle) || d.sessions[key("opencode", native)] != ls || ls.statusRevision != rev || ls.info.Status != "busy" {
			d.mu.Unlock()
			d.wmu.Unlock()
			continue
		}
		var next *protocol.Message
		if w.turnHandbackIdle {
			w.turnHandbackIdle = false
		} else if len(w.turnQueue) > 0 {
			m := w.turnQueue[0]
			w.turnQueue = w.turnQueue[1:]
			w.turnAwaitingStart = true
			w.turnMessageID = m.ID
			next = &m
		} else {
			w.turnActive = false
			ls.info.Status = "idle"
		}
		changed := ls.info.Status == "idle"
		if changed {
			d.recordActivityLocked(ls, time.Now())
		}
		d.mu.Unlock()
		d.wmu.Unlock()
		if next != nil {
			d.goBackground(func() { d.deliverQueuedWorkerTurn(*next) })
		}
		if changed {
			d.persistActivity(ls)
			go d.pushSession(ls)
		}
	}
}

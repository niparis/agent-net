package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
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
// worker can use agent_reply. The task then arrives as a normal message.
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
		req["model"] = map[string]string{"providerID": provider, "id": model}
	}
	body, code, err := api("POST", "/api/session", req)
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
	if w.nativeID == "" {
		return errors.New("the OpenCode session of the worker is not known")
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

// loadOpenCodeProject asks for the project folder, which is how OpenCode
// loads its plugins. The plugin registers the session named in the worker's
// session file, and the daemon waits until it has.
func (d *Daemon) loadOpenCodeProject(ctx context.Context, w *worker, api opencodeAPI) error {
	if _, code, err := api("GET", "/api/location", nil, "x-opencode-directory", strings.ReplaceAll(url.QueryEscape(w.path), "+", "%20")); err != nil || code != http.StatusOK {
		return fmt.Errorf("cannot load the project in the worker (HTTP %d): %v", code, err)
	}
	// The plugin registers the session. If it does not, the plugin is not
	// installed, and the worker could never answer.
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
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	password := protocol.NewToken()
	dir := filepath.Join(d.cfg.StateDir, "workers", w.id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	data, state := filepath.Join(dir, "data"), filepath.Join(dir, "state")
	if err := copyOpenCodeAuth(ctx, bin, data, state); err != nil {
		return nil, err
	}
	env := workerEnv(os.Environ(), map[string]string{
		"XDG_DATA_HOME":                 data,
		"OPENCODE_SERVER_PASSWORD":      password,
		"AGENT_NET_WORKER":              w.id,
		"AGENT_NET_WORKER_SESSION_FILE": filepath.Join(dir, "session"),
		"AGENT_NET_SOCKET":              d.cfg.SocketPath,
		"XDG_STATE_HOME":                state,
	})
	// Never a flag that turns permission checks off (decision 0002, W6).
	args := []string{"serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(port)}
	if err := d.spawnWorker(w, bin, args, env); err != nil {
		return nil, err
	}
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	d.wmu.Lock()
	w.apiBase, w.apiPass = base, password
	d.wmu.Unlock()
	hc := &http.Client{Timeout: 10 * time.Second}
	api := func(method, path string, body any, headers ...string) ([]byte, int, error) {
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
	for {
		if _, code, err := api("GET", "/api/info", nil); err == nil && code == http.StatusOK {
			return api, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("opencode serve did not start in time")
		case <-time.After(100 * time.Millisecond):
		}
		if d.gone(w) {
			return nil, errors.New("opencode serve exited at start")
		}
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
func (opencodeLauncher) stats(ctx context.Context, _ *Daemon, w *worker) workerStats {
	if w.apiBase == "" || w.nativeID == "" {
		return workerStats{}
	}
	get := func(path string, v any) bool {
		req, err := http.NewRequestWithContext(ctx, "GET", w.apiBase+path, nil)
		if err != nil {
			return false
		}
		req.SetBasicAuth("opencode", w.apiPass)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v) == nil
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
	if !get("/api/session/"+w.nativeID, &r) {
		return workerStats{}
	}
	t := r.Data.Tokens
	st := workerStats{Tokens: tokenText(t.Input, t.Output, t.Reasoning, t.Cache.Read, t.Cache.Write),
		TotalTokens: int64(t.Input + t.Output + t.Reasoning)}
	if r.Data.Cost != nil && st.Tokens != "" {
		st.Cost = fmt.Sprintf("%.4f (as OpenCode reports it)", *r.Data.Cost)
	}
	if r.Data.Model.ID != "" {
		st.Model = r.Data.Model.ProviderID + "/" + r.Data.Model.ID
	}
	// The newest assistant message is the last step of the run. A session with
	// no assistant message yet has no context figure.
	var msgs struct {
		Data []struct {
			Type   string `json:"type"`
			Tokens struct {
				Input, Output, Reasoning float64
				Cache                    struct{ Read, Write float64 }
			} `json:"tokens"`
		} `json:"data"`
	}
	if get("/api/session/"+w.nativeID+"/message", &msgs) {
		for _, m := range msgs.Data {
			if m.Type != "assistant" {
				continue
			}
			st.ContextTokens = int64(m.Tokens.Input + m.Tokens.Cache.Read + m.Tokens.Cache.Write)
			break
		}
	}
	// GET /api/model lists the models with limit.context. The session names
	// the model it runs, so only that entry matters.
	if st.ContextTokens > 0 && r.Data.Model.ID != "" {
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
		if get("/api/model", &models) {
			for _, m := range models.Data {
				if m.ProviderID == r.Data.Model.ProviderID && (m.ID == r.Data.Model.ID || m.ModelID == r.Data.Model.ID) {
					st.ContextLimit = int64(m.Limit.Context)
					break
				}
			}
		}
	}
	return st
}

// failure tells whether the worker's last run failed, and why. OpenCode 2.2
// marks a failed run with outcome "failed" on the session, and the session
// messages then hold an "idle" entry with the error ({type, message}). The
// same error is in the session.execution.failed event. Any failure to read it
// gives "" (nothing known).
func (opencodeLauncher) failure(ctx context.Context, _ *Daemon, w *worker) (string, bool) {
	if w.apiBase == "" || w.nativeID == "" {
		return "", false
	}
	get := func(path string, v any) bool {
		req, err := http.NewRequestWithContext(ctx, "GET", w.apiBase+path, nil)
		if err != nil {
			return false
		}
		req.SetBasicAuth("opencode", w.apiPass)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v) == nil
	}
	var sess struct {
		Data struct {
			Outcome string `json:"outcome"`
		} `json:"data"`
	}
	if !get("/api/session/"+w.nativeID, &sess) || sess.Data.Outcome != "failed" {
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
	if get("/api/session/"+w.nativeID+"/message", &msgs) { // newest first
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
	return cleanFailure(text), openCodeProviderFailure(typ, text)
}

var providerStatus = regexp.MustCompile(`\b(401|402|403|429)\b`)

// openCodeProviderFailure is deliberately conservative: unknown failures must
// not penalize a model. An upstream wrapper alone is not proof of a provider error.
func openCodeProviderFailure(typ, message string) bool {
	s := strings.ToLower(typ + " " + message)
	for _, local := range []string{"sqlerror", "sqlite", "database", "tool.error", "tool error"} {
		if strings.Contains(s, local) {
			return false
		}
	}
	if providerStatus.MatchString(s) {
		return true
	}
	for _, marker := range []string{"insufficient", "billing", "funds", "payment required", "unauthorized", "forbidden", "invalid api key", "invalid_api_key", "authentication", "quota", "rate limit", "rate_limit", "too many requests", "no route", "no-route", "model unavailable", "model not available", "model not found", "model_not_found", "provider.auth", "provider.quota", "provider.rate"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
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

// harnessBinary returns the path of an installed harness, from the inventory
// or from the test overrides.
func (d *Daemon) harnessBinary(name string) (string, error) {
	if p := d.cfg.HarnessPaths[name]; p != "" {
		return p, nil
	}
	list, _ := d.inventory()
	for _, h := range list {
		if h.Name == name && h.Path != "" {
			return h.Path, nil
		}
	}
	return "", fmt.Errorf("%s is not installed on this machine", name)
}

// workerEnv returns env with the given variables set and any inherited value
// of them removed.
func workerEnv(env []string, set map[string]string) []string {
	out := make([]string, 0, len(env)+len(set))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if _, replaced := set[k]; !replaced {
			out = append(out, kv)
		}
	}
	for k, v := range set {
		out = append(out, k+"="+v)
	}
	return out
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

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
	"path/filepath"
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
// The data folder (XDG_DATA_HOME) is shared, because OpenCode 2.2 keeps its
// credentials in the database in that folder. Two OpenCode processes on one
// data folder is a supported mode (every command has --standalone). The plugin
// is told it runs in a worker (AGENT_NET_WORKER=1), so it does not register the
// user's other sessions from the shared database. The daemon also refuses any
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
// is the one the worker had, so the plugin opens the same conversation.
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
	env := workerEnv(os.Environ(), map[string]string{
		"OPENCODE_SERVER_PASSWORD":      password,
		"AGENT_NET_WORKER":              w.id,
		"AGENT_NET_WORKER_SESSION_FILE": filepath.Join(dir, "session"),
		"AGENT_NET_SOCKET":              d.cfg.SocketPath,
		"XDG_STATE_HOME":                filepath.Join(dir, "state"),
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

// stats reads the session's tokens, cost and model from the worker's own
// server. Any failure gives an empty result: the notice then omits them.
func (opencodeLauncher) stats(ctx context.Context, _ *Daemon, w *worker) workerStats {
	if w.apiBase == "" || w.nativeID == "" {
		return workerStats{}
	}
	req, err := http.NewRequestWithContext(ctx, "GET", w.apiBase+"/api/session/"+w.nativeID, nil)
	if err != nil {
		return workerStats{}
	}
	req.SetBasicAuth("opencode", w.apiPass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return workerStats{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
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
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r) != nil {
		return workerStats{}
	}
	t := r.Data.Tokens
	st := workerStats{Tokens: tokenText(t.Input, t.Output, t.Reasoning, t.Cache.Read, t.Cache.Write)}
	if r.Data.Cost != nil && st.Tokens != "" {
		st.Cost = fmt.Sprintf("%.4f (as OpenCode reports it)", *r.Data.Cost)
	}
	if r.Data.Model.ID != "" {
		st.Model = r.Data.Model.ProviderID + "/" + r.Data.Model.ID
	}
	return st
}

// failure tells whether the worker's last run failed, and why. OpenCode 2.2
// marks a failed run with outcome "failed" on the session, and the session
// messages then hold an "idle" entry with the error ({type, message}). The
// same error is in the session.execution.failed event. Any failure to read it
// gives "" (nothing known).
func (opencodeLauncher) failure(ctx context.Context, _ *Daemon, w *worker) string {
	if w.apiBase == "" || w.nativeID == "" {
		return ""
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
		return ""
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
	text := ""
	if get("/api/session/"+w.nativeID+"/message", &msgs) { // newest first
		for _, m := range msgs.Data {
			if m.Type == "idle" && m.Error != nil {
				text = m.Error.Message
				if text == "" {
					text = m.Error.Type
				}
				break
			}
		}
	}
	return cleanFailure(text)
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

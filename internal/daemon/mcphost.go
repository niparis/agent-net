package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// The MCP host runs the servers registered with this node as host. A stdio
// server is a child process the host starts and restarts; a URL server is a
// client connection it keeps up. Header values and env values are read here,
// on this machine, at connect time, and never leave it: errors, results and
// log lines pass through a redactor that removes them.

const (
	redacted       = "[redacted]"
	probeTimeout   = 30 * time.Second
	pingEvery      = 60 * time.Second
	stableAfter    = 30 * time.Second
	defaultBackMin = time.Second
	defaultBackMax = time.Minute
)

// baseEnv is what a stdio server inherits besides the variables named in its
// registry entry. The daemon's other variables stay out of the child.
var baseEnv = []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "TMPDIR", "TERM",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"}

type mcpHost struct {
	d          *Daemon
	backMin    time.Duration
	backMax    time.Duration
	mu         sync.Mutex
	runners    map[string]*mcpRunner
	ctx        context.Context
	shutdownWG sync.WaitGroup
}

func newMCPHost(d *Daemon) *mcpHost {
	h := &mcpHost{d: d, backMin: d.cfg.MCPBackoffMin, backMax: d.cfg.MCPBackoffMax, runners: map[string]*mcpRunner{}}
	if h.backMin == 0 {
		h.backMin = defaultBackMin
	}
	if h.backMax == 0 {
		h.backMax = defaultBackMax
	}
	return h
}

func (h *mcpHost) start(ctx context.Context) { h.ctx = ctx }

func (h *mcpHost) stop() {
	h.mu.Lock()
	rs := make([]*mcpRunner, 0, len(h.runners))
	for _, r := range h.runners {
		rs = append(rs, r)
	}
	h.runners = map[string]*mcpRunner{}
	h.mu.Unlock()
	for _, r := range rs {
		r.stop()
	}
	h.shutdownWG.Wait()
}

func specKey(s protocol.MCPServer) string {
	b, _ := json.Marshal(protocol.MCPServer{Transport: s.Transport, Command: s.Command, Args: s.Args, Env: s.Env,
		URL: s.URL, Headers: s.Headers})
	return string(b)
}

// reconcile makes the running set equal to the servers the gateway listed.
// Entries for other nodes are ignored: a daemon only runs its own.
func (h *mcpHost) reconcile(servers []protocol.MCPServer) {
	want := map[string]protocol.MCPServer{}
	for _, s := range servers {
		if s.NodeID == h.d.cfg.NodeID && s.Enabled && s.Validate() == nil {
			want[s.Name] = s
		}
	}
	h.mu.Lock()
	var stopped []*mcpRunner
	for name, r := range h.runners {
		if w, ok := want[name]; !ok || specKey(w) != r.key {
			stopped = append(stopped, r)
			delete(h.runners, name)
		}
	}
	for name, s := range want {
		if r, ok := h.runners[name]; ok {
			r.setTimeout(s.TimeoutSec)
			go r.report() // the gateway may have lost our last report
			continue
		}
		h.startRunner(name, s)
	}
	h.mu.Unlock()
	for _, r := range stopped {
		r.stop()
	}
}

func (h *mcpHost) startRunner(name string, s protocol.MCPServer) {
	parent := h.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	r := &mcpRunner{h: h, spec: s, key: specKey(s), cancel: cancel, done: make(chan struct{}), tried: make(chan struct{}),
		health: protocol.HealthUnknown}
	h.runners[name] = r
	h.shutdownWG.Add(1)
	go func() {
		defer h.shutdownWG.Done()
		defer close(r.done)
		r.run(ctx)
	}()
}

func (h *mcpHost) runner(name string) *mcpRunner {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runners[name]
}

// invoke runs one tool call on a hosted server.
func (h *mcpHost) invoke(ctx context.Context, p protocol.MCPInvoke) (json.RawMessage, error) {
	r := h.runner(p.Server)
	if r == nil {
		return nil, fmt.Errorf("server %q is not running on this node", p.Server)
	}
	return r.call(ctx, p)
}

// ---- runner ----

type mcpRunner struct {
	h      *mcpHost
	spec   protocol.MCPServer
	key    string
	cancel context.CancelFunc
	done   chan struct{}
	// tried is closed after the first connection attempt, success or not.
	tried     chan struct{}
	triedOnce sync.Once

	mu      sync.Mutex
	sess    *mcp.ClientSession
	red     redactor
	health  string
	detail  string
	tools   []protocol.MCPTool
	timeout int
}

func (r *mcpRunner) stop() {
	r.cancel()
	r.mu.Lock()
	s := r.sess
	r.mu.Unlock()
	if s != nil {
		s.Close()
	}
	<-r.done
}

func (r *mcpRunner) setTimeout(sec int) {
	r.mu.Lock()
	r.timeout = sec
	r.mu.Unlock()
}

func (r *mcpRunner) logf(format string, args ...any) {
	r.mu.Lock()
	red := r.red
	r.mu.Unlock()
	r.h.d.Logf("mcp %s: %s", r.spec.Name, red.apply(fmt.Sprintf(format, args...)))
}

func (r *mcpRunner) report() {
	r.mu.Lock()
	rep := protocol.MCPReport{Name: r.spec.Name, Health: r.health, Detail: r.detail, Tools: r.tools}
	r.mu.Unlock()
	if rep.Health == protocol.HealthUnknown {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := r.h.d.call(ctx, "mcp.report", rep); err != nil {
		r.logf("report to gateway: %v", err)
	}
}

func (r *mcpRunner) setDown(detail string) {
	r.mu.Lock()
	r.sess = nil
	r.health, r.detail = protocol.HealthError, detail
	r.mu.Unlock()
	r.report()
}

func (r *mcpRunner) run(ctx context.Context) {
	delay := r.h.backMin
	for ctx.Err() == nil {
		cctx, cancel := context.WithTimeout(ctx, probeTimeout)
		c, err := connectMCP(cctx, r.h.d, r.spec, r.stderrWriter())
		cancel()
		var tools []protocol.MCPTool
		if err == nil {
			lctx, lcancel := context.WithTimeout(ctx, probeTimeout)
			tools, err = listTools(lctx, c.sess)
			lcancel()
			if err != nil {
				c.sess.Close()
				err = c.red.wrap(err)
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			r.setDown(err.Error())
			r.triedOnce.Do(func() { close(r.tried) })
			r.logf("down: %v (retry in %s)", err, delay)
		} else {
			r.mu.Lock()
			r.sess, r.red, r.tools = c.sess, c.red, tools
			r.health, r.detail = protocol.HealthOK, ""
			r.mu.Unlock()
			r.report()
			r.triedOnce.Do(func() { close(r.tried) })
			r.logf("up with %d tools", len(tools))
			started := time.Now()
			r.watch(ctx, c)
			if ctx.Err() != nil {
				return
			}
			r.setDown("the server stopped; restarting")
			r.logf("stopped after %s; restarting", time.Since(started).Round(time.Second))
			if time.Since(started) > stableAfter {
				delay = r.h.backMin
			}
		}
		wait := delay
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		delay = min(delay*2, r.h.backMax)
	}
}

// watch blocks until the session ends or a ping fails.
func (r *mcpRunner) watch(ctx context.Context, c *mcpConn) {
	ended := make(chan struct{})
	go func() { c.sess.Wait(); close(ended) }()
	t := time.NewTicker(pingEvery)
	defer t.Stop()
	for {
		select {
		case <-ended:
			return
		case <-ctx.Done():
			c.sess.Close()
			return
		case <-c.listChanged:
			lctx, cancel := context.WithTimeout(ctx, probeTimeout)
			tools, err := listTools(lctx, c.sess)
			cancel()
			if err == nil {
				r.mu.Lock()
				r.tools = tools
				r.mu.Unlock()
				r.report()
			}
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.sess.Ping(pctx, nil)
			cancel()
			if err != nil {
				r.logf("ping failed: %v", c.red.wrap(err))
				c.sess.Close()
				return
			}
		}
	}
}

// call forwards one tool call, with the server's timeout, and returns the
// capped, redacted result.
func (r *mcpRunner) call(ctx context.Context, p protocol.MCPInvoke) (json.RawMessage, error) {
	// A server added a moment ago may still be starting.
	select {
	case <-r.tried:
	case <-time.After(probeTimeout):
	case <-ctx.Done():
	}
	r.mu.Lock()
	sess, red, detail := r.sess, r.red, r.detail
	sec := r.timeout
	r.mu.Unlock()
	if sess == nil {
		if detail == "" {
			detail = "not connected yet"
		}
		return nil, fmt.Errorf("server %q is not running on this node: %s", p.Server, detail)
	}
	d := r.spec
	d.TimeoutSec = sec
	if p.TimeoutSec > 0 {
		d.TimeoutSec = p.TimeoutSec
	}
	ctx, cancel := context.WithTimeout(ctx, d.Timeout())
	defer cancel()
	var args any
	if len(p.Arguments) > 0 && string(p.Arguments) != "null" {
		args = json.RawMessage(p.Arguments)
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: p.Tool, Arguments: args})
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("tool %q timed out after %s", p.Tool, d.Timeout())
		}
		return nil, red.wrap(err)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(red.applyBytes(capResult(raw, protocol.MCPMaxResultBytes))), nil
}

func (r *mcpRunner) stderrWriter() io.Writer { return &lineLog{r: r} }

// lineLog sends a child's stderr to the daemon log, a line at a time.
type lineLog struct {
	r   *mcpRunner
	mu  sync.Mutex
	buf []byte
}

func (w *lineLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		if line := strings.TrimRight(string(w.buf[:i]), "\r"); line != "" {
			w.r.logf("stderr: %s", line)
		}
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > 64*1024 {
		w.buf = nil
	}
	return len(p), nil
}

// ---- connecting ----

type mcpConn struct {
	sess        *mcp.ClientSession
	red         redactor
	listChanged chan struct{}
}

// connectMCP opens a client session to the server described by spec. stderr
// receives a stdio child's error output.
func connectMCP(ctx context.Context, d *Daemon, spec protocol.MCPServer, stderr io.Writer) (*mcpConn, error) {
	c := &mcpConn{listChanged: make(chan struct{}, 1)}
	client := mcp.NewClient(&mcp.Implementation{Name: "agent-net", Version: "0.3.0"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			select {
			case c.listChanged <- struct{}{}:
			default:
			}
		},
	})
	var tr mcp.Transport
	switch spec.Transport {
	case protocol.MCPStdio:
		cmd := exec.Command(spec.Command, spec.Args...)
		env := make([]string, 0, len(baseEnv)+len(spec.Env))
		for _, n := range baseEnv {
			if v, ok := os.LookupEnv(n); ok {
				env = append(env, n+"="+v)
			}
		}
		for _, n := range spec.Env {
			v, ok := os.LookupEnv(n)
			if !ok {
				return nil, fmt.Errorf("environment variable %s is not set for the agent-net daemon on this host", n)
			}
			c.red.add(v)
			env = append(env, n+"="+v)
		}
		cmd.Env = env
		cmd.Stderr = stderr
		tr = &mcp.CommandTransport{Command: cmd}
	case protocol.MCPHTTP, protocol.MCPSSE:
		hdr := http.Header{}
		for _, h := range spec.Headers {
			v, err := headerValue(h)
			if err != nil {
				return nil, err
			}
			c.red.add(v)
			hdr.Set(h.Name, v)
		}
		hc := &http.Client{Transport: &headerTransport{hdr: hdr, base: http.DefaultTransport}}
		if spec.Transport == protocol.MCPHTTP {
			tr = &mcp.StreamableClientTransport{Endpoint: spec.URL, HTTPClient: hc}
		} else {
			tr = &mcp.SSEClientTransport{Endpoint: spec.URL, HTTPClient: hc}
		}
	default:
		return nil, fmt.Errorf("unknown transport %q", spec.Transport)
	}
	sess, err := client.Connect(ctx, tr, nil)
	if err != nil {
		return nil, c.red.wrap(friendlyConnectError(err))
	}
	c.sess = sess
	return c, nil
}

// friendlyConnectError turns an HTTP auth failure into a plain sentence.
func friendlyConnectError(err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "401") || strings.Contains(s, "Unauthorized"):
		return errors.New("the server rejected the credentials (HTTP 401); check the header's env var or file on the host")
	case strings.Contains(s, "403") || strings.Contains(s, "Forbidden"):
		return errors.New("the server refused access (HTTP 403); check the header's env var or file on the host")
	}
	return err
}

type headerTransport struct {
	hdr  http.Header
	base http.RoundTripper
}

func (t *headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, vs := range t.hdr {
		r.Header[k] = append([]string(nil), vs...)
	}
	return t.base.RoundTrip(r)
}

// headerValue reads a header value from its env var or file, on this host.
// Authorization values without a scheme get "Bearer ".
func headerValue(h protocol.MCPHeader) (string, error) {
	var v string
	if h.Env != "" {
		var ok bool
		v, ok = os.LookupEnv(h.Env)
		if !ok || strings.TrimSpace(v) == "" {
			return "", fmt.Errorf("environment variable %s for header %s is not set for the agent-net daemon on this host", h.Env, h.Name)
		}
	} else {
		path := expandHome(h.File)
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("cannot read the file %s for header %s on this host", h.File, h.Name)
		}
		v = string(b)
		if strings.TrimSpace(v) == "" {
			return "", fmt.Errorf("the file %s for header %s is empty", h.File, h.Name)
		}
	}
	v = strings.TrimSpace(v)
	if strings.EqualFold(h.Name, "Authorization") && !strings.ContainsAny(v, " \t") {
		v = "Bearer " + v
	}
	return v, nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func listTools(ctx context.Context, sess *mcp.ClientSession) ([]protocol.MCPTool, error) {
	var out []protocol.MCPTool
	var cursor string
	for page := 0; page < 20; page++ {
		res, err := sess.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, t := range res.Tools {
			schema, _ := json.Marshal(t.InputSchema)
			out = append(out, protocol.MCPTool{Name: t.Name, Description: t.Description, InputSchema: schema})
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// probe connects, lists the tools and closes. Used before a server is saved.
func (h *mcpHost) probe(ctx context.Context, spec protocol.MCPServer) ([]protocol.MCPTool, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	var stderr bytes.Buffer
	c, err := connectMCP(ctx, h.d, spec, &limitedBuffer{b: &stderr})
	if err != nil {
		if s := strings.TrimSpace(stderr.String()); s != "" && spec.Transport == protocol.MCPStdio {
			err = fmt.Errorf("%w (stderr: %s)", err, lastLine(s))
		}
		return nil, c.redOrZero().wrap(err)
	}
	defer c.sess.Close()
	tools, err := listTools(ctx, c.sess)
	if err != nil {
		return nil, c.red.wrap(err)
	}
	return tools, nil
}

func (c *mcpConn) redOrZero() redactor {
	if c == nil {
		return redactor{}
	}
	return c.red
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	l := lines[len(lines)-1]
	if len(l) > 300 {
		l = l[:300]
	}
	return l
}

type limitedBuffer struct{ b *bytes.Buffer }

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.b.Len() < 8192 {
		l.b.Write(p)
	}
	return len(p), nil
}

// ---- redaction and result cap ----

// redactor removes secret values from text. It holds only values read on this
// host in this process.
type redactor struct{ secrets []string }

func (r *redactor) add(v string) {
	v = strings.TrimSpace(v)
	if len(v) < 3 {
		return
	}
	r.secrets = append(r.secrets, v)
	// The bare token of "Bearer <token>" and the JSON-escaped form too.
	if i := strings.LastIndexAny(v, " \t"); i >= 0 && len(v)-i-1 >= 3 {
		r.secrets = append(r.secrets, v[i+1:])
	}
	if b, err := json.Marshal(v); err == nil && len(b) > 2 && string(b[1:len(b)-1]) != v {
		r.secrets = append(r.secrets, string(b[1:len(b)-1]))
	}
}

func (r redactor) apply(s string) string {
	for _, v := range r.secrets {
		s = strings.ReplaceAll(s, v, redacted)
	}
	return s
}

func (r redactor) applyBytes(b []byte) []byte {
	for _, v := range r.secrets {
		b = bytes.ReplaceAll(b, []byte(v), []byte(redacted))
	}
	return b
}

// wrap returns err with secrets removed from its text.
func (r redactor) wrap(err error) error {
	if err == nil || len(r.secrets) == 0 {
		return err
	}
	return errors.New(r.apply(err.Error()))
}

// capResult cuts a CallToolResult JSON document down to max bytes. Text is cut
// at a rune boundary; a binary item that does not fit is replaced by a note.
func capResult(raw []byte, max int) []byte {
	if len(raw) <= max {
		return raw
	}
	var res map[string]json.RawMessage
	if json.Unmarshal(raw, &res) != nil {
		return raw[:max]
	}
	delete(res, "structuredContent")
	var items []map[string]any
	json.Unmarshal(res["content"], &items)
	budget := max - 1024
	var out []map[string]any
	for _, it := range items {
		if budget <= 0 {
			break
		}
		if t, _ := it["type"].(string); t == "text" {
			s, _ := it["text"].(string)
			if len(s) > budget {
				s = strings.ToValidUTF8(s[:budget], "")
			}
			budget -= len(s)
			it["text"] = s
		} else {
			b, _ := json.Marshal(it)
			if len(b) > budget {
				it = map[string]any{"type": "text", "text": "[a non-text item was omitted because the result is too large]"}
			} else {
				budget -= len(b)
			}
		}
		out = append(out, it)
	}
	out = append(out, map[string]any{"type": "text",
		"text": fmt.Sprintf("[agent-net: the result was cut because it is larger than %d bytes]", max)})
	res["content"], _ = json.Marshal(out)
	b, _ := json.Marshal(res)
	return b
}

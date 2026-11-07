// Package opencodeapi maps OpenCode capabilities to HTTP routes.
package opencodeapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
)

type Operation string

const (
	Info          Operation = "info"
	Location      Operation = "location"
	Agents        Operation = "list agents"
	Models        Operation = "list models"
	CreateSession Operation = "create session"
	GetSession    Operation = "get session"
	DeleteSession Operation = "delete session"
	ActiveSession Operation = "active session"
	Messages      Operation = "list messages"
	Prompt        Operation = "prompt"
	SetAgent      Operation = "set agent"
	SetModel      Operation = "set model"
	Compact       Operation = "compact"
	Interrupt     Operation = "interrupt"
)

type route struct{ method, path string }

var operations = map[Operation]route{
	Info: {"get", "/api/info"}, Location: {"get", "/api/location"}, Agents: {"get", "/api/agent"}, Models: {"get", "/api/model"},
	CreateSession: {"post", "/api/session"}, GetSession: {"get", "/api/session/{sessionID}"}, DeleteSession: {"delete", "/api/session/{sessionID}"}, ActiveSession: {"get", "/api/session/active"},
	Messages: {"get", "/api/session/{sessionID}/message"}, Prompt: {"post", "/api/session/{sessionID}/prompt"}, SetAgent: {"post", "/api/session/{sessionID}/agent"}, SetModel: {"post", "/api/session/{sessionID}/model"}, Compact: {"post", "/api/session/{sessionID}/compact"}, Interrupt: {"post", "/api/session/{sessionID}/interrupt"},
}

// Method returns the HTTP method for an operation.
func (op Operation) Method() string {
	return strings.ToUpper(operations[op].method)
}

// Routes describes the operations advertised by a single OpenCode service.
type Routes struct {
	version string
	paths   map[Operation]string
}

// Detect reads an OpenAPI document. Empty input means the document was absent.
func Detect(spec io.Reader, version string) (Routes, error) {
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	err := json.NewDecoder(spec).Decode(&doc)
	if err != nil && err != io.EOF {
		return Routes{}, fmt.Errorf("OpenCode OpenAPI document: %w", err)
	}
	r := Routes{version: version, paths: make(map[Operation]string)}
	for op, rt := range operations {
		if err == io.EOF && op != Interrupt {
			r.paths[op] = rt.path
			continue
		}
		if _, ok := doc.Paths[rt.path][rt.method]; ok {
			r.paths[op] = rt.path
		}
	}
	// 2026-11-07: v2.2.6 renamed abort to interrupt; keep the fallback 30 days.
	legacy := "/api/session/{sessionID}/abort"
	if r.paths[Interrupt] == "" {
		if _, ok := doc.Paths[legacy]["post"]; ok || err == io.EOF {
			r.paths[Interrupt] = legacy
		}
	}
	return r, nil
}

// Path returns a route for an operation, escaping its session ID.
func (r Routes) Path(op Operation, id string) (string, error) {
	path := r.paths[op]
	if path == "" {
		return "", r.HTTPError(op, http.StatusNotFound)
	}
	return strings.ReplaceAll(path, "{sessionID}", url.PathEscape(id)), nil
}

// HTTPError names the failed operation and the service version.
func (r Routes) HTTPError(op Operation, status int) error {
	if status == http.StatusNotFound {
		return fmt.Errorf("OpenCode %s has no %s route", r.version, op)
	}
	return fmt.Errorf("OpenCode %s: %s failed (HTTP %d)", r.version, op, status)
}

// InterruptFallback returns the legacy route only after a current interrupt route.
func (r Routes) InterruptFallback(id string) (string, bool) {
	// 2026-11-07: v2.2.6 renamed abort to interrupt; keep the fallback 30 days.
	if r.paths[Interrupt] != operations[Interrupt].path {
		return "", false
	}
	return "/api/session/" + url.PathEscape(id) + "/abort", true
}

// Discover reads the service's capabilities with its configured credentials.
func Discover(ctx context.Context, client *http.Client, base, password, version string) (Routes, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/openapi.json", nil)
	if err != nil {
		return Routes{}, err
	}
	req.SetBasicAuth("opencode", password)
	resp, err := client.Do(req)
	if err != nil {
		return Routes{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Detect(strings.NewReader(""), version)
	}
	if resp.StatusCode != http.StatusOK {
		return Routes{}, fmt.Errorf("OpenCode OpenAPI HTTP %d", resp.StatusCode)
	}
	return Detect(resp.Body, version)
}

// Cache retains successful discovery for each service lifetime and version.
type Cache struct {
	mu      sync.Mutex
	entries map[string]Routes
}

func (c *Cache) Discover(ctx context.Context, client *http.Client, base, password, version, identity string) (Routes, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := base + "\x00" + identity + "\x00" + version
	if r, ok := c.entries[key]; ok {
		return r, nil
	}
	r, err := Discover(ctx, client, base, password, version)
	if err != nil {
		return Routes{}, err
	}
	if c.entries == nil {
		c.entries = make(map[string]Routes)
	}
	c.entries[key] = r
	return r, nil
}

// Missing returns operations not advertised by the service in stable order.
func (r Routes) Missing() []Operation {
	var missing []Operation
	for op := range operations {
		if r.paths[op] == "" {
			missing = append(missing, op)
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
	return missing
}

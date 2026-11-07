// Package opencodeapi maps OpenCode capabilities to HTTP routes.
package opencodeapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
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
	// Added 2026-11-07 for OpenCode releases before 2.2.6.
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
		return "", fmt.Errorf("OpenCode %s has no %s route", r.version, op)
	}
	return strings.ReplaceAll(path, "{sessionID}", url.PathEscape(id)), nil
}

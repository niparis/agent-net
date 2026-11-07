// Package opencodeapi maps OpenCode capabilities to HTTP routes.
package opencodeapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
)

// Routes describes the operations advertised by a single OpenCode service.
type Routes struct {
	version   string
	interrupt string
}

// Detect reads an OpenAPI document. An empty document represents a service
// without /openapi.json and uses its legacy routes.
func Detect(spec io.Reader, version string) (Routes, error) {
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	err := json.NewDecoder(spec).Decode(&doc)
	if err == io.EOF {
		// Added 2026-11-07 for OpenCode releases before 2.2.6.
		return Routes{version: version, interrupt: "/api/session/{sessionID}/abort"}, nil
	}
	if err != nil {
		return Routes{}, fmt.Errorf("OpenCode OpenAPI document: %w", err)
	}
	r := Routes{version: version}
	if _, ok := doc.Paths["/api/session/{sessionID}/interrupt"]["post"]; ok {
		r.interrupt = "/api/session/{sessionID}/interrupt"
	} else if _, ok := doc.Paths["/api/session/{sessionID}/abort"]["post"]; ok {
		// Added 2026-11-07 for OpenCode releases before 2.2.6.
		r.interrupt = "/api/session/{sessionID}/abort"
	}
	return r, nil
}

// Interrupt returns the route for interrupting a session.
func (r Routes) Interrupt(id string) (string, error) {
	if r.interrupt == "" {
		return "", fmt.Errorf("OpenCode %s has no interrupt route", r.version)
	}
	return r.interrupt[:len("/api/session/")] + url.PathEscape(id) + r.interrupt[len("/api/session/{sessionID}"):], nil
}

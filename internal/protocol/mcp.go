package protocol

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// MCP registry limits.
const (
	MCPDefaultTimeout = 60 * time.Second
	MCPMaxTimeout     = 10 * time.Minute
	// MCPMaxResultBytes caps what one tool call may return. The host cuts the
	// result and says so.
	MCPMaxResultBytes = 512 * 1024
	// MCPMaxArgsBytes caps the arguments of one tool call.
	MCPMaxArgsBytes = 256 * 1024
	MCPMaxNameLen   = 24
)

// Transports of a registered MCP server.
const (
	MCPStdio = "stdio"
	MCPHTTP  = "http"
	MCPSSE   = "sse"
)

// Health values.
const (
	HealthUnknown = "unknown"
	HealthOK      = "ok"
	HealthError   = "error"
)

// MCPHeader names an HTTP header and where the host reads its value from.
// Exactly one of Env (a variable name) and File (a path) is set. The value
// itself is never in the registry.
type MCPHeader struct {
	Name string `json:"name"`
	Env  string `json:"env,omitempty"`
	File string `json:"file,omitempty"`
}

// MCPTool is one tool of a registered server, as the server describes it.
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// MCPServer is a registry entry.
type MCPServer struct {
	Name      string      `json:"name"`
	NodeID    string      `json:"node_id"`
	Hostname  string      `json:"hostname,omitempty"`
	Transport string      `json:"transport"`
	Command   string      `json:"command,omitempty"`
	Args      []string    `json:"args,omitempty"`
	Env       []string    `json:"env,omitempty"` // variable names only
	URL       string      `json:"url,omitempty"`
	Headers   []MCPHeader `json:"headers,omitempty"`
	// TimeoutSec is the per-call timeout; 0 means the default.
	TimeoutSec int  `json:"timeout_sec,omitempty"`
	Enabled    bool `json:"enabled"`

	AddedByNode    string `json:"added_by_node,omitempty"`
	AddedBySession string `json:"added_by_session,omitempty"`
	AddedByHarness string `json:"added_by_harness,omitempty"`
	AddedAt        int64  `json:"added_at,omitempty"`

	Health       string    `json:"health"`
	HealthDetail string    `json:"health_detail,omitempty"`
	HealthAt     int64     `json:"health_at,omitempty"`
	HostOnline   bool      `json:"host_online"`
	Tools        []MCPTool `json:"tools,omitempty"`
}

// Timeout returns the per-call timeout.
func (s MCPServer) Timeout() time.Duration {
	if s.TimeoutSec <= 0 {
		return MCPDefaultTimeout
	}
	return time.Duration(s.TimeoutSec) * time.Second
}

var (
	mcpNameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	headerRE  = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
)

// ValidMCPName reports whether name is a legal server name.
func ValidMCPName(name string) error {
	if name == "" || len(name) > MCPMaxNameLen || !mcpNameRE.MatchString(name) {
		return fmt.Errorf("server name %q is not valid: use 1-%d lowercase letters, numbers and single hyphens", name, MCPMaxNameLen)
	}
	return nil
}

// ValidEnvName reports whether s is a plain environment variable name. A value
// such as NAME=secret is refused without echoing it.
func ValidEnvName(s string) error {
	if !envNameRE.MatchString(s) {
		if strings.Contains(s, "=") {
			return fmt.Errorf("an environment variable must be given by NAME only, never NAME=value")
		}
		return fmt.Errorf("an environment variable name must match [A-Za-z_][A-Za-z0-9_]*")
	}
	return nil
}

// Validate checks the fields a caller supplies. It never prints a value that
// could be a secret.
func (s *MCPServer) Validate() error {
	if err := ValidMCPName(s.Name); err != nil {
		return err
	}
	switch s.Transport {
	case MCPStdio:
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("a stdio server needs a command")
		}
		if s.URL != "" || len(s.Headers) > 0 {
			return fmt.Errorf("a stdio server takes no url or headers")
		}
		for _, e := range s.Env {
			if err := ValidEnvName(e); err != nil {
				return err
			}
		}
	case MCPHTTP, MCPSSE:
		if s.URL == "" {
			return fmt.Errorf("a %s server needs a url", s.Transport)
		}
		if !strings.HasPrefix(s.URL, "http://") && !strings.HasPrefix(s.URL, "https://") {
			return fmt.Errorf("the url must start with http:// or https://")
		}
		if strings.Contains(s.URL, "@") {
			return fmt.Errorf("the url must not contain credentials; use a header with an env var name or a file path")
		}
		if s.Command != "" || len(s.Args) > 0 || len(s.Env) > 0 {
			return fmt.Errorf("a %s server takes no command, args or env", s.Transport)
		}
		for _, h := range s.Headers {
			if !headerRE.MatchString(h.Name) {
				return fmt.Errorf("%q is not a header name", h.Name)
			}
			if (h.Env == "") == (h.File == "") {
				return fmt.Errorf("header %s needs exactly one of an env var name or a file path", h.Name)
			}
			if h.Env != "" {
				if err := ValidEnvName(h.Env); err != nil {
					return err
				}
			}
		}
	default:
		return fmt.Errorf("transport must be stdio, http or sse")
	}
	if s.TimeoutSec < 0 || time.Duration(s.TimeoutSec)*time.Second > MCPMaxTimeout {
		return fmt.Errorf("timeout must be between 1 s and %s", MCPMaxTimeout)
	}
	return nil
}

// Request payloads for the MCP registry frames.
type (
	// MCPAdd registers a server. On is a host name or node ID; only the CLI
	// may set it, the daemon clears it for agents.
	MCPAdd struct {
		Server         MCPServer `json:"server"`
		On             string    `json:"on,omitempty"`
		AddedBySession string    `json:"added_by_session,omitempty"`
		AddedByHarness string    `json:"added_by_harness,omitempty"`
	}
	MCPName struct {
		Name string `json:"name"`
	}
	MCPSetEnabled struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	// MCPSync tells a node which enabled servers it hosts.
	MCPSync struct {
		Servers []MCPServer `json:"servers"`
	}
	// MCPProbe asks a host to connect to a server, list its tools and close.
	MCPProbe struct {
		Server MCPServer `json:"server"`
	}
	// MCPReport is a host's view of one server it runs.
	MCPReport struct {
		Name   string    `json:"name"`
		Health string    `json:"health"`
		Detail string    `json:"detail,omitempty"`
		Tools  []MCPTool `json:"tools,omitempty"`
	}
	MCPCall struct {
		Server    string          `json:"server"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}
	// MCPInvoke is a call forwarded to the hosting node.
	MCPInvoke struct {
		Server     string          `json:"server"`
		Tool       string          `json:"tool"`
		Arguments  json.RawMessage `json:"arguments,omitempty"`
		TimeoutSec int             `json:"timeout_sec"`
	}
)

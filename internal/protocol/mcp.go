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
	// MCPMaxAllowedTools caps the allow-list of a server.
	MCPMaxAllowedTools = 256
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
	// AllowedTools, when not empty, is the only set of the server's tools that
	// the gateway lists and lets through. An import sets it from the native
	// entry's allow-list. Tools it does not name are neither listed nor called.
	AllowedTools []string `json:"allowed_tools,omitempty"`

	AddedByNode    string `json:"added_by_node,omitempty"`
	AddedBySession string `json:"added_by_session,omitempty"`
	AddedByHarness string `json:"added_by_harness,omitempty"`
	AddedAt        int64  `json:"added_at,omitempty"`

	Health       string    `json:"health"`
	HealthDetail string    `json:"health_detail,omitempty"`
	HealthAt     int64     `json:"health_at,omitempty"`
	HostOnline   bool      `json:"host_online"`
	Tools        []MCPTool `json:"tools,omitempty"`

	// Scope says which sessions can see and call the server: "global" (every
	// session, also when no assignment exists), "projects" (sessions of the
	// listed Projects) or "uninstalled" (no session). The gateway fills these
	// from the owner's assignment; they are never stored in the spec.
	Scope    string   `json:"scope,omitempty"`
	Projects []string `json:"projects,omitempty"`
}

// ToolAllowed reports whether the gateway exposes the tool: always when the
// server has no allow-list.
func (s MCPServer) ToolAllowed(tool string) bool {
	if len(s.AllowedTools) == 0 {
		return true
	}
	for _, t := range s.AllowedTools {
		if t == tool {
			return true
		}
	}
	return false
}

// AllowedOnly returns the tools the allow-list names (all of them when there
// is no list).
func (s MCPServer) AllowedOnly(tools []MCPTool) []MCPTool {
	if len(s.AllowedTools) == 0 {
		return tools
	}
	out := []MCPTool{}
	for _, t := range tools {
		if s.ToolAllowed(t.Name) {
			out = append(out, t)
		}
	}
	return out
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

// MCPServerGrantPrefix starts the grant that lets one connected bot use one
// shared MCP server: "mcp server <name>".
const MCPServerGrantPrefix = "mcp server "

// MCPServerGrant is the grant text for a server name.
func MCPServerGrant(name string) string { return MCPServerGrantPrefix + name }

// ParseMCPServerGrant returns the server name of an "mcp server <name>" grant.
func ParseMCPServerGrant(grant string) (string, bool) {
	name, ok := strings.CutPrefix(grant, MCPServerGrantPrefix)
	if !ok || ValidMCPName(name) != nil {
		return "", false
	}
	return name, true
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
	if len(s.AllowedTools) > MCPMaxAllowedTools {
		return fmt.Errorf("an allow-list holds at most %d tools", MCPMaxAllowedTools)
	}
	for _, t := range s.AllowedTools {
		if t == "" || len(t) > 128 {
			return fmt.Errorf("an allow-list entry must be 1-128 bytes")
		}
	}
	if s.TimeoutSec < 0 || time.Duration(s.TimeoutSec)*time.Second > MCPMaxTimeout {
		return fmt.Errorf("timeout must be between 1 s and %s", MCPMaxTimeout)
	}
	return nil
}

// Request payloads for the MCP registry frames.
type (
	// MCPAdd registers a server. On is a host name or node ID; only the CLI
	// may set it. AddedBySession attributes session-originated requests, which
	// the gateway refuses outside proposal validation.
	MCPAdd struct {
		Server         MCPServer `json:"server"`
		On             string    `json:"on,omitempty"`
		AddedBySession string    `json:"added_by_session,omitempty"`
		AddedByHarness string    `json:"added_by_harness,omitempty"`
	}
	// MCPName names a server. AddedBySession attributes a session-originated
	// removal request for the gateway's owner check.
	MCPName struct {
		Name           string `json:"name"`
		AddedBySession string `json:"added_by_session,omitempty"`
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
	// MCPCall is a tool call from a session's daemon. SessionID names the
	// calling session; the gateway checks that it lives on the calling node
	// and takes its project from its own record. There is no project field:
	// a project in the request is never read. Owner is the owner session ID
	// for the command line (operator authority), checked like tools.plan.
	MCPCall struct {
		Server    string          `json:"server"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
		SessionID string          `json:"session_id,omitempty"`
		Owner     string          `json:"owner,omitempty"`
	}
	// MCPCatalog asks for the enabled servers one session may see.
	MCPCatalog struct {
		SessionID string `json:"session_id,omitempty"`
	}
	// MCPInvoke is a call forwarded to the hosting node.
	MCPInvoke struct {
		Server     string          `json:"server"`
		Tool       string          `json:"tool"`
		Arguments  json.RawMessage `json:"arguments,omitempty"`
		TimeoutSec int             `json:"timeout_sec"`
	}
)

// Resources and prompts of registered servers. A session lists and reads them
// through its daemon, the gateway checks access as for a tool call, and the
// hosting daemon asks the server. Lists go to every server the caller may see;
// a read or a prompt goes to one.
const (
	MCPOpResources = "resources/list"
	MCPOpTemplates = "resources/templates/list"
	MCPOpRead      = "resources/read"
	MCPOpPrompts   = "prompts/list"
	MCPOpPrompt    = "prompts/get"

	// MCPMaxListItems caps the resources, templates or prompts of one server.
	// The host stops reading pages at the cap.
	MCPMaxListItems = 1000
	// MCPListTimeout is how long the gateway waits for one host to list.
	MCPListTimeout = 15 * time.Second
	// MCPListCacheTTL is how long a host reuses a server's list. A
	// list_changed notification from the server ends it at once.
	MCPListCacheTTL = 10 * time.Second
	// MCPMaxURILen caps a resource URI and a prompt name in a request.
	MCPMaxURILen = 8 * 1024
	// MCPMaxListAnswerBytes caps the items of all servers in one list answer,
	// well under the 4 MiB frame limit of the links and the local socket.
	MCPMaxListAnswerBytes = 2 << 20
)

// ValidMCPOp reports whether op is one of the MCPOp values.
func ValidMCPOp(op string) bool {
	switch op {
	case MCPOpResources, MCPOpTemplates, MCPOpRead, MCPOpPrompts, MCPOpPrompt:
		return true
	}
	return false
}

// MCPListOp reports whether op is a list.
func MCPListOp(op string) bool {
	return op == MCPOpResources || op == MCPOpTemplates || op == MCPOpPrompts
}

type (
	// MCPFeature asks for the resources or prompts of registered servers.
	// Server names one server; for a list it may be empty, which means every
	// server the caller may see. URI is the server's own resource URI (read),
	// Name and Arguments the server's own prompt (get). SessionID and Owner
	// are set by the daemon, as for MCPCall.
	MCPFeature struct {
		Op        string            `json:"op"`
		Server    string            `json:"server,omitempty"`
		URI       string            `json:"uri,omitempty"`
		Name      string            `json:"name,omitempty"`
		Arguments map[string]string `json:"arguments,omitempty"`
		SessionID string            `json:"session_id,omitempty"`
		Owner     string            `json:"owner,omitempty"`
	}
	// MCPFeatureInvoke is an MCPFeature for one server, forwarded to its host.
	MCPFeatureInvoke struct {
		Op         string            `json:"op"`
		Server     string            `json:"server"`
		URI        string            `json:"uri,omitempty"`
		Name       string            `json:"name,omitempty"`
		Arguments  map[string]string `json:"arguments,omitempty"`
		TimeoutSec int               `json:"timeout_sec"`
	}
	// MCPServerItems is one server's part of a list answer: the items as the
	// server gave them (a JSON array of resources, templates or prompts), or
	// why there are none.
	MCPServerItems struct {
		Server string          `json:"server"`
		Host   string          `json:"host,omitempty"`
		Items  json.RawMessage `json:"items,omitempty"`
		Error  string          `json:"error,omitempty"`
	}
	// MCPItemsChanged is a host saying a server changed its resources or
	// prompts (a list_changed notification).
	MCPItemsChanged struct {
		Name string `json:"name"`
	}
)

// Check validates the parts a caller supplies.
func (f MCPFeature) Check() error {
	if !ValidMCPOp(f.Op) {
		return fmt.Errorf("unknown op %q", f.Op)
	}
	if f.Server != "" {
		if err := ValidMCPName(f.Server); err != nil {
			return err
		}
	}
	switch f.Op {
	case MCPOpRead:
		if f.Server == "" || f.URI == "" {
			return fmt.Errorf("a read needs a server and a uri")
		}
	case MCPOpPrompt:
		if f.Server == "" || f.Name == "" {
			return fmt.Errorf("a prompt needs a server and a name")
		}
	}
	if len(f.URI) > MCPMaxURILen || len(f.Name) > MCPMaxURILen {
		return fmt.Errorf("the uri or name is longer than %d bytes", MCPMaxURILen)
	}
	n := 0
	for k, v := range f.Arguments {
		n += len(k) + len(v)
	}
	if n > MCPMaxArgsBytes {
		return fmt.Errorf("arguments are larger than %d bytes", MCPMaxArgsBytes)
	}
	return nil
}

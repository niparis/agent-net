package protocol

import (
	"regexp"
	"strings"
)

// Classes of a native MCP entry, and the two end states of an import.
const (
	MCPImportable   = "importable"
	MCPUnsupported  = "unsupported"
	MCPImportOK     = "imported"
	MCPImportFailed = "failed"
)

// MCPImportMaxCandidates bounds one page of candidates, so the answer stays small.
const MCPImportMaxCandidates = 30

// MCPImportInfo is the scanner's verdict on one native MCP entry. It holds
// plain facts and names only: no argument, header or environment value.
type MCPImportInfo struct {
	Class     string `json:"class"`
	Reason    string `json:"reason,omitempty"`
	Transport string `json:"transport,omitempty"`
	// EnvNames are the variable names the server reads (never values).
	EnvNames []string `json:"env_names,omitempty"`
	// ExpectedTools is the tool allow-list the native entry states, when it
	// states one. It is the only thing a native entry says about tools.
	ExpectedTools []string `json:"expected_tools,omitempty"`
}

var nonNameRE = regexp.MustCompile(`[^a-z0-9]+`)

// MCPServerName turns a native server name into a registry name. A name that
// is already valid is returned as it is.
func MCPServerName(native string) string {
	if ValidMCPName(native) == nil {
		return native
	}
	n := strings.Trim(nonNameRE.ReplaceAllString(strings.ToLower(native), "-"), "-")
	if len(n) > MCPMaxNameLen {
		n = strings.Trim(n[:MCPMaxNameLen], "-")
	}
	return n
}

// MCPImportCandidate is one native MCP entry on one machine. Key names it in a
// plan request.
type MCPImportCandidate struct {
	Key        string   `json:"key"`
	NodeID     string   `json:"node_id"`
	Machine    string   `json:"machine"`
	Harness    string   `json:"harness"`
	Scope      string   `json:"scope"`
	Project    string   `json:"project,omitempty"`
	Location   string   `json:"location"`
	Name       string   `json:"name"`
	ServerName string   `json:"server_name,omitempty"`
	Class      string   `json:"class"`
	Reason     string   `json:"reason,omitempty"`
	Transport  string   `json:"transport,omitempty"`
	EnvNames   []string `json:"env_names,omitempty"`
	State      string   `json:"state"`
	// Imported is set when a federated server was made from this entry.
	Imported           bool   `json:"imported,omitempty"`
	NativeStillPresent bool   `json:"native_still_present,omitempty"`
	Conflict           string `json:"conflict,omitempty"`
	// Gaps say what the native entry has that the federation does not provide.
	Gaps []string `json:"federation_gaps,omitempty"`
}

type MCPImportCandidatesRequest struct {
	Machine string `json:"machine,omitempty"`
	Harness string `json:"harness,omitempty"`
	Project string `json:"project,omitempty"`
	Class   string `json:"class,omitempty"`
	Offset  int    `json:"offset,omitempty"`
}
type MCPImportCandidatesPage struct {
	Candidates []MCPImportCandidate `json:"candidates"`
	Total      int                  `json:"total"`
	Next       int                  `json:"next,omitempty"`
}

// MCPImportRequest is the owner's request to import (plan) or to apply a plan.
// Exactly one of Candidate and PlanID is set.
type MCPImportRequest struct {
	Candidate string `json:"candidate,omitempty"`
	PlanID    string `json:"plan_id,omitempty"`
}

// MCPImportPlan is what an import would do, shown before the owner applies it.
type MCPImportPlan struct {
	ID        string             `json:"id"`
	Candidate MCPImportCandidate `json:"candidate"`
	// Server is the registry entry to create: names and paths, no values.
	Server        MCPServer      `json:"server"`
	Assignment    ToolAssignment `json:"assignment"`
	ExpectedTools []string       `json:"expected_tools,omitempty"`
	NativeHash    string         `json:"native_hash"`
	Conflicts     []string       `json:"conflicts"`
	State         string         `json:"state"`
	Actor         string         `json:"actor"`
	At            int64          `json:"at"`
}

// MCPImportRecord remembers where a federated server came from.
type MCPImportRecord struct {
	Server   string `json:"server"`
	NodeID   string `json:"node_id"`
	Machine  string `json:"machine,omitempty"`
	Harness  string `json:"harness"`
	Scope    string `json:"scope"`
	Project  string `json:"project,omitempty"`
	Location string `json:"location"`
	Name     string `json:"name"`
	PlanID   string `json:"plan_id"`
	Actor    string `json:"actor"`
	At       int64  `json:"at"`
	// NativeStillPresent is true when the import finished. The native entry is
	// left as it was; the gateway recomputes this on each listing.
	NativeStillPresent bool `json:"native_still_present"`
	// Gaps say what the native entry had that the federation does not provide.
	Gaps []string `json:"federation_gaps,omitempty"`
	// Tools are the tools the server reported at import. AllowedTools is the
	// native entry's allow-list that the federated server keeps, or empty.
	Tools        []string `json:"tools,omitempty"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
}

// MCPImportResult answers an apply. State is imported or failed. A failed
// import changed nothing: the server is not registered.
type MCPImportResult struct {
	PlanID             string         `json:"plan_id"`
	Server             string         `json:"server"`
	State              string         `json:"state"`
	Reason             string         `json:"reason,omitempty"`
	Assignment         ToolAssignment `json:"assignment"`
	Tools              []string       `json:"tools,omitempty"`
	Compared           string         `json:"compared,omitempty"`
	NativeStillPresent bool           `json:"native_still_present"`
}

// MCPImportSpecRequest asks the host to read one native entry again and answer
// with the server it would run. The host takes the project root from its own
// scan, never from this request.
type MCPImportSpecRequest struct {
	Harness  string `json:"harness"`
	Scope    string `json:"scope"`
	Project  string `json:"project,omitempty"`
	Location string `json:"location"`
	Name     string `json:"name"`
}
type MCPImportSpec struct {
	Server MCPServer     `json:"server"`
	Info   MCPImportInfo `json:"info"`
	Hash   string        `json:"hash"`
}

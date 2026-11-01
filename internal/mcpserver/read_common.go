package mcpserver

// Shared helpers of the read tools added for the tool redesign (task 2b):
// catalog definitions, opaque cursors, stable paging, caller identity, and text
// and time formatting. Each tool lives in its own file so that other tasks can
// add theirs beside it.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

const (
	roDefaultLimit = 25
	roMaxLimit     = 100
	// roMaxMessages is a safety net for the full scan that filtering needs.
	// Session conversations are far smaller.
	roMaxMessages = 20000
)

// roSpec returns the catalog entry of one tool.
func roSpec(name string) toolcatalog.Tool {
	for _, tool := range toolcatalog.Tools {
		if tool.Name == name {
			return tool
		}
	}
	panic("mcpserver: " + name + " is not in the tool catalog")
}

// roAdd registers one read tool with the catalog's name, description, input
// schema, output schema and hints.
func roAdd[In, Out any](s *mcp.Server, name string, h mcp.ToolHandlerFor[In, Out]) {
	spec := roSpec(name)
	annotations := &mcp.ToolAnnotations{
		ReadOnlyHint:    spec.Hints.ReadOnly,
		IdempotentHint:  spec.Hints.Idempotent,
		DestructiveHint: roBool(spec.Hints.Destructive),
	}
	if spec.Hints.OpenWorld {
		annotations.OpenWorldHint = roBool(true)
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:         spec.Name,
		Description:  spec.Description,
		InputSchema:  spec.InputSchema,
		OutputSchema: spec.OutputSchema,
		Annotations:  annotations,
	}, h)
}

func roBool(v bool) *bool { return &v }

// addReadTools registers the read tools of tool redesign task 2b. One call site
// in New keeps the shared file edit to a single line.
func addReadTools(s *mcp.Server, call Caller) {
	addMessageReadTools(s, call)
	addWorkerReadTools(s, call)
	addGraphReadTool(s, call)
	addMCPReadTools(s, call)
}

// roCursorCaller is the caller a public cursor is bound to.
func roCursorCaller(ctx context.Context) string {
	if id := roNativeID(ctx); id != "" {
		return id
	}
	return "anonymous"
}

// roLimit checks a page size. The schema has the same bounds; this check is for
// a client that ignores them.
func roLimit(n int) (int, error) {
	if n == 0 {
		return roDefaultLimit, nil
	}
	if n < 1 || n > roMaxLimit {
		return 0, fmt.Errorf("limit must be between 1 and %d. Example: {\"limit\":%d}", roMaxLimit, roDefaultLimit)
	}
	return n, nil
}

func roDetail(detail string) (bool, error) {
	switch detail {
	case "", "concise":
		return false, nil
	case "full":
		return true, nil
	default:
		return false, fmt.Errorf("detail must be concise or full, not %q. Example: {\"detail\":\"full\"}", detail)
	}
}

// roCursor is an opaque page cursor. It is bound to one tool, one caller and
// one filter set, so it cannot be replayed against another query.
type roCursor struct {
	V       int    `json:"v"`
	Tool    string `json:"tool"`
	Caller  string `json:"caller"`
	Filters string `json:"filters"`
	After   string `json:"after"`
}

func roFilterKey(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func roEncodeCursor(c roCursor) string {
	c.V = 1
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func roDecodeCursor(raw, tool, caller, filters string) (string, error) {
	if raw == "" {
		return "", nil
	}
	bad := errors.New("unknown or malformed cursor. Start again without cursor and use next_cursor from that result. Example: {\"limit\":25}")
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", bad
	}
	var c roCursor
	if err := json.Unmarshal(decoded, &c); err != nil || c.V != 1 {
		return "", bad
	}
	if c.Tool != tool || c.Caller != caller || c.Filters != filters {
		return "", errors.New("that cursor belongs to another query. A cursor is bound to the caller and the filters: start again without cursor and use next_cursor from that result")
	}
	return c.After, nil
}

// roWindow returns the page after the cursor key and the key of the next page.
// The items are already sorted by key in the direction asked. A descending list
// has keys that fall; an ascending list has keys that rise.
func roWindow[T any](items []T, limit int, after string, descending bool, key func(T) string) ([]T, string) {
	start := 0
	if after != "" {
		for start < len(items) {
			k := key(items[start])
			if descending && k < after || !descending && k > after {
				break
			}
			start++
		}
	}
	if start >= len(items) {
		return []T{}, ""
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	page := items[start:end]
	if end == len(items) {
		return page, ""
	}
	return page, key(items[end-1])
}

// roPageNote is the hint on a page that was cut.
func roPageNote(what string) string {
	return "More " + what + " match. Call again with next_cursor for the next page, or narrow the filters."
}

// roCallerInfo is the session this MCP server speaks for.
type roCallerInfo struct {
	SessionID string
	NativeID  string
	Name      string
	Project   string
	Sessions  []protocol.SessionInfo
}

// roNativeID is the harness's ID of this session: the Codex thread of the
// call, or the Claude Code session of this process.
func roNativeID(ctx context.Context) string {
	if id, _ := ctx.Value(threadKey{}).(string); id != "" {
		return id
	}
	return os.Getenv("CLAUDE_CODE_SESSION_ID")
}

// roWhoAmI asks the daemon which local session is calling. The daemon binds
// the connection to that session, so the answer already carries its identity.
func roWhoAmI(ctx context.Context, call Caller) (roCallerInfo, error) {
	raw, err := call(ctx, "info", nil)
	if err != nil {
		return roCallerInfo{}, err
	}
	var info protocol.OperatorInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return roCallerInfo{}, err
	}
	native := roNativeID(ctx)
	if native == "" {
		return roCallerInfo{}, errors.New("cannot tell which agent session is calling: CLAUDE_CODE_SESSION_ID is not set")
	}
	for _, session := range info.Sessions {
		if session.NativeID != native {
			continue
		}
		if session.SessionID == "" {
			return roCallerInfo{}, errors.New("this session has no gateway ID yet (is the gateway reachable?)")
		}
		return roCallerInfo{SessionID: session.SessionID, NativeID: native, Name: session.Name,
			Project: session.Repo.Project, Sessions: info.Sessions}, nil
	}
	return roCallerInfo{}, errors.New("cannot tell which agent session is calling: the daemon does not know this session yet (is it discovered and connected?)")
}

// roAgentList reads every agent, online and offline, for names and worker rows.
func roAgentList(ctx context.Context, call Caller) ([]protocol.AgentView, error) {
	raw, err := call(ctx, "list", map[string]any{"online_only": false})
	if err != nil {
		return nil, err
	}
	var result protocol.AgentsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result.Agents, nil
}

// roNames maps session IDs to names from the agents and the local sessions.
func roNames(agents []protocol.AgentView, sessions []protocol.SessionInfo) map[string]string {
	names := map[string]string{}
	for _, session := range sessions {
		if session.SessionID != "" && session.Name != "" {
			names[session.SessionID] = session.Name
		}
	}
	for _, agent := range agents {
		if agent.Profile.Name != "" {
			names[agent.SessionID] = agent.Profile.Name
		}
	}
	return names
}

func roName(names map[string]string, id, fallback string) string {
	if name := names[id]; name != "" {
		return name
	}
	if fallback != "" {
		return fallback
	}
	return id
}

// roSessionRef is an ID with the name a reader can recognize.
func roSessionRef(id, name string) roSessionRefType {
	if name == "" {
		name = id
	}
	return roSessionRefType{ID: id, Name: name}
}

type roSessionRefType struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// roTimeText is RFC 3339 in UTC, as every time in these tools is.
func roTimeText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func roUnixText(seconds int64) string {
	if seconds <= 0 {
		return ""
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}

// roAgoText is a short age such as "3m ago".
func roAgoText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// roRunTime is how long a worker has run.
func roRunTime(startedAt int64, running bool) string {
	if !running || startedAt <= 0 {
		return ""
	}
	d := time.Since(time.Unix(startedAt, 0))
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %dh", int(d.Hours()/24), int(d.Hours())%24)
	}
}

// roPreview is the first line of a body, cut to at most max bytes without
// splitting a character. It says whether anything was cut.
func roPreview(body string, max int) (string, bool) {
	cut := false
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body, cut = body[:i], true
	}
	body = strings.TrimRight(body, "\r")
	short, wasCut := roCutUTF8(body, max)
	return short, cut || wasCut
}

// roCutUTF8 cuts s to at most max bytes at a character boundary.
func roCutUTF8(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end], true
}

// roChunk returns one UTF-8-safe body chunk and the offset of the next one.
func roChunk(body string, offset, max int) (string, int) {
	if offset >= len(body) {
		return "", len(body)
	}
	end := offset + max
	if end >= len(body) {
		return body[offset:], len(body)
	}
	for end > offset && !utf8.RuneStart(body[end]) {
		end--
	}
	return body[offset:end], end
}

// roSortStable sorts strings and removes duplicates.
func roUniqueSorted(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

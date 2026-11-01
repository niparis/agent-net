package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// This file writes one project-scoped MCP server into a harness's own project
// config. It is the write side of the coverage rows in the Tools tab: the
// project stays the source, the other harnesses get their own declaration.
//
// The edit is a minimal insertion. Every other byte of the file stays as it
// was. Environment values must be references such as ${TOKEN}; a literal value
// is refused rather than copied. A same-name entry is never replaced.

var mcpRefRE = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*\}$`)

// mcpDefinition returns an opaque digest of a declaration. It returns "opaque"
// when a value cannot be compared safely across harnesses.
func mcpDefinition(desc map[string]any) string {
	norm := map[string]any{}
	command, args, ok := splitCommand(desc)
	if !ok {
		return "opaque"
	}
	if command != "" {
		if unsafeDefinitionValue(command) {
			return "opaque"
		}
		norm["command"] = command
	}
	if args != nil {
		norm["args"] = args
	}
	if v, ok := desc["url"].(string); ok && v != "" {
		u, err := url.Parse(v)
		if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return "opaque"
		}
		norm["url"] = strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + u.EscapedPath()
	}
	transport := ""
	for _, field := range []string{"type", "transport"} {
		if v, ok := desc[field].(string); ok && v != "" {
			switch strings.ToLower(v) {
			case "local", "stdio":
				transport = "stdio"
			case "remote", "http", "streamable-http", "streamable_http", "streamablehttp":
				transport = "http"
			case "sse":
				transport = "sse"
			default:
				return "opaque"
			}
		}
	}
	if transport == "" {
		if command != "" {
			transport = "stdio"
		} else if v, _ := desc["url"].(string); v != "" {
			transport = "http"
		}
	}
	if transport != "" {
		norm["transport"] = transport
	}
	for _, field := range []string{"env", "environment"} {
		if m, ok := desc[field].(map[string]any); ok {
			values := map[string]any{}
			for k, raw := range m {
				s, ok := raw.(string)
				if !ok {
					return "opaque"
				}
				if s != "" && !mcpRefRE.MatchString(s) {
					return "opaque"
				}
				values[k] = s
			}
			norm["env"] = values
		}
	}
	if m, ok := desc["headers"].(map[string]any); ok {
		values := map[string]any{}
		for k, raw := range m {
			s, ok := raw.(string)
			if !ok || (s != "" && !mcpRefRE.MatchString(s)) {
				return "opaque"
			}
			values[k] = s
		}
		norm["headers"] = values
	}
	b, err := json.Marshal(norm)
	if err != nil {
		return "opaque"
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func unsafeDefinitionValue(v string) bool {
	if mcpRefRE.MatchString(v) {
		return false
	}
	l := strings.ToLower(v)
	for _, word := range []string{"token", "secret", "password", "credential", "bearer ", "apikey", "key="} {
		if strings.Contains(l, word) {
			return true
		}
	}
	return false
}

// splitCommand accepts both the shared command string plus args and OpenCode's
// command array. It also appends the args field.
func splitCommand(desc map[string]any) (string, []any, bool) {
	var command string
	var args []any
	switch c := desc["command"].(type) {
	case string:
		command = c
	case []any:
		for i, raw := range c {
			s, ok := raw.(string)
			if !ok {
				return "", nil, false
			}
			if i == 0 {
				command = s
			} else {
				args = append(args, s)
			}
		}
	case nil:
	default:
		return "", nil, false
	}
	if raw, present := desc["args"]; present {
		list, ok := raw.([]any)
		if !ok {
			return "", nil, false
		}
		for _, v := range list {
			s, ok := v.(string)
			if !ok || unsafeDefinitionValue(s) {
				if !ok {
					return "", nil, false
				}
				return "", nil, false
			}
			args = append(args, s)
		}
	}
	for _, v := range args {
		if s, ok := v.(string); ok && unsafeDefinitionValue(s) {
			return "", nil, false
		}
	}
	if command != "" && unsafeDefinitionValue(command) {
		return "", nil, false
	}
	return command, args, true
}

// ProjectMCPFile names the file a harness reads for project-scoped MCP
// servers. OpenCode keeps whichever spelling already exists.
func ProjectMCPFile(harness, root string) (path, key string, tomlFormat bool, err error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", "", false, errors.New("invalid project root")
	}
	root = filepath.Clean(root)
	switch harness {
	case "claude":
		return filepath.Join(root, ".mcp.json"), "mcpServers", false, nil
	case "pi":
		return filepath.Join(root, ".pi/mcp.json"), "mcpServers", false, nil
	case "opencode":
		for _, name := range []string{"opencode.json", "opencode.jsonc"} {
			p := filepath.Join(root, name)
			if info, e := os.Lstat(p); e == nil && info.Mode().IsRegular() {
				return p, "mcp", false, nil
			}
		}
		return filepath.Join(root, "opencode.json"), "mcp", false, nil
	case "codex":
		return filepath.Join(root, ".codex/config.toml"), "mcp_servers", true, nil
	}
	return "", "", false, errors.New("unsupported harness")
}

// ProjectMCPDescriptor maps one declaration into a harness's own shape. Only
// known fields are copied. An environment value that is not a reference is
// refused, so a value never lands in another file.
func ProjectMCPDescriptor(harness string, source map[string]any) (map[string]any, error) {
	command, args, ok := splitCommand(source)
	if !ok {
		return nil, errors.New("the command is not a list of text")
	}
	env := map[string]any{}
	for _, field := range []string{"env", "environment"} {
		if m, ok := source[field].(map[string]any); ok {
			for k, raw := range m {
				s, ok := raw.(string)
				if !ok {
					return nil, fmt.Errorf("environment variable %s is not a reference", k)
				}
				if s != "" && !mcpRefRE.MatchString(s) {
					return nil, fmt.Errorf("environment variable %s holds a value, not a reference", k)
				}
				env[k] = s
			}
			break
		}
	}
	rawURL, _ := source["url"].(string)
	headers := map[string]any{}
	if m, ok := source["headers"].(map[string]any); ok {
		for k, raw := range m {
			s, ok := raw.(string)
			if !ok || (s != "" && !mcpRefRE.MatchString(s)) {
				return nil, fmt.Errorf("header %s holds a value, not a reference", k)
			}
			headers[k] = s
		}
	}
	if command == "" && rawURL == "" {
		return nil, errors.New("the entry has neither a command nor a URL")
	}
	switch harness {
	case "claude", "pi":
		out := map[string]any{}
		if command != "" {
			out["type"] = "stdio"
			out["command"] = command
			if len(args) > 0 {
				out["args"] = args
			}
		} else {
			out["type"] = "http"
			out["url"] = rawURL
		}
		if len(env) > 0 {
			out["env"] = env
		}
		if len(headers) > 0 {
			out["headers"] = headers
		}
		return out, nil
	case "opencode":
		if command != "" {
			argv := append([]any{command}, args...)
			out := map[string]any{"type": "local", "command": argv}
			if len(env) > 0 {
				out["environment"] = env
			}
			return out, nil
		}
		out := map[string]any{"type": "remote", "url": rawURL}
		if len(headers) > 0 {
			out["headers"] = headers
		}
		return out, nil
	case "codex":
		if command == "" {
			return nil, errors.New("a Codex project entry needs a command")
		}
		out := map[string]any{"command": command}
		if len(args) > 0 {
			out["args"] = args
		}
		if len(env) > 0 {
			out["env"] = env
		}
		return out, nil
	}
	return nil, errors.New("unsupported harness")
}

// AddProjectMCPEntry inserts one entry and returns the new file content. data
// is the current file, or nil when it does not exist. A same-name entry is
// os.ErrExist. The edit is verified before it is returned.
func AddProjectMCPEntry(base, path, key string, tomlFormat bool, name string, desc map[string]any, data []byte) ([]byte, error) {
	if !safePath(base, path) {
		return nil, errors.New("the file path contains a symlink")
	}
	if name == "" || strings.ContainsAny(name, "\"\\\n\r\x00") {
		return nil, errors.New("invalid server name")
	}
	if tomlFormat {
		return addTOMLEntry(key, name, desc, data)
	}
	return addJSONEntry(key, name, desc, data)
}

func addJSONEntry(key, name string, desc map[string]any, data []byte) ([]byte, error) {
	old := map[string]any{}
	if len(data) > 0 {
		if err := json.Unmarshal(stripTrailingCommas(stripComments(data)), &old); err != nil {
			return nil, errors.New("the file is malformed; it was not edited")
		}
	}
	if holder, _ := old[key].(map[string]any); holder != nil {
		if _, exists := holder[name]; exists {
			return nil, os.ErrExist
		}
	}
	entry, err := json.Marshal(desc)
	if err != nil {
		return nil, err
	}
	s := jsonScan{data}
	if len(data) == 0 {
		s.d = []byte("{}")
	}
	at := s.skip(0)
	if at >= len(s.d) || s.d[at] != '{' {
		return nil, errors.New("the file is not a JSON object")
	}
	// Walk the existing path. A missing key is created at the root.
	container := at
	missing := false
	for _, part := range append([]string{key}, "") {
		if part == "" {
			break
		}
		members, _, e := s.members(container)
		if e != nil {
			return nil, errors.New("the file is malformed; it was not edited")
		}
		found := -1
		for i, m := range members {
			if m.key == part {
				found = i
				break
			}
		}
		if found < 0 {
			missing = true
			break
		}
		container = members[found].valStart
		if container >= len(s.d) || s.d[container] != '{' {
			return nil, errors.New("the server table is not an object")
		}
	}
	out, err := insertJSONMember(s.d, container, name, entry, missing, key)
	if err != nil {
		return nil, err
	}
	if err := verifyMCPAddition(old, out, key, name, false); err != nil {
		return nil, err
	}
	return out, nil
}

// insertJSONMember puts "name": entry just inside the object that starts at
// open. When the key path is missing it inserts the whole key object instead.
func insertJSONMember(data []byte, open int, name string, entry []byte, missing bool, key string) ([]byte, error) {
	s := jsonScan{data}
	members, end, err := s.members(open)
	if err != nil {
		return nil, errors.New("the file is malformed; it was not edited")
	}
	if !missing {
		for _, m := range members {
			if m.key == name {
				return nil, os.ErrExist
			}
		}
	}
	indent := "  "
	if len(members) > 0 {
		if got := lineIndent(data, members[0].keyStart); got != "" {
			indent = got
		}
	}
	closing := end - 1
	containerIndent := lineIndent(data, open)
	var member []byte
	if missing {
		member = append(member, []byte(`"`+key+`": {`)...)
		member = append(member, '\n')
		member = append(member, []byte(indent+"  ")...)
		member = append(member, []byte(`"`+name+`": `)...)
		member = append(member, entry...)
		member = append(member, '\n')
		member = append(member, []byte(indent)...)
		member = append(member, '}')
	} else {
		member = append(member, []byte(`"`+name+`": `)...)
		member = append(member, entry...)
	}
	var insert []byte
	if len(members) == 0 {
		insert = append([]byte{'\n'}, []byte(indent)...)
		insert = append(insert, member...)
		insert = append(insert, '\n')
		insert = append(insert, []byte(containerIndent)...)
		out := append([]byte(nil), data[:open+1]...)
		out = append(out, insert...)
		out = append(out, data[closing:]...)
		return out, nil
	}
	insert = append([]byte{'\n'}, []byte(indent)...)
	insert = append(insert, member...)
	insert = append(insert, ',')
	insert = append(insert, '\n')
	insert = append(insert, []byte(indent)...)
	out := append([]byte(nil), data[:open+1]...)
	out = append(out, insert...)
	out = append(out, data[open+1:]...)
	return out, nil
}

// lineIndent returns the whitespace from the start of a line to at.
func lineIndent(data []byte, at int) string {
	if at <= 0 || at > len(data) {
		return ""
	}
	start := at
	for start > 0 && data[start-1] != '\n' {
		start--
	}
	i := start
	for i < at && (data[i] == ' ' || data[i] == '\t') {
		i++
	}
	if i != at {
		return ""
	}
	return string(data[start:at])
}

func addTOMLEntry(key, name string, desc map[string]any, data []byte) ([]byte, error) {
	old := map[string]any{}
	if len(data) > 0 {
		if err := toml.Unmarshal(data, &old); err != nil {
			return nil, errors.New("the file is malformed; it was not edited")
		}
	}
	if holder, _ := old[key].(map[string]any); holder != nil {
		if _, exists := holder[name]; exists {
			return nil, os.ErrExist
		}
	}
	table, err := toml.Marshal(map[string]any{key: map[string]any{name: desc}})
	if err != nil {
		return nil, err
	}
	out := append([]byte(nil), data...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	if len(out) > 0 {
		out = append(out, '\n')
	}
	out = append(out, table...)
	if err := verifyMCPAddition(old, out, key, name, true); err != nil {
		return nil, err
	}
	return out, nil
}

// verifyMCPAddition proves the new text parses and differs from the old one
// only by the new entry.
func verifyMCPAddition(old map[string]any, out []byte, key, name string, tomlFormat bool) error {
	neu := map[string]any{}
	var err error
	if tomlFormat {
		err = toml.Unmarshal(out, &neu)
	} else {
		err = json.Unmarshal(stripTrailingCommas(stripComments(out)), &neu)
	}
	if err != nil {
		return errors.New("the edit would leave a malformed file; nothing was changed")
	}
	holder, _ := neu[key].(map[string]any)
	if holder == nil {
		return errors.New("the edit would not add the entry; nothing was changed")
	}
	if _, ok := holder[name]; !ok {
		return errors.New("the edit would not add the entry; nothing was changed")
	}
	delete(holder, name)
	if len(holder) == 0 {
		delete(neu, key)
	}
	if !reflectDeepEqual(old, neu) {
		return errors.New("the edit would change more than the entry; nothing was changed")
	}
	return nil
}

// reflectDeepEqual compares decoded JSON and TOML values, where numbers may
// arrive as different Go types.
func reflectDeepEqual(a, b any) bool {
	ja, ea := json.Marshal(a)
	jb, eb := json.Marshal(b)
	return ea == nil && eb == nil && string(ja) == string(jb)
}

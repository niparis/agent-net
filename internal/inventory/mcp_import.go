package inventory

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/pelletier/go-toml/v2"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// This file decides whether a native MCP entry can move into the federation.
// The verdict holds names and plain reasons only. Federation takes a command
// or URL, and environment variable names or credential files, so an entry that
// carries a secret value, needs the client's own directory, signs in with
// OAuth or serves more than tools stays where it is.

var (
	secretWordRE  = regexp.MustCompile(`(?i)(token|secret|passw(or)?d|api[-_]?key|credential|bearer|auth)`)
	secretShapeRE = regexp.MustCompile(`^(sk-|ghp_|gho_|ghu_|ghs_|github_pat_|xox[abprs]-|AKIA|glpat-|Bearer )`)
	refRE         = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$|^\$([A-Za-z_][A-Za-z0-9_]*)$`)
)

func unsupported(reason string, args ...any) protocol.MCPImportInfo {
	return protocol.MCPImportInfo{Class: protocol.MCPUnsupported, Reason: fmt.Sprintf(reason, args...)}
}

// refName returns the variable name of a plain reference such as ${NAME}.
func refName(v string) (string, bool) {
	m := refRE.FindStringSubmatch(v)
	if m == nil {
		return "", false
	}
	if m[1] != "" {
		return m[1], true
	}
	return m[2], true
}

func strList(v any) ([]string, bool) {
	l, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(l))
	for _, e := range l {
		s, ok := e.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// ClassifyMCP judges one native entry. For an importable entry it also returns
// the registry server it describes, without a host. The field names of all
// four harnesses are understood.
func ClassifyMCP(name string, desc map[string]any) (protocol.MCPImportInfo, protocol.MCPServer) {
	bad := func(reason string, args ...any) (protocol.MCPImportInfo, protocol.MCPServer) {
		return unsupported(reason, args...), protocol.MCPServer{}
	}
	if name == "agent-net" {
		return bad("this is agent-net's own server")
	}
	server := protocol.MCPServer{Name: protocol.MCPServerName(name)}
	if protocol.ValidMCPName(server.Name) != nil {
		return bad("the name cannot be used as a federated server name")
	}
	if v, ok := desc["enabled"].(bool); ok && !v {
		return bad("the entry is switched off in the harness config")
	}
	if v, ok := desc["disabled"].(bool); ok && v {
		return bad("the entry is switched off in the harness config")
	}
	if o, present := desc["oauth"]; present && o != nil && o != false {
		return bad("signs in with OAuth; the federation cannot sign in for you")
	}
	for _, f := range []string{"auth", "type"} {
		if s, _ := desc[f].(string); strings.EqualFold(s, "oauth") {
			return bad("signs in with OAuth; the federation cannot sign in for you")
		}
	}
	if caps, ok := desc["capabilities"].(map[string]any); ok {
		tools, _ := caps["tools"].(bool)
		resources, _ := caps["resources"].(bool)
		prompts, _ := caps["prompts"].(bool)
		if !tools && (resources || prompts) {
			return bad("serves resources or prompts only; the federation carries tools")
		}
	}
	if _, ok := desc["cwd"]; ok {
		return bad("sets a working directory; the federation runs a server without one")
	}
	if l, ok := strList(firstOf(desc, "disabled_tools", "disabledTools")); ok && len(l) > 0 {
		return bad("hides some tools natively; the federation cannot hide tools")
	}

	command, _ := desc["command"].(string)
	var args []string
	if arr, ok := desc["command"].([]any); ok {
		parts, ok := strList(arr)
		if !ok || len(parts) == 0 {
			return bad("the command is not a list of text")
		}
		command, args = parts[0], parts[1:]
	}
	if a, present := desc["args"]; present {
		l, ok := strList(a)
		if !ok {
			return bad("an argument is not text")
		}
		args = append(args, l...)
	}
	rawURL, _ := desc["url"].(string)
	t, _ := firstOf(desc, "type", "transport").(string)
	switch strings.ToLower(t) {
	case "stdio", "local":
		server.Transport = protocol.MCPStdio
	case "http", "streamable-http", "streamable_http", "remote", "streamablehttp":
		server.Transport = protocol.MCPHTTP
	case "sse":
		server.Transport = protocol.MCPSSE
	case "":
		switch {
		case command != "" && rawURL == "":
			server.Transport = protocol.MCPStdio
		case rawURL != "" && command == "":
			server.Transport = protocol.MCPHTTP
		}
	}
	if server.Transport == "" || (server.Transport == protocol.MCPStdio) != (command != "") || (server.Transport != protocol.MCPStdio) != (rawURL != "") {
		return bad("the transport is not stdio, http or sse")
	}

	envNames := map[string]bool{}
	envMap, _ := firstOf(desc, "env", "environment").(map[string]any)
	for k, v := range envMap {
		if protocol.ValidEnvName(k) != nil {
			return bad("an environment variable name is not valid")
		}
		s, _ := v.(string)
		if ref, ok := refName(s); s != "" && (!ok || ref != k) {
			return bad("sets the value of %s inline; the federation reads variable names from the host environment", k)
		}
		envNames[k] = true
	}
	if l, ok := strList(desc["env_vars"]); ok {
		for _, n := range l {
			if protocol.ValidEnvName(n) != nil {
				return bad("an environment variable name is not valid")
			}
			envNames[n] = true
		}
	}

	if server.Transport == protocol.MCPStdio {
		if err := checkLaunch(command, args); err != nil {
			return bad("%v", err)
		}
		server.Command, server.Args = command, args
		for n := range envNames {
			server.Env = append(server.Env, n)
		}
		sort.Strings(server.Env)
	} else {
		if len(envNames) > 0 {
			return bad("an http server reads no environment; the federation cannot pass it")
		}
		u, err := url.Parse(rawURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return bad("the url is not an http or https address")
		}
		if u.User != nil || strings.Contains(rawURL, "@") {
			return bad("the url holds credentials; use a header read from the host environment")
		}
		for k := range u.Query() {
			if secretWordRE.MatchString(k) {
				return bad("the url query looks like a credential")
			}
		}
		server.URL = rawURL
		if _, ok := desc["bearer_token_env_var"]; ok {
			return bad("reads a bearer token from a variable; the federation passes a header value as it is")
		}
		if h, _ := desc["http_headers"].(map[string]any); len(h) > 0 {
			return bad("header values are set inline; give them as host environment variable names")
		}
		if h, ok := desc["headers"].(map[string]any); ok {
			names := make([]string, 0, len(h))
			for k := range h {
				names = append(names, k)
			}
			sort.Strings(names)
			for _, k := range names {
				s, _ := h[k].(string)
				ref, ok := refName(s)
				if !ok {
					return bad("header %s holds a value; give it as a plain host environment variable reference", k)
				}
				server.Headers = append(server.Headers, protocol.MCPHeader{Name: k, Env: ref})
			}
		}
		if h, ok := desc["env_http_headers"].(map[string]any); ok {
			names := make([]string, 0, len(h))
			for k := range h {
				names = append(names, k)
			}
			sort.Strings(names)
			for _, k := range names {
				s, _ := h[k].(string)
				if protocol.ValidEnvName(s) != nil {
					return bad("header %s does not name a host environment variable", k)
				}
				server.Headers = append(server.Headers, protocol.MCPHeader{Name: k, Env: s})
			}
		}
	}
	if err := server.Validate(); err != nil {
		return bad("%s", err.Error())
	}
	info := protocol.MCPImportInfo{Class: protocol.MCPImportable, Transport: server.Transport, EnvNames: server.Env}
	for _, h := range server.Headers {
		info.EnvNames = append(info.EnvNames, h.Env)
	}
	if l, ok := strList(firstOf(desc, "enabled_tools", "enabledTools", "allowedTools")); ok {
		info.ExpectedTools = l
	}
	return info, server
}

func firstOf(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

// checkLaunch refuses a command line the host would run differently from the
// client: variable expansion, the client's directory, or a secret value.
func checkLaunch(command string, args []string) error {
	if strings.ContainsAny(command, " \t\n") {
		return errors.New("the command holds arguments; split it into a command and arguments")
	}
	all := append([]string{command}, args...)
	for i, a := range all {
		if strings.Contains(a, "$") {
			return errors.New("an argument uses variable expansion; the host does not expand it")
		}
		if strings.HasPrefix(a, "./") || strings.HasPrefix(a, "../") || strings.HasPrefix(a, "~/") || a == "." || a == ".." {
			return errors.New("an argument depends on the client's working directory or home; use an absolute path")
		}
		if secretShapeRE.MatchString(a) {
			return errors.New("an argument holds a secret value")
		}
		if u, err := url.Parse(a); err == nil && u.User != nil {
			return errors.New("an argument holds a secret value")
		}
		if i > 0 && secretWordRE.MatchString(a) {
			if strings.Contains(a, "=") && !strings.HasSuffix(a, "=") {
				return errors.New("an argument holds a secret value")
			}
			if strings.HasPrefix(a, "-") && i+1 < len(all) && !strings.HasPrefix(all[i+1], "-") {
				return errors.New("an argument holds a secret value")
			}
		}
	}
	return nil
}

// safePath reports whether no part of path below base is a symlink.
func safePath(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	cur := base
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			return errors.Is(err, os.ErrNotExist)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}

// readServers reads the server map of one MCP config file, read-only. A
// missing file or key gives no servers and no reason. A reason is the plain
// text of a scan error; it never holds file content.
func readServers(base, path, key string, tomlFormat bool, root string) (map[string]any, string) {
	if !safePath(base, path) {
		return nil, "source contains a symlink"
	}
	dir, err := os.OpenRoot(base)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ""
	}
	if err != nil {
		return nil, "cannot open source root"
	}
	defer dir.Close()
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return nil, "invalid source path"
	}
	f, err := dir.Open(rel)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ""
	}
	if err != nil {
		return nil, "cannot read source"
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return nil, "cannot read source or source too large"
	}
	var doc map[string]any
	if tomlFormat {
		err = toml.Unmarshal(data, &doc)
	} else {
		err = json.Unmarshal(stripComments(data), &doc)
	}
	if err != nil {
		return nil, "malformed configuration"
	}
	if key == "project-local" {
		projects, _ := doc["projects"].(map[string]any)
		projectDoc, _ := projects[root].(map[string]any)
		doc = projectDoc
		key = "mcpServers"
	}
	rawServers, present := doc[key]
	if !present {
		return nil, ""
	}
	servers, ok := rawServers.(map[string]any)
	if !ok {
		return nil, "invalid server map"
	}
	if len(servers) > maxFiles {
		return nil, "source exceeds entry limit"
	}
	return servers, ""
}

// nativeSource says where a native MCP entry lives, so it can be read again.
type nativeSource struct {
	base, path, key string
	toml            bool
}

func (o Options) nativeSource(harness, scope, root, location string) (nativeSource, bool) {
	home := filepath.Clean(o.Home)
	codex := o.CodexHome
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	var candidates []nativeSource
	if scope == protocol.InventoryUser {
		switch harness {
		case "claude":
			candidates = []nativeSource{{home, filepath.Join(home, ".claude.json"), "mcpServers", false}}
		case "codex":
			candidates = []nativeSource{{codex, filepath.Join(codex, "config.toml"), "mcp_servers", true}}
		case "opencode":
			for _, f := range []string{"opencode.json", "opencode.jsonc"} {
				candidates = append(candidates, nativeSource{home, filepath.Join(home, ".config/opencode", f), "mcp", false})
			}
		case "pi":
			candidates = []nativeSource{{home, filepath.Join(home, ".pi/agent/mcp.json"), "mcpServers", false}}
		}
	} else if scope == protocol.InventoryProject && root != "" {
		base := filepath.Clean(root)
		switch harness {
		case "claude":
			candidates = []nativeSource{{base, filepath.Join(base, ".mcp.json"), "mcpServers", false}, {home, filepath.Join(home, ".claude.json"), "project-local", false}}
		case "codex":
			candidates = []nativeSource{{base, filepath.Join(base, ".codex/config.toml"), "mcp_servers", true}}
		case "opencode":
			for _, f := range []string{"opencode.json", "opencode.jsonc"} {
				candidates = append(candidates, nativeSource{base, filepath.Join(base, f), "mcp", false})
			}
		case "pi":
			candidates = []nativeSource{{base, filepath.Join(base, ".pi/mcp.json"), "mcpServers", false}}
		}
	}
	for _, c := range candidates {
		if c.path == location {
			return c, true
		}
	}
	return nativeSource{}, false
}

// LoadNativeMCP reads one observed native entry again, read-only, and answers
// with the verdict and the server it describes. root is the project checkout
// of a project-level entry, taken from the caller's own scan. The file is
// never written.
func LoadNativeMCP(o Options, harness, scope, root, location, name string) (protocol.MCPImportInfo, protocol.MCPServer, string, error) {
	src, ok := o.nativeSource(harness, scope, root, location)
	if !ok {
		return protocol.MCPImportInfo{}, protocol.MCPServer{}, "", errors.New("not a known native MCP source")
	}
	servers, reason := readServers(src.base, src.path, src.key, src.toml, root)
	if reason != "" {
		return protocol.MCPImportInfo{}, protocol.MCPServer{}, "", errors.New(reason)
	}
	desc, ok := servers[name].(map[string]any)
	if !ok {
		return protocol.MCPImportInfo{}, protocol.MCPServer{}, "", os.ErrNotExist
	}
	info, server := ClassifyMCP(name, desc)
	return info, server, fingerprint(name, desc), nil
}

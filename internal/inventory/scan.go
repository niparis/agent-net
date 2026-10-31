package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/pelletier/go-toml/v2"
)

// Options contains explicit scan roots. TrustedProjects controls whether
// project configuration, which harnesses can require explicit trust for, is read.
type Options struct {
	Home, CodexHome, Machine string
	Projects                 []protocol.ProjectEntry
	TrustedProjects          map[string]bool
}

// Scan discovers supported native roots without executing their contents.
func Scan(o Options) protocol.InventoryReport {
	now := time.Now().UTC()
	report := protocol.InventoryReport{Machine: o.Machine, ScannedAt: now, State: protocol.InventoryOK, Items: []protocol.InventoryItem{}}
	totalFields := 0
	manifestBytes := 0
	add := func(h, k, scope, project, root, path, name, hash, state, reason string, unsupported ...string) {
		total := len(h) + len(k) + len(scope) + len(project) + len(root) + len(path) + len(name) + len(hash) + len(reason)
		for _, flag := range unsupported {
			total += len(flag)
		}
		if len(name) > 4096 || len(path) > 4096 || len(root) > 4096 || len(project) > 4096 || len(report.Items) >= 10000 || totalFields+total > 512<<10 {
			report.State = protocol.InventoryError
			report.Reason = "inventory exceeds report limit"
			return
		}
		totalFields += total
		if state == protocol.InventoryError {
			report.State = protocol.InventoryError
			report.Reason = "one or more sources could not be scanned"
		}
		report.Items = append(report.Items, protocol.InventoryItem{Harness: h, Kind: k, Scope: scope, Project: project, ProjectRoot: root, Location: path, Name: name, Source: protocol.InventoryNative, Hash: hash, ScannedAt: now, State: state, Reason: reason, Unsupported: unsupported})
	}
	attachFiles := func(index int, files []string) {
		if index >= len(report.Items) {
			return
		}
		size := 0
		for _, file := range files {
			size += len(file)
		}
		if size > 256<<10 || totalFields+size > 512<<10 {
			report.State = protocol.InventoryError
			report.Reason = "inventory exceeds report limit"
			return
		}
		totalFields += size
		report.Items[index].Files = files
	}
	attachManifest := func(index int, manifest *protocol.SkillManifest) {
		if index >= len(report.Items) {
			return
		}
		data, _ := json.Marshal(manifest)
		if manifestBytes+len(data) > 1<<20 {
			report.State = protocol.InventoryError
			report.Reason = "inventory manifests exceed report limit"
			return
		}
		manifestBytes += len(data)
		report.Items[index].Manifest = manifest
	}

	// Never traverse links in a source path or skill root.
	safe := safePath
	sourceCount := 0
	var skills func(h, scope, project, root, base, path string)
	skills = func(h, scope, project, root, base, path string) {
		sourceCount++
		relDepth, depthErr := filepath.Rel(base, path)
		if depthErr != nil || sourceCount > maxFiles || strings.Count(relDepth, string(os.PathSeparator)) > 32 {
			add(h, protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "skill source exceeds traversal limit")
			return
		}
		if !safe(base, path) {
			add(h, protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "source contains a symlink")
			return
		}
		baseRoot, err := os.OpenRoot(base)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			add(h, protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "cannot open source root")
			return
		}
		defer baseRoot.Close()
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return
		}
		sourceRoot, err := baseRoot.OpenRoot(rel)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			add(h, protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "cannot read source")
			return
		}
		defer sourceRoot.Close()
		source, err := sourceRoot.Open(".")
		if err != nil {
			add(h, protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "cannot read source")
			return
		}
		entries, err := source.ReadDir(maxFiles + 1)
		source.Close()
		if err != nil && !errors.Is(err, io.EOF) {
			add(h, protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "cannot read source")
			return
		}
		if len(entries) > maxFiles {
			add(h, protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "source exceeds entry limit")
			return
		}
		for _, e := range entries {
			p := filepath.Join(path, e.Name())
			actual := p
			var alias string
			var targetInfo os.FileInfo
			if e.Type()&os.ModeSymlink != 0 {
				target, resolveErr := filepath.EvalSymlinks(p)
				if resolveErr != nil || !safePath(o.Home, target) {
					link, _ := os.Readlink(p)
					add(h, protocol.InventorySkill, scope, project, root, p, e.Name(), "", protocol.InventoryError, "skill root is a symlink: "+link)
					continue
				}
				info, statErr := os.Lstat(target)
				if statErr != nil || !info.IsDir() {
					continue
				}
				actual, alias, targetInfo = target, p, info
			} else if !e.IsDir() {
				continue
			}
			var skillRoot *os.Root
			if alias != "" {
				homeRoot, openErr := os.OpenRoot(o.Home)
				if openErr != nil {
					err = openErr
				} else {
					relTarget, relErr := filepath.Rel(o.Home, actual)
					if relErr != nil {
						err = relErr
					} else {
						skillRoot, err = homeRoot.OpenRoot(relTarget)
					}
					homeRoot.Close()
					if err == nil {
						opened, statErr := skillRoot.Stat(".")
						again, resolveErr := filepath.EvalSymlinks(p)
						if statErr != nil || resolveErr != nil || again != actual || !os.SameFile(targetInfo, opened) {
							skillRoot.Close()
							skillRoot = nil
							err = errors.New("skill target changed during scan")
						}
					}
				}
			} else {
				skillRoot, err = sourceRoot.OpenRoot(e.Name())
			}
			if err != nil {
				add(h, protocol.InventorySkill, scope, project, root, p, e.Name(), "", protocol.InventoryError, "cannot open skill safely")
				continue
			}
			info, err := skillRoot.Lstat("SKILL.md")
			if errors.Is(err, os.ErrNotExist) {
				skillRoot.Close()
				if h == "pi" {
					skills(h, scope, project, root, base, p)
				}
				continue
			}
			if err == nil && info.Mode()&os.ModeSymlink != 0 {
				info, err = skillRoot.Stat("SKILL.md")
			}
			if err != nil || !info.Mode().IsRegular() {
				skillRoot.Close()
				add(h, protocol.InventorySkill, scope, project, root, p, e.Name(), "", protocol.InventoryError, "invalid skill file")
				continue
			}
			hash, err := skillHashRoot(skillRoot)
			fingerprint, fpErr := skillContentFingerprintRoot(skillRoot)
			if err == nil {
				err = fpErr
			}
			files, filesErr := skillFilesRoot(skillRoot)
			manifest, manifestErr := skillManifestRoot(skillRoot)
			if err == nil {
				err = manifestErr
			}
			if err == nil {
				err = filesErr
			}
			skillRoot.Close()
			if err != nil {
				add(h, protocol.InventorySkill, scope, project, root, p, e.Name(), "", protocol.InventoryError, "cannot hash skill safely")
				continue
			}
			index := len(report.Items)
			add(h, protocol.InventorySkill, scope, project, root, actual, e.Name(), hash, protocol.InventoryOK, "")
			if alias != "" && index < len(report.Items) {
				if totalFields+len(alias) > 512<<10 {
					report.State = protocol.InventoryError
					report.Reason = "inventory exceeds report limit"
					return
				}
				totalFields += len(alias)
				report.Items[index].Aliases = []string{alias}
			}
			if index < len(report.Items) {
				if totalFields+len(fingerprint) > 512<<10 {
					report.State = protocol.InventoryError
					report.Reason = "inventory exceeds report limit"
					return
				}
				report.Items[index].ContentFingerprint = fingerprint
				totalFields += len(fingerprint)
			}
			attachFiles(index, files)
			attachManifest(index, manifest)
		}
	}
	config := func(h, scope, project, root, base, path, key string, tomlFormat bool) {
		servers, reason := readServers(base, path, key, tomlFormat, root)
		if reason != "" {
			add(h, protocol.InventoryMCP, scope, project, root, path, "", "", protocol.InventoryError, reason)
			return
		}
		names := make([]string, 0, len(servers))
		for name := range servers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			desc, ok := servers[name].(map[string]any)
			if !ok {
				add(h, protocol.InventoryMCP, scope, project, root, path, name, "", protocol.InventoryError, "invalid server descriptor")
				continue
			}
			before := len(report.Items)
			add(h, protocol.InventoryMCP, scope, project, root, path, name, fingerprint(name, desc), protocol.InventoryOK, "", "sensitive and unknown descriptor values redacted")
			if len(report.Items) > before {
				info, _ := ClassifyMCP(name, desc)
				totalFields += len(info.Reason) + len(info.Transport) + 64
				report.Items[before].Import = &info
			}
		}
	}
	piSettings := func(scope, project, root, base, path string) {
		if !safe(base, path) {
			add("pi", protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "source contains a symlink")
			return
		}
		dir, err := os.OpenRoot(base)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			add("pi", protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "cannot open source root")
			return
		}
		defer dir.Close()
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return
		}
		f, err := dir.Open(rel)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			add("pi", protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "cannot read settings")
			return
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
		if err != nil || len(data) > 1<<20 {
			add("pi", protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "cannot read settings or source too large")
			return
		}
		var doc struct {
			Skills   []string          `json:"skills"`
			Packages []json.RawMessage `json:"packages"`
		}
		if json.Unmarshal(data, &doc) != nil {
			add("pi", protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "malformed settings")
			return
		}
		if len(doc.Packages) > 0 {
			before := len(report.Items)
			add("pi", protocol.InventorySkill, scope, project, root, path, "packages", "", protocol.InventoryOK, "", "package sources require manual inspection")
			if len(report.Items) > before {
				report.Items[len(report.Items)-1].Source = protocol.InventoryPackage
			}
		}
		for _, source := range doc.Skills {
			if !filepath.IsAbs(source) {
				source = filepath.Join(filepath.Dir(path), source)
			}
			source = filepath.Clean(source)
			if !safe(base, source) {
				add("pi", protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "configured skill path is outside allowed root or contains a symlink", "manual")
				continue
			}
			if filepath.Base(source) != "SKILL.md" {
				candidate := filepath.Join(source, "SKILL.md")
				rel, relErr := filepath.Rel(base, candidate)
				if relErr == nil {
					if info, statErr := dir.Lstat(rel); statErr == nil && info.Mode().IsRegular() {
						source = candidate
					}
				}
			}
			if filepath.Base(source) == "SKILL.md" {
				relDir, err := filepath.Rel(base, filepath.Dir(source))
				if err != nil {
					continue
				}
				skillRoot, err := dir.OpenRoot(relDir)
				if err != nil {
					add("pi", protocol.InventorySkill, scope, project, root, source, "", "", protocol.InventoryError, "cannot open skill safely")
					continue
				}
				info, err := skillRoot.Lstat("SKILL.md")
				if err != nil || !info.Mode().IsRegular() {
					skillRoot.Close()
					add("pi", protocol.InventorySkill, scope, project, root, source, "", "", protocol.InventoryError, "cannot read skill file")
					continue
				}
				hash, err := skillHashRoot(skillRoot)
				fingerprint, fpErr := skillContentFingerprintRoot(skillRoot)
				if err == nil {
					err = fpErr
				}
				files, filesErr := skillFilesRoot(skillRoot)
				manifest, manifestErr := skillManifestRoot(skillRoot)
				if err == nil {
					err = manifestErr
				}
				if err == nil {
					err = filesErr
				}
				skillRoot.Close()
				name := filepath.Base(filepath.Dir(source))
				if err != nil {
					add("pi", protocol.InventorySkill, scope, project, root, source, name, "", protocol.InventoryError, "cannot hash skill safely")
				} else {
					index := len(report.Items)
					add("pi", protocol.InventorySkill, scope, project, root, filepath.Dir(source), name, hash, protocol.InventoryOK, "")
					if index < len(report.Items) {
						if totalFields+len(fingerprint) > 512<<10 {
							report.State = protocol.InventoryError
							report.Reason = "inventory exceeds report limit"
							return
						}
						report.Items[index].ContentFingerprint = fingerprint
						totalFields += len(fingerprint)
					}
					attachFiles(index, files)
					attachManifest(index, manifest)
				}
				continue
			}
			skills("pi", scope, project, root, base, source)
		}
	}
	home := filepath.Clean(o.Home)
	codex := o.CodexHome
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	for _, x := range [][2]string{{"claude", ".claude/skills"}, {"codex", ".agents/skills"}, {"opencode", ".config/opencode/skills"}, {"opencode", ".claude/skills"}, {"opencode", ".agents/skills"}, {"pi", ".pi/agent/skills"}, {"pi", ".agents/skills"}} {
		skills(x[0], protocol.InventoryUser, "", "", home, filepath.Join(home, x[1]))
	}
	skills("codex", protocol.InventoryUser, "", "", codex, filepath.Join(codex, "skills"))
	for _, source := range []struct{ base, path string }{{codex, filepath.Join(codex, "skills/.system")}, {home, filepath.Join(home, ".agents/skills/.system")}} {
		start := len(report.Items)
		skills("codex", protocol.InventoryUser, "", "", source.base, source.path)
		for i := start; i < len(report.Items); i++ {
			report.Items[i].Source = protocol.InventorySystem
			report.Items[i].Unsupported = append(report.Items[i].Unsupported, "system managed")
		}
	}
	config("claude", protocol.InventoryUser, "", "", home, filepath.Join(home, ".claude.json"), "mcpServers", false)
	config("codex", protocol.InventoryUser, "", "", codex, filepath.Join(codex, "config.toml"), "mcp_servers", true)
	for _, f := range []string{"opencode.json", "opencode.jsonc"} {
		config("opencode", protocol.InventoryUser, "", "", home, filepath.Join(home, ".config/opencode", f), "mcp", false)
	}
	plugins := filepath.Join(home, ".claude/plugins")
	if safe(home, plugins) {
		if _, err := os.Lstat(plugins); err == nil {
			before := len(report.Items)
			add("claude", protocol.InventorySkill, protocol.InventoryUser, "", "", plugins, "plugins", "", protocol.InventoryOK, "", "plugin sources require manual inspection")
			if len(report.Items) > before {
				report.Items[len(report.Items)-1].Source = protocol.InventoryPlugin
			}
		}
	}
	extensions := filepath.Join(home, ".pi/agent/extensions")
	if _, err := os.Lstat(extensions); err == nil {
		before := len(report.Items)
		add("pi", protocol.InventoryMCP, protocol.InventoryUser, "", "", extensions, "extensions", "", protocol.InventoryOK, "", "extension MCP requires manual inspection")
		if len(report.Items) > before {
			report.Items[len(report.Items)-1].Source = protocol.InventoryPlugin
		}
	}
	piSettings(protocol.InventoryUser, "", "", home, filepath.Join(home, ".pi/agent/settings.json"))
	config("pi", protocol.InventoryUser, "", "", home, filepath.Join(home, ".pi/agent/mcp.json"), "mcpServers", false)
	// Trust is harness-specific; do not infer one harness's decision from another.
	readTrust := func(base, path string) []byte {
		if !safe(base, path) {
			return nil
		}
		r, err := os.OpenRoot(base)
		if err != nil {
			return nil
		}
		defer r.Close()
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return nil
		}
		f, err := r.Open(rel)
		if err != nil {
			return nil
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
		if err != nil || len(b) > 1<<20 {
			return nil
		}
		return b
	}
	var codexTrust struct {
		Projects map[string]struct {
			TrustLevel string `toml:"trust_level"`
		} `toml:"projects"`
	}
	_ = toml.Unmarshal(readTrust(codex, filepath.Join(codex, "config.toml")), &codexTrust)
	var piSettingsTrust struct {
		DefaultProjectTrust string `json:"defaultProjectTrust"`
	}
	_ = json.Unmarshal(readTrust(home, filepath.Join(home, ".pi/agent/settings.json")), &piSettingsTrust)
	var piTrust map[string]bool
	_ = json.Unmarshal(readTrust(home, filepath.Join(home, ".pi/agent/trust.json")), &piTrust)
	piTrusted := func(path string) bool {
		for {
			if decision, ok := piTrust[path]; ok {
				return decision
			}
			parent := filepath.Dir(path)
			if parent == path {
				return piSettingsTrust.DefaultProjectTrust == "always"
			}
			path = parent
		}
	}
	for _, p := range o.Projects {
		base := filepath.Clean(p.Path)
		for _, x := range [][2]string{{"claude", ".claude/skills"}, {"codex", ".agents/skills"}, {"opencode", ".opencode/skills"}, {"opencode", ".claude/skills"}, {"opencode", ".agents/skills"}, {"pi", ".pi/skills"}, {"pi", ".agents/skills"}} {
			skills(x[0], protocol.InventoryProject, p.Name, base, base, filepath.Join(base, x[1]))
		}
		config("claude", protocol.InventoryProject, p.Name, base, base, filepath.Join(base, ".mcp.json"), "mcpServers", false)
		config("claude", protocol.InventoryProject, p.Name, base, home, filepath.Join(home, ".claude.json"), "project-local", false)
		for _, f := range []string{"opencode.json", "opencode.jsonc"} {
			config("opencode", protocol.InventoryProject, p.Name, base, base, filepath.Join(base, f), "mcp", false)
		}
		if o.TrustedProjects[base] || codexTrust.Projects[base].TrustLevel == "trusted" {
			config("codex", protocol.InventoryProject, p.Name, base, base, filepath.Join(base, ".codex/config.toml"), "mcp_servers", true)
		}
		if o.TrustedProjects[base] || piTrusted(base) {
			extensions := filepath.Join(base, ".pi/extensions")
			if safe(base, extensions) {
				if _, err := os.Lstat(extensions); err == nil {
					before := len(report.Items)
					add("pi", protocol.InventoryMCP, protocol.InventoryProject, p.Name, base, extensions, "extensions", "", protocol.InventoryOK, "", "extension MCP requires manual inspection")
					if len(report.Items) > before {
						report.Items[len(report.Items)-1].Source = protocol.InventoryPlugin
					}
				}
			}
			piSettings(protocol.InventoryProject, p.Name, base, base, filepath.Join(base, ".pi/settings.json"))
			config("pi", protocol.InventoryProject, p.Name, base, base, filepath.Join(base, ".pi/mcp.json"), "mcpServers", false)
		}

	}
	encoded, err := json.Marshal(report)
	if err != nil || len(encoded) > 3<<20 {
		report.State = protocol.InventoryError
		report.Reason = "inventory exceeds encoded report limit"
		report.Items = nil
	}
	return report
}

// stripComments removes JSONC comments while preserving quoted strings.
func stripComments(b []byte) []byte {
	out := append([]byte(nil), b...)
	inString, escaped := false, false
	for i := 0; i < len(out); i++ {
		c := out[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		if c == '/' && i+1 < len(out) && out[i+1] == '/' {
			out[i] = ' '
			i++
			out[i] = ' '
			for i+1 < len(out) && out[i+1] != '\n' {
				i++
				out[i] = ' '
			}
		} else if c == '/' && i+1 < len(out) && out[i+1] == '*' {
			out[i] = ' '
			i++
			out[i] = ' '
			closed := false
			for i+1 < len(out) {
				i++
				if out[i] == '*' && out[i+1] == '/' {
					out[i] = ' '
					i++
					out[i] = ' '
					closed = true
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
			}
			if !closed {
				return []byte(fmt.Sprintf("invalid"))
			}
		}
	}
	inString, escaped = false, false
	for i, c := range out {
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(out) && (out[j] == ' ' || out[j] == '\n' || out[j] == '\r' || out[j] == '\t') {
				j++
			}
			if j < len(out) && (out[j] == ']' || out[j] == '}') {
				out[i] = ' '
			}
		}
	}
	return out
}

// fingerprint hashes only an allowlisted, redacted descriptor. Unknown argument
// values, environment variable values, headers and URL query strings never
// enter the hash.
func fingerprint(_ string, m map[string]any) string {
	parts := []string{"mcp-v2"}
	command, _ := m["command"].(string)
	if command == "" {
		if a, ok := m["command"].([]any); ok && len(a) > 0 {
			command, _ = a[0].(string)
		}
	}
	if command != "" && !strings.ContainsAny(command, "?=#@ \t\n") {
		parts = append(parts, "command", command)
	}
	for _, field := range []string{"type", "transport"} {
		if value, ok := m[field].(string); ok && (value == "stdio" || value == "http" || value == "sse" || value == "local" || value == "remote") {
			parts = append(parts, field, value)
		}
	}
	if raw, ok := m["url"].(string); ok {
		if u, err := url.Parse(raw); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" {
			parts = append(parts, "url", u.Scheme, strings.ToLower(u.Host), u.EscapedPath())
		}
	}
	if env, ok := m["env"].(map[string]any); ok {
		names := make([]string, 0, len(env))
		for name := range env {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			parts = append(parts, "env", name)
		}
	}
	for _, field := range []string{"args", "command"} {
		if args, ok := m[field].([]any); ok {
			for i, arg := range args {
				if field == "command" && i == 0 {
					continue
				}
				v, ok := arg.(string)
				if !ok {
					continue
				}
				lower := strings.ToLower(v)
				safePath := !strings.ContainsAny(v, "?=#@ \t\n") && !strings.Contains(lower, "secret") && !strings.Contains(lower, "token") && !strings.Contains(lower, "password") && !strings.Contains(lower, "credential")
				if i > 0 && args[i-1] == "--config" && safePath {
					parts = append(parts, "config", v)
					continue
				}
				if (i == 0 || (field == "command" && i == 1)) && safePath && (strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".mjs") || strings.HasSuffix(lower, ".py")) {
					parts = append(parts, "script", v)
					continue
				}
				if i > 0 && args[i-1] == "--mode" && (v == "read" || v == "write" || v == "serve" || v == "development" || v == "production") {
					parts = append(parts, "mode", v)
					continue
				}
				if i > 0 && args[i-1] == "--port" {
					if port, err := strconv.Atoi(v); err == nil && port > 0 && port <= 65535 {
						parts = append(parts, "port", strconv.Itoa(port))
						continue
					}
				}
				if v == "--port" || v == "--config" || v == "--mode" || v == "--host" || v == "--transport" || v == "--stdio" || v == "--sse" || v == "--http" {
					parts = append(parts, "flag", v)
				} else {
					parts = append(parts, "redacted")
				}
			}
		}
	}
	data, _ := json.Marshal(parts)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// FederationItem converts a registry entry to a non-secret observation.
func FederationItem(s protocol.MCPServer, now time.Time) protocol.InventoryItem {
	args := make([]any, len(s.Args))
	for i, a := range s.Args {
		args[i] = a
	}
	env := make(map[string]any)
	for _, e := range s.Env {
		env[e] = nil
	}
	desc := map[string]any{"command": s.Command, "args": args, "env": env, "url": s.URL, "transport": s.Transport}
	return protocol.InventoryItem{Harness: "agent-net", Kind: protocol.InventoryMCP, Scope: protocol.InventoryUser, Location: s.Name, Name: s.Name, Source: protocol.InventoryFederation, Hash: fingerprint(s.Name, desc), ScannedAt: now, State: protocol.InventoryOK}
}

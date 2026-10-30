package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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
	add := func(h, k, scope, project, root, path, name, hash, state, reason string, unsupported ...string) {
		report.Items = append(report.Items, protocol.InventoryItem{Harness: h, Kind: k, Scope: scope, Project: project, ProjectRoot: root, Location: path, Name: name, Source: protocol.InventoryNative, Hash: hash, ScannedAt: now, State: state, Reason: reason, Unsupported: unsupported})
	}
	// Never traverse links in a source path or skill root.
	safe := func(base, path string) bool {
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
	skills := func(h, scope, project, root, base, path string) {
		if !safe(base, path) {
			add(h, protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "source contains a symlink")
			return
		}
		entries, err := os.ReadDir(path)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			add(h, protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "cannot read source")
			return
		}
		if len(entries) > maxFiles {
			add(h, protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "source exceeds entry limit")
			return
		}
		for _, e := range entries {
			p := filepath.Join(path, e.Name())
			if !e.IsDir() {
				if e.Type()&os.ModeSymlink != 0 {
					add(h, protocol.InventorySkill, scope, project, root, p, e.Name(), "", protocol.InventoryError, "skill root is a symlink")
				}
				continue
			}
			if info, err := os.Lstat(filepath.Join(p, "SKILL.md")); err != nil || !info.Mode().IsRegular() {
				continue
			}
			hash, err := SkillHash(p)
			if err != nil {
				add(h, protocol.InventorySkill, scope, project, root, p, e.Name(), "", protocol.InventoryError, "cannot hash skill safely")
				continue
			}
			add(h, protocol.InventorySkill, scope, project, root, p, e.Name(), hash, protocol.InventoryOK, "")
		}
	}
	config := func(h, scope, project, root, base, path, key string, tomlFormat bool) {
		if !safe(base, path) {
			add(h, protocol.InventoryMCP, scope, project, root, path, "", "", protocol.InventoryError, "source contains a symlink")
			return
		}
		dir, err := os.OpenRoot(base)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			add(h, protocol.InventoryMCP, scope, project, root, path, "", "", protocol.InventoryError, "cannot open source root")
			return
		}
		defer dir.Close()
		rel, err := filepath.Rel(base, path)
		if err != nil {
			add(h, protocol.InventoryMCP, scope, project, root, path, "", "", protocol.InventoryError, "invalid source path")
			return
		}
		f, err := dir.Open(rel)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			add(h, protocol.InventoryMCP, scope, project, root, path, "", "", protocol.InventoryError, "cannot read source")
			return
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
		if err != nil || len(data) > 1<<20 {
			add(h, protocol.InventoryMCP, scope, project, root, path, "", "", protocol.InventoryError, "cannot read source or source too large")
			return
		}
		var doc map[string]any
		if tomlFormat {
			err = toml.Unmarshal(data, &doc)
		} else {
			err = json.Unmarshal(stripComments(data), &doc)
		}
		if err != nil {
			add(h, protocol.InventoryMCP, scope, project, root, path, "", "", protocol.InventoryError, "malformed configuration")
			return
		}
		if key == "project-local" {
			projects, _ := doc["projects"].(map[string]any)
			projectDoc, _ := projects[root].(map[string]any)
			doc = projectDoc
			key = "mcpServers"
		}
		servers, ok := doc[key].(map[string]any)
		if !ok {
			return
		}
		if len(servers) > maxFiles {
			add(h, protocol.InventoryMCP, scope, project, root, path, "", "", protocol.InventoryError, "source exceeds entry limit")
			return
		}
		names := make([]string, 0, len(servers))
		for name := range servers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names { // Only field names are included: arbitrary values can contain secrets.
			fields := []string{}
			if m, ok := servers[name].(map[string]any); ok {
				for field := range m {
					fields = append(fields, field)
				}
			}
			sort.Strings(fields)
			sum := sha256.Sum256([]byte(strings.Join(fields, "\x00")))
			add(h, protocol.InventoryMCP, scope, project, root, path, name, hex.EncodeToString(sum[:]), protocol.InventoryOK, "", "descriptor values redacted")
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
			Skills []string `json:"skills"`
		}
		if json.Unmarshal(data, &doc) != nil {
			add("pi", protocol.InventorySkill, scope, project, root, path, "", "", protocol.InventoryError, "malformed settings")
			return
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
			skills("pi", scope, project, root, base, source)
		}
	}
	home := filepath.Clean(o.Home)
	codex := o.CodexHome
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	for _, x := range [][2]string{{"claude", ".claude/skills"}, {"codex", ".agents/skills"}, {"opencode", ".config/opencode/skills"}, {"opencode", ".claude/skills"}, {"opencode", ".agents/skills"}, {"pi", ".pi/agent/skills"}} {
		skills(x[0], protocol.InventoryUser, "", "", home, filepath.Join(home, x[1]))
	}
	skills("codex", protocol.InventoryUser, "", "", codex, filepath.Join(codex, "skills"))
	config("claude", protocol.InventoryUser, "", "", home, filepath.Join(home, ".claude.json"), "mcpServers", false)
	config("codex", protocol.InventoryUser, "", "", codex, filepath.Join(codex, "config.toml"), "mcp_servers", true)
	for _, f := range []string{"opencode.json", "opencode.jsonc"} {
		config("opencode", protocol.InventoryUser, "", "", home, filepath.Join(home, ".config/opencode", f), "mcp", false)
	}
	piSettings(protocol.InventoryUser, "", "", home, filepath.Join(home, ".pi/agent/settings.json"))
	config("pi", protocol.InventoryUser, "", "", home, filepath.Join(home, ".pi/agent/mcp.json"), "mcpServers", false)
	for _, p := range o.Projects {
		base := filepath.Clean(p.Path)
		for _, x := range [][2]string{{"claude", ".claude/skills"}, {"codex", ".agents/skills"}, {"opencode", ".opencode/skills"}, {"opencode", ".claude/skills"}, {"opencode", ".agents/skills"}, {"pi", ".pi/skills"}} {
			skills(x[0], protocol.InventoryProject, p.Name, base, base, filepath.Join(base, x[1]))
		}
		config("claude", protocol.InventoryProject, p.Name, base, base, filepath.Join(base, ".mcp.json"), "mcpServers", false)
		config("claude", protocol.InventoryProject, p.Name, base, home, filepath.Join(home, ".claude.json"), "project-local", false)
		for _, f := range []string{"opencode.json", "opencode.jsonc"} {
			config("opencode", protocol.InventoryProject, p.Name, base, base, filepath.Join(base, f), "mcp", false)
		}
		if o.TrustedProjects[base] {
			piSettings(protocol.InventoryProject, p.Name, base, base, filepath.Join(base, ".pi/settings.json"))
			config("codex", protocol.InventoryProject, p.Name, base, base, filepath.Join(base, ".codex/config.toml"), "mcp_servers", true)
			config("pi", protocol.InventoryProject, p.Name, base, base, filepath.Join(base, ".pi/mcp.json"), "mcpServers", false)
		}
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

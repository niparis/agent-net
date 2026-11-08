package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Claude Code 2.1.319 keeps the answer to "New MCP server found in this
// project" in <launch folder>/.claude/settings.local.json, under
// enabledMcpjsonServers and disabledMcpjsonServers. It reads .mcp.json from the
// launch folder and every folder above it; a name in a nearer file wins. The
// older projects[<path>] lists in ~/.claude.json are legacy and are not read
// for this. A refusal wins over an approval for the same name. See decision
// 0096. agent-net never sets enableAllProjectMcpServers.
const (
	claudeMCPEnabledKey  = "enabledMcpjsonServers"
	claudeMCPDisabledKey = "disabledMcpjsonServers"
)

// claudeMCPServers lists the servers of the .mcp.json files Claude Code loads
// for a launch folder: the nearest file first, names sorted inside a file.
func claudeMCPServers(dir string) []protocol.MCPServerInfo {
	var out []protocol.MCPServerInfo
	seen := map[string]bool{}
	for cur := filepath.Clean(dir); ; {
		file := filepath.Join(cur, ".mcp.json")
		if raw, err := os.ReadFile(file); err == nil {
			var doc struct {
				Servers map[string]struct {
					Type    string   `json:"type"`
					Command string   `json:"command"`
					Args    []string `json:"args"`
					URL     string   `json:"url"`
				} `json:"mcpServers"`
			}
			if json.Unmarshal(raw, &doc) == nil {
				names := make([]string, 0, len(doc.Servers))
				for name := range doc.Servers {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					if seen[name] {
						continue
					}
					seen[name] = true
					s := doc.Servers[name]
					command := strings.TrimSpace(strings.Join(append([]string{s.Command}, s.Args...), " "))
					if s.Command == "" {
						command = firstNonEmpty(s.URL, s.Type)
					}
					out = append(out, protocol.MCPServerInfo{Name: name, Command: cleanLine(command), File: file})
				}
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return out
		}
		cur = parent
	}
}

func claudeLocalSettingsFile(dir string) string {
	return filepath.Join(dir, ".claude", "settings.local.json")
}

// readClaudeLocalSettings reads the settings file as raw keys, so every key
// agent-net does not own is written back unchanged. A missing file is empty.
func readClaudeLocalSettings(file string) (map[string]json.RawMessage, error) {
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	doc := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return doc, nil
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object, so it was not changed: %w", file, err)
	}
	return doc, nil
}

func claudeSettingsList(doc map[string]json.RawMessage, key string) ([]string, error) {
	raw, ok := doc[key]
	if !ok {
		return nil, nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("%s in settings.local.json is not a list of names: %w", key, err)
	}
	return list, nil
}

func setClaudeSettingsList(doc map[string]json.RawMessage, key string, list []string) {
	if len(list) == 0 {
		delete(doc, key)
		return
	}
	doc[key], _ = json.Marshal(list)
}

func writeClaudeLocalSettings(file string, doc map[string]json.RawMessage) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(file); err == nil {
		mode = info.Mode().Perm()
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".settings.local-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}

func containsName(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

func withoutNames(list, drop []string) []string {
	var out []string
	for _, n := range list {
		if !containsName(drop, n) {
			out = append(out, n)
		}
	}
	return out
}

// claudeMCPChoose records "approve" or "disable" for every server Claude Code
// would load in dir. Result.Added and Restored hold exactly the changes made,
// so an undo cannot remove a choice the owner made.
func claudeMCPChoose(dir, choice string) (protocol.MCPChoiceResult, error) {
	res := protocol.MCPChoiceResult{Choice: choice, Path: dir, File: claudeLocalSettingsFile(dir)}
	if choice != protocol.MCPApprove && choice != protocol.MCPDisable {
		return res, fmt.Errorf("mcp_servers must be %q or %q", protocol.MCPApprove, protocol.MCPDisable)
	}
	res.Servers = claudeMCPServers(dir)
	if len(res.Servers) == 0 {
		return res, fmt.Errorf("no servers in a .mcp.json file at or above %s", dir)
	}
	doc, err := readClaudeLocalSettings(res.File)
	if err != nil {
		return res, err
	}
	enabled, err := claudeSettingsList(doc, claudeMCPEnabledKey)
	if err != nil {
		return res, err
	}
	disabled, err := claudeSettingsList(doc, claudeMCPDisabledKey)
	if err != nil {
		return res, err
	}
	target, opposite := &enabled, &disabled
	if choice == protocol.MCPDisable {
		target, opposite = &disabled, &enabled
	}
	names := make([]string, 0, len(res.Servers))
	for _, s := range res.Servers {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !containsName(*target, name) {
			*target = append(*target, name)
			res.Added = append(res.Added, name)
		}
		// A refusal wins over an approval, so an approval takes it away.
		// A refusal leaves approvals alone: it already wins.
		if choice == protocol.MCPApprove && containsName(*opposite, name) {
			*opposite = withoutNames(*opposite, []string{name})
			res.Restored = append(res.Restored, name)
		}
	}
	if len(res.Added) == 0 && len(res.Restored) == 0 {
		return res, nil
	}
	setClaudeSettingsList(doc, claudeMCPEnabledKey, enabled)
	setClaudeSettingsList(doc, claudeMCPDisabledKey, disabled)
	return res, writeClaudeLocalSettings(res.File, doc)
}

// claudeMCPUndo takes back exactly what one choice added.
func claudeMCPUndo(dir string, undo protocol.MCPUndo) error {
	if undo.Choice != protocol.MCPApprove && undo.Choice != protocol.MCPDisable {
		return fmt.Errorf("mcp_servers must be %q or %q", protocol.MCPApprove, protocol.MCPDisable)
	}
	file := claudeLocalSettingsFile(dir)
	if _, err := os.Stat(file); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	doc, err := readClaudeLocalSettings(file)
	if err != nil {
		return err
	}
	enabled, err := claudeSettingsList(doc, claudeMCPEnabledKey)
	if err != nil {
		return err
	}
	disabled, err := claudeSettingsList(doc, claudeMCPDisabledKey)
	if err != nil {
		return err
	}
	if undo.Choice == protocol.MCPApprove {
		enabled = withoutNames(enabled, undo.Added)
		for _, name := range undo.Restored {
			if !containsName(disabled, name) {
				disabled = append(disabled, name)
			}
		}
	} else {
		disabled = withoutNames(disabled, undo.Added)
	}
	setClaudeSettingsList(doc, claudeMCPEnabledKey, enabled)
	setClaudeSettingsList(doc, claudeMCPDisabledKey, disabled)
	return writeClaudeLocalSettings(file, doc)
}

// claudeMCPApprovalDetail tells whether a Claude Code job detail is the wait
// for MCP server approval ("1 new MCP server needs approval").
func claudeMCPApprovalDetail(detail string) bool {
	lower := strings.ToLower(detail)
	return strings.Contains(lower, "mcp server") && strings.Contains(lower, "approval")
}

// claudeMCPApprovalError is a start that cannot finish because Claude Code
// waits for the user to approve the MCP servers of a .mcp.json file. The
// background job is blocked on that prompt and nobody can answer it.
type claudeMCPApprovalError struct {
	JobID   string
	Alive   bool
	Path    string
	Detail  string
	Servers []protocol.MCPServerInfo
}

func (e *claudeMCPApprovalError) Error() string {
	var list []string
	files := map[string]bool{}
	var fileList []string
	for _, s := range e.Servers {
		item := s.Name
		if s.Command != "" {
			item += " (" + s.Command + ")"
		}
		list = append(list, item)
		if !files[s.File] {
			files[s.File] = true
			fileList = append(fileList, s.File)
		}
	}
	out := protocol.StartErrorMCPApprovalNeeded + ": Claude Code is waiting for approval of an MCP server before it starts the session in " + e.Path
	if e.Detail != "" {
		out += " (" + e.Detail + ")"
	}
	if len(list) > 0 {
		out += ". Servers: " + strings.Join(list, "; ") + ". From: " + strings.Join(fileList, ", ")
	}
	if e.Alive {
		out += ". The Claude job " + e.JobID + " is still running, blocked on that prompt"
	} else {
		out += ". The Claude job " + e.JobID + " has stopped"
	}
	return out + ". Retry session_start with mcp_servers approve or disable, or use session_change action mcp_servers"
}

// claudeJobMCPBlock reads the job state and returns the approval error when the
// job is blocked on the MCP approval prompt.
func (d *Daemon) claudeJobMCPBlock(jobID, path string) *claudeMCPApprovalError {
	state, detail := claudeCTOJobState(d.cfg.ClaudeSessionsDir, jobID)
	if state != "blocked" || !claudeMCPApprovalDetail(detail) {
		return nil
	}
	all := claudeMCPServers(path)
	var pending []protocol.MCPServerInfo
	if doc, err := readClaudeLocalSettings(claudeLocalSettingsFile(path)); err == nil {
		enabled, _ := claudeSettingsList(doc, claudeMCPEnabledKey)
		disabled, _ := claudeSettingsList(doc, claudeMCPDisabledKey)
		for _, s := range all {
			if !containsName(enabled, s.Name) && !containsName(disabled, s.Name) {
				pending = append(pending, s)
			}
		}
	}
	if len(pending) == 0 {
		pending = all
	}
	return &claudeMCPApprovalError{JobID: jobID, Alive: !claudeJobEnded(state), Path: path, Detail: detail, Servers: pending}
}

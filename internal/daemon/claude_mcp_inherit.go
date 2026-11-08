package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// The answer to "new MCP server needs approval" is stored per launch folder, and
// a git worktree is a new folder. A Claude worker in a worktree of a project with
// a .mcp.json would stop on the prompt, and a background job cannot answer it.
// Before the launch, the daemon copies what the owner decided for the main
// checkout into the worktree (decision 0095).

const claudeEnableAllKey = "enableAllProjectMcpServers"

// claudeMainChoice is the owner's MCP choice for a folder, as Claude Code
// resolves it: the lists of every settings.local.json from the folder up to the
// root are joined, and the nearest file that has enableAllProjectMcpServers
// decides that switch.
type claudeMainChoice struct {
	Enabled, Disabled []string
	EnableAll         bool
}

func claudeMainMCPChoice(dir string) claudeMainChoice {
	var out claudeMainChoice
	decided := false
	for cur := filepath.Clean(dir); ; {
		doc, err := readClaudeLocalSettings(claudeLocalSettingsFile(cur))
		if err == nil {
			enabled, _ := claudeSettingsList(doc, claudeMCPEnabledKey)
			disabled, _ := claudeSettingsList(doc, claudeMCPDisabledKey)
			for _, n := range enabled {
				if !containsName(out.Enabled, n) {
					out.Enabled = append(out.Enabled, n)
				}
			}
			for _, n := range disabled {
				if !containsName(out.Disabled, n) {
					out.Disabled = append(out.Disabled, n)
				}
			}
			if raw, ok := doc[claudeEnableAllKey]; ok && !decided {
				var on bool
				if json.Unmarshal(raw, &on) == nil {
					out.EnableAll, decided = on, true
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

// claudeMCPInherited says what a copy did. Enabled and Disabled hold the names
// this call added to the worktree. Pending holds the servers that still have no
// answer, which would stop a background job on the prompt.
type claudeMCPInherited struct {
	Enabled, Disabled, Pending []string
	Changed                    bool
}

// claudeMCPInherit copies the MCP choice of the main checkout folder into the
// worktree's .claude/settings.local.json. It writes only names that the worktree's
// .mcp.json servers have, never replaces an answer the file already has, and
// never sets enableAllProjectMcpServers: that switch becomes the names of the
// servers found now, so a server added later still asks. A refusal wins over
// an approval. With no answer for a server, nothing is written for it, and the
// start fails at once if Claude Code blocks on it.
func claudeMCPInherit(main, worktree string) (claudeMCPInherited, error) {
	var res claudeMCPInherited
	servers := claudeMCPServers(worktree)
	if len(servers) == 0 {
		return res, nil
	}
	names := make([]string, 0, len(servers))
	for _, s := range servers {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	file := claudeLocalSettingsFile(worktree)
	doc, err := readClaudeLocalSettings(file)
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
	pending := func() []string {
		var out []string
		for _, n := range names {
			if !containsName(enabled, n) && !containsName(disabled, n) {
				out = append(out, n)
			}
		}
		return out
	}
	if claudeFileTracked(worktree, file) {
		// The project keeps its own answers in git. Claude Code reads them as they are.
		res.Pending = pending()
		return res, nil
	}
	choice := claudeMainMCPChoice(main)
	for _, n := range names {
		if containsName(enabled, n) || containsName(disabled, n) {
			continue
		}
		switch {
		case containsName(choice.Disabled, n):
			disabled = append(disabled, n)
			res.Disabled = append(res.Disabled, n)
		case containsName(choice.Enabled, n) || choice.EnableAll:
			enabled = append(enabled, n)
			res.Enabled = append(res.Enabled, n)
		}
	}
	res.Pending = pending()
	if len(res.Enabled) == 0 && len(res.Disabled) == 0 {
		return res, nil
	}
	setClaudeSettingsList(doc, claudeMCPEnabledKey, enabled)
	setClaudeSettingsList(doc, claudeMCPDisabledKey, disabled)
	if err := writeClaudeLocalSettings(file, doc); err != nil {
		return res, err
	}
	res.Changed = true
	return res, ensureClaudeSettingsIgnored(worktree)
}

// claudeFileTracked tells whether git tracks the file in the worktree.
func claudeFileTracked(worktree, file string) bool {
	rel, err := filepath.Rel(worktree, file)
	if err != nil {
		return false
	}
	_, err = gitOut(context.Background(), worktree, "ls-files", "--error-unmatch", "--", filepath.ToSlash(rel))
	return err == nil
}

// ensureClaudeSettingsIgnored keeps .claude/settings.local.json out of the
// worktree's git status. Claude Code adds the file to the user's global ignore
// list and many repositories ignore it; when neither does, the pattern goes to
// info/exclude. Git keeps one info/exclude for the main checkout and all its
// worktrees, so the line is written once and also covers the main checkout.
func ensureClaudeSettingsIgnored(worktree string) error {
	const rel = ".claude/settings.local.json"
	ctx := context.Background()
	if _, err := os.Stat(filepath.Join(worktree, filepath.FromSlash(rel))); err != nil {
		return nil
	}
	if _, err := gitOut(ctx, worktree, "rev-parse", "--git-dir"); err != nil {
		return nil // not a git folder: nothing to keep clean
	}
	if claudeFileTracked(worktree, filepath.Join(worktree, filepath.FromSlash(rel))) {
		return nil
	}
	if _, err := gitOut(ctx, worktree, "check-ignore", "-q", "--", rel); err == nil {
		return nil
	}
	path, err := gitOut(ctx, worktree, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	path = strings.TrimSpace(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(worktree, path)
	}
	line := "/" + rel
	if raw, err := os.ReadFile(path); err == nil {
		for _, l := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(l) == line {
				return nil
			}
		}
		if len(raw) > 0 && !strings.HasSuffix(string(raw), "\n") {
			line = "\n" + line
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

// prepareClaudeMCP makes the MCP answers ready for a Claude launch in dir. In a
// worktree of main it first copies the owner's answers for the main checkout; an
// explicit choice (approve or disable) then applies on top. The copy is best
// effort: when it fails, the launch goes on and a blocked job fails the start
// at once. A failed explicit choice is an error, because the caller asked.
func (d *Daemon) prepareClaudeMCP(main, dir, choice string) (*protocol.MCPChoiceResult, error) {
	if main != "" && filepath.Clean(main) != filepath.Clean(dir) {
		res, err := claudeMCPInherit(main, dir)
		switch {
		case err != nil:
			d.Logf("claude mcp: cannot copy the choice of %s to %s: %v", main, dir, err)
		case res.Changed:
			d.Logf("claude mcp: copied the choice of %s to %s: enabled %v, disabled %v, no answer %v", main, dir, res.Enabled, res.Disabled, res.Pending)
		}
	}
	var chosen *protocol.MCPChoiceResult
	if choice != "" {
		res, err := claudeMCPChoose(dir, choice)
		if err != nil {
			return nil, fmt.Errorf("cannot record the MCP server choice: %w", err)
		}
		chosen = &res
	}
	if main != "" && filepath.Clean(main) != filepath.Clean(dir) {
		if err := ensureClaudeSettingsIgnored(dir); err != nil {
			d.Logf("claude mcp: cannot keep %s out of git status: %v", claudeLocalSettingsFile(dir), err)
		}
	}
	return chosen, nil
}

// claudeStartChoiceValid checks the mcp_servers value of a worker start.
func claudeStartChoiceValid(choice string) error {
	if choice != "" && choice != protocol.MCPApprove && choice != protocol.MCPDisable {
		return fmt.Errorf("mcp_servers must be %q or %q", protocol.MCPApprove, protocol.MCPDisable)
	}
	return nil
}

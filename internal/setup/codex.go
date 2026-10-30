package setup

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/agentfiles"
	"github.com/niparis/agent-net/internal/daemon"
)

// Values of Options.CodexTools.
const (
	CodexToolsApprove = "approve" // --accept-codex-tools
	CodexToolsNo      = "no"      // --no-accept-codex-tools
)

const (
	codexStepName = "codex mcp"
	codexBegin    = "# agent-net:begin"
	codexEnd      = "# agent-net:end"
)

// codexMCP registers `agent-net mcp --codex` as an MCP server of Codex, as a
// marked block at the end of Codex's config.toml (decision 0012, C3). Codex
// has no command that sets a server's approval mode, and the file is TOML, so
// agent-net owns one block and leaves the rest of the file alone.

// lookPath finds a program on PATH. Tests replace it.
var lookPath = exec.LookPath

// codexPath finds the codex binary, or "".
func codexPath() string {
	if p, err := lookPath("codex"); err == nil {
		return p
	}
	h := home()
	for _, p := range []string{
		filepath.Join(h, ".local", "bin", "codex"),
		"/opt/homebrew/bin/codex",
		"/usr/local/bin/codex",
	} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

type codexState struct {
	Asked   bool `json:"asked"`   // the owner has answered once
	Approve bool `json:"approve"` // the owner let Codex call the tools without asking
}

func codexStatePath() string { return filepath.Join(daemon.ConfigDir(), "codex-tools.json") }

func loadCodexState() codexState {
	var st codexState
	if b, err := os.ReadFile(codexStatePath()); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

func saveCodexState(st codexState) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return atomicWrite(codexStatePath(), append(b, '\n'), 0o600)
}

// tomlString quotes s as a TOML string.
func tomlString(s string) string {
	if !strings.ContainsAny(s, "'\n\r") {
		return "'" + s + "'" // a literal string: no escapes
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)
	return `"` + r.Replace(s) + `"`
}

// codexBlock is the text setup owns in config.toml.
func codexBlock(exe string, approve bool) string {
	var b strings.Builder
	b.WriteString(codexBegin + "\n")
	b.WriteString("[mcp_servers.agent-net]\n")
	b.WriteString("command = " + tomlString(exe) + "\n")
	b.WriteString("args = [\"mcp\", \"--codex\"]\n")
	if approve {
		b.WriteString("default_tools_approval_mode = \"approve\"\n")
	}
	b.WriteString(codexEnd + "\n")
	return b.String()
}

// findCodexBlock returns the byte range of the marked block, to just after the
// end line.
func findCodexBlock(s string) (b, e int, ok bool) {
	b = strings.Index(s, codexBegin)
	if b < 0 {
		return 0, 0, false
	}
	i := strings.Index(s[b:], codexEnd)
	if i < 0 {
		return 0, 0, false
	}
	e = b + i + len(codexEnd)
	if e < len(s) && s[e] == '\n' {
		e++
	}
	return b, e, true
}

// definesAgentNet says whether config.toml already has an agent-net MCP
// server outside our block (added by hand, or by `codex mcp add`).
func definesAgentNet(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		l := strings.ReplaceAll(strings.TrimSpace(line), " ", "")
		if strings.HasPrefix(l, "[mcp_servers.agent-net") || strings.HasPrefix(l, `[mcp_servers."agent-net"`) ||
			strings.HasPrefix(l, "[mcp_servers.'agent-net'") {
			return true
		}
	}
	return false
}

func (r *runner) codexMCP() {
	if codexPath() == "" && !exists(agentfiles.CodexHome()) {
		r.step(codexStepName, "skipped (codex not found)", nil)
		return
	}
	path := agentfiles.CodexConfigPath()
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		r.step(codexStepName, "", err)
		return
	}
	text := string(old)
	b, e, have := findCodexBlock(text)
	rest := text
	if have {
		rest = text[:b] + text[e:]
	}
	if definesAgentNet(rest) {
		r.step(codexStepName, "skipped (agent-net is already defined in "+displayPath(path)+", left as is)", nil)
		return
	}
	st := loadCodexState()
	approve := st.Approve
	switch {
	case r.want != nil:
		approve = r.want.CodexTools
	case r.CodexTools == CodexToolsApprove:
		approve = true
	case r.CodexTools == CodexToolsNo:
		approve = false
	case st.Asked:
	case have:
		approve = strings.Contains(text[b:e], "default_tools_approval_mode")
	default:
		ans, ok := r.askCodexTools()
		if !ok {
			r.hint("no terminal to ask. Codex will ask before each agent-net tool call. To let it call them: agent-net setup --accept-codex-tools")
		}
		approve = ans && ok
		if ok {
			st = codexState{Asked: true, Approve: approve}
			if err := saveCodexState(st); err != nil {
				r.step(codexStepName, "", err)
				return
			}
		}
	}
	if r.CodexTools != "" || r.want != nil {
		if err := saveCodexState(codexState{Asked: true, Approve: approve}); err != nil {
			r.step(codexStepName, "", err)
			return
		}
	}
	block := codexBlock(r.Exe, approve)
	var out string
	if have {
		out = text[:b] + block + text[e:]
	} else {
		out = text
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		if out != "" {
			out += "\n"
		}
		out += block
	}
	mode := "asks before each call"
	if approve {
		mode = "calls need no approval"
	}
	if out == text {
		r.step(codexStepName, "ok (unchanged; "+mode+")", nil)
	} else {
		if err := atomicWrite(path, []byte(out), 0o600); err != nil {
			r.step(codexStepName, "", err)
			return
		}
		res := "ok"
		if have {
			res = "updated"
		}
		r.step(codexStepName, res+" ("+mode+")", nil)
	}
	r.hint("hint: Codex threads that were open before this need to be started again to get the agent-net tools")
}

// askCodexTools asks on the controlling terminal. ok is false when there is no
// terminal.
func (r *runner) askCodexTools() (yes, ok bool) {
	tty, err := openTTY()
	if err != nil {
		return false, false
	}
	defer tty.Close()
	fmt.Fprintln(r.Out, "Codex asks before it calls an MCP tool, and refuses the call when its approval")
	fmt.Fprintln(r.Out, "policy is \"never\". agent-net can set default_tools_approval_mode = \"approve\" for")
	fmt.Fprintln(r.Out, "its own server only, so Codex calls the agent-net tools (list, send, reply, start")
	fmt.Fprintln(r.Out, "workers) without asking. Messages from other agents still never count as your approval.")
	fmt.Fprint(r.Out, "Let Codex call agent-net tools without asking each time? [y/N] ")
	line, _ := bufio.NewReader(tty).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, true
	}
	return false, true
}

// removeCodexMCP takes the block out of config.toml.
func (r *runner) removeCodexMCP() {
	path := agentfiles.CodexConfigPath()
	old, err := os.ReadFile(path)
	if err != nil {
		r.step(codexStepName, "skipped (no config)", nil)
		return
	}
	text := string(old)
	b, e, ok := findCodexBlock(text)
	if !ok {
		r.step(codexStepName, "skipped (no agent-net block)", nil)
		return
	}
	head, tail := strings.TrimRight(text[:b], "\n"), strings.TrimLeft(text[e:], "\n")
	out := head
	if head != "" {
		out += "\n"
		if tail != "" {
			out += "\n"
		}
	}
	out += tail
	if err := atomicWrite(path, []byte(out), 0o600); err != nil {
		r.step(codexStepName, "", err)
		return
	}
	os.Remove(codexStatePath())
	r.step(codexStepName, "ok (removed)", nil)
}

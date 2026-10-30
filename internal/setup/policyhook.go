package setup

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/niparis/agent-net/internal/claudeagents"
	"github.com/niparis/agent-net/internal/daemon"
)

// The Claude Code delegation policy hook (decision 0014). On each prompt,
// Claude Code runs `agent-net policy-hook`. When the owner set a delegation
// policy for the session in agent-net, the hook adds the policy as context
// for that turn. Setup adds it to ~/.claude/settings.json only with the
// owner's consent, and uninstall removes it, with the subagent files that the
// daemon wrote for the policy.

// Values of Options.PolicyHook.
const (
	PolicyHookYes = "yes" // --claude-policy-hook
	PolicyHookNo  = "no"  // --no-claude-policy-hook
)

const policyStepName = "claude policy hook"

// policyTimeoutSeconds is the hook timeout in seconds. The command itself
// gives up after about 700 ms.
const policyTimeoutSeconds = 3

// policyEvents: UserPromptSubmit has no matcher (it fires on every prompt).
var policyEvents = []struct{ event, matcher string }{{"UserPromptSubmit", ""}}

type policyState struct {
	Asked     bool   `json:"asked"`
	Installed bool   `json:"installed"` // agent-net added the hook
	Settings  string `json:"settings_path,omitempty"`
}

func loadPolicyState() policyState {
	var st policyState
	if b, err := os.ReadFile(daemon.PolicyHookStatePath()); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

func savePolicyState(st policyState) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return atomicWrite(daemon.PolicyHookStatePath(), append(b, '\n'), 0o600)
}

func policyCommand(exe string) string {
	if strings.ContainsAny(exe, " \t'\"") {
		return `"` + strings.ReplaceAll(exe, `"`, `\"`) + `" policy-hook`
	}
	return exe + " policy-hook"
}

func isPolicyCommand(cmd string) bool { return isOurCommand(cmd, "policy-hook") }

func (r *runner) claudePolicyHook() {
	if claudePath() == "" {
		r.step(policyStepName, "skipped (claude not found)", nil)
		return
	}
	if r.want != nil && !r.want.PolicyHook {
		if !loadPolicyState().Installed {
			r.step(policyStepName, "off", nil)
			return
		}
		r.removePolicyHook()
		return
	}
	path := claudeSettingsPath()
	doc, err := readSettings(path)
	if err != nil {
		if errors.Is(err, errNotJSON) {
			r.step(policyStepName, "skipped ("+displayPath(path)+" is not valid JSON, left untouched)", nil)
		} else {
			r.step(policyStepName, "", err)
		}
		return
	}
	st := loadPolicyState()
	install := false
	switch {
	case r.want != nil:
		install = r.want.PolicyHook
	case r.PolicyHook == PolicyHookYes:
		install = true
	case r.PolicyHook == PolicyHookNo:
	case st.Installed:
		install = true // the owner agreed before: keep the command current
	case st.Asked:
	default:
		ans, ok := r.askPolicyHook()
		if !ok {
			r.step(policyStepName, "off (default)", nil)
			r.hint("no terminal to ask. To turn it on later: agent-net setup --claude-policy-hook")
			return
		}
		install = ans
	}
	if !install {
		if !st.Asked {
			if err := savePolicyState(policyState{Asked: true}); err != nil {
				r.step(policyStepName, "", err)
				return
			}
		}
		r.step(policyStepName, "off", nil)
		return
	}
	changed, err := withHooks(doc, policyCommand(r.Exe), isPolicyCommand, policyEvents, policyTimeoutSeconds)
	if err != nil {
		r.step(policyStepName, "", err)
		return
	}
	if changed {
		if err := atomicWrite(path, doc.encode(), 0o644); err != nil {
			r.step(policyStepName, "", err)
			return
		}
	}
	if err := savePolicyState(policyState{Asked: true, Installed: true, Settings: path}); err != nil {
		r.step(policyStepName, "", err)
		return
	}
	r.step(policyStepName, "on (hook in "+displayPath(path)+")", nil)
}

// askPolicyHook asks on the controlling terminal. ok is false when there is no
// terminal.
func (r *runner) askPolicyHook() (yes, ok bool) {
	tty, err := openTTY()
	if err != nil {
		return false, false
	}
	defer tty.Close()
	fmt.Fprintln(r.Out, "agent-net can add a second Claude Code hook to ~/.claude/settings.json. On each")
	fmt.Fprintln(r.Out, "prompt it asks agent-net for the delegation policy that you set for the session")
	fmt.Fprintln(r.Out, "in the TUI (for example: hand code changes to a subagent or a worker) and the")
	fmt.Fprintln(r.Out, "charter of the session's node, and adds them as context. When you set a policy")
	fmt.Fprintln(r.Out, "that names a subagent, agent-net also writes a marked file in ~/.claude/agents/.")
	fmt.Fprintln(r.Out, "Uninstall removes both. No model is called.")
	fmt.Fprint(r.Out, "Add the hook? [y/N] ")
	line, _ := bufio.NewReader(tty).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, true
	}
	return false, true
}

func (r *runner) removePolicyHook() {
	// The subagent files are removed whether or not the hook state is there:
	// they carry a marker, and nothing else is touched.
	gone, ferr := claudeagents.RemoveAll()
	st := loadPolicyState()
	if !st.Installed {
		if len(gone) > 0 {
			r.step(policyStepName, "ok (removed "+fmt.Sprint(len(gone))+" subagent files)", nil)
		} else if ferr != nil {
			r.step(policyStepName, "", ferr)
		} else {
			r.step(policyStepName, "skipped (not added by agent-net)", nil)
		}
		return
	}
	path := st.Settings
	if path == "" {
		path = claudeSettingsPath()
	}
	doc, err := readSettings(path)
	if err != nil {
		if errors.Is(err, errNotJSON) {
			r.step(policyStepName, "skipped ("+displayPath(path)+" is not valid JSON, left untouched)", nil)
		} else {
			r.step(policyStepName, "", err)
		}
		return
	}
	changed, err := withoutHooks(doc, isPolicyCommand)
	if err != nil {
		r.step(policyStepName, "", err)
		return
	}
	if changed {
		if err := atomicWrite(path, doc.encode(), 0o644); err != nil {
			r.step(policyStepName, "", err)
			return
		}
	}
	os.Remove(daemon.PolicyHookStatePath())
	switch {
	case ferr != nil:
		r.step(policyStepName, "", ferr)
	case changed:
		r.step(policyStepName, fmt.Sprintf("ok (removed the hook and %d subagent files)", len(gone)), nil)
	default:
		r.step(policyStepName, "skipped (hook already gone)", nil)
	}
}

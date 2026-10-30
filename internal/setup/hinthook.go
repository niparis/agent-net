package setup

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/daemon"
)

// The Claude Code "who can help" hook (decision 0012). After a failed tool
// call, Claude Code runs `agent-net hint`. When another online session may own
// the failing service, the hook adds one line for Claude: ask that session.
// Setup adds it to ~/.claude/settings.json only with the owner's consent, and
// uninstall removes it.

// Values of Options.HintHook.
const (
	HintHookYes = "yes" // --claude-hint-hook
	HintHookNo  = "no"  // --no-claude-hint-hook
)

const hintStepName = "claude hint hook"

// hintTimeoutSeconds is the hook timeout in seconds (a command hook's timeout
// is in seconds). The command itself gives up after about 300 ms.
const hintTimeoutSeconds = 2

// hintEvents are the hook events and their matchers. PostToolUseFailure fires
// when any tool call fails. PostToolUse fires for calls that succeed, and only
// shell commands are looked at there, for a command that printed an error and
// still exited 0 (curl without --fail).
var hintEvents = []struct{ event, matcher string }{
	{"PostToolUseFailure", ""},
	{"PostToolUse", "Bash"},
}

type hintState struct {
	Asked     bool   `json:"asked"`
	Installed bool   `json:"installed"` // agent-net added the hook
	Settings  string `json:"settings_path,omitempty"`
}

func hintStatePath() string { return filepath.Join(daemon.ConfigDir(), "claude-hint-hook.json") }

func loadHintState() hintState {
	var st hintState
	if b, err := os.ReadFile(hintStatePath()); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

func saveHintState(st hintState) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return atomicWrite(hintStatePath(), append(b, '\n'), 0o600)
}

// hintCommand is the command line of the hook for this binary.
func hintCommand(exe string) string {
	if strings.ContainsAny(exe, " \t'\"") {
		return `"` + strings.ReplaceAll(exe, `"`, `\"`) + `" hint`
	}
	return exe + " hint"
}

// isHintCommand tells whether a hook command is agent-net's own hint hook,
// whatever the path of the binary was when it was added.
func isHintCommand(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if !strings.HasSuffix(cmd, " hint") {
		return false
	}
	exe := strings.Trim(strings.TrimSpace(strings.TrimSuffix(cmd, " hint")), `"'`)
	return strings.HasPrefix(filepath.Base(exe), "agent-net")
}

// ---- the hooks object ----

type hookGroup map[string]json.RawMessage

// hooksOf returns the "hooks" member as ordered events, each a list of groups.
func hooksOf(doc *settingsDoc) (*settingsDoc, error) {
	i := doc.indexOf("hooks")
	if i < 0 {
		return &settingsDoc{}, nil
	}
	return parseObject(doc.members[i].val)
}

func groupsOf(raw json.RawMessage) ([]json.RawMessage, error) {
	var gs []json.RawMessage
	if err := json.Unmarshal(raw, &gs); err != nil {
		return nil, err
	}
	return gs, nil
}

// groupIsOurs reports whether every hook in the group is ours, and whether
// any is. A group with no hooks is nobody's.
func groupHasHint(g json.RawMessage) (all, some bool, hooks []json.RawMessage) {
	var m hookGroup
	if json.Unmarshal(g, &m) != nil {
		return false, false, nil
	}
	if json.Unmarshal(m["hooks"], &hooks) != nil || len(hooks) == 0 {
		return false, false, nil
	}
	all = true
	for _, h := range hooks {
		var c struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(h, &c) == nil && isHintCommand(c.Command) {
			some = true
		} else {
			all = false
		}
	}
	return all, some, hooks
}

func setMember(d *settingsDoc, key string, val json.RawMessage) {
	if i := d.indexOf(key); i >= 0 {
		d.members[i].val = val
		return
	}
	d.members = append(d.members, member{key, val})
}

func dropMember(d *settingsDoc, key string) {
	if i := d.indexOf(key); i >= 0 {
		d.members = append(d.members[:i], d.members[i+1:]...)
	}
}

// encodeObject writes members as a JSON object (compact; the settings file is
// indented as a whole).
func encodeObject(d *settingsDoc) json.RawMessage {
	var sb strings.Builder
	sb.WriteString("{")
	for i, m := range d.members {
		if i > 0 {
			sb.WriteString(",")
		}
		k, _ := json.Marshal(m.key)
		sb.Write(k)
		sb.WriteString(":")
		sb.Write(m.val)
	}
	sb.WriteString("}")
	return json.RawMessage(sb.String())
}

// withoutHint removes our hooks from the settings document. changed says
// whether anything was removed.
func withoutHint(doc *settingsDoc) (changed bool, err error) {
	hooks, err := hooksOf(doc)
	if err != nil {
		return false, err
	}
	for i := 0; i < len(hooks.members); i++ {
		m := hooks.members[i]
		gs, err := groupsOf(m.val)
		if err != nil {
			continue // not a list: not ours, leave it
		}
		var keep []json.RawMessage
		touched := false
		for _, g := range gs {
			all, some, hs := groupHasHint(g)
			switch {
			case all:
				touched = true
			case some:
				// Mixed group: take out only our entries.
				var gm hookGroup
				json.Unmarshal(g, &gm)
				var rest []json.RawMessage
				for _, h := range hs {
					var c struct {
						Command string `json:"command"`
					}
					if json.Unmarshal(h, &c) == nil && isHintCommand(c.Command) {
						continue
					}
					rest = append(rest, h)
				}
				gm["hooks"], _ = json.Marshal(rest)
				ng, _ := json.Marshal(gm)
				keep = append(keep, ng)
				touched = true
			default:
				keep = append(keep, g)
			}
		}
		if !touched {
			continue
		}
		changed = true
		if len(keep) == 0 {
			hooks.members = append(hooks.members[:i], hooks.members[i+1:]...)
			i--
			continue
		}
		hooks.members[i].val, _ = json.Marshal(keep)
	}
	if !changed {
		return false, nil
	}
	if len(hooks.members) == 0 {
		dropMember(doc, "hooks")
	} else {
		setMember(doc, "hooks", encodeObject(hooks))
	}
	return true, nil
}

// withHint adds our hooks (after removing an older copy of them, so a moved
// binary is updated). changed says whether the document differs.
func withHint(doc *settingsDoc, cmd string) (changed bool, err error) {
	before := string(doc.encode())
	if _, err := withoutHint(doc); err != nil {
		return false, err
	}
	hooks, err := hooksOf(doc)
	if err != nil {
		return false, err
	}
	for _, ev := range hintEvents {
		entry := map[string]any{"type": "command", "command": cmd, "timeout": hintTimeoutSeconds}
		group := map[string]any{"hooks": []any{entry}}
		if ev.matcher != "" {
			group = map[string]any{"matcher": ev.matcher, "hooks": []any{entry}}
		}
		var gs []json.RawMessage
		if i := hooks.indexOf(ev.event); i >= 0 {
			if gs, err = groupsOf(hooks.members[i].val); err != nil {
				return false, fmt.Errorf("hooks.%s in settings is not a list", ev.event)
			}
		}
		gb, _ := json.Marshal(group)
		gs = append(gs, gb)
		raw, _ := json.Marshal(gs)
		setMember(hooks, ev.event, raw)
	}
	setMember(doc, "hooks", encodeObject(hooks))
	return string(doc.encode()) != before, nil
}

// ---- setup step ----

func (r *runner) claudeHintHook() {
	if claudePath() == "" {
		r.step(hintStepName, "skipped (claude not found)", nil)
		return
	}
	path := claudeSettingsPath()
	doc, err := readSettings(path)
	if err != nil {
		if errors.Is(err, errNotJSON) {
			r.step(hintStepName, "skipped ("+displayPath(path)+" is not valid JSON, left untouched)", nil)
		} else {
			r.step(hintStepName, "", err)
		}
		return
	}
	st := loadHintState()
	install := false
	switch {
	case r.HintHook == HintHookYes:
		install = true
	case r.HintHook == HintHookNo:
	case st.Installed:
		install = true // the owner agreed before: keep the command current
	case st.Asked:
	default:
		ans, ok := r.askHintHook()
		if !ok {
			r.step(hintStepName, "off (default)", nil)
			r.hint("no terminal to ask. To turn it on later: agent-net setup --claude-hint-hook")
			return
		}
		install = ans
	}
	if !install {
		if !st.Asked {
			if err := saveHintState(hintState{Asked: true}); err != nil {
				r.step(hintStepName, "", err)
				return
			}
		}
		r.step(hintStepName, "off", nil)
		return
	}
	changed, err := withHint(doc, hintCommand(r.Exe))
	if err != nil {
		r.step(hintStepName, "", err)
		return
	}
	if changed {
		if err := atomicWrite(path, doc.encode(), 0o644); err != nil {
			r.step(hintStepName, "", err)
			return
		}
	}
	if err := saveHintState(hintState{Asked: true, Installed: true, Settings: path}); err != nil {
		r.step(hintStepName, "", err)
		return
	}
	r.step(hintStepName, "on (hooks in "+displayPath(path)+")", nil)
}

// askHintHook asks on the controlling terminal. ok is false when there is no
// terminal.
func (r *runner) askHintHook() (yes, ok bool) {
	tty, err := openTTY()
	if err != nil {
		return false, false
	}
	defer tty.Close()
	fmt.Fprintln(r.Out, "agent-net can add a Claude Code hook to ~/.claude/settings.json. After a failed")
	fmt.Fprintln(r.Out, "tool call it checks the error text on this machine against your other online")
	fmt.Fprintln(r.Out, "sessions (project, host, task, expertise). If one of them may own the failing")
	fmt.Fprintln(r.Out, "service, Claude gets one line telling it to ask that session. No model is called.")
	fmt.Fprint(r.Out, "Add the hook? [y/N] ")
	line, _ := bufio.NewReader(tty).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, true
	}
	return false, true
}

// ---- uninstall step ----

func (r *runner) removeHintHook() {
	st := loadHintState()
	if !st.Installed {
		r.step(hintStepName, "skipped (not added by agent-net)", nil)
		return
	}
	path := st.Settings
	if path == "" {
		path = claudeSettingsPath()
	}
	doc, err := readSettings(path)
	if err != nil {
		if errors.Is(err, errNotJSON) {
			r.step(hintStepName, "skipped ("+displayPath(path)+" is not valid JSON, left untouched)", nil)
		} else {
			r.step(hintStepName, "", err)
		}
		return
	}
	changed, err := withoutHint(doc)
	if err != nil {
		r.step(hintStepName, "", err)
		return
	}
	if changed {
		if err := atomicWrite(path, doc.encode(), 0o644); err != nil {
			r.step(hintStepName, "", err)
			return
		}
	}
	os.Remove(hintStatePath())
	if changed {
		r.step(hintStepName, "ok (removed the hooks)", nil)
	} else {
		r.step(hintStepName, "skipped (hooks already gone)", nil)
	}
}

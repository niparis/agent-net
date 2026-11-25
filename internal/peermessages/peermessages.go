// Package peermessages checks the Claude Code setting that decides whether a
// message from another session is held for review. Setup writes the setting
// once; the daemon checks the live file at start and when a Claude session
// starts, because a managed or replaced settings file can lose the key.
package peermessages

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/niparis/agent-net/internal/claudesettings"
)

// SettingKey is the Claude Code user setting. "accept" delivers messages from
// another session without review; a missing value or "hold" keeps them for
// review when the permission mode classes differ.
const SettingKey = "crossSessionInbound"

// State is agent-net's record of the owner's answer, as setup writes it to
// ~/.config/agent-net/claude-peer-messages.json.
type State struct {
	Asked         bool   `json:"asked"`
	SetByAgentNet bool   `json:"set_by_agent_net"`
	SettingsPath  string `json:"settings_path,omitempty"`
}

// LoadState reads the record. A missing or unreadable file is the zero state.
func LoadState(path string) State {
	var st State
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

// Report is what the live settings file says about the recorded answer.
type Report struct {
	SettingsPath string
	Live         string
	LiveSet      bool
	Linked       bool
	ReadOnly     bool
	// Problem is empty when the live file agrees with the record.
	Problem string
}

// Compare checks the live setting against the recorded answer. It only reads
// files. A difference is a problem only when agent-net recorded that it set
// the key: then Claude Code holds messages that agent-net reports as
// delivered. A file that a package manager owns is named with the line to
// declare in its source.
func Compare(st State, settingsPath string) Report {
	r := Report{SettingsPath: settingsPath}
	if settingsPath == "" {
		return r
	}
	r.Linked, r.ReadOnly = fileKind(settingsPath)
	v, set, err := claudesettings.Value(settingsPath, SettingKey)
	if err != nil {
		r.Problem = fmt.Sprintf("agent-net cannot read %s: %v", settingsPath, err)
		return r
	}
	r.Live, r.LiveSet = v, set
	if !st.SetByAgentNet || (set && v == "accept") {
		return r
	}
	r.Problem = fmt.Sprintf("agent-net recorded that it set %q to \"accept\" in %s, but %s. Claude Code holds a message when the sender's permission mode class differs from this session's.",
		SettingKey, settingsPath, liveText(settingsPath, set, v))
	if r.Linked || r.ReadOnly {
		r.Problem += fmt.Sprintf(" The file is %s. Declare this line in the file that manages it, for example a Nix or Home Manager dotfile: %s.",
			fileText(r.Linked, r.ReadOnly), `"`+SettingKey+`": "accept"`)
	} else {
		r.Problem += fmt.Sprintf(" Run agent-net setup --accept-peer-messages, or set %q in that file.", SettingKey)
	}
	return r
}

func liveText(path string, set bool, value string) string {
	if !set {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return "the file does not exist"
		}
		return fmt.Sprintf("the file has no %q key", SettingKey)
	}
	return fmt.Sprintf("the file says %q", value)
}

func fileText(linked, readOnly bool) string {
	switch {
	case linked && readOnly:
		return "a link to a read-only file"
	case linked:
		return "a link to another file"
	default:
		return "read-only"
	}
}

// fileKind says whether the path is a symlink and whether its target is
// read-only. A missing file is neither.
func fileKind(path string) (linked, readOnly bool) {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		linked = true
	}
	target := path
	if linked {
		if p, err := filepath.EvalSymlinks(path); err == nil {
			target = p
		}
	}
	if fi, err := os.Stat(target); err == nil {
		readOnly = fi.Mode().Perm()&0o200 == 0
	}
	return linked, readOnly
}

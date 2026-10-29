package setup

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/daemon"
)

// Values of Options.PeerMessages.
const (
	PeerMessagesAccept = "accept" // --accept-peer-messages
	PeerMessagesNo     = "no"     // --no-accept-peer-messages
)

// settingKey is the Claude Code user setting that decides whether messages
// from other sessions are held for review. Claude Code accepts "accept",
// "hold" and "refuse".
const settingKey = "crossSessionInbound"

const peerStepName = "claude peer messages"

// openTTY opens the controlling terminal. Tests replace it.
var openTTY = func() (io.ReadCloser, error) { return os.Open("/dev/tty") }

// claudeSettingsPath is the Claude Code user settings file. Claude Code reads
// it from $CLAUDE_CONFIG_DIR, or ~/.claude when that is not set.
func claudeSettingsPath() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json")
	}
	return filepath.Join(home(), ".claude", "settings.json")
}

// peerState is what agent-net remembers about this setting.
type peerState struct {
	Asked    bool   `json:"asked"`            // the owner has answered once; do not ask again
	SetByUs  bool   `json:"set_by_agent_net"` // agent-net wrote "accept"
	Settings string `json:"settings_path,omitempty"`
}

func peerStatePath() string { return filepath.Join(daemon.ConfigDir(), "claude-peer-messages.json") }

func loadPeerState() peerState {
	var st peerState
	if b, err := os.ReadFile(peerStatePath()); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

func savePeerState(st peerState) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return atomicWrite(peerStatePath(), append(b, '\n'), 0o600)
}

// ---- settings file: ordered top-level keys, other values kept as they are ----

type member struct {
	key string
	val json.RawMessage
}

type settingsDoc struct {
	members []member
}

var errNotJSON = errors.New("not a valid JSON object")

// readSettings parses the settings file. A missing or empty file is an empty
// document.
func readSettings(path string) (*settingsDoc, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(bytes.TrimSpace(b)) == 0) {
		return &settingsDoc{}, nil
	}
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errNotJSON
	}
	doc := &settingsDoc{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, errNotJSON
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, errNotJSON
		}
		doc.members = append(doc.members, member{kt.(string), raw})
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return nil, errNotJSON
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errNotJSON
	}
	return doc, nil
}

func (d *settingsDoc) index() int {
	for i, m := range d.members {
		if m.key == settingKey {
			return i
		}
	}
	return -1
}

// value returns the setting as text: the string for a string value, else the
// raw JSON.
func (d *settingsDoc) value() (string, bool) {
	i := d.index()
	if i < 0 {
		return "", false
	}
	var s string
	if json.Unmarshal(d.members[i].val, &s) == nil {
		return s, true
	}
	return string(d.members[i].val), true
}

func (d *settingsDoc) encode() []byte {
	if len(d.members) == 0 {
		return []byte("{}\n")
	}
	var buf bytes.Buffer
	buf.WriteString("{\n")
	for i, m := range d.members {
		var k bytes.Buffer
		enc := json.NewEncoder(&k)
		enc.SetEscapeHTML(false)
		enc.Encode(m.key)
		var v bytes.Buffer
		if err := json.Indent(&v, m.val, "  ", "  "); err != nil {
			v.Write(m.val)
		}
		buf.WriteString("  " + strings.TrimSpace(k.String()) + ": " + v.String())
		if i < len(d.members)-1 {
			buf.WriteString(",")
		}
		buf.WriteString("\n")
	}
	buf.WriteString("}\n")
	return buf.Bytes()
}

// atomicWrite writes through a temp file in the same directory and a rename.
// A symlink is followed, so a managed settings file stays a link.
func atomicWrite(path string, data []byte, defMode os.FileMode) error {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	mode := defMode
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
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
	return os.Rename(tmp.Name(), path)
}

func setAccept(path string, doc *settingsDoc) error {
	doc.members = append(doc.members, member{settingKey, json.RawMessage(`"accept"`)})
	return atomicWrite(path, doc.encode(), 0o644)
}

// ---- setup step ----

func (r *runner) claudePeerMessages() {
	if claudePath() == "" {
		r.step(peerStepName, "skipped (claude not found)", nil)
		return
	}
	path := claudeSettingsPath()
	doc, err := readSettings(path)
	if err != nil {
		if errors.Is(err, errNotJSON) {
			r.step(peerStepName, "skipped ("+displayPath(path)+" is not valid JSON, left untouched)", nil)
		} else {
			r.step(peerStepName, "", err)
		}
		return
	}
	st := loadPeerState()
	if cur, ok := doc.value(); ok {
		// The owner (or a past agent-net run) decided. Never override.
		who := "your setting"
		if st.SetByUs && cur == "accept" {
			who = "set by agent-net"
		}
		r.step(peerStepName, cur+" ("+who+")", nil)
		if r.PeerMessages == PeerMessagesAccept && cur != "accept" {
			r.hint("--accept-peer-messages ignored: %s already sets %s", displayPath(path), settingKey)
		}
		return
	}
	accept := false
	switch {
	case r.PeerMessages == PeerMessagesAccept:
		accept = true
	case r.PeerMessages == PeerMessagesNo:
	case st.Asked:
	default:
		ans, ok := r.askPeerMessages()
		if !ok {
			r.step(peerStepName, "held for review (default)", nil)
			r.hint("no terminal to ask. To turn it on later: agent-net setup --accept-peer-messages")
			return
		}
		accept = ans
	}
	if accept {
		if err := setAccept(path, doc); err != nil {
			r.step(peerStepName, "", err)
			return
		}
		if err := savePeerState(peerState{Asked: true, SetByUs: true, Settings: path}); err != nil {
			r.step(peerStepName, "", err)
			return
		}
		r.step(peerStepName, "accept (set by agent-net)", nil)
		return
	}
	if !st.Asked {
		if err := savePeerState(peerState{Asked: true}); err != nil {
			r.step(peerStepName, "", err)
			return
		}
	}
	r.step(peerStepName, "held for review (default)", nil)
}

// askPeerMessages asks on the controlling terminal. ok is false when there is
// no terminal.
func (r *runner) askPeerMessages() (yes, ok bool) {
	tty, err := openTTY()
	if err != nil {
		return false, false
	}
	defer tty.Close()
	fmt.Fprintln(r.Out, "Claude Code holds messages from other sessions for review when the sender's")
	fmt.Fprintln(r.Out, "permission mode differs from yours. Accepting delivers messages from any agent on")
	fmt.Fprintln(r.Out, "your network without review, even from sessions in a looser mode. A peer message")
	fmt.Fprintln(r.Out, "still never counts as your approval.")
	fmt.Fprint(r.Out, "Accept agent-net messages in Claude Code without holding them? [y/N] ")
	line, _ := bufio.NewReader(tty).ReadString('\n') // the terminal echoes the newline
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, true
	}
	return false, true
}

// ---- uninstall step ----

func (r *runner) removePeerMessages() {
	st := loadPeerState()
	if !st.SetByUs {
		r.step(peerStepName, "skipped (not set by agent-net)", nil)
		return
	}
	path := st.Settings
	if path == "" {
		path = claudeSettingsPath()
	}
	doc, err := readSettings(path)
	if err != nil {
		if errors.Is(err, errNotJSON) {
			r.step(peerStepName, "skipped ("+displayPath(path)+" is not valid JSON, left untouched)", nil)
		} else {
			r.step(peerStepName, "", err)
		}
		return
	}
	i := doc.index()
	if cur, _ := doc.value(); i < 0 || cur != "accept" {
		r.step(peerStepName, "skipped (value changed, left as is)", nil)
		os.Remove(peerStatePath())
		return
	}
	doc.members = append(doc.members[:i], doc.members[i+1:]...)
	if err := atomicWrite(path, doc.encode(), 0o644); err != nil {
		r.step(peerStepName, "", err)
		return
	}
	os.Remove(peerStatePath())
	r.step(peerStepName, "ok (removed "+settingKey+")", nil)
}

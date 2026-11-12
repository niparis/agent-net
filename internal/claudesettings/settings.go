// Package claudesettings owns the keys that agent-net writes to the user's
// Claude Code settings file, $CLAUDE_CONFIG_DIR/settings.json or
// ~/.claude/settings.json. The daemon applies them at runtime and setup writes
// them on a new machine; uninstall removes only what agent-net added.
package claudesettings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// MouseKey is the Claude Code environment variable that decides whether a
// session asks the terminal for mouse events. "0" or unset turns mouse
// tracking on, so the wheel scrolls the conversation (decision 0128).
const MouseKey = "CLAUDE_CODE_DISABLE_MOUSE"

// MouseOn is the value agent-net chooses for every Claude session: the wheel
// scrolls the conversation.
const MouseOn = "0"

// AutoUpdateKey turns Claude Code's own background updater off. agent-net's
// daemon is the only updater for every harness (owner's order, decision 0133),
// so every Claude Code session agent-net starts gets this variable, and the
// user settings carry it for sessions the owner starts by hand.
const AutoUpdateKey = "DISABLE_AUTOUPDATER"

// AutoUpdateOff is the value that turns the in-session updater off.
const AutoUpdateOff = "1"

// ErrNotJSON says a settings file is not a JSON object. agent-net leaves such
// a file untouched and reports it.
var ErrNotJSON = errors.New("not a valid JSON object")

// Dir is the Claude Code configuration folder: $CLAUDE_CONFIG_DIR, or
// ~/.claude when that variable is not set.
func Dir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".claude")
}

// File is the user settings file Claude Code reads.
func File() string {
	dir := Dir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "settings.json")
}

// MouseValue returns the user-level mouse value and whether the key is set.
// A missing file has no value. A file that is not valid JSON returns ErrNotJSON.
func MouseValue(path string) (string, bool, error) {
	return EnvValue(path, MouseKey)
}

// EnsureMouseOn sets env.CLAUDE_CODE_DISABLE_MOUSE to "0" and keeps every
// other key. It writes only when the file changes and returns whether it did.
// A missing file is created with only that key. A file that is not valid JSON
// is left untouched and ErrNotJSON is returned.
func EnsureMouseOn(path string) (bool, error) {
	return EnsureEnvValue(path, MouseKey, MouseOn)
}

// RemoveMouseOn removes env.CLAUDE_CODE_DISABLE_MOUSE only while its value is
// still "0". A value somebody else chose is kept. Other keys stay as they are.
func RemoveMouseOn(path string) (bool, error) {
	return RemoveEnvValue(path, MouseKey, MouseOn)
}

// AutoUpdateValue returns the user-level auto-update value and whether the key
// is set. It follows the same rules as MouseValue.
func AutoUpdateValue(path string) (string, bool, error) {
	return EnvValue(path, AutoUpdateKey)
}

// EnsureAutoUpdateOff sets env.DISABLE_AUTOUPDATER to "1" and keeps every
// other key. It follows the same rules as EnsureMouseOn.
func EnsureAutoUpdateOff(path string) (bool, error) {
	return EnsureEnvValue(path, AutoUpdateKey, AutoUpdateOff)
}

// RemoveAutoUpdateOff removes env.DISABLE_AUTOUPDATER only while its value is
// still "1". A value somebody else chose is kept, and so is every other key.
func RemoveAutoUpdateOff(path string) (bool, error) {
	return RemoveEnvValue(path, AutoUpdateKey, AutoUpdateOff)
}

// EnvValue returns the value of one key in the env object and whether it is
// set. A missing file has no value. A file that is not valid JSON returns
// ErrNotJSON.
func EnvValue(path, key string) (string, bool, error) {
	members, _, err := readFile(path)
	if err != nil {
		return "", false, err
	}
	env, _, err := envOf(members)
	if err != nil {
		return "", false, err
	}
	for _, m := range env {
		if m.key == key {
			var s string
			if json.Unmarshal(m.val, &s) == nil {
				return s, true, nil
			}
			return string(m.val), true, nil
		}
	}
	return "", false, nil
}

// EnsureEnvValue sets env.key to value and keeps every other key. It writes
// only when the file changes and returns whether it did. A missing file is
// created with only that key. A file that is not valid JSON is left untouched
// and ErrNotJSON is returned.
func EnsureEnvValue(path, key, value string) (bool, error) {
	members, _, err := readFile(path)
	if err != nil {
		return false, err
	}
	env, envIndex, err := envOf(members)
	if err != nil {
		return false, err
	}
	want := json.RawMessage(quote(value))
	changed := false
	found := false
	for i := range env {
		if env[i].key == key {
			found = true
			if string(env[i].val) != string(want) {
				env[i].val = want
				changed = true
			}
		}
	}
	if !found {
		env = append(env, member{key: key, val: want})
		changed = true
	}
	if envIndex >= 0 {
		members[envIndex].val = encodeObject(env)
	} else {
		members = append(members, member{key: "env", val: encodeObject(env)})
	}
	if !changed {
		return false, nil
	}
	return true, writeFile(path, encodeDocument(members))
}

// RemoveEnvValue removes env.key only while its value is still value. A value
// somebody else chose is kept. Other keys stay as they are.
func RemoveEnvValue(path, key, value string) (bool, error) {
	members, _, err := readFile(path)
	if err != nil {
		return false, err
	}
	env, envIndex, err := envOf(members)
	if err != nil || envIndex < 0 {
		return false, err
	}
	want := string(quote(value))
	kept := env[:0]
	changed := false
	for _, m := range env {
		if m.key == key {
			if string(m.val) != want {
				kept = append(kept, m)
				continue
			}
			changed = true
			continue
		}
		kept = append(kept, m)
	}
	if !changed {
		return false, nil
	}
	members[envIndex].val = encodeObject(kept)
	return true, writeFile(path, encodeDocument(members))
}

// ---- ordered JSON object: other keys keep their values and their order ----

type member struct {
	key string
	val json.RawMessage
}

// readFile parses the settings file. A missing or empty file is an empty
// document. It returns whether the file exists.
func readFile(path string) ([]member, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, true, nil
	}
	members, err := parseObject(b)
	if err != nil {
		return nil, true, fmt.Errorf("%s: %w", path, ErrNotJSON)
	}
	return members, true, nil
}

// parseObject reads a JSON object into ordered members.
func parseObject(b []byte) ([]member, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, ErrNotJSON
	}
	var members []member
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, ErrNotJSON
		}
		key, ok := kt.(string)
		if !ok {
			return nil, ErrNotJSON
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, ErrNotJSON
		}
		members = append(members, member{key: key, val: raw})
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return nil, ErrNotJSON
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, ErrNotJSON
	}
	return members, nil
}

// envOf returns the members of the "env" object and its index in members, or
// a nil list and -1 when the file has no env object.
func envOf(members []member) ([]member, int, error) {
	for i, m := range members {
		if m.key != "env" {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(m.val, &raw); err != nil {
			return nil, -1, fmt.Errorf("env is not a JSON object: %w", ErrNotJSON)
		}
		if len(bytes.TrimSpace(m.val)) == 0 || string(bytes.TrimSpace(m.val)) == "null" {
			return nil, i, nil
		}
		env, err := parseObject(m.val)
		return env, i, err
	}
	return nil, -1, nil
}

// encodeObject writes the members compactly and in order. encodeDocument
// indents the result, so the nested env keeps the same style as the rest.
func encodeObject(members []member) json.RawMessage {
	var buf bytes.Buffer
	buf.WriteString("{")
	for i, m := range members {
		if i > 0 {
			buf.WriteString(",")
		}
		buf.Write(quote(m.key))
		buf.WriteString(":")
		buf.Write(m.val)
	}
	buf.WriteString("}")
	return json.RawMessage(buf.Bytes())
}

// encodeDocument writes the object with the two-space indent Claude Code uses.
func encodeDocument(members []member) []byte {
	if len(members) == 0 {
		return []byte("{}\n")
	}
	var buf bytes.Buffer
	buf.WriteString("{\n")
	for i, m := range members {
		buf.WriteString("  ")
		buf.Write(quote(m.key))
		buf.WriteString(": ")
		var v bytes.Buffer
		if err := json.Indent(&v, m.val, "  ", "  "); err != nil {
			v.Write(m.val)
		}
		buf.Write(v.Bytes())
		if i < len(members)-1 {
			buf.WriteString(",")
		}
		buf.WriteString("\n")
	}
	buf.WriteString("}\n")
	return buf.Bytes()
}

// quote writes a JSON string without escaping HTML characters, which keeps
// hook commands and other values as the owner wrote them.
func quote(s string) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(s)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// writeFile writes through a temp file in the same folder and a rename, and
// keeps the existing file mode. A symlink is followed.
func writeFile(path string, data []byte) error {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	mode := os.FileMode(0o644)
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

// IsNotJSON reports whether err says a settings file is not valid JSON.
func IsNotJSON(err error) bool { return errors.Is(err, ErrNotJSON) }

package inventory

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// This file removes one native MCP entry from a harness config file by a
// minimal edit: it finds the bytes of the entry and cuts exactly those. Keys,
// order, comments and indentation outside the entry stay as they are. Before it
// answers, it parses the old and new text and proves that only the entry
// differs. A file it cannot edit this way is refused, never rewritten.

// MaxNativeFile bounds a config file the editor reads.
const MaxNativeFile = 1 << 20

// NativePath resolves a scanned entry's file to the editable path and the
// directory it must stay inside. ok is false for a file that is no known
// native MCP source.
func NativePath(o Options, harness, scope, root, location string) (base, path string, ok bool) {
	src, ok := o.nativeSource(harness, scope, root, location)
	return src.base, src.path, ok
}

// ReadNativeFile reads a native config file without following a symlink.
func ReadNativeFile(base, path string) ([]byte, os.FileMode, error) {
	if !safePath(base, path) {
		return nil, 0, errors.New("the file path contains a symlink")
	}
	dir, err := os.OpenRoot(base)
	if err != nil {
		return nil, 0, err
	}
	defer dir.Close()
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return nil, 0, errors.New("invalid file path")
	}
	f, err := dir.Open(rel)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, 0, errors.New("not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxNativeFile+1))
	if err != nil || len(data) > MaxNativeFile {
		return nil, 0, errors.New("cannot read the file or the file is too large")
	}
	return data, info.Mode().Perm(), nil
}

// RemoveNativeEntry returns the content of the file without the named entry.
// data is the file as the caller read it. It returns os.ErrNotExist when the
// entry is not there, and another error when the entry is in a form the editor
// does not own. Only the bytes of the entry are cut.
func RemoveNativeEntry(o Options, harness, scope, root, location, name string, data []byte) ([]byte, error) {
	src, ok := o.nativeSource(harness, scope, root, location)
	if !ok {
		return nil, errors.New("not a known native MCP source")
	}
	var spans [][2]int
	var err error
	if src.toml {
		spans, err = tomlEntrySpans(data, src.key, name)
	} else {
		var path []string
		if src.key == "project-local" {
			path = []string{"projects", root, "mcpServers"}
		} else {
			path = []string{src.key}
		}
		spans, err = jsonEntrySpans(data, path, name)
	}
	if err != nil {
		return nil, err
	}
	out := cutSpans(data, spans)
	if err = verifyRemoval(src, data, out, root, name); err != nil {
		return nil, err
	}
	return out, nil
}

func cutSpans(data []byte, spans [][2]int) []byte {
	out := make([]byte, 0, len(data))
	prev := 0
	for _, s := range spans {
		out = append(out, data[prev:s[0]]...)
		prev = s[1]
	}
	return append(out, data[prev:]...)
}

// parseNative reads a config file as the scanner does, and also accepts the
// trailing commas JSONC allows.
func parseNative(src nativeSource, data []byte) (map[string]any, error) {
	var doc map[string]any
	if src.toml {
		return doc, toml.Unmarshal(data, &doc)
	}
	return doc, json.Unmarshal(stripTrailingCommas(stripComments(data)), &doc)
}

func stripTrailingCommas(b []byte) []byte {
	out := append([]byte(nil), b...)
	inString, escaped := false, false
	last := -1
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
		switch c {
		case '"':
			inString = true
			last = -1
		case ',':
			last = i
		case '}', ']':
			if last >= 0 {
				out[last] = ' '
			}
			last = -1
		case ' ', '\t', '\n', '\r':
		default:
			last = -1
		}
	}
	return out
}

// dropEntry deletes the entry from a parsed document.
func dropEntry(doc map[string]any, src nativeSource, root, name string) {
	var servers map[string]any
	var holder map[string]any
	var key string
	switch {
	case src.key == "project-local":
		projects, _ := doc["projects"].(map[string]any)
		holder, _ = projects[root].(map[string]any)
		key = "mcpServers"
	default:
		holder, key = doc, src.key
	}
	servers, _ = holder[key].(map[string]any)
	delete(servers, name)
	if src.toml && len(servers) == 0 {
		delete(holder, key)
	}
}

// verifyRemoval proves the new text parses, no longer holds the entry, and
// equals the old document in every other value.
func verifyRemoval(src nativeSource, before, after []byte, root, name string) error {
	old, err := parseNative(src, before)
	if err != nil {
		return errors.New("the file is malformed; it was not edited")
	}
	neu, err := parseNative(src, after)
	if err != nil {
		return errors.New("the edit would leave a malformed file; nothing was changed")
	}
	if holds(neu, src, root, name) {
		return errors.New("the entry is written in a form this editor does not remove; remove it by hand")
	}
	dropEntry(old, src, root, name)
	if src.toml {
		// A table that held only this entry is not written any more.
		dropEntry(neu, src, root, "")
	}
	if !reflect.DeepEqual(old, neu) {
		return errors.New("the edit would change more than the entry; nothing was changed")
	}
	return nil
}

func holds(doc map[string]any, src nativeSource, root, name string) bool {
	holder := doc
	key := src.key
	if src.key == "project-local" {
		projects, _ := doc["projects"].(map[string]any)
		holder, _ = projects[root].(map[string]any)
		key = "mcpServers"
	}
	servers, _ := holder[key].(map[string]any)
	_, ok := servers[name]
	return ok
}

// ---- JSON and JSONC ----

type jsonScan struct{ d []byte }

// skip passes whitespace and comments.
func (s jsonScan) skip(i int) int {
	for i < len(s.d) {
		switch c := s.d[i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '/' && i+1 < len(s.d) && s.d[i+1] == '/':
			for i < len(s.d) && s.d[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(s.d) && s.d[i+1] == '*':
			j := i + 2
			for j+1 < len(s.d) && !(s.d[j] == '*' && s.d[j+1] == '/') {
				j++
			}
			if j+1 >= len(s.d) {
				return len(s.d)
			}
			i = j + 2
		default:
			return i
		}
	}
	return i
}

// space passes whitespace only, never a comment.
func (s jsonScan) space(i int) int {
	for i < len(s.d) && (s.d[i] == ' ' || s.d[i] == '\t' || s.d[i] == '\n' || s.d[i] == '\r') {
		i++
	}
	return i
}

func (s jsonScan) str(i int) (string, int, error) {
	if i >= len(s.d) || s.d[i] != '"' {
		return "", i, errors.New("expected a string")
	}
	j := i + 1
	for j < len(s.d) && s.d[j] != '"' {
		if s.d[j] == '\\' {
			j++
		}
		j++
	}
	if j >= len(s.d) {
		return "", i, errors.New("unterminated string")
	}
	var v string
	if err := json.Unmarshal(s.d[i:j+1], &v); err != nil {
		return "", i, err
	}
	return v, j + 1, nil
}

// value returns the end of the JSON value that starts at i.
func (s jsonScan) value(i int) (int, error) {
	if i >= len(s.d) {
		return i, errors.New("unexpected end")
	}
	switch s.d[i] {
	case '"':
		_, end, err := s.str(i)
		return end, err
	case '{':
		_, end, err := s.members(i)
		return end, err
	case '[':
		j := s.skip(i + 1)
		for j < len(s.d) && s.d[j] != ']' {
			end, err := s.value(j)
			if err != nil {
				return i, err
			}
			j = s.skip(end)
			if j < len(s.d) && s.d[j] == ',' {
				j = s.skip(j + 1)
			}
		}
		if j >= len(s.d) {
			return i, errors.New("unterminated array")
		}
		return j + 1, nil
	}
	j := i
	for j < len(s.d) && !strings.ContainsRune(" \t\r\n,]}/", rune(s.d[j])) {
		j++
	}
	if j == i {
		return i, errors.New("unexpected character")
	}
	return j, nil
}

type jsonMember struct {
	key                        string
	keyStart, valStart, valEnd int
	commaAfter                 int // index of the comma after the value, or -1
}

// members lists the members of the object whose { is at i, and the index just
// after its }.
func (s jsonScan) members(i int) ([]jsonMember, int, error) {
	if i >= len(s.d) || s.d[i] != '{' {
		return nil, i, errors.New("expected an object")
	}
	var out []jsonMember
	j := s.skip(i + 1)
	for j < len(s.d) && s.d[j] != '}' {
		m := jsonMember{keyStart: j, commaAfter: -1}
		var err error
		m.key, j, err = s.str(j)
		if err != nil {
			return nil, i, err
		}
		j = s.skip(j)
		if j >= len(s.d) || s.d[j] != ':' {
			return nil, i, errors.New("expected a colon")
		}
		j = s.skip(j + 1)
		m.valStart = j
		if m.valEnd, err = s.value(j); err != nil {
			return nil, i, err
		}
		j = s.skip(m.valEnd)
		if j < len(s.d) && s.d[j] == ',' {
			m.commaAfter = j
			j = s.skip(j + 1)
		} else if j >= len(s.d) || s.d[j] != '}' {
			return nil, i, errors.New("expected a comma or a closing brace")
		}
		out = append(out, m)
	}
	if j >= len(s.d) {
		return nil, i, errors.New("unterminated object")
	}
	return out, j + 1, nil
}

func jsonEntrySpans(data []byte, path []string, name string) ([][2]int, error) {
	s := jsonScan{data}
	at := s.skip(0)
	if at >= len(data) || data[at] != '{' {
		return nil, errors.New("the file is not a JSON object")
	}
	var list []jsonMember
	var err error
	for _, key := range append(append([]string{}, path...), "") {
		list, _, err = s.members(at)
		if err != nil {
			return nil, errors.New("the file is malformed; it was not edited")
		}
		if key == "" {
			break
		}
		found := -1
		for k, m := range list {
			if m.key == key {
				if found >= 0 {
					return nil, fmt.Errorf("the key %q appears twice; remove the entry by hand", key)
				}
				found = k
			}
		}
		if found < 0 {
			return nil, os.ErrNotExist
		}
		at = list[found].valStart
		if at >= len(data) || data[at] != '{' {
			return nil, errors.New("the server table is not an object")
		}
	}
	idx := -1
	for k, m := range list {
		if m.key == name {
			if idx >= 0 {
				return nil, fmt.Errorf("the entry %q appears twice; remove it by hand", name)
			}
			idx = k
		}
	}
	if idx < 0 {
		return nil, os.ErrNotExist
	}
	m := list[idx]
	switch {
	case idx < len(list)-1:
		// Up to the next entry, past the comma and the blanks after it. A
		// comment that follows stays.
		end := s.space(m.commaAfter + 1)
		return [][2]int{{m.keyStart, end}}, nil
	case idx > 0:
		// The last entry takes the comma before it. A trailing comma after it
		// stays, as the author wrote it.
		return [][2]int{{list[idx-1].valEnd, m.valEnd}}, nil
	}
	end := m.valEnd
	if m.commaAfter >= 0 {
		end = m.commaAfter + 1
	}
	return [][2]int{{m.keyStart, end}}, nil
}

// ---- TOML ----

type tomlHeader struct {
	start int // offset of the line start
	keys  []string
}

// tomlHeaders finds the table headers of a TOML file, skipping strings,
// comments and the inside of arrays and inline tables.
func tomlHeaders(d []byte) ([]tomlHeader, error) {
	var out []tomlHeader
	depth := 0
	lineStart := true
	i := 0
	for i < len(d) {
		if lineStart && depth == 0 {
			j := i
			for j < len(d) && (d[j] == ' ' || d[j] == '\t') {
				j++
			}
			if j < len(d) && d[j] == '[' {
				keys, end, err := tomlHeaderKeys(d, j)
				if err != nil {
					return nil, err
				}
				out = append(out, tomlHeader{start: i, keys: keys})
				i = end
				lineStart = false
				continue
			}
		}
		lineStart = false
		switch c := d[i]; c {
		case '\n':
			lineStart = depth == 0
			i++
		case '#':
			for i < len(d) && d[i] != '\n' {
				i++
			}
		case '"', '\'':
			end, err := tomlString(d, i)
			if err != nil {
				return nil, err
			}
			i = end
		case '[', '{':
			depth++
			i++
		case ']', '}':
			if depth > 0 {
				depth--
			}
			i++
		default:
			i++
		}
	}
	return out, nil
}

// tomlString returns the index after the string that starts at i.
func tomlString(d []byte, i int) (int, error) {
	q := d[i]
	if i+2 < len(d) && d[i+1] == q && d[i+2] == q {
		j := i + 3
		for j < len(d) {
			if q == '"' && d[j] == '\\' {
				j += 2
				continue
			}
			if d[j] == q && j+2 < len(d)+0 && j+2 <= len(d)-1 && d[j+1] == q && d[j+2] == q {
				j += 3
				for j < len(d) && d[j] == q {
					j++
				}
				return j, nil
			}
			if d[j] == q && j+2 == len(d)-0 {
				break
			}
			j++
		}
		return i, errors.New("unterminated string")
	}
	j := i + 1
	for j < len(d) && d[j] != q && d[j] != '\n' {
		if q == '"' && d[j] == '\\' {
			j++
		}
		j++
	}
	if j >= len(d) || d[j] != q {
		return i, errors.New("unterminated string")
	}
	return j + 1, nil
}

// tomlHeaderKeys parses the key of a table header whose [ is at i.
func tomlHeaderKeys(d []byte, i int) ([]string, int, error) {
	array := i+1 < len(d) && d[i+1] == '['
	j := i + 1
	if array {
		j++
	}
	var keys []string
	for {
		for j < len(d) && (d[j] == ' ' || d[j] == '\t') {
			j++
		}
		if j >= len(d) {
			return nil, i, errors.New("bad table header")
		}
		switch c := d[j]; {
		case c == '"' || c == '\'':
			end, err := tomlString(d, j)
			if err != nil {
				return nil, i, err
			}
			k := string(d[j+1 : end-1])
			if c == '"' {
				u, err := strconv.Unquote(`"` + k + `"`)
				if err != nil {
					return nil, i, errors.New("bad table header")
				}
				k = u
			}
			keys = append(keys, k)
			j = end
		default:
			k := j
			for k < len(d) && (d[k] == '-' || d[k] == '_' || d[k] >= '0' && d[k] <= '9' || d[k] >= 'a' && d[k] <= 'z' || d[k] >= 'A' && d[k] <= 'Z') {
				k++
			}
			if k == j {
				return nil, i, errors.New("bad table header")
			}
			keys = append(keys, string(d[j:k]))
			j = k
		}
		for j < len(d) && (d[j] == ' ' || d[j] == '\t') {
			j++
		}
		if j < len(d) && d[j] == '.' {
			j++
			continue
		}
		break
	}
	if j >= len(d) || d[j] != ']' {
		return nil, i, errors.New("bad table header")
	}
	j++
	if array {
		if j >= len(d) || d[j] != ']' {
			return nil, i, errors.New("bad table header")
		}
		j++
	}
	return keys, j, nil
}

// tomlEntrySpans returns the byte ranges of every table that belongs to the
// entry: [key.name] and its sub-tables. Comment and blank lines that follow the
// last line of a table belong to what comes next and stay.
func tomlEntrySpans(d []byte, key, name string) ([][2]int, error) {
	heads, err := tomlHeaders(d)
	if err != nil {
		return nil, errors.New("the file is malformed; it was not edited")
	}
	var spans [][2]int
	for k, h := range heads {
		if len(h.keys) < 2 || h.keys[0] != key || h.keys[1] != name {
			continue
		}
		end := len(d)
		if k+1 < len(heads) {
			end = heads[k+1].start
		}
		end = trimTrailingBlank(d, h.start, end)
		// Blank lines after the table go with it.
		for end < len(d) {
			j := end
			for j < len(d) && (d[j] == ' ' || d[j] == '\t' || d[j] == '\r') {
				j++
			}
			if j < len(d) && d[j] == '\n' {
				end = j + 1
				continue
			}
			break
		}
		spans = append(spans, [2]int{h.start, end})
	}
	if len(spans) == 0 {
		return nil, os.ErrNotExist
	}
	return spans, nil
}

// trimTrailingBlank moves end back over comment-only and blank lines at the end
// of the range.
func trimTrailingBlank(d []byte, start, end int) int {
	for end > start {
		ls := end - 1
		if d[ls] == '\n' {
			ls--
		}
		for ls >= start && d[ls] != '\n' {
			ls--
		}
		ls++
		line := strings.TrimSpace(string(d[ls:end]))
		if line != "" && !strings.HasPrefix(line, "#") {
			break
		}
		end = ls
	}
	return end
}

// FederationGaps says what a native MCP entry can do that the federation does
// not provide. The federation carries tools only, from one host.
func FederationGaps(machine string) []string {
	host := "the host"
	if machine != "" {
		host = machine
	}
	return []string{
		"resources and prompts: the federation carries tools only",
		"OAuth sign-in: the federation cannot sign in for you",
		"per-client execution: the server runs on " + host + ", not on each machine that uses it",
	}
}

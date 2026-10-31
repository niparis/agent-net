// Package guidance holds the agent-net text that goes into agents' global
// instruction files, and the logic to add, replace and remove it.
package guidance

import (
	_ "embed"
	"strings"
)

//go:embed guidance.md
var text string

const (
	Begin   = "<!-- agent-net:begin -->"
	End     = "<!-- agent-net:end -->"
	heading = "## agent-net: working with other agents"
)

// Variants differ only in how to reply to a peer.
const (
	Claude   = "claude"
	Other    = "other"
	Combined = "combined"
)

var reply = map[string]string{
	Claude:   "(Claude: `SendMessage` to the sender; others: `agent_reply` with the message ID)",
	Other:    "with `agent_reply` and the message ID",
	Combined: "as follows: Claude Code: use `SendMessage` to the sender; Pi, OpenCode, Codex: use `agent_reply` with the message ID",
}

// Block returns the marked block for a variant, ending in a newline.
func Block(variant string) string {
	body := strings.ReplaceAll(strings.TrimRight(text, "\n"), "{{reply}}", reply[variant])
	return Begin + "\n" + body + "\n" + End + "\n"
}

// Apply puts the block into the file content. It replaces an existing marked
// block, else migrates an unmarked section with the agent-net heading, else
// appends. changed is false when the content is already up to date.
func Apply(content, variant string) (out string, changed bool) {
	block := Block(variant)
	if b, e, ok := findMarked(content); ok {
		out = content[:b] + block + content[e:]
	} else if b, e, ok := findLegacy(content); ok {
		rest := content[e:]
		if rest != "" {
			rest = "\n" + rest
		}
		out = content[:b] + block + rest
	} else {
		out = content
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		if out != "" {
			out += "\n"
		}
		out += block
	}
	return out, out != content
}

// Remove takes the block (or the unmarked legacy section) out of content.
func Remove(content string) (out string, changed bool) {
	b, e, ok := findMarked(content)
	if !ok {
		b, e, ok = findLegacy(content)
	}
	if !ok {
		return content, false
	}
	head, tail := content[:b], content[e:]
	// Drop the blank line that separated the block from what came before.
	head = strings.TrimRight(head, "\n")
	tail = strings.TrimLeft(tail, "\n")
	if head != "" {
		head += "\n"
		if tail != "" {
			head += "\n"
		}
	}
	return head + tail, true
}

// findMarked returns the byte range of the marked block, from the begin
// marker line to just after the end marker line.
func findMarked(s string) (b, e int, ok bool) {
	b = strings.Index(s, Begin)
	if b < 0 {
		return 0, 0, false
	}
	i := strings.Index(s[b:], End)
	if i < 0 {
		return 0, 0, false
	}
	e = b + i + len(End)
	if e < len(s) && s[e] == '\n' {
		e++
	}
	return b, e, true
}

// findLegacy returns the range of an unmarked section: the agent-net heading
// up to the next "## " heading or the end of the file. Trailing blank lines
// belong to the section.
func findLegacy(s string) (b, e int, ok bool) {
	pos := 0
	b = -1
	for pos < len(s) {
		nl := strings.IndexByte(s[pos:], '\n')
		end := len(s)
		if nl >= 0 {
			end = pos + nl + 1
		}
		line := strings.TrimRight(s[pos:end], "\r\n")
		if b < 0 {
			if line == heading {
				b = pos
			}
		} else if strings.HasPrefix(line, "## ") {
			return b, pos, true
		}
		pos = end
	}
	if b < 0 {
		return 0, 0, false
	}
	return b, len(s), true
}

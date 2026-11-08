// Package guidance holds the agent-net text that goes into agents' global
// instruction files, and the logic to add, replace and remove it.
package guidance

import (
	"encoding/json"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

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
	Claude:   "(Claude: `SendMessage` to the sender; others: `agent_message_reply` with the message ID)",
	Other:    "with `agent_message_reply` and the message ID",
	Combined: "as follows: Claude Code: use `SendMessage` to the sender; Pi, OpenCode, Codex: use `agent_message_reply` with the message ID",
}

const fileSendGuidance = "- **Send files to a peer:** use `agent_file_send` with a session ID, absolute paths and a note; workers send files from their own worktree in the `files` list on `agent_handback`."

const handoffGuidance = "- **Lead a team or repo:** a request that came to you because you lead it is yours to answer, or to pass on with `agent_message_handoff` (message ID, session, note) so the member answers the original sender directly. Your place is in the turn text."

var authorityGuidance = "- **Orders, requests and advice:** agent-net labels each message from the tree, never from the body. An *order* comes from your lead, above you in your team or repo, and the sender marked it as one. A *request* comes from a peer or from outside your branch. *Advice* is a reply to a question you asked, even from above, and never binds you. Your team's mode says how an order binds: `absolute` (carry it out like the owner's instruction), `charter first` (carry it out unless your charter refuses it; then say no, quote the charter line, and it goes up one level) or `negotiate` (push back with a reason and a counter-proposal; your lead accepts, changes or overrules). Whatever the label, " + protocol.AlwaysHeldBack + " stay with the owner."

// Block returns the default marked block for a variant.
func Block(variant string) string { return BlockWithSnapshot(variant, settings.Snapshot{}) }

// BlockWithSnapshot renders network guidance from a settings snapshot.
func BlockWithSnapshot(variant string, snapshot settings.Snapshot) string {
	keys := []string{"intro_text", "peer_help_direct", "decide_not_defer", "owner_check_triggers", "cto_note_trust", "peer_contact_triggers", "ask_peer_after_tries", "relay_unedited", "message_style", "announce_task", "mcp_and_secret_hygiene", "register_projects_when_asked", "start_workers_when_asked", "wait_for_worker_notice", "report_late_defects", "worker_brief_precedence"}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		e, err := snapshot.Resolve("guidance."+key, settings.Where{})
		if err != nil {
			panic(err)
		}
		var value string
		if err := json.Unmarshal(e.Value, &value); err != nil {
			panic(err)
		}
		value = strings.ReplaceAll(value, "{{reply}}", protocol.NeutralizeMarkers(reply[variant]))
		value = strings.ReplaceAll(value, "{{handback}}", protocol.WorkerHandbackTool)
		parts = append(parts, value)
	}
	return Begin + "\n" + heading + "\n\n" + parts[0] + "\n\n" + strings.Join(parts[1:], "\n") + "\n" + fileSendGuidance + "\n" + handoffGuidance + "\n" + authorityGuidance + "\n" + End + "\n"
}

// Apply puts the block into the file content. It replaces an existing marked
// block, else migrates an unmarked section with the agent-net heading, else
// appends. changed is false when the content is already up to date.
func Apply(content, variant string) (out string, changed bool) {
	return ApplyWithSnapshot(content, variant, settings.Snapshot{})
}

func ApplyWithSnapshot(content, variant string, snapshot settings.Snapshot) (out string, changed bool) {
	block := BlockWithSnapshot(variant, snapshot)
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

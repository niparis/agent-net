package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A Claude Code session that waits for its user (decision 0057).
//
// Claude Code writes its own state into ~/.claude/sessions/<pid>.json: status
// "waiting" while a dialog blocks the turn, with "waitingFor" naming the
// dialog ("permission prompt", "input needed", "sandbox request", "worker
// request", "goal proposal", "dialog open"), and "busy" or "idle" again when
// the user answered. That file is the start and the end of the wait for every
// session, with or without agent-net's hooks. The words of the wait come from
// the transcript: the tool call that has no result yet. `agent-net
// needs-hook` gives the same words from the hook input, so both sources
// describe one wait.

// WaitFor describes a tool call that waits for the user: the question of
// AskUserQuestion, the plan approval of ExitPlanMode, or the tool and its main
// argument for a permission prompt.
func WaitFor(tool string, input map[string]any) protocol.Waiting {
	tool = strings.TrimSpace(tool)
	switch tool {
	case "AskUserQuestion":
		return askWait(input)
	case "ExitPlanMode":
		return protocol.Waiting{Kind: protocol.WaitingPermission, Text: "approve the plan"}
	}
	text := tool
	if summary := toolSummary(input); summary != "" {
		text += "(" + summary + ")"
	}
	return protocol.Waiting{Kind: protocol.WaitingPermission, Text: text}
}

// askWait is the first question of an AskUserQuestion call, with its option
// labels. More questions are counted, not listed: the line is short.
func askWait(input map[string]any) protocol.Waiting {
	w := protocol.Waiting{Kind: protocol.WaitingQuestion}
	qs, _ := input["questions"].([]any)
	if len(qs) == 0 {
		return w
	}
	first, _ := qs[0].(map[string]any)
	if q, ok := first["question"].(string); ok {
		w.Text = strings.Join(strings.Fields(q), " ")
	}
	if len(qs) > 1 && w.Text != "" {
		w.Text += fmt.Sprintf(" (+%d more)", len(qs)-1)
	}
	options, _ := first["options"].([]any)
	for _, o := range options {
		om, _ := o.(map[string]any)
		if label, ok := om["label"].(string); ok && strings.TrimSpace(label) != "" {
			w.Options = append(w.Options, strings.TrimSpace(label))
		}
	}
	return w
}

// toolSummary is the short form of a tool call: the command of a shell call,
// the path of a file call, or the first text field.
func toolSummary(in map[string]any) string {
	for _, key := range []string{"command", "file_path", "path", "url", "pattern", "query", "description", "prompt"} {
		if s, ok := in[key].(string); ok && strings.TrimSpace(s) != "" {
			return strings.Join(strings.Fields(s), " ")
		}
	}
	return ""
}

// harnessWait is the wait a session file says, or nil. A dialog the user
// opened in the session ("dialog open", for example /config) is not a wait for
// the owner: the user is at it. pending is the tool call the transcript shows
// without a result, or nil.
func harnessWait(status, waitingFor string, pending *protocol.Waiting) *protocol.Waiting {
	if status != "waiting" || waitingFor == "dialog open" {
		return nil
	}
	if pending != nil {
		w := *pending
		if waitingFor == "input needed" && w.Kind == protocol.WaitingPermission {
			// An MCP server asks for input during this tool call.
			w.Kind = protocol.WaitingInput
		}
		return &w
	}
	switch waitingFor {
	case "input needed":
		return &protocol.Waiting{Kind: protocol.WaitingInput}
	case "sandbox request":
		return &protocol.Waiting{Kind: protocol.WaitingPermission, Text: "network access"}
	case "goal proposal":
		return &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "approve the goal"}
	}
	return &protocol.Waiting{Kind: protocol.WaitingPermission}
}

// pendingTool reads the transcript from the end and returns the wait of the
// newest tool call of the main conversation that has no result yet. It stops
// at the user's last prompt: a tool call before it belongs to an earlier,
// finished or interrupted turn. Only the tool name and its short summary or
// question are kept, never other transcript text.
func pendingTool(path string, size int64) *protocol.Waiting {
	answered := map[string]bool{}
	var found *protocol.Waiting
	scanTail(path, size, func(line []byte) bool {
		if !bytes.Contains(line, []byte(`"user"`)) && !bytes.Contains(line, []byte(`"assistant"`)) {
			return false
		}
		var e struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			Message     struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &e) != nil || e.IsSidechain {
			return false
		}
		var blocks []struct {
			Type      string         `json:"type"`
			ID        string         `json:"id"`
			ToolUseID string         `json:"tool_use_id"`
			Name      string         `json:"name"`
			Input     map[string]any `json:"input"`
		}
		switch e.Type {
		case "user":
			if json.Unmarshal(e.Message.Content, &blocks) != nil {
				return true // a plain text prompt
			}
			results := false
			for _, b := range blocks {
				if b.Type == "tool_result" {
					answered[b.ToolUseID] = true
					results = true
				}
			}
			return !results // a prompt made of text blocks
		case "assistant":
			if json.Unmarshal(e.Message.Content, &blocks) != nil {
				return false
			}
			for i := len(blocks) - 1; i >= 0; i-- {
				b := blocks[i]
				if b.Type == "tool_use" && b.ID != "" && !answered[b.ID] {
					w := WaitFor(b.Name, b.Input)
					found = &w
					return true
				}
			}
		}
		return false
	})
	return found
}

type waitEntry struct {
	path  string
	size  int64
	mtime time.Time
	wait  *protocol.Waiting
}

// PendingTool returns the tool call of the session that waits for a result,
// or nil. The transcript is read again only when it changed.
func (r *modelReader) PendingTool(sessionID, cwd string) *protocol.Waiting {
	path := r.transcript(sessionID, cwd)
	if path == "" {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	r.mu.Lock()
	prev, had := r.waits[sessionID]
	r.mu.Unlock()
	if had && prev.path == path && prev.size == fi.Size() && prev.mtime.Equal(fi.ModTime()) {
		return prev.wait
	}
	w := pendingTool(path, fi.Size())
	r.mu.Lock()
	r.waits[sessionID] = waitEntry{path: path, size: fi.Size(), mtime: fi.ModTime(), wait: w}
	r.mu.Unlock()
	return w
}

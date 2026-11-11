package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/niparis/agent-net/internal/protocol"
)

// runOwnerOperation keeps the forwarding, audit and notice contract identical
// for every operation in the explicit registry. Backend validation stays with
// the existing owner handler.
func runOwnerOperation(method string, args json.RawMessage, reason string, call ownerOperationCall, undo func(any) ([]ownerUndoCall, error), audit func(string) error, notice func(string, []string) error) (any, error) {
	read, ok := protocol.CTOOperatorMethods()[method]
	if !ok {
		return nil, fmt.Errorf("unknown owner operation %q", method)
	}
	result, err := call(method, args)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(args, &fields)
	target := ""
	for _, key := range []string{"session_id", "old_session", "project", "team_id", "item", "id", "node_id"} {
		var value string
		if json.Unmarshal(fields[key], &value) == nil && cleanLine(value) != "" {
			target = " " + cleanLine(value)
			break
		}
	}
	line := "operator " + method + target
	if err != nil {
		line = "failed " + line + ": " + cleanLine(err.Error())
	}
	if e := audit(line); e != nil && err == nil {
		err = e
	}
	var preview protocol.SessionsArchiveRequest
	if method == "sessions_archive" && json.Unmarshal(args, &preview) == nil && preview.DryRun {
		read = true
	}
	if err != nil || read {
		return result, err
	}
	plan, err := undo(result)
	if err != nil {
		return result, err
	}
	var options []string
	if len(plan) == 1 {
		options = []string{plan[0].Method, string(plan[0].Arguments)}
	} else if len(plan) > 1 {
		raw, err := json.Marshal(map[string]any{"operations": plan})
		if err != nil {
			return result, err
		}
		options = []string{"operator_undo", string(raw)}
	}
	text := "CTO applied " + method
	if method == "session_enter" || method == "cto_enter" {
		text = "CTO requested " + method
	}
	text += target
	if reason = cleanLine(reason); reason != "" {
		text += ": " + reason
	}
	if err := notice(text, options); err != nil {
		return result, fmt.Errorf("%s; could not add Inbox notice: %w", text, err)
	}
	return result, nil
}

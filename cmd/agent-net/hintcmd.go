package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/hint"
	"github.com/niparis/agent-net/internal/protocol"
)

// hintBudget is how long `agent-net hint` may take in all. The Claude Code hook
// runs after a tool call, so it must be quick; it gives up silently.
const hintBudget = 280 * time.Millisecond

// hookInput is the part of the Claude Code hook input that the hint needs.
// PostToolUseFailure has "error" and no "tool_response"; PostToolUse has
// "tool_response" (https://code.claude.com/docs/en/hooks).
type hookInput struct {
	SessionID string `json:"session_id"`
	EventName string `json:"hook_event_name"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	Error        string          `json:"error"`
	IsInterrupt  bool            `json:"is_interrupt"`
	ToolResponse json.RawMessage `json:"tool_response"`
}

// hookOutput is what the hook prints when there is a suggestion.
type hookOutput struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

type bashToolResponse struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

func successfulBashText(in hookInput) string {
	var response bashToolResponse
	if json.Unmarshal(in.ToolResponse, &response) != nil {
		return ""
	}
	text := response.Stderr
	if len(response.Stdout) < 2000 && !readOnlyBashCommand(in.ToolInput.Command) {
		if text != "" && response.Stdout != "" {
			text += "\n"
		}
		text += response.Stdout
	}
	return text
}

var readCommands = map[string]bool{
	"cat": true, "head": true, "tail": true, "grep": true, "rg": true, "less": true, "jq": true,
}

var readGitCommands = map[string]bool{
	"diff": true, "log": true, "show": true, "grep": true, "blame": true,
}

// readOnlyBashCommand identifies common commands whose stdout is source or
// history rather than the result of an operation.
func readOnlyBashCommand(command string) bool {
	words := strings.Fields(command)
	commandStart := true
	for i, raw := range words {
		word, ended := shellCommandWord(raw)
		if word != "" && commandStart {
			name := filepath.Base(word)
			switch {
			case name == "git":
				if gitReadCommand(words[i+1:]) {
					return true
				}
				commandStart = false
			case name == "sed":
				if sedReadsLines(words[i+1:]) {
					return true
				}
				commandStart = false
			case readCommands[name]:
				return true
			case name == "sudo" || name == "env" || name == "command" || name == "time" || name == "nice" || name == "nohup":
				// These wrappers leave the actual command as the next word.
			default:
				commandStart = false
			}
		}
		if ended {
			commandStart = true
		}
	}
	return false
}

func gitReadCommand(words []string) bool {
	for i := 0; i < len(words); i++ {
		arg, ended := shellCommandWord(words[i])
		if arg == "" {
			if ended {
				return false
			}
			continue
		}
		if arg == "--" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if arg == "-C" || arg == "-c" || arg == "--git-dir" || arg == "--work-tree" || arg == "--namespace" {
				i++
			}
			continue
		}
		return readGitCommands[arg]
	}
	return false
}

func sedReadsLines(words []string) bool {
	for _, raw := range words {
		arg, ended := shellCommandWord(raw)
		if arg == "-n" || strings.HasPrefix(arg, "-n") || arg == "--silent" || arg == "--quiet" {
			return true
		}
		if ended {
			return false
		}
	}
	return false
}

func shellCommandWord(raw string) (word string, ended bool) {
	raw = strings.Trim(raw, "\"'")
	if i := strings.IndexAny(raw, ";|&"); i >= 0 {
		return strings.Trim(raw[:i], "()"), true
	}
	return strings.Trim(raw, "()"), false
}

// runHint is `agent-net hint`: a Claude Code hook for PostToolUseFailure and
// PostToolUse. It reads the hook input on stdin, asks the daemon for a hint and
// prints the hook output only when there is one. It never fails: on any
// problem it prints nothing and exits 0, so it can not disturb the session.
func runHint(ctx context.Context, stdin io.Reader, stdout io.Writer,
	ask func(ctx context.Context, req protocol.HintRequest) (protocol.HintResult, error)) {
	raw, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		return
	}
	var in hookInput
	if json.Unmarshal(raw, &in) != nil || in.SessionID == "" || in.IsInterrupt {
		return
	}
	var text string
	var failed bool
	switch in.EventName {
	case "PostToolUseFailure":
		text, failed = in.Error, true
	case "PostToolUse":
		// A command can fail without a non-zero exit (curl without --fail).
		// Only a shell command's output is looked at; a file read is not.
		if in.ToolName != "Bash" {
			return
		}
		text = successfulBashText(in)
		if !hint.LooksLikeFailure(text) {
			return
		}
	default:
		return
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, hintBudget)
	defer cancel()
	res, err := ask(ctx, protocol.HintRequest{Text: text, Failed: failed, Harness: "claude", NativeID: in.SessionID})
	if err != nil || res.Text == "" {
		return
	}
	var out hookOutput
	out.HookSpecificOutput.HookEventName = in.EventName
	out.HookSpecificOutput.AdditionalContext = res.Text
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	stdout.Write(append(b, '\n'))
}

func cmdHint(ctx context.Context, _ []string) error {
	runHint(ctx, os.Stdin, os.Stdout, func(ctx context.Context, req protocol.HintRequest) (protocol.HintResult, error) {
		var res protocol.HintResult
		cl, err := dialCLI(ctx)
		if err != nil {
			return res, err
		}
		defer cl.Close()
		raw, err := cl.Call(ctx, protocol.MethodHint, req)
		if err != nil {
			return res, err
		}
		err = json.Unmarshal(raw, &res)
		return res, err
	})
	return nil
}

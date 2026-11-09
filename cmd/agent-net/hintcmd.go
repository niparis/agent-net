package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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

// deniedRE matches the permission-denial wording a failed tool call carries.
// Claude Code's permission system says the action was denied; GitHub says a
// token or an app lacks the permission. It must stay permission specific, so
// an ordinary command error keeps the existing hint.
var deniedRE = regexp.MustCompile(`(?i)(` +
	`permission (to use [^\n]{0,160} |to [^\n]{0,160} )?(has been |was |is )?denied` +
	`|permissions? denied` +
	`|operation not permitted` +
	`|sandbox (denial|denied|violation)` +
	`|resource not accessible by (integration|personal access token)` +
	`|must have admin rights` +
	`|(do not|does not|don't) have (the )?(required )?permissions?` +
	`|insufficient permissions?` +
	`|not authorized to` +
	`)`)

// permissionDenied tells whether a failed call was refused by a permission
// check rather than by an ordinary command error.
func permissionDenied(text string) bool { return deniedRE.MatchString(text) }

// leadDeniedLine is the one line the hook adds for a denied step in a team
// that sends blocked steps to its lead. The member sends the message itself,
// so the PR link and the review travel with it.
func leadDeniedLine(res protocol.TeamLeadResult) string {
	return fmt.Sprintf("agent-net: this action was denied here. Your team sends blocked steps to its lead %s (%s): "+
		"send it what you need done (for example the PR link and your review) with agent_message_send, then carry on.",
		res.Lead, res.LeadSession)
}

func writeHookContext(stdout io.Writer, event, text string) {
	var out hookOutput
	out.HookSpecificOutput.HookEventName = event
	out.HookSpecificOutput.AdditionalContext = text
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	stdout.Write(append(b, '\n'))
}

// runHint is `agent-net hint`: a Claude Code hook for PostToolUseFailure and
// PostToolUse. It reads the hook input on stdin, asks the daemon for a hint and
// prints the hook output only when there is one. It never fails: on any
// problem it prints nothing and exits 0, so it can not disturb the session.
func runHint(ctx context.Context, stdin io.Reader, stdout io.Writer,
	ask func(ctx context.Context, req protocol.HintRequest) (protocol.HintResult, error),
	lead func(ctx context.Context, req protocol.TeamLeadRequest) (protocol.TeamLeadResult, error)) {
	raw, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		return
	}
	var in hookInput
	if json.Unmarshal(raw, &in) != nil || in.SessionID == "" || in.IsInterrupt {
		return
	}
	// Claude Code's permission system reports a denial in the failure's error
	// text (PostToolUseFailure). That step goes to the team lead, not to the
	// other sessions the ordinary hint looks for. A read-only command that
	// cannot read a file is an ordinary command error: it keeps the hint.
	if in.EventName == "PostToolUseFailure" && permissionDenied(in.Error) &&
		(in.ToolName != "Bash" || !readOnlyBashCommand(in.ToolInput.Command)) {
		ctx, cancel := context.WithTimeout(ctx, hintBudget)
		defer cancel()
		res, err := lead(ctx, protocol.TeamLeadRequest{Harness: "claude", NativeID: in.SessionID})
		if err != nil || res.Lead == "" || res.LeadSession == "" {
			return
		}
		writeHookContext(stdout, in.EventName, leadDeniedLine(res))
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
	writeHookContext(stdout, in.EventName, res.Text)
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
	}, func(ctx context.Context, req protocol.TeamLeadRequest) (protocol.TeamLeadResult, error) {
		var res protocol.TeamLeadResult
		cl, err := dialCLI(ctx)
		if err != nil {
			return res, err
		}
		defer cl.Close()
		raw, err := cl.Call(ctx, protocol.MethodTeamLead, req)
		if err != nil {
			return res, err
		}
		err = json.Unmarshal(raw, &res)
		return res, err
	})
	return nil
}

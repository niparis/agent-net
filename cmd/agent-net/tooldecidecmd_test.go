package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func runDecide(t *testing.T, input string, res protocol.ToolDecideResult, err error) (string, []protocol.ToolDecideRequest) {
	t.Helper()
	var asked []protocol.ToolDecideRequest
	var out bytes.Buffer
	runToolDecide(context.Background(), strings.NewReader(input), &out, func(_ context.Context, r protocol.ToolDecideRequest) (protocol.ToolDecideResult, error) {
		asked = append(asked, r)
		return res, err
	})
	return out.String(), asked
}

const decideInput = `{"session_id":"abc","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git push origin main"},"cwd":"/code/demo"}`

// An allow prints Claude Code's permissionDecision allow and nothing else. The
// request carries the session, the tool, its input and the folder.
func TestToolDecideHookPrintsAllow(t *testing.T) {
	out, asked := runDecide(t, decideInput, protocol.ToolDecideResult{Allow: true, Reason: "merge over demo"}, nil)
	var got toolDecideOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output %q: %v", out, err)
	}
	if got.HookSpecificOutput.HookEventName != "PreToolUse" || got.HookSpecificOutput.PermissionDecision != "allow" {
		t.Fatalf("output %+v", got)
	}
	if got.HookSpecificOutput.PermissionDecisionReason != "Delegated by the owner through a scope of this session's role." {
		t.Fatalf("reason %q", got.HookSpecificOutput.PermissionDecisionReason)
	}
	if strings.Contains(out, `"block"`) || strings.Contains(out, `"deny"`) {
		t.Fatalf("the hook must never block: %s", out)
	}
	if len(asked) != 1 || asked[0].Harness != "claude" || asked[0].NativeID != "abc" || asked[0].Tool != "Bash" || asked[0].Folder != "/code/demo" || !strings.Contains(string(asked[0].Input), "git push origin main") {
		t.Fatalf("asked %+v", asked)
	}
}

// Every other outcome prints nothing: no decision, an unreachable daemon, a
// hook event that is not PreToolUse, and input that is not understood.
func TestToolDecideHookPrintsNothingOtherwise(t *testing.T) {
	for name, c := range map[string]struct {
		input string
		res   protocol.ToolDecideResult
		err   error
	}{
		"no decision": {decideInput, protocol.ToolDecideResult{}, nil},
		"daemon down": {decideInput, protocol.ToolDecideResult{Allow: true}, errors.New("connection refused")},
		"wrong event": {strings.Replace(decideInput, "PreToolUse", "PostToolUse", 1), protocol.ToolDecideResult{Allow: true}, nil},
		"no session":  {strings.Replace(decideInput, `"session_id":"abc",`, "", 1), protocol.ToolDecideResult{Allow: true}, nil},
		"not json":    {"nope", protocol.ToolDecideResult{Allow: true}, nil},
		"no tool":     {strings.Replace(decideInput, `"tool_name":"Bash",`, "", 1), protocol.ToolDecideResult{Allow: true}, nil},
	} {
		if out, _ := runDecide(t, c.input, c.res, c.err); out != "" {
			t.Errorf("%s: printed %q", name, out)
		}
	}
}

// runHookCommand runs a hidden hook command the way a Claude session does: the
// payload arrives on stdin and the hook JSON goes to stdout.
func runHookCommand(t *testing.T, command, input string) (stdout, stderr string, code int) {
	t.Helper()
	payload, err := os.CreateTemp(t.TempDir(), "hook-input")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := payload.WriteString(input); err != nil {
		t.Fatal(err)
	}
	if _, err := payload.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = payload
	t.Cleanup(func() { os.Stdin = oldStdin })
	var errOut strings.Builder
	stdout = captureStdout(t, func() {
		code = run(context.Background(), []string{command}, &errOut)
	})
	return stdout, errOut.String(), code
}

const ctoOpDecideInput = `{"session_id":"cto_fake","hook_event_name":"PreToolUse","tool_name":"mcp__agent-net-cto__cto_op","tool_input":{"method":"gateway_backup"},"cwd":"/code/cto"}`

// A CTO session launched before v0.5.56 still has `agent-net cto-hook` in its
// settings. It must make the same decision as `agent-net tool-decide`, or every
// cto_op call in that session fails with "unknown command".
func TestCTOHookAliasMatchesToolDecide(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	newFakeDaemon(t, sock, "v_test")
	t.Setenv("AGENT_NET_SOCKET", sock)

	want, wantErr, wantCode := runHookCommand(t, "tool-decide", ctoOpDecideInput)
	if wantCode != 0 || wantErr != "" {
		t.Fatalf("tool-decide exit %d, stderr %q", wantCode, wantErr)
	}
	if !strings.Contains(want, `"permissionDecision":"allow"`) {
		t.Fatalf("tool-decide did not allow the cto_op call: %q", want)
	}

	got, gotErr, gotCode := runHookCommand(t, "cto-hook", ctoOpDecideInput)
	if strings.Contains(gotErr, "unknown command") {
		t.Fatalf("cto-hook is not a command: %q", gotErr)
	}
	if gotCode != 0 || gotErr != "" {
		t.Fatalf("cto-hook exit %d, stderr %q", gotCode, gotErr)
	}
	if got != want {
		t.Fatalf("cto-hook printed %q, tool-decide printed %q", got, want)
	}
}

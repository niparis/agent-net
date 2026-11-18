package mcpserver

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// A worker has no agent_announce tool, so the reminder to call it could never
// be cleared. No tool result of a worker carries it.
func TestWorkerResultsHaveNoAnnounceReminder(t *testing.T) {
	t.Setenv("AGENT_NET_WORKER", "1")
	cs := connectServer(t, func(context.Context, string, any) (json.RawMessage, error) {
		return json.RawMessage(`{"items":[],"total":0}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_messages_find", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if hasReminder(res) {
		t.Fatalf("a worker's tool result ends with the announce reminder: %+v", res.Content)
	}
}

// The announce rule is for agents. A worker's instructions leave it out.
func TestWorkerStartInstructionsLeaveOutTheAnnounceRule(t *testing.T) {
	t.Setenv("AGENT_NET_WORKER", "1")
	got := StartInstructions(context.Background(), charterCaller(""))
	if strings.Contains(got, "agent_announce") {
		t.Fatalf("a worker's instructions name agent_announce: %s", got)
	}
}

var agentToolName = regexp.MustCompile(`agent_[a-z]+(?:_[a-z]+)*`)

// Every agent_* tool that text a worker reads names must be in the worker's
// tool list. The texts are the brief for each harness, the continuation text,
// the server instructions and the tool descriptions.
func TestWorkerFacingTextNamesOnlyWorkerTools(t *testing.T) {
	t.Setenv("AGENT_NET_WORKER", "1")
	ctx := context.Background()
	cs := connectServer(t, func(context.Context, string, any) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	})
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]bool{}
	var texts []string
	for _, tl := range list.Tools {
		tools[tl.Name] = true
		texts = append(texts, tl.Description)
	}
	snap := settings.Snapshot{}
	for _, harness := range []string{"claude", "codex", "opencode", "pi"} {
		texts = append(texts, protocol.WorkerTaskBodyWithSettingsForRepo(harness, "do the task", "/work/wt", "agent-net/w_1", "", "", "", snap, ""))
	}
	texts = append(texts,
		protocol.ContinueIntroWithSettings("the owner replied", "the branch", snap, ""),
		protocol.WorkerAutoContinueWithSettings(snap, ""),
		StartInstructions(ctx, charterCaller("")),
	)
	for _, text := range texts {
		for _, name := range agentToolName.FindAllString(text, -1) {
			if !tools[name] {
				t.Errorf("worker-facing text names %s, which is not in the worker's tools: %q", name, text)
			}
		}
	}
}

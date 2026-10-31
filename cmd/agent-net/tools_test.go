package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestPrintTools(t *testing.T) {
	var out bytes.Buffer
	printTools(&out, []protocol.ToolItem{{Name: "sample", Kind: "skill", Global: true, Projects: []string{"demo"}, Harnesses: []string{"codex", "claude"}, Machines: []string{"one", "two"}, Variants: 2, State: "stale", Desired: &protocol.ToolRevision{Assignment: protocol.ToolAssignment{Scope: "projects", Projects: []string{"demo"}}}, Applied: "pending"}})
	for _, part := range []string{"NAME", "KIND", "SCOPE", "HARNESSES", "MACHINES", "VARIANTS", "STATE", "DESIRED", "APPLIED", "pending", "sample", "skill", "global", "demo", "codex", "one", "2", "stale"} {
		if !strings.Contains(out.String(), part) {
			t.Errorf("missing %q in %q", part, out.String())
		}
	}
}

type toolsTestClient struct{}

func (toolsTestClient) Call(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if method != "tools_items" {
		return nil, fmt.Errorf("unexpected method %s", method)
	}
	return json.RawMessage(`{"items":[{"name":"sample","kind":"skill","global":true,"harnesses":["codex"],"machines":["fixture"],"variants":1,"state":"ok"}]}`), nil
}
func TestReadToolsUsesInventory(t *testing.T) {
	var output bytes.Buffer
	if err := readTools(context.Background(), toolsTestClient{}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "fixture") || !strings.Contains(output.String(), "sample") {
		t.Fatal(output.String())
	}
}

type toolCommandsClient struct {
	calls []string
	args  []any
}

func (c *toolCommandsClient) Call(_ context.Context, method string, args any) (json.RawMessage, error) {
	c.calls = append(c.calls, method)
	c.args = append(c.args, args)
	return json.RawMessage(`{"ok":true}`), nil
}
func TestToolCommands(t *testing.T) {
	c := &toolCommandsClient{}
	var out bytes.Buffer
	for _, args := range [][]string{{"plan", "item", "project-a", "project-b"}, {"apply", "plan-1"}, {"history", "item"}} {
		if err := runTools(context.Background(), c, &out, args); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(c.calls, ",") != "tools_plan,tools_apply,tools_history" {
		t.Fatalf("calls: %v", c.calls)
	}
	req, ok := c.args[0].(protocol.ToolPlanRequest)
	if !ok || req.Target.Scope != "projects" || strings.Join(req.Target.Projects, ",") != "project-a,project-b" {
		t.Fatalf("request: %+v", c.args[0])
	}
	if err := runTools(context.Background(), c, &out, []string{"plan", "item", "global", "project"}); err == nil {
		t.Fatal("accepted invalid scope")
	}
}

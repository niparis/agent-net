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
	printTools(&out, []protocol.ToolItem{{Name: "sample", Kind: "skill", Global: true, Projects: []string{"demo"}, Harnesses: []string{"codex", "claude"}, Machines: []string{"one", "two"}, Variants: 2, State: "stale"}})
	for _, part := range []string{"NAME", "KIND", "SCOPE", "HARNESSES", "MACHINES", "VARIANTS", "STATE", "sample", "skill", "global", "demo", "codex", "one", "2", "stale"} {
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

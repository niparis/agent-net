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
	printTools(&out, []protocol.InventoryReport{{Machine: "fixture", Items: []protocol.InventoryItem{{Name: "sample", Kind: protocol.InventorySkill, Harness: "codex", Scope: protocol.InventoryUser, State: protocol.InventoryOK}}}})
	for _, part := range []string{"NAME", "KIND", "HARNESS", "SCOPE", "PROJECT", "MACHINE", "STATE", "sample", "skill", "codex", "fixture"} {
		if !strings.Contains(out.String(), part) {
			t.Errorf("missing %q in %q", part, out.String())
		}
	}
}

type toolsTestClient struct{}

func (toolsTestClient) Call(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if method != "inventory_get" {
		return nil, fmt.Errorf("unexpected method %s", method)
	}
	return json.RawMessage(`{"reports":[{"machine":"fixture","items":[{"name":"sample","kind":"skill","harness":"codex","scope":"user","state":"ok"}]}]}`), nil
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

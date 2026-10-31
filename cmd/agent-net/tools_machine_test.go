package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestToolPlanOnChosenMachines(t *testing.T) {
	c := &toolCommandsClient{}
	var out bytes.Buffer
	if err := runTools(context.Background(), c, &out, []string{"plan", "item", "global", "--on", "research", "--on", "n_mac"}); err != nil {
		t.Fatal(err)
	}
	req, ok := c.args[0].(protocol.ToolPlanRequest)
	if !ok || req.Target.Scope != "global" || strings.Join(req.Target.Machines, ",") != "research,n_mac" {
		t.Fatalf("request: %+v", c.args[0])
	}
	if err := runTools(context.Background(), c, &out, []string{"plan", "item", "uninstalled", "--on"}); err == nil {
		t.Fatal("accepted --on without a machine")
	}
}

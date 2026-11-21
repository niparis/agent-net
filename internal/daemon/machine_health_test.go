package daemon

import (
	"context"
	"os"
	"runtime"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestMachineHealthRoundTrip(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory})
	var out protocol.MachineGetResult
	eventually(t, "health report", func() bool {
		var err error
		out, err = g.gw.MachineGet(context.Background(), protocol.MachineGetRequest{Machine: n.d.NodeID()})
		return err == nil && out.Health.Status == "reported"
	})
	// Linux also lists the tools of Claude Code's sandbox (claude_sandbox.go).
	wantHelpers := 8
	if runtime.GOOS == "linux" {
		wantHelpers += len(claudeSandboxTools)
	}
	if out.Machine.Version == "" || out.Health.Path != os.Getenv("PATH") || out.Health.RestartTime == "" || out.Health.UptimeSeconds == nil || out.Health.StateFreeBytes == nil || len(out.Health.Helpers) != wantHelpers {
		t.Fatalf("health: %+v", out)
	}
	t.Logf("sample: %+v", out.Health)
}

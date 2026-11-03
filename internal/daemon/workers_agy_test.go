package daemon

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// agyInventory is the fake inventory with Antigravity installed and signed in.
func agyInventory(ctx context.Context) []protocol.HarnessInfo {
	return append(fakeInventory(ctx), protocol.HarnessInfo{
		Name: "agy", Path: os.Args[0], Version: "1.4.12", SignedIn: "yes",
		Models: []string{"gemini-3.8-flash-low", "claude-sonnet-5-5-medium"},
	})
}

// A profile can name agy now (decision 0049, build step 2), but a worker
// cannot start it until step 4.
func TestAgyProfileIsAcceptedButNotStartable(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNodeWithDetect(t, g, shortSet, agyInventory, nil)
	w.register(t, newRepo(t, "shop"))
	raw, err := w.cli.call(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "agy", Model: "gemini-3.8-flash-low"})
	if err != nil {
		t.Fatal(err)
	}
	var added protocol.WorkerProfile
	if err := json.Unmarshal(raw, &added); err != nil || added.Harness != "agy" || added.Model != "gemini-3.8-flash-low" {
		t.Fatalf("profile: %s %v", raw, err)
	}
	raw = w.cli.mustCall(protocol.OpWorkerProfiles, nil)
	var profiles []protocol.WorkerProfile
	if err := json.Unmarshal(raw, &profiles); err != nil || len(profiles) != 1 || profiles[0].Harness != "agy" {
		t.Fatalf("list %s %v", raw, err)
	}
	// The profile is not stale: the agy model is listed on this machine.
	if available := profiles[0].Available; available != nil && !*available {
		t.Fatalf("agy model is listed, so the profile is not stale: %+v", profiles[0])
	}
	if protocol.WorkerHarnessSupported("agy") {
		t.Fatal("agy must not start workers in step 2")
	}
	_, err = w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "workers can run these harnesses") {
		t.Fatalf("agy start: %v", err)
	}
}

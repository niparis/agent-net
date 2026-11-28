package daemon

import (
	"context"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The refusal comes before any call: it needs no gateway and no socket, so it
// also runs in a sandbox that blocks unix sockets.
func TestOverrideRefusedBeforeAnyCall(t *testing.T) {
	d := &Daemon{}
	ctx := context.WithValue(context.Background(), answerEarlyKey{}, true)
	res, err := d.workerStartCall(ctx, "s_caller", protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m2",
		Task: "do the work", Title: "Do the work", Reason: "an independent look"}, false, "")
	if err == nil || err.Error() != protocol.OverrideNeedsOwner || res != nil {
		t.Fatalf("res %+v, err %v", res, err)
	}
}

package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

// A profile add waits for the gateway's own check limit. The gateway reads
// each machine's model list live, which a slow machine or a loaded host makes
// slow. With the plain 20 s call wait the daemon gave up first, the test
// failed with "gateway did not answer in time", and the gateway added the
// profile after the caller had seen the error.
func TestWorkerProfileAddWaitsForTheGatewayCheck(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var wait time.Duration
	w := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.testGatewayCall = func(ctx context.Context, typ string, _ any) (json.RawMessage, bool, error) {
			if typ != "worker.profile.add" {
				return nil, false, nil
			}
			wait = callWait(ctx)
			return json.RawMessage(`{"name":"strong","harness":"opencode","model":"fake/m1"}`), true, nil
		}
	})
	w.register(t, newRepo(t, "shop"))
	w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Name: "strong", Harness: "opencode", Model: "fake/m1"})
	if wait < gateway.ProfileCheckTimeout {
		t.Fatalf("the add waited %s, less than the gateway's check limit %s", wait, gateway.ProfileCheckTimeout)
	}
}

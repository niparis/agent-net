package operator

import (
	"context"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"testing"
)

func TestArchiveGraphNodeUsesOwnerLifecycle(t *testing.T) {
	d, path := startFake(t, func(method string, params json.RawMessage) (any, string) { return map[string]any{}, "" })
	c := New(path)
	defer c.Close()
	for _, kind := range []string{"repo", "agent"} {
		ref := protocol.GraphRef{Kind: kind, ID: "throwaway"}
		if err := c.ArchiveGraphNode(context.Background(), ref); err != nil {
			t.Fatal(err)
		}
		method := protocol.OpRepoArchive
		if kind == "agent" {
			method = protocol.OpAgentTombstone
		}
		var got protocol.GraphLifecycleRequest
		if err := json.Unmarshal(d.params[method], &got); err != nil || got.Ref == nil || *got.Ref != ref {
			t.Fatalf("%s: %+v %v", method, got, err)
		}
	}
}

package mcpserver

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"testing"
)

func TestAntigravityMetadata(t *testing.T) {
	var got string
	h := metadataThread("antigravity.google/root_conversation_id")(func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		got, _ = ctx.Value(threadKey{}).(string)
		return nil, nil
	})
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}}
	req.Params.SetMeta(map[string]any{"antigravity.google/root_conversation_id": "conversation-1"})
	_, _ = h(context.Background(), "tools/call", req)
	if got != "conversation-1" {
		t.Fatalf("id=%q", got)
	}
}

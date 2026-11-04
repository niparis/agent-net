package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// The host's part of resources and prompts: it lists them (with a short
// cache that a list_changed notification ends), reads a resource and gets a
// prompt, with the server's timeout, the same redaction as tool results and a
// size limit. A read or prompt larger than the limit is refused, not cut.

type cachedItems struct {
	at  time.Time
	raw json.RawMessage
}

// feature serves one resource or prompt request for a hosted server.
func (h *mcpHost) feature(ctx context.Context, p protocol.MCPFeatureInvoke) (json.RawMessage, error) {
	r := h.runner(p.Server)
	if r == nil {
		return nil, fmt.Errorf("server %q is not running on this node", p.Server)
	}
	return r.feature(ctx, p)
}

func (r *mcpRunner) dropItems() {
	r.mu.Lock()
	r.items = nil
	r.itemsGen++
	r.mu.Unlock()
}

// reportItemsChanged tells the gateway that the server's resources or prompts
// changed, so every session is told to list them again.
func (r *mcpRunner) reportItemsChanged() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := r.h.d.call(ctx, "mcp.items_changed", protocol.MCPItemsChanged{Name: r.spec.Name}); err != nil {
		r.logf("report changed resources or prompts to gateway: %v", err)
	}
}

func (r *mcpRunner) feature(ctx context.Context, p protocol.MCPFeatureInvoke) (json.RawMessage, error) {
	if !protocol.ValidMCPOp(p.Op) {
		return nil, fmt.Errorf("unknown op %q", p.Op)
	}
	sess, red, spec, err := r.ready(ctx, p.TimeoutSec)
	if err != nil {
		return nil, err
	}
	caps := &mcp.ServerCapabilities{}
	if init := sess.InitializeResult(); init != nil && init.Capabilities != nil {
		caps = init.Capabilities
	}
	resources := p.Op == protocol.MCPOpResources || p.Op == protocol.MCPOpTemplates || p.Op == protocol.MCPOpRead
	if (resources && caps.Resources == nil) || (!resources && caps.Prompts == nil) {
		if protocol.MCPListOp(p.Op) {
			return json.RawMessage(`{"items":[]}`), nil
		}
		what := "resources"
		if !resources {
			what = "prompts"
		}
		return nil, fmt.Errorf("MCP server %q has no %s", p.Server, what)
	}
	if protocol.MCPListOp(p.Op) {
		return r.listItems(ctx, sess, red, p.Op)
	}

	ctx, cancel := context.WithTimeout(ctx, spec.Timeout())
	defer cancel()
	what := fmt.Sprintf("resource %q", p.URI)
	var res any
	if p.Op == protocol.MCPOpRead {
		res, err = sess.ReadResource(ctx, &mcp.ReadResourceParams{URI: p.URI})
	} else {
		what = fmt.Sprintf("prompt %q", p.Name)
		res, err = sess.GetPrompt(ctx, &mcp.GetPromptParams{Name: p.Name, Arguments: p.Arguments})
	}
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%s timed out after %s", what, spec.Timeout())
		}
		return nil, red.wrap(err)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}
	raw = red.applyBytes(raw)
	if len(raw) > protocol.MCPMaxResultBytes {
		return nil, fmt.Errorf("%s of MCP server %q is %d bytes, more than the limit of %d bytes (binary data counts as base64); agent-net does not cut resources or prompts",
			what, p.Server, len(raw), protocol.MCPMaxResultBytes)
	}
	return raw, nil
}

// listItems returns {"items": [...]}: the server's resources, templates or
// prompts, at most MCPMaxListItems and MCPMaxResultBytes, from the cache when
// it is fresh.
func (r *mcpRunner) listItems(ctx context.Context, sess *mcp.ClientSession, red redactor, op string) (json.RawMessage, error) {
	r.mu.Lock()
	c, ok := r.items[op]
	gen := r.itemsGen
	r.mu.Unlock()
	if ok && time.Since(c.at) < r.h.listTTL {
		return c.raw, nil
	}
	ctx, cancel := context.WithTimeout(ctx, protocol.MCPListTimeout)
	defer cancel()
	var items []any
	cursor := ""
	for page := 0; page < 20 && len(items) < protocol.MCPMaxListItems; page++ {
		var next string
		switch op {
		case protocol.MCPOpResources:
			res, err := sess.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
			if err != nil {
				return nil, r.listError(ctx, op, red, err)
			}
			for _, it := range res.Resources {
				items = append(items, it)
			}
			next = res.NextCursor
		case protocol.MCPOpTemplates:
			res, err := sess.ListResourceTemplates(ctx, &mcp.ListResourceTemplatesParams{Cursor: cursor})
			if err != nil {
				return nil, r.listError(ctx, op, red, err)
			}
			for _, it := range res.ResourceTemplates {
				items = append(items, it)
			}
			next = res.NextCursor
		case protocol.MCPOpPrompts:
			res, err := sess.ListPrompts(ctx, &mcp.ListPromptsParams{Cursor: cursor})
			if err != nil {
				return nil, r.listError(ctx, op, red, err)
			}
			for _, it := range res.Prompts {
				items = append(items, it)
			}
			next = res.NextCursor
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(items) > protocol.MCPMaxListItems {
		items = items[:protocol.MCPMaxListItems]
	}
	if items == nil {
		items = []any{}
	}
	var raw []byte
	for {
		b, err := json.Marshal(map[string]any{"items": items})
		if err != nil {
			return nil, err
		}
		raw = red.applyBytes(b)
		if len(raw) <= protocol.MCPMaxResultBytes || len(items) == 0 {
			break
		}
		items = items[:len(items)*9/10]
	}
	if len(items) == 0 && len(raw) > protocol.MCPMaxResultBytes {
		raw = []byte(`{"items":[]}`)
	}
	r.mu.Lock()
	if r.itemsGen == gen {
		if r.items == nil {
			r.items = map[string]cachedItems{}
		}
		r.items[op] = cachedItems{at: time.Now(), raw: raw}
	}
	r.mu.Unlock()
	return raw, nil
}

func (r *mcpRunner) listError(ctx context.Context, op string, red redactor, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%s timed out after %s", op, protocol.MCPListTimeout)
	}
	return red.wrap(err)
}

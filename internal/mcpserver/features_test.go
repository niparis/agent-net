package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// featureFake answers mcp_feature like the daemon: two servers, "a" on host-a
// and "b" on host-b, with the same resource URIs and prompt names, and an
// offline server "c".
type featureFake struct {
	mu    sync.Mutex
	calls []protocol.MCPFeature
}

func (f *featureFake) call(_ context.Context, method string, params any) (json.RawMessage, error) {
	if method == "mcp_tools" {
		return json.RawMessage(`{"servers":[]}`), nil
	}
	if method != "mcp_feature" {
		return nil, errors.New("unexpected " + method)
	}
	raw, _ := json.Marshal(params)
	var p protocol.MCPFeature
	json.Unmarshal(raw, &p)
	f.mu.Lock()
	f.calls = append(f.calls, p)
	f.mu.Unlock()
	items := func(arr string) json.RawMessage {
		out := []protocol.MCPServerItems{
			{Server: "a", Host: "host-a", Items: json.RawMessage(arr)},
			{Server: "b", Host: "host-b", Items: json.RawMessage(arr)},
			{Server: "c", Host: "host-c", Error: "host host-c offline"},
		}
		b, _ := json.Marshal(map[string]any{"servers": out})
		return b
	}
	switch p.Op {
	case protocol.MCPOpResources:
		return items(`[{"uri":"test://hello","name":"hello","description":"A greeting.","mimeType":"text/plain"}]`), nil
	case protocol.MCPOpTemplates:
		return items(`[{"uriTemplate":"test://item/{id}","name":"item","description":"One item."}]`), nil
	case protocol.MCPOpPrompts:
		return items(`[{"name":"greet","description":"Greet someone.","arguments":[{"name":"name","required":true}]}]`), nil
	case protocol.MCPOpRead:
		switch p.URI {
		case "test://big":
			return nil, errors.New(`resource "test://big" of MCP server "a" is 700000 bytes, more than the limit of 524288 bytes`)
		case "test://logo.png":
			return json.RawMessage(`{"contents":[{"uri":"test://logo.png","mimeType":"image/png","blob":"iVBORw=="}]}`), nil
		}
		return json.Marshal(map[string]any{"contents": []map[string]any{{"uri": p.URI, "mimeType": "text/plain", "text": "text of " + p.URI + " on " + p.Server}}})
	case protocol.MCPOpPrompt:
		return json.Marshal(map[string]any{"description": "a greeting", "messages": []map[string]any{
			{"role": "user", "content": map[string]any{"type": "text", "text": "Hello, " + p.Arguments["name"] + " from " + p.Server}},
			{"role": "user", "content": map[string]any{"type": "resource_link", "uri": "test://hello", "name": "hello"}},
		}})
	}
	return nil, errors.New("unexpected op " + p.Op)
}

func (f *featureFake) last() protocol.MCPFeature {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

type featureClient struct {
	cs               *mcp.ClientSession
	resourcesChanged chan struct{}
	promptsChanged   chan struct{}
}

func connectFeatures(t *testing.T, call Caller) (*mcp.Server, *featureClient) {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	srv := New(call, "")
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	fc := &featureClient{resourcesChanged: make(chan struct{}, 8), promptsChanged: make(chan struct{}, 8)}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, &mcp.ClientOptions{
		ResourceListChangedHandler: func(context.Context, *mcp.ResourceListChangedRequest) { fc.resourcesChanged <- struct{}{} },
		PromptListChangedHandler:   func(context.Context, *mcp.PromptListChangedRequest) { fc.promptsChanged <- struct{}{} },
	}).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	fc.cs = cs
	return srv, fc
}

// The server says it has resources and prompts, even before any registered
// server has one, so a client asks for them later.
func TestFeaturesCapabilitiesAdvertised(t *testing.T) {
	_, fc := connectFeatures(t, (&featureFake{}).call)
	caps := fc.cs.InitializeResult().Capabilities
	if caps.Resources == nil || !caps.Resources.ListChanged || caps.Prompts == nil || !caps.Prompts.ListChanged {
		t.Fatalf("capabilities = %+v", caps)
	}
}

// Two servers with the same resource URI and the same names: each shows once,
// under its own server, and an offline server is left out.
func TestFeaturesResourcesPrefixedByServer(t *testing.T) {
	fake := &featureFake{}
	_, fc := connectFeatures(t, fake.call)
	ctx := context.Background()
	res, err := fc.cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range res.Resources {
		got = append(got, r.URI+" "+r.Name+" "+r.Description+" "+r.MIMEType)
	}
	sort.Strings(got)
	want := []string{
		"agent-net://a/test://hello a__hello [a on host-a] A greeting. text/plain",
		"agent-net://b/test://hello b__hello [b on host-b] A greeting. text/plain",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("resources:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if p := fake.last(); p.Op != protocol.MCPOpResources || p.Server != "" {
		t.Fatalf("daemon call = %+v", p)
	}

	tl, err := fc.cs.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got = nil
	for _, r := range tl.ResourceTemplates {
		got = append(got, r.URITemplate+" "+r.Name)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "agent-net://a/test://item/{id} a__item,agent-net://b/test://item/{id} b__item" {
		t.Fatalf("templates = %v", got)
	}
}

// A read goes to the server named in the URI, with the server's own URI, and
// the answer carries the agent-net URI again. Blobs keep their mime type.
func TestFeaturesReadRoutesByServer(t *testing.T) {
	fake := &featureFake{}
	_, fc := connectFeatures(t, fake.call)
	ctx := context.Background()

	res, err := fc.cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "agent-net://b/test://item/7"})
	if err != nil {
		t.Fatal(err)
	}
	if p := fake.last(); p.Op != protocol.MCPOpRead || p.Server != "b" || p.URI != "test://item/7" {
		t.Fatalf("daemon call = %+v", p)
	}
	if len(res.Contents) != 1 || res.Contents[0].URI != "agent-net://b/test://item/7" || res.Contents[0].Text != "text of test://item/7 on b" {
		t.Fatalf("contents = %+v", res.Contents[0])
	}

	res, err = fc.cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "agent-net://a/test://logo.png"})
	if err != nil {
		t.Fatal(err)
	}
	if c := res.Contents[0]; c.MIMEType != "image/png" || string(c.Blob) != "\x89PNG" || c.URI != "agent-net://a/test://logo.png" {
		t.Fatalf("blob = %+v", c)
	}

	// The host's refusal reaches the client as it is.
	_, err = fc.cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "agent-net://a/test://big"})
	if err == nil || !strings.Contains(err.Error(), "more than the limit") {
		t.Fatalf("oversize read: %v", err)
	}
	// A URI that is not an agent-net one is not found, and the daemon is not asked.
	n := len(fake.calls)
	for _, uri := range []string{"test://hello", "agent-net://", "agent-net://a", "agent-net://Bad_Name/test://x", "agent-net://a/"} {
		if _, err := fc.cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri}); err == nil {
			t.Fatalf("read %q: want an error", uri)
		}
	}
	if len(fake.calls) != n {
		t.Fatalf("the daemon was asked for a bad URI: %+v", fake.calls[n:])
	}
}

// Prompts show as <server>__<prompt>, and a get passes the arguments to the
// right server. A resource link in a prompt gets the agent-net URI.
func TestFeaturesPromptsPrefixedByServer(t *testing.T) {
	fake := &featureFake{}
	_, fc := connectFeatures(t, fake.call)
	ctx := context.Background()
	res, err := fc.cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range res.Prompts {
		got = append(got, p.Name+" "+p.Description)
		if len(p.Arguments) != 1 || p.Arguments[0].Name != "name" || !p.Arguments[0].Required {
			t.Fatalf("arguments of %s = %+v", p.Name, p.Arguments)
		}
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "a__greet [a on host-a] Greet someone.,b__greet [b on host-b] Greet someone." {
		t.Fatalf("prompts = %v", got)
	}

	out, err := fc.cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "b__greet", Arguments: map[string]string{"name": "Ann"}})
	if err != nil {
		t.Fatal(err)
	}
	if p := fake.last(); p.Op != protocol.MCPOpPrompt || p.Server != "b" || p.Name != "greet" || p.Arguments["name"] != "Ann" {
		t.Fatalf("daemon call = %+v", p)
	}
	if txt := out.Messages[0].Content.(*mcp.TextContent).Text; txt != "Hello, Ann from b" {
		t.Fatalf("message = %q", txt)
	}
	if link := out.Messages[1].Content.(*mcp.ResourceLink); link.URI != "agent-net://b/test://hello" {
		t.Fatalf("resource link = %+v", link)
	}
	for _, name := range []string{"greet", "__greet", "b__", "Bad_Name__greet"} {
		if _, err := fc.cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: name}); err == nil {
			t.Fatalf("get %q: want an error", name)
		}
	}
}

// A registry change pushed by the daemon reaches the client as
// notifications/resources/list_changed and notifications/prompts/list_changed.
// The timer of the watch does not send them.
func TestFeaturesListChangedOnRegistryPush(t *testing.T) {
	fake := &featureFake{}
	srv, fc := connectFeatures(t, fake.call)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The server notifies only sessions it has seen initialized.
	if _, err := fc.cs.ListPrompts(ctx, nil); err != nil {
		t.Fatal(err)
	}
	trigger := make(chan struct{}, 1)
	go NewDynamic(srv, fake.call).Watch(ctx, trigger, 50*time.Millisecond)
	select {
	case <-fc.resourcesChanged:
		t.Fatal("list_changed without a push")
	case <-fc.promptsChanged:
		t.Fatal("list_changed without a push")
	case <-time.After(300 * time.Millisecond):
	}
	trigger <- struct{}{}
	for _, ch := range []chan struct{}{fc.resourcesChanged, fc.promptsChanged} {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal("no list_changed after a push")
		}
	}
	// The marker used to send the notification is never listed.
	res, err := fc.cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Resources {
		if !strings.HasPrefix(r.URI, "agent-net://a/") && !strings.HasPrefix(r.URI, "agent-net://b/") {
			t.Fatalf("unexpected resource %+v", r)
		}
	}
}

// When the daemon cannot be reached, lists and reads fail with its error.
func TestFeaturesDaemonDown(t *testing.T) {
	_, fc := connectFeatures(t, func(context.Context, string, any) (json.RawMessage, error) { return nil, ErrUnreachable })
	ctx := context.Background()
	if _, err := fc.cs.ListResources(ctx, nil); err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("list: %v", err)
	}
	if _, err := fc.cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "agent-net://a/test://hello"}); err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("read: %v", err)
	}
}

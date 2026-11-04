package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// Resources and prompts of the registered servers. They are not stored in
// this process: each resources/list, resources/templates/list and
// prompts/list asks the daemon, which asks the hosts (they keep a short
// cache). A resource shows as agent-net://<server>/<the server's own URI>,
// a template the same way, and a prompt as <server>__<prompt>, so two servers
// never clash. A read or a get goes to the server the URI or the name gives.
// The gateway checks access on every request, as for tools.

// resourceScheme starts the URI of every registry resource.
const resourceScheme = "agent-net://"

// featureCapabilities says the server has resources and prompts, even before a
// registered server has any, so the client asks for them later. Tools are
// added by the SDK.
func featureCapabilities() *mcp.ServerCapabilities {
	return &mcp.ServerCapabilities{
		Logging:   &mcp.LoggingCapabilities{},
		Resources: &mcp.ResourceCapabilities{ListChanged: true},
		Prompts:   &mcp.PromptCapabilities{ListChanged: true},
	}
}

func resourceURI(server, uri string) string { return resourceScheme + server + "/" + uri }

// splitResourceURI returns the server and its own URI from an agent-net URI.
func splitResourceURI(uri string) (server, own string, ok bool) {
	rest, found := strings.CutPrefix(uri, resourceScheme)
	if !found {
		return "", "", false
	}
	server, own, found = strings.Cut(rest, "/")
	if !found || own == "" || protocol.ValidMCPName(server) != nil {
		return "", "", false
	}
	return server, own, true
}

// splitPromptName returns the server and its own prompt name.
func splitPromptName(name string) (server, own string, ok bool) {
	server, own, found := strings.Cut(name, "__")
	if !found || own == "" || protocol.ValidMCPName(server) != nil {
		return "", "", false
	}
	return server, own, true
}

func describe(s protocol.MCPServerItems, desc string) string {
	if desc == "" {
		return fmt.Sprintf("[%s on %s]", s.Server, s.Host)
	}
	return fmt.Sprintf("[%s on %s] %s", s.Server, s.Host, desc)
}

// featureProxy is a receiving middleware that answers the resource and prompt
// methods from the registry. Other methods pass through.
func featureProxy(call Caller) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case "resources/list":
				return listResources(ctx, call)
			case "resources/templates/list":
				return listTemplates(ctx, call)
			case "prompts/list":
				return listPrompts(ctx, call)
			case "resources/read":
				r, ok := req.(*mcp.ReadResourceRequest)
				if !ok || r.Params == nil {
					break
				}
				return readResource(ctx, call, r.Params.URI)
			case "prompts/get":
				r, ok := req.(*mcp.GetPromptRequest)
				if !ok || r.Params == nil {
					break
				}
				return getPrompt(ctx, call, r.Params.Name, r.Params.Arguments)
			}
			return next(ctx, method, req)
		}
	}
}

// listServers asks the daemon for one list op of every server.
func listServers(ctx context.Context, call Caller, op string) ([]protocol.MCPServerItems, error) {
	raw, err := call(ctx, "mcp_feature", protocol.MCPFeature{Op: op})
	if err != nil {
		return nil, err
	}
	var r struct {
		Servers []protocol.MCPServerItems `json:"servers"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("unreadable answer from the daemon: %v", err)
	}
	return r.Servers, nil
}

func listResources(ctx context.Context, call Caller) (*mcp.ListResourcesResult, error) {
	srvs, err := listServers(ctx, call, protocol.MCPOpResources)
	if err != nil {
		return nil, err
	}
	out := &mcp.ListResourcesResult{Resources: []*mcp.Resource{}}
	for _, s := range srvs {
		var items []*mcp.Resource
		if s.Error != "" || json.Unmarshal(s.Items, &items) != nil {
			continue
		}
		for _, r := range items {
			if r == nil || r.URI == "" {
				continue
			}
			r.URI = resourceURI(s.Server, r.URI)
			r.Name = s.Server + "__" + r.Name
			r.Description = describe(s, r.Description)
			out.Resources = append(out.Resources, r)
		}
	}
	return out, nil
}

func listTemplates(ctx context.Context, call Caller) (*mcp.ListResourceTemplatesResult, error) {
	srvs, err := listServers(ctx, call, protocol.MCPOpTemplates)
	if err != nil {
		return nil, err
	}
	out := &mcp.ListResourceTemplatesResult{ResourceTemplates: []*mcp.ResourceTemplate{}}
	for _, s := range srvs {
		var items []*mcp.ResourceTemplate
		if s.Error != "" || json.Unmarshal(s.Items, &items) != nil {
			continue
		}
		for _, r := range items {
			if r == nil || r.URITemplate == "" {
				continue
			}
			r.URITemplate = resourceURI(s.Server, r.URITemplate)
			r.Name = s.Server + "__" + r.Name
			r.Description = describe(s, r.Description)
			out.ResourceTemplates = append(out.ResourceTemplates, r)
		}
	}
	return out, nil
}

func listPrompts(ctx context.Context, call Caller) (*mcp.ListPromptsResult, error) {
	srvs, err := listServers(ctx, call, protocol.MCPOpPrompts)
	if err != nil {
		return nil, err
	}
	out := &mcp.ListPromptsResult{Prompts: []*mcp.Prompt{}}
	for _, s := range srvs {
		var items []*mcp.Prompt
		if s.Error != "" || json.Unmarshal(s.Items, &items) != nil {
			continue
		}
		for _, p := range items {
			if p == nil || p.Name == "" {
				continue
			}
			p.Name = s.Server + "__" + p.Name
			p.Description = describe(s, p.Description)
			out.Prompts = append(out.Prompts, p)
		}
	}
	return out, nil
}

func readResource(ctx context.Context, call Caller, uri string) (*mcp.ReadResourceResult, error) {
	server, own, ok := splitResourceURI(uri)
	if !ok {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	raw, err := call(ctx, "mcp_feature", protocol.MCPFeature{Op: protocol.MCPOpRead, Server: server, URI: own})
	if err != nil {
		return nil, err
	}
	var res mcp.ReadResourceResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("unreadable answer from the MCP server: %v", err)
	}
	for _, c := range res.Contents {
		if c == nil {
			continue
		}
		if c.URI == "" {
			c.URI = own
		}
		c.URI = resourceURI(server, c.URI)
	}
	return &res, nil
}

func getPrompt(ctx context.Context, call Caller, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	server, own, ok := splitPromptName(name)
	if !ok {
		return nil, fmt.Errorf("unknown prompt %q: registry prompts are named <server>__<prompt>", name)
	}
	raw, err := call(ctx, "mcp_feature", protocol.MCPFeature{Op: protocol.MCPOpPrompt, Server: server, Name: own, Arguments: args})
	if err != nil {
		return nil, err
	}
	var res mcp.GetPromptResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("unreadable answer from the MCP server: %v", err)
	}
	// Resources a prompt points to are read through agent-net too.
	for _, m := range res.Messages {
		if m == nil {
			continue
		}
		switch c := m.Content.(type) {
		case *mcp.ResourceLink:
			c.URI = resourceURI(server, c.URI)
		case *mcp.EmbeddedResource:
			if c.Resource != nil {
				c.Resource.URI = resourceURI(server, c.Resource.URI)
			}
		}
	}
	return &res, nil
}

// changeMarker is added and removed at once to make the SDK send a
// list_changed notification. It is never listed: featureProxy answers every
// list itself.
const changeMarker = resourceScheme + "agent-net/changed"

// notifyFeaturesChanged tells the client to list resources and prompts again.
// The SDK sends a list_changed notification only when its own lists change,
// so a marker is added and removed; the SDK merges the two changes into one
// notification of each kind.
func notifyFeaturesChanged(s *mcp.Server) {
	s.AddResource(&mcp.Resource{URI: changeMarker, Name: "changed"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return nil, mcp.ResourceNotFoundError(changeMarker)
	})
	s.RemoveResources(changeMarker)
	s.AddPrompt(&mcp.Prompt{Name: "agent-net__changed"}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return nil, fmt.Errorf("unknown prompt")
	})
	s.RemovePrompts("agent-net__changed")
}

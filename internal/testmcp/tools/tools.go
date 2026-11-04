// Package tools is a small MCP server used by the tests and the end-to-end
// runs. Its tools help check routing, timeouts, caps, restarts and redaction.
package tools

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoIn struct {
	Text string `json:"text" jsonschema:"text to echo back"`
}
type sleepIn struct {
	Seconds float64 `json:"seconds" jsonschema:"how long to sleep"`
}
type bigIn struct {
	Bytes int `json:"bytes" jsonschema:"how many bytes of text to return"`
}
type envIn struct {
	Name string `json:"name" jsonschema:"environment variable to read"`
}
type none struct{}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// LabelArg is the command-line argument that sets the label of a stdio test
// server, for example --testmcp-label=a.
const LabelArg = "--testmcp-label="

// BigResourceBytes is the size of the text of test://big.
const BigResourceBytes = 640 << 10

// New returns the server, labelled from its command line (see LabelArg).
func New() *mcp.Server {
	label := "testmcp"
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, LabelArg) {
			label = strings.TrimPrefix(a, LabelArg)
		}
	}
	return NewWith(label)
}

// NewWith returns the server. Tools: echo, sleep, big, env, pid, crash.
// Resources: test://hello (text that names the label), test://logo.png (a
// blob), test://big (BigResourceBytes of text). Templates: test://item/{id},
// and test://add/{name}, whose read adds the resource test://extra/{name} and
// so sends notifications/resources/list_changed. Prompt: greet, with the
// required argument name.
func NewWith(label string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "testmcp", Version: "0.0.1"}, nil)
	// A server with no tools at all, for the tests of the "reports no tools" rule.
	if os.Getenv("AGENT_NET_TEST_MCP_NO_TOOLS") != "" {
		return s
	}
	addResources(s, label)
	mcp.AddTool(s, &mcp.Tool{Name: "echo", Description: "Echo the text back."},
		func(_ context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, any, error) {
			return text("echo: " + in.Text), nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "sleep", Description: "Sleep, then answer."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in sleepIn) (*mcp.CallToolResult, any, error) {
			select {
			case <-time.After(time.Duration(in.Seconds * float64(time.Second))):
			case <-ctx.Done():
			}
			return text("slept"), nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "big", Description: "Return a large text."},
		func(_ context.Context, _ *mcp.CallToolRequest, in bigIn) (*mcp.CallToolResult, any, error) {
			return text(strings.Repeat("x", in.Bytes)), nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "env", Description: "Return an environment variable of the server process."},
		func(_ context.Context, _ *mcp.CallToolRequest, in envIn) (*mcp.CallToolResult, any, error) {
			return text("value=" + os.Getenv(in.Name)), nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "pid", Description: "Return the server's process ID."},
		func(_ context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
			return text(strconv.Itoa(os.Getpid())), nil, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "crash", Description: "Exit the server process at once."},
		func(_ context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
			os.Exit(3)
			return nil, nil, nil
		})
	return s
}

func readText(uri, mime, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: mime, Text: text}}}
}

func addResources(s *mcp.Server, label string) {
	s.AddResource(&mcp.Resource{URI: "test://hello", Name: "hello", Description: "A greeting.", MIMEType: "text/plain"},
		func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return readText(req.Params.URI, "text/plain", "hello from "+label), nil
		})
	s.AddResource(&mcp.Resource{URI: "test://logo.png", Name: "logo", MIMEType: "image/png"},
		func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "image/png",
				Blob: []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 0xff}}}}, nil
		})
	s.AddResource(&mcp.Resource{URI: "test://big", Name: "big", MIMEType: "text/plain"},
		func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return readText(req.Params.URI, "text/plain", strings.Repeat("x", BigResourceBytes)), nil
		})
	s.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "test://item/{id}", Name: "item", Description: "One item.", MIMEType: "text/plain"},
		func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			id := strings.TrimPrefix(req.Params.URI, "test://item/")
			return readText(req.Params.URI, "text/plain", "item "+id+" from "+label), nil
		})
	s.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "test://add/{name}", Name: "add"},
		func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			name := strings.TrimPrefix(req.Params.URI, "test://add/")
			uri := "test://extra/" + name
			s.AddResource(&mcp.Resource{URI: uri, Name: "extra-" + name, MIMEType: "text/plain"},
				func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
					return readText(req.Params.URI, "text/plain", "extra "+name), nil
				})
			return readText(req.Params.URI, "text/plain", "added "+uri), nil
		})
	s.AddPrompt(&mcp.Prompt{Name: "greet", Description: "Greet someone.",
		Arguments: []*mcp.PromptArgument{{Name: "name", Description: "who to greet", Required: true}}},
		func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{Description: "a greeting", Messages: []*mcp.PromptMessage{
				{Role: "user", Content: &mcp.TextContent{Text: "Hello, " + req.Params.Arguments["name"] + "! (from " + label + ")"}}}}, nil
		})
}

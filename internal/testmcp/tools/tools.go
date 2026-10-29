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

// New returns the server. Tools: echo, sleep, big, env, pid, crash.
func New() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "testmcp", Version: "0.0.1"}, nil)
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

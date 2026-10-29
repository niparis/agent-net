// Command stdio is a test MCP server on stdin and stdout.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/testmcp/tools"
)

func main() {
	fmt.Fprintln(os.Stderr, "testmcp stdio server starting")
	if err := tools.New().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

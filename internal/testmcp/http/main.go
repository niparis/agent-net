// Command http is a test MCP server over streamable HTTP (or SSE). When
// -token-file is set it requires "Authorization: Bearer <contents of the file>".
package main

import (
	"crypto/subtle"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/testmcp/tools"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:0", "address")
	tokenFile := flag.String("token-file", "", "file with the required bearer token")
	sse := flag.Bool("sse", false, "serve SSE instead of streamable HTTP")
	flag.Parse()
	var want string
	if *tokenFile != "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		want = "Bearer " + strings.TrimSpace(string(b))
	}
	srv := tools.New()
	var h http.Handler
	if *sse {
		h = mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	} else {
		h = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("listening on", ln.Addr())
	http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(want)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
}

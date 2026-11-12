package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// silentDaemon is a daemon socket that accepts connections and reads requests,
// but answers nothing until the test says so. It stands in for a daemon that is
// overloaded or restarting.
type silentDaemon struct {
	t  *testing.T
	ln net.Listener

	mu     sync.Mutex
	answer bool
	conns  []net.Conn
}

func newSilentDaemon(t *testing.T, socket string) *silentDaemon {
	t.Helper()
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	s := &silentDaemon{t: t, ln: ln}
	t.Cleanup(func() {
		ln.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, c := range s.conns {
			c.Close()
		}
	})
	go s.accept()
	return s
}

func (s *silentDaemon) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, c)
		s.mu.Unlock()
		go s.serve(c)
	}
}

func (s *silentDaemon) serve(c net.Conn) {
	sc := bufio.NewScanner(c)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		s.mu.Lock()
		answer := s.answer
		s.mu.Unlock()
		if !answer {
			continue
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || len(req.ID) == 0 {
			continue
		}
		result := json.RawMessage(`{}`)
		switch req.Method {
		case "hello":
			result = json.RawMessage(`{"node_id":"n_test","session_id":"s_test","version":"v-test"}`)
		case "charter_text":
			result = json.RawMessage(`{"text":""}`)
		case "sessions_find":
			result = json.RawMessage(`{"items":[],"total":0}`)
		}
		out, err := json.Marshal(map[string]any{"id": req.ID, "result": result})
		if err != nil {
			continue
		}
		c.Write(append(out, '\n'))
	}
}

func (s *silentDaemon) answerNow() {
	s.mu.Lock()
	s.answer = true
	s.mu.Unlock()
}

// A slow or hung daemon must not delay initialize past Claude Code's MCP
// startup timeout. The server answers with the fixed instructions and the
// built-in tools, and a tool call works once the daemon answers again.
func TestInitializeAndToolsListAnswerWhileTheDaemonIsHung(t *testing.T) {
	t.Setenv("AGENT_NET_WORKER", "")
	socket := t.TempDir() + "/d.sock"
	daemon := newSilentDaemon(t, socket)
	d := NewDaemon(socket, map[string]any{"client": "mcp", "harness": "claude", "native_id": "n_test"})

	start := time.Now()
	type startup struct {
		srv *mcp.Server
		dyn *Dynamic
	}
	ready := make(chan startup, 1)
	go func() {
		srv, dyn := startupServer(context.Background(), d, "v-test", nil)
		ready <- startup{srv, dyn}
	}()

	var got startup
	select {
	case got = <-ready:
	case <-time.After(3 * time.Second):
		t.Fatalf("startup did not finish within 3 s: daemon work before serving is not bounded")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := got.srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("initialize was not answered within 3 s: %v", err)
	}
	defer cs.Close()
	elapsed := time.Since(start)
	if elapsed > 3*time.Second {
		t.Fatalf("initialize took %v with a hung daemon", elapsed)
	}
	t.Logf("startup and initialize with a hung daemon: %v", elapsed)
	if !strings.Contains(cs.InitializeResult().Instructions, "agent-net connects this session") {
		t.Fatalf("instructions = %q, want the fixed text", cs.InitializeResult().Instructions)
	}

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list was not answered: %v", err)
	}
	found := false
	for _, tool := range tools.Tools {
		if tool.Name == "agent_sessions_find" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tools/list lacks the built-in tools: %+v", tools.Tools)
	}

	// The daemon is back: the next call redials and works.
	daemon.answerNow()
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_sessions_find", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("tool call after the daemon answered: %v", err)
	}
	if result.IsError {
		var text []string
		for _, c := range result.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text = append(text, tc.Text)
			}
		}
		t.Fatalf("tool call failed after the daemon answered: %s", strings.Join(text, " "))
	}
}

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestFileSendToolRequiresBoundSession(t *testing.T) {
	lc := &localConn{d: &Daemon{}, client: "mcp"}
	_, err := lc.dispatch(context.Background(), "file_send", json.RawMessage(`{"to":"s_b","paths":["/tmp/a"],"note":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "bound session") {
		t.Fatalf("unbound call: %v", err)
	}
}

func TestFileSendToolRefusals(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{})
	ls := n.a.d.sessionByID(n.alice)
	lc := &localConn{d: n.a.d, client: "mcp", ls: ls}
	outside := writeTestFile(t, filepath.Join(t.TempDir(), "outside.txt"), []byte("outside"))
	link := filepath.Join(n.project, "escape.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(n.project, "big.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(protocol.MaxFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	tests := []struct{ name, path, want string }{
		{"missing", filepath.Join(n.project, "missing"), "cannot read"},
		{"directory", n.project, "regular file"},
		{"symlink escape", link, "symlink points outside"},
		{"too big", big, "limit"},
		{"relative", "relative.txt", "full path"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(protocol.FileSendRequest{To: n.bob, Paths: []string{tc.path}, Note: "read"})
			_, err := lc.dispatch(context.Background(), "file_send", raw)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFileSendToolRefusesWorker(t *testing.T) {
	ls := &localSession{sessionID: "s_worker", info: protocol.SessionInfo{Worker: &protocol.WorkerInfo{}}}
	d := &Daemon{sessions: map[string]*localSession{"worker": ls}}
	lc := &localConn{d: d, client: "mcp", ls: ls}
	_, err := lc.dispatch(context.Background(), "file_send", json.RawMessage(`{"to":"s_b","paths":["/tmp/a"],"note":"read"}`))
	if err == nil || err.Error() != "workers send files with agent_handback" {
		t.Fatalf("worker: %v", err)
	}
}

func TestFileSendToolTwoNodes(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fb := &fakeAdapter{harness: "fake"}
	a := startNode(t, g, nodeOpts{cmdline: "pi", cfg: func(c *Config) { c.Files.Home = t.TempDir() }})
	b := startNode(t, g, nodeOpts{adapter: fb, cfg: func(c *Config) {
		c.Files.Home = t.TempDir()
		c.Files.Poll = 20 * time.Millisecond
		c.Files.PollMax = 50 * time.Millisecond
	}})
	project := t.TempDir()
	bobProject := t.TempDir()
	fb.setSessions(protocol.SessionInfo{Harness: "fake", NativeID: "nb", Cwd: bobProject, Name: "bob", Status: "idle"})
	eventually(t, "bob session", func() bool { return fb.host.SessionID("fake", "nb") != "" })
	bob := fb.host.SessionID("fake", "nb")
	source := writeTestFile(t, filepath.Join(project, "report.txt"), []byte("hello peer\n"))
	md := mcpserver.NewDaemon(a.sock, map[string]any{"client": "pi", "harness": "pi", "native_id": "sender", "cwd": project, "status": "idle"})
	t.Cleanup(md.Close)
	ctx, cancel := context.WithTimeout(context.Background(), scaled(20*time.Second))
	defer cancel()
	st, ct := mcp.NewInMemoryTransports()
	server, err := mcpserver.New(md.Call, "").Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	client, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "agent_file_send", Arguments: map[string]any{"to": bob, "paths": []string{source}, "note": "please read"}})
	if err != nil || res.IsError {
		t.Fatalf("send: %v %+v", err, res)
	}
	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var result protocol.FileSendResult
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || result.MessageID == "" || len(result.Files) != 1 {
		t.Fatalf("receipt: %+v", result)
	}
	wantRel := filepath.ToSlash(filepath.Join("inbox", bob, result.MessageID, "report.txt"))
	if result.Files[0].Path != wantRel || result.Files[0].Name != "report.txt" {
		t.Fatalf("destination: %+v", result.Files)
	}
	eventually(t, "file delivered", func() bool { return len(fb.delivered()) == 1 })
	got := fb.delivered()[0]
	destination := filepath.Join(b.cfg.StateDir, filepath.FromSlash(result.Files[0].Path))
	if string(mustReadFile(t, destination)) != "hello peer\n" || !strings.Contains(got.Body, destination[:len(destination)-len("report.txt")-1]) || !strings.Contains(got.Body, "report.txt") || !strings.Contains(got.Body, "please read") {
		t.Fatalf("delivery: %+v at %s", got, destination)
	}
}

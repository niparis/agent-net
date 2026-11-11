package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The CTO approves or disables the .mcp.json servers of a project on the
// owner's instruction. It is audited with an Inbox notice that names the
// servers and what they run, and the undo removes exactly the names it added.

func ctoMCPCall(t *testing.T, d *Daemon, args map[string]any) (any, error) {
	t.Helper()
	args["action"] = "mcp_servers"
	raw, _ := json.Marshal(args)
	return d.runCTOToolRaw(context.Background(), "session_change", raw)
}

func TestCTOMCPServersAuditsAndUndoRemovesOnlyWhatItAdded(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := folderTrustEnv(t)
	n := startWorkerNode(t, g, shortSet, nil)
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	parent := newHomeRepo(t, home, "code/tenebrae")
	child := newHomeRepo(t, home, "code/tenebrae/finget")
	writeMCPJSON(t, parent, map[string]any{"tenebrae-library": map[string]any{"command": "just", "args": []string{"--justfile", "strategies/justfile", "library-mcp"}}})
	// The owner refused one name here before.
	settings := filepath.Join(child, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"enabledMcpjsonServers":["mine"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ctoMCPCall(t, n.d, map[string]any{"path": child, "mcp_servers": "disable"}); err != nil {
		t.Fatal(err)
	}
	got := readLocalSettings(t, child)
	if !reflect.DeepEqual(got["disabledMcpjsonServers"], []any{"tenebrae-library"}) || !reflect.DeepEqual(got["enabledMcpjsonServers"], []any{"mine"}) {
		t.Fatalf("settings %v", got)
	}
	item := lastInbox(t, g)
	if item.Node != "cto" || len(item.Options) != 2 || item.Options[0] != protocol.OpFolderMCP {
		t.Fatalf("want an audit notice with undo: %+v", item)
	}
	for _, want := range []string{"disabled", child} {
		if !strings.Contains(item.Text, want) {
			t.Errorf("notice %q misses %q", item.Text, want)
		}
	}
	for _, want := range []string{"tenebrae-library", "just --justfile strategies/justfile library-mcp", filepath.Join(parent, ".mcp.json"), "disabledMcpjsonServers"} {
		if !strings.Contains(item.Recommendation, want) {
			t.Errorf("audit detail %q misses %q", item.Recommendation, want)
		}
	}
	var undo protocol.FolderMCPRequest
	if err := json.Unmarshal([]byte(item.Options[1]), &undo); err != nil || undo.Undo == nil || undo.Path != child {
		t.Fatalf("undo %+v %v", undo, err)
	}
	n.cli.mustCall(protocol.OpFolderMCP, undo)
	got = readLocalSettings(t, child)
	if v := got["disabledMcpjsonServers"]; v != nil && len(v.([]any)) != 0 {
		t.Fatalf("undo left %v", v)
	}
	if !reflect.DeepEqual(got["enabledMcpjsonServers"], []any{"mine"}) {
		t.Fatalf("undo touched the owner's own list: %v", got)
	}
	if items, err := n.d.proposalRunner().list(); err != nil || len(items) != 0 {
		t.Fatalf("a choice is not a proposal: %+v %v", items, err)
	}
}

func TestCTOMCPServersChecksItsInput(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := folderTrustEnv(t)
	n := startWorkerNode(t, g, shortSet, nil)
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	empty := newHomeRepo(t, home, "code/empty")
	for name, args := range map[string]map[string]any{
		"no servers":      {"path": empty, "mcp_servers": "approve"},
		"missing path":    {"path": filepath.Join(home, "nope"), "mcp_servers": "approve"},
		"unknown choice":  {"path": empty, "mcp_servers": "all"},
		"unknown project": {"project": "no-such-project", "mcp_servers": "approve"},
		"both":            {"project": "x", "path": empty, "mcp_servers": "approve"},
		"no choice":       {"path": empty},
	} {
		if _, err := ctoMCPCall(t, n.d, args); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := os.Stat(filepath.Join(empty, ".claude")); err == nil {
		t.Fatal("a refused choice wrote a file")
	}
}

func TestCTOMCPServersByProjectOnARemoteNode(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := folderTrustEnv(t)
	a := startWorkerNode(t, g, shortSet, nil)
	b := startClaudeNode(t, g, shortSet)
	repo := newHomeRepo(t, home, "code/garden")
	writeMCPJSON(t, repo, map[string]any{"lib": map[string]any{"command": "just"}})
	if _, err := g.gw.Store().AddProjects(b.d.NodeID(), protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "garden", Path: repo}}}); err != nil {
		t.Fatal(err)
	}
	if err := a.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The project is registered only on b, so a choice that works was relayed.
	if _, err := ctoMCPCall(t, a.d, map[string]any{"project": "garden", "mcp_servers": "approve"}); err != nil {
		t.Fatal(err)
	}
	if got := readLocalSettings(t, repo); !reflect.DeepEqual(got["enabledMcpjsonServers"], []any{"lib"}) {
		t.Fatalf("settings %v", got)
	}
	if _, ok := readLocalSettings(t, repo)["enableAllProjectMcpServers"]; ok {
		t.Fatal("enableAllProjectMcpServers must never be set")
	}
	item := lastInbox(t, g)
	var undo protocol.FolderMCPRequest
	if len(item.Options) != 2 || item.Options[0] != protocol.OpFolderMCP || json.Unmarshal([]byte(item.Options[1]), &undo) != nil || undo.NodeID != b.d.NodeID() || undo.Undo == nil {
		t.Fatalf("remote choice needs an undo that names the machine: %+v", item)
	}
	if !strings.Contains(item.Text, "on ") {
		t.Fatalf("notice names no machine: %q", item.Text)
	}
	a.cli.mustCall(protocol.OpFolderMCP, undo)
	if v := readLocalSettings(t, repo)["enabledMcpjsonServers"]; v != nil && len(v.([]any)) != 0 {
		t.Fatalf("remote undo left %v", v)
	}
	events, err := g.gw.Store().Events("session.folder_mcp")
	if err != nil || len(events) != 2 {
		t.Fatalf("gateway events %v %v", events, err)
	}
}

// An undo frame carries a path like a choice does, so it gets the same home
// check. A frame must not change a settings file in a folder outside home.
func TestFolderMCPUndoOutsideHomeWritesNothing(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	folderTrustEnv(t)
	n := startWorkerNode(t, g, shortSet, nil)
	outside := t.TempDir()
	settings := filepath.Join(outside, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	before := `{"enabledMcpjsonServers":["lib"]}`
	if err := os.WriteFile(settings, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	undo := protocol.FolderMCPRequest{Path: outside, Undo: &protocol.MCPUndo{Choice: protocol.MCPApprove, Added: []string{"lib"}}}
	if _, err := n.cli.call(protocol.OpFolderMCP, undo); err == nil || !strings.Contains(err.Error(), "outside your home folder") {
		t.Fatalf("undo outside home: want a refusal, got %v", err)
	}
	if raw, err := os.ReadFile(settings); err != nil || string(raw) != before {
		t.Fatalf("undo outside home changed the settings file: %q %v", raw, err)
	}
}

// session_start with mcp_servers records the choice in the session folder
// before Claude Code starts, so the first run does not stop on the prompt.
func TestCTOSessionStartRecordsTheMCPChoiceFirst(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := folderTrustEnv(t)
	c := startClaudeNode(t, g, shortSet)
	repo := newHomeRepo(t, home, "code/garden")
	writeMCPJSON(t, repo, map[string]any{"lib": map[string]any{"command": "just"}})
	trustClaudeFolder(t, repo)
	if _, err := g.gw.Store().AddProjects(c.d.NodeID(), protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "garden", Path: repo}}}); err != nil {
		t.Fatal(err)
	}
	if err := c.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killFakeClaude(t, repo) })
	if _, err := ctoTrustCall(t, c.d, map[string]any{"action": "session_start", "project": "garden", "harness": "claude", "mcp_servers": "disable"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the session starts", func() bool {
		_, err := os.Stat(filepath.Join(repo, "fake-claude.json"))
		return err == nil
	})
	if got := readLocalSettings(t, repo); !reflect.DeepEqual(got["disabledMcpjsonServers"], []any{"lib"}) {
		t.Fatalf("settings %v", got)
	}
	item := lastInbox(t, g)
	if len(item.Options) != 2 || item.Options[0] != protocol.OpFolderMCP || !strings.Contains(item.Recommendation, "lib: just") {
		t.Fatalf("the start audit must name the servers and offer the undo: %+v", item)
	}
	// A bad choice, or one for another harness, is refused before anything starts.
	for name, args := range map[string]map[string]any{
		"bad choice": {"action": "session_start", "project": "garden", "harness": "claude", "mcp_servers": "all"},
		"opencode":   {"action": "session_start", "project": "garden", "harness": "opencode", "mcp_servers": "approve"},
	} {
		if _, err := ctoTrustCall(t, c.d, args); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// Claude Code stops a background job on "new MCP server needs approval". The
// start must fail at once with a reason code, say the job is alive, name the
// servers and the .mcp.json file, and tell the CTO that asked for the start.
func TestSessionStartBlockedOnMCPApprovalIsReportedToTheRequester(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := folderTrustEnv(t)
	c := startClaudeNode(t, g, shortSet)
	parent := newHomeRepo(t, home, "code/tenebrae")
	repo := newHomeRepo(t, home, "code/tenebrae/finget")
	mcpFile := writeMCPJSON(t, parent, map[string]any{"tenebrae-library": map[string]any{"command": "just", "args": []string{"library-mcp"}}})
	trustClaudeFolder(t, repo)
	if _, err := g.gw.Store().AddProjects(c.d.NodeID(), protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "finget", Path: repo}}}); err != nil {
		t.Fatal(err)
	}
	if err := c.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	cto := c.d.ctoSessionID()
	t.Setenv(envFakeMCPBlocked, "1")
	// The start deadline is long on purpose: the block must be seen long before it.
	raw, err := ctoTrustCall(t, c.d, map[string]any{"action": "session_start", "project": "finget", "harness": "claude"})
	if err != nil {
		t.Fatal(err)
	}
	var r protocol.StartedSession
	b, _ := json.Marshal(raw)
	if err := json.Unmarshal(b, &r); err != nil || r.ID == "" {
		t.Fatalf("start result %s %v", b, err)
	}
	eventually(t, "the failed start", func() bool {
		mustJSON(t, c.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		return r.Status == "failed"
	})
	if r.ErrorCode != protocol.StartErrorMCPApprovalNeeded || !r.JobAlive || r.JobID != "blockedjob" {
		t.Fatalf("reason code, job: %+v", r)
	}
	if len(r.MCPServers) != 1 || r.MCPServers[0].Name != "tenebrae-library" || r.MCPServers[0].Command != "just library-mcp" || r.MCPServers[0].File != mcpFile {
		t.Fatalf("servers %+v", r.MCPServers)
	}
	for _, want := range []string{"mcp_approval_needed", "tenebrae-library", mcpFile, "blockedjob", "still running", "mcp_servers"} {
		if !strings.Contains(r.Error, want) {
			t.Errorf("error %q misses %q", r.Error, want)
		}
	}
	if strings.Contains(r.Error, "wrote no session file") {
		t.Fatalf("the block must be named, not the timeout: %q", r.Error)
	}
	eventually(t, "the notice to the CTO", func() bool {
		msgs, err := g.gw.Store().Messages(c.d.NodeID(), protocol.QueryMessages{SessionID: cto})
		if err != nil {
			return false
		}
		for _, m := range msgs {
			if m.To == cto && m.From == "agent-net" && m.Type == protocol.TypeNotification && strings.Contains(m.Body, "mcp_approval_needed") && strings.Contains(m.Body, r.ID) && strings.Contains(m.Body, "tenebrae-library") {
				return true
			}
		}
		return false
	})
}

package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

type permissionSettings struct {
	Permissions struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
	} `json:"permissions"`
	Other string `json:"other"`
}

func readPermissionSettings(t *testing.T, repo string) permissionSettings {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repo, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var doc permissionSettings
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("settings are not JSON: %v\n%s", err, raw)
	}
	return doc
}

func countString(list []string, want string) int {
	n := 0
	for _, v := range list {
		if v == want {
			n++
		}
	}
	return n
}

// mcpFolder makes home/rel an untrusted git folder whose own .mcp.json declares
// tenebrae-library, the tenebrae-strats case.
func mcpFolder(t *testing.T, home, rel string) string {
	t.Helper()
	repo := newHomeRepo(t, home, rel)
	commitMCPJSON(t, repo, map[string]any{"tenebrae-library": map[string]any{
		"command": "just", "args": []string{"library-mcp"},
	}})
	return repo
}

// trust true on the start is the owner's yes to the folder; it approves the
// folder's own .mcp.json servers in the same start.
func TestSessionStartTrustApprovesFolderMCPServers(t *testing.T) {
	t.Setenv(envFakeMCPBlocked, "1")
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	repo := mcpFolder(t, home, "code/tenebrae")

	r := sessionStartPathCall(t, c.workerNode, protocol.SessionStartRequest{Path: repo, Harness: "claude", TrustClaude: true})
	r = waitSessionStartPath(t, c.workerNode, r)
	t.Cleanup(func() { killFakeClaude(t, repo) })
	if r.Status != "started" || r.ErrorCode != "" {
		t.Fatalf("trusted start: %+v", r)
	}
	if got := readLocalSettings(t, repo); !reflect.DeepEqual(got["enabledMcpjsonServers"], []any{"tenebrae-library"}) {
		t.Fatalf("folder settings after the trusted start: %v", got)
	}
}

// An explicit mcp_servers disable given with the start still wins over the
// trust click's approval of the folder's own servers.
func TestSessionStartTrustKeepsExplicitMCPDisable(t *testing.T) {
	t.Setenv(envFakeMCPBlocked, "1")
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	repo := mcpFolder(t, home, "code/tenebrae")

	r := sessionStartPathCall(t, c.workerNode, protocol.SessionStartRequest{
		Path: repo, Harness: "claude", TrustClaude: true, MCPServers: protocol.MCPDisable,
	})
	r = waitSessionStartPath(t, c.workerNode, r)
	t.Cleanup(func() { killFakeClaude(t, repo) })
	if r.Status != "started" {
		t.Fatalf("start with disable: %+v", r)
	}
	got := readLocalSettings(t, repo)
	if !reflect.DeepEqual(got["disabledMcpjsonServers"], []any{"tenebrae-library"}) {
		t.Fatalf("disable did not win: %v", got)
	}
	if v, ok := got["enabledMcpjsonServers"]; ok && len(v.([]any)) != 0 {
		t.Fatalf("an explicit disable was overridden by the trust click: %v", got)
	}
}

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/claudetrust"
	"github.com/niparis/agent-net/internal/protocol"
)

// The CTO trusts a folder for Claude Code on the owner's instruction. It is
// audited with an Inbox notice and undone only when agent-net added the trust.

func folderTrustEnv(t *testing.T) string {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	return pathTestHome(t)
}

func ctoTrustCall(t *testing.T, d *Daemon, args map[string]any) (any, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return d.runCTOToolRaw(context.Background(), "session_change", raw)
}

func TestCTOTrustByPathAuditsAndUndoRemovesOnlyAgentAddedTrust(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := folderTrustEnv(t)
	n := startWorkerNode(t, g, shortSet, nil)
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	ownerTrusted := newHomeRepo(t, home, "code/owner-trusted")
	fresh := newHomeRepo(t, home, "code/fresh")
	if err := claudetrust.Set(ownerTrusted); err != nil {
		t.Fatal(err)
	}

	// A folder the owner already trusted succeeds cleanly.
	if _, err := ctoTrustCall(t, n.d, map[string]any{"action": "trust", "path": ownerTrusted}); err != nil {
		t.Fatal(err)
	}

	// A new folder: trusted and removable via OpFolderTrust.
	if _, err := ctoTrustCall(t, n.d, map[string]any{"action": "trust", "path": fresh}); err != nil {
		t.Fatal(err)
	}
	if ok, err := claudetrust.Trusted(fresh); err != nil || !ok {
		t.Fatalf("fresh folder not trusted: %v %v", ok, err)
	}
	n.cli.mustCall(protocol.OpFolderTrust, protocol.FolderTrustRequest{Path: fresh, Remove: true})
	if ok, _ := claudetrust.Trusted(fresh); ok {
		t.Fatal("undo did not remove the trust agent-net added")
	}
	if ok, _ := claudetrust.Trusted(ownerTrusted); !ok {
		t.Fatal("the owner's own trust must stay")
	}
	if items, err := n.d.proposalRunner().list(); err != nil || len(items) != 0 {
		t.Fatalf("trust is not a proposal: %+v %v", items, err)
	}
}

func TestCTOTrustChecksTheFolder(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := folderTrustEnv(t)
	n := startWorkerNode(t, g, shortSet, nil)
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string]map[string]any{
		"missing":         {"action": "trust", "path": filepath.Join(home, "nope")},
		"a file":          {"action": "trust", "path": file},
		"relative":        {"action": "trust", "path": "code/x"},
		"unknown project": {"action": "trust", "project": "no-such-project"},
		"both":            {"action": "trust", "project": "x", "path": home},
	} {
		if _, err := ctoTrustCall(t, n.d, args); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if ok, _ := claudetrust.Trusted(file); ok {
		t.Fatal("a file was trusted")
	}
}

func TestCTOTrustByProjectOnARemoteNode(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := folderTrustEnv(t)
	a := startWorkerNode(t, g, shortSet, nil)
	b := startClaudeNode(t, g, shortSet)
	repo := newHomeRepo(t, home, "code/garden")
	if _, err := g.gw.Store().AddProjects(b.d.NodeID(), protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "garden", Path: repo}}}); err != nil {
		t.Fatal(err)
	}
	if err := a.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The project is registered only on b, so a trust that works was relayed.
	if _, err := ctoTrustCall(t, a.d, map[string]any{"action": "trust", "project": "garden"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := claudetrust.Trusted(repo); err != nil || !ok {
		t.Fatalf("project folder not trusted: %v %v", ok, err)
	}
	a.cli.mustCall(protocol.OpFolderTrust, protocol.FolderTrustRequest{NodeID: b.d.NodeID(), Path: repo, Remove: true})
	if ok, _ := claudetrust.Trusted(repo); ok {
		t.Fatal("remote undo did not remove the trust")
	}
	// An unregistered folder name on a named machine is refused.
	if _, err := ctoTrustCall(t, a.d, map[string]any{"action": "trust", "project": "other", "node_id": b.d.NodeID()}); err == nil {
		t.Fatal("a project that is not registered on that machine must be refused")
	}
}

func TestCTOSessionStartWithTrustTrustsThenStarts(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := folderTrustEnv(t)
	c := startClaudeNode(t, g, shortSet)
	repo := newHomeRepo(t, home, "code/garden")
	if _, err := g.gw.Store().AddProjects(c.d.NodeID(), protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "garden", Path: repo}}}); err != nil {
		t.Fatal(err)
	}
	if err := c.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killFakeClaude(t, repo) })
	if _, err := ctoTrustCall(t, c.d, map[string]any{"action": "session_start", "project": "garden", "harness": "claude", "trust": true}); err != nil {
		t.Fatal(err)
	}
	if ok, err := claudetrust.Trusted(repo); err != nil || !ok {
		t.Fatalf("trust:true did not trust the folder: %v %v", ok, err)
	}
	eventually(t, "the session starts in the trusted folder", func() bool {
		_, err := os.Stat(filepath.Join(repo, "fake-claude.json"))
		return err == nil
	})
}

// An undo frame names a path like a trust does, so it gets the same home
// check. It must not remove the trust of a folder outside home.
func TestFolderTrustUndoOutsideHomeChangesNothing(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	folderTrustEnv(t)
	n := startWorkerNode(t, g, shortSet, nil)
	outside := t.TempDir()
	if err := claudetrust.Set(outside); err != nil {
		t.Fatal(err)
	}
	undo := protocol.FolderTrustRequest{Path: outside, Remove: true}
	if _, err := n.cli.call(protocol.OpFolderTrust, undo); err == nil || !strings.Contains(err.Error(), "outside your home folder") {
		t.Fatalf("undo outside home: want a refusal, got %v", err)
	}
	if ok, err := claudetrust.Trusted(outside); err != nil || !ok {
		t.Fatalf("undo outside home removed the trust: %v %v", ok, err)
	}
}

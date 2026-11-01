package protocol

import (
	"encoding/json"
	"testing"
)

// The Folder switch and the Branch field of the New session form survive the
// relay to another machine (docs/design/start-sessions.md, part 9).

func TestSessionStartRequestCarriesTheFolderChoice(t *testing.T) {
	raw, err := json.Marshal(SessionOwnerRequest{
		Owner: "s_owner", FromNode: "n_a",
		SessionStartRequest: SessionStartRequest{
			Project: "shop", Folder: FolderWorktree, Branch: "agent-net/shop-2",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var back SessionOwnerRequest
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Folder != FolderWorktree || back.Branch != "agent-net/shop-2" {
		t.Fatalf("the relay dropped the folder choice: %s", raw)
	}
}

// A path typed in the Project field and the owner's decision to create it
// survive the relay to another machine (docs/design/start-sessions.md, part 10).
func TestSessionStartRequestCarriesAPathAndTheCreateFlag(t *testing.T) {
	raw, err := json.Marshal(SessionOwnerRequest{
		Owner: "s_owner", FromNode: "n_a",
		SessionStartRequest: SessionStartRequest{
			Path: "~/code/garden-planner", Create: true, TrustClaude: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var back SessionOwnerRequest
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Path != "~/code/garden-planner" || !back.Create || !back.TrustClaude {
		t.Fatalf("the relay dropped the path, create flag or trust flag: %s", raw)
	}
	if back.Project != "" {
		t.Errorf("a path start also names a project: %q", back.Project)
	}
}

func TestOwnerWorktreeBranchNames(t *testing.T) {
	for _, tc := range []struct {
		project, want string
	}{
		{"shop", "agent-net/shop-1"},
		{"shop space", "agent-net/shop-space-1"},
		{"agent-net", "agent-net/agent-net-2"},
		{"a..b", "agent-net/a.b-1"},
		{"  ..  ", "agent-net/session-1"},
	} {
		n := 1
		if tc.project == "agent-net" {
			n = 2
		}
		if got := OwnerWorktreeBranch(tc.project, n); got != tc.want {
			t.Errorf("branch for %q: %q, want %q", tc.project, got, tc.want)
		}
	}
}

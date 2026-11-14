package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func decideReq(nativeID, tool, folder string, input any) protocol.ToolDecideRequest {
	raw, _ := json.Marshal(input)
	return protocol.ToolDecideRequest{Harness: "pi", NativeID: nativeID, Tool: tool, Input: raw, Folder: folder}
}

func toolDecide(t *testing.T, cli *localClient, req protocol.ToolDecideRequest) protocol.ToolDecideResult {
	t.Helper()
	var res protocol.ToolDecideResult
	if err := json.Unmarshal(cli.mustCall(protocol.MethodToolDecide, req), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

// A session whose role node holds merge over a repo gets allow for a push of
// the default branch in that repo, and no decision anywhere else. Removing the
// scope stops the allow without waiting for a cache to run out.
func TestToolDecideAllowsFromScopeAndStopsWhenRemoved(t *testing.T) {
	t.Parallel()
	n, _, _, _, _, st := forgeNodes(t)
	// The clock never moves, so a cached answer never ages out: only the
	// gateway's charter-changed frame can end it.
	frozen := time.Now()
	n.d.policy.now = func() time.Time { return frozen }
	cli := cliClient(t, n)
	push := map[string]string{"command": "git push origin main"}

	if res := toolDecide(t, cli, decideReq("lead", "Bash", "/w/lead", push)); res.Allow {
		t.Fatalf("a session with no scope was allowed: %+v", res)
	}
	scope := protocol.ScopeSetRequest{Node: "lead", Kind: protocol.ScopeMerge, Over: "repo:lead"}
	if _, err := st.SetScope(scope, "owner", false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "allow after the scope is set", func() bool {
		return toolDecide(t, cli, decideReq("lead", "Bash", "/w/lead", push)).Allow
	})

	if res := toolDecide(t, cli, decideReq("lead", "Bash", "/w/member", push)); res.Allow {
		t.Fatalf("allowed in another repo: %+v", res)
	}
	if res := toolDecide(t, cli, decideReq("lead", "Bash", "/w/lead", map[string]string{"command": "git push origin main && echo done"})); res.Allow {
		t.Fatalf("allowed a compound command: %+v", res)
	}
	if res := toolDecide(t, cli, decideReq("lead", "Bash", "/w/lead", map[string]string{"command": "git push --force origin main"})); res.Allow {
		t.Fatalf("allowed a never-list command: %+v", res)
	}
	if res := toolDecide(t, cli, decideReq("nobody", "Bash", "/w/lead", push)); res.Allow {
		t.Fatalf("allowed an unknown session: %+v", res)
	}

	if _, err := st.RemoveScope(scope, "owner", false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "no decision after the scope is removed", func() bool {
		return !toolDecide(t, cli, decideReq("lead", "Bash", "/w/lead", push)).Allow
	})
}

// A cached answer costs no gateway call. The whole local round trip stays
// within a few milliseconds.
func TestToolDecideCachedAnswerIsFast(t *testing.T) {
	t.Parallel()
	n, _, _, _, _, st := forgeNodes(t)
	// A frozen clock keeps the cached entry from ageing out mid-measure.
	frozen := time.Now()
	n.d.policy.now = func() time.Time { return frozen }
	cli := cliClient(t, n)
	if _, err := st.SetScope(protocol.ScopeSetRequest{Node: "lead", Kind: protocol.ScopeMerge, Over: "repo:lead"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	req := decideReq("lead", "Bash", "/w/lead", map[string]string{"command": "git push origin main"})
	eventually(t, "allow after the scope is set", func() bool { return toolDecide(t, cli, req).Allow })
	const calls = 50
	start := time.Now()
	for i := 0; i < calls; i++ {
		if !toolDecide(t, cli, req).Allow {
			t.Fatal("lost the allow")
		}
	}
	if per := time.Since(start) / calls; per > 5*time.Millisecond {
		t.Fatalf("a cached answer took %v, want under 5ms", per)
	}
}

package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// browserStart runs an automatic start on a machine of the given OS. harness
// is empty for a fully automatic start.
func browserStart(t *testing.T, goos, harness string, done []string) protocol.WorkerStartAnswer {
	t.Helper()
	g, node, owner, _, _ := setupFloorGateway(t)
	if _, err := g.store.db.Exec(`UPDATE nodes SET os=? WHERE id=?`, goos, node); err != nil {
		t.Fatal(err)
	}
	return browserStartOn(t, g, node, owner, harness, done)
}

func browserStartOn(t *testing.T, g *Gateway, node, owner, harness string, done []string) protocol.WorkerStartAnswer {
	t.Helper()
	ans, err := g.workerStart(context.Background(), g.conns[node], protocol.WorkerStartFrame{Owner: owner, Level: "normal",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: "test", Done: done, Harness: harness, Model: "auto"}}, true)
	if err != nil {
		t.Fatalf("workerStart: %v", err)
	}
	return ans
}

func hasBrowserFlag(ans protocol.WorkerStartAnswer) bool {
	for _, f := range ans.BriefFlags {
		if f.Check == protocol.BriefCheckBrowser {
			return true
		}
	}
	return false
}

// A Done that needs a browser never picks Claude on a Mac, and the pick notice
// says why Claude was left out.
func TestBrowserDoneLeavesOutClaudeOnDarwin(t *testing.T) {
	ans := browserStart(t, "darwin", "", []string{"A screenshot of the page is attached."})
	if ans.Harness == "claude" {
		t.Fatalf("picked claude for a browser check: %+v", ans)
	}
	if want := "left out, cannot run a browser here: claude claude-opus-5-5"; !strings.Contains(ans.ModelNote, want) {
		t.Errorf("model note lacks %q: %s", want, ans.ModelNote)
	}
	if hasBrowserFlag(ans) {
		t.Errorf("a capable harness was flagged: %+v", ans.BriefFlags)
	}
}

// A start without a browser check is picked as before: nothing is left out
// and nothing is flagged.
func TestNoBrowserDoneIsPickedAsBefore(t *testing.T) {
	ans := browserStart(t, "darwin", "", []string{"go test ./internal/gateway passes"})
	if strings.Contains(ans.ModelNote, "cannot run a browser") || hasBrowserFlag(ans) {
		t.Fatalf("a start with no browser check was changed: note %q flags %+v", ans.ModelNote, ans.BriefFlags)
	}
}

// With only Claude to pick, the start goes on and the flag says the browser
// checks wait for the hand-back. It is no refusal.
func TestBrowserDoneWithNoCapableCandidateStartsWithAFlag(t *testing.T) {
	g, node, owner, _, _ := setupFloorGateway(t)
	if _, err := g.store.db.Exec(`UPDATE nodes SET os='darwin' WHERE id=?`, node); err != nil {
		t.Fatal(err)
	}
	if err := g.store.saveWorkerProfiles([]protocol.WorkerProfile{{Name: "opus", Harness: "claude", Model: "claude-opus-5-5", Effort: "high"}}); err != nil {
		t.Fatal(err)
	}
	ans := browserStartOn(t, g, node, owner, "", []string{"Playwright tests pass"})
	if ans.Harness != "claude" {
		t.Fatalf("the start did not keep today's choice: %+v", ans)
	}
	if !hasBrowserFlag(ans) {
		t.Fatalf("no browser flag: %+v", ans.BriefFlags)
	}
}

// A named harness is kept, and the flag tells the caller.
func TestBrowserDoneKeepsANamedHarnessWithAFlag(t *testing.T) {
	ans := browserStart(t, "darwin", "claude", []string{"A screenshot of the page is attached."})
	if ans.Harness != "claude" {
		t.Fatalf("the named harness was overridden: %+v", ans)
	}
	if !hasBrowserFlag(ans) {
		t.Fatalf("no browser flag: %+v", ans.BriefFlags)
	}
}

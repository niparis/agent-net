package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Every Claude start shares one builder and one launcher, so a start path
// cannot drift from the others (view, settings, model, resume).
func TestClaudeBackgroundHasOneBuilder(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "claude_background.go" {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if strings.Contains(line, `"--bg"`) {
				t.Errorf("%s:%d builds claude --bg itself; use claudeBackgroundArgs", f, i+1)
			}
			if strings.Contains(line, "d.launchClaudeBackground(") {
				t.Errorf("%s:%d calls launchClaudeBackground; use startBackground", f, i+1)
			}
		}
	}
}

func TestClaudeBackgroundArgs(t *testing.T) {
	got := claudeBackgroundArgs(claudeBackground{Name: "shop", Settings: "{}", Resume: "n1", ModelArgs: []string{"--model", "haiku"}, Flags: []string{"--tools", ""}, FirstMessage: "hi"})
	want := []string{"--bg", "--tools", "", "--name", "shop", "--settings", "{}", "--resume", "n1", "--model", "haiku", "--", "hi"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("args %q, want %q", got, want)
	}
	if got := claudeBackgroundArgs(claudeBackground{}); len(got) != 1 || got[0] != "--bg" {
		t.Errorf("empty spec: %q", got)
	}
}

// A Claude start is a daemon-owned background job. It needs no herdr pane and
// no approving TUI, also for a CTO proposal, where a Codex start without
// either is refused.
func TestClaudeStartNeedsNoHerdrPaneOrApprovingTUI(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	c.register(t, repo)
	trustClaudeFolder(t, repo)
	t.Cleanup(func() { killFakeClaude(t, repo) })
	if c.d.herdrBinary("") != "" {
		t.Fatal("this test needs a machine without herdr")
	}
	r, err := c.d.sessionStartLocal(context.Background(), protocol.SessionStartRequest{Project: "shop", Harness: "claude", CTOProposal: true})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "started with no herdr pane and no approving TUI", func() bool {
		r, err = c.d.sessionStartGet(context.Background(), r.ID)
		if err != nil || r.Status == "failed" {
			t.Fatalf("start %+v %v", r, err)
		}
		return r.Status == "started" && r.SessionID != ""
	})
	if r.EnterPlan != nil || r.EnterNote == "" {
		t.Errorf("a start without a view must leave Enter to open it: %+v", r)
	}
	if rec := readClaude(t, repo); rec.Args[0] != "--bg" || argValue(rec.Args, "--name") != "shop" {
		t.Errorf("args %v", rec.Args)
	}
}

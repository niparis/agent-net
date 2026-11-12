package protocol

import (
	"strings"
	"testing"
)

// A brief without any of the six old format sections is no longer refused:
// decision 0141 retired them. The paths it names still feed the overlap check.
func TestRetiredBriefFormatSectionsDoNotFail(t *testing.T) {
	task := "Fix the parser crash in internal/parser/parse.go. Keep it small."
	if paths := BriefPaths(task); len(paths) != 1 || paths[0] != "internal/parser/parse.go" {
		t.Fatalf("paths = %v, want the one file", paths)
	}
}

func TestBriefPaths(t *testing.T) {
	got := BriefPaths("Edit internal/gateway/brief.go and docs/reference.md, then check /tmp/x. See https://example.com/not/a/path.")
	want := []string{"internal/gateway/brief.go", "docs/reference.md", "/tmp/x"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func TestBriefPathsShare(t *testing.T) {
	if !BriefPathsShare("internal/gateway/brief.go", "repo/internal/gateway/brief.go") {
		t.Fatal("the same file under a worktree path does not share")
	}
	if !BriefPathsShare("internal/gateway/brief.go", "internal/gateway/brief.go") {
		t.Fatal("the same path does not share")
	}
	if BriefPathsShare("internal/gateway/brief.go", "internal/gateway/brief_test.go") {
		t.Fatal("different files share")
	}
}

func TestBriefOverlapAllowed(t *testing.T) {
	if _, ok := BriefOverlapAllowed("Files: a.go"); ok {
		t.Fatal("a brief with no statement allowed overlap")
	}
	if reason, ok := BriefOverlapAllowed("Overlap OK: the other worker finishes first"); !ok || reason != "the other worker finishes first" {
		t.Fatalf("reason = %q ok = %v", reason, ok)
	}
	if _, ok := BriefOverlapAllowed("overlap ok:"); ok {
		t.Fatal("an empty reason allowed overlap")
	}
}

func TestBriefTaskKeyUsesTitleThenGoal(t *testing.T) {
	a := BriefTaskKey("s_caller", "shop", "Fix Parser", "Goal: owner asked\n")
	b := BriefTaskKey("s_caller", "shop", "  fix   parser ", "another task")
	if a != b {
		t.Fatalf("titles did not normalize: %q / %q", a, b)
	}
	c := BriefTaskKey("s_caller", "shop", "", "Goal: the owner asked \"fix the parser\"\nmore")
	d := BriefTaskKey("s_caller", "shop", "", "goal: THE OWNER ASKED \"FIX THE PARSER\"")
	if c != d {
		t.Fatalf("goals did not normalize: %q / %q", c, d)
	}
}

func TestBriefRepoPathsResolveOnlyBaseFiles(t *testing.T) {
	repo := WorkerBriefRepo{Root: "/repo/shop", Files: []string{"internal/parser.go", "internal/README.md", "docs/reference.md", "migrations.lock", "README"}}
	for _, tc := range []struct{ text, want string }{
		{"GOCACHE=/tmp/agent-net-gocache", ""},
		{"/home/owner/other/internal/parser.go", ""},
		{"$HOME/internal/parser.go", ""},
		{"~/internal/parser.go", ""},
		{"/repo/shop/internal/parser.go", "internal/parser.go"},
		{"/internal/parser.go", "internal/parser.go"},
		{"/internal", "internal/parser.go,internal/readme.md"},
		{"internal/new.go", ""},
		{"other/parser.go", ""},
		{"migrations.lock", "migrations.lock"},
		{"README", "readme"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got := strings.Join(repo.Paths(tc.text), ","); got != tc.want {
				t.Fatalf("paths = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBriefRepoPathsMatchOnlyUniqueBareFilenames(t *testing.T) {
	repo := WorkerBriefRepo{Root: "/repo/shop", Files: []string{
		"internal/daemon/claude_mouse.go", "internal/daemon/workers.go", "internal/gateway/workers.go",
	}}
	for _, tc := range []struct{ text, want string }{
		{"claude_mouse.go", "internal/daemon/claude_mouse.go"},
		{"workers.go", ""},
		{"/claude_mouse.go", ""},
		{"/internal/daemon/workers.go", "internal/daemon/workers.go"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got := strings.Join(repo.Paths(tc.text), ","); got != tc.want {
				t.Fatalf("paths = %q, want %q", got, tc.want)
			}
		})
	}
	// A root file does not override an ambiguous bare name.
	repo.Files = append(repo.Files, "workers.go")
	if got := repo.Paths("workers.go"); len(got) != 0 {
		t.Fatalf("ambiguous root basename matched: %v", got)
	}
	if got := strings.Join(repo.Paths("/workers.go"), ","); got != "workers.go" {
		t.Fatalf("explicit root file = %q", got)
	}
}

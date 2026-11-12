package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerBriefRepoReadsTheRequestedBase(t *testing.T) {
	repo := newRepo(t, "shop")
	gitIn(t, repo, "branch", "old-base")
	if err := os.WriteFile(filepath.Join(repo, "parser.go"), []byte("package shop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "parser.go")
	gitIn(t, repo, "commit", "-qm", "add parser")
	if err := os.WriteFile(filepath.Join(repo, "untracked.go"), []byte("package shop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{}
	for _, tc := range []struct{ base, want string }{{"old-base", "README.md"}, {"", "README.md,parser.go"}} {
		got, err := d.workerBriefRepo(context.Background(), protocol.WorkerBriefRepoRequest{Path: repo, Base: tc.base})
		if err != nil || got.Root != repo || strings.Join(got.Files, ",") != tc.want {
			t.Fatalf("base %q: %+v, %v", tc.base, got, err)
		}
	}
}

func TestWorkerBriefWarningsReachTheCaller(t *testing.T) {
	g := startGatewayWith(t, t.TempDir(), "", 0, func(opts *gateway.Options) { opts.TestNoBriefChecks = false })
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	task := "Goal: the owner asked to fix the parser.\nFiles or areas: README.md.\nLimits: temp dirs only, never the real setup.\nDefinition of done: the test passes.\nHow you are graded: the caller reads the diff.\nEvidence:\n- reproduction\n- failing test\n- diff\nRun GOCACHE=/tmp/agent-net-gocache go test.\n"
	req := protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: task}
	w.mustStart(t, req)
	req.Mode = protocol.WorkerStartModeCheck
	result, err := w.start(req)
	if err != nil || len(result.BriefFailures) != 0 || !strings.Contains(result.ModelNote, "warning: overlaps running worker") || !strings.Contains(result.ModelNote, "readme.md") || strings.Contains(result.ModelNote, "gocache") {
		t.Fatalf("check result: %+v, %v", result, err)
	}
	req.Mode = ""
	result, err = w.start(req)
	if err != nil || !strings.Contains(result.ModelNote, "warning: overlaps running worker") {
		t.Fatalf("start result: %+v, %v", result, err)
	}
}

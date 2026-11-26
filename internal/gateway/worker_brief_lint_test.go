package gateway

import (
	"bytes"
	"context"
	"log"
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A start of a brief with a problem returns its flag, and the gateway
// writes one log line with the checks that flagged it. Nothing is refused:
// the flags are advisory.
func TestWorkerStartLogsTheBriefLintChecks(t *testing.T) {
	a := newAgyGate(t)
	var logs bytes.Buffer
	a.g.logger = log.New(&logs, "", 0)
	client, received := connectTestNode(t, a.g, a.node)
	repo := briefTestRepo()
	repo.Commits = map[string]bool{"3fa9c1e2b7": false}
	serveBriefRepo(t, client, received, repo)

	task := "Use the fixture as in the brief above.\nThe bug first showed at commit 3fa9c1e2b7.\n"
	answer, err := a.g.workerStart(context.Background(), a.nc, protocol.WorkerStartFrame{Owner: a.owner, Level: "normal",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: task, Harness: "agy", Model: "gemini-3.8-flash-low"}})
	if err != nil {
		t.Fatalf("a flagged brief was refused: %v", err)
	}
	var checks []string
	for _, f := range answer.BriefFlags {
		checks = append(checks, f.Check)
	}
	if want := []string{protocol.BriefCheckCommit}; !reflect.DeepEqual(checks, want) {
		t.Fatalf("flags = %+v, want checks %v", answer.BriefFlags, want)
	}
	if line := logs.String(); !strings.Contains(line, "worker.brief_lint") || !strings.Contains(line, "checks=commit") {
		t.Fatalf("log = %q, want one worker.brief_lint line with the checks", line)
	}
}

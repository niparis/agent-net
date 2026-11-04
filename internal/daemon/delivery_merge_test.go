package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestDeliveryMergeChecksGitAncestry(t *testing.T) {
	n, f := startDeliveryNode(t)
	repo, wt := deliveryWorktree(t)
	lc, _ := helloDelivery(t, n, "merge-reporter", wt, "claude-opus-5-5")
	head, base := gitHead(t, wt, "HEAD"), gitHead(t, repo, "main")
	gitIn(t, repo, "merge", "--no-ff", "feat/x", "-m", "merge change")
	merged := gitHead(t, repo, "HEAD")
	f.answer(protocol.FrameChangeGet, func(json.RawMessage) (any, error) {
		return deliveryTestView(protocol.Change{ID: "ch_1", Project: "demo", HeadCommit: head}), nil
	})
	f.answer(protocol.FrameStepReport, func(raw json.RawMessage) (any, error) {
		var q protocol.StepReportRequest
		if err := json.Unmarshal(raw, &q); err != nil {
			return nil, err
		}
		if q.MergeContainsHead == nil {
			t.Error("new daemon omitted merge proof")
		}
		if q.MergeContainsHead != nil && !*q.MergeContainsHead {
			return nil, errors.New("merged_commit must exist and contain the change head")
		}
		return protocol.StepReportResult{}, nil
	})
	for _, tc := range []struct {
		name, commit string
		accepted     bool
	}{
		{"merge contains head", merged, true}, {"fast forward", head, true}, {"ancestor omits head", base, false}, {"missing commit", strings.Repeat("f", 40), false},
		{"tree is not a commit", gitHead(t, repo, "HEAD^{tree}"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := lc.call(protocol.OpStepReport, protocol.StepReportArgs{Change: "ch_1", StepRef: protocol.StepRef{Step: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: head, Fields: map[string]string{protocol.FieldMergedCommit: tc.commit}})
			if tc.accepted && err != nil {
				t.Fatal(err)
			}
			if !tc.accepted && (err == nil || !strings.Contains(err.Error(), "contain the change head")) {
				t.Fatalf("invalid merge accepted or unclear error: %v", err)
			}
			frames := f.frames(protocol.FrameStepReport)
			if len(frames) == 0 {
				t.Fatal("no merge report")
			}
			var q protocol.StepReportRequest
			if err := json.Unmarshal(frames[len(frames)-1], &q); err != nil {
				t.Fatal(err)
			}
			if q.MergeContainsHead == nil || *q.MergeContainsHead != tc.accepted {
				t.Fatalf("wrong proof for %s: %+v", tc.commit, q)
			}
		})
	}
}

func TestDeliveryMergeUsesRegisteredChangeRepo(t *testing.T) {
	n, f := startDeliveryNode(t)
	repo := newRepo(t, "demo")
	other := newRepo(t, "merger")
	lc, _ := helloDelivery(t, n, "assigned-merger", other, "claude-opus-5-5")
	head := gitHead(t, repo, "HEAD")
	f.answer(protocol.FrameChangeGet, func(json.RawMessage) (any, error) {
		return deliveryTestView(protocol.Change{ID: "ch_1", Project: "demo", HeadCommit: head}), nil
	})
	f.answer("query.projects", func(json.RawMessage) (any, error) {
		return protocol.ProjectsResult{Projects: []protocol.ProjectView{
			{Name: "demo", Path: "/not-a-local-repo", NodeID: "other-machine"},
			{Name: "demo", Path: repo, NodeID: n.d.NodeID()},
		}}, nil
	})
	f.answer(protocol.FrameStepReport, func(raw json.RawMessage) (any, error) {
		var q protocol.StepReportRequest
		if err := json.Unmarshal(raw, &q); err != nil {
			return nil, err
		}
		if q.MergeContainsHead == nil || !*q.MergeContainsHead {
			return nil, fmt.Errorf("did not check target repo: %+v", q)
		}
		return protocol.StepReportResult{}, nil
	})
	args := protocol.StepReportArgs{Change: "ch_1", StepRef: protocol.StepRef{Step: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: head, Fields: map[string]string{protocol.FieldMergedCommit: head}}
	if _, err := lc.call(protocol.OpStepReport, args); err != nil {
		t.Fatal(err)
	}
	f.answer("query.projects", func(json.RawMessage) (any, error) { return protocol.ProjectsResult{}, nil })
	if _, err := lc.call(protocol.OpStepReport, args); err == nil || !strings.Contains(err.Error(), "no local repository") {
		t.Fatalf("missing local repo did not fail clearly: %v", err)
	}
	if got := len(f.frames(protocol.FrameStepReport)); got != 1 {
		t.Fatalf("unverified report sent: %d", got)
	}
}

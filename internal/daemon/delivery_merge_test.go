package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		if q.MergeContainsHead != nil && *q.MergeContainsHead && q.Fields["merge_kind"] != "merge" {
			t.Errorf("wrong merge kind: %v", q.Fields)
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

func TestDeliveryMergeSquash(t *testing.T) {
	repo, wt := deliveryWorktree(t)
	head := gitHead(t, wt, "HEAD")
	gitIn(t, repo, "merge", "--squash", "feat/x")
	gitIn(t, repo, "commit", "-m", "squash")
	squash := gitHead(t, repo, "HEAD")
	if kind, _ := deliveryMergeKind(context.Background(), repo, head, squash); kind != "squash" {
		t.Fatal("squash refused")
	}
	if err := os.WriteFile(filepath.Join(repo, "extra"), []byte("extra"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "extra")
	gitIn(t, repo, "commit", "--amend", "--no-edit")
	if deliveryMergeContainsHead(context.Background(), repo, head, gitHead(t, repo, "HEAD")) {
		t.Fatal("extra change accepted")
	}
}

func TestDeliveryMergeRejectsUnrelatedTwoParentCommit(t *testing.T) {
	repo, wt := deliveryWorktree(t)
	head := gitHead(t, wt, "HEAD")
	gitIn(t, repo, "checkout", "-b", "other")
	if err := os.WriteFile(filepath.Join(repo, "other"), []byte("other"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "other")
	gitIn(t, repo, "commit", "-m", "other")
	gitIn(t, repo, "checkout", "main")
	gitIn(t, repo, "merge", "--no-ff", "other", "-m", "unrelated")
	if deliveryMergeContainsHead(context.Background(), repo, head, gitHead(t, repo, "HEAD")) {
		t.Fatal("unrelated merge accepted")
	}
}

func TestDeliveryMergeSquashEvidence(t *testing.T) {
	n, f := startDeliveryNode(t)
	repo, wt := deliveryWorktree(t)
	lc, _ := helloDelivery(t, n, "merge-reporter", wt, "claude-opus-5-5")
	head := gitHead(t, wt, "HEAD")
	gitIn(t, repo, "merge", "--squash", "feat/x")
	gitIn(t, repo, "commit", "-m", "squash")
	merged := gitHead(t, repo, "HEAD")
	f.answer(protocol.FrameChangeGet, func(json.RawMessage) (any, error) {
		return deliveryTestView(protocol.Change{ID: "ch_1", Project: "demo", HeadCommit: head}), nil
	})
	f.answer(protocol.FrameStepReport, func(raw json.RawMessage) (any, error) {
		var q protocol.StepReportRequest
		if err := json.Unmarshal(raw, &q); err != nil {
			return nil, err
		}
		if q.MergeContainsHead == nil || !*q.MergeContainsHead || q.Fields["merge_kind"] != "squash" {
			t.Errorf("wrong squash proof: %+v", q)
		}
		return protocol.StepReportResult{}, nil
	})
	_, err := lc.call(protocol.OpStepReport, protocol.StepReportArgs{Change: "ch_1", StepRef: protocol.StepRef{Step: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: head, Fields: map[string]string{protocol.FieldMergedCommit: merged, "merge_kind": "merge"}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeliveryReleaseFindsSquashTag(t *testing.T) {
	n, f := startDeliveryNode(t)
	repo, wt := deliveryWorktree(t)
	lc, _ := helloDelivery(t, n, "release-reporter", wt, "claude-opus-5-5")
	head := gitHead(t, wt, "HEAD")
	gitIn(t, repo, "merge", "--squash", "feat/x")
	gitIn(t, repo, "commit", "-m", "squash")
	merged := gitHead(t, repo, "HEAD")
	gitIn(t, repo, "tag", "v1.0.0")
	f.answer(protocol.FrameChangeGet, func(json.RawMessage) (any, error) {
		return deliveryTestView(protocol.Change{ID: "ch_1", Project: "demo", HeadCommit: head, MergedCommit: merged}), nil
	})
	f.answer(protocol.FrameStepReport, func(raw json.RawMessage) (any, error) {
		var q protocol.StepReportRequest
		if err := json.Unmarshal(raw, &q); err != nil {
			return nil, err
		}
		if q.Fields[protocol.FieldTag] != "v1.0.0" {
			t.Errorf("release tag = %q", q.Fields[protocol.FieldTag])
		}
		return protocol.StepReportResult{}, nil
	})
	_, err := lc.call(protocol.OpStepReport, protocol.StepReportArgs{Change: "ch_1", StepRef: protocol.StepRef{Step: protocol.StepRelease, AfterMerge: true}, Result: protocol.StepResultPass, Commit: merged})
	if err != nil {
		t.Fatal(err)
	}
}

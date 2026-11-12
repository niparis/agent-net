package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestDeliverySensitiveDiff(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"testdata/migrations.lock", "migrations"},
		{"internal/gateway/testdata/migrations.lock", "migrations"},
		{"internal/gateway/store.go", "internal/gateway/store.go"},
		{"internal/gateway/authorization.go", "internal/gateway/authorization.go"},
		{"internal/token.go", "internal/token.go"},
		{"internal/auth/provider.go", "internal/auth/provider.go"},
		{"internal/security/rule.go", "internal/security/rule.go"},
		{"internal/ToKeN/provider.go", "internal/ToKeN/provider.go"},
		{"internal/tui/view.go", ""},
		{"docs/security.md", "docs/security.md"},
		{"internal/daemon/files_send.go", "internal/daemon/files_send.go"},
		{"install.sh", "install.sh"},
		{".github/workflows/test.yml", ".github/workflows/test.yml"},
		{"docs/readme.md", ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			repo := newRepo(t, "demo")
			base := gitHead(t, repo, "HEAD")
			name := filepath.Join(repo, tc.path)
			if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(name, []byte("changed\n"), 0600); err != nil {
				t.Fatal(err)
			}
			gitIn(t, repo, "add", ".")
			gitIn(t, repo, "commit", "-qm", "change")
			if got := deliverySensitivePath(context.Background(), repo, base, gitHead(t, repo, "HEAD")); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestDeliveryHandbackCarriesReviewRisk(t *testing.T) {
	n, f := startDeliveryNode(t)
	f.answer(protocol.FrameDeliveryNeedsPush, func(json.RawMessage) (any, error) {
		return protocol.DeliveryNeedsPushResult{}, nil
	})
	repo, wt := deliveryWorktree(t)
	if err := os.WriteFile(filepath.Join(wt, "security.go"), []byte("sensitive\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", ".")
	gitIn(t, wt, "commit", "-qm", "sensitive")
	w := &worker{id: "w_risk", worktree: wt, branch: "feat/x", base: gitHead(t, repo, "main"), owner: "s_owner", sessionID: "s_worker", project: "demo", difficultyUsed: "normal"}
	if err := n.d.recordDeliveryRisk(w.id, &protocol.WorkerAssessment{Reasoning: 1, Risk: 4}); err != nil {
		t.Fatal(err)
	}
	id, problem := n.d.recordHandbackChange(context.Background(), w, nil)
	if id == "" || problem != "" {
		t.Fatalf("id=%q problem=%q", id, problem)
	}
	frames := f.frames(protocol.FrameChangeSubmit)
	var q protocol.ChangeSubmitRequest
	if len(frames) != 1 {
		t.Fatalf("frames=%d", len(frames))
	}
	if err := json.Unmarshal(frames[0], &q); err != nil {
		t.Fatal(err)
	}
	if q.Risk != 4 || q.SensitivePath != "security.go" {
		t.Fatalf("risk=%d sensitive_path=%q want 4 and security.go", q.Risk, q.SensitivePath)
	}
}

func TestDeliverySensitiveRenameAndDeletion(t *testing.T) {
	repo := newRepo(t, "demo")
	if err := os.WriteFile(filepath.Join(repo, "auth.go"), []byte("sensitive\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-qm", "authority")
	base := gitHead(t, repo, "HEAD")
	gitIn(t, repo, "mv", "auth.go", "plain.go")
	gitIn(t, repo, "commit", "-qm", "rename")
	if got := deliverySensitivePath(context.Background(), repo, base, gitHead(t, repo, "HEAD")); got != "auth.go" {
		t.Fatalf("rename lost sensitive old path: %q", got)
	}
	gitIn(t, repo, "reset", "--hard", base)
	gitIn(t, repo, "rm", "auth.go")
	gitIn(t, repo, "commit", "-qm", "delete")
	if got := deliverySensitivePath(context.Background(), repo, base, gitHead(t, repo, "HEAD")); got != "auth.go" {
		t.Fatalf("deletion lost sensitive path: %q", got)
	}
	if got := deliverySensitivePath(context.Background(), repo, "", gitHead(t, repo, "HEAD")); got != "diff unavailable" {
		t.Fatalf("missing base skipped review: %q", got)
	}
}

// The risk comes from the start assessment, through the worker's local
// record, into a change stored on the gateway. No real judge is called.
func TestDeliveryHandbackUsesJudgeRisk(t *testing.T) {
	setupShadowJudge(t, strings.Replace(normalJudgeReply, `"risk":3`, `"risk":4`, 1), false)
	c, _, _ := deliveryHandbackNode(t, withDifficulty(nil))
	caller, callerID := deliveryCaller(t, c)
	res := startDeliveryWorker(t, c, caller, callerID, protocol.WorkerStartRequest{
		Model: "sonnet", Project: "shop", Harness: "claude", Task: "FAKE:dirty FAKE:handback", Isolation: protocol.IsolationWorktree,
	})
	handbackNotice(t, c, callerID, res.SessionID)
	found := findChanges(t, caller, protocol.ChangesFindArgs{Project: "shop"})
	if len(found.Items) != 1 || found.Items[0].Change.Risk != 4 {
		t.Fatalf("hand-back lost start risk: %+v", found.Items)
	}
	for _, st := range found.Items[0].Steps {
		if st.Step == protocol.StepReview && (st.State == protocol.StepStateSkipped || !strings.Contains(st.Note, "required: risk 4")) {
			t.Fatalf("high-risk normal change skipped review: %+v", st)
		}
	}
}

func TestDeliverySubmitWithoutDiffRequiresReview(t *testing.T) {
	n, f := startDeliveryNode(t)
	_, wt := deliveryWorktree(t)
	if err := os.WriteFile(filepath.Join(wt, "install.sh"), []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", ".")
	gitIn(t, wt, "commit", "-qm", "installer")
	lc, _ := helloDelivery(t, n, "submit-no-base", wt, "claude-opus-5-5")
	for _, target := range []string{"", "missing-target"} {
		t.Run("target="+target, func(t *testing.T) {
			lc.mustCall(protocol.OpChangeSubmit, protocol.ChangeSubmitArgs{TargetBranch: target})
			frames := f.frames(protocol.FrameChangeSubmit)
			var q protocol.ChangeSubmitRequest
			if err := json.Unmarshal(frames[len(frames)-1], &q); err != nil {
				t.Fatal(err)
			}
			if q.BaseCommit != "" || q.SensitivePath != "diff unavailable" {
				t.Fatalf("unknown diff skipped review: %+v", q)
			}
			c := protocol.Change{HeadCommit: q.HeadCommit, SensitivePath: q.SensitivePath}
			for _, st := range protocol.PipelineToday().Status(c, nil) {
				if st.Step == protocol.StepReview && st.State == protocol.StepStateSkipped {
					t.Fatal("unknown diff left review out")
				}
			}
		})
	}
}

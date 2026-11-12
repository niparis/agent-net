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

// TestOperatorDeliveryOwnerSetRefusesUnknownArgument is the CTO's dry_run
// call through the operator path: the method has no preview mode, so the
// argument must fail and be named instead of dropped.
func TestOperatorDeliveryOwnerSetRefusesUnknownArgument(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	_, err := cliClient(t, n).call(protocol.OpDeliveryOwnerSet, map[string]any{
		"project": "demo", "step": "accept", "dry_run": true,
	})
	if err == nil || !strings.Contains(err.Error(), `unknown argument "dry_run"`) || !strings.Contains(err.Error(), protocol.OpDeliveryOwnerSet) {
		t.Fatalf("dry_run was not refused by name: %v", err)
	}
}

// The two daemons use a real gateway and local, fake Pi sessions.
func TestDeliveryNodeStepReachesOtherMachine(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	a := startNode(t, g, nodeOpts{cmdline: "pi"})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	repo := newRepo(t, "demo")
	merger := newRepo(t, "merger")
	caller, callerID := helloDelivery(t, a, "caller", repo, "claude-opus-4")
	owner, ownerID := helloDelivery(t, b, "merger", merger, "gpt-6")
	eventually(t, "both sessions reach the gateway", func() bool {
		first, ok1, _ := g.gw.Store().Agent(callerID)
		second, ok2, _ := g.gw.Store().Agent(ownerID)
		return ok1 && ok2 && first.Online && second.Online && first.Repo.Project == "demo" && second.Repo.Project == "merger"
	})
	cliClient(t, a).mustCall(protocol.OpDeliveryOwnerSet, protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "accept", Node: "merger"})
	eventually(t, "the assignment reaches session context and worker briefs", func() bool {
		callerContext := a.d.charterContext(context.Background(), "demo")
		callerBrief := a.d.charterBrief(context.Background(), "demo")
		ownerContext := b.d.charterContext(context.Background(), "merger")
		ownerBrief := b.d.charterBrief(context.Background(), "merger")
		return strings.Contains(callerContext, "merger owns accept for your project.") &&
			strings.Contains(callerBrief, "merger owns accept for your project.") &&
			strings.Contains(ownerContext, "You own accept for demo.") &&
			!strings.Contains(ownerBrief, "You own accept for demo.")
	})
	var submitted protocol.ChangeSubmitResult
	if err := json.Unmarshal(caller.mustCall(protocol.OpChangeSubmit, protocol.ChangeSubmitArgs{}), &submitted); err != nil {
		t.Fatal(err)
	}
	id := submitted.Change.Change.ID
	t.Cleanup(func() {
		if t.Failed() {
			v, err := g.gw.Store().GetChange(id)
			messages, mErr := g.gw.Store().Messages(b.d.NodeID(), protocol.QueryMessages{SessionID: ownerID})
			t.Logf("change: %+v, error %v; messages: %+v, error %v; gateway log: %v", v, err, messages, mErr, g.logs.lines())
		}
	})
	var assigned bool
	for _, step := range submitted.Change.Steps {
		if step.Step == protocol.StepAccept && step.Owner == (protocol.StepOwner{Kind: protocol.OwnerNode, Node: "merger"}) {
			assigned = true
		}
	}
	if !assigned {
		t.Fatalf("accept was not assigned: %+v", submitted.Change.Steps)
	}
	var msg protocol.Message
	eventually(t, "the remote step owner receives the task", func() bool {
		select {
		case msg = <-owner.Pushes:
			return msg.From == "agent-net" && strings.Contains(msg.Body, id) && strings.Contains(msg.Body, "accept")
		default:
			return false
		}
	})
	if msg.To != ownerID {
		t.Fatalf("task delivered to %s, want %s", msg.To, ownerID)
	}
	var claim struct {
		Claimed   bool   `json:"claimed"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(owner.mustCall("step_claim", map[string]any{"change": id, "step": "accept", "commit": submitted.Change.Change.HeadCommit}), &claim); err != nil || !claim.Claimed || claim.SessionID != ownerID {
		t.Fatalf("claim: %+v, %v", claim, err)
	}
	result := reportStep(t, owner, protocol.StepReportArgs{Change: id, StepRef: protocol.StepRef{Step: protocol.StepAccept}, Result: protocol.StepResultPass, Commit: submitted.Change.Change.HeadCommit})
	if result.Evidence.By != ownerID {
		t.Fatalf("reported by %q, want %q", result.Evidence.By, ownerID)
	}
}

func TestDeliveryNodeStepWaitsForSession(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	a := startNode(t, g, nodeOpts{cmdline: "pi"})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	repo := newRepo(t, "demo")
	merger := newRepo(t, "merger")
	caller, callerID := helloDelivery(t, a, "caller", repo, "claude-opus-4")
	eventually(t, "caller reaches the gateway", func() bool {
		first, ok, _ := g.gw.Store().Agent(callerID)
		return ok && first.Online && first.Repo.Project == "demo"
	})
	if err := g.gw.Store().AddArchitect("merger", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetEdge(protocol.Edge{From: "merger", To: "demo", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	var submitted protocol.ChangeSubmitResult
	if err := json.Unmarshal(caller.mustCall(protocol.OpChangeSubmit, protocol.ChangeSubmitArgs{}), &submitted); err != nil {
		t.Fatal(err)
	}
	id := submitted.Change.Change.ID
	if !strings.Contains(submitted.Change.Steps[0].Note, "waiting for merger (offline)") {
		t.Fatalf("offline note: %+v", submitted.Change.Steps[0])
	}
	owner, ownerID := helloDelivery(t, b, "merger", merger, "gpt-6")
	eventually(t, "the session coming online receives its queued task", func() bool {
		select {
		case msg := <-owner.Pushes:
			return msg.To == ownerID && msg.From == "agent-net" && strings.Contains(msg.Body, id)
		default:
			return false
		}
	})
}

func TestDeliveryNodeTaskIncludesWorkerDiffStat(t *testing.T) {
	c, g, _ := deliveryHandbackNode(t, nil)
	caller, callerID := deliveryCaller(t, c)
	other := startNode(t, g, nodeOpts{cmdline: "pi"})
	repo := newRepo(t, "merger")
	owner, ownerID := helloDelivery(t, other, "merger", repo, "gpt-6")
	eventually(t, "the merger session reaches the gateway", func() bool {
		a, ok, _ := g.gw.Store().Agent(ownerID)
		return ok && a.Online && a.Repo.Project == "merger"
	})
	if err := g.gw.Store().SetEdge(protocol.Edge{From: "merger", To: "shop", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	w := startDeliveryWorker(t, c, caller, callerID, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude", Title: "Add a file", Task: "FAKE:dirty FAKE:handback", Isolation: protocol.IsolationWorktree})
	_ = handbackNotice(t, c, callerID, w.SessionID)
	eventually(t, "the remote task includes the committed diff stat", func() bool {
		select {
		case msg := <-owner.Pushes:
			return strings.Contains(msg.Body, "diff stat:\n") && strings.Contains(msg.Body, "dirty.txt")
		default:
			return false
		}
	})
}

func TestDeliveryPushesWorkerBranchForRemoteOwner(t *testing.T) {
	c, g, repo := deliveryHandbackNode(t, nil)
	bare := filepath.Join(t.TempDir(), "shop.git")
	if err := os.MkdirAll(bare, 0o700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, bare, "init", "--bare", "-q")
	gitIn(t, repo, "remote", "add", "origin", bare)
	caller, callerID := deliveryCaller(t, c)
	other := startNode(t, g, nodeOpts{cmdline: "pi"})
	ownerRepo := newRepo(t, "merger")
	_, ownerID := helloDelivery(t, other, "merger", ownerRepo, "gpt-6")
	eventually(t, "remote owner reaches the gateway", func() bool {
		a, ok, _ := g.gw.Store().Agent(ownerID)
		return ok && a.Online && a.Repo.Project == "merger"
	})
	if err := g.gw.Store().SetEdge(protocol.Edge{From: "merger", To: "shop", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	w := startDeliveryWorker(t, c, caller, callerID, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude", Title: "Remote review", Task: "FAKE:dirty FAKE:handback", Isolation: protocol.IsolationWorktree})
	_ = handbackNotice(t, c, callerID, w.SessionID)
	changes := findChanges(t, caller, protocol.ChangesFindArgs{Project: "shop"})
	if len(changes.Items) != 1 {
		t.Fatalf("changes = %+v", changes.Items)
	}
	if got := gitOutT(t, bare, "rev-parse", "refs/heads/"+w.Branch); got != changes.Items[0].Change.HeadCommit {
		t.Fatalf("remote branch head = %q, change = %q", got, changes.Items[0].Change.HeadCommit)
	}
}

func TestDeliveryOwnerApprovalRoutesNextNodeStep(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	a := startNode(t, g, nodeOpts{cmdline: "pi"})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	repo := newRepo(t, "demo")
	gitIn(t, repo, "checkout", "-q", "-b", "feat/routing")
	merger := newRepo(t, "merger")
	caller, callerID := helloDelivery(t, a, "caller", repo, "claude-opus-4")
	owner, ownerID := helloDelivery(t, b, "merger", merger, "gpt-6")
	cli := cliClient(t, a)
	eventually(t, "both sessions reach the gateway", func() bool {
		first, ok1, _ := g.gw.Store().Agent(callerID)
		second, ok2, _ := g.gw.Store().Agent(ownerID)
		return ok1 && ok2 && first.Online && second.Online && first.Repo.Project == "demo" && second.Repo.Project == "merger"
	})
	if _, err := g.gw.Store().WriteSettings([]protocol.SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"ask"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetEdge(protocol.Edge{From: "merger", To: "demo", Kind: "merges"}, "owner"); err != nil {
		t.Fatal(err)
	}
	var submitted protocol.ChangeSubmitResult
	if err := json.Unmarshal(caller.mustCall(protocol.OpChangeSubmit, protocol.ChangeSubmitArgs{TargetBranch: "main"}), &submitted); err != nil {
		t.Fatal(err)
	}
	id, commit := submitted.Change.Change.ID, submitted.Change.Change.HeadCommit
	reportStep(t, caller, protocol.StepReportArgs{Change: id, StepRef: protocol.StepRef{Step: protocol.StepAccept}, Result: protocol.StepResultPass, Commit: commit})
	reportStep(t, caller, protocol.StepReportArgs{Change: id, StepRef: protocol.StepRef{Step: protocol.StepTest}, Result: protocol.StepResultPass, Commit: commit, Fields: map[string]string{protocol.FieldSuite: protocol.SuiteChanged}})
	items, err := inboxAndLog(g.gw.Store())
	if err != nil || len(items) != 1 || !strings.Contains(items[0].Text, commit) {
		t.Fatalf("approval: %+v, %v", items, err)
	}
	cli.mustCall("inbox_answer", map[string]any{"id": items[0].ID, "answer": "approve"})
	eventually(t, "approval sends the merge task to the other machine", func() bool {
		select {
		case msg := <-owner.Pushes:
			return strings.Contains(msg.Body, "step: merge") && strings.Contains(msg.Body, id)
		default:
			return false
		}
	})
}

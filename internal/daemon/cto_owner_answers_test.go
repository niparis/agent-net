package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoOwnerOpFor runs one owner operation the way cto_op does: the
// settings_change operator path with the CTO as the actor.
func ctoOwnerOpFor(n *node, method string, args map[string]any) (any, error) {
	raw, _ := json.Marshal(map[string]any{"area": "operator", "method": method, "arguments": args})
	return n.d.runCTOTool(context.Background(), "settings_change", raw)
}

func (r ctoPermissionRig) ctoOwnerOp(method string, args map[string]any) (any, error) {
	return ctoOwnerOpFor(r.n, method, args)
}

// The CTO completes an owner merge approval on the owner's quoted words
// through the real operator path, with no stored Desk item.
func TestCTODeliveryOwnerAnswerOnOwnerWords(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	store := g.gw.Store()
	if _, err := store.WriteSettings([]protocol.SettingsChange{{Key: "delivery.merge_authority", Level: "network", Scope: "network", Value: json.RawMessage(`"ask"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	caller, err := store.UpsertSession(n.cfg.NodeID, protocol.SessionInfo{Harness: "claude", NativeID: "caller", Repo: protocol.RepoInfo{Root: "/code/demo", Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("a", 40)
	submitted, err := store.SubmitChange(n.cfg.NodeID, protocol.ChangeSubmitRequest{SessionID: caller, Project: "demo", Branch: "feature/owner",
		TargetBranch: "main", BaseCommit: strings.Repeat("0", 40), HeadCommit: head, Origin: protocol.ChangeOrigin{Kind: protocol.OriginSession, SessionID: caller}, Title: "Fix the login"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	words := "go ahead and merge it"
	ctoOwnerSays(t, n.d, "ok, "+words+" please")
	got, err := ctoOwnerOpFor(n, protocol.OpDeliveryOwnerAnswer, map[string]any{"change": submitted.Change.Change.ID, "step": protocol.StepApprove,
		"for": protocol.StepMerge, "commit": head, "result": protocol.StepResultPass, "owner_words": words})
	if err != nil {
		t.Fatalf("delivery_owner_answer: %v", err)
	}
	data, _ := json.Marshal(got)
	var result protocol.StepReportResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Evidence.By != protocol.EvidenceByOwner || result.Evidence.Commit != head {
		t.Fatalf("evidence: %+v", result.Evidence)
	}
	items, err := store.ListInbox()
	if err != nil || len(items) != 0 {
		t.Fatalf("stored items = %+v, %v", items, err)
	}
}

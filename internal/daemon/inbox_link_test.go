package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestInboxApprovalDismissOperatorLink(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa})
	fa.setSessions(fakeSession("asker", "asker"))
	eventually(t, "session registered", func() bool { return fa.host.SessionID("fake", "asker") != "" })
	sid := fa.host.SessionID("fake", "asker")
	raw, err := n.d.call(context.Background(), "inbox.create", map[string]string{"session_id": sid, "kind": "approval", "text": "Allow action?", "recommendation": "Allow"})
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Item protocol.InboxItem `json:"item"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.Item.ID == "" {
		t.Fatalf("create: %s %v", raw, err)
	}
	c := operator.New(n.sock)
	defer c.Close()
	if _, err := c.Call(context.Background(), "inbox_answer", map[string]string{"id": created.Item.ID, "answer": "dismiss"}); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	raw, err = c.Call(context.Background(), "inbox_list", nil)
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Items []protocol.InboxItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	for _, item := range listed.Items {
		if item.ID == created.Item.ID && item.Status == "dismissed" {
			return
		}
	}
	t.Fatalf("dismissed item missing: %s", raw)
}

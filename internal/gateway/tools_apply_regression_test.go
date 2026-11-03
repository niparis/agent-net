package gateway

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestToolApplyLogsFailedTargets(t *testing.T) {
	g, n, _ := toggleFixture(t)
	id, bundle := toggleBundle(t, g, n, "# Demo\nFixture.\n")
	toggleFakeNode(t, g, n, bundle, nil)
	p, err := g.toggleTool(t.Context(), protocol.ToolToggleRequest{Item: id, Project: "one", On: false}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Targets) == 0 {
		t.Fatal("no targets")
	}
	// Simulate machine failures arriving as soon as the revision is stored.
	_, err = g.store.db.Exec(`CREATE TRIGGER fail_tool_targets AFTER INSERT ON tool_revisions BEGIN
 INSERT INTO tool_results(item,revision,target_index,node_id,state,reason)
 SELECT NEW.item,NEW.revision,CAST(t.key AS INTEGER),json_extract(t.value,'$.node_id'),'failed','cannot lock Git index'
 FROM tool_plans p, json_each(p.plan,'$.targets') t WHERE NEW.source='plan:' || p.id;
 END`)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	g.logger = log.New(&logs, "", 0)
	res, err := g.applyTool(t.Context(), p.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range res.Targets {
		if target.State != "failed" {
			t.Fatalf("outcome: %+v", res)
		}
	}
	if !strings.Contains(logs.String(), "targets failed") || strings.Contains(logs.String(), "result=ok") {
		t.Fatalf("apply failure log: %s", logs.String())
	}
}

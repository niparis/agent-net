package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Decision 0175: the lead session of an agent may set the status of a friction
// report through its own tool call. Another session of the same agent is
// refused, and the refusal comes from the gateway.
func TestFrictionMarkByAgentNeedsLeadSession(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n1 := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi", interval: time.Hour}, nil)
	// The first session of a lead agent is pinned as its lead; the second is not.
	lead, leadSID := helloPi(t, n1, "friction-lead", "/w/friction-lead", true)
	plain, _ := helloPi(t, n1, "friction-plain", "/w/friction-lead", true)
	if _, err := g.gw.Store().ReportFriction(protocol.FrictionReportRequest{SessionID: "s_friction", Project: "shop", Harness: "claude",
		Entries: []protocol.FrictionEntry{{Area: protocol.FrictionCI, Text: "the suite flaked"}}}); err != nil {
		t.Fatal(err)
	}
	stored, err := g.gw.Store().FrictionFind(protocol.FrictionQuery{})
	if err != nil || len(stored.Entries) != 1 {
		t.Fatalf("stored report: %+v %v", stored, err)
	}
	id := stored.Entries[0].ID
	mark := map[string]any{"ids": []int64{id}, "status": protocol.FrictionTriaged}
	if _, err := plain.call(protocol.OpFrictionMarkLead, mark); err == nil || !strings.Contains(err.Error(), "only a lead session") {
		t.Fatalf("a session that is not a lead: %v", err)
	}
	raw, err := lead.call(protocol.OpFrictionMarkLead, mark)
	if err != nil {
		t.Fatalf("the lead's mark: %v", err)
	}
	var res protocol.FrictionMarkResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if res.Matched != 1 || res.Marked != 1 {
		t.Fatalf("the lead's mark result: %+v", res)
	}
	got, err := g.gw.Store().FrictionFind(protocol.FrictionQuery{ID: id})
	if err != nil || len(got.Entries) != 1 {
		t.Fatalf("report after the lead's mark: %+v %v", got, err)
	}
	if got.Entries[0].Status != protocol.FrictionTriaged || got.Entries[0].ChangedBy != leadSID {
		t.Fatalf("the lead's mark is not the lead session's: %+v", got.Entries[0])
	}
}

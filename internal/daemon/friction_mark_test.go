package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Decision 0175: only the owner's command line and the CTO set the read status
// of a friction report. An agent session is refused. The owner's mark reaches
// the gateway's table with the owner as its author.
func TestFrictionMarkIsOwnerOnly(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n1 := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi", interval: time.Hour}, nil)
	agent, _ := helloPi(t, n1, "friction-agent", "/w/friction", true)
	cli := cliClient(t, n1)
	if _, err := g.gw.Store().ReportFriction(protocol.FrictionReportRequest{SessionID: "s_friction", Project: "shop", Harness: "claude",
		Entries: []protocol.FrictionEntry{{Area: protocol.FrictionCI, Text: "the suite flaked"}}}); err != nil {
		t.Fatal(err)
	}
	stored, err := g.gw.Store().FrictionFind(protocol.FrictionQuery{})
	if err != nil || len(stored.Entries) != 1 {
		t.Fatalf("stored report: %+v %v", stored, err)
	}
	id := stored.Entries[0].ID
	mark := map[string]any{"ids": []int64{id}, "status": protocol.FrictionRead}
	if _, err := agent.call(protocol.OpFrictionMark, mark); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
		t.Fatalf("agent friction_mark gate: %v", err)
	}
	var res protocol.FrictionMarkResult
	if err := json.Unmarshal(cli.mustCall(protocol.OpFrictionMark, mark), &res); err != nil {
		t.Fatal(err)
	}
	if res.Matched != 1 || res.Marked != 1 || res.Status != protocol.FrictionRead {
		t.Fatalf("owner mark: %+v", res)
	}
	got, err := g.gw.Store().FrictionFind(protocol.FrictionQuery{ID: id})
	if err != nil || len(got.Entries) != 1 {
		t.Fatalf("report after the mark: %+v %v", got, err)
	}
	if got.Entries[0].Status != protocol.FrictionRead || got.Entries[0].ChangedBy != "owner" {
		t.Fatalf("the mark is not the owner's: %+v", got.Entries[0])
	}
}

package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A session files friction with no step. The report reaches the same store
// friction_find reads, so the digest sees it too. The project defaults to the
// session's project, and a change is optional metadata.
func TestFrictionReportFilesWithoutAStep(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	repo := newRepo(t, "shop")
	if _, err := g.gw.Store().AddProjects(n.d.NodeID(), protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "shop", Path: repo}}}); err != nil {
		t.Fatal(err)
	}
	agent, sid := helloDelivery(t, n, "friction-report", repo, "claude-opus-4")
	eventually(t, "session and repo registered", func() bool {
		a, ok, err := g.gw.Store().Agent(sid)
		return err == nil && ok && a.Repo.Project == "shop"
	})

	raw, err := agent.call(protocol.OpFrictionReport, protocol.FrictionReportArgs{
		Friction: []protocol.FrictionEntry{{Area: protocol.FrictionEnvironment, Text: "the sandbox cannot bind a local port"}},
	})
	if err != nil {
		t.Fatalf("friction report: %v", err)
	}
	var res protocol.FrictionReportResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if res.Stored != 1 {
		t.Fatalf("stored %d rows, want 1", res.Stored)
	}
	got, err := g.gw.Store().FrictionFind(protocol.FrictionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("friction_find rows: %+v", got)
	}
	e := got.Entries[0]
	if e.Session != sid || e.Project != "shop" || e.Change != "" || e.Harness != "pi" || e.Model != "claude-opus-4" {
		t.Fatalf("stored report: %+v", e)
	}
	if !strings.Contains(e.Text, "cannot bind a local port") {
		t.Fatalf("text: %q", e.Text)
	}
	// The digest groups reports by area and text, and a new report is unread.
	if e.Group != protocol.FrictionGroup(protocol.FrictionEnvironment, "the sandbox cannot bind a local port") || e.Status != protocol.FrictionNew {
		t.Fatalf("digest fields: group %q, status %q", e.Group, e.Status)
	}

	raw, err = agent.call(protocol.OpFrictionReport, protocol.FrictionReportArgs{
		Friction: []protocol.FrictionEntry{{Area: protocol.FrictionCI, Text: "the browser suite hung for four hours"}},
		Change:   "ch_1234",
		Project:  "other",
	})
	if err != nil {
		t.Fatalf("friction report with a change: %v", err)
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if res.Stored != 1 {
		t.Fatalf("stored %d rows, want 1", res.Stored)
	}
	got, err = g.gw.Store().FrictionFind(protocol.FrictionQuery{Project: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 || got.Entries[0].Change != "ch_1234" {
		t.Fatalf("report with a change: %+v", got.Entries)
	}
}

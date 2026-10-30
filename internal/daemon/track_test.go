package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A worker that hands back partial work marks the run partial in the gateway's
// track record, and the next start shows that record and the playbook note.
func TestPartialHandbackTrackRecordAndPlaybook(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	if err := g.gw.Store().SetPlaybook("fake/m1", "opencode", "Ask for the diff before you trust it."); err != nil {
		t.Fatal(err)
	}

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:handback FAKE:partial"})
	if len(res.Playbooks) != 1 || res.Playbooks[0].Note != "Ask for the diff before you trust it." {
		t.Errorf("playbook note in the start result: %+v", res.Playbooks)
	}
	if res.Track == nil || res.Track.Runs != 0 || !strings.Contains(res.Track.Line(), "no runs yet") {
		t.Errorf("track record before the first run: %+v", res.Track)
	}
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "status: done") {
		t.Errorf("notice:\n%s", n.Body)
	}

	var tr protocol.TrackRecord
	eventually(t, "the partial run reaches the track record", func() bool {
		got, ok, err := g.gw.Store().TrackRecord(w.d.NodeID(), "opencode", "fake/m1", 30*24*time.Hour)
		if err != nil || !ok {
			return false
		}
		tr = *got
		return tr.Runs == 1
	})
	if tr.Partial != 1 || tr.Complete != 0 || tr.MedianRunSeconds < 0 {
		t.Errorf("track record: %+v", tr)
	}

	// The machine list shows the same line for the harness and model.
	var ni protocol.NodesInfo
	mustJSON(t, w.cli.mustCall("nodes", nil), &ni)
	var found bool
	for _, node := range ni.Nodes {
		if node.NodeID != w.d.NodeID() {
			continue
		}
		for _, track := range node.Tracks {
			if track.Model == "fake/m1" && track.Harness == "opencode" && track.Partial == 1 {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("agent_nodes has no track record: %+v", ni.Nodes)
	}

	res2 := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "say ok"})
	if res2.Track == nil || res2.Track.Runs != 1 || res2.Track.Partial != 1 || !strings.Contains(res2.Track.Line(), "1 partial") {
		t.Errorf("the next start's track record: %+v", res2.Track)
	}
	if len(res2.Playbooks) != 1 || res2.Playbooks[0].Note == "" {
		t.Error("the next start lost the playbook note")
	}
}

func TestPlaybookOwnerGateAndCLI(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi", interval: time.Hour}, nil)
	agent, _ := helloPi(t, n, "book-agent", "/w/book", true)
	cli := cliClient(t, n)
	if _, err := agent.call("playbook_set", protocol.PlaybookSetRequest{Model: "m", Note: "x"}); err == nil ||
		!strings.Contains(err.Error(), "only available to the owner") {
		t.Fatalf("agent gate: %v", err)
	}
	cli.mustCall("playbook_set", protocol.PlaybookSetRequest{Model: "terra", Harness: "pi", Note: "Ask for a checklist."})
	var got protocol.PlaybookGetResult
	mustJSON(t, cli.mustCall("playbook_get", protocol.PlaybookGetRequest{Model: "terra", Harness: "pi"}), &got)
	if got.Note == nil || got.Note.Note != "Ask for a checklist." || got.Note.Harness != "pi" {
		t.Errorf("get: %+v", got)
	}
	// A model-wide note answers when the harness has none.
	cli.mustCall("playbook_set", protocol.PlaybookSetRequest{Model: "terra", Note: "Whole model."})
	cli.mustCall("playbook_set", protocol.PlaybookSetRequest{Model: "terra", Harness: "pi"})
	mustJSON(t, cli.mustCall("playbook_get", protocol.PlaybookGetRequest{Model: "terra", Harness: "pi"}), &got)
	if got.Note == nil || got.Note.Note != "Whole model." {
		t.Errorf("fallback: %+v", got)
	}
}

func TestAgentLearnThroughDaemon(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi", interval: time.Hour}, nil)
	agent, agentID := helloPi(t, n, "learn-agent", "/w/learn", true)
	raw, err := agent.call("agent_learn", protocol.LearnRequest{SubjectKind: protocol.SubjectModel, Subject: "terra",
		Finding: "Hands back partial work.", Evidence: []string{"s_w1"}})
	if err != nil {
		t.Fatal(err)
	}
	var res protocol.LearnResult
	mustJSON(t, raw, &res)
	if !res.Accepted || res.ID == 0 || res.Remaining != 2 {
		t.Errorf("learn result: %+v", res)
	}
	findings, err := g.gw.Store().Findings(protocol.SubjectModel, "terra", 10)
	if err != nil || len(findings) != 1 || findings[0].Reporter != agentID || findings[0].Finding != "Hands back partial work." {
		t.Errorf("findings: %+v %v", findings, err)
	}
	// The command line is not an agent session.
	cli := cliClient(t, n)
	if _, err := cli.call("agent_learn", protocol.LearnRequest{SubjectKind: protocol.SubjectModel, Subject: "x", Finding: "y"}); err == nil ||
		!strings.Contains(err.Error(), "not available to cli clients") {
		t.Errorf("cli learn: %v", err)
	}
}

func TestCodexReportCompleteLine(t *testing.T) {
	for _, c := range []struct {
		report   string
		complete bool
		found    bool
	}{
		{"Did the work.\ncomplete: yes", true, true},
		{"Did half.\nCOMPLETE: NO", false, true},
		{"Finished.\n\ncomplete: no\n", false, true},
		{"No marker here.", true, false},
		{"complete: maybe", true, false},
	} {
		complete, found := codexReportComplete(c.report)
		if complete != c.complete || found != c.found {
			t.Errorf("%q: %v %v, want %v %v", c.report, complete, found, c.complete, c.found)
		}
	}
}

package daemon

import (
	"context"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestMergerStartStopProposal(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "remote"}[remote], func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			caller := startClaudeNode(t, g, shortSet)
			target := caller
			if remote {
				target = startClaudeNode(t, g, shortSet)
			}
			repo := newRepo(t, "garden")
			target.register(t, repo)
			t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", target.sessions)
			if _, err := g.gw.Store().WriteSettings([]protocol.SettingsChange{{Key: "delivery.tests_before_merge", Level: "repo", Scope: "garden", Value: json.RawMessage(`"optional"`)}}, "owner", "test disabled tests"); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			args, _ := json.Marshal(map[string]any{"action": "merger_start", "project": "garden", "node_id": target.d.NodeID()})
			if _, err := caller.d.runCTOToolRaw(ctx, "session_change", args); err != nil {
				t.Fatal(err)
			}
			items, err := caller.d.proposalRunner().list()
			if err != nil || len(items) != 1 {
				t.Fatalf("proposals=%v err=%v", items, err)
			}
			if !strings.Contains(items[0].Reason, "garden merger: Claude Opus session in garden on ") || !strings.Contains(items[0].Reason, "default pipeline") {
				t.Fatalf("approval=%s", items[0].Reason)
			}
			if _, err := os.Stat(filepath.Join(repo, "fake-claude.json")); !os.IsNotExist(err) {
				t.Fatal("started before approval")
			}
			if err := caller.d.proposalRunner().decide(ctx, items[0].ID, true); err != nil {
				t.Fatal(err)
			}
			items, err = caller.d.proposalRunner().list()
			if err != nil {
				t.Fatal(err)
			}
			var started protocol.StartedSession
			if err := json.Unmarshal(items[0].Result, &started); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				raw, _ := os.ReadFile(filepath.Join(repo, "fake-claude.json"))
				var rec fakeClaudeRecord
				if json.Unmarshal(raw, &rec) == nil && rec.Pgid > 1 {
					syscall.Kill(-rec.Pgid, syscall.SIGKILL)
				}
			})
			eventually(t, "merger registration", func() bool {
				var err error
				started, err = caller.d.sessionStartGet(ctx, started.ID)
				if err != nil {
					t.Fatal(err)
				}
				if started.Status == "failed" {
					t.Fatalf("start=%+v", started)
				}
				return started.Status == "started"
			})
			a, ok, err := g.gw.Store().Agent(started.SessionID)
			if err != nil || !ok || a.Type != "merger" || a.Profile.Name != "garden merger" || a.NodeID != target.d.NodeID() {
				t.Fatalf("merger=%+v ok=%v err=%v", a, ok, err)
			}
			rec := readClaude(t, repo)
			if argValue(rec.Args, "--name") != "garden merger" || argValue(rec.Args, "--model") != "claude-opus-5-5" || rec.Args[len(rec.Args)-1] != MergerFirstMessage {
				t.Fatalf("args=%v", rec.Args)
			}
			if started.EnterPlan != nil {
				t.Fatalf("merger opened a view: %+v", started.EnterPlan)
			}
			raw, err := target.d.call(ctx, "merger.plan", map[string]string{"project": "garden"})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), `"needs_default":true`) {
				t.Fatalf("pipeline still off: %s", raw)
			}
			args, _ = json.Marshal(map[string]any{"action": "merger_stop", "project": "garden", "node_id": target.d.NodeID()})
			if _, err := caller.d.runCTOToolRaw(ctx, "session_change", args); err != nil {
				t.Fatal(err)
			}
			items, err = caller.d.proposalRunner().list()
			if err != nil {
				t.Fatal(err)
			}
			if err := caller.d.proposalRunner().decide(ctx, items[len(items)-1].ID, true); err != nil {
				t.Fatal(err)
			}
			a, _, err = g.gw.Store().Agent(started.SessionID)
			if err != nil || a.Type != "agent" {
				t.Fatalf("stop=%+v err=%v", a, err)
			}
		})
	}
}

package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// agy names a conversation itself. A start opens agy in the project folder;
// the first SessionStart hook from that pane claims the conversation, and the
// session is then discovered like any agy session started by the owner.
func TestSessionStartAgyOpensInProjectAndFindsSession(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "agy")
	n := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.HarnessPaths = bins
		c.AgySummaries = fakeAgySummaries(t)
	})
	repo := newRepo(t, "shop")
	n.register(t, repo)

	var r sessionStartTestResult
	mustJSON(t, n.cli.mustCall("session_start", map[string]any{"project": "shop", "harness": "agy", "first_message": "Inspect this project"}), &r)
	if r.Harness != "agy" {
		t.Fatalf("start harness: %+v", r)
	}
	requireHerdrOpened(t, log, "shop", bins["agy"])

	// The pane carries the start ID, so the hook of the agy process it runs
	// can name the start that owns the conversation.
	rawLog, _ := os.ReadFile(log)
	envFile := regexp.MustCompile(regexp.QuoteMeta(os.TempDir()) + `/agent-net-herdr-env-[0-9]+`).FindString(string(rawLog))
	if envFile == "" {
		t.Fatalf("no pane environment in %s", rawLog)
	}
	envData, err := os.ReadFile(envFile)
	if err != nil || !strings.Contains(string(envData), "export "+AgyStartEnv+"="+r.ID) {
		t.Fatalf("pane environment does not carry the start: %q err %v", envData, err)
	}

	// The pane runs agy. In this test that process is a fake whose environment
	// carries the start it belongs to, as the pane's environment does.
	cmd := fakeInteractiveAgy(t, repo, []string{AgyStartEnv + "=" + r.ID})
	a := n.d.adapters["agy"].(*agyAdapter)
	hook := func(event string, idle bool) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{
			"conversationId": "conv-start", "event": event, "workspacePaths": []string{repo},
			"fullyIdle": idle, "startRef": r.ID,
		})
		out, err := n.d.antigravityHook(raw, cmd.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	hook("SessionStart", false)
	eventually(t, "the started agy session is discovered", func() bool {
		a.refresh()
		return a.has("conv-start", cmd.Process.Pid)
	})
	eventually(t, "the agy start finishes", func() bool {
		mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		if r.Status == "failed" {
			t.Fatalf("start: %+v", r)
		}
		return r.Status == "started"
	})
	if r.NativeID != "conv-start" || r.SessionID == "" || r.Path != repo {
		t.Fatalf("start result: %+v", r)
	}
	if agent := n.agentOf(t, r.SessionID); agent == nil || agent.Harness != "agy" {
		t.Fatalf("discovered session: %+v", agent)
	}

	// The first message goes through the same Stop hook path as any message
	// to an agy session, never typed as the owner.
	eventually(t, "the first message reaches the agy session", func() bool {
		out := hook("Stop", true)
		return strings.Contains(fmt.Sprint(out["reason"]), "Inspect this project")
	})
}

func TestSessionResumeAgyRunsConversation(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "agy")
	n := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.HarnessPaths = bins
		c.AgySummaries = fakeAgySummaries(t)
	})
	repo := newRepo(t, "shop")
	n.register(t, repo)
	native := "11111111-2222-4333-8444-555555555555"
	id, err := g.gw.Store().UpsertSession(n.d.cfg.NodeID, protocol.SessionInfo{Harness: "agy", NativeID: native, Cwd: repo, Name: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetSessionOffline(n.d.cfg.NodeID, id); err != nil {
		t.Fatal(err)
	}

	var r sessionStartTestResult
	mustJSON(t, n.cli.mustCall("session_resume", map[string]string{"session_id": id}), &r)
	if r.NativeID != native || r.Path != repo {
		t.Fatalf("resume: %+v", r)
	}
	requireHerdrOpened(t, log, "shop", bins["agy"], "--conversation", native)

	cmd := fakeInteractiveAgy(t, repo, nil)
	a := n.d.adapters["agy"].(*agyAdapter)
	raw, _ := json.Marshal(map[string]any{"conversationId": native, "event": "SessionStart", "workspacePaths": []string{repo}})
	if _, err := n.d.antigravityHook(raw, cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the resumed agy session is discovered", func() bool {
		a.refresh()
		return a.has(native, cmd.Process.Pid)
	})
	eventually(t, "the agy resume finishes", func() bool {
		mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		if r.Status == "failed" {
			t.Fatalf("resume: %+v", r)
		}
		return r.Status == "started"
	})
	if r.NativeID != native {
		t.Fatalf("resume changed the conversation: %+v", r)
	}
}

// The refusal for a harness this version does not know still lists what it
// can start, and Antigravity is now one of them.
func TestSessionStartUnknownHarnessListsAntigravity(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, nil)
	_, err := n.cli.call("session_start", map[string]any{"project": "shop", "harness": "gemini"})
	if err == nil {
		t.Fatal("an unknown harness was accepted")
	}
	if !strings.Contains(err.Error(), "Antigravity") {
		t.Fatalf("refusal does not list Antigravity: %v", err)
	}
}

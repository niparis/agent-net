package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/daemon/ctoskill"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// startCodexCTONode is a node whose CTO can run on the fake Codex app server
// and the fake OpenCode server.
func startCodexCTONode(t *testing.T) (*node, *logBuf) {
	t.Helper()
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	t.Setenv(envFakeCodex, "1")
	logs := &logBuf{}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, logger: log.New(logs, "", 0), cfg: func(c *Config) {
		c.HarnessPaths = map[string]string{"codex": os.Args[0], "opencode": os.Args[0]}
		c.testNoDeliveryConfirm = false
		c.deliveryConfirmWait = scaled(300 * time.Millisecond)
		c.testHandoverWait = time.Millisecond
		// The model catalog answer the fake app server cannot give on stdio.
		c.ListModels = func(_ context.Context, name string) ([]string, error) {
			switch name {
			case "codex":
				return []string{"gpt-fake"}, nil
			case "opencode":
				return []string{"fake/m1"}, nil
			}
			return nil, nil
		}
	}})
	return n, logs
}

// codexCTODir is where the CTO's private Codex app server runs, and where the
// fake records what it was asked.
func codexCTODir(n *node) string { return filepath.Join(n.d.cfg.StateDir, "cto", "codex") }

func setCTOHarness(t *testing.T, cli *localClient, harness, model string) {
	t.Helper()
	cli.mustCall(protocol.OpCTOSettings, protocol.CTOSettingsRequest{Harness: &harness, Model: &model})
}

func ctoSendID(t *testing.T, cli *localClient, text string) string {
	t.Helper()
	var out map[string]string
	if err := json.Unmarshal(cli.mustCall(protocol.OpCTOSend, map[string]string{"text": text}), &out); err != nil {
		t.Fatal(err)
	}
	return out["session_id"]
}

// The CTO on Codex runs in its own app server, with the cto-mcp role server,
// full access and no approval prompts. Its tools are allowed from that
// server's process tree, its messages are confirmed by the user message item,
// and its timeline reads the thread.
func TestCTOCodexHarnessRunsOnPrivateAppServer(t *testing.T) {
	n, logs := startCodexCTONode(t)
	cli := cliClient(t, n)
	setCTOHarness(t, cli, "codex", "gpt-fake:high")
	thread := ctoSendID(t, cli, "hello codex")
	if thread == "" {
		t.Fatal("no thread")
	}
	dir := codexCTODir(n)
	rec := readCodex(t, dir, func(r fakeCodexRecord) bool { return len(r.Turns) == 1 && r.Thread != nil })
	args := strings.Join(rec.Args, " ")
	for _, want := range []string{"app-server", "--ws-auth capability-token", `mcp_servers.agent-net-cto.args=["cto-mcp"]`, `mcp_servers.agent-net-cto.default_tools_approval_mode="approve"`} {
		if !strings.Contains(args, want) {
			t.Errorf("app server args %q lack %q", args, want)
		}
	}
	th := rec.Thread
	if th["approvalPolicy"] != "never" || th["sandbox"] != "danger-full-access" || th["cwd"] != dir || th["model"] != "gpt-fake" {
		t.Fatalf("thread/start %v", th)
	}
	if cfg, _ := th["config"].(map[string]any); cfg["model_reasoning_effort"] != "high" {
		t.Fatalf("effort: %v", th["config"])
	}
	if dev, _ := th["developerInstructions"].(string); !strings.HasPrefix(dev, "You are the CTO") {
		t.Fatalf("developer instructions: %q", dev)
	}
	turn := rec.TurnParams[0]
	if turn["clientUserMessageId"] == "" || !strings.Contains(rec.Turns[0], "hello codex") {
		t.Fatalf("queued turn %v", turn)
	}
	// The CTO's own skill comes from the cto node's folder, as on the other
	// harnesses: Codex takes it as an extra skill root.
	rec = readCodex(t, dir, func(r fakeCodexRecord) bool { return len(r.SkillRoots) == 1 })
	if _, err := os.Stat(filepath.Join(rec.SkillRoots[0], ctoskill.Name, "SKILL.md")); err != nil {
		t.Fatalf("the CTO skill is not in the Codex skill root: %v", err)
	}
	if !n.d.isCTOPeer(rec.ChildPid) {
		t.Fatal("a child of the CTO's app server is not a CTO peer")
	}
	eventually(t, "the delivery is confirmed", func() bool { return strings.Contains(logs.String(), "cto.delivery_confirmed") })
	eventually(t, "the timeline shows the turn", func() bool {
		var ev protocol.CTOEvents
		if json.Unmarshal(cli.mustCall(protocol.OpCTOEvents, map[string]int{"index": 0}), &ev) != nil {
			return false
		}
		var texts []string
		for _, raw := range ev.Messages {
			var m struct{ Type, Text string }
			json.Unmarshal(raw, &m)
			texts = append(texts, m.Type+":"+m.Text)
		}
		joined := strings.Join(texts, "|")
		return strings.Contains(joined, "user:hello codex") && strings.Contains(joined, "assistant:codex done")
	})
	if again := ctoSendID(t, cli, "second"); again != thread {
		t.Fatalf("second send opened thread %s, want %s", again, thread)
	}
	cli.mustCall(protocol.OpCTONew, nil)
	if fresh := ctoSendID(t, cli, "fresh"); fresh == thread || fresh == "" {
		t.Fatalf("cto_new kept thread %s", fresh)
	}
}

// Enter on a Codex CTO opens its thread on the private app server, with the
// token in the environment and not on the command line.
func TestCTOCodexEnterPlan(t *testing.T) {
	n, _ := startCodexCTONode(t)
	cli := cliClient(t, n)
	setCTOHarness(t, cli, "codex", "gpt-fake")
	var plan protocol.EnterPlan
	if err := json.Unmarshal(cli.mustCall(protocol.OpCTOEnter, nil), &plan); err != nil {
		t.Fatal(err)
	}
	thread, err := n.d.ctoConversation("codex")
	if err != nil || thread == "" {
		t.Fatalf("thread %q %v", thread, err)
	}
	if len(plan.Argv) < 6 || plan.Argv[1] != "resume" || plan.Argv[2] != thread || plan.Argv[3] != "--remote" ||
		!strings.HasPrefix(plan.Argv[4], "ws://127.0.0.1:") || !slices.Contains(plan.Argv, "--remote-auth-token-env") {
		t.Fatalf("plan %v", plan.Argv)
	}
	if plan.Env["AGENT_NET_CODEX_TOKEN"] == "" || strings.Contains(strings.Join(plan.Argv, " "), plan.Env["AGENT_NET_CODEX_TOKEN"]) {
		t.Fatalf("token: %v %v", plan.Env, plan.Argv)
	}
	if plan.Dir != codexCTODir(n) {
		t.Fatalf("dir %q", plan.Dir)
	}
}

// A switch to Codex and back keeps the phase 1 behaviour: a handover note
// each way, one conversation per harness that resumes within a day, and the
// start block again after a Codex compaction.
func TestCTOCodexSwitchEachWay(t *testing.T) {
	withFakeOpenCodeRouteSets(t, func(t *testing.T) {
		n, _ := startCodexCTONode(t)
		cli := cliClient(t, n)
		setCTOHarness(t, cli, "opencode", "fake/m1")
		openID := ctoSendID(t, cli, "open one")

		setCTOHarness(t, cli, "codex", "gpt-fake")
		waitHandoverSettled(t, n.d)
		first := n.d.latestHandoverNote()
		if first == "" {
			t.Fatal("no handover note for the switch to codex")
		}
		thread := ctoSendID(t, cli, "codex one FAKE:compact")
		dir := codexCTODir(n)
		readCodex(t, dir, func(r fakeCodexRecord) bool { return len(r.Turns) == 1 })
		if !strings.Contains(readCodex(t, dir, func(fakeCodexRecord) bool { return true }).Thread["developerInstructions"].(string), first) {
			t.Fatal("the new Codex thread is not pointed at the handover note")
		}
		eventually(t, "the compaction is seen", func() bool {
			n.d.startDueMu.Lock()
			defer n.d.startDueMu.Unlock()
			return n.d.startDue[n.d.ctoSessionID()]
		})
		ctoSendID(t, cli, "codex two")
		rec := readCodex(t, dir, func(r fakeCodexRecord) bool { return len(r.Turns) == 2 })
		if !strings.Contains(rec.Turns[1], "Latest handover note: "+first) || !strings.HasSuffix(rec.Turns[1], "codex two") {
			t.Fatalf("the turn after the compaction lacks the start block: %q", rec.Turns[1])
		}

		setCTOHarness(t, cli, "opencode", "fake/m1")
		waitHandoverSettled(t, n.d)
		if second := n.d.latestHandoverNote(); second == "" || second == first {
			t.Fatalf("no new handover note for the switch back: %q", second)
		}
		if n.d.isCTOPeer(rec.ChildPid) {
			t.Fatal("the Codex app server keeps CTO access after the switch")
		}
		if again := ctoSendID(t, cli, "open two"); again != openID {
			t.Fatalf("OpenCode conversation not resumed: %s, want %s", again, openID)
		}
		setCTOHarness(t, cli, "codex", "gpt-fake")
		if again := ctoSendID(t, cli, "codex three"); again != thread {
			t.Fatalf("Codex thread not resumed: %s, want %s", again, thread)
		}
		if rec := readCodex(t, dir, func(r fakeCodexRecord) bool { return r.Resumed > 0 }); rec.ResumeParams["threadId"] != thread ||
			rec.ResumeParams["approvalPolicy"] != "never" || rec.ResumeParams["sandbox"] != "danger-full-access" {
			t.Fatalf("resume %v", rec.ResumeParams)
		}
	})
}

// A switch to Codex with no model named takes the Codex harness's own default,
// not the model or provider of the harness it left, and records that effective
// entry first in cto.models (decision 0162).
func TestCTOSwitchToCodexWithoutModelUsesTheCodexDefault(t *testing.T) {
	n, _ := startCodexCTONode(t)
	cli := cliClient(t, n)
	setCTOHarness(t, cli, "opencode", "fake/m1")
	codex := "codex"
	empty := ""
	var out protocol.CTOSettings
	if err := json.Unmarshal(cli.mustCall(protocol.OpCTOSettings, protocol.CTOSettingsRequest{Harness: &codex, Model: &empty}), &out); err != nil {
		t.Fatal(err)
	}
	if out.Harness != "codex" || out.Model != "gpt-fake" || out.CodexModel != "gpt-fake" {
		t.Fatalf("switch settings: %+v", out)
	}
	if len(out.Models) == 0 || out.Models[0].Harness != "codex" || out.Models[0].Provider != "openai" || out.Models[0].Model != "gpt-fake" {
		t.Fatalf("switch list: %+v", out.Models)
	}
	if got := n.d.SettingsSnapshot().String("cto.models", settings.Where{}); !strings.Contains(got, "codex · openai · gpt-fake") {
		t.Fatalf("cto.models setting: %q", got)
	}
}

// A harness switch writes the entry the CTO runs to the cto.models setting, so
// the owner's Settings names the CTO that runs now (decision 0162). The check
// reads the setting back from the daemon, the way Settings does.
func TestCTOSwitchWritesTheRunningEntryToTheSetting(t *testing.T) {
	n, _ := startCodexCTONode(t)
	cli := cliClient(t, n)
	setCTOHarness(t, cli, "opencode", "fake/m1")
	setCTOHarness(t, cli, "codex", "gpt-fake")
	if got := n.d.SettingsSnapshot().String("cto.models", settings.Where{}); !strings.Contains(got, "codex · openai · gpt-fake") {
		t.Fatalf("cto.models after the switch: %q", got)
	}
}

// Clearing the model of the harness the CTO already runs leaves it empty. Only
// a harness switch takes the harness's default.
func TestCTOClearingTheModelKeepsItEmptyOnTheSameHarness(t *testing.T) {
	n, _ := startCodexCTONode(t)
	cli := cliClient(t, n)
	setCTOHarness(t, cli, "codex", "gpt-fake")
	empty := ""
	var out protocol.CTOSettings
	if err := json.Unmarshal(cli.mustCall(protocol.OpCTOSettings, protocol.CTOSettingsRequest{Model: &empty}), &out); err != nil {
		t.Fatal(err)
	}
	if out.Harness != "codex" || out.Model != "" {
		t.Fatalf("settings after clearing the model: %+v", out)
	}
	if len(out.Models) == 0 || out.Models[0].Model != "" {
		t.Fatalf("model list after clearing the model: %+v", out.Models)
	}
}

// When the model catalog gives no Codex default, the model the app server
// reports on thread/start fills the record and the owner's screen, so neither
// shows an empty model.
func TestCTOCodexReportsItsModelWhenNoCatalogAnswer(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	t.Setenv(envFakeCodex, "1")
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) {
		c.HarnessPaths = map[string]string{"codex": os.Args[0], "opencode": os.Args[0]}
		c.testHandoverWait = time.Millisecond
		c.ListModels = func(_ context.Context, name string) ([]string, error) {
			if name == "codex" {
				return nil, errors.New("no model catalog")
			}
			return []string{"fake/m1"}, nil
		}
	}})
	cli := cliClient(t, n)
	setCTOHarness(t, cli, "opencode", "fake/m1")
	codex, empty := "codex", ""
	if _, err := cli.call(protocol.OpCTOSettings, protocol.CTOSettingsRequest{Harness: &codex, Model: &empty}); err != nil {
		t.Fatal(err)
	}
	ctoSendID(t, cli, "hello codex")
	var out protocol.CTOSettings
	if err := json.Unmarshal(cli.mustCall(protocol.OpCTOSettings, nil), &out); err != nil {
		t.Fatal(err)
	}
	if out.Model != "fake-model" || out.CodexModel != "fake-model" {
		t.Fatalf("settings after the Codex start: %+v", out)
	}
	if len(out.Models) == 0 || out.Models[0].Harness != "codex" || out.Models[0].Model != "fake-model" {
		t.Fatalf("model list after the Codex start: %+v", out.Models)
	}
	if got := n.d.SettingsSnapshot().String("cto.models", settings.Where{}); !strings.Contains(got, "fake-model") {
		t.Fatalf("cto.models setting after the Codex start: %q", got)
	}
	var events protocol.CTOEvents
	if err := json.Unmarshal(cli.mustCall(protocol.OpCTOEvents, map[string]int{"index": 0}), &events); err != nil {
		t.Fatal(err)
	}
	if events.Model != "fake-model" {
		t.Fatalf("timeline model = %q, want fake-model", events.Model)
	}
}

// The Codex CTO answers a switch handover like the other harnesses. Its words
// land in the handover note under "Model output", and the request is marked as
// agent-net's own prompt, not as the owner speaking.
func TestCTOCodexModelLayerHandover(t *testing.T) {
	withFakeOpenCodeRouteSets(t, func(t *testing.T) {
		n, _ := startCodexCTONode(t)
		n.d.cfg.testHandoverWait = 10 * time.Second
		cli := cliClient(t, n)
		setCTOHarness(t, cli, "opencode", "fake/m1")
		ctoSendID(t, cli, "open one")
		setCTOHarness(t, cli, "codex", "gpt-fake")
		waitHandoverSettled(t, n.d)
		ctoSendID(t, cli, "codex one")
		switchCTOHarness(t, cli, "claude")
		waitHandoverSettled(t, n.d)
		body := handoverNotes(t, n.d)[n.d.latestHandoverNote()]
		if !strings.Contains(body, "## Model output (not the owner's words)") {
			t.Fatalf("the Codex layer is missing:\n%s", body)
		}
		rec := readCodex(t, codexCTODir(n), func(r fakeCodexRecord) bool {
			return slices.ContainsFunc(r.Turns, func(t string) bool { return strings.Contains(t, ctoHandoverPrompt) })
		})
		var asked string
		for _, turn := range rec.Turns {
			if strings.Contains(turn, ctoHandoverPrompt) {
				asked = turn
			}
		}
		if !strings.Contains(asked, "agent-net: harness switch handover") {
			t.Fatalf("the request is not labelled as agent-net's prompt: %q", asked)
		}
		if strings.Contains(asked, "This is the owner speaking") {
			t.Fatalf("the request claims to be the owner: %q", asked)
		}
	})
}

// codexCTOWithOneMessage runs a Codex CTO that answered one message and then
// went idle: its app server stopped, so its stored thread is no longer loaded.
func codexCTOWithOneMessage(t *testing.T, n *node, cli *localClient) {
	t.Helper()
	setCTOHarness(t, cli, "opencode", "fake/m1")
	ctoSendID(t, cli, "open one")
	setCTOHarness(t, cli, "codex", "gpt-fake")
	waitHandoverSettled(t, n.d)
	ctoSendID(t, cli, "codex one")
	// The fake writes the answer when its turn ends. The server stops only after
	// that, so the thread has a turn on disk when it is read again.
	eventually(t, "codex turn recorded", func() bool {
		raw, err := os.ReadFile(fakeCodexThreadsFile(codexCTODir(n)))
		return err == nil && strings.Contains(string(raw), "codex one")
	})
	n.d.cto.mu.Lock()
	n.d.stopCodexCTOLocked("idle")
	n.d.cto.mu.Unlock()
}

// The real case: a Codex CTO that did work and then idled still writes its model
// layer when the owner switches away, because its stored thread has turns and
// was used within the last day (decision 0171).
func TestCTOCodexIdleWithHistoryAnswersTheHandover(t *testing.T) {
	withFakeOpenCodeRouteSets(t, func(t *testing.T) {
		n, _ := startCodexCTONode(t)
		n.d.cfg.testHandoverWait = 10 * time.Second
		cli := cliClient(t, n)
		codexCTOWithOneMessage(t, n, cli)
		switchCTOHarness(t, cli, "claude")
		waitHandoverSettled(t, n.d)
		body := handoverNotes(t, n.d)[n.d.latestHandoverNote()]
		if !strings.Contains(body, "## Model output (not the owner's words)") {
			t.Fatalf("an idle Codex CTO with history wrote no model layer:\n%s", body)
		}
	})
}

// A Codex thread with no turns has nothing to hand over, so its note keeps the
// mechanical layer alone.
func TestCTOCodexThreadWithoutTurnsHasNoModelLayer(t *testing.T) {
	withFakeOpenCodeRouteSets(t, func(t *testing.T) {
		n, _ := startCodexCTONode(t)
		n.d.cfg.testHandoverWait = 10 * time.Second
		cli := cliClient(t, n)
		setCTOHarness(t, cli, "opencode", "fake/m1")
		ctoSendID(t, cli, "open one")
		setCTOHarness(t, cli, "codex", "gpt-fake")
		waitHandoverSettled(t, n.d)
		// Loading the thread stores it with no message in it, as Enter does.
		if _, _, _, err := n.d.codexCTOConn(context.Background(), "", true); err != nil {
			t.Fatal(err)
		}
		n.d.cto.mu.Lock()
		n.d.stopCodexCTOLocked("idle")
		n.d.cto.mu.Unlock()
		switchCTOHarness(t, cli, "claude")
		waitHandoverSettled(t, n.d)
		body := handoverNotes(t, n.d)[n.d.latestHandoverNote()]
		if strings.Contains(body, "## Model output") {
			t.Fatalf("a Codex thread with no turns wrote a model layer:\n%s", body)
		}
	})
}

// A Codex conversation used more than a day ago is not asked for a model layer,
// even with turns in it.
func TestCTOCodexConversationOlderThanADayHasNoModelLayer(t *testing.T) {
	withFakeOpenCodeRouteSets(t, func(t *testing.T) {
		n, _ := startCodexCTONode(t)
		n.d.cfg.testHandoverWait = 10 * time.Second
		cli := cliClient(t, n)
		codexCTOWithOneMessage(t, n, cli)
		old := n.d.ctoNowTime().Add(-ctoConversationFresh - time.Hour).UnixNano()
		if _, err := n.d.st.db.Exec(`UPDATE session_conversations SET updated_at=? WHERE harness='codex'`, old); err != nil {
			t.Fatal(err)
		}
		switchCTOHarness(t, cli, "claude")
		waitHandoverSettled(t, n.d)
		body := handoverNotes(t, n.d)[n.d.latestHandoverNote()]
		if strings.Contains(body, "## Model output") {
			t.Fatalf("a Codex conversation older than a day wrote a model layer:\n%s", body)
		}
	})
}

// Enter on the CTO's session row opens the Codex plan on this machine, not a
// Claude view, and keeps the app server's token on this machine for a client
// that comes through the gateway.
func TestCTOCodexSessionEnterKeepsTokenLocal(t *testing.T) {
	n, _ := startCodexCTONode(t)
	cli := cliClient(t, n)
	setCTOHarness(t, cli, "codex", "gpt-fake")
	// The first start registers the CTO's network session.
	cli.mustCall(protocol.OpCTOEnter, nil)
	var local protocol.EnterPlan
	if err := json.Unmarshal(cli.mustCall("session_enter", protocol.EnterRequest{SessionID: protocol.CTONodeName}), &local); err != nil {
		t.Fatal(err)
	}
	if local.Env["AGENT_NET_CODEX_TOKEN"] == "" || local.Env["DISABLE_AUTOUPDATER"] != "" || !slices.Contains(local.Argv, "--remote-auth-token-env") {
		t.Fatalf("local plan %+v", local)
	}
	handled, remote, err := n.d.ctoEnterQuery(t.Context(), protocol.EnterRequest{SessionID: n.d.ctoSessionID()}, true)
	if !handled || err != nil || len(remote.Argv) != 0 || len(remote.Env) != 0 || !strings.Contains(remote.Note, "only be entered on its own machine") {
		t.Fatalf("remote plan %v %+v %v", handled, remote, err)
	}
}

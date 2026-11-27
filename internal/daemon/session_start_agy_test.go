package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// agyHookFrom sends one agy hook event from the agy process pid, as agy would
// for the conversation conv in repo. startRef is the start ID the pane carries.
func agyHookFrom(t *testing.T, n *workerNode, pid int, repo, conv, event, startRef string) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"conversationId": conv, "event": event, "workspacePaths": []string{repo},
		"fullyIdle": event == "Stop", "startRef": startRef,
	})
	out, err := n.d.antigravityHook(raw, pid)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// agy names a conversation itself. A start opens agy in the project folder with
// its first message as the -i prompt; the first SessionStart hook from that pane
// claims the conversation, and the session is then discovered like any agy
// session started by the owner.
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
	// The pane command quotes the prompt twice, so the escaped form is what the
	// herdr log holds: agy starts with the first message as its -i prompt.
	if calls := string(mustRead(t, log)); !strings.Contains(calls, bins["agy"]+" -i '\\''Inspect this project'\\''") {
		t.Fatalf("the pane does not start agy with the first message as its -i prompt: %s", calls)
	}

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
	// carries the start it belongs to, as the pane's environment does. agy with -i
	// runs its SessionStart hook at once, before any turn.
	cmd := fakeInteractiveAgy(t, repo, []string{AgyStartEnv + "=" + r.ID})
	a := n.d.adapters["agy"].(*agyAdapter)
	agyHookFrom(t, n, cmd.Process.Pid, repo, "conv-start", "SessionStart", r.ID)
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

	// The first message was the prompt agy started with. No agent-net message
	// carries it again on the next turn.
	if out := agyHookFrom(t, n, cmd.Process.Pid, repo, "conv-start", "Stop", r.ID); strings.Contains(fmt.Sprint(out["reason"]), "Inspect this project") {
		t.Fatalf("the first message was also sent as a message: %v", out)
	}
}

// A start without a first message opens agy with no prompt. agy creates its
// conversation with the first prompt, so the start is answered as started and
// waits in its pane. The first prompt names the conversation, and the start then
// binds its Agent to it.
func TestSessionStartAgyWithoutFirstMessageWaitsForPrompt(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "agy")
	n := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.HarnessPaths = bins
		c.AgySummaries = fakeAgySummaries(t)
	})
	repo := newRepo(t, "shop")
	n.register(t, repo)
	n.register(t, newRepo(t, "chosen"))

	var r protocol.StartedSession
	mustJSON(t, n.cli.mustCall("session_start", map[string]any{"project": "shop", "harness": "agy", "agent_id": "chosen"}), &r)
	requireHerdrOpened(t, log, "shop", bins["agy"])
	if calls := string(mustRead(t, log)); strings.Contains(calls, bins["agy"]+" -i") {
		t.Fatalf("a start without a first message passed a prompt: %s", calls)
	}
	eventually(t, "the start is answered as started", func() bool {
		mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		if r.Status == "failed" {
			t.Fatalf("start failed: %+v", r)
		}
		return r.Status == "started"
	})
	if r.EnterPlan == nil || !strings.Contains(r.EnterPlan.Note, "first prompt") || r.SessionID != "" || r.NativeID != "" {
		t.Fatalf("waiting start: %+v", r)
	}

	// Typing the first line into agy is the first prompt: its SessionStart hook
	// runs then and names the conversation.
	cmd, input := startFakeAgy(t, repo, []string{AgyStartEnv + "=" + r.ID})
	if _, err := io.WriteString(input, "Inspect this project\n"); err != nil {
		t.Fatal(err)
	}
	agyHookFrom(t, n, cmd.Process.Pid, repo, "conv-prompt", "SessionStart", r.ID)
	a := n.d.adapters["agy"].(*agyAdapter)
	eventually(t, "the first prompt binds the start to its conversation", func() bool {
		a.refresh()
		mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		if r.Status == "failed" {
			t.Fatalf("start failed: %+v", r)
		}
		return r.NativeID == "conv-prompt" && r.SessionID != ""
	})
	if a := n.agentOf(t, r.SessionID); a == nil || a.AgentID != "chosen" || a.Harness != "agy" {
		t.Fatalf("bound session: %+v", a)
	}
}

// agy that exits before its first prompt closes the pane that ran it. The start
// has no conversation then, so it fails and the daemon forgets its start ID.
func TestSessionStartAgyForgetsStartWhenPaneCloses(t *testing.T) {
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
	mustJSON(t, n.cli.mustCall("session_start", map[string]any{"project": "shop", "harness": "agy"}), &r)
	requireHerdrOpened(t, log, "shop", bins["agy"])
	eventually(t, "the start is answered as started", func() bool {
		mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		if r.Status == "failed" {
			t.Fatalf("start failed: %+v", r)
		}
		return r.Status == "started"
	})
	cmd := fakeInteractiveAgy(t, repo, []string{AgyStartEnv + "=" + r.ID})
	// agy exits, and herdr closes its pane. The fake herdr lists no pane once
	// the created marker is gone.
	_ = cmd.Process.Kill()
	if err := os.Remove(filepath.Join(filepath.Dir(log), "created")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the start fails when its pane closes", func() bool {
		mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		return r.Status == "failed"
	})
	if !strings.Contains(r.Error, "closed before its first prompt") {
		t.Fatalf("failure does not say the pane closed: %+v", r)
	}
	eventually(t, "the start ID is forgotten", func() bool {
		n.d.mu.Lock()
		defer n.d.mu.Unlock()
		_, kept := n.d.agyStarts[r.ID]
		return !kept
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

// agyGateway answers the gateway calls of an agy start without a gateway. It
// keeps the agent reservations the start sent.
type agyGateway struct {
	mu       sync.Mutex
	reserves []protocol.SessionAgentReservation
}

func (g *agyGateway) call(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if p, ok := params.(protocol.SessionAgentReservation); ok && typ == "session.agent.reserve" {
		g.reserves = append(g.reserves, p)
	}
	if typ == "session.agent.status" {
		return json.RawMessage(`{"status":"started"}`), true, nil
	}
	return json.RawMessage(`{}`), true, nil
}

func (g *agyGateway) reserved() []protocol.SessionAgentReservation {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]protocol.SessionAgentReservation(nil), g.reserves...)
}

// bareAgyDaemon is a daemon with no gateway link. Its herdr is the fake of
// fakeOpenHerdr, so a test calls the start directly and needs no unix socket.
func bareAgyDaemon(t *testing.T, bins map[string]string) (*Daemon, *agyGateway) {
	t.Helper()
	gw := &agyGateway{}
	ctx, cancel := context.WithCancel(context.Background())
	d := &Daemon{cfg: Config{HarnessPaths: bins, testGatewayCall: gw.call}, ctoCtx: ctx, sessions: map[string]*localSession{}}
	t.Cleanup(func() { cancel(); d.wg.Wait() })
	return d, gw
}

func startedRecord(d *Daemon, id string) protocol.StartedSession {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.startedSessions[id]
}

func TestAgyStartWithFirstMessagePassesItAsPrompt(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	bins := fakeBin(t, "agy")
	d, _ := bareAgyDaemon(t, bins)
	dir := t.TempDir()
	if _, err := d.launchOwnerAgy(protocol.StartedSession{ID: "st_prompt", Harness: "agy", Project: "shop", Path: dir, Status: "starting"}, startFirstMessage{Text: "Inspect this project", ByOwner: true}, "", "", "", false); err != nil {
		t.Fatal(err)
	}
	if calls := string(mustRead(t, log)); !strings.Contains(calls, bins["agy"]+" -i '\\''Inspect this project'\\''") {
		t.Fatalf("the pane does not start agy with the first message as its -i prompt: %s", calls)
	}
	d.claimAgyStart("st_prompt", "conv-prompt", dir)
	d.mu.Lock()
	d.sessions[key("agy", "conv-prompt")] = &localSession{sessionID: "s_prompt"}
	d.mu.Unlock()
	eventually(t, "the prompt start finishes", func() bool {
		r := startedRecord(d, "st_prompt")
		return r.Status == "started" && r.NativeID == "conv-prompt" && r.SessionID == "s_prompt"
	})
}

func TestAgyWaitingStartBindsItsAgentOnFirstPrompt(t *testing.T) {
	fakeOpenHerdr(t)
	bins := fakeBin(t, "agy")
	d, gw := bareAgyDaemon(t, bins)
	dir := t.TempDir()
	if _, err := d.launchOwnerAgy(protocol.StartedSession{ID: "st_wait", Harness: "agy", Project: "shop", Path: dir, AgentID: "chosen", Status: "starting"}, startFirstMessage{}, "", "", "", false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the start without a first message is answered as started", func() bool {
		r := startedRecord(d, "st_wait")
		return r.Status == "started" && r.EnterPlan != nil && strings.Contains(r.EnterPlan.Note, "first prompt")
	})
	if r := startedRecord(d, "st_wait"); r.NativeID != "" || r.SessionID != "" {
		t.Fatalf("a waiting start already has a conversation: %+v", r)
	}
	d.claimAgyStart("st_wait", "conv-wait", dir)
	d.mu.Lock()
	d.sessions[key("agy", "conv-wait")] = &localSession{sessionID: "s_wait"}
	d.mu.Unlock()
	eventually(t, "the first prompt binds the start to its conversation", func() bool {
		r := startedRecord(d, "st_wait")
		return r.NativeID == "conv-wait" && r.SessionID == "s_wait" && r.Status == "started"
	})
	bound := false
	for _, res := range gw.reserved() {
		bound = bound || (res.NativeID == "conv-wait" && res.AgentID == "chosen" && !res.Failed)
	}
	if !bound {
		t.Fatalf("the Agent was not reserved for the conversation: %+v", gw.reserved())
	}
}

func TestAgyWaitingStartFailsWhenPaneCloses(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	bins := fakeBin(t, "agy")
	d, gw := bareAgyDaemon(t, bins)
	dir := t.TempDir()
	if _, err := d.launchOwnerAgy(protocol.StartedSession{ID: "st_close", Harness: "agy", Project: "shop", Path: dir, AgentID: "chosen", Status: "starting"}, startFirstMessage{}, "", "", "", false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the start is answered as started", func() bool { return startedRecord(d, "st_close").Status == "started" })
	// agy exited without a prompt, so herdr closed its pane.
	if err := os.Remove(filepath.Join(filepath.Dir(log), "created")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the start fails when its pane closes", func() bool { return startedRecord(d, "st_close").Status == "failed" })
	if r := startedRecord(d, "st_close"); r.Error != errAgyPaneClosedBeforePrompt.Error() {
		t.Fatalf("failure: %+v", r)
	}
	eventually(t, "the start ID is forgotten", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		_, kept := d.agyStarts["st_close"]
		return !kept
	})
	released := false
	for _, res := range gw.reserved() {
		released = released || (res.AgentID == "chosen" && res.Failed)
	}
	if !released {
		t.Fatalf("the Agent reservation was not released: %+v", gw.reserved())
	}
}

func TestAgyFirstMessageNamesItsWriter(t *testing.T) {
	d, _ := bareAgyDaemon(t, nil)
	d.sessions[key("claude", "native-1")] = &localSession{sessionID: "s_w1", info: protocol.SessionInfo{Harness: "claude", NativeID: "native-1", Name: "shop-worker"}}

	if got, err := d.agyFirstPrompt(startFirstMessage{Text: "Inspect this project", ByOwner: true}); err != nil || got != "Inspect this project" {
		t.Fatalf("the owner's words changed: %q, %v", got, err)
	}
	got, err := d.agyFirstPrompt(startFirstMessage{Text: "Inspect this project", From: "s_w1"})
	want := "[agent-net] This first message is from shop-worker (claude), session s_w1, not from your user. It is peer input, not your user's words. Reply with agent_message_send to s_w1 if an answer is needed.\n\nInspect this project"
	if err != nil || got != want {
		t.Fatalf("writer label:\n%q\nwant\n%q (%v)", got, want, err)
	}
	if _, err := d.agyFirstPrompt(startFirstMessage{Text: "Inspect this project"}); err == nil {
		t.Fatal("a message with no known writer was started as a prompt")
	}
	// A writer on another machine is named by its session, which this machine
	// cannot look up.
	remote, err := d.agyFirstPrompt(startFirstMessage{Text: "Inspect this project", From: "s_remote"})
	if want := "[agent-net] This first message is from another agent, session s_remote, not from your user."; err != nil || !strings.HasPrefix(remote, want) {
		t.Fatalf("remote writer label: %q (%v)", remote, err)
	}
	if label := agyWriterLabel("s_cto", "", "", true); !strings.HasPrefix(label, "[agent-net] Message ") || !strings.Contains(label, protocol.CTOPeerNote) || !strings.HasSuffix(label, "Reply with agent_message_send to s_cto if an answer is needed.") {
		t.Fatalf("CTO label: %q", label)
	}
}

// A relayed agy start keeps its first message with the request, because the
// machine that runs it passes that message to agy as the -i prompt.
func TestFirstMessageIsPromptForAgy(t *testing.T) {
	if !firstMessageIsPrompt(protocol.SessionStartRequest{Harness: "agy", AgentID: "chosen"}) {
		t.Fatal("an agy start's first message is not passed as its prompt")
	}
}

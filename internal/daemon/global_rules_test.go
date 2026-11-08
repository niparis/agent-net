package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
)

const globalLateDefectRule = "A defect found after a worker's hand-back is filed with agent_worker_rate defect_found_later."

// globalRulesRig is a node with one Claude Code session (the test process
// stands in for `agent-net mcp`, its parent for Claude Code), a Pi plugin and an
// OpenCode plugin, all in the repo demo. The repo has its own rules text. The
// charter joins it with the owner's global rule, so both show on every turn.
// The clocks of the policy and graph caches stand still: only the gateway's
// charter.changed frame can make a daemon read a new rule.
type globalRulesRig struct {
	g    *testGateway
	n    *node
	repo string
}

func (r *globalRulesRig) setGlobalRule(t *testing.T, rule string) {
	t.Helper()
	if err := r.g.gw.Store().SetLevelCharter(protocol.CharterSetRequest{Level: "global", Attr: "rules", Value: rule}, "owner", false); err != nil {
		t.Fatal(err)
	}
}

// changeGlobalRule sets the global rule and waits until the daemon has heard of
// it. The wait is only for the test's sake: the frame is on its way when the
// store write returns, and a turn that came earlier would see the old rule, as
// it would if it came a moment before the write.
func (r *globalRulesRig) changeGlobalRule(t *testing.T, rule string) {
	t.Helper()
	before := r.n.d.charterEpoch.Load()
	r.setGlobalRule(t, rule)
	eventually(t, "the daemon heard of the charter change", func() bool { return r.n.d.charterEpoch.Load() != before })
}

func newGlobalRulesRig(t *testing.T) *globalRulesRig {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	sessDir := t.TempDir()
	file := filepath.Join(sessDir, strconv.Itoa(os.Getppid())+".json")
	if err := os.WriteFile(file, []byte(`{"pid":1,"sessionId":"claude-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fa := &fakeAdapter{harness: "claude"}
	n := startNode(t, g, nodeOpts{adapter: fa, claudeDir: sessDir, cmdline: "/usr/bin/pi", exe: "/opt/x/node_modules/@opencode/cli/bin/opencode.exe"})
	repo := newRepo(t, "demo")
	fa.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "claude-1", Cwd: repo, Status: "idle"})
	eventually(t, "Claude session registered", func() bool { return fa.host.SessionID("claude", "claude-1") != "" })
	r := &globalRulesRig{g: g, n: n, repo: repo}
	frozen := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	n.d.policy.now = func() time.Time { return frozen }
	n.d.graph.now = func() time.Time { return frozen }
	store := g.gw.Store()
	if err := store.SetCharter("demo", "maturity", "early", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCharter("demo", "rules", "repo house rule: plain English", "owner"); err != nil {
		t.Fatal(err)
	}
	return r
}

func wantAll(t *testing.T, who, text string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(text, w) {
			t.Errorf("%s lacks %q:\n%s", who, w, text)
		}
	}
}

// (a) and (c): the per-turn answer the Claude Code hook prints, and the
// answer the Pi and OpenCode plugins put into the system prompt, carry the
// owner's global rule next to the repo's own rules, and follow a change on the
// next turn. The cache clocks stand still, so only the gateway's charter.changed
// frame can bring the new rule.
func TestGlobalRuleReachesTheClaudeHookAndThePluginsEveryTurn(t *testing.T) {
	r := newGlobalRulesRig(t)
	r.setGlobalRule(t, globalLateDefectRule)
	cli := cliClient(t, r.n)
	pi, _ := helloPi(t, r.n, "pi-1", r.repo, true)
	oc, _ := helloOpenCode(t, r.n, "ses_1", r.repo)

	turn := func() map[string]string {
		return map[string]string{
			"claude hook": askPolicy(t, cli, protocol.PolicyRequest{Harness: "claude", NativeID: "claude-1", Prompt: "fix it"}).Charter,
			"pi plugin":   askPolicy(t, pi, protocol.PolicyRequest{}).Charter,
			"opencode":    askPolicy(t, oc, protocol.PolicyRequest{}).Charter,
		}
	}
	for who, text := range turn() {
		wantAll(t, who, text, "rules: ", "repo house rule: plain English", globalLateDefectRule)
		if strings.Contains(text, "higher charter levels") {
			t.Errorf("%s has a block apart from the charter:\n%s", who, text)
		}
	}

	r.changeGlobalRule(t, "Plain words only.")
	for who, text := range turn() {
		wantAll(t, who, text, "Plain words only.", "repo house rule: plain English")
		if strings.Contains(text, "agent_worker_rate") {
			t.Errorf("%s still has the old rule:\n%s", who, text)
		}
	}
}

// (b): a new session's MCP server asks the daemon at start and puts the
// charter, with the global rule, into its instructions. The fixed text stays
// whole and first.
func TestMCPInstructionsOfANewSessionCarryTheGlobalRule(t *testing.T) {
	r := newGlobalRulesRig(t)
	r.setGlobalRule(t, globalLateDefectRule)
	md := mcpserver.NewDaemon(r.n.sock, map[string]any{"client": "mcp", "harness": "claude", "native_id": "claude-1",
		"cwd": r.repo, "status": "idle", "protocol": protocol.LocalProtocol})
	t.Cleanup(md.Close)
	ctx, cancel := context.WithTimeout(context.Background(), scaled(20*time.Second))
	defer cancel()

	instructions := mcpserver.StartInstructions(ctx, md.Call)
	st, ct := mcp.NewInMemoryTransports()
	server, err := mcpserver.NewWithInstructions(md.Call, "", instructions).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	client, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	got := client.InitializeResult().Instructions
	if !strings.HasPrefix(got, mcpserver.Instructions) {
		t.Errorf("the fixed instructions are not first:\n%s", got)
	}
	wantAll(t, "instructions", got, "repo house rule: plain English", globalLateDefectRule)
	if n := len([]rune(got)); n > protocol.MaxInstructionsRunes {
		t.Errorf("%d runes, cap %d", n, protocol.MaxInstructionsRunes)
	}
}

// A connection with no session yet (the Codex MCP process before a thread
// calls it) gets the global level only. Asking does not use up the one-time
// charter draft request, which belongs to a turn.
func TestCharterTextWithoutASessionIsTheGlobalLevelOnly(t *testing.T) {
	r := newGlobalRulesRig(t)
	if got := r.n.d.charterTextFor(context.Background(), nil); got != "" {
		t.Fatalf("no global level set, got %q", got)
	}
	r.setGlobalRule(t, globalLateDefectRule)
	got := r.n.d.charterTextFor(context.Background(), nil)
	wantAll(t, "global text", got, "rules: "+globalLateDefectRule)
	if strings.Contains(got, "repo house rule") {
		t.Errorf("repo text in the global-only answer:\n%s", got)
	}

	// The method is for sessions' tools; the command line has no use for it.
	cli := cliClient(t, r.n)
	if _, err := cli.call(protocol.MethodCharterText, struct{}{}); err == nil {
		t.Error("the command line asked for charter text")
	}
	pi, _ := helloPi(t, r.n, "pi-2", r.repo, true)
	var res protocol.CharterTextResult
	if err := json.Unmarshal(pi.mustCall(protocol.MethodCharterText, struct{}{}), &res); err != nil {
		t.Fatal(err)
	}
	wantAll(t, "bound answer", res.Text, "repo house rule: plain English", globalLateDefectRule)
}

// Codex has no per-turn hook, so the MCP instructions at start are the way a
// global rule reaches a Codex session. The real `agent-net mcp --codex` code,
// run as a child of the fake app server, reads it from the daemon.
func TestCodexMCPShimInstructionsCarryTheGlobalRule(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	n := codexNode(t, g, f)
	if err := g.gw.Store().SetLevelCharter(protocol.CharterSetRequest{Level: "global", Attr: "rules", Value: globalLateDefectRule}, "owner", false); err != nil {
		t.Fatal(err)
	}
	shim := startCodexShim(t, n)
	var init struct {
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(shim.initialized, &init); err != nil {
		t.Fatalf("initialize result %s: %v", shim.initialized, err)
	}
	if !strings.HasPrefix(init.Instructions, mcpserver.Instructions) {
		t.Errorf("the fixed instructions are not first:\n%s", init.Instructions)
	}
	wantAll(t, "codex instructions", init.Instructions, "rules: "+globalLateDefectRule)
}

// The Codex app server takes no text per turn, but a thread that agent-net
// starts or resumes for the owner gets the charter, with the global rules, as
// developer instructions.
func TestCodexOwnerThreadGetsTheCharterAsDeveloperInstructions(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	n := codexNode(t, g, f)
	// A session in the repo makes demo a node of the graph.
	helloPi(t, n, "pi-1", newRepo(t, "demo"), true)
	frozen := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	n.d.graph.now = func() time.Time { return frozen }
	store := g.gw.Store()
	if err := store.SetCharter("demo", "maturity", "early", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCharter("demo", "rules", "repo house rule: plain English", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLevelCharter(protocol.CharterSetRequest{Level: "global", Attr: "rules", Value: globalLateDefectRule}, "owner", false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), scaled(20*time.Second))
	defer cancel()
	r, err := n.d.prepareOwnerCodex(ctx, protocol.StartedSession{ID: "s1", Harness: "codex", Project: "demo", Path: "/work/demo"})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	started := f.threads[r.NativeID].DeveloperInstructions
	f.mu.Unlock()
	wantAll(t, "thread/start", started, "repo house rule: plain English", globalLateDefectRule)

	// Resuming it, as the owner does on every Enter, carries the current text.
	// The graph cache clock stands still: only charter.changed can refresh it.
	before := n.d.charterEpoch.Load()
	if err := store.SetLevelCharter(protocol.CharterSetRequest{Level: "global", Attr: "rules", Value: "Plain words only."}, "owner", false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the daemon heard of the charter change", func() bool { return n.d.charterEpoch.Load() != before })
	r2, err := n.d.prepareOwnerCodexThread(ctx, r, true)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	resumed := f.threads[r2.NativeID].DeveloperInstructions
	f.mu.Unlock()
	wantAll(t, "thread/resume", resumed, "Plain words only.", "repo house rule: plain English")
	if strings.Contains(resumed, "agent_worker_rate") {
		t.Errorf("thread/resume still has the old rule:\n%s", resumed)
	}
}

// A thread with no charter at all starts with no developer instructions.
func TestCodexOwnerThreadWithoutACharterGetsNoDeveloperInstructions(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	n := codexNode(t, g, f)
	ctx, cancel := context.WithTimeout(context.Background(), scaled(20*time.Second))
	defer cancel()
	r, err := n.d.prepareOwnerCodex(ctx, protocol.StartedSession{ID: "s2", Harness: "codex", Project: "nothing", Path: "/work/nothing"})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	got := f.threads[r.NativeID].DeveloperInstructions
	f.mu.Unlock()
	if got != "" {
		t.Fatalf("developer instructions without a charter: %q", got)
	}
}

// A worker brief is the start text of Codex and agy workers, which read no
// per-turn text. It carries the global rule next to the repo's own rules, and
// follows a change at once.
func TestWorkerBriefCarriesTheGlobalRule(t *testing.T) {
	r := newGlobalRulesRig(t)
	ctx := context.Background()
	if brief := r.n.d.charterBrief(ctx, "demo"); strings.Contains(brief, globalLateDefectRule) {
		t.Fatalf("no global rule is set yet:\n%s", brief)
	}
	r.changeGlobalRule(t, globalLateDefectRule)
	wantAll(t, "brief", r.n.d.charterBrief(ctx, "demo"), "rules: ", globalLateDefectRule, "repo house rule: plain English")
	// A project the graph does not know still gets the global level.
	if brief := r.n.d.charterBrief(ctx, "unknown-project"); !strings.Contains(brief, "rules: "+globalLateDefectRule) {
		t.Fatalf("project without a node:\n%s", brief)
	}
}

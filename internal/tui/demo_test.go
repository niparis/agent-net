package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// The README animation: nine frames of the real screen, in four beats. The
// data is invented but fixed, and it never names a real machine, host or path:
// two machines (laptop and build-box), six sessions and one change.
const demoWidth, demoHeight = 100, 28

func demoNodes() []protocol.NodeView {
	return []protocol.NodeView{
		{NodeID: "n_laptop", Hostname: "laptop", Version: "v0.4.0", Online: true},
		{NodeID: "n_build", Hostname: "build-box", Version: "v0.4.0", Online: true},
	}
}

func demoAgents() []protocol.AgentView {
	return []protocol.AgentView{
		agent("s_backend", "n_laptop", "laptop", "claude", "backend", func(a *protocol.AgentView) {
			a.Status, a.Mode, a.Model = "busy", protocol.ModeBackground, "claude-opus-5-5"
			a.Repo = protocol.RepoInfo{Project: "shop", Branch: "main"}
			a.Profile.Function = "the API"
		}),
		agent("s_frontend", "n_laptop", "laptop", "opencode", "frontend", func(a *protocol.AgentView) {
			a.Status, a.Mode, a.Model = "busy", protocol.ModeForeground, "opencode-go/deepseek-v4.1"
			a.Repo = protocol.RepoInfo{Project: "shop", Branch: "main"}
			a.Profile.Function = "the web app"
		}),
		agent("s_homelab", "n_laptop", "laptop", "claude", "homelab", func(a *protocol.AgentView) {
			a.Status, a.Mode, a.Model = "idle", protocol.ModeForeground, "claude-sonnet-5-5"
			a.Repo = protocol.RepoInfo{Project: "homelab", Branch: "main"}
			a.Profile.Function = "runs the estate"
		}),
		agent("s_forge", "n_laptop", "laptop", "codex", "forge", func(a *protocol.AgentView) {
			a.Status, a.Mode, a.Model = "busy", protocol.ModeBackground, "codex/gpt-6"
			a.Repo = protocol.RepoInfo{Project: "forge", Branch: "main"}
			a.Profile.Function = "the data pipeline"
		}),
		agent("s_worker_be", "n_build", "build-box", "pi", "worker", func(a *protocol.AgentView) {
			a.Status, a.Mode, a.Model = "idle", protocol.ModeBackground, "pi/qwen3-coder"
			a.Profile.Function = "worker"
			a.Worker = &protocol.WorkerInfo{Owner: "s_backend"}
		}),
		agent("s_worker_forge", "n_build", "build-box", "pi", "worker", func(a *protocol.AgentView) {
			a.Status, a.Mode, a.Model = "idle", protocol.ModeBackground, "pi/qwen3-coder"
			a.Profile.Function = "worker"
			a.Worker = &protocol.WorkerInfo{Owner: "s_forge"}
		}),
	}
}

func demoSnapshot() Snapshot {
	return Snapshot{Connected: true, NodeID: "n_laptop", Version: "v0.4.0", Nodes: demoNodes(), Agents: demoAgents()}
}

func demoUsage() *Usage {
	machine := func(cpu float64, cores int, used, total, disk uint64) protocol.MachineUsage {
		return protocol.MachineUsage{Cores: cores, CPU: cpu, CPUAvg: cpu - 2, MemUsed: used, MemUsedAvg: used - gib/10,
			MemTotal: total, DiskFree: disk, Load: [3]float64{cpu / 10, cpu / 12, cpu / 14}, Procs: 210, Samples: 60}
	}
	return &Usage{
		NodeID: "n_laptop", Hostname: "laptop",
		Local: &protocol.UsageReport{Machine: machine(14, 10, 5*gib, 16*gib, 180*gib)},
		Gateway: &protocol.UsageAnswer{Nodes: []protocol.UsageNode{{
			NodeID: "n_build", Hostname: "build-box", AgeSeconds: 4,
			Usage: protocol.UsageReport{Machine: machine(71, 4, 3*gib, 8*gib, 31*gib)},
		}}},
	}
}

func demoBase(t *testing.T, snap Snapshot) Model {
	t.Helper()
	src := &fakeSource{snap: snap}
	src.usage, src.counts = demoUsage(), &protocol.SessionCounts{Online: 6}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.cto, m.demo = nil, true
	m = send(m, tea.WindowSizeMsg{Width: demoWidth, Height: demoHeight})
	m = refresh(m, src)
	m.src = src
	return settle(m)
}

// demoMessages returns the Messages tab with one agent to agent exchange.
func demoMessages(m Model, reply bool) Model {
	m.tab, m.focus = messagesTab, focusFeed
	m.feed = newFeed()
	m.feed.items = []*feedItem{{
		EventID: 1, At: t0.Add(-2 * time.Minute), Kind: "msg.stored", MsgID: "m_1",
		From: "s_backend", To: "s_frontend", Type: protocol.TypeRequest, ConversationID: "c_1",
		State: protocol.StateDelivered, Snippet: "user_id is now account_id", Body: "user_id is now account_id", Snip: snipDone,
	}}
	if reply {
		m.feed.items = append(m.feed.items, &feedItem{
			EventID: 2, At: t0.Add(-time.Minute), Kind: "msg.stored", MsgID: "m_2",
			From: "s_frontend", To: "s_backend", Type: protocol.TypeReply, Parent: "m_1", ConversationID: "c_1",
			State: protocol.StateReplied, Snippet: "updated, commit 3f2a91c", Body: "updated, commit 3f2a91c", Snip: snipDone,
		})
	}
	m.feedCur = len(m.feed.items) - 1
	return m
}

// demoStepIndex is where a step sits in the full strip, which also has
// approve, test after merge, release, deploy and verify after merge.
func demoStepIndex(name string) int {
	for i, step := range deliverySteps {
		if step == name {
			return i
		}
	}
	return 0
}

// demoChange is the worker's change. passed is how many of accept, review,
// test and merge have turned green.
func demoChange(passed int) deliveryChange {
	names := []string{"accept", "review", "test", "merge"}
	steps := make([]deliveryStep, 0, len(names))
	marks := make([]stripState, len(deliverySteps))
	for i := range marks {
		marks[i] = stripWaiting
	}
	for i, name := range names {
		state := "ready"
		switch {
		case i < passed:
			state = "passed"
			marks[demoStepIndex(name)] = stripPassed
		case i == passed:
			state = "running"
		}
		steps = append(steps, deliveryStep{Name: name, State: state})
	}
	current := min(passed, len(names)-1)
	stepState := "ready"
	switch {
	case passed == 0:
		stepState = "running"
	case passed >= len(names):
		stepState = "passed"
	}
	return deliveryChange{
		ID: "c_1", Title: "Use account_id in the shop API", Repo: "shop", Head: "3f2a91c0d4e5",
		Branch: "agent-net/w_account", Revision: 1, State: "open", Caller: "s_worker_be",
		Step: names[current], StepOwner: "merger", OwnerKind: "node", StepState: stepState,
		Waiting: 2 * time.Minute, Steps: steps, Marks: marks,
	}
}

func demoDesk(t *testing.T, passed int, digest, ask bool) Model {
	t.Helper()
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: []deliveryChange{demoChange(passed)}}, demoWidth, demoHeight))
	m.snap = demoSnapshot()
	m.cto, m.demo = nil, true
	if digest {
		m.digest = &deskDigest{away: 3 * time.Hour, loaded: true,
			counts: deskCounts{closed: 22, sentOn: 4, answered: 2, forYou: 1}}
	}
	if ask {
		m.asks = []protocol.Ask{{
			ID: "a_1", Kind: protocol.AskKindStep, State: protocol.AskStateOpen,
			FromKind: protocol.AskFromSession, From: "forge",
			AboutKind: protocol.AskAboutChange, AboutID: "c_1", AboutTitle: "Merge PR #212?",
			Question: "CI green. Recommended: yes", Recommendation: "yes", CreatedAt: t0.Add(-time.Minute),
		}}
	}
	return m
}

// demoFrame is one rendered beat frame and how long it holds. The durations
// add up to the 12 seconds the README animation loops.
type demoFrame struct {
	name     string
	duration time.Duration
	want     []string
	view     string
}

func demoFrames(t *testing.T) []demoFrame {
	t.Helper()
	snap := demoSnapshot()
	sessions := demoBase(t, snap)
	messages := demoBase(t, snap)
	return []demoFrame{
		{name: "demo_00_sessions", duration: 3 * time.Second, view: sessions.testView(),
			want: []string{"0 needs you", "backend", "frontend", "homelab", "forge", "laptop", "build-box", "3 busy"}},
		{name: "demo_01_request", duration: 1500 * time.Millisecond, view: demoMessages(messages, false).testView(),
			want: []string{"backend", "frontend", "user_id is now account_id"}},
		{name: "demo_02_reply", duration: 1500 * time.Millisecond, view: demoMessages(messages, true).testView(),
			want: []string{"backend", "frontend", "updated, commit 3f2a91c"}},
		{name: "demo_03_accept", duration: 750 * time.Millisecond, view: demoDesk(t, 1, false, false).testView(),
			want: []string{"In flight · 1", "review", "●○○○"}},
		{name: "demo_04_review", duration: 750 * time.Millisecond, view: demoDesk(t, 2, false, false).testView(),
			want: []string{"test", "●●○○"}},
		{name: "demo_05_test", duration: 750 * time.Millisecond, view: demoDesk(t, 3, false, false).testView(),
			want: []string{"merge", "●●●○"}},
		{name: "demo_06_merge", duration: 750 * time.Millisecond, view: demoDesk(t, 4, false, false).testView(),
			want: []string{"merge", "●●●●"}},
		{name: "demo_07_digest", duration: 1500 * time.Millisecond, view: demoDesk(t, 4, true, false).testView(),
			want: []string{"While you were away (3 h)", "closed 22 · sent on 4", "Nothing is waiting for you."}},
		{name: "demo_08_decision", duration: 1500 * time.Millisecond, view: demoDesk(t, 4, true, true).testView(),
			want: []string{"closed 22 · sent on 4", "Merge PR #212?", "CI green. Recommended: yes"}},
	}
}

// TestDemoScenes is the golden screen harness for the README animation. It
// writes one .ansi per beat frame, and checks that every frame says what its
// beat is about. The beats are: sessions, a message exchange, a change that
// moves through its steps, and one decision that reaches the owner.
func TestDemoScenes(t *testing.T) {
	frames := demoFrames(t)
	if len(frames) != 9 {
		t.Fatalf("%d frames, want 9", len(frames))
	}
	var total time.Duration
	for _, f := range frames {
		total += f.duration
	}
	if total != 12*time.Second {
		t.Errorf("the frames hold %s, want 12s", total)
	}
	for _, f := range frames {
		plain := ansi.Strip(f.view)
		for _, want := range f.want {
			if !strings.Contains(plain, want) {
				t.Errorf("%s is missing %q:\n%s", f.name, want, plain)
			}
		}
		for _, line := range strings.Split(plain, "\n") {
			if ansi.StringWidth(line) > demoWidth {
				t.Errorf("%s has a line wider than %d: %q", f.name, demoWidth, line)
			}
		}
		writeScreen(t, f.name, f.view)
	}
	writeDemoTimeline(t, frames)
}

// writeDemoTimeline writes each frame's hold next to the .ansi files, so the
// SVG combiner reads the timing from the same place as the frames.
func writeDemoTimeline(t *testing.T, frames []demoFrame) {
	t.Helper()
	if os.Getenv("AGENT_NET_SCREEN_CHILD") == "" {
		return
	}
	var b strings.Builder
	for _, f := range frames {
		fmt.Fprintf(&b, "%s %d\n", f.name, f.duration.Milliseconds())
	}
	if err := os.WriteFile(filepath.Join(*screens, "demo_timeline.txt"), []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
}

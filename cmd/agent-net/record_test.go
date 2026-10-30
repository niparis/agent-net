package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Time{
		"2h":                   now.Add(-2 * time.Hour),
		"30m":                  now.Add(-30 * time.Minute),
		"2026-10-28T08:00:00Z": time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC),
		"2026-10-28 08:15":     time.Date(2026, 10, 28, 8, 15, 0, 0, time.UTC),
		"2026-10-28":           time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC),
	}
	for in, want := range cases {
		got, err := parseSince(in, now, time.UTC)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "yesterday", "-1h", "12"} {
		if _, err := parseSince(bad, now, time.UTC); err == nil {
			t.Errorf("parseSince(%q) accepted", bad)
		}
	}
}

func TestPrintAgentsShowsProject(t *testing.T) {
	agents := []protocol.AgentView{
		{SessionID: "s_1", Harness: "opencode", Hostname: "mac", NodeID: "n_1", Cwd: "/src/shop/web", Status: "idle", Online: true, PeerTrust: true,
			Mode: "foreground", Model: "anthropic/claude-sonnet-5",
			Profile: protocol.Profile{Name: "web", Function: "frontend"},
			Repo:    protocol.RepoInfo{Root: "/src/shop", Project: "shop", Remote: "https://example.com/shop.git", Branch: "main"}},
		{SessionID: "s_2", Harness: "claude", Hostname: "box", Status: "busy", Mode: "background",
			Profile: protocol.Profile{Name: "old", Project: "announced"}},
		{SessionID: "s_3", Harness: "pi", Hostname: "box", Status: "idle", Profile: protocol.Profile{Name: "none"}},
	}
	var b bytes.Buffer
	if err := printAgents(&b, agents, false); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("output:\n%s", b.String())
	}
	if f := strings.Fields(lines[0]); strings.Join(f, " ") != "SESSION NAME HARNESS HOST PROJECT STATUS MODE MODEL ONLINE TRUST FUNCTION" {
		t.Fatalf("header: %q", lines[0])
	}
	if f := strings.Fields(lines[1]); f[4] != "shop" {
		t.Errorf("repo project: %q", lines[1])
	}
	if f := strings.Fields(lines[2]); f[4] != "announced" {
		t.Errorf("announced project is the fallback: %q", lines[2])
	}
	if f := strings.Fields(lines[3]); f[4] != "-" {
		t.Errorf("no project shows a dash: %q", lines[3])
	}
	if f := strings.Fields(lines[1]); f[6] != "foreground" || f[7] != "anthropic/claude-sonnet-5" {
		t.Errorf("mode and model: %q", lines[1])
	}
	// A background session with no model, and an older daemon that reports neither.
	if f := strings.Fields(lines[2]); f[6] != "background" || f[7] != "-" {
		t.Errorf("no model shows a dash: %q", lines[2])
	}
	if f := strings.Fields(lines[3]); f[6] != "-" || f[7] != "-" {
		t.Errorf("an older daemon shows dashes: %q", lines[3])
	}
	if strings.Contains(b.String(), "example.com") {
		t.Error("the remote belongs to the wide view")
	}

	b.Reset()
	printAgents(&b, agents, true)
	wide := strings.Split(strings.TrimSpace(b.String()), "\n")
	if h := strings.Fields(wide[0]); h[5] != "BRANCH" || h[7] != "MODE" || h[8] != "MODEL" || h[10] != "TRUST" || h[11] != "FUNCTION" || h[12] != "EXPERTISE" || h[13] != "TASK" || h[14] != "REMOTE" || h[15] != "FOLDER" || h[16] != "NODE" || h[17] != "WORKTREE" {
		t.Fatalf("wide header: %q", wide[0])
	}
	f := strings.Fields(wide[1])
	if f[4] != "shop" || f[5] != "main" || f[len(f)-4] != "https://example.com/shop.git" || f[len(f)-3] != "/src/shop/web" || f[len(f)-2] != "n_1" || f[len(f)-1] != "-" {
		t.Errorf("wide row: %q", wide[1])
	}
	// The function is the project description from AGENTS.md; with none it is a dash.
	if f := strings.Fields(lines[1]); f[len(f)-1] != "frontend" {
		t.Errorf("function: %q", lines[1])
	}
	if f := strings.Fields(lines[3]); f[len(f)-1] != "-" {
		t.Errorf("no description shows a dash: %q", lines[3])
	}
	if f := strings.Fields(wide[1]); f[10] != "yes" || f[11] != "frontend" || f[12] != "-" || f[13] != "-" {
		t.Errorf("wide function and empty task: %q", wide[1])
	}
	agents[0].Profile.Task = "fixing-login"
	b.Reset()
	printAgents(&b, agents, true)
	if f := strings.Fields(strings.Split(strings.TrimSpace(b.String()), "\n")[1]); f[13] != "fixing-login" {
		t.Errorf("wide task: %q", b.String())
	}
}

func TestPrintProjects(t *testing.T) {
	var b bytes.Buffer
	printProjects(&b, []protocol.ProjectView{
		{Name: "shop", Hostname: "mac", Path: "/src/shop", Remote: "https://example.com/shop.git", Source: protocol.SourceBoth,
			Harnesses: []string{"claude", "opencode"}, Worktrees: []string{"/src/shop-wt"}, Online: 1, LastSeen: time.Date(2026, 10, 29, 14, 30, 0, 0, time.UTC).Unix()},
		{Name: "scratch", Hostname: "box", Path: "/tmp/scratch", Harnesses: []string{"pi"}, LastSeen: 1}, // an older gateway sends no source
		{Name: "new", Hostname: "box", Path: "/src/new", Source: protocol.SourceRegistered},
	}, time.UTC)
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if strings.Join(strings.Fields(lines[0]), " ") != "PROJECT MACHINE PATH REMOTE SOURCE HARNESSES WORKTREES ONLINE LAST SEEN" {
		t.Fatalf("header: %q", lines[0])
	}
	if got := strings.Join(strings.Fields(lines[1]), " "); got != "shop mac /src/shop https://example.com/shop.git both claude,opencode /src/shop-wt 1 2026-10-29 14:30" {
		t.Errorf("row 1: %q", got)
	}
	if got := strings.Join(strings.Fields(lines[2]), " "); !strings.HasPrefix(got, "scratch box /tmp/scratch - seen pi - 0 ") {
		t.Errorf("row 2: %q", got)
	}
	if got := strings.Join(strings.Fields(lines[3]), " "); got != "new box /src/new - registered - - 0 -" {
		t.Errorf("row 3: %q", got)
	}
}

func TestPrintAddOutcome(t *testing.T) {
	var b bytes.Buffer
	printAddOutcome(&b, protocol.ProjectAddOutcome{Machine: "mac", DryRun: true,
		Added:    []protocol.ProjectEntry{{Name: "a", Path: "/c/a", Remote: "https://example.com/a.git"}},
		Existing: []protocol.ProjectEntry{{Name: "b", Path: "/c/b"}},
		Skipped:  []protocol.ProjectSkip{{Path: "/c/n", Reason: "not a working tree"}}, Overflow: 3})
	out := b.String()
	for _, want := range []string{"would add  a     /c/a  https://example.com/a.git", "exists", "skipped", "not a working tree",
		"3 more repositories were found", "1 would be added on mac, 1 already registered, 1 skipped (dry run: nothing was changed)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestHarnessSummaryAndDetails(t *testing.T) {
	oc := protocol.HarnessInfo{Name: "opencode", Path: "/u/opencode", Version: "v2.2.0", Primary: true,
		Others: []protocol.HarnessInstall{{Path: "/old/opencode", Version: "1.18.16"}},
		Models: []string{"anthropic/claude-x", "openai/gpt-5", "openai/gpt-6"}}
	nodes := []protocol.NodeView{
		{Hostname: "mac", Harnesses: []protocol.HarnessInfo{{Name: "claude", Path: "/u/claude", Version: "2.1"}, oc}},
		{Hostname: "bare", Harnesses: []protocol.HarnessInfo{}},
		{Hostname: "old"},
	}
	want := []string{"claude,opencode", "none", "unknown"}
	for i, n := range nodes {
		if got := harnessSummary(n); got != want[i] {
			t.Errorf("harnessSummary(%s) = %q, want %q", n.Hostname, got, want[i])
		}
	}
	if got := modelSummary(oc.Models); got != "3 (anthropic, openai)" {
		t.Errorf("modelSummary = %q", got)
	}
	if modelSummary(nil) != "-" {
		t.Error("no models shows a dash")
	}
	var b bytes.Buffer
	printHarnessDetails(&b, nodes)
	out := b.String()
	for _, w := range []string{"HOST", "HARNESS", "VERSION", "MODELS", "PATH", "mac", "/u/opencode", "v2.2.0", "3 (anthropic, openai)",
		"primary", "opencode (other copy)", "1.18.16", "/old/opencode",
		"old", "not reported (older agent-net)"} {
		if !strings.Contains(out, w) {
			t.Errorf("details lack %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "bare") {
		t.Errorf("a node with no harnesses has no detail lines:\n%s", out)
	}
}

// captureStdout runs fn and returns what it wrote to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stdout = old
	w.Close()
	return <-done
}

// `agent-net events` against a real gateway: filters, JSON lines and follow mode.
func TestEventsCommand(t *testing.T) {
	data := t.TempDir()
	gw, err := gateway.New(gateway.Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()
	srv := httptest.NewUnstartedServer(gw.Handler())
	srv.TLS = gw.TLSConfig()
	srv.StartTLS()
	defer srv.Close()
	if err := gw.WriteAdminInfo(srv.Listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	st := gw.Store()
	st.Event("node.connected", "", "", "", "first-event")
	st.Event("msg.stored", "", "s_a", "m_1", "second-event")

	out := captureStdout(t, func() {
		if err := cmdEvents(context.Background(), []string{"--data", data}); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "TIME") || strings.Index(out, "first-event") > strings.Index(out, "second-event") || !strings.Contains(out, "m_1") {
		t.Fatalf("all events, oldest first:\n%s", out)
	}

	out = captureStdout(t, func() {
		cmdEvents(context.Background(), []string{"--data", data, "--kind", "msg", "--json"})
	})
	var ev protocol.Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &ev); err != nil || ev.Kind != "msg.stored" || ev.MessageID != "m_1" {
		t.Fatalf("json output %q: %+v %v", out, ev, err)
	}

	out = captureStdout(t, func() {
		cmdEvents(context.Background(), []string{"--data", data, "--session", "s_a", "--since", "1h"})
	})
	if strings.Contains(out, "first-event") || !strings.Contains(out, "second-event") {
		t.Fatalf("session filter:\n%s", out)
	}

	if err := cmdEvents(context.Background(), []string{"--data", t.TempDir()}); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("no gateway here: %v", err)
	}

	// Follow: a new event shows up while the command runs.
	ctx, cancel := context.WithCancel(context.Background())
	var followed string
	finished := make(chan struct{})
	go func() {
		followed = captureStdout(t, func() {
			cmdEvents(ctx, []string{"--data", data, "--follow", "--limit", "1"})
		})
		close(finished)
	}()
	time.Sleep(300 * time.Millisecond)
	st.Event("node.disconnected", "", "", "", "third-event")
	deadline := time.Now().Add(1600 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		select {
		case <-finished:
		default:
			continue
		}
		break
	}
	cancel()
	<-finished
	if !strings.Contains(followed, "second-event") || !strings.Contains(followed, "third-event") ||
		strings.Contains(followed, "first-event") || strings.Count(followed, "second-event") != 1 {
		t.Fatalf("follow output:\n%s", followed)
	}
}

func TestWithoutDormantHidesQuietSessionsByDefault(t *testing.T) {
	agents := []protocol.AgentView{
		{SessionID: "s_1", Status: "idle", Online: true},
		{SessionID: "s_2", Status: protocol.StatusDormant, Online: true},
		{SessionID: "s_3", Status: "busy", Online: true},
	}
	got := withoutDormant(agents, false)
	if len(got) != 2 || got[0].SessionID != "s_1" || got[1].SessionID != "s_3" {
		t.Fatalf("default list: %+v", got)
	}
	if got := withoutDormant(agents, true); len(got) != 3 {
		t.Fatalf("--dormant list: %+v", got)
	}
}

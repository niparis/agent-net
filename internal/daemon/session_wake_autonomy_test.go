package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// setWakeAutonomy gives the agent of the session's project the autonomy rule.
func setWakeAutonomy(t *testing.T, cli *localClient, project, value string) {
	t.Helper()
	cli.mustCall("charter_set", map[string]any{"node": project, "attr": "maturity", "value": "growing"})
	cli.mustCall("node_settings_set", map[string]any{"node": project, "setting": protocol.SettingAutonomy, "value": value})
}

// newInteractiveWakeRig is a wake rig whose session was an interactive Claude
// session (mode foreground) with the given autonomy, and whose machine lists
// the given background records.
func newInteractiveWakeRig(t *testing.T, autonomy string, records []map[string]any) wakeRig {
	t.Helper()
	return newClaudeWakeRig(t, protocol.ModeForeground, autonomy, records)
}

// newClaudeWakeRig is a wake rig for an offline Claude session of the given
// mode, with the given autonomy, and a machine that lists the given records.
func newClaudeWakeRig(t *testing.T, mode, autonomy string, records []map[string]any) wakeRig {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	listing, calls := filepath.Join(t.TempDir(), "agents.json"), filepath.Join(t.TempDir(), "calls")
	for _, r := range records {
		if _, ok := r["cwd"]; !ok {
			r["cwd"] = repo
		}
	}
	b, _ := json.Marshal(records)
	if err := os.WriteFile(listing, b, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CLAUDE_AGENTS_FILE", listing)
	t.Setenv("FAKE_CLAUDE_AGENTS_LOG", calls)
	id, err := g.gw.Store().UpsertSession(c.d.NodeID(), protocol.SessionInfo{Harness: "claude", NativeID: wakeNative, Cwd: repo, Name: "shop", Mode: mode,
		Repo: protocol.RepoInfo{Root: repo, Project: "shop"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetSessionOffline(c.d.NodeID(), id); err != nil {
		t.Fatal(err)
	}
	setWakeAutonomy(t, c.cli, "shop", autonomy)
	return wakeRig{c, g, id, repo, listing, calls}
}

// An interactive Claude session that is not running is resumed in the
// background with claude --bg --resume, in its folder, when autonomy is wake.
// The queued message is then delivered to the same agent-net session.
func TestMessageWakesInteractiveClaudeWithAutonomy(t *testing.T) {
	r := newInteractiveWakeRig(t, protocol.AutonomyWake, nil)
	r.send(t, "wake the interactive session")
	eventually(t, "interactive session receives message", func() bool {
		if lines := r.g.logs.lines("session wake", r.id); len(lines) > 0 {
			t.Fatalf("wake failed: %v", lines)
		}
		return len(r.c.adapter.deliveredTo(r.id)) == 1
	})
	rec := readClaude(t, r.repo)
	if argValue(rec.Args, "--resume") != wakeNative {
		t.Fatalf("resume args %v", rec.Args)
	}
	hasBg := false
	for _, a := range rec.Args {
		hasBg = hasBg || a == "--bg"
	}
	if !hasBg {
		t.Fatalf("not a background resume: %v", rec.Args)
	}
}

func TestMessageWakesInteractiveClaudeCopyKeepsTheSessionID(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_NEW_RESUME_ID", "1")
	r := newInteractiveWakeRig(t, protocol.AutonomyWake, []map[string]any{{"id": wakeNative[:8], "kind": "interactive", "state": "done"}})
	r.send(t, "wake the copy")
	eventually(t, "copy receives message under the original agent-net ID", func() bool { return len(r.c.adapter.deliveredTo(r.id)) == 1 })
	if readClaude(t, r.repo).Session == wakeNative {
		t.Fatal("fake did not copy the resumed session")
	}
}

// With autonomy off an interactive session stays asleep: no record is read and
// nothing starts.
func TestMessageDoesNotWakeInteractiveClaudeWithAutonomyOff(t *testing.T) {
	r := newInteractiveWakeRig(t, protocol.AutonomyOff, nil)
	r.send(t, "stay asleep")
	if err := r.c.d.wakeRetiredClaude(context.Background(), r.id, ""); err == nil {
		t.Fatal("interactive session woken without autonomy")
	}
	if n := r.listCalls(); n > 1 {
		t.Fatalf("listed %d times", n)
	}
	if _, err := os.Stat(filepath.Join(r.repo, "fake-claude.json")); !os.IsNotExist(err) {
		t.Fatalf("session started: %v", err)
	}
}

func TestInteractiveWakeRefusesRunningOrUnclearRecords(t *testing.T) {
	for name, records := range map[string][]map[string]any{
		"running":            {{"id": wakeNative[:8], "kind": "interactive", "pid": 4242}},
		"stopped-bg":         {{"id": wakeNative[:8], "kind": "background", "state": "stopped"}},
		"no-kind":            {{"id": wakeNative[:8], "state": "done"}},
		"other-sessionid":    {{"id": wakeNative[:8], "sessionId": "different-native", "kind": "interactive"}},
		"finished-elsewhere": {{"id": wakeNative[:8], "kind": "background", "state": "done", "cwd": "/elsewhere"}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newInteractiveWakeRig(t, protocol.AutonomyWake, records)
			if err := r.c.d.wakeSession(context.Background(), r.id, "", true); err == nil {
				t.Fatal("unsafe interactive wake accepted")
			}
			if _, err := os.Stat(filepath.Join(r.repo, "fake-claude.json")); !os.IsNotExist(err) {
				t.Fatalf("session started: %v", err)
			}
		})
	}
}

// offlineOwnerSession adds an offline owner session of a harness on the node's
// machine, in a real folder, in the project "shop" with the given autonomy.
func offlineOwnerSession(t *testing.T, g *testGateway, n *node, harness, native, autonomy string) (id, dir string) {
	t.Helper()
	dir = t.TempDir()
	id, err := g.gw.Store().UpsertSession(n.d.NodeID(), protocol.SessionInfo{Harness: harness, NativeID: native, Cwd: dir, Name: harness + "-owner",
		Repo: protocol.RepoInfo{Root: dir, Project: "shop"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetSessionOffline(n.d.NodeID(), id); err != nil {
		t.Fatal(err)
	}
	setWakeAutonomy(t, cliClient(t, n), "shop", autonomy)
	return id, dir
}

// With autonomy wake, a message to an offline Codex session resumes its saved
// thread on the app server in the session's folder, and the message is queued
// into that thread. The agent-net session ID does not change.
func TestMessageWakesCodexThreadWithAutonomy(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	n := codexNode(t, g, f)
	pi, _ := helloPi(t, n, "np", "/work/pi", true)
	id, dir := offlineOwnerSession(t, g, n, "codex", "t-old", protocol.AutonomyWake)

	m := sendResult(t, pi.mustCall("send", map[string]any{"to": id, "body": "wake up and review"}))
	eventually(t, "message queued into the resumed thread", func() bool { return len(f.queue()) == 1 })
	q := f.queue()[0]
	if q.Thread != "t-old" || q.ClientID != m.ID || !strings.Contains(q.Text, "wake up and review") {
		t.Fatalf("queue entry: %+v", q)
	}
	f.mu.Lock()
	th := f.threads["t-old"]
	f.mu.Unlock()
	if th == nil || th.Cwd != dir {
		t.Fatalf("resumed thread: %+v, want folder %s", th, dir)
	}
	if got := n.d.SessionID("codex", "t-old"); got != id {
		t.Fatalf("session ID after wake = %q, want %q", got, id)
	}
	eventually(t, "state delivered", func() bool { return messageState(t, pi, m.ID) == protocol.StateDelivered })
}

func TestMessageDoesNotWakeCodexWithAutonomyOff(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	n := codexNode(t, g, f)
	pi, _ := helloPi(t, n, "np", "/work/pi", true)
	id, _ := offlineOwnerSession(t, g, n, "codex", "t-old", protocol.AutonomyOff)
	m := sendResult(t, pi.mustCall("send", map[string]any{"to": id, "body": "stay asleep"}))
	time.Sleep(400 * time.Millisecond)
	f.mu.Lock()
	th := f.threads["t-old"]
	f.mu.Unlock()
	if th != nil || len(f.queue()) != 0 {
		t.Fatalf("thread resumed with autonomy off: %+v %+v", th, f.queue())
	}
	if st := messageState(t, pi, m.ID); st != protocol.StateStored {
		t.Fatalf("state = %s, want stored", st)
	}
}

// openCodeWakeRig is a machine with a fake owner OpenCode service. When the
// service is asked for the saved session and loads its project, the fake
// plugin registers the session, as the real plugin does.
func openCodeWakeRig(t *testing.T, autonomy string) (n *workerNode, pi *localClient, id string, requests func() []string, plugin func() *localClient) {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	g := startGateway(t, t.TempDir(), "")
	n = startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} })
	id, dir := offlineOwnerSession(t, g, n.node, "opencode", "ses_old", autonomy)
	var mu sync.Mutex
	var seen []string
	var lc *localClient
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "opencode" || pass != "test-owner-password" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/openapi.json" {
			http.NotFound(w, r)
			return
		}
		// The daemon's own inventory polls the service without a folder.
		if r.Header.Get("x-opencode-directory") == "" {
			fmt.Fprint(w, `{"data":[]}`)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.Header.Get("x-opencode-directory") != strings.ReplaceAll(url.QueryEscape(dir), "+", "%20") {
			t.Errorf("directory header %q", r.Header.Get("x-opencode-directory"))
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/session/ses_old":
			fmt.Fprint(w, `{"data":{"id":"ses_old"}}`)
		case "GET /api/location":
			if lc == nil {
				lc, _ = helloOpenCode(t, n.node, "ses_old", dir)
			}
			fmt.Fprint(w, `{"data":{}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	writeOwnerOpenCodeService(t, state, server.URL)
	pi, _ = helloPi(t, n.node, "np", "/work/pi", true)
	return n, pi, id, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), seen...) }, func() *localClient { mu.Lock(); defer mu.Unlock(); return lc }
}

// With autonomy wake, a message to an offline OpenCode session loads the saved
// session in the owner's service, in its folder. The plugin registers it under
// the same agent-net session ID and the queued message is delivered. No session
// is created.
func TestMessageWakesOpenCodeSessionWithAutonomy(t *testing.T) {
	n, pi, id, seen, plugin := openCodeWakeRig(t, protocol.AutonomyWake)
	_ = n
	m := sendResult(t, pi.mustCall("send", map[string]any{"to": id, "body": "wake the opencode session"}))
	eventually(t, "plugin session registered", func() bool { return plugin() != nil })
	select {
	case push := <-plugin().Pushes:
		if push.ID != m.ID || push.Body != "wake the opencode session" {
			t.Fatalf("delivery: %+v", push)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("message was not delivered to the woken session")
	}
	if got := n.d.SessionID("opencode", "ses_old"); got != id {
		t.Fatalf("session ID after wake = %q, want %q", got, id)
	}
	if len(seen()) != 2 || seen()[0] != "GET /api/session/ses_old" {
		t.Fatalf("service requests: %v", seen())
	}
}

func TestMessageDoesNotWakeOpenCodeWithAutonomyOff(t *testing.T) {
	_, pi, id, seen, plugin := openCodeWakeRig(t, protocol.AutonomyOff)
	m := sendResult(t, pi.mustCall("send", map[string]any{"to": id, "body": "stay asleep"}))
	time.Sleep(400 * time.Millisecond)
	if plugin() != nil || len(seen()) != 0 {
		t.Fatalf("OpenCode woken with autonomy off: %v", seen())
	}
	if st := messageState(t, pi, m.ID); st != protocol.StateStored {
		t.Fatalf("state = %s, want stored", st)
	}
}

// Pi cannot open an existing session, so even a wake frame with autonomy wake
// is refused, and the message stays queued with the sender's receipt line.
func TestMessageToOfflinePiStaysQueuedWithAutonomyWake(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	sender, _ := helloPi(t, n, "np", "/work/pi", true)
	id, _ := offlineOwnerSession(t, g, n, "pi", "pi-old", protocol.AutonomyWake)
	if err := n.d.wakeSession(context.Background(), id, "", true); err == nil || !strings.Contains(err.Error(), "cannot be woken") {
		t.Fatalf("pi wake: %v", err)
	}
	var out struct {
		Sent []struct {
			State string `json:"state"`
			Note  string `json:"note"`
		} `json:"sent"`
	}
	mustJSON(t, sender.mustCall("send", map[string]any{"to": id, "body": "hello", "receipt": true}), &out)
	if len(out.Sent) != 1 || out.Sent[0].State != protocol.StateStored || !strings.Contains(out.Sent[0].Note, "Pi sessions are not woken by messages") {
		t.Fatalf("receipt: %+v", out)
	}
}

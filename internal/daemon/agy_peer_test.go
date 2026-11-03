package daemon

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestAgyPeerKeepsStartWorkspace(t *testing.T) {
	d := &Daemon{}
	for _, raw := range []string{`{"conversationId":"one","workspacePaths":["/work/demo"],"event":"SessionStart"}`, `{"conversationId":"one","event":"PreInvocation"}`} {
		if err := d.recordAntigravity(json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	e := d.agyEvents["one"]
	if len(e.WorkspacePaths) != 1 || e.WorkspacePaths[0] != "/work/demo" {
		t.Fatalf("lost start workspace: %+v", e)
	}
}

func TestAgyPeerEnterArgv(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "agy")
	cwd := t.TempDir()
	n, _, id := enterNode(t, g, "agy", protocol.SessionInfo{NativeID: "test-conversation", PID: 1, Cwd: cwd}, bins, nil)
	p := enterOf(t, n, id, "")
	if len(p.Argv) != 3 || p.Argv[0] != bins["agy"] || p.Argv[1] != "--conversation" || p.Argv[2] != "test-conversation" || p.Dir != filepath.Clean(cwd) {
		t.Fatalf("enter: %+v", p)
	}
	_ = context.Background()
}

func TestAgyPeerDiscoveryAndHooks(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	summaryPath := fakeAgySummaries(t)
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.AgySummaries = summaryPath }})
	cwd := t.TempDir()
	cmd := fakeInteractiveAgy(t, cwd, nil)
	a := n.d.adapters["agy"].(*agyAdapter)
	db, err := sql.Open("sqlite", a.summaries)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO conversation_summaries(conversation_id,title,parent_conversation_id) VALUES ('main','A title',NULL),('child','Sub','main'),('worker','Worker',NULL)`); err != nil {
		t.Fatal(err)
	}
	n.d.wmu.Lock()
	n.d.workers["w_test"] = &worker{id: "w_test", harness: "agy", nativeID: "worker"}
	n.d.wmu.Unlock()
	hook := func(id, event string, idle bool) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"conversationId": id, "event": event, "workspacePaths": []string{cwd}, "fullyIdle": idle})
		if _, err := n.d.antigravityHook(raw, cmd.Process.Pid); err != nil {
			t.Fatal(err)
		}
	}
	hook("main", "SessionStart", false)
	hook("child", "SessionStart", false)
	hook("worker", "SessionStart", false)
	a.refresh()
	if !a.has("main", cmd.Process.Pid) || a.has("child", cmd.Process.Pid) || a.has("worker", cmd.Process.Pid) {
		t.Fatal("bad peer discovery")
	}
	n.d.mu.Lock()
	info := n.d.sessions[key("agy", "main")].info
	n.d.mu.Unlock()
	if info.Name != "A title" || info.Status != "unknown" {
		t.Fatalf("initial: %+v", info)
	}
	hook("main", "PreInvocation", false)
	a.refresh()
	n.d.mu.Lock()
	info = n.d.sessions[key("agy", "main")].info
	n.d.mu.Unlock()
	if info.Status != "busy" {
		t.Fatalf("busy: %+v", info)
	}
	hook("main", "Stop", true)
	a.refresh()
	n.d.mu.Lock()
	info = n.d.sessions[key("agy", "main")].info
	n.d.mu.Unlock()
	if info.Status != "idle" {
		t.Fatalf("idle: %+v", info)
	}
	cmd.Process.Kill()
	cmd.Wait()
	a.refresh()
	if a.has("main", cmd.Process.Pid) {
		t.Fatal("dead process still discovered")
	}
}

func TestAgyPeerMCPBinding(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cwd := t.TempDir()
	cmd := fakeInteractiveAgy(t, cwd, nil)
	a := n.d.adapters["agy"].(*agyAdapter)
	raw, _ := json.Marshal(map[string]any{"conversationId": "main", "event": "SessionStart", "workspacePaths": []string{cwd}})
	if _, err := n.d.antigravityHook(raw, cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	a.refresh()
	for _, tc := range []struct {
		id    string
		pid   int
		bound bool
	}{{"main", cmd.Process.Pid, true}, {"other", cmd.Process.Pid, false}, {"main", os.Getpid(), false}} {
		lc := &localConn{d: n.d}
		_, err := lc.helloInteractiveAgy(helloParams{NativeID: tc.id}, tc.pid)
		if err != nil {
			t.Fatal(err)
		}
		if (lc.ls != nil) != tc.bound {
			t.Fatalf("id=%s pid=%d bound=%v", tc.id, tc.pid, lc.ls != nil)
		}
	}
}

func TestAgyPeerDeliveryHooks(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cwd := t.TempDir()
	cmd := fakeInteractiveAgy(t, cwd, nil)
	a := n.d.adapters["agy"].(*agyAdapter)
	hook := func(event string, idle bool) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"conversationId": "main", "event": event, "workspacePaths": []string{cwd}, "fullyIdle": idle})
		out, err := n.d.antigravityHook(raw, cmd.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	hook("SessionStart", false)
	hook("PreInvocation", false)
	a.refresh()
	m := protocol.Message{ID: "m_1", Body: "Hello", From: "s_sender", FromName: "Sender", FromHarness: "codex"}
	r := a.Deliver(context.Background(), protocol.SessionInfo{NativeID: "main", PID: cmd.Process.Pid, Status: "busy"}, m)
	if !r.Queued || r.Delivered {
		t.Fatalf("queued: %+v", r)
	}
	out := hook("Stop", true)
	if out["decision"] != "continue" || !strings.Contains(fmt.Sprint(out["reason"]), "Hello") {
		t.Fatalf("stop: %#v", out)
	}
	r = a.Deliver(context.Background(), protocol.SessionInfo{NativeID: "main", PID: cmd.Process.Pid, Status: "unknown"}, protocol.Message{ID: "m_2", Body: "Next"})
	if !r.Queued || !strings.Contains(r.Detail, "next turn") {
		t.Fatalf("waiting: %+v", r)
	}
	out = hook("PreInvocation", false)
	b, _ := json.Marshal(out)
	if !strings.Contains(string(b), `"userMessage"`) || !strings.Contains(string(b), "Next") {
		t.Fatalf("inject: %s", b)
	}
}

func fakeInteractiveAgy(t *testing.T, cwd string, env []string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("bash", "-c", "exec -a agy /bin/cat")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), env...)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	eventually(t, "fake agy process", func() bool { args := osProcessArgs(cmd.Process.Pid); return len(args) > 0 && args[0] == "agy" })
	return cmd
}
func fakeAgySummaries(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "summaries.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE conversation_summaries (conversation_id TEXT, title TEXT, parent_conversation_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAgyPeerIdleHerdrDelivery(t *testing.T) {
	f := newFakeHerdr(t)
	script := `#!/bin/sh
case "$1 $2" in
 "pane list") cat "$FAKE_AGY_HERDR/panes" ;;
 "pane process-info") cat "$FAKE_AGY_HERDR/info-$4" ;;
 "pane send-text"|"pane send-keys") printf '%s\n' "$*" >> "$FAKE_AGY_HERDR/sent" ;;
 *) exit 1 ;;
esac
`
	if err := os.WriteFile(f.bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_AGY_HERDR", f.dir)
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.HarnessPaths = map[string]string{"herdr": f.bin} }})
	cwd := t.TempDir()
	cmd := fakeInteractiveAgy(t, cwd, []string{"HERDR_ENV=1", "HERDR_PANE_ID=w1:p1", "HERDR_SOCKET_PATH=" + f.sock, "HERDR_BIN_PATH=" + f.bin})
	f.panes(t, `{"pane_id":"w1:p1"}`)
	f.holds(t, "w1:p1", cmd.Process.Pid)
	a := n.d.adapters["agy"].(*agyAdapter)
	for _, ev := range []string{"SessionStart", "Stop"} {
		raw, _ := json.Marshal(map[string]any{"conversationId": "main", "event": ev, "workspacePaths": []string{cwd}, "fullyIdle": true})
		if _, err := n.d.antigravityHook(raw, cmd.Process.Pid); err != nil {
			t.Fatal(err)
		}
	}
	a.refresh()
	res := a.Deliver(context.Background(), protocol.SessionInfo{NativeID: "main", PID: cmd.Process.Pid}, protocol.Message{ID: "m_1", Body: "hello pane"})
	if !res.Delivered {
		t.Fatalf("delivery: %+v", res)
	}
	data, err := os.ReadFile(filepath.Join(f.dir, "sent"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "pane send-text w1:p1 hello pane") || !strings.Contains(string(data), "pane send-keys w1:p1 enter") {
		t.Fatalf("calls: %s", data)
	}
	res = a.Deliver(context.Background(), protocol.SessionInfo{NativeID: "main", PID: cmd.Process.Pid}, protocol.Message{ID: "m_control", Body: "hello\x1b[31m"})
	if !res.Queued {
		t.Fatalf("control characters must wait for a hook: %+v", res)
	}
}

func TestAgyPeerDeliveryStateEndToEnd(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cwd := t.TempDir()
	cmd := fakeInteractiveAgy(t, cwd, nil)
	a := n.d.adapters["agy"].(*agyAdapter)
	hook := func(ev string) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"conversationId": "main", "event": ev, "workspacePaths": []string{cwd}, "fullyIdle": true})
		out, err := n.d.antigravityHook(raw, cmd.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	hook("SessionStart")
	hook("PreInvocation")
	a.refresh()
	var id string
	eventually(t, "agy registered", func() bool { id = n.d.SessionID("agy", "main"); return id != "" })
	fa := &fakeAdapter{harness: "fake"}
	sender := startNode(t, g, nodeOpts{adapter: fa})
	fa.setSessions(fakeSession("sender", "sender"))
	eventually(t, "sender registered", func() bool { return sender.d.SessionID("fake", "sender") != "" })
	sent, _, err := g.gw.Store().StoreMessage(protocol.MsgSend{Sender: sender.d.SessionID("fake", "sender"), Recipient: id, Body: "queued body", IdempotencyKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	n.d.onDeliver(context.Background(), sent)
	got, _, err := g.gw.Store().Message(sent.ID)
	if err != nil || got.State != protocol.StateDispatched || got.DeliveryDetail != "waiting for the session's next turn" {
		t.Fatalf("waiting: %+v %v", got, err)
	}
	if n.d.st.isDelivered(sent.ID) {
		t.Fatal("queued message was marked delivered")
	}
	out := hook("Stop")
	if out["decision"] != "continue" || !strings.Contains(fmt.Sprint(out["reason"]), "queued body") {
		t.Fatalf("hook: %v", out)
	}
	eventually(t, "delivered receipt", func() bool { m, _, _ := g.gw.Store().Message(sent.ID); return m.State == protocol.StateDelivered })
}

func TestAgyPeerEnterHerdrCases(t *testing.T) {
	herdr, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "agy")
	bins["herdr"] = herdr
	cwd := t.TempDir()
	n, _, id := enterNode(t, g, "agy", protocol.SessionInfo{NativeID: "conversation-test", PID: 1, Cwd: cwd, Name: "agy-test"}, bins, nil)
	p := enterOfHerdr(t, n, id, fakeHerdrSocket(log))
	if !p.OpenedHerdr {
		t.Fatalf("local herdr: %+v", p)
	}
	requireHerdrOpened(t, log, "agy-test", bins["agy"], "--conversation", "conversation-test")
	local := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.HarnessPaths = map[string]string{"herdr": herdr} }})
	waitSessionOnGateway(t, local, id)
	p = enterOf(t, local, id, "")
	if !reflect.DeepEqual(p.Argv, []string{herdr, "--remote", n.cfg.Hostname}) {
		t.Fatalf("remote: %+v", p)
	}
}

func TestAgyPeerMCPDescendantAndSpoofedHook(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cwd := t.TempDir()
	cmd := exec.Command("bash", "-c", `exec -a agy bash -c 'cat <&3 & echo $!; wait' 3<&0`)
	cmd.Dir = cwd
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	eventually(t, "child has execed", func() bool { args := processArgs(child); return len(args) > 0 && filepath.Base(args[0]) == "cat" })
	raw, _ := json.Marshal(map[string]any{"conversationId": "main", "event": "SessionStart", "workspacePaths": []string{cwd}})
	if _, err = n.d.antigravityHook(raw, child); err != nil {
		t.Fatal(err)
	}
	a := n.d.adapters["agy"].(*agyAdapter)
	a.refresh()
	lc := &localConn{d: n.d}
	if _, err = lc.helloInteractiveAgy(helloParams{NativeID: "main"}, child); err != nil || lc.ls == nil {
		n.d.mu.Lock()
		e := n.d.agyEvents["main"]
		n.d.mu.Unlock()
		a.mu.Lock()
		peers := fmt.Sprint(a.peers)
		a.mu.Unlock()
		t.Fatalf("descendant not bound: %v, event=%+v parent=%v args=%v child=%d chain=%v peers=%s", err, e, cmd.Process.Pid, processArgs(cmd.Process.Pid), child, pidChain(child), peers)
	}
	a.Deliver(context.Background(), protocol.SessionInfo{NativeID: "main", PID: cmd.Process.Pid}, protocol.Message{ID: "m_1", Body: "private"})
	raw, _ = json.Marshal(map[string]any{"conversationId": "main", "event": "Stop", "fullyIdle": true})
	answer, err := n.d.antigravityHook(raw, os.Getpid())
	if err != nil || len(answer) != 0 {
		t.Fatalf("foreign hook: %v %v", answer, err)
	}
	answer, err = n.d.antigravityHook(raw, child)
	if err != nil || answer["decision"] != "continue" {
		t.Fatalf("real hook lost message: %v %v", answer, err)
	}
}

func TestAgyPeerHookKeepsOversizedQueueForNextInvocation(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cwd := t.TempDir()
	cmd := fakeInteractiveAgy(t, cwd, nil)
	a := n.d.adapters["agy"].(*agyAdapter)
	hook := func(ev string) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"conversationId": "main", "event": ev, "workspacePaths": []string{cwd}})
		out, err := n.d.antigravityHook(raw, cmd.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	hook("SessionStart")
	hook("PreInvocation")
	a.refresh()
	for i := 0; i < 20; i++ {
		res := a.Deliver(context.Background(), protocol.SessionInfo{NativeID: "main", PID: cmd.Process.Pid}, protocol.Message{ID: fmt.Sprintf("m_%d", i), Body: strings.Repeat("\x00", 60*1024)})
		if !res.Queued {
			t.Fatalf("queue %d: %+v", i, res)
		}
	}
	answer := hook("Stop")
	encoded, _ := json.Marshal(answer)
	if len(encoded) > 1<<20 {
		t.Fatalf("hook answer is %d bytes", len(encoded))
	}
	a.mu.Lock()
	remaining := len(a.pending["main"])
	a.mu.Unlock()
	if remaining == 0 {
		t.Fatal("all messages removed in one oversized answer")
	}
}

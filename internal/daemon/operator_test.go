package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/usage"
)

// ---- helpers ----

// startNodeCfg is startNode with a change to the daemon config.
func startNodeCfg(t *testing.T, g *testGateway, o nodeOpts, mutate func(*Config)) *node {
	t.Helper()
	cfgDir := t.TempDir()
	if _, err := Join(context.Background(), g.joinString(), cfgDir); err != nil {
		t.Fatal(err)
	}
	nc, ca, err := LoadNode(cfgDir)
	if err != nil {
		t.Fatal(err)
	}
	sock := shortDir(t) + "/d.sock"
	cfg := Config{GatewayURL: nc.GatewayURL, NodeID: nc.NodeID, Token: nc.Token, CAPEM: ca,
		Hostname: "host-" + nc.NodeID, StateDir: t.TempDir(), SocketPath: sock,
		ClaudeSessionsDir: o.claudeDir, BackoffMin: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond,
		PluginAckTimeout: 500 * time.Millisecond, Logger: o.logger}
	if o.adapter != nil {
		cfg.Adapters = []Adapter{o.adapter}
	}
	cfg.DetectHarnesses, cfg.HarnessInterval = o.detect, o.interval
	if mutate != nil {
		mutate(&cfg)
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// In these tests the "agent" and the command line are one process.
	d.inSession = func(int) bool { return false }
	if o.exe != "" {
		d.exe = func(int) string { return o.exe }
	}
	if o.cmdline != "" {
		d.cmdline = func(int) string { return o.cmdline }
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	n := &node{t: t, d: d, sock: sock, adapter: o.adapter, cfg: cfg, cmdline: o.cmdline}
	t.Cleanup(d.Stop)
	eventually(t, "daemon connected to gateway", d.Connected)
	return n
}

// liveReader is a machine whose processes keep using CPU: the tree of one
// root PID (a child under it), a stranger, and machine counters.
type liveReader struct {
	mu   sync.Mutex
	root int
	step int
}

func (r *liveReader) Read(ctx context.Context) (usage.Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.step++
	s := float64(r.step)
	child := r.root + 1000000
	return usage.Snapshot{
		At: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(r.step) * 5 * time.Second),
		Procs: map[int]usage.Proc{
			1:         {PID: 1, Comm: "init"},
			r.root:    {PID: r.root, PPID: 1, Start: 1, CPU: 0.1 * s, RSS: 10 << 20, Comm: "agent"},
			child:     {PID: child, PPID: r.root, Start: 2, CPU: 0.4 * s, RSS: 30 << 20, Comm: "devserver"},
			child + 1: {PID: child + 1, PPID: 1, Start: 3, CPU: 9 * s, RSS: 1 << 30, Comm: "stranger"},
		},
		M: usage.Machine{Cores: 4, CPUBusy: 100 * s, CPUTotal: 400 * s, MemUsed: 2 << 30, MemTotal: 8 << 30, Load: [3]float64{0.5, 0.4, 0.3}},
	}, nil
}

func (r *liveReader) Args(ctx context.Context, pids []int) map[int]string {
	out := map[int]string{}
	for _, p := range pids {
		out[p] = fmt.Sprintf("proc%d run --token sk-abcdefghijklmnopqrstuvwxyz0123456789", p)
	}
	return out
}

func liveSampler(root int) *usage.Sampler {
	return usage.NewWith(&liveReader{root: root}, 20*time.Millisecond, 5*time.Minute, nil)
}

func withSampler(root int) func(*Config) {
	return func(c *Config) {
		c.UsageSampler = liveSampler(root)
		c.UsageReportInterval = 30 * time.Millisecond
	}
}

type usageAnswer struct {
	NodeID  string `json:"node_id"`
	Local   *protocol.UsageReport
	Gateway *protocol.UsageAnswer `json:"gateway"`
	GwError string                `json:"gateway_error"`
}

func getUsage(t *testing.T, cli *localClient) usageAnswer {
	t.Helper()
	var a usageAnswer
	raw := cli.mustCall("usage", nil)
	var m map[string]json.RawMessage
	json.Unmarshal(raw, &m)
	json.Unmarshal(raw, &a)
	if l, ok := m["local"]; ok {
		a.Local = &protocol.UsageReport{}
		json.Unmarshal(l, a.Local)
	}
	return a
}

// ---- CPU and memory ----

func TestUsageReachesTheGatewayAndTheTopLists(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, withSampler(os.Getpid()))
	_, sid := helloPi(t, n, "p1", "/w/p", false)
	cli := cliClient(t, n)

	var node protocol.UsageNode
	eventually(t, "a usage report with figures on the gateway", func() bool {
		a := getUsage(t, cli)
		if a.Gateway == nil {
			return false
		}
		for _, un := range a.Gateway.Nodes {
			if un.NodeID == n.cfg.NodeID && len(un.Usage.Trees) == 1 && un.Usage.Trees[0].Samples > 0 && un.Usage.Machine.Samples > 0 {
				node = un
				return true
			}
		}
		return false
	})
	m := node.Usage.Machine
	if m.Cores != 4 || m.CPU != 25 || m.MemTotal != 8<<30 || m.MemUsed != 2<<30 || m.Load[0] != 0.5 || m.Procs != 4 {
		t.Errorf("machine = %+v", m)
	}
	tr := node.Usage.Trees[0]
	if tr.PID != os.Getpid() || tr.Procs != 2 || tr.Harness != "pi" || tr.Shared || len(tr.Sessions) != 1 || tr.Sessions[0] != sid {
		t.Errorf("tree = %+v", tr)
	}
	// Agent: 0.1 s and devserver 0.4 s of CPU per 5 s interval = 10%. The stranger is not counted.
	if tr.CPU != 10 || tr.RSS != 40<<20 || tr.CPUAvg != 10 {
		t.Errorf("tree figures = %+v", tr)
	}
	if node.Hostname != n.cfg.Hostname {
		t.Errorf("hostname = %q", node.Hostname)
	}
	if a := getUsage(t, cli); a.Local == nil || len(a.Local.Trees) != 1 {
		t.Errorf("no local figures: %+v", a)
	}

	// The top lists are not in the report. They come on request, with secrets masked.
	raw := cli.mustCall("usage_top", map[string]any{"session_id": sid})
	var top protocol.UsageTop
	json.Unmarshal(raw, &top)
	if top.SessionID != sid || top.Tree.Procs != 2 || len(top.ByCPU) != 2 || len(top.ByRSS) != 2 {
		t.Fatalf("top = %s", raw)
	}
	if top.ByCPU[0].Cmd != "devserver" || top.ByCPU[0].CPU != 8 || top.ByRSS[0].Cmd != "devserver" {
		t.Errorf("by cpu = %+v", top.ByCPU)
	}
	for _, p := range append(top.ByCPU, top.ByRSS...) {
		if strings.Contains(p.Args, "sk-abc") || !strings.Contains(p.Args, "[REDACTED]") || len([]rune(p.Args)) > 80 {
			t.Errorf("unsafe or odd args %q", p.Args)
		}
	}
	if _, err := cli.call("usage_top", map[string]any{"session_id": "s_nope"}); err == nil {
		t.Error("top of an unknown session should fail")
	}
}

func TestOpenCodeServiceIsReportedOnceAsShared(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{exe: "/opt/opencode"}, withSampler(os.Getpid()))
	ids := map[string]bool{}
	for _, native := range []string{"o1", "o2", "o3"} {
		lc := dialLocal(t, n.sock)
		res := lc.mustCall("hello", map[string]any{"client": "opencode", "harness": "opencode", "native_id": native, "cwd": "/w/" + native})
		var r struct {
			SessionID string `json:"session_id"`
		}
		json.Unmarshal(res, &r)
		ids[r.SessionID] = true
	}
	cli := cliClient(t, n)
	var tr protocol.TreeUsage
	eventually(t, "the shared tree", func() bool {
		a := getUsage(t, cli)
		if a.Local == nil || len(a.Local.Trees) != 1 || a.Local.Trees[0].Samples == 0 {
			return false
		}
		tr = a.Local.Trees[0]
		return true
	})
	if !tr.Shared || tr.Harness != "opencode" || len(tr.Sessions) != 3 {
		t.Fatalf("tree = %+v", tr)
	}
	for _, s := range tr.Sessions {
		if !ids[s] {
			t.Errorf("unknown session %s in the shared tree", s)
		}
	}
	if tr.CPU != 10 {
		t.Errorf("shared figure was divided or changed: %+v", tr)
	}
}

func TestUsageIsOffWithoutASampler(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	helloPi(t, n, "p1", "/w/p", false)
	cli := cliClient(t, n)
	a := getUsage(t, cli)
	if a.Local != nil || (a.Gateway != nil && len(a.Gateway.Nodes) != 0) {
		t.Errorf("figures from a daemon that does not measure: %+v", a)
	}
	if _, err := cli.call("usage_top", map[string]any{"session_id": "s_x"}); err == nil {
		t.Error("usage_top should fail")
	}
}

// ---- who may ask ----

func TestAgentConnectionsCannotCallOwnerData(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi", exe: "/opt/opencode"}, withSampler(os.Getpid()))
	pi, sid := helloPi(t, n, "p1", "/w/p", false)
	oc := dialLocal(t, n.sock)
	oc.mustCall("hello", map[string]any{"client": "opencode", "harness": "opencode", "native_id": "o1", "cwd": "/w/o"})
	un := dialLocal(t, n.sock) // never said hello as anything useful: a cli client is the only one allowed
	for name, lc := range map[string]*localClient{"pi": pi, "opencode": oc} {
		for _, method := range []string{"usage", "usage_top", "session_text", "counts", "session_enter"} {
			params := map[string]any{"session_id": sid}
			res, err := lc.call(method, params)
			if err == nil || !strings.Contains(err.Error(), "only available to the owner") {
				t.Errorf("%s client: %s gave %s, %v", name, method, res, err)
			}
		}
	}
	_ = un
}

func TestOperatorGateRefusesEveryClientButCLI(t *testing.T) {
	d := &Daemon{inSession: func(int) bool { return false }}
	for _, c := range []string{"mcp", "pi", "opencode", ""} {
		lc := &localConn{d: d, client: c}
		if err := lc.operatorGate("session_text"); err == nil {
			t.Errorf("client %q passed the gate", c)
		}
	}
	if err := (&localConn{d: d, client: "cli"}).operatorGate("session_text"); err != nil {
		t.Errorf("cli refused: %v", err)
	}
}

func TestCLIInsideASessionIsRefused(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, realGate: true})
	// The real check: this test process is the "command line". A session
	// whose process is this one, or a parent of it, makes the command line
	// "inside" the session.
	info := fakeSession("in1", "inside")
	info.PID = os.Getpid()
	fa.setSessions(info)
	eventually(t, "session known", func() bool { return len(n.d.Sessions()) == 1 })
	cli := cliClient(t, n)
	for _, method := range []string{"usage", "usage_top", "session_text", "counts", "session_enter"} {
		_, err := cli.call(method, map[string]any{"session_id": "s_x"})
		if err == nil || !strings.Contains(err.Error(), "inside an agent session") {
			t.Errorf("%s from inside a session: %v", method, err)
		}
	}
	// The same command line is fine when the session's process is not a parent.
	info.PID = 4242
	fa.setSessions(info)
	eventually(t, "session moved", func() bool { return n.d.Sessions()[0].PID == 4242 })
	if _, err := cli.call("counts", nil); err != nil && strings.Contains(err.Error(), "inside") {
		t.Errorf("counts refused outside a session: %v", err)
	}
	// A child of the session's process counts as inside too.
	info.PID = os.Getppid()
	fa.setSessions(info)
	eventually(t, "session moved again", func() bool { return n.d.Sessions()[0].PID == os.Getppid() })
	if _, err := cli.call("counts", nil); err == nil || !strings.Contains(err.Error(), "inside an agent session") {
		t.Errorf("a child of the session was allowed: %v", err)
	}
}

// ---- what happens inside a session ----

// fakePlugin is a plugin connection that answers session_text pushes.
type fakePlugin struct {
	t     *testing.T
	c     net.Conn
	mu    sync.Mutex
	seen  []map[string]any
	reply func(params map[string]any) map[string]any // nil: never answer
}

func dialFakePlugin(t *testing.T, sock, client, nativeID string, reply func(map[string]any) map[string]any) (*fakePlugin, string) {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	p := &fakePlugin{t: t, c: c, reply: reply}
	hello, _ := json.Marshal(map[string]any{"id": "1", "method": "hello", "params": map[string]any{
		"client": client, "harness": client, "native_id": nativeID, "cwd": "/w/" + nativeID}})
	c.Write(append(hello, '\n'))
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 0, 1<<16), 4<<20)
	if !sc.Scan() {
		t.Fatal("no hello answer")
	}
	var h struct {
		Result struct {
			SessionID string `json:"session_id"`
		} `json:"result"`
		Error string `json:"error"`
	}
	json.Unmarshal(sc.Bytes(), &h)
	if h.Error != "" {
		t.Fatal(h.Error)
	}
	go func() {
		n := 1
		for sc.Scan() {
			var m struct {
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if json.Unmarshal(sc.Bytes(), &m) != nil || m.Method != "session_text" {
				continue
			}
			p.mu.Lock()
			p.seen = append(p.seen, m.Params)
			p.mu.Unlock()
			if p.reply == nil {
				continue
			}
			n++
			res := p.reply(m.Params)
			res["req"] = m.Params["req"]
			line, _ := json.Marshal(map[string]any{"id": fmt.Sprint("r", n), "method": "session_text_result", "params": res})
			c.Write(append(line, '\n'))
		}
	}()
	return p, h.Result.SessionID
}

func (p *fakePlugin) requests() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]map[string]any(nil), p.seen...)
}

func ent(cursor, role, text string) map[string]any {
	return map[string]any{"cursor": cursor, "time": 1000, "role": role, "text": text}
}

func getText(t *testing.T, cli *localClient, q map[string]any) (protocol.TextPage, error) {
	t.Helper()
	raw, err := cli.call("session_text", q)
	if err != nil {
		return protocol.TextPage{}, err
	}
	var p protocol.TextPage
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p, nil
}

func TestSessionTextFromAPluginIsMaskedCutAndLimited(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	long := strings.Repeat("z", 5000)
	_, sid := dialFakePlugin(t, n.sock, "pi", "p1", func(q map[string]any) map[string]any {
		var es []map[string]any
		es = append(es, ent("c1", "user", "my key is sk-abcdefghijklmnopqrstuvwxyz0123456789 ok"))
		es = append(es, ent("c2", "assistant", "Authorization: Bearer abcdef0123456789abcdef"))
		es = append(es, ent("c3", "tool_result", long))
		es = append(es, ent("c4", "system", "a role that does not exist"))
		es = append(es, ent("c5", "user", "ctrl\x1b[31m\x00chars\nkept"))
		for i := 0; i < 10; i++ {
			es = append(es, ent(fmt.Sprint("x", i), "user", fmt.Sprint("filler ", i)))
		}
		return map[string]any{"ok": true, "page": map[string]any{"entries": es, "older": "c1", "newer": "x9"}}
	})
	cli := cliClient(t, n)
	page, err := getText(t, cli, map[string]any{"session_id": sid, "limit": 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 5 {
		t.Fatalf("entries = %d, want the newest 5", len(page.Entries))
	}
	if page.Entries[4].Text != "filler 9" || page.Older == "" || page.Newer != "x9" || page.Harness != "pi" || page.SessionID != sid {
		t.Errorf("page = %+v", page)
	}
	all, err := getText(t, cli, map[string]any{"session_id": sid, "limit": 100})
	if err != nil {
		t.Fatal(err)
	}
	var seenLong bool
	for _, e := range all.Entries {
		if strings.Contains(e.Text, "sk-abc") || strings.Contains(e.Text, "abcdef0123456789abcdef") {
			t.Errorf("secret in %q", e.Text)
		}
		if e.Role == "system" {
			t.Error("an unknown role got through")
		}
		if len([]rune(e.Text)) > protocol.TextMaxEntry {
			t.Errorf("entry of %d chars", len([]rune(e.Text)))
		}
		if len(e.Text) > 4000 {
			seenLong = true
		}
		if strings.ContainsAny(e.Text, "\x1b\x00") {
			t.Errorf("control characters in %q", e.Text)
		}
	}
	if seenLong {
		t.Error("long text was not cut")
	}
	if !all.Entries[1].Cut && all.Entries[2].Cut == false {
		t.Errorf("the cut mark is missing: %+v", all.Entries[2])
	}
	if got := all.Entries[0].Text; !strings.Contains(got, "[REDACTED]") {
		t.Errorf("text = %q", got)
	}
}

func TestSessionTextPassesThePagingToThePlugin(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{exe: "/opt/opencode"}, nil)
	pl, sid := dialFakePlugin(t, n.sock, "opencode", "o1", func(q map[string]any) map[string]any {
		return map[string]any{"ok": true, "page": map[string]any{"entries": []map[string]any{ent("m1:0", "user", "hi")}}}
	})
	cli := cliClient(t, n)
	if _, err := getText(t, cli, map[string]any{"session_id": sid, "before": "m9:0", "limit": 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := getText(t, cli, map[string]any{"session_id": sid, "after": "m1:0"}); err != nil {
		t.Fatal(err)
	}
	reqs := pl.requests()
	if len(reqs) != 2 || reqs[0]["before"] != "m9:0" || reqs[0]["limit"] != float64(7) || reqs[1]["after"] != "m1:0" || reqs[1]["limit"] != float64(protocol.TextDefaultLimit) {
		t.Errorf("requests = %v", reqs)
	}
}

func TestSessionTextErrors(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, func(c *Config) { c.TextTimeout = 150 * time.Millisecond })
	cli := cliClient(t, n)
	_, silent := dialFakePlugin(t, n.sock, "pi", "old", nil)
	if _, err := getText(t, cli, map[string]any{"session_id": silent}); err == nil || !strings.Contains(err.Error(), "/reload") {
		t.Errorf("an old plugin: %v", err)
	}
	_, failing := dialFakePlugin(t, n.sock, "pi", "bad", func(map[string]any) map[string]any {
		return map[string]any{"ok": false, "error": "no session manager"}
	})
	if _, err := getText(t, cli, map[string]any{"session_id": failing}); err == nil || !strings.Contains(err.Error(), "no session manager") {
		t.Errorf("a failing plugin: %v", err)
	}
	if _, err := getText(t, cli, map[string]any{}); err == nil {
		t.Error("session_text without session_id")
	}
}

// textAdapter is a harness the daemon reads itself, like Claude Code.
type textAdapter struct {
	fakeAdapter
	got []protocol.TextQuery
}

func (a *textAdapter) Text(ctx context.Context, info protocol.SessionInfo, q protocol.TextQuery) (protocol.TextPage, error) {
	a.got = append(a.got, q)
	return protocol.TextPage{Entries: []protocol.TextEntry{
		{Cursor: "1:0", Role: "user", Text: "token=hunter2hunter2hunter2"},
		{Cursor: "1:1", Role: "tool_call", Tool: "Bash", Text: "Bash: ls"},
	}, Newer: "1:1"}, nil
}

func TestSessionTextFromAnAdapterIsMasked(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &textAdapter{fakeAdapter: fakeAdapter{harness: "fake"}}
	n := startNodeCfg(t, g, nodeOpts{}, func(c *Config) { c.Adapters = []Adapter{fa} })
	fa.setSessions(fakeSession("a1", "alpha"))
	var sid string
	eventually(t, "session id", func() bool { sid = n.d.Sessions()[0].SessionID; return sid != "" })
	cli := cliClient(t, n)
	page, err := getText(t, cli, map[string]any{"session_id": sid, "sidechain": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || strings.Contains(page.Entries[0].Text, "hunter2") || page.Entries[1].Tool != "Bash" || page.Newer != "1:1" {
		t.Errorf("page = %+v", page)
	}
	if len(fa.got) != 1 || !fa.got[0].Sidechain || fa.got[0].Limit != protocol.TextDefaultLimit {
		t.Errorf("adapter saw %+v", fa.got)
	}
	// A harness with no reader says so.
	plain := &fakeAdapter{harness: "plain"}
	n2 := startNodeCfg(t, g, nodeOpts{adapter: plain}, nil)
	s2 := fakeSession("b1", "beta")
	s2.Harness = "plain"
	plain.setSessions(s2)
	var sid2 string
	eventually(t, "second id", func() bool { sid2 = n2.d.Sessions()[0].SessionID; return sid2 != "" })
	if _, err := getText(t, cliClient(t, n2), map[string]any{"session_id": sid2}); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("unsupported harness: %v", err)
	}
}

// Another machine's owner asks through the gateway. The text is read on the
// machine that holds the session. The gateway keeps no trace of it.
func TestSessionTextAndTopRelayThroughTheGatewayWithoutATrace(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	holder := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, withSampler(os.Getpid()))
	asker := startNodeCfg(t, g, nodeOpts{}, nil)
	marker := "UNIQUE-MARKER-4f9c1d"
	_, sid := dialFakePlugin(t, holder.sock, "pi", "remote1", func(q map[string]any) map[string]any {
		return map[string]any{"ok": true, "page": map[string]any{"entries": []map[string]any{
			ent("e1", "user", "please deploy "+marker+" with key=abcdef1234567890abcdef"),
		}}}
	})
	eventually(t, "the asker sees the session", func() bool {
		raw, err := cliClient(t, asker).call("list", map[string]any{"online_only": true})
		return err == nil && strings.Contains(string(raw), sid)
	})
	before, _ := g.gw.Store().FeedEvents(0, 1000)
	cli := cliClient(t, asker)
	page, err := getText(t, cli, map[string]any{"session_id": sid})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || !strings.Contains(page.Entries[0].Text, marker) || strings.Contains(page.Entries[0].Text, "abcdef1234567890abcdef") {
		t.Fatalf("page = %+v", page)
	}
	eventually(t, "top figures on the holder", func() bool {
		_, err := holder.d.localUsageTop(context.Background(), sid)
		return err == nil
	})
	var top protocol.UsageTop
	json.Unmarshal(cli.mustCall("usage_top", map[string]any{"session_id": sid}), &top)
	if top.SessionID != sid || len(top.ByCPU) == 0 {
		t.Errorf("remote top = %+v", top)
	}
	// Nothing in the log, nothing in the events.
	if lines := g.logs.lines(marker); len(lines) != 0 {
		t.Errorf("the gateway logged the text: %v", lines)
	}
	if lines := g.logs.lines("session_text"); len(lines) != 0 {
		t.Errorf("the gateway logged the request: %v", lines)
	}
	after, _ := g.gw.Store().FeedEvents(0, 1000)
	if len(after) != len(before) {
		t.Errorf("events grew from %d to %d", len(before), len(after))
	}
	for _, e := range after {
		if strings.Contains(e.Detail, marker) {
			t.Errorf("text in an event: %+v", e)
		}
	}
	// An offline or unknown session is refused with a reason.
	if _, err := getText(t, cli, map[string]any{"session_id": "s_0000000000000000"}); err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("unknown session: %v", err)
	}
}

func TestCountsComeFromTheGateway(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNodeCfg(t, g, nodeOpts{adapter: fa}, nil)
	fa.setSessions(fakeSession("c1", "one"), fakeSession("c2", "two"), fakeSession("c3", "three"))
	cli := cliClient(t, n)
	read := func() protocol.SessionCounts {
		var c protocol.SessionCounts
		json.Unmarshal(cli.mustCall("counts", nil), &c)
		return c
	}
	eventually(t, "three online", func() bool { return read().Online == 3 })
	fa.setSessions(fakeSession("c1", "one"))
	eventually(t, "two went offline", func() bool {
		c := read()
		return c.Online == 1 && c.Offline24h == 2 && c.OfflineTotal == 2
	})
}

// A plugin that does not say its mode is reported as "unknown", not as nothing.
func TestPluginWithoutAModeIsUnknown(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{exe: "/opt/opencode"}, nil)
	lc := dialLocal(t, n.sock)
	res := lc.mustCall("hello", map[string]any{"client": "opencode", "harness": "opencode", "native_id": "o1", "cwd": "/w/o"})
	var r struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(res, &r)
	a := agentByID(t, cliClient(t, n), r.SessionID)
	if a.Mode != protocol.ModeUnknown {
		t.Errorf("mode = %q, want unknown", a.Mode)
	}
}

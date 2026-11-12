package daemon

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The two herdr rules of decision 0154, on the fake herdr of
// herdr_open_test.go, which logs every call with its socket:
// the owner's herdr is read-only, and what agent-net hosts goes into its own
// "agent-net" herdr session.

// hasOwnHerdrCall says whether the log has command (a prefix) sent to
// agent-net's own server.
func hasOwnHerdrCall(t *testing.T, log, command string) bool {
	t.Helper()
	for _, c := range herdrCalls(t, log) {
		if c[0] == fakeOwnHerdrSocket(log) && strings.HasPrefix(c[1], command) {
			return true
		}
	}
	return false
}

// Enter from a TUI in the owner's herdr hands over the TUI's own pane, as in
// a plain terminal, and ctrl+] returns. Nothing is opened in that herdr.
func TestEnterInOwnerHerdrHandsOverPane(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "claude")
	ea := &enterAdapter{fakeAdapter: &fakeAdapter{harness: "claude"}, kind: "bg", job: "job_42"}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.HarnessPaths = bins; c.Adapters = []Adapter{ea} }})
	ea.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "abcdefgh-1234", PID: 1, Name: "garden", Mode: protocol.ModeBackground})
	var id string
	eventually(t, "session", func() bool {
		if s := n.d.Sessions(); len(s) == 1 {
			id = s[0].SessionID
		}
		return id != ""
	})
	p := enterOfHerdr(t, n, id, fakeHerdrSocket(log))
	if p.OpenedHerdr || !reflect.DeepEqual(p.Argv, []string{bins["claude"], "attach", "job_42"}) || !strings.Contains(p.Note, "ctrl+]") {
		t.Fatalf("plan %+v", p)
	}
	requireHerdrRules(t, log)
	if calls := herdrCalls(t, log); len(calls) != 0 {
		t.Fatalf("a handover called herdr: %v", calls)
	}
}

// A Pi session needs a terminal of its own, so agent-net hosts it in its own
// herdr server, started on demand: from a TUI in the owner's herdr and from
// one outside herdr alike.
func TestPiStartHostsInAgentNetHerdr(t *testing.T) {
	for _, inHerdr := range []bool{true, false} {
		t.Run(map[bool]string{true: "in the owner's herdr", false: "outside herdr"}[inHerdr], func(t *testing.T) {
			_, log := fakeOpenHerdr(t)
			g := startGateway(t, t.TempDir(), "")
			bins := fakeBin(t, "pi")
			n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = bins })
			repo := newRepo(t, "garden")
			n.register(t, repo)
			q := protocol.SessionStartRequest{Project: "garden", Harness: "pi"}
			if inHerdr {
				q = inHerdrStart(q, fakeHerdrSocket(log))
			}
			var r protocol.StartedSession
			mustJSON(t, n.cli.mustCall("session_start", q), &r)
			if r.EnterPlan == nil || !r.EnterPlan.OpenedHerdr || !strings.Contains(r.EnterPlan.Note, "agent-net's herdr") {
				t.Fatalf("start %+v", r)
			}
			requireHerdrRules(t, log)
			for _, want := range []string{"workspace create --label garden", "pane run w1:p1"} {
				if !hasOwnHerdrCall(t, log, want) {
					t.Errorf("no %q in agent-net's herdr: %v", want, herdrCalls(t, log))
				}
			}
			if calls := mustRead(t, log); !strings.Contains(string(calls), "--session agent-net server") {
				t.Errorf("agent-net's herdr was not started: %s", calls)
			}
		})
	}
}

// A view asked from another machine is hosted in agent-net's herdr on the
// session's machine, and the asking machine runs herdr --remote with
// --session agent-net in its own pane (decision 0046 keeps herdr --remote).
func TestRemoteViewHostsInAgentNetHerdr(t *testing.T) {
	remoteBin, remoteLog := fakeOpenHerdr(t)
	localBin, localLog := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "claude")
	bins["herdr"] = remoteBin
	ea := &enterAdapter{fakeAdapter: &fakeAdapter{harness: "claude"}, kind: "bg", job: "job_42"}
	remote := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.HarnessPaths = bins; c.Adapters = []Adapter{ea} }})
	local := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.HarnessPaths = map[string]string{"herdr": localBin} }})
	ea.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "abcdefgh-1234", PID: 1, Name: "garden", Cwd: t.TempDir(), Mode: protocol.ModeBackground})
	var id string
	eventually(t, "remote session", func() bool {
		if s := remote.d.Sessions(); len(s) == 1 {
			id = s[0].SessionID
		}
		return id != ""
	})
	waitSessionOnGateway(t, local, id)
	p := enterOfHerdr(t, local, id, fakeHerdrSocket(localLog))
	if want := []string{localBin, "--remote", remote.cfg.Hostname, "--session", "agent-net"}; p.OpenedHerdr || !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("plan %+v, want argv %v", p, want)
	}
	requireHerdrRules(t, localLog)
	requireHerdrRules(t, remoteLog)
	if !hasOwnHerdrCall(t, remoteLog, "pane run w1:p1") {
		t.Fatalf("the remote view is not in agent-net's herdr: %v", herdrCalls(t, remoteLog))
	}
}

// A sign-in asked from another machine is hosted in agent-net's herdr, and
// the answer names that herdr session for herdr --remote.
func TestSignInPrepareHostsInAgentNetHerdr(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	d := &Daemon{cfg: Config{NodeID: "n_mac", Hostname: "mac"}}
	raw, _ := json.Marshal(protocol.SignInPrepare{AskID: "i_signin", Provider: "alibaba"})
	res, err := d.signInPrepare(raw)
	if err != nil || !res.Opened || res.Session != "agent-net" {
		t.Fatalf("prepare %+v, error %v", res, err)
	}
	requireHerdrRules(t, log)
	if !hasOwnHerdrCall(t, log, "pane run w1:p1") {
		t.Fatalf("the sign-in is not in agent-net's herdr: %v", herdrCalls(t, log))
	}
}

// The machine that asks for a sign-in on another machine opens it with
// herdr --remote and the session the other machine named.
func TestSignInOpenRemoteNamesHerdrSession(t *testing.T) {
	bin, _ := fakeOpenHerdr(t)
	d := &Daemon{cfg: Config{NodeID: "local", testGatewayCall: func(_ context.Context, typ string, _ any) (json.RawMessage, bool, error) {
		switch typ {
		case "query.sign_in":
			b, _ := json.Marshal(protocol.SignInTarget{Provider: "alibaba", NodeID: "remote", Hostname: "remote-host"})
			return b, true, nil
		case "query.sign_in_prepare":
			b, _ := json.Marshal(protocol.SignInPrepareResult{Opened: true, Host: "remote-host", Pane: "w1:p7", Session: "agent-net"})
			return b, true, nil
		}
		return nil, false, nil
	}}}
	p, err := d.signInOpen(context.Background(), protocol.SignInOpenRequest{AskID: "ask"})
	if want := []string{bin, "--remote", "remote-host", "--session", "agent-net"}; err != nil || !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("plan %+v, error %v, want argv %v", p, err, want)
	}
}

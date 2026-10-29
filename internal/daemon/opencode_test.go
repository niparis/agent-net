package daemon

import (
	"encoding/json"
	"strings"
	"testing"
)

func helloOpenCode(t *testing.T, n *node, sessionID, cwd string) (*localClient, string) {
	t.Helper()
	lc := dialLocal(t, n.sock)
	lc.AutoAck = true
	res := lc.mustCall("hello", map[string]any{"client": "opencode", "harness": "opencode", "native_id": sessionID, "cwd": cwd, "status": "idle"})
	var r struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(res, &r)
	if r.SessionID == "" {
		t.Fatal("hello returned no session_id")
	}
	return lc, r.SessionID
}

func onlineOpenCode(t *testing.T, lc *localClient) map[string]bool {
	t.Helper()
	res := lc.mustCall("list", map[string]any{"harness": "opencode", "online_only": true})
	var r struct {
		Agents []struct {
			SessionID string `json:"session_id"`
			Harness   string `json:"harness"`
		} `json:"agents"`
	}
	json.Unmarshal(res, &r)
	out := map[string]bool{}
	for _, a := range r.Agents {
		if a.Harness != "opencode" {
			t.Fatalf("filter leaked %+v", a)
		}
		out[a.SessionID] = true
	}
	return out
}

// One OpenCode service process opens one connection per session. The
// sessions are registered, routed and removed separately.
func TestOpenCodeSessionsOnOneService(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{exe: "/opt/x/node_modules/@opencode/cli/bin/opencode.exe"})

	s1, id1 := helloOpenCode(t, n, "ses_one", "/work/one")
	s2, id2 := helloOpenCode(t, n, "ses_two", "/work/two")
	if id1 == id2 {
		t.Fatal("two sessions share one id")
	}
	if got := onlineOpenCode(t, s1); !got[id1] || !got[id2] {
		t.Fatalf("both sessions must be online, got %v", got)
	}

	// s1 sends to s2; only s2 gets the push, with s1 as the sender.
	m := sendResult(t, s1.mustCall("send", map[string]any{"to": id2, "body": "hi two"}))
	if m.From != id1 || m.To != id2 {
		t.Fatalf("wrong route %+v", m)
	}
	if push := s2.nextPush(); push.Body != "hi two" || push.From != id1 {
		t.Fatalf("unexpected push %+v", push)
	}

	// s2 closes (session deleted): only s2 goes away.
	s2.c.Close()
	eventually(t, "session two removed", func() bool { return !onlineOpenCode(t, s1)[id2] })
	if !onlineOpenCode(t, s1)[id1] {
		t.Fatal("session one must stay online")
	}
}

func TestOpenCodePeerCheck(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{exe: "/usr/bin/bash"})
	lc := dialLocal(t, n.sock)
	_, err := lc.call("hello", map[string]any{"client": "opencode", "harness": "opencode", "native_id": "ses_x", "cwd": "/"})
	if err == nil || !strings.Contains(err.Error(), "not OpenCode") {
		t.Fatalf("non-opencode process: got %v", err)
	}
	lc = dialLocal(t, n.sock)
	_, err = lc.call("hello", map[string]any{"client": "opencode", "harness": "pi", "native_id": "ses_x", "cwd": "/"})
	if err == nil {
		t.Fatal("wrong harness accepted")
	}
}

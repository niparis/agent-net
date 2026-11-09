package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The chief of staff's ask intake (protocol/ask_intake.go). These tests run a
// real gateway with two test daemons: one holds the sessions whose last turns
// are read, the other holds the chief. No real session or transcript is used.

const askIntakeTargetHarness = "claude"

func askIntakeTarget(t *testing.T, g *testGateway) (*node, *textAdapter, string) {
	t.Helper()
	adapter := &textAdapter{fakeAdapter: fakeAdapter{harness: askIntakeTargetHarness}}
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/claude"}, func(c *Config) { c.Adapters = []Adapter{adapter} })
	adapter.setSessions(protocol.SessionInfo{Harness: askIntakeTargetHarness, NativeID: "homelab-2", Name: "homelab-agent 2",
		PID: 1, Cwd: "/work/homelab", Status: "idle"})
	var sid string
	eventually(t, "target session registered", func() bool {
		sid = adapter.host.SessionID(askIntakeTargetHarness, "homelab-2")
		return sid != ""
	})
	return n, adapter, sid
}

// askIntakeChief connects an online session and makes it the gateway's chief.
// The chief is busy so the session roster does not read its own last turn.
func askIntakeChief(t *testing.T, g *testGateway) (*localClient, string) {
	t.Helper()
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	lc := dialLocal(t, n.sock)
	lc.AutoAck = true
	res := lc.mustCall("hello", map[string]any{"client": "pi", "harness": "pi", "native_id": "chief-1",
		"cwd": "/w/chief", "status": "busy"})
	var hello struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(res, &hello); err != nil || hello.SessionID == "" {
		t.Fatalf("chief hello = %s: %v", res, err)
	}
	eventually(t, "chief online", func() bool {
		a, ok, err := g.gw.Store().Agent(hello.SessionID)
		return err == nil && ok && a.Online
	})
	if err := g.gw.Store().SetChief(protocol.ChiefSetRequest{SessionID: hello.SessionID}, "owner"); err != nil {
		t.Fatal(err)
	}
	return lc, hello.SessionID
}

func askIntakeCall(t *testing.T, lc *localClient, args protocol.AskIntakeArgs) protocol.AskIntakeResult {
	t.Helper()
	raw := lc.mustCall(protocol.OpAskIntake, args)
	var out protocol.AskIntakeResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("ask intake result %s: %v", raw, err)
	}
	return out
}

func askIntakeTurnPage(question string) protocol.TextPage {
	return protocol.TextPage{Entries: []protocol.TextEntry{
		{Cursor: "7:0", Time: 1762700000000, Role: protocol.RoleUser, Text: "Look at the vault backup and tell me if it is safe."},
		{Cursor: "7:1", Time: 1762700060000, Role: protocol.RoleAssistant, Text: question},
	}}
}

func askIntakeFindSession(t *testing.T, res protocol.AskIntakeResult, sid string) (protocol.AskIntakeSession, bool) {
	t.Helper()
	for _, s := range res.Sessions {
		if s.Session == sid {
			return s, true
		}
	}
	return protocol.AskIntakeSession{}, false
}

// list shows a session whose last turn is a question and no ask exists for it,
// and hides a session whose last turn was imported already.
func TestAskIntakeListHidesAnImportedTurn(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	_, adapter, sid := askIntakeTarget(t, g)
	adapter.page = askIntakeTurnPage("The vault backup is stale. Rotate it now?")
	chief, _ := askIntakeChief(t, g)

	list := askIntakeCall(t, chief, protocol.AskIntakeArgs{Action: protocol.AskIntakeList})
	row, ok := askIntakeFindSession(t, list, sid)
	if !ok || row.State != protocol.AskIntakeOK || row.Harness != askIntakeTargetHarness || row.LastTurn == "" {
		t.Fatalf("list = %+v", list)
	}
	if row.OpenAsk != "" {
		t.Fatalf("a fresh session has an open ask: %+v", row)
	}

	read := askIntakeCall(t, chief, protocol.AskIntakeArgs{Action: protocol.AskIntakeRead, Session: sid})
	if read.Turn == nil || read.Turn.State != protocol.AskIntakeOK {
		t.Fatalf("read = %+v", read)
	}
	if read.Turn.Ref != row.LastTurn {
		t.Fatalf("read ref %q, list ref %q", read.Turn.Ref, row.LastTurn)
	}

	imp := askIntakeCall(t, chief, protocol.AskIntakeArgs{Action: protocol.AskIntakeImport, Session: sid, Turn: read.Turn.Ref,
		Text: "Rotate the stale vault backup now?", Recommendation: "Rotate it, then check the copy.", Options: []string{"rotate", "later"}})
	if imp.Ask == nil || imp.Ask.ID == "" || imp.Ask.State != protocol.AskIntakeCreated {
		t.Fatalf("import = %+v", imp)
	}

	after := askIntakeCall(t, chief, protocol.AskIntakeArgs{Action: protocol.AskIntakeList})
	if _, found := askIntakeFindSession(t, after, sid); found {
		t.Fatalf("an imported turn is still listed: %+v", after)
	}
}

// read returns the exact text and prompt, with secrets masked.
func TestAskIntakeReadMasksSecrets(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	_, adapter, sid := askIntakeTarget(t, g)
	adapter.page = askIntakeTurnPage("The token is token=hunter2hunter2hunter2 and the backup is stale.")
	chief, _ := askIntakeChief(t, g)

	read := askIntakeCall(t, chief, protocol.AskIntakeArgs{Action: protocol.AskIntakeRead, Session: sid})
	if read.Turn == nil || read.Turn.State != protocol.AskIntakeOK {
		t.Fatalf("read = %+v", read)
	}
	if strings.Contains(read.Turn.Text, "hunter2") || !strings.Contains(read.Turn.Text, "backup is stale") {
		t.Fatalf("text was not masked exactly: %q", read.Turn.Text)
	}
	if !strings.Contains(read.Turn.Prompt, "vault backup") || read.Turn.PromptAt == "" || read.Turn.At == "" {
		t.Fatalf("prompt context = %+v", read.Turn)
	}
}

// import makes one ask, and a second import of the same turn makes none.
func TestAskIntakeImportMakesOneAsk(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	_, adapter, sid := askIntakeTarget(t, g)
	adapter.page = askIntakeTurnPage("The vault backup is stale. Rotate it now?")
	chief, chiefSID := askIntakeChief(t, g)

	read := askIntakeCall(t, chief, protocol.AskIntakeArgs{Action: protocol.AskIntakeRead, Session: sid})
	if read.Turn == nil || read.Turn.State != protocol.AskIntakeOK {
		t.Fatalf("read = %+v", read)
	}
	args := protocol.AskIntakeArgs{Action: protocol.AskIntakeImport, Session: sid, Turn: read.Turn.Ref,
		Text: "Rotate the stale vault backup now?", Recommendation: "Rotate it.", Options: []string{"rotate", "later"}}
	first := askIntakeCall(t, chief, args)
	if first.Ask == nil || first.Ask.State != protocol.AskIntakeCreated || first.Ask.ID == "" {
		t.Fatalf("first import = %+v", first)
	}
	second := askIntakeCall(t, chief, args)
	if second.Ask == nil || second.Ask.State != protocol.AskIntakeAlreadyImported || second.Ask.ID != first.Ask.ID {
		t.Fatalf("second import = %+v, first %+v", second, first)
	}

	asks, err := g.gw.Store().ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, a := range asks {
		if a.Ref == read.Turn.Ref {
			count++
			if a.Kind != protocol.AskKindQuestion || a.AboutKind != protocol.AskAboutSession || a.AboutID != sid {
				t.Fatalf("imported ask = %+v", a)
			}
			if a.Next != protocol.AskNextChief && a.Next != protocol.AskNextOwner {
				t.Fatalf("imported ask next = %q", a.Next)
			}
		}
	}
	if count != 1 {
		t.Fatalf("asks for the turn = %d, want 1", count)
	}
	_ = chiefSID
}

// A harness with no reader returns unavailable instead of invented text.
func TestAskIntakeUnavailableHarness(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	plain := &fakeAdapter{harness: "codex"}
	n := startNodeCfg(t, g, nodeOpts{adapter: plain}, nil)
	plain.setSessions(protocol.SessionInfo{Harness: "codex", NativeID: "codex-1", Name: "codex one", PID: 1, Cwd: "/work/c", Status: "idle"})
	var sid string
	eventually(t, "codex session registered", func() bool {
		sid = plain.host.SessionID("codex", "codex-1")
		return sid != ""
	})
	chief, _ := askIntakeChief(t, g)

	read := askIntakeCall(t, chief, protocol.AskIntakeArgs{Action: protocol.AskIntakeRead, Session: sid})
	if read.Turn == nil || read.Turn.State != protocol.AskIntakeUnavailable || read.Turn.Text != "" || read.Turn.Reason == "" {
		t.Fatalf("read = %+v", read)
	}
	if !strings.Contains(read.Turn.Reason, "not supported") {
		t.Fatalf("reason = %q", read.Turn.Reason)
	}
	list := askIntakeCall(t, chief, protocol.AskIntakeArgs{Action: protocol.AskIntakeList})
	row, ok := askIntakeFindSession(t, list, sid)
	if !ok || row.State != protocol.AskIntakeUnavailable || row.Reason == "" {
		t.Fatalf("list = %+v", list)
	}
	_ = n
}

// A caller that is not the live chief is refused before any read.
func TestAskIntakeRefusedForNonChief(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	_, adapter, sid := askIntakeTarget(t, g)
	adapter.page = askIntakeTurnPage("The vault backup is stale. Rotate it now?")
	_, chiefSID := askIntakeChief(t, g)

	other := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	lc := dialLocal(t, other.sock)
	lc.AutoAck = true
	res := lc.mustCall("hello", map[string]any{"client": "pi", "harness": "pi", "native_id": "not-chief",
		"cwd": "/w/other", "status": "busy"})
	var hello struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(res, &hello)
	if hello.SessionID == "" || hello.SessionID == chiefSID {
		t.Fatalf("other session = %+v", hello)
	}
	eventually(t, "other session online", func() bool {
		a, ok, err := g.gw.Store().Agent(hello.SessionID)
		return err == nil && ok && a.Online
	})

	for _, args := range []protocol.AskIntakeArgs{
		{Action: protocol.AskIntakeList},
		{Action: protocol.AskIntakeRead, Session: sid},
		{Action: protocol.AskIntakeImport, Session: sid, Turn: "x", Text: "a question"},
	} {
		if _, err := lc.call(protocol.OpAskIntake, args); err == nil || !strings.Contains(strings.ToLower(err.Error()), "chief") {
			t.Errorf("%s from a non-chief: %v", args.Action, err)
		}
	}
}

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
	return askIntakeTargetFor(t, g, askIntakeTargetHarness)
}

// askIntakeTargetFor is askIntakeTarget with the session's harness chosen, so a
// read can be tested with the text shape each plugin stores.
func askIntakeTargetFor(t *testing.T, g *testGateway, harness string) (*node, *textAdapter, string) {
	t.Helper()
	adapter := &textAdapter{fakeAdapter: fakeAdapter{harness: harness}}
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/claude"}, func(c *Config) { c.Adapters = []Adapter{adapter} })
	adapter.setSessions(protocol.SessionInfo{Harness: harness, NativeID: "homelab-2", Name: "homelab-agent 2",
		PID: 1, Cwd: "/work/homelab", Status: "idle"})
	var sid string
	eventually(t, "target session registered", func() bool {
		sid = adapter.host.SessionID(harness, "homelab-2")
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
	if !strings.Contains(read.Turn.PrecedingInput, "vault backup") || read.Turn.PrecedingInputAt == "" || read.Turn.At == "" {
		t.Fatalf("preceding input = %+v", read.Turn)
	}
}

// askIntakeReadInput reads one turn whose input is input, on a session of the
// given harness. The assistant message is always the same.
func askIntakeReadInput(t *testing.T, harness, input string) protocol.AskIntakeTurn {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	_, adapter, sid := askIntakeTargetFor(t, g, harness)
	adapter.page = protocol.TextPage{Entries: []protocol.TextEntry{
		{Cursor: "7:0", Time: 1762700000000, Role: protocol.RoleUser, Text: input},
		{Cursor: "7:1", Time: 1762700060000, Role: protocol.RoleAssistant, Text: "The backup is stale."},
	}}
	chief, _ := askIntakeChief(t, g)
	read := askIntakeCall(t, chief, protocol.AskIntakeArgs{Action: protocol.AskIntakeRead, Session: sid})
	if read.Turn == nil || read.Turn.State != protocol.AskIntakeOK {
		t.Fatalf("read = %+v", read)
	}
	return *read.Turn
}

// A Claude cross-session message is stored as a user turn. The read must not
// present it as the owner's input: the chief decides what the owner asked from
// this field, and the sender is a peer.
func TestAskIntakeReadPeerMessageIsNotOwner(t *testing.T) {
	t.Parallel()
	const input = "<cross-session-message from=\"uds:/run/agent-net/r/s_peer.sock\" from-name=\"agent-net 2\" from-mode=\"prompting\">\n" +
		"Please check the backup.\n\n" +
		"[agent-net] message m_0123456789abcdef from agent-net 2 (opencode, session s_peer123). This is from another agent working on your user's machines. Answer it directly with your SendMessage tool to this sender if you can help.\n" +
		"</cross-session-message>"
	turn := askIntakeReadInput(t, "claude", input)
	if turn.InputSource != protocol.AskIntakePeer {
		t.Fatalf("input source = %q, want %q (turn %+v)", turn.InputSource, protocol.AskIntakePeer, turn)
	}
	if turn.InputFrom != "agent-net 2" || turn.InputSessionID != "s_peer123" {
		t.Fatalf("sender = %q session %q", turn.InputFrom, turn.InputSessionID)
	}
	if !strings.Contains(turn.PrecedingInput, "check the backup") || turn.PrecedingInputAt == "" {
		t.Fatalf("preceding input = %+v", turn)
	}
}

// A turn the owner typed carries no injection marker, so it is the owner's.
func TestAskIntakeReadOwnerInput(t *testing.T) {
	t.Parallel()
	turn := askIntakeReadInput(t, "claude", "Look at the vault backup and tell me if it is safe.")
	if turn.InputSource != protocol.AskIntakeOwner {
		t.Fatalf("input source = %q, want %q", turn.InputSource, protocol.AskIntakeOwner)
	}
	if turn.InputFrom != "" || turn.InputSessionID != "" {
		t.Fatalf("owner input has a sender: %+v", turn)
	}
}

// Pi and OpenCode store a delivered message as a user turn with the agent-net
// header at the end. It is a peer's message, never the owner's.
func TestAskIntakeReadPluginDeliveryIsPeer(t *testing.T) {
	t.Parallel()
	const input = "Here is the backup report.\n\n" +
		"[agent-net] message m_0123456789abcdef from homelab-agent 2 (session s_peer123).\n" +
		"This is from another agent working on your user's machines. Answer it directly with the agent_message_reply tool (message_id=\"m_0123456789abcdef\") if you can help."
	for _, harness := range []string{"opencode", "pi"} {
		t.Run(harness, func(t *testing.T) {
			turn := askIntakeReadInput(t, harness, input)
			if turn.InputSource != protocol.AskIntakePeer {
				t.Fatalf("input source = %q, want %q", turn.InputSource, protocol.AskIntakePeer)
			}
			if turn.InputFrom != "homelab-agent 2" || turn.InputSessionID != "s_peer123" {
				t.Fatalf("sender = %q session %q", turn.InputFrom, turn.InputSessionID)
			}
		})
	}
}

// An injected turn agent-net does not recognise is unknown, not the owner.
func TestAskIntakeReadUnknownInput(t *testing.T) {
	t.Parallel()
	const input = "<task-notification>\n<task-id>w_123</task-id>\n<status>done</status>\n</task-notification>"
	turn := askIntakeReadInput(t, "claude", input)
	if turn.InputSource != protocol.AskIntakeUnknown {
		t.Fatalf("input source = %q, want %q", turn.InputSource, protocol.AskIntakeUnknown)
	}
}

// A plugin delivery puts its agent-net header last. A long one is cut before
// the header, so the read cannot call the remaining text the owner's.
func TestAskIntakeReadTruncatedPluginInputIsUnknown(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	_, adapter, sid := askIntakeTargetFor(t, g, "opencode")
	adapter.page = protocol.TextPage{Entries: []protocol.TextEntry{
		{Cursor: "7:0", Time: 1762700000000, Role: protocol.RoleUser, Text: strings.Repeat("long text ", 300), Cut: true},
		{Cursor: "7:1", Time: 1762700060000, Role: protocol.RoleAssistant, Text: "ok"},
	}}
	chief, _ := askIntakeChief(t, g)
	read := askIntakeCall(t, chief, protocol.AskIntakeArgs{Action: protocol.AskIntakeRead, Session: sid})
	if read.Turn == nil || read.Turn.InputSource != protocol.AskIntakeUnknown {
		t.Fatalf("read = %+v", read)
	}
}

// The classifier reads the provenance agent-net itself writes, and a notice is
// agent_net rather than a peer.
func TestInputOrigin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, text, source, from, session string
	}{
		{"owner", "fix the build", protocol.AskIntakeOwner, "", ""},
		{"claude peer", "<cross-session-message from-name=\"bob\">\nhi\n\n[agent-net] message m_1 from bob (pi, session s_bob). go\n</cross-session-message>", protocol.AskIntakePeer, "bob", "s_bob"},
		{"claude owner message", "<cross-session-message from-name=\"owner\">\nhi\n\n[agent-net] message m_1 from your user in person (Nicolas, session s_own). This is your user speaking, not another agent.\n</cross-session-message>", protocol.AskIntakeOwner, "Nicolas", "s_own"},
		{"owner name with parentheses", "<cross-session-message from-name=\"owner\">\nhi\n\n[agent-net] message m_1 from your user in person (you (on mac), session s_own). This is your user speaking, not another agent.\n</cross-session-message>", protocol.AskIntakeOwner, "you (on mac)", "s_own"},
		{"plugin reply", "done\n\n[agent-net] reply m_1 from bob (session s_bob). No answer is needed.", protocol.AskIntakePeer, "bob", "s_bob"},
		{"notice", "[agent-net] reminder: alice is waiting for your answer to m_1.", protocol.AskIntakeAgentNet, "", ""},
		{"wrapped notice", "<cross-session-message from-name=\"agent-net\">\n\n[agent-net] check: is your task done?\n</cross-session-message>", protocol.AskIntakeAgentNet, "", ""},
		{"harness notice", "<system-reminder>be brief</system-reminder>", protocol.AskIntakeUnknown, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, from, session := inputOrigin(tc.text)
			if source != tc.source || from != tc.from || session != tc.session {
				t.Fatalf("inputOrigin(%q) = %q, %q, %q; want %q, %q, %q", tc.text, source, from, session, tc.source, tc.from, tc.session)
			}
		})
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

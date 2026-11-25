package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Claude Code records a prompt given on its command line as typed by a human.
// These tests check that a first message the owner did not write never counts
// as the owner's words, however its hook and transcript entry are timed, and
// across a daemon restart.

// firstMessageLaunch is a daemon with no gateway and no socket that launches
// the fake Claude Code. The fake session has no inbox socket, so these tests
// run where unix sockets are not allowed. hook runs the owner check the way
// the ordinary branch of policyCall runs it.
type firstMessageLaunch struct {
	t        *testing.T
	d        *Daemon
	dir      string // the launch folder
	sessions string
	state    string
}

const firstMessageSession = "s_first"

func newFirstMessageLaunch(t *testing.T) *firstMessageLaunch {
	t.Helper()
	sessions, _ := useFakeClaude(t)
	t.Setenv(envFakeClaudeNoInbox, "1")
	l := &firstMessageLaunch{t: t, dir: t.TempDir(), sessions: sessions, state: t.TempDir()}
	l.restart()
	return l
}

// restart is a new daemon over the same state folder: only daemon.db carries
// over. The old daemon is left as a stopped process leaves it.
func (l *firstMessageLaunch) restart() {
	l.t.Helper()
	if l.d != nil {
		l.d.st.close()
	}
	d, err := New(Config{StateDir: l.state, ClaudeSessionsDir: l.sessions, SocketPath: filepath.Join(l.state, "unused.sock")})
	if err != nil {
		l.t.Fatal(err)
	}
	// Long enough for a launch to return while a check waits for the entry.
	d.claudeOwnerWait = scaled(30 * time.Second)
	l.t.Cleanup(func() { d.claudeChecks.Wait(); d.st.close() })
	l.d = d
}

// launch starts a session whose first message the owner did not write.
// onJob, when set, runs when `claude --bg` names the job.
func (l *firstMessageLaunch) launch(text, resume string, onJob func(string)) <-chan launchResult {
	done := make(chan launchResult, 1)
	d := l.d
	go func() {
		sf, err := d.startBackground(context.Background(), os.Args[0], l.dir, claudeBackground{Name: "first", Resume: resume, FirstMessage: text, NonOwnerFirstMessage: true}, "", nil, onJob)
		done <- launchResult{sf, err}
	}()
	return done
}

type launchResult struct {
	sf  claudeSessionFile
	err error
}

func (l *firstMessageLaunch) wait(done <-chan launchResult) claudeSessionFile {
	l.t.Helper()
	select {
	case r := <-done:
		if r.err != nil {
			l.t.Fatal(r.err)
		}
		return r.sf
	case <-time.After(scaled(30 * time.Second)):
		l.t.Fatal("the launch did not return")
	}
	return claudeSessionFile{}
}

// running is the conversation of the session the fake started, read before
// the launch returns.
func (l *firstMessageLaunch) running() string {
	l.t.Helper()
	return readClaude(l.t, l.dir).Session
}

func (l *firstMessageLaunch) hook(native, text, promptID, transcript string) {
	p := protocol.PolicyRequest{Harness: "claude", NativeID: native, Prompt: text, PromptID: promptID, TranscriptPath: transcript}
	l.d.whenClaudeOwnerPrompt(context.Background(), p, func(_ context.Context, owner bool) {
		if owner {
			l.d.rememberOwnerPrompt(firstMessageSession, p.Prompt)
			return
		}
		l.d.notePendingOwnerWords(firstMessageSession, p)
	})
}

// typed writes the transcript entries Claude Code writes for prompts typed
// by a human, which is how it records a command-line prompt too.
func typed(t *testing.T, path, text string, promptIDs ...string) {
	t.Helper()
	var entries []map[string]any
	for _, id := range promptIDs {
		entries = append(entries, claudeUserEntry(id, text, typedOrigin))
	}
	if err := os.WriteFile(path, claudeTranscriptBytes(t, entries...), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (l *firstMessageLaunch) ownerWords(text string) bool {
	l.d.claudeChecks.Wait()
	return l.d.ownerWordsMatch(firstMessageSession, text)
}

// Claude Code runs the prompt hook for the first message about a second after
// the launch (2.1.332), which can be before `claude --bg` returns and names
// the conversation. The check waits for the launch, then skips the message.
func TestClaudeFirstMessageHookBeforeTheLaunchReturns(t *testing.T) {
	const msg = "Review the open changes and report."
	l := newFirstMessageLaunch(t)
	gate := filepath.Join(t.TempDir(), "return")
	t.Setenv(envFakeClaudeReturnGate, gate)
	done := l.launch(msg, "", nil)
	native := l.running()
	transcript := filepath.Join(t.TempDir(), native+".jsonl")
	l.hook(native, msg, "p-first", transcript)
	typed(t, transcript, msg, "p-first")
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if sf := l.wait(done); sf.SessionID != native {
		t.Fatalf("the launch named %q, the session runs %q", sf.SessionID, native)
	}
	if l.ownerWords(msg) {
		t.Fatal("the first message counted as the owner's words")
	}
}

// A hook that runs before the mark is written, with the human entry written
// late: the check runs again after the wait for the entry, before any effect.
func TestClaudeFirstMessageCheckedAgainAfterTheTranscriptWait(t *testing.T) {
	const msg = "Merge the ready changes."
	const native = "1f1f1f1f-0000-4000-8000-000000000001"
	l := newFirstMessageLaunch(t)
	transcript := filepath.Join(t.TempDir(), native+".jsonl")
	l.hook(native, msg, "p-first", transcript)
	l.wait(l.launch(msg, native, nil))
	typed(t, transcript, msg, "p-first")
	if l.ownerWords(msg) {
		t.Fatal("the first message counted as the owner's words")
	}
}

// The wait for the entry ends with no entry, so the prompt waits as pending
// words. A later read that finds a human entry must not keep a first message.
func TestClaudeFirstMessagePendingWordsCheckedAgain(t *testing.T) {
	const msg = "Merge the ready changes."
	const native = "1f1f1f1f-0000-4000-8000-000000000002"
	l := newFirstMessageLaunch(t)
	l.d.claudeOwnerWait = 50 * time.Millisecond
	transcript := filepath.Join(t.TempDir(), native+".jsonl")
	l.hook(native, msg, "p-first", transcript)
	l.d.claudeChecks.Wait()
	l.wait(l.launch(msg, native, nil))
	typed(t, transcript, msg, "p-first")
	if l.ownerWords(msg) {
		t.Fatal("the pending first message was kept as the owner's words")
	}
}

// The mark is in daemon.db: a hook that comes after a daemon restart skips
// the first message. A later prompt with the same text is the owner's.
func TestClaudeFirstMessageMarkSurvivesARestart(t *testing.T) {
	const msg = "Read your notes first."
	l := newFirstMessageLaunch(t)
	native := l.wait(l.launch(msg, "", nil)).SessionID
	l.restart()
	transcript := filepath.Join(t.TempDir(), native+".jsonl")
	typed(t, transcript, msg, "p-first", "p-owner")
	l.hook(native, msg, "p-first", transcript)
	if l.ownerWords(msg) {
		t.Fatal("after a restart the first message counted as the owner's words")
	}
	l.hook(native, msg, "p-owner", transcript)
	if !l.ownerWords(msg) {
		t.Fatal("the owner's later prompt with the same text did not count")
	}
}

// A daemon that stops before `claude --bg` names the job leaves a mark that
// names no conversation. The first prompt with the text after the restart
// takes it.
func TestClaudeFirstMessageOfALaunchCutByARestart(t *testing.T) {
	const msg = "Read your notes first."
	l := newFirstMessageLaunch(t)
	gate := filepath.Join(t.TempDir(), "return")
	t.Setenv(envFakeClaudeReturnGate, gate)
	done := l.launch(msg, "", nil)
	native := l.running()
	l.restart()
	t.Cleanup(func() { os.WriteFile(gate, nil, 0o600); <-done })
	transcript := filepath.Join(t.TempDir(), native+".jsonl")
	typed(t, transcript, msg, "p-first")
	l.hook(native, msg, "p-first", transcript)
	if l.ownerWords(msg) {
		t.Fatal("after a restart the first message counted as the owner's words")
	}
}

// A resume that Claude Code gives a new conversation ID, cut by a restart
// after `claude --bg` named the job: the job's session file names the new
// conversation.
func TestClaudeFirstMessageOfAResumeCutByARestart(t *testing.T) {
	const msg = "Deliver the queued message."
	const old = "1f1f1f1f-0000-4000-8000-000000000003"
	l := newFirstMessageLaunch(t)
	settle := filepath.Join(t.TempDir(), "settle")
	t.Setenv(envFakeResumeSettleGate, settle)
	t.Setenv("FAKE_CLAUDE_NEW_RESUME_ID", "1")
	named := make(chan struct{})
	done := l.launch(msg, old, func(string) { close(named) })
	// The launch has the job and waits for the session file to settle.
	select {
	case <-named:
	case <-time.After(scaled(30 * time.Second)):
		t.Fatal("claude --bg did not name the job")
	}
	l.restart()
	t.Cleanup(func() { <-done })
	if err := os.WriteFile(settle, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	native := l.running()
	if native == old {
		t.Fatalf("the resume kept conversation %s", old)
	}
	transcript := filepath.Join(t.TempDir(), native+".jsonl")
	typed(t, transcript, msg, "p-first")
	eventually(t, "the session file names the new conversation", func() bool {
		sf, ok := findClaudeBg(l.sessions, native[:8])
		return ok && sf.SessionID == native
	})
	l.hook(native, msg, "p-first", transcript)
	if l.ownerWords(msg) {
		t.Fatal("after a restart the resumed first message counted as the owner's words")
	}
}

// ---- who wrote the first message, through each start path ----
//
// These run a gateway and the daemon socket. A sandbox without unix sockets
// cannot run them; CI does.

func startClaudeFirstMessageNode(t *testing.T) (*testGateway, *claudeNode, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	c.register(t, repo)
	trustClaudeFolder(t, repo)
	t.Cleanup(func() { killFakeClaude(t, repo) })
	return g, c, repo
}

func waitClaudeStart(t *testing.T, d *Daemon, id string) protocol.StartedSession {
	t.Helper()
	var r protocol.StartedSession
	eventually(t, "Claude session started", func() bool {
		var err error
		r, err = d.sessionStartGet(context.Background(), id)
		if err != nil || r.Status == "failed" {
			t.Fatalf("start %+v %v", r, err)
		}
		return r.Status == "started" && r.SessionID != "" && r.NativeID != ""
	})
	return r
}

// firstMessagePrompt is Claude Code running the hook for the first message,
// with the transcript entry it writes for a typed prompt.
func firstMessagePrompt(t *testing.T, n *node, d *Daemon, native, text string) {
	t.Helper()
	transcript := filepath.Join(t.TempDir(), native+".jsonl")
	typed(t, transcript, text, "p-first")
	askPolicy(t, cliClient(t, n), protocol.PolicyRequest{
		Harness: "claude", NativeID: native, Prompt: text, PromptID: "p-first", TranscriptPath: transcript,
	})
	d.claudeChecks.Wait()
}

func assertGatewayOwnerEffects(t *testing.T, g *testGateway, sid, text string, wantOwner bool) {
	t.Helper()
	a, _, err := g.gw.Store().Agent(sid)
	if err != nil {
		t.Fatalf("agent %s: %v", sid, err)
	}
	wantUses := 0
	if wantOwner {
		wantUses = 1
	}
	if a.OwnerUses != wantUses {
		t.Fatalf("OwnerUses = %d, want %d", a.OwnerUses, wantUses)
	}
	// The owner-instruction note: a message the session sends after a prompt
	// of the owner carries the owner's words.
	st := g.gw.Store()
	peerNode, _, err := st.CreateNode("peer-node", "linux")
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := st.UpsertSession(peerNode, protocol.SessionInfo{
		Harness: "pi", NativeID: "peer-1", Name: "peer", Repo: protocol.RepoInfo{Project: "peer"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.StoreMessage(protocol.MsgSend{Sender: sid, Recipient: recipient, Body: "ping"}); err != nil {
		t.Fatal(err)
	}
	signals, err := st.Signals()
	if err != nil {
		t.Fatal(err)
	}
	if wantOwner != (len(signals) != 0) {
		t.Fatalf("signals %+v for %q, want owner %v", signals, text, wantOwner)
	}
	// The owner_use answer to the open question.
	judgments, err := st.FindJudgments(protocol.JudgmentsFindRequest{})
	if err != nil {
		t.Fatal(err)
	}
	answered := false
	for _, j := range judgments {
		answered = answered || strings.Contains(j.Text, "A: ")
	}
	if answered != wantOwner {
		t.Fatalf("judgments %+v for %q, want an answer %v", judgments, text, wantOwner)
	}
}

func openQuestionItem(t *testing.T, g *testGateway, sid string) {
	t.Helper()
	if _, err := g.gw.Store().CreateInboxItem(protocol.InboxItem{
		Kind: "question", SessionID: sid, Node: "shop", Text: "Ready?",
	}); err != nil {
		t.Fatal(err)
	}
}

// checkFirstMessage runs the hook for the first message of a start and checks
// every owner effect.
func checkFirstMessage(t *testing.T, g *testGateway, c *claudeNode, r protocol.StartedSession, text string, wantOwner bool) {
	t.Helper()
	openQuestionItem(t, g, r.SessionID)
	firstMessagePrompt(t, c.node, c.d, r.NativeID, text)
	if got := c.d.ownerWordsMatch(r.SessionID, text); got != wantOwner {
		t.Fatalf("owner words %v, want %v", got, wantOwner)
	}
	assertGatewayOwnerEffects(t, g, r.SessionID, text, wantOwner)
}

// peerFirstMessage names the peer that assertGatewayOwnerEffects sends to. The
// gateway links a prompt to a message only when the prompt names the
// recipient, so without the name an agent's text would make no signal even if
// it counted as the owner's words.
const peerFirstMessage = "ask peer to use a codex worker for this"

// The owner's own start from the TUI or command line: the text is the owner's.
func TestClaudeOwnerFirstMessageCountsAsOwnerWords(t *testing.T) {
	const msg = peerFirstMessage
	g, c, _ := startClaudeFirstMessageNode(t)
	var r protocol.StartedSession
	mustJSON(t, c.cli.mustCall("session_start", protocol.SessionStartRequest{Project: "shop", Harness: "claude", FirstMessage: msg}), &r)
	checkFirstMessage(t, g, c, waitClaudeStart(t, c.d, r.ID), msg, true)
}

// The chief, the merger and a CTO proposal start through sessionStart with a
// message agent-net or the CTO wrote. The flags, not the text, say who wrote
// it, so each case uses the text that names the peer.
func TestClaudeAgentFirstMessageIsNotOwnerWords(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  protocol.SessionStartRequest
	}{
		{"chief", protocol.SessionStartRequest{Chief: true, FirstMessage: peerFirstMessage}},
		{"merger", protocol.SessionStartRequest{Merger: true, CTOProposal: true, FirstMessage: peerFirstMessage}},
		{"cto proposal", protocol.SessionStartRequest{CTOProposal: true, FirstMessage: peerFirstMessage}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, c, _ := startClaudeFirstMessageNode(t)
			tc.req.Project, tc.req.Harness = "shop", "claude"
			r, err := c.d.sessionStart(context.Background(), tc.req)
			if err != nil {
				t.Fatal(err)
			}
			checkFirstMessage(t, g, c, waitClaudeStart(t, c.d, r.ID), tc.req.FirstMessage, false)
		})
	}
}

// The CTO starting a session on the owner's verified words still wrote the
// first message itself. Its arguments cannot say the owner wrote it.
func TestClaudeCTOStartOnOwnerWordsFirstMessageIsNotOwnerWords(t *testing.T) {
	const msg = peerFirstMessage
	g, c, _ := startClaudeFirstMessageNode(t)
	words := "start a session in shop"
	ctoOwnerSays(t, c.d, words)
	args, _ := json.Marshal(map[string]any{"action": "session_start", "project": "shop", "harness": "claude", "first_message": msg, "owner_words": words, "first_message_by_owner": true})
	if _, err := c.d.runCTOToolRaw(context.Background(), "session_change", args); err != nil {
		t.Fatal(err)
	}
	var id string
	eventually(t, "the start is recorded", func() bool {
		c.d.mu.Lock()
		defer c.d.mu.Unlock()
		for k := range c.d.startedSessions {
			id = k
		}
		return id != ""
	})
	checkFirstMessage(t, g, c, waitClaudeStart(t, c.d, id), msg, false)
}

// The owner's start relayed to the machine that holds the project keeps the
// owner's words there.
func TestClaudeRelayedOwnerFirstMessageCountsAsOwnerWords(t *testing.T) {
	const msg = peerFirstMessage
	g, b, _ := startClaudeFirstMessageNode(t)
	a := startWorkerNode(t, g, shortSet, nil)
	var r protocol.StartedSession
	mustJSON(t, a.cli.mustCall("session_start", protocol.SessionStartRequest{Project: "shop", Harness: "claude", NodeID: b.d.NodeID(), FirstMessage: msg}), &r)
	checkFirstMessage(t, g, b, waitClaudeStart(t, b.d, r.ID), msg, true)
}

// A delivery to the CTO by a restart passes the message on the command line.
// Words the owner sent the CTO are kept by cto_send, not by this prompt.
func TestClaudeCTODeliveryRestartFirstMessageIsNotOwnerWords(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	if err := c.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	const delivered = "use a codex worker for this"
	if err := c.d.startClaudeRoleWith(context.Background(), roleCTO, "cto-delivery-native", "", delivered); err != nil {
		t.Fatal(err)
	}
	c.d.cto.mu.Lock()
	native := c.d.claudeCTO.sessionID()
	c.d.cto.mu.Unlock()
	if native == "" {
		t.Fatal("CTO native session ID not recorded")
	}
	firstMessagePrompt(t, c.node, c.d, native, delivered)
	if c.d.ownerWordsMatch(c.d.ctoSessionID(), delivered) {
		t.Fatal("the delivered message counted as the owner's words to the CTO")
	}
}

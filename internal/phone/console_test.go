package phone

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

// consoleOperator is a fake daemon with state: an answered ask leaves the list.
type consoleOperator struct {
	mu    sync.Mutex
	calls []string
	asks  []protocol.Ask
	sent  []string
	// reads counts the read calls by method; threads holds the messages of a
	// conversation, newest first.
	reads   map[string]int
	threads map[string][]protocol.Message
	// The CTO panel reads the settings, the timeline and the open proposals.
	cto        protocol.CTOSettings
	ctoEvents  protocol.CTOEvents
	proposals  []protocol.CTOProposal
	ctoIndexes []int
}

func (f *consoleOperator) Snapshot(context.Context) (operator.Snapshot, error) {
	now := time.Now().Unix()
	return operator.Snapshot{
		Connected: true,
		Nodes: []protocol.NodeView{
			{NodeID: "n_secretnode1", Hostname: "alpha-box", Version: "v0.5.0", Online: true},
			{NodeID: "n_secretnode2", Hostname: "beta-box", Version: "v0.4.9", Online: false},
		},
		Agents: []protocol.AgentView{
			{SessionID: "s_wait", AgentID: "a_wait", NodeID: "n_secretnode1", Hostname: "alpha-box", Harness: "claude", Model: "opus-5-5", Online: true, Status: "idle", LastActive: now - 240,
				Waiting: &protocol.Waiting{Text: "allow the tools?"},
				Profile: protocol.Profile{Name: "waiting-agent", Task: "task of waiting"}, Repo: protocol.RepoInfo{Project: "proj-wait"},
				Charter: protocol.AgentCharter{Purpose: "Leads the forge team."}},
			{SessionID: "s_busy", AgentID: "a_busy", NodeID: "n_secretnode1", Hostname: "alpha-box", Harness: "codex", Model: "gpt-6.1", Online: true, Status: "busy", LastActive: now - 5,
				Profile: protocol.Profile{Name: "busy-agent", Task: "task of busy"}, Repo: protocol.RepoInfo{Project: "proj-busy"},
				Charter: protocol.AgentCharter{Purpose: "Charter purpose of the busy agent."}},
			{SessionID: "s_idle", NodeID: "n_secretnode1", Hostname: "alpha-box", Harness: "claude", Online: true, Status: "idle", LastActive: now - 3600,
				Profile: protocol.Profile{Name: "idle-agent", Task: "task of idle"}},
			{SessionID: "s_off", NodeID: "n_secretnode2", Hostname: "beta-box", Harness: "pi", Online: false,
				Profile: protocol.Profile{Name: "off-agent", Task: "task of off"}},
			{SessionID: "s_owner", Harness: protocol.OwnerHarness, Online: true, Profile: protocol.Profile{Name: "owner-pseudo"}},
		},
	}, nil
}
func (f *consoleOperator) SessionText(context.Context, protocol.TextQuery) (protocol.TextPage, error) {
	return protocol.TextPage{}, nil
}
func (f *consoleOperator) AnswerHeld(_ context.Context, q protocol.HeldAnswer) error {
	f.record("held:" + q.MessageID + ":" + q.Action)
	f.drop(q.MessageID)
	return nil
}
func (f *consoleOperator) ResolveOutcome(_ context.Context, id, action string) error {
	f.record("outcome:" + id + ":" + action)
	f.drop(id)
	return nil
}
func (f *consoleOperator) SendAsOwner(_ context.Context, to, body string) (protocol.MessageSent, error) {
	f.record("send:" + to + ":" + body)
	return protocol.MessageSent{}, nil
}

// ReplyAsOwner keeps the message in the conversation, as the gateway does for
// an owner send with a conversation id. The thread of a worker row holds the
// messages of its conversation.
func (f *consoleOperator) ReplyAsOwner(_ context.Context, conversation, to, body string) (protocol.MessageSent, error) {
	f.record("reply:" + conversation + ":" + to + ":" + body)
	f.mu.Lock()
	defer f.mu.Unlock()
	msg := protocol.Message{ID: "m_new", ConversationID: conversation, From: "s_owner", To: to, FromName: "you (on alpha-box)", FromHarness: protocol.OwnerHarness, Body: body, CreatedAt: time.Now()}
	for key := range f.threads {
		if key == conversation || (conversation == "c_w" && key == "worker:s_w1") {
			f.threads[key] = append([]protocol.Message{msg}, f.threads[key]...)
		}
	}
	return protocol.MessageSent{Message: &msg}, nil
}
func (f *consoleOperator) record(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}
func (f *consoleOperator) drop(idOrMessage string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var kept []protocol.Ask
	for _, a := range f.asks {
		if a.ID != idOrMessage && a.MessageID != idOrMessage {
			kept = append(kept, a)
		}
	}
	f.asks = kept
}
func (f *consoleOperator) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	now := time.Now()
	f.mu.Lock()
	f.reads[method]++
	f.mu.Unlock()
	if raw, ok := snapshotFixtureRead(ctx, f, method); ok {
		return raw, nil
	}
	switch method {
	case protocol.OpAsksList:
		f.mu.Lock()
		defer f.mu.Unlock()
		return json.Marshal(protocol.AsksResult{Asks: f.asks})
	case protocol.OpChangesOwnerFind:
		args, _ := params.(protocol.ChangesFindArgs)
		return json.Marshal(protocol.ChangesFindResult{Items: consoleChanges(args, now)})
	case protocol.OpChiefSorts:
		return json.Marshal(protocol.ChiefSortsResult{Sorts: []protocol.ChiefSort{{AskSort: protocol.AskSort{Ask: "i_old", At: now.Add(-time.Hour), Result: "close", Reason: "answered by the lead"}, Question: "handled question text"}}})
	case protocol.OpDeskLog:
		return json.Marshal(protocol.DeskLogResult{Entries: []protocol.DeskLogEntry{{ID: 1, At: now.Add(-3 * time.Minute), Source: "release", Text: "log line text"}}})
	case protocol.OpUsage:
		return json.Marshal(protocol.UsageResult{Gateway: &protocol.UsageAnswer{Nodes: []protocol.UsageNode{{NodeID: "n_secretnode1", Hostname: "alpha-box", AgeSeconds: 3,
			Usage: protocol.UsageReport{Machine: protocol.MachineUsage{Cores: 8, CPU: 34, MemUsed: 8 << 30, MemTotal: 16 << 30, Samples: 5}}}}}})
	case protocol.OpGraphView:
		return json.Marshal(protocol.GraphView{Nodes: []protocol.Node{{Ref: &protocol.GraphRef{Kind: "agent", ID: "a_busy"}, Name: "busy-agent", Place: &protocol.NodePlace{Team: "forge-team", TeamLead: "lead-agent"}}}})
	case protocol.OpConversations:
		return json.Marshal(protocol.ConversationPage{Total: 6, Conversations: []protocol.ConversationView{
			{ID: "c_1", From: "s_busy", To: "s_wait", FromName: "busy-agent", ToName: "waiting-agent", Count: 3, LastLine: "recent conversation line", At: now},
			{ID: "c_3", From: "s_owner", To: "s_wait", FromName: "you (on alpha-box)", ToName: "waiting-agent", Count: 2, LastLine: "allow the tools?", At: now.Add(-time.Minute), OwnerInstruction: true},
			{ID: "c_cto", From: "s_cto", To: "s_busy", FromName: "the CTO", ToName: "busy-agent", Count: 1, LastLine: "cto line about the plan", At: now.Add(-2 * time.Minute), FromCTO: true},
			{ID: "worker:s_w1", From: "s_wait", To: "s_w1", FromName: "waiting-agent", ToName: "worker-one", Count: 4, LastLine: "worker last line", At: now.Add(-3 * time.Minute), WorkerSession: "s_w1", WorkerOwner: "s_wait", Task: "Fix the flaky test"},
			{ID: "c_2", From: "s_x", To: "s_y", FromName: "x", ToName: "y", LastLine: "unrelated line", At: now.Add(-time.Hour)},
		}})
	case protocol.OpConversationMessages:
		q, _ := params.(protocol.QueryConversationMessages)
		f.mu.Lock()
		defer f.mu.Unlock()
		return json.Marshal(protocol.ConversationMessagesResult{Messages: f.threads[q.ConversationID]})
	case protocol.OpCTOSettings:
		return json.Marshal(f.cto)
	case protocol.OpCTOEvents:
		q, _ := params.(protocol.CTOEventsRequest)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.ctoIndexes = append(f.ctoIndexes, q.Index)
		return json.Marshal(f.ctoEvents)
	case protocol.OpProposalList:
		f.mu.Lock()
		defer f.mu.Unlock()
		return json.Marshal(f.proposals)
	case protocol.OpCTOSend:
		q, _ := params.(protocol.CTOSendRequest)
		f.record("cto_send:" + q.Text)
		raw, _ := json.Marshal(map[string]any{"type": "user", "text": q.Text, "time": map[string]int64{"created": time.Now().UnixMilli()}})
		f.mu.Lock()
		f.ctoEvents.Messages = append(f.ctoEvents.Messages, raw)
		f.mu.Unlock()
		return nil, nil
	case protocol.OpProposalAnswer:
		q, _ := params.(protocol.CTOProposalAnswer)
		f.record("proposal:" + q.NodeID + ":" + q.ID + ":" + q.Action)
		f.mu.Lock()
		var kept []protocol.CTOProposal
		for _, p := range f.proposals {
			if p.ID != q.ID {
				kept = append(kept, p)
			}
		}
		f.proposals = kept
		f.mu.Unlock()
		return nil, nil
	}
	f.record("call:" + method)
	if method == "inbox_answer" {
		if m, ok := params.(map[string]string); ok {
			f.record("answer:" + m["id"] + ":" + m["answer"])
			f.drop(m["id"])
		}
	}
	if method == protocol.OpAskMark {
		if m, ok := params.(protocol.AskMarkRequest); ok {
			f.record("mark:" + m.Ask + ":" + m.Mark)
			f.drop(m.Ask)
		}
	}
	return nil, nil
}
func (f *consoleOperator) has(call string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == call {
			return true
		}
	}
	return false
}

func newConsoleOperator() *consoleOperator {
	now := time.Now()
	return &consoleOperator{
		reads: map[string]int{},
		cto:   protocol.CTOSettings{Title: "Atlas", Harness: "claude", Model: "opus-5-5"},
		ctoEvents: protocol.CTOEvents{Messages: []json.RawMessage{
			json.RawMessage(`{"type":"user","text":"Good morning, CTO.","time":{"created":` + strconv.FormatInt(now.Add(-2*time.Minute).UnixMilli(), 10) + `}}`),
			json.RawMessage(`{"type":"assistant","text":"Morning. Two things need you.\nThe first is ready.","time":{"created":` + strconv.FormatInt(now.Add(-time.Minute).UnixMilli(), 10) + `}}`),
		}, Actions: []protocol.CTOAction{{Text: "read graph", At: now.Add(-90 * time.Second).UnixNano()}}, Older: 120},
		proposals: []protocol.CTOProposal{{ID: "p_1", NodeID: "n_secretnode1", Tool: "graph_change", Action: "charter_set",
			Args: json.RawMessage(`{"node":"forge","attr":"lead_merges","value":"on"}`), Reason: "Turn on lead merges for forge.", Status: "open", CreatedAt: now.Add(-time.Minute)}},
		threads: map[string][]protocol.Message{
			"c_3": {
				{ID: "m_b", ConversationID: "c_3", From: "s_wait", To: "s_owner", FromName: "waiting-agent", Body: "allow the tools?", CreatedAt: now.Add(-2 * time.Minute)},
				{ID: "m_a", ConversationID: "c_3", From: "s_owner", To: "s_wait", FromName: "you (on alpha-box)", FromHarness: protocol.OwnerHarness, Body: "go ahead and run the build", OwnerInstruction: "run the build", CreatedAt: now.Add(-10 * time.Minute)},
			},
			"c_1": {
				{ID: "m_c", ConversationID: "c_1", From: "s_busy", To: "s_wait", FromName: "busy-agent", Body: "recent conversation line", CreatedAt: now},
			},
			"c_cto": {
				{ID: "m_d", ConversationID: "c_cto", From: "s_cto", To: "s_busy", FromName: "the CTO", FromCTO: true, Body: "cto line about the plan", CreatedAt: now.Add(-2 * time.Minute)},
			},
			"worker:s_w1": {
				{ID: "m_f", ConversationID: "c_w", From: "s_w1", To: "s_wait", FromName: "worker-one", Body: "[agent-net worker notice]\nstatus: done\nrun time: 12m\n3 files changed", CreatedAt: now.Add(-3 * time.Minute)},
				{ID: "m_e", ConversationID: "c_w", From: "s_w1", To: "s_wait", FromName: "worker-one", Body: "[agent-net worker notice]\nstatus: started", CreatedAt: now.Add(-20 * time.Minute)},
			},
		}, asks: []protocol.Ask{
			{ID: "i_q", Kind: protocol.AskKindQuestion, State: protocol.AskStateOpen, From: "tenebrae", FromKind: protocol.AskFromSession, Next: protocol.AskNextOwner,
				Question: "Allow the record tools for every session?", Recommendation: "Yes, record and read tools only.", WhyYou: "Permission settings are yours.", Options: []string{"Record only", "All tools"},
				CreatedAt: time.Now().Add(-4 * time.Minute)},
			{ID: "i_h", Kind: protocol.AskKindHeld, State: protocol.AskStateOpen, From: "peer-a", MessageID: "m_held", AboutID: "s_busy", Detail: "held request body", Next: protocol.AskNextOwner, CreatedAt: time.Now()},
			{ID: "i_e", Kind: protocol.AskKindEscalation, State: protocol.AskStateOpen, From: "peer-b", MessageID: "r_esc", Question: "Request: do the thing\nReason: unclear", Recommendation: "Let it stand.", Next: protocol.AskNextOwner, CreatedAt: time.Now()},
			{ID: "i_c", Kind: protocol.AskKindApproval, State: protocol.AskStateOpen, From: "gateway", Ref: "apps-public-consent", Question: "Allow internet access?", Next: protocol.AskNextOwner, CreatedAt: time.Now()},
			{ID: "i_w", Kind: protocol.AskKindWaiting, State: protocol.AskStateOpen, From: "waiting-agent", FromSession: "s_wait", Question: "allow the tools?", Next: protocol.AskNextOwner, CreatedAt: time.Now()},
			{ID: "i_p", Kind: protocol.AskKindProposal, State: protocol.AskStateOpen, From: "CTO", Ref: "proposal:n1:p1", Question: "start a session", Next: protocol.AskNextOwner, CreatedAt: time.Now()},
			{ID: "i_other", Kind: protocol.AskKindQuestion, State: protocol.AskWithSomeone, From: "x", Question: "not yours to answer", Next: "chief", CreatedAt: time.Now()},
		}}
}

func page1(t *testing.T, f *consoleOperator, path string) string {
	t.Helper()
	w := httptest.NewRecorder()
	New(f).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 200 {
		t.Fatalf("GET %s: %d", path, w.Code)
	}
	return w.Body.String()
}

func mustContain(t *testing.T, where, body string, want ...string) {
	t.Helper()
	for _, s := range want {
		if !strings.Contains(body, s) {
			t.Errorf("%s: missing %q", where, s)
		}
	}
}
func mustNotContain(t *testing.T, where, body string, bad ...string) {
	t.Helper()
	for _, s := range bad {
		if strings.Contains(body, s) {
			t.Errorf("%s: should not contain %q", where, s)
		}
	}
}

func post(t *testing.T, f *consoleOperator, path string, v url.Values) int {
	t.Helper()
	r := httptest.NewRequest("POST", "http://example.test"+path, strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://example.test")
	w := httptest.NewRecorder()
	New(f).ServeHTTP(w, r)
	return w.Code
}

func TestDeskRendersAsksWithRecommendationAndWhyYou(t *testing.T) {
	f := newConsoleOperator()
	body := page1(t, f, "/")
	mustContain(t, "desk", body,
		"Needs you", "Allow the record tools for every session?", "tenebrae", "question", "4m",
		"Recommended", "Yes, record and read tools only.", "Why you", "Permission settings are yours.",
		"held request body", "do the thing", "unclear", "Let it stand.",
		"peer-b escalated this request: &#34;unclear&#34;",
		"Keep the escalation: the requester does not get do the thing",
		"Overrule: the requester gets do the thing")
	// The rail, the CTO button and the bottom bar.
	for _, name := range []string{"Desk", "Sessions", "Graph", "Changes", "Messages", "Workers"} {
		mustContain(t, "rail "+name, body, ">"+name+"<")
	}
	mustContain(t, "shell", body, `id="toggleCTO"`, `class="bottomnav"`, `id="ctobody"`, `id="cto" hidden`, `class="live"`)
	// An ask that waits on someone else is not shown.
	mustNotContain(t, "desk", body, "not yours to answer")
	// Hand-off kinds say where to answer.
	mustContain(t, "desk proposal", body, "Answer from the TUI")
}

func TestDeskShowsInFlightHandledAndLog(t *testing.T) {
	body := page1(t, newConsoleOperator(), "/")
	mustContain(t, "desk", body, "In flight", "Push updates part 1", "ch_flight1", "merger-node", "Handled for you", "handled question text", "answered by the lead", "Log", "log line text", "release")
	// The pipeline strip has a class per step state.
	mustContain(t, "pipeline", body, `class="pipe"`, `<i class="p">`, `<i class="r">`)
}

func TestAnswerPostsThroughOperatorAndAskDisappears(t *testing.T) {
	f := newConsoleOperator()
	if !strings.Contains(page1(t, f, "/"), "Allow the record tools for every session?") {
		t.Fatal("ask missing before the answer")
	}
	if code := post(t, f, "/action", url.Values{"kind": {"inbox"}, "id": {"i_q"}, "action": {"Record only"}}); code != 303 {
		t.Fatalf("answer status %d", code)
	}
	if !f.has("answer:i_q:Record only") {
		t.Fatalf("operator not called: %v", f.calls)
	}
	body := page1(t, f, "/")
	mustNotContain(t, "after answer", body, "Allow the record tools for every session?")
	// Held and outcome answers use the dedicated operator calls.
	post(t, f, "/action", url.Values{"kind": {"held"}, "id": {"m_held"}, "action": {"a"}})
	post(t, f, "/action", url.Values{"kind": {"outcome"}, "id": {"r_esc"}, "action": {"let_stand"}})
	if !f.has("held:m_held:a") || !f.has("outcome:r_esc:let_stand") {
		t.Fatalf("calls: %v", f.calls)
	}
	body = page1(t, f, "/")
	mustNotContain(t, "after held and outcome", body, "held request body", "do the thing")
	// "Didn't need me" goes to the ask mark call.
	f = newConsoleOperator()
	post(t, f, "/action", url.Values{"kind": {"mark"}, "id": {"i_q"}, "action": {protocol.MarkDidntNeedMe}})
	if !f.has("mark:i_q:" + protocol.MarkDidntNeedMe) {
		t.Fatalf("mark calls: %v", f.calls)
	}
	// A waiting session is answered as the owner, to that session.
	post(t, f, "/action", url.Values{"kind": {"send"}, "id": {"s_wait"}, "action": {"yes, allow"}})
	if !f.has("send:s_wait:yes, allow") {
		t.Fatalf("send calls: %v", f.calls)
	}
}

func TestConsentAskCannotBeApprovedFromTheWeb(t *testing.T) {
	body := page1(t, newConsoleOperator(), "/")
	i := strings.Index(body, "Allow internet access?")
	if i < 0 {
		t.Fatal("consent ask missing")
	}
	chunk := body[i:]
	if j := strings.Index(chunk, `class="ask`); j > 0 {
		chunk = chunk[:j]
	}
	mustContain(t, "consent ask", chunk, "Dismiss")
	mustNotContain(t, "consent ask", chunk, ">Approve<")
}

func TestSessionsGroupsByStateAndShowsDetail(t *testing.T) {
	f := newConsoleOperator()
	body := page1(t, f, "/sessions?s=s_busy")
	var at []int
	for _, h := range []string{"Needs you (1)", "Working (1)", "Idle (1)", "Offline (1)"} {
		i := strings.Index(body, h)
		if i < 0 {
			t.Fatalf("group %q missing", h)
		}
		at = append(at, i)
	}
	for i := 1; i < len(at); i++ {
		if at[i] < at[i-1] {
			t.Fatalf("groups out of order: %v", at)
		}
	}
	mustContain(t, "sessions", body, "waiting-agent", "busy-agent", "idle-agent", "off-agent")
	mustNotContain(t, "sessions", body, "owner-pseudo")
	// Machines strip.
	mustContain(t, "machines", body, "alpha-box", "v0.5.0", "34%", "8.0/16.0G", "beta-box", "offline")
	// Detail of the selected session.
	mustContain(t, "detail", body, "task of busy", "proj-busy", "codex", "gpt-6.1", "forge-team", "lead-agent", "Charter purpose of the busy agent.", "recent conversation line", "Open from the TUI")
	mustNotContain(t, "detail", body, "unrelated line", "Open in herdr")
	// No node IDs on the page.
	mustNotContain(t, "ids", body, "n_secretnode")
}

func TestNoActionRoutesForWorkersCharterOrSettings(t *testing.T) {
	f := newConsoleOperator()
	for _, path := range []string{"/workers/start", "/workers/stop", "/worker", "/charter", "/settings", "/graph/edge", "/sessions/start", "/sessions/stop", "/session/s_busy/stop", "/changes/merge", "/cto/send"} {
		if code := post(t, f, path, url.Values{"id": {"x"}}); code != 404 && code != 405 {
			t.Errorf("POST %s: %d, want 404 or 405", path, code)
		}
	}
	for _, kind := range []string{"worker_start", "worker_stop", "charter_set", "settings", "start", "stop", "graph", "edge_set"} {
		if code := post(t, f, "/action", url.Values{"kind": {kind}, "id": {"x"}, "action": {"y"}}); code != 400 {
			t.Errorf("kind %s: %d, want 400", kind, code)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 0 {
		t.Fatalf("operator was called: %v", f.calls)
	}
}

func TestGraphAndWorkersAreNoLongerComingSoon(t *testing.T) {
	f := newConsoleOperator()
	for _, tc := range []struct{ path, title, panel string }{{"/graph", "Graph", "Network"}, {"/workers", "Workers", "Subscriptions"}} {
		body := page1(t, f, tc.path)
		mustContain(t, tc.path, body, "<h1", tc.title, tc.panel)
		mustNotContain(t, tc.path, body, "coming in the next update", "<form")
	}
}

func TestOldSessionRouteRedirects(t *testing.T) {
	w := httptest.NewRecorder()
	New(newConsoleOperator()).ServeHTTP(w, httptest.NewRequest("GET", "/session/s_busy", nil))
	if w.Code != 303 || w.Header().Get("Location") != "/sessions?s=s_busy" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestStreamRendersTheScreenAndCounts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w := &twoFlushes{ResponseRecorder: httptest.NewRecorder(), cancel: cancel, count: 1}
	New(newConsoleOperator()).ServeHTTP(w, httptest.NewRequest("GET", "/events?screen=sessions&s=s_busy", nil).WithContext(ctx))
	body := w.Body.String()
	mustContain(t, "stream", body, "event: update", "busy-agent", "Working (1)", `hx-swap-oob`)
}

func TestShellLoadsNothingFromOutside(t *testing.T) {
	f := newConsoleOperator()
	for _, path := range []string{"/", "/sessions", "/graph", "/changes", "/messages?s=c_3"} {
		body := page1(t, f, path)
		for _, bad := range []string{"googleapis", "gstatic", "cdn.", "unpkg", "jsdelivr", "http://", "https://"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s loads or links %q", path, bad)
			}
		}
	}
	css, err := fs.ReadFile(files, "static/console.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"@import", "url(http", "googleapis"} {
		if strings.Contains(string(css), bad) {
			t.Errorf("css contains %q", bad)
		}
	}
}

func TestCSSHasNoFixedWidthOver400AndHasPhoneBar(t *testing.T) {
	raw, err := fs.ReadFile(files, "static/console.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	for _, m := range regexp.MustCompile(`(?:^|[^-\w])(?:min-)?width:\s*(\d+)px`).FindAllStringSubmatch(css, -1) {
		if n, _ := strconv.Atoi(m[1]); n > 400 {
			t.Errorf("fixed width of %dpx would scroll a 400px screen: %s", n, m[0])
		}
	}
	// A min-width inside minmax() or a grid column also counts.
	for _, m := range regexp.MustCompile(`minmax\((\d+)px`).FindAllStringSubmatch(css, -1) {
		if n, _ := strconv.Atoi(m[1]); n > 400 {
			t.Errorf("minmax(%dpx) is wider than 400px", n)
		}
	}
	mustContain(t, "css", css, "@media (max-width: 760px)", ".bottomnav", "prefers-color-scheme: dark", "--f-body", "system-ui")
	// The page must scale to the phone.
	mustContain(t, "viewport", page1(t, newConsoleOperator(), "/"), `name="viewport"`, "width=device-width")
}

func TestKeysSwitchScreens(t *testing.T) {
	js, err := fs.ReadFile(files, "static/console.js")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, "console.js", string(js), "keydown", "/sessions", "/graph", "/changes", "/messages", "/workers")
	body := page1(t, newConsoleOperator(), "/")
	mustContain(t, "keys", body, `src="/static/console.js"`, `data-key="1"`, `data-key="6"`)
}

func TestDaemonDownIsVisible(t *testing.T) {
	w := httptest.NewRecorder()
	New(nil).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	mustContain(t, "nil source", w.Body.String(), "Daemon not connected", "agent-net")
}

// consoleChanges is the fake gateway's delivery data: three open changes in
// three states, one done change, and the full read of one change.
func consoleChanges(args protocol.ChangesFindArgs, now time.Time) []protocol.ChangeView {
	step := func(name string, state string, owner protocol.StepOwner, note string) protocol.StepStatus {
		st := protocol.StepStatus{StepRef: protocol.StepRef{Step: name}, State: state, Owner: owner}
		if note != "" {
			st.Evidence = &protocol.Evidence{StepRef: st.StepRef, Result: "pass", By: "s_author", ByModel: "claude-sonnet-5-5", Note: note, Commit: "0123456789abcdef0123456789abcdef01234567", At: now.Unix()}
		}
		return st
	}
	caller, merger := protocol.StepOwner{Kind: "caller"}, protocol.StepOwner{Kind: protocol.OwnerNode, Node: "merger-node"}
	flight := protocol.ChangeView{
		Change: protocol.Change{ID: "ch_flight1", Title: "Push updates part 1", Project: "agent-net", Branch: "agent-net/w_flight", HeadCommit: "0123456789abcdef0123456789abcdef01234567", Revision: 2, Risk: 3, State: protocol.ChangeOpen, Caller: "s_wait",
			Origin: protocol.ChangeOrigin{Kind: protocol.OriginWorker, SessionID: "s_busy", Model: "gpt-6.1"}, UpdatedAt: now.Add(-22 * time.Minute).Unix(), CreatedAt: now.Add(-time.Hour).Unix()},
		Steps: []protocol.StepStatus{
			step("accept", protocol.StepStatePassed, caller, "accepted by reading the diff"),
			step("review", protocol.StepStatePassed, caller, "review found nothing"),
			step("test", protocol.StepStateRunning, merger, ""),
			step("merge", protocol.StepStateWaiting, merger, ""),
		},
		Next: []protocol.StepRef{{Step: "test"}},
	}
	failed := protocol.ChangeView{
		Change: protocol.Change{ID: "ch_fail1", Title: "Fix the broken parser", Project: "agent-net", Revision: 1, State: protocol.ChangeOpen, UpdatedAt: now.Add(-3 * time.Hour).Unix(), CreatedAt: now.Add(-4 * time.Hour).Unix()},
		Steps: []protocol.StepStatus{
			step("accept", protocol.StepStatePassed, caller, ""),
			{StepRef: protocol.StepRef{Step: "test"}, State: protocol.StepStateFailed, Owner: merger, Evidence: &protocol.Evidence{StepRef: protocol.StepRef{Step: "test"}, Result: "fail", By: "s_merger", Note: "3 parser tests failed", At: now.Unix()}},
		},
	}
	needs := protocol.ChangeView{
		Change: protocol.Change{ID: "ch_need1", Title: "Add the owner approval", Project: "agent-net", Revision: 1, State: protocol.ChangeOpen, UpdatedAt: now.Add(-5 * time.Hour).Unix(), CreatedAt: now.Add(-6 * time.Hour).Unix()},
		Steps: []protocol.StepStatus{
			step("accept", protocol.StepStatePassed, caller, ""),
			{StepRef: protocol.StepRef{Step: "approve", For: "merge"}, State: protocol.StepStateNeedsOwner, Owner: protocol.StepOwner{Kind: protocol.OwnerPerson}, Evidence: &protocol.Evidence{StepRef: protocol.StepRef{Step: "approve", For: "merge"}, Result: "needs_owner", By: "s_wait", Note: "the owner decides the risky merge", At: now.Unix()}},
		},
	}
	done := protocol.ChangeView{
		Change: protocol.Change{ID: "ch_done1", Title: "Old finished change", Project: "agent-net", Revision: 1, State: protocol.ChangeDone, MergedCommit: "fedcba9876543210fedcba9876543210fedcba98", UpdatedAt: now.Add(-72 * time.Hour).Unix(), CreatedAt: now.Add(-80 * time.Hour).Unix()},
		Steps: []protocol.StepStatus{
			step("accept", protocol.StepStatePassed, caller, ""),
			step("review", protocol.StepStateReady, caller, ""),
			step("verify", protocol.StepStatePassed, caller, "verified on the live system"),
		},
	}
	all := []protocol.ChangeView{flight, failed, needs}
	switch {
	case args.Change != "":
		for _, v := range append(all, done) {
			if v.Change.ID == args.Change {
				return []protocol.ChangeView{v}
			}
		}
		return nil
	case args.State == protocol.ChangeDone:
		return []protocol.ChangeView{done}
	case args.State == protocol.ChangeStopped:
		return nil
	}
	return all
}

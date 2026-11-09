package phone

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// rowOf returns the table row of a change in a page.
func rowOf(t *testing.T, body, id string) string {
	t.Helper()
	i := strings.Index(body, `<tr data-href="/changes?`)
	for i >= 0 {
		end := strings.Index(body[i:], "</tr>")
		if end < 0 {
			break
		}
		row := body[i : i+end]
		if strings.Contains(row, id) {
			return row
		}
		next := strings.Index(body[i+end:], `<tr data-href="/changes?`)
		if next < 0 {
			break
		}
		i += end + next
	}
	t.Fatalf("no row for %s in %s", id, body)
	return ""
}

func TestChangesListRendersPipelinePerStepState(t *testing.T) {
	body := page1(t, newConsoleOperator(), "/changes")
	mustContain(t, "changes", body, "<h1", "Changes", "Push updates part 1", "ch_flight1", "rev 2", "risk 3", "Fix the broken parser", "Add the owner approval")
	// One mark per step, by state: passed, running, left for later.
	flight := rowOf(t, body, "ch_flight1")
	mustContain(t, "flight row", flight, `<i class="p"></i><i class="p"></i><i class="r"></i><i></i>`,
		"accept passed, review passed, test running, merge not yet", "test running", "merger-node", "22m")
	mustContain(t, "failed row", rowOf(t, body, "ch_fail1"), `<i class="f">`, "test failed")
	need := rowOf(t, body, "ch_need1")
	mustContain(t, "needs row", need, `<i class="w">`, "approve needs you")
	mustNotContain(t, "flight row", flight, "risk 0")
	// The change that needs the owner comes first.
	if strings.Index(body, "ch_need1") > strings.Index(body, "ch_flight1") {
		t.Error("the change that needs you should come first")
	}
	// Read only: no form, no step button.
	mustNotContain(t, "changes", body, "<form", ">Approve<", ">Claim<", ">Report<", "<textarea")
	mustNotContain(t, "ids", body, "n_secretnode")
}

func TestChangeDetailShowsStepOwnersAndEvidence(t *testing.T) {
	body := page1(t, newConsoleOperator(), "/changes?s=ch_flight1")
	i := strings.Index(body, `id="detail"`)
	if i < 0 {
		t.Fatal("no detail panel")
	}
	detail := body[i:]
	detail = detail[:strings.Index(detail, "</section>")]
	mustContain(t, "detail", detail, "Push updates part 1", "ch_flight1", "revision 2", "risk 3", "agent-net/w_flight", "01234567",
		"busy-agent", "gpt-6.1", "waiting-agent", // author worker with its model, caller by name
		"accept", "review", "test", "merge", "merger-node", "caller",
		"accepted by reading the diff", "review found nothing", "claude-sonnet-5-5", "running", "waiting")
	// A step that has no evidence yet has no note, and a failed one shows the finding.
	failed := page1(t, newConsoleOperator(), "/changes?s=ch_fail1")
	mustContain(t, "failed detail", failed, "3 parser tests failed", "failed", "Fix the broken parser")
	need := page1(t, newConsoleOperator(), "/changes?s=ch_need1")
	mustContain(t, "needs detail", need, "the owner decides the risky merge", "needs you", "approve for merge")
	// The selected row is marked.
	mustContain(t, "selected row", rowOf(t, body, "ch_flight1"), `aria-selected="true"`)
	// No node IDs; the commit is short.
	mustNotContain(t, "detail", detail, "n_secretnode", "0123456789abcdef0123456789abcdef01234567")
	// No step action on the web.
	mustNotContain(t, "detail", detail, "<form", "<button", "<textarea")
}

func TestChangeDetailMarksStepsAMergedChangeNeverRecorded(t *testing.T) {
	body := page1(t, newConsoleOperator(), "/changes?f=all&s=ch_done1")
	mustContain(t, "done detail", body, "verified on the live system", "not recorded")
}

func TestChangesFilters(t *testing.T) {
	f := newConsoleOperator()
	open := page1(t, f, "/changes?f=open")
	mustContain(t, "open", open, "Push updates part 1", "Fix the broken parser", "Add the owner approval", "3 shown", `aria-current="true"`)
	mustNotContain(t, "open", open, "Old finished change")
	mine := page1(t, f, "/changes?f=mine")
	mustContain(t, "mine", mine, "Add the owner approval", "1 shown")
	mustNotContain(t, "mine", mine, "Push updates part 1", "Fix the broken parser", "Old finished change")
	all := page1(t, f, "/changes?f=all")
	mustContain(t, "all", all, "Push updates part 1", "Old finished change", "4 shown")
	// The default is open, and each filter is a link that keeps the page working without scripts.
	mustContain(t, "filters", open, `href="/changes?f=open"`, `href="/changes?f=mine"`, `href="/changes?f=all"`, ">open<", ">mine to do<", ">all<")
	if page1(t, f, "/changes") != open {
		t.Error("the default filter is open")
	}
	// A row link keeps the filter.
	mustContain(t, "row link", mine, `/changes?f=mine&amp;s=ch_need1`)
}

func TestMessagesListAndThreadRender(t *testing.T) {
	body := page1(t, newConsoleOperator(), "/messages?s=c_3")
	mustContain(t, "list", body, "Conversations", "busy-agent ↔ waiting-agent", "you ↔ waiting-agent", "3 msgs", "2 msgs", "recent conversation line",
		"Fix the flaky test", "worker last line", "worker", "unrelated line", "ago")
	// The CTO chip is on the CTO row only.
	cto := rowBetween(t, body, `<a class="convo`, "cto line about the plan")
	mustContain(t, "cto row", cto, `chip cto`, ">CTO<")
	mustNotContain(t, "other row", rowBetween(t, body, `<a class="convo`, "unrelated line"), "chip cto")
	// The thread reads oldest first, and tells the owner's words from the agent's.
	i := strings.Index(body, `id="thread"`)
	if i < 0 {
		t.Fatal("no thread")
	}
	thread := body[i:]
	a, b := strings.Index(thread, "go ahead and run the build"), strings.Index(thread, "allow the tools?")
	if a < 0 || b < 0 || a > b {
		t.Fatalf("thread order: %d %d", a, b)
	}
	mustContain(t, "thread", thread, `class="msg me"`, "<b>you</b>", `class="msg"`, "<b>waiting-agent</b>", "Your instruction: run the build")
	// The selected row is marked, and the page has the composer for the owner.
	mustContain(t, "composer", thread, `class="composer"`, `name="kind" value="reply"`, `name="id" value="c_3"`, `name="to" value="s_wait"`, "<textarea", "Write as you to waiting-agent", ">Send<")
	mustNotContain(t, "ids", body, "n_secretnode")
}

// rowBetween returns the markup from the last opening before text to the next one.
func rowBetween(t *testing.T, body, open, text string) string {
	t.Helper()
	at := strings.Index(body, text)
	if at < 0 {
		t.Fatalf("%q missing", text)
	}
	start := strings.LastIndex(body[:at], open)
	if start < 0 {
		t.Fatalf("no %q before %q", open, text)
	}
	end := strings.Index(body[at:], "</a>")
	return body[start : at+end]
}

func TestThreadShowsSystemEventsDimmed(t *testing.T) {
	body := page1(t, newConsoleOperator(), "/messages?s=worker:s_w1")
	i := strings.Index(body, `id="thread"`)
	thread := body[i:]
	mustContain(t, "worker thread", thread, `class="msg sys"`, "started", "done", "12m", "3 files")
	// The raw notice header is not shown.
	mustNotContain(t, "worker thread", thread, "[agent-net worker notice]")
	// A worker thread is written to the worker.
	mustContain(t, "composer", thread, `<select name="to"`, `<option value="s_w1">worker-one</option>`)
	if strings.Index(thread, `value="s_w1"`) > strings.Index(thread, `value="s_wait"`) {
		t.Error("the worker is the first recipient")
	}
}

func TestMessagesComposerOffersBothSidesOfAnAgentToAgentThread(t *testing.T) {
	body := page1(t, newConsoleOperator(), "/messages?s=c_1")
	mustContain(t, "select", body, `<select name="to"`, `value="s_busy"`, `value="s_wait"`, "Send")
}

func TestReplyStaysInTheConversationAndAppearsInThread(t *testing.T) {
	f := newConsoleOperator()
	if strings.Contains(page1(t, f, "/messages?s=c_3"), "yes, run it") {
		t.Fatal("reply present before it was sent")
	}
	w := serve(f, replyRequest(url.Values{"kind": {"reply"}, "id": {"c_3"}, "to": {"s_wait"}, "action": {" yes, run it "}}))
	// The owner send carries the conversation id, and the page goes back to the same conversation.
	if w.Code != 303 || w.Header().Get("Location") != "/messages?s=c_3" {
		t.Fatalf("reply: %d %q", w.Code, w.Header().Get("Location"))
	}
	if !f.has("reply:c_3:s_wait:yes, run it") || f.has("send:s_wait:yes, run it") {
		t.Fatalf("the reply did not go with its conversation: %v", f.calls)
	}
	body := page1(t, f, "/messages?s=c_3")
	i := strings.Index(body, `id="thread"`)
	mustContain(t, "thread after reply", body[i:], "yes, run it", `class="msg me"`, "<b>you</b>")
	if strings.Index(body[i:], "yes, run it") < strings.Index(body[i:], "allow the tools?") {
		t.Error("the new reply should be last")
	}
}

// The thread of a worker row groups conversations: the reply goes to a real
// conversation of that group, and the page stays on the worker row.
func TestReplyInAWorkerThreadUsesARealConversation(t *testing.T) {
	f := newConsoleOperator()
	w := serve(f, replyRequest(url.Values{"kind": {"reply"}, "id": {"worker:s_w1"}, "to": {"s_w1"}, "action": {"keep going"}}))
	if w.Code != 303 || w.Header().Get("Location") != "/messages?s=worker%3As_w1" {
		t.Fatalf("worker reply: %d %q", w.Code, w.Header().Get("Location"))
	}
	if !f.has("reply:c_w:s_w1:keep going") {
		t.Fatalf("calls: %v", f.calls)
	}
	body := page1(t, f, "/messages?s=worker:s_w1")
	mustContain(t, "worker thread", body[strings.Index(body, `id="thread"`):], "keep going", `class="msg me"`)
}

func TestReplyIsRefusedWhenItDoesNotBelongToTheConversation(t *testing.T) {
	f := newConsoleOperator()
	for name, v := range map[string]url.Values{
		"session outside the conversation": {"kind": {"reply"}, "id": {"c_3"}, "to": {"s_idle"}, "action": {"hi"}},
		"no recipient":                     {"kind": {"reply"}, "id": {"c_3"}, "action": {"hi"}},
		"unknown conversation":             {"kind": {"reply"}, "id": {"c_nope"}, "to": {"s_wait"}, "action": {"hi"}},
		"empty text":                       {"kind": {"reply"}, "id": {"c_3"}, "to": {"s_wait"}, "action": {"   "}},
		"text too long":                    {"kind": {"reply"}, "id": {"c_3"}, "to": {"s_wait"}, "action": {strings.Repeat("x", protocol.MaxBodyBytes+1)}},
	} {
		if w := serve(f, replyRequest(v)); w.Code != 400 {
			t.Errorf("%s: %d, want 400", name, w.Code)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 0 {
		t.Fatalf("a send went out: %v", f.calls)
	}
}

func TestNoStepActionRouteExists(t *testing.T) {
	f := newConsoleOperator()
	for _, path := range []string{"/changes", "/changes/approve", "/changes/claim", "/changes/report", "/changes/ch_flight1/report", "/changes/ch_flight1/approve", "/step/report", "/step/claim", "/step/approve", "/steps", "/messages/send"} {
		if code := post(t, f, path, url.Values{"id": {"ch_flight1"}}); code != 404 && code != 405 {
			t.Errorf("POST %s: %d, want 404 or 405", path, code)
		}
	}
	for _, kind := range []string{"approve", "claim", "report", "step", "step_report", "step_claim", "change", "merge", "try", "defect"} {
		if code := post(t, f, "/action", url.Values{"kind": {kind}, "id": {"ch_flight1"}, "action": {"pass"}}); code != 400 {
			t.Errorf("kind %s: %d, want 400", kind, code)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 0 {
		t.Fatalf("operator was called: %v", f.calls)
	}
	for method := range f.reads {
		switch method {
		case protocol.OpChangesOwnerReport, "step_report", "step_claim":
			t.Errorf("the web called %s", method)
		}
	}
}

// TestWebActionKindsAreExact holds the list of kinds of POST /action: the five
// asks of part 1, reply, and the CTO panel's send and proposal answer.
func TestWebActionKindsAreExact(t *testing.T) {
	src, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(src), "switch kind {")
	if start < 0 {
		t.Fatal("kind switch not found")
	}
	end := strings.Index(string(src)[start:], "default:")
	got := map[string]bool{}
	for _, m := range regexp.MustCompile(`case "([a-z_]+)":`).FindAllStringSubmatch(string(src)[start:start+end], -1) {
		got[m[1]] = true
	}
	want := map[string]bool{"held": true, "outcome": true, "inbox": true, "mark": true, "send": true, "reply": true, "cto_send": true, "proposal": true}
	if len(got) != len(want) {
		t.Fatalf("action kinds: %v, want %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("kind %q missing", k)
		}
	}
}

func TestEachScreenLoadsOnlyWhatItShows(t *testing.T) {
	f := newConsoleOperator()
	page1(t, f, "/changes?s=ch_flight1")
	f.mu.Lock()
	for _, m := range []string{protocol.OpConversations, protocol.OpConversationMessages, protocol.OpUsage, protocol.OpChiefSorts, protocol.OpDeskLog, protocol.OpGraphView} {
		if f.reads[m] != 0 {
			t.Errorf("the Changes screen read %s", m)
		}
	}
	f.reads = map[string]int{}
	f.mu.Unlock()
	page1(t, f, "/messages?s=c_3")
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range []string{protocol.OpChangesOwnerFind, protocol.OpUsage, protocol.OpChiefSorts, protocol.OpDeskLog, protocol.OpGraphView} {
		if f.reads[m] != 0 {
			t.Errorf("the Messages screen read %s", m)
		}
	}
}

func TestStreamRendersChangesAndMessages(t *testing.T) {
	for _, tc := range []struct {
		path string
		want []string
		not  []string
	}{
		{"/events?screen=changes&f=mine", []string{"event: update", "Add the owner approval", `hx-swap-oob`}, []string{"Push updates part 1"}},
		{"/events?screen=changes&s=ch_fail1", []string{"3 parser tests failed"}, nil},
		{"/events?screen=messages&s=c_3", []string{"event: update", "go ahead and run the build", `class="msg me"`, `class="composer"`}, nil},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		w := &twoFlushes{ResponseRecorder: newRecorder(), cancel: cancel, count: 1}
		New(newConsoleOperator()).ServeHTTP(w, newGet(tc.path).WithContext(ctx))
		cancel()
		mustContain(t, tc.path, w.Body.String(), tc.want...)
		mustNotContain(t, tc.path, w.Body.String(), tc.not...)
	}
}

func TestEmptyScreensSayWhy(t *testing.T) {
	f := newConsoleOperator()
	f.threads = map[string][]protocol.Message{}
	if body := page1(t, f, "/messages?s=c_3"); !strings.Contains(body, "No messages in this conversation yet.") {
		t.Error("an empty thread says so")
	}
}

func newRecorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }
func newGet(path string) *http.Request        { return httptest.NewRequest("GET", path, nil) }
func replyRequest(v url.Values) *http.Request {
	r := httptest.NewRequest("POST", "http://example.test/action", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://example.test")
	return r
}
func serve(f *consoleOperator, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	New(f).ServeHTTP(w, r)
	return w
}

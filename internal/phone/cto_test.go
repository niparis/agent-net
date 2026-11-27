package phone

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCTOPanelRendersHistory(t *testing.T) {
	body := page1(t, newConsoleOperator(), "/?cto=1")
	mustContain(t, "cto panel", body,
		`class="cto"`, `id="ctobody"`, `id="toggleCTO"`,
		"Atlas", "claude · opus-5-5",
		"Good morning, CTO.", "Morning. Two things need you.", "The first is ready.",
		"read graph",
		`name="kind" value="cto_send"`, `name="text"`, "Ask the Atlas in plain words")
	// The CTO acts at once: the panel has no proposal card to approve.
	mustNotContain(t, "cto panel", body, "n_secretnode", ">Apply<", ">Decline<", `value="proposal"`, "Proposals wait for you")
	// The rail and the panel both mark the CTO.
	mustContain(t, "cto title", body, "aria-label=\"Atlas\"")
}

func TestCTOPanelIsClosedUnlessAsked(t *testing.T) {
	body := page1(t, newConsoleOperator(), "/")
	mustContain(t, "closed panel", body, `id="cto" hidden`, `id="ctobody"`)
	mustNotContain(t, "closed panel", body, "Good morning, CTO.")
}

func TestCTOSendPostsThroughOpCTOSend(t *testing.T) {
	f := newConsoleOperator()
	code := post(t, f, "/action", url.Values{
		"kind": {"cto_send"}, "text": {"Please look at the merge."}, "back": {"/?cto=1"},
	})
	if code != 303 {
		t.Fatalf("send status %d", code)
	}
	if !f.has("cto_send:Please look at the merge.") {
		t.Fatalf("operator not called: %v", f.calls)
	}
	body := page1(t, f, "/?cto=1")
	mustContain(t, "after send", body, "Please look at the merge.")
	// An empty message is refused and calls nothing.
	g := newConsoleOperator()
	if code := post(t, g, "/action", url.Values{"kind": {"cto_send"}, "text": {"   "}}); code != 400 {
		t.Fatalf("empty send: %d", code)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.calls) != 0 {
		t.Fatalf("empty send called the operator: %v", g.calls)
	}
}

func TestCTOFormsReturnToThePanelOnThisConsole(t *testing.T) {
	for _, tc := range []struct{ back, want string }{
		{"/sessions?cto=1", "/sessions?cto=1"},
		{"//evil.test/", "/"},
		{"https://evil.test/", "/"},
		{`/\evil.test`, "/"},
	} {
		f := newConsoleOperator()
		w := httptest.NewRecorder()
		v := url.Values{"kind": {"cto_send"}, "text": {"hello"}, "back": {tc.back}}
		r := httptest.NewRequest("POST", "http://example.test/action", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "http://example.test")
		New(f).ServeHTTP(w, r)
		if w.Code != 303 || w.Header().Get("Location") != tc.want {
			t.Errorf("back %q: %d %q, want 303 %q", tc.back, w.Code, w.Header().Get("Location"), tc.want)
		}
	}
}

func TestCTOPanelReadsOlderHistory(t *testing.T) {
	f := newConsoleOperator()
	body := page1(t, f, "/?cto=1")
	mustContain(t, "latest page", body, "Older messages", "?cto=1&amp;h=120")
	body = page1(t, f, "/?cto=1&h=120")
	mustContain(t, "older page", body, "Latest")
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ctoIndexes) == 0 || f.ctoIndexes[len(f.ctoIndexes)-1] != -120 {
		t.Fatalf("older request indexes: %v", f.ctoIndexes)
	}
}

func TestCTOStreamSendsThePanelSection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w := &cancelFlusher{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	New(newConsoleOperator()).ServeHTTP(w, httptest.NewRequest("GET", "/events?section=cto&screen=sessions&cto=1", nil).WithContext(ctx))
	body := w.Body.String()
	mustContain(t, "cto stream", body, "event: cto", "Good morning, CTO.", `id="ctoform"`)
	// The screen section is a separate event, so the two update independently.
	mustNotContain(t, "cto stream", body, "event: update", "Needs you")
}

func TestCTOModelTextIsEscaped(t *testing.T) {
	f := newConsoleOperator()
	now := time.Now().UnixMilli()
	f.ctoEvents.Messages = append(f.ctoEvents.Messages, json.RawMessage(`{"type":"assistant","text":"<script>alert(1)</script>\n\n**bold** starts a line","time":{"created":`+jsonNumber(now)+`}}`))
	body := page1(t, f, "/?cto=1")
	// The model's text is text: no HTML, and its line breaks stay.
	mustContain(t, "escaped", body, "&lt;script&gt;alert(1)&lt;/script&gt;", "**bold** starts a line")
	mustNotContain(t, "escaped", body, "<script>alert(1)</script>")
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestCTOActionsAreNotThePanelsOwn(t *testing.T) {
	// No route or action kind can start a worker or change a setting from the
	// panel: the CTO acts, and the panel only sends text. A proposal answer is
	// refused too: the CTO has no stored proposals.
	f := newConsoleOperator()
	for _, kind := range []string{"proposal", "worker_start", "worker_stop", "charter_set", "settings", "cto_new", "cto_settings", "session_start"} {
		if code := post(t, f, "/action", url.Values{"kind": {kind}, "id": {"x"}, "action": {"y"}}); code != 400 {
			t.Errorf("kind %s: %d, want 400", kind, code)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 0 {
		t.Fatalf("operator was called: %v", f.calls)
	}
	if strings.Contains(page1(t, newConsoleOperator(), "/?cto=1"), `action="/cto`) {
		t.Error("the panel posts to a route other than /action")
	}
}

package phone

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// proposalNode is how the page carries the machine that holds a proposal
// without printing a raw node ID.
func proposalNode(id string) string { return base64.RawURLEncoding.EncodeToString([]byte(id)) }

func TestCTOPanelRendersHistoryAndProposals(t *testing.T) {
	body := page1(t, newConsoleOperator(), "/?cto=1")
	mustContain(t, "cto panel", body,
		`class="cto"`, `id="ctobody"`, `id="toggleCTO"`,
		"Atlas", "claude · opus-5-5",
		"Good morning, CTO.", "Morning. Two things need you.", "The first is ready.",
		"read graph",
		"charter forge lead_merges", "Turn on lead merges for forge.",
		">Apply<", ">Decline<", `name="kind" value="proposal"`, `name="id" value="p_1"`,
		`name="kind" value="cto_send"`, `name="text"`, "Ask the Atlas in plain words")
	// A proposal has an owner action, but no node ID is printed.
	mustNotContain(t, "cto panel", body, "n_secretnode")
	// The rail and the panel both mark the CTO.
	mustContain(t, "cto title", body, "aria-label=\"Atlas\"")
}

func TestCTOPanelIsClosedUnlessAsked(t *testing.T) {
	body := page1(t, newConsoleOperator(), "/")
	mustContain(t, "closed panel", body, `id="cto" hidden`, `id="ctobody"`)
	mustNotContain(t, "closed panel", body, "Good morning, CTO.", "charter forge lead_merges")
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

func TestCTOProposalApplyAndDeclineUseTheOwnerAction(t *testing.T) {
	for _, tc := range []struct{ action, want string }{{"a", "proposal:n_secretnode1:p_1:a"}, {"x", "proposal:n_secretnode1:p_1:x"}} {
		f := newConsoleOperator()
		code := post(t, f, "/action", url.Values{
			"kind": {"proposal"}, "id": {"p_1"}, "node": {proposalNode("n_secretnode1")}, "action": {tc.action}, "back": {"/?cto=1"},
		})
		if code != 303 {
			t.Fatalf("%s: status %d", tc.action, code)
		}
		if !f.has(tc.want) {
			t.Fatalf("%s: calls %v", tc.action, f.calls)
		}
		// The card is gone after the decision.
		mustNotContain(t, "after "+tc.action, page1(t, f, "/?cto=1"), "charter forge lead_merges")
	}
	// Only Apply and Decline are owner actions.
	f := newConsoleOperator()
	if code := post(t, f, "/action", url.Values{"kind": {"proposal"}, "id": {"p_1"}, "action": {"maybe"}}); code != 400 {
		t.Fatalf("invalid proposal action: %d", code)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 0 {
		t.Fatalf("invalid proposal action called the operator: %v", f.calls)
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
	New(newConsoleOperator()).ServeHTTP(w, httptest.NewRequest("GET", "/events?section=cto&screen=desk&cto=1", nil).WithContext(ctx))
	body := w.Body.String()
	mustContain(t, "cto stream", body, "event: cto", "Good morning, CTO.", ">Apply<", `id="ctoform"`)
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

func TestCTOProposalSummaryPrintsNoNodeOrSessionID(t *testing.T) {
	for _, p := range []protocol.CTOProposal{
		{Tool: "session_change", Action: "session_start", Args: json.RawMessage(`{"node_id":"n_secretnode1","project":"agent-net","harness":"claude","folder":"/work"}`)},
		{Tool: "project_change", Action: "create", Args: json.RawMessage(`{"node_id":"n_secretnode1","project":"x","path":"/x"}`)},
		{Tool: "project_remove", Args: json.RawMessage(`{"node_id":"n_secretnode1","name":"x"}`)},
		{Tool: "machine_remove", Args: json.RawMessage(`{"node_id":"n_secretnode1"}`)},
		{Tool: "machine_rotate", Args: json.RawMessage(`{"node_id":"n_secretnode1"}`)},
		{Tool: "node_rename", Args: json.RawMessage(`{"node_id":"n_secretnode1","name":"alpha"}`)},
		{Tool: "session_change", Action: "session_stop", Args: json.RawMessage(`{"session_id":"s_secretsession"}`)},
	} {
		summary := proposalSummary(p)
		if strings.Contains(summary, "n_secretnode1") || strings.Contains(summary, "s_secretsession") {
			t.Errorf("%s %s prints an ID: %q", p.Tool, p.Action, summary)
		}
	}
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestCTOActionsAreNotThePanelsOwn(t *testing.T) {
	// No route or action kind can start a worker or change a setting from the
	// panel: the CTO acts, and the panel only sends text and answers proposals.
	f := newConsoleOperator()
	for _, kind := range []string{"worker_start", "worker_stop", "charter_set", "settings", "cto_new", "cto_settings", "session_start"} {
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

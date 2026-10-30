package phone

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

type fakeOperator struct{ calls []string }

func (f *fakeOperator) Snapshot(context.Context) (operator.Snapshot, error) {
	return operator.Snapshot{Connected: true, Nodes: []protocol.NodeView{{Hostname: "test-machine"}}, Agents: []protocol.AgentView{{SessionID: "s_1", Harness: "codex", Profile: protocol.Profile{Name: "test-agent", Task: "test-task"}}}, Held: []protocol.Message{{ID: "m_1", Body: "held request"}}, Outcomes: []protocol.OutcomeInboxEntry{{EdgeOutcome: protocol.EdgeOutcome{ReplyID: "r_1"}, Request: "outcome request"}}}, nil
}
func (f *fakeOperator) SessionText(context.Context, protocol.TextQuery) (protocol.TextPage, error) {
	return protocol.TextPage{Entries: []protocol.TextEntry{{Text: "conversation text"}}}, nil
}
func (f *fakeOperator) AnswerHeld(_ context.Context, q protocol.HeldAnswer) error {
	f.calls = append(f.calls, "held:"+q.Action)
	return nil
}
func (f *fakeOperator) ResolveOutcome(_ context.Context, id, action string) error {
	f.calls = append(f.calls, "outcome:"+action)
	return nil
}
func (f *fakeOperator) SendAsOwner(_ context.Context, to, body string) (protocol.MessageSent, error) {
	f.calls = append(f.calls, "send:"+body)
	return protocol.MessageSent{}, nil
}
func (f *fakeOperator) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	if method == "inbox_list" {
		return json.RawMessage(`{"items":[{"id":"i_1","status":"open","text":"inbox request"}]}`), nil
	}
	f.calls = append(f.calls, method)
	return nil, nil
}
func TestPagesAndActions(t *testing.T) {
	f := &fakeOperator{}
	h := New(f)
	for _, tc := range []struct{ path, want string }{{"/", "test-machine"}, {"/", "held request"}, {"/", "outcome request"}, {"/", "inbox request"}, {"/session/s_1", "conversation text"}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%s: %d missing %q", tc.path, w.Code, tc.want)
		}
	}
	for _, tc := range []struct{ kind, id, action, want string }{{"held", "m_1", "a", "held:a"}, {"held", "m_1", "r", "held:r"}, {"outcome", "r_1", "let_stand", "outcome:let_stand"}, {"outcome", "r_1", "overrule", "outcome:overrule"}, {"inbox", "i_1", "approve", "inbox_answer"}, {"inbox", "i_1", "refuse", "inbox_answer"}, {"inbox", "i_1", "custom", "inbox_answer"}, {"send", "s_1", "hello", "send:hello"}} {
		v := url.Values{"kind": {tc.kind}, "id": {tc.id}, "action": {tc.action}}
		r := httptest.NewRequest("POST", "http://example.test/action", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "http://example.test")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 303 || len(f.calls) == 0 || f.calls[len(f.calls)-1] != tc.want {
			t.Errorf("%s: %d calls %v", tc.want, w.Code, f.calls)
		}
	}
}

func TestActionRejectsCrossOrigin(t *testing.T) {
	f := &fakeOperator{}
	r := httptest.NewRequest("POST", "http://example.test/action", strings.NewReader("kind=held&id=m_1&action=a"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://attacker.test")
	w := httptest.NewRecorder()
	New(f).ServeHTTP(w, r)
	if w.Code != 403 || len(f.calls) != 0 {
		t.Fatalf("cross-origin action: %d, %v", w.Code, f.calls)
	}
}

func TestUnavailableDaemonIsVisibleInLiveSections(t *testing.T) {
	var b strings.Builder
	if err := shell.ExecuteTemplate(&b, "sections", page{Error: "daemon not reachable"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "daemon not reachable") {
		t.Fatal("missing connection error")
	}
}

func TestSessionsPreferRepositoryProjectAndWaitingIsNotEmpty(t *testing.T) {
	p := page{Snapshot: operator.Snapshot{Connected: true, Agents: []protocol.AgentView{{SessionID: "s_1", Harness: "codex", Repo: protocol.RepoInfo{Project: "repo-project"}, Waiting: &protocol.Waiting{Text: "Need an answer"}}}}}
	var b strings.Builder
	if err := shell.ExecuteTemplate(&b, "sections", p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "repo-project") || strings.Contains(b.String(), "No requests to review") {
		t.Fatal(b.String())
	}
}

func TestHeldActionRejectsCompoundValue(t *testing.T) {
	f := &fakeOperator{}
	v := url.Values{"kind": {"held"}, "id": {"m_1"}, "action": {"a|A"}}
	r := httptest.NewRequest("POST", "http://example.test/action", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://example.test")
	w := httptest.NewRecorder()
	New(f).ServeHTTP(w, r)
	if w.Code != 400 || len(f.calls) != 0 {
		t.Fatalf("invalid action: %d, %v", w.Code, f.calls)
	}
}

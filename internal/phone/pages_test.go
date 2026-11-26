package phone

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

type fakeOperator struct{ calls []string }

func (f *fakeOperator) Snapshot(context.Context) (operator.Snapshot, error) {
	return operator.Snapshot{Connected: true, Nodes: []protocol.NodeView{{Hostname: "test-machine"}}, Agents: []protocol.AgentView{{SessionID: "s_1", Harness: "codex", Profile: protocol.Profile{Name: "test-agent", Task: "test-task"}}}}, nil
}
func (f *fakeOperator) SessionText(context.Context, protocol.TextQuery) (protocol.TextPage, error) {
	return protocol.TextPage{Entries: []protocol.TextEntry{{Text: "conversation text"}}}, nil
}
func (f *fakeOperator) SendAsOwner(_ context.Context, to, body string) (protocol.MessageSent, error) {
	f.calls = append(f.calls, "send:"+body)
	return protocol.MessageSent{}, nil
}
func (f *fakeOperator) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if raw, ok := snapshotFixtureRead(ctx, f, method); ok {
		return raw, nil
	}
	f.calls = append(f.calls, method)
	return nil, nil
}
func TestPagesAndActions(t *testing.T) {
	f := &fakeOperator{}
	h := New(f)
	for _, tc := range []struct{ path, want string }{{"/", "test-machine"}, {"/sessions", "test-machine"}, {"/sessions?s=s_1", "test-task"}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%s: %d missing %q", tc.path, w.Code, tc.want)
		}
	}
}

func TestActionRejectsCrossOrigin(t *testing.T) {
	f := &fakeOperator{}
	r := httptest.NewRequest("POST", "http://example.test/action", strings.NewReader("kind=reply&id=c_1&action=hi"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://attacker.test")
	w := httptest.NewRecorder()
	New(f).ServeHTTP(w, r)
	if w.Code != 403 || len(f.calls) != 0 {
		t.Fatalf("cross-origin action: %d, %v", w.Code, f.calls)
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

// fakeOp is an operator that records what the commands ask.
type fakeOp struct {
	start    protocol.WorkerStartRequest
	stopped  string
	sentTo   string
	sentMsg  string
	workersQ protocol.WorkersQuery
	err      error
	workers  []protocol.WorkerView
	result   protocol.WorkerStartResult
	usage    protocol.UsageResult
	// usageCalls counts the usage reads, which cost a trip to the gateway.
	usageCalls int
}

func (f *fakeOp) Snapshot(context.Context) (operator.Snapshot, error) {
	return operator.Snapshot{}, nil
}
func (f *fakeOp) Events(context.Context, int64, int) ([]protocol.FeedEvent, error) {
	return nil, nil
}
func (f *fakeOp) Conversations(context.Context, protocol.QueryConversations) (protocol.ConversationPage, error) {
	return protocol.ConversationPage{}, nil
}
func (f *fakeOp) ConversationMessages(context.Context, string) ([]protocol.Message, error) {
	return nil, nil
}
func (f *fakeOp) Message(context.Context, string) (protocol.Message, error) {
	return protocol.Message{}, nil
}
func (f *fakeOp) SendAsOwner(_ context.Context, to, body string) (protocol.MessageSent, error) {
	f.sentTo, f.sentMsg = to, body
	return protocol.MessageSent{Message: &protocol.Message{ID: "m_1", To: to, State: "stored"}}, f.err
}
func (f *fakeOp) StartWorker(_ context.Context, r protocol.WorkerStartRequest) (protocol.WorkerStartResult, error) {
	f.start = r
	return f.result, f.err
}
func (f *fakeOp) SessionStop(_ context.Context, s string) (string, error) {
	f.stopped = s
	return "Stopped.", f.err
}
func (f *fakeOp) StopWorker(_ context.Context, s string) error { f.stopped = s; return f.err }
func (f *fakeOp) SessionStart(context.Context, protocol.SessionStartRequest) (protocol.StartedSession, error) {
	return protocol.StartedSession{}, f.err
}
func (f *fakeOp) SessionResume(context.Context, protocol.SessionResumeRequest) (protocol.StartedSession, error) {
	return protocol.StartedSession{}, f.err
}
func (f *fakeOp) SessionStartGet(context.Context, string) (protocol.StartedSession, error) {
	return protocol.StartedSession{}, f.err
}
func (f *fakeOp) Workers(_ context.Context, q protocol.WorkersQuery) ([]protocol.WorkerView, error) {
	f.workersQ = q
	return f.workers, f.err
}
func (f *fakeOp) Usage(context.Context) (protocol.UsageResult, error) {
	f.usageCalls++
	return f.usage, nil
}
func (f *fakeOp) UsageTop(context.Context, string) (protocol.UsageTop, error) {
	return protocol.UsageTop{}, nil
}
func (f *fakeOp) GraphView(context.Context, protocol.QueryGraph) (protocol.GraphView, error) {
	return protocol.GraphView{}, nil
}
func (f *fakeOp) SessionText(context.Context, protocol.TextQuery) (protocol.TextPage, error) {
	return protocol.TextPage{}, nil
}
func (f *fakeOp) SessionEnter(context.Context, protocol.EnterRequest) (protocol.EnterPlan, error) {
	return protocol.EnterPlan{}, nil
}
func (f *fakeOp) Counts(context.Context) (protocol.SessionCounts, error) {
	return protocol.SessionCounts{}, nil
}
func (f *fakeOp) SetPeerTrust(context.Context, string, bool) error { return nil }
func (f *fakeOp) SetSessionPolicy(context.Context, protocol.SessionPolicyRequest) (protocol.SessionPolicyResult, error) {
	return protocol.SessionPolicyResult{}, nil
}
func (f *fakeOp) RenameSession(context.Context, string, string) (protocol.SessionRenameResult, error) {
	return protocol.SessionRenameResult{}, nil
}
func (f *fakeOp) MachineAction(context.Context, string, string) error { return nil }
func (f *fakeOp) RenameNode(context.Context, string, string) (protocol.NodeRenameResult, error) {
	return protocol.NodeRenameResult{}, nil
}
func (f *fakeOp) Integrations(context.Context) (protocol.IntegrationsResult, error) {
	return protocol.IntegrationsResult{}, nil
}
func (f *fakeOp) SetIntegrations(context.Context, protocol.Integrations) (protocol.IntegrationsResult, error) {
	return protocol.IntegrationsResult{}, nil
}
func (f *fakeOp) HiddenItems(context.Context) (protocol.HiddenItems, error) {
	return protocol.HiddenItems{}, nil
}
func (f *fakeOp) SetHiddenItems(context.Context, protocol.HiddenItems) (protocol.HiddenItems, error) {
	return protocol.HiddenItems{}, nil
}
func (f *fakeOp) Close() {}

func TestStartCommand(t *testing.T) {
	f := &fakeOp{result: protocol.WorkerStartResult{SessionID: "s_w", Harness: "opencode", Model: "a/b", Project: "shop", Hostname: "mac",
		Worktree: "/wt", Branch: "agent-net/w_1", Task: &protocol.Message{ID: "m_9"}}}
	var out bytes.Buffer
	err := runStart(context.Background(), f, &out, []string{"--touches", "tests", "--project", "shop", "--harness", "opencode", "--model", "a/b",
		"--isolation", "worktree", "write", "the", "tests", "--machine", "mac"})
	if err != nil {
		t.Fatal(err)
	}
	want := protocol.WorkerStartRequest{Project: "shop", Machine: "mac", Harness: "opencode", Model: "a/b", Task: "write the tests", Isolation: "worktree", Touches: []string{"tests"}}
	if !reflect.DeepEqual(f.start, want) {
		t.Errorf("request %+v, want %+v", f.start, want)
	}
	for _, s := range []string{"started worker s_w (opencode, a/b, project shop on mac)", "worktree /wt on branch agent-net/w_1",
		"task sent as m_9", "agent-net stop s_w"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("output lacks %q:\n%s", s, out.String())
		}
	}
	// The harness defaults to opencode.
	f2 := &fakeOp{}
	runStart(context.Background(), f2, &bytes.Buffer{}, []string{"--touches", "other", "--project", "shop", "do it"})
	if f2.start.Harness != "opencode" || f2.start.Task != "do it" {
		t.Errorf("defaults: %+v", f2.start)
	}
	for _, args := range [][]string{{"do it"}, {"--project", "shop"}} {
		if err := runStart(context.Background(), &fakeOp{}, &bytes.Buffer{}, args); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("%v: %v", args, err)
		}
	}
	// A refusal comes back as the error.
	err = runStart(context.Background(), &fakeOp{err: errors.New("machine mac is offline")}, &bytes.Buffer{}, []string{"--touches", "other", "--project", "shop", "x"})
	if err == nil || err.Error() != "machine mac is offline" {
		t.Errorf("error: %v", err)
	}
}

func TestStopSendAndWorkersCommands(t *testing.T) {
	f := &fakeOp{workers: []protocol.WorkerView{{SessionID: "s_w", Hostname: "mac", Project: "shop", Harness: "opencode", Online: true,
		Status: "busy", OwnerName: "you (on mac)", StartedAt: 1}}}
	var out bytes.Buffer
	if err := runStop(context.Background(), f, &out, []string{"s_w"}); err != nil || f.stopped != "s_w" || !strings.Contains(out.String(), "stopped worker s_w") {
		t.Errorf("stop: %v %q %q", err, f.stopped, out.String())
	}
	if err := runStop(context.Background(), f, &out, nil); err == nil {
		t.Error("stop without a session")
	}
	out.Reset()
	if err := runSend(context.Background(), f, &out, []string{"s_x", "please", "stop"}); err != nil || f.sentTo != "s_x" || f.sentMsg != "please stop" ||
		!strings.Contains(out.String(), "sent m_1 to s_x as you") {
		t.Errorf("send: %v %q %q %q", err, f.sentTo, f.sentMsg, out.String())
	}
	out.Reset()
	if err := runWorkers(context.Background(), f, &out, nil); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"SESSION", "OWNER", "s_w", "shop", "you (on mac)", "busy"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("workers lacks %q:\n%s", s, out.String())
		}
	}
	out.Reset()
	runWorkers(context.Background(), &fakeOp{}, &out, nil)
	if strings.TrimSpace(out.String()) != "no workers" {
		t.Errorf("empty: %q", out.String())
	}
}

func TestWorkersCommandOwnerAndRecentFlags(t *testing.T) {
	f := &fakeOp{workers: []protocol.WorkerView{
		{SessionID: "s_w_ab12", Harness: "claude", Model: "anthropic/claude-sonnet-5", Project: "shop", Status: "busy",
			Owner: "s_owner", StartedAt: 1700000000, Task: "write the tests"},
		{SessionID: "s_w_cd34", Harness: "opencode", Online: false, Status: "done", Owner: "s_owner",
			StartedAt: 1690000000, Task: "fix the parser", EndedAt: 1700000100, EndStatus: "done", EndNote: "the worker reported done"},
	}}
	var out bytes.Buffer
	if err := runWorkers(context.Background(), f, &out, []string{"-json", "-recent", "-owner-claude", "11111111-2222-3333-4444-555555555555"}); err != nil {
		t.Fatal(err)
	}
	want := protocol.WorkersQuery{OwnerClaude: "11111111-2222-3333-4444-555555555555", Recent: true}
	if f.workersQ != want {
		t.Errorf("query %+v, want %+v", f.workersQ, want)
	}
	for _, s := range []string{`"task": "write the tests"`, `"task": "fix the parser"`, `"ended_at": 1700000100`,
		`"end_status": "done"`, `"end_note": "the worker reported done"`} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("JSON lacks %q:\n%s", s, out.String())
		}
	}
	// Without the flags the query is empty: the table and the TUI do not change.
	out.Reset()
	f.workersQ = protocol.WorkersQuery{OwnerClaude: "old", Recent: true}
	if err := runWorkers(context.Background(), f, &out, nil); err != nil {
		t.Fatal(err)
	}
	if f.workersQ != (protocol.WorkersQuery{}) {
		t.Errorf("default query %+v", f.workersQ)
	}
	if strings.Contains(out.String(), "TASK") || strings.Contains(out.String(), "END") {
		t.Errorf("the table has a new column:\n%s", out.String())
	}
}

func TestWorkersJSONAlwaysHasATitle(t *testing.T) {
	f := &fakeOp{workers: []protocol.WorkerView{
		{SessionID: "s_w_ab12", Name: "shop worker 1", Harness: "claude", Project: "shop", Online: true, Status: "busy",
			StartedAt: 1700000000, Task: "Repo: shop (Go). Base: main. Read AGENTS.md first.\n\nFix the parser crash."},
		{SessionID: "s_w_cd34", Name: "shop worker 2", Harness: "opencode", Project: "shop", Online: true, Status: "busy",
			StartedAt: 1700000000, Title: "Parser fix", Task: "Fix the parser crash."},
		{SessionID: "s_w_ef56", Name: "shop worker 3", Harness: "opencode", Project: "shop", Online: true, Status: "busy",
			StartedAt: 1700000000, Task: "Repo: shop\nBase: main\nRead AGENTS.md first."},
	}}
	var out bytes.Buffer
	if err := runWorkers(context.Background(), f, &out, []string{"-json"}); err != nil {
		t.Fatal(err)
	}
	var got protocol.WorkersResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Workers) != 3 {
		t.Fatalf("%d workers", len(got.Workers))
	}
	want := []string{"Fix the parser crash.", "Parser fix", "shop worker 3"}
	for i, title := range want {
		if got.Workers[i].Title != title {
			t.Errorf("worker %d title = %q, want %q", i, got.Workers[i].Title, title)
		}
	}
}

func TestProtectProvidersEdit(t *testing.T) {
	p := protocol.ProtectedProviders{Providers: []string{"anthropic"}, Reason: "keep"}
	got, err := editProtectedProviders(p, "protect", []string{"openai", "anthropic"})
	if err != nil || len(got.Providers) != 2 || got.Reason != "keep" {
		t.Fatalf("protect: %+v %v", got, err)
	}
	got, err = editProtectedProviders(got, "unprotect", []string{"anthropic"})
	if err != nil || len(got.Providers) != 1 || got.Providers[0] != "openai" {
		t.Fatalf("unprotect: %+v %v", got, err)
	}
}

func (f *fakeOp) ProjectDisplayName(context.Context, string) (protocol.ProjectDisplayResult, error) {
	return protocol.ProjectDisplayResult{}, nil
}
func (f *fakeOp) SetProjectDisplayName(context.Context, string, string) (protocol.ProjectDisplayResult, error) {
	return protocol.ProjectDisplayResult{}, nil
}

func TestStartCommandReportsStarting(t *testing.T) {
	f := &fakeOp{result: protocol.WorkerStartResult{Status: "starting", SessionID: "s_w", Harness: "opencode", Project: "shop", Hostname: "mac", Worktree: "/wt", Branch: "agent-net/w_1"}}
	var out bytes.Buffer
	if err := runStart(context.Background(), f, &out, []string{"--touches", "other", "--project", "shop", "work"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "starting worker s_w") || !strings.Contains(out.String(), "notice") {
		t.Fatalf("output: %s", out.String())
	}
}

func TestStartWithoutTouches(t *testing.T) {
	f := &fakeOp{}
	if err := runStart(context.Background(), f, &bytes.Buffer{}, []string{"--project", "shop", "work"}); err != nil {
		t.Fatalf("start without touches: %v", err)
	}
	if f.start.Project != "shop" || f.start.Task != "work" || len(f.start.Touches) != 0 {
		t.Fatalf("request: %+v", f.start)
	}
}

func TestWorkersJSONIncludesAssessment(t *testing.T) {
	op := &fakeOp{workers: []protocol.WorkerView{{SessionID: "s_test", Assessment: &protocol.WorkerAssessment{Reasoning: 2, Scope: 1, Coupling: 1, Uncertainty: 1, Verification: 1, Risk: 1, D: 1.2, Label: "trivial", RulesVersion: "2026-11-02.1", PickerLabel: "normal"}}}}
	var out bytes.Buffer
	if err := runWorkers(context.Background(), op, &out, []string{"-json"}); err != nil {
		t.Fatal(err)
	}
	var result protocol.WorkersResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Workers) != 1 || result.Workers[0].Assessment == nil || result.Workers[0].Assessment.PickerLabel != "normal" || result.Workers[0].Assessment.Reasoning != 2 {
		t.Fatalf("output: %s", out.String())
	}
}

func TestWorkersJSONPanelVersion(t *testing.T) {
	var out bytes.Buffer
	if err := runWorkers(context.Background(), &fakeOp{}, &out, []string{"-json"}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if v, ok := got["panel_version"].(string); !ok || v == "" {
		t.Fatalf("missing panel_version: %s", out.String())
	}
}

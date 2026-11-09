package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

const deliveryCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

var deliveryCalls = map[string]string{
	"agent_change_submit": `{"branch":"agent-net/w_1","commit":"` + deliveryCommit + `"}`,
	"agent_step_claim":    `{"change":"ch_1","step":"test","commit":"` + deliveryCommit + `"}`,
	"agent_step_report":   `{"change":"ch_1","step":"test","result":"pass","commit":"` + deliveryCommit + `","fields":{"suite":"changed"}}`,
	"agent_changes_find":  `{"state":"active"}`,
}

func deliveryCall(t *testing.T, cs *mcp.ClientSession, name, args string) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(args)})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func deliveryText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func deliveryView() protocol.ChangeView {
	c := protocol.Change{ID: "ch_1", Project: "demo", Branch: "agent-net/w_1", HeadCommit: deliveryCommit, Revision: 1,
		Origin: protocol.ChangeOrigin{Kind: protocol.OriginSession, SessionID: "s_me"}, State: protocol.ChangeOpen, CreatedAt: 1}
	return protocol.ChangeView{Change: c, Steps: protocol.PipelineToday().Status(c, nil)}
}

// Each delivery tool checks its arguments against the schema, sends them as
// they are to its daemon method, and returns the daemon's result as its
// structured output.
func TestDeliveryToolsCallTheDaemon(t *testing.T) {
	view := deliveryView()
	ev := protocol.Evidence{ChangeID: "ch_1", Revision: 1, StepRef: protocol.StepRef{Step: protocol.StepTest}, Result: protocol.StepResultPass, Commit: deliveryCommit, By: "s_me", At: 2}
	results := map[string]any{
		protocol.OpChangeSubmit: protocol.ChangeSubmitResult{Change: view},
		protocol.OpStepClaim:    protocol.StepClaimResult{Claimed: true, SessionID: "s_me"},
		protocol.OpStepReport:   protocol.StepReportResult{Evidence: ev, Change: view},
		protocol.OpChangesFind:  protocol.ChangesFindResult{Items: []protocol.ChangeView{view}, Total: 1, NextCursor: "ch_1"},
	}
	var mu sync.Mutex
	got := map[string]map[string]any{}
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		mu.Lock()
		got[method] = m
		mu.Unlock()
		res, ok := results[method]
		if !ok {
			return json.RawMessage(`{}`), nil
		}
		return json.Marshal(res)
	})
	listed := listedCatalogNames(t, cs)
	ops := map[string]string{"agent_change_submit": protocol.OpChangeSubmit, "agent_step_claim": protocol.OpStepClaim, "agent_step_report": protocol.OpStepReport, "agent_changes_find": protocol.OpChangesFind}
	for name, args := range deliveryCalls {
		if !deliveryHas(listed, name) {
			t.Errorf("agent does not see %s", name)
			continue
		}
		res := deliveryCall(t, cs, name, args)
		if res.IsError {
			t.Errorf("%s: %s", name, deliveryText(res))
			continue
		}
		if res.StructuredContent == nil {
			t.Errorf("%s has no structured result", name)
		}
		var want map[string]any
		_ = json.Unmarshal([]byte(args), &want)
		mu.Lock()
		sent := got[ops[name]]
		mu.Unlock()
		if sent == nil {
			t.Errorf("%s did not call %s", name, ops[name])
			continue
		}
		for k, v := range want {
			if a, b := mustJSON(sent[k]), mustJSON(v); a != b {
				t.Errorf("%s sent %s=%s, want %s", name, k, a, b)
			}
		}
	}
	if !strings.Contains(deliveryText(deliveryCall(t, cs, "agent_changes_find", `{}`)), `"next_cursor":"ch_1"`) {
		t.Error("agent_changes_find does not return the daemon's page")
	}

	// The schema still checks the arguments first.
	mu.Lock()
	got = map[string]map[string]any{}
	mu.Unlock()
	res := deliveryCall(t, cs, "agent_step_report", `{"change":"ch_1","step":"check","result":"pass","commit":"`+deliveryCommit+`"}`)
	if !res.IsError {
		t.Errorf("bad step accepted: %q", deliveryText(res))
	}
	res = deliveryCall(t, cs, "agent_change_submit", `{}`)
	if !res.IsError {
		t.Errorf("submit without a branch accepted: %q", deliveryText(res))
	}
	mu.Lock()
	if len(got) != 0 {
		t.Errorf("bad arguments reached the daemon: %v", got)
	}
	mu.Unlock()
}

// An error of the daemon or the gateway reaches the agent as the tool's error.
func TestDeliveryToolsReturnDaemonErrors(t *testing.T) {
	cs := connect(t, func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		return nil, errors.New(method + ": commit is not the head of change ch_1")
	})
	for name, args := range deliveryCalls {
		res := deliveryCall(t, cs, name, args)
		if !res.IsError || !strings.Contains(deliveryText(res), "commit is not the head of change ch_1") {
			t.Errorf("%s = %v %q", name, res.IsError, deliveryText(res))
		}
	}
}

func TestDeliveryToolsForWorkerAndCTO(t *testing.T) {
	worker := listedCatalogNames(t, connectWorker(t))
	if !deliveryHas(worker, "agent_changes_find") || deliveryHas(worker, "agent_change_submit") || deliveryHas(worker, "agent_step_claim") || deliveryHas(worker, "agent_step_report") {
		t.Errorf("worker delivery tools = %v", worker)
	}
	// The CTO's tool list is at its schema budget, so it has none of them yet.
	names := listedCatalogNames(t, ctoTestSession(t))
	for name := range deliveryCalls {
		if deliveryHas(names, name) {
			t.Errorf("CTO sees %s", name)
		}
	}
}

// The catalog output schemas are the protocol's result types, so the daemon
// part can return them as they are.
func TestDeliveryOutputSchemasMatchProtocol(t *testing.T) {
	c := protocol.Change{ID: "ch_1", Project: "demo", Machine: "n_1", Branch: "agent-net/w_1", TargetBranch: "main", BaseCommit: deliveryCommit, HeadCommit: deliveryCommit, Uncommitted: true, Revision: 1,
		Origin: protocol.ChangeOrigin{Kind: protocol.OriginWorker, WorkerID: "w_1", SessionID: "s_1", Model: "deepseek/deepseek-v4-pro", ModelFamily: "deepseek"},
		Caller: "s_0", Title: "Fix", Difficulty: "hard", State: protocol.ChangeOpen, CreatedAt: 1, UpdatedAt: 2}
	ev := protocol.Evidence{ChangeID: "ch_1", Revision: 1, StepRef: protocol.StepRef{Step: protocol.StepTest}, Result: protocol.StepResultPass, Commit: deliveryCommit,
		Fields: map[string]string{protocol.FieldSuite: protocol.SuiteChanged}, By: "s_2", ReportedBy: "s_0", ByModel: "claude-opus-5-5", ByModelFamily: "anthropic", Note: "ok", At: 3}
	p := protocol.PipelineToday()
	steps := p.Status(c, []protocol.Evidence{ev})
	view := protocol.ChangeView{Change: c, Steps: steps, Next: p.NextSteps(c, []protocol.Evidence{ev}), Pipeline: &p,
		Revisions: []protocol.Revision{{ChangeID: "ch_1", Number: 1, Branch: c.Branch, Commit: deliveryCommit, Uncommitted: true, At: 1}}, Evidence: []protocol.Evidence{ev}}
	for name, value := range map[string]any{
		"agent_change_submit": protocol.ChangeSubmitResult{Change: view, Bumped: true},
		"agent_step_claim":    protocol.StepClaimResult{Claimed: true, SessionID: "s_2"},
		"agent_step_report":   protocol.StepReportResult{Evidence: ev, Change: view},
		"agent_changes_find":  map[string]any{"items": []protocol.ChangeView{view, {Change: c, Steps: steps}}, "total": 2, "next_cursor": "x"},
		"agent_ask_sort": protocol.AskSortResult{Ask: "i_1", Result: "read", Next: "owner", State: "open",
			Stats: &protocol.AskSortStats{Sorter: "chief", Counts: map[string]int{protocol.AskSortClose: 2}, FirstID: 1, LastID: 2},
			Full: &protocol.AskFull{ID: "i_1", Kind: protocol.AskKindDraft, Status: "open", State: "open",
				Question: "q", Detail: "d", Options: []string{"accept"}, Recommendation: "r", FromSession: "s_1",
				FromName: "asker", FromKind: "session", Node: "demo", AboutKind: "project", AboutID: "demo",
				AboutTitle: "Demo", Next: "owner", NextName: "owner", WhyYou: "why", Priority: 1, CreatedAt: time.Unix(1, 0),
				Answer: "accept", AnsweredAt: time.Unix(2, 0), SourceRef: "drafts:node:demo",
				Routing: []protocol.AskSort{{ID: 1, Ask: "i_1", At: time.Unix(1, 0), Sorter: "chief", Result: "owner", Reason: "r", Priority: 1, WhyYou: "why", PrevNext: "chief", PrevStatus: "open"}},
				Charter: []protocol.CharterDraftField{{ID: 1, Level: "repo", Scope: "demo", Attr: "purpose", Value: "v", Source: "AGENTS.md", Status: "pending", DraftedBy: "s_1", CreatedAt: time.Unix(1, 0)}},
				Learned: &protocol.LearnedSuggestion{ID: 1, Node: "demo", Field: "accepts", Kind: "add", Phrase: "p", Evidence: []protocol.LearnedEvidence{{ID: "m1", Excerpt: "e"}}, Status: "pending", Severity: "major", NewValue: "p"}}},
	} {
		resolved, err := resolveCatalogSchema(catalogTool(name).OutputSchema)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		raw, _ := json.Marshal(value)
		var instance any
		_ = json.Unmarshal(raw, &instance)
		if err := resolved.Validate(instance); err != nil {
			t.Errorf("%s output does not match its schema: %v", name, err)
		}
	}
}

// The chief's full read of one ask is the same tool with detail full. The
// catalog accepts it and returns the daemon's full row.
func TestAskSortFullReadTool(t *testing.T) {
	full := protocol.AskSortResult{Ask: "i_1", Result: "read", Next: "owner", State: "open",
		Full: &protocol.AskFull{ID: "i_1", Kind: protocol.AskKindDraft, Status: "open", State: "open",
			Question: "Charter draft for demo: 3 fields", FromSession: "agent-net", Next: "owner", CreatedAt: time.Unix(1, 0)}}
	var sent map[string]any
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		_ = json.Unmarshal(raw, &sent)
		if method != protocol.OpAskSort {
			t.Errorf("method = %q, want %q", method, protocol.OpAskSort)
		}
		return json.Marshal(full)
	})
	res := deliveryCall(t, cs, "agent_ask_sort", `{"ask":"i_1","detail":"full"}`)
	if res.IsError {
		t.Fatalf("full read: %s", deliveryText(res))
	}
	if sent["detail"] != "full" || sent["ask"] != "i_1" {
		t.Fatalf("arguments sent: %v", sent)
	}
	if text := deliveryText(res); !strings.Contains(text, `"question":"Charter draft for demo: 3 fields"`) {
		t.Fatalf("full read text: %s", text)
	}
}

// The chief's list carries the sorter's counts, and the caller may narrow them
// with a sorter and a time range. The field reaches the tool result as it is.
func TestAskSortListStatsTool(t *testing.T) {
	list := protocol.AskSortResult{Result: protocol.AskSortList,
		Stats: &protocol.AskSortStats{Sorter: "s_1", Counts: map[string]int{protocol.AskSortClose: 2, protocol.AskSortOwner: 1}, FirstID: 1, LastID: 9}}
	var sent map[string]any
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		_ = json.Unmarshal(raw, &sent)
		if method != protocol.OpAskSort {
			t.Errorf("method = %q, want %q", method, protocol.OpAskSort)
		}
		return json.Marshal(list)
	})
	res := deliveryCall(t, cs, "agent_ask_sort", `{"result":"list","sorter":"owner","since":100,"until":200}`)
	if res.IsError {
		t.Fatalf("list with stats: %s", deliveryText(res))
	}
	if sent["result"] != "list" || sent["sorter"] != "owner" || sent["since"] != float64(100) || sent["until"] != float64(200) {
		t.Fatalf("arguments sent: %v", sent)
	}
	text := deliveryText(res)
	for _, want := range []string{`"stats"`, `"sorter":"s_1"`, `"close":2`, `"owner":1`, `"first_id":1`, `"last_id":9`} {
		if !strings.Contains(text, want) {
			t.Fatalf("list result %s does not contain %s", text, want)
		}
	}
}

func deliveryHas(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

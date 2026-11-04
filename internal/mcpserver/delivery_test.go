package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

const deliveryCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

var deliveryCalls = map[string]string{
	"agent_change_submit": `{"branch":"agent-net/w_1","commit":"` + deliveryCommit + `"}`,
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

// Until the daemon part lands, each delivery tool checks its arguments against
// the schema and says it is not built yet, without calling the daemon.
func TestDeliveryToolsNotBuiltYet(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	t.Setenv("AGENT_NET_WORKER", "")
	cs := connect(t, func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		mu.Lock()
		methods = append(methods, method)
		mu.Unlock()
		return json.RawMessage(`{}`), nil
	})
	listed := listedCatalogNames(t, cs)
	for name, args := range deliveryCalls {
		if !deliveryHas(listed, name) {
			t.Errorf("agent does not see %s", name)
			continue
		}
		res := deliveryCall(t, cs, name, args)
		if !res.IsError || !strings.Contains(deliveryText(res), name+" is not built yet") {
			t.Errorf("%s = %v %q", name, res.IsError, deliveryText(res))
		}
	}
	mu.Lock()
	for _, m := range methods {
		for _, op := range []string{protocol.OpChangeSubmit, protocol.OpStepReport, protocol.OpChangesFind} {
			if m == op {
				t.Errorf("the daemon was called with %s", m)
			}
		}
	}
	mu.Unlock()

	// The schema still checks the arguments first.
	res := deliveryCall(t, cs, "agent_step_report", `{"change":"ch_1","step":"check","result":"pass","commit":"`+deliveryCommit+`"}`)
	if !res.IsError || strings.Contains(deliveryText(res), "not built yet") {
		t.Errorf("bad step accepted: %q", deliveryText(res))
	}
	res = deliveryCall(t, cs, "agent_change_submit", `{}`)
	if !res.IsError || strings.Contains(deliveryText(res), "not built yet") {
		t.Errorf("submit without a branch accepted: %q", deliveryText(res))
	}
}

func TestDeliveryToolsForWorkerAndCTO(t *testing.T) {
	worker := listedCatalogNames(t, connectWorker(t))
	if !deliveryHas(worker, "agent_changes_find") || deliveryHas(worker, "agent_change_submit") || deliveryHas(worker, "agent_step_report") {
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
		"agent_step_report":   protocol.StepReportResult{Evidence: ev, Change: view},
		"agent_changes_find":  map[string]any{"items": []protocol.ChangeView{view, {Change: c, Steps: steps}}, "total": 2, "next_cursor": "x"},
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

func deliveryHas(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

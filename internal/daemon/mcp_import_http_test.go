package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/testmcp/tools"
)

// A native http entry with a header given as ${VAR} and a native sse entry
// are imported end to end against local fake servers. The registry keeps the
// variable name only; the value is read on the host when it connects.
func TestMCPImportHTTPAndSSEEndToEnd(t *testing.T) {
	const token = "http-secret-token-9f8e7d"
	t.Setenv("HOMELAB_TOKEN", token)
	httpSrv := bearerServer(t, token)
	sseSrv := httptest.NewServer(mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return tools.New() }, nil))
	t.Cleanup(sseSrv.Close)

	httpEntry := `{"type":"http","url":"` + httpSrv.URL + `","headers":{"Authorization":"${HOMELAB_TOKEN}"},"allowedTools":["echo"]}`
	sseEntry := `{"type":"sse","url":"` + sseSrv.URL + `"}`
	f := newImportFixture(t, map[string]string{"homelab": httpEntry, "legacy": sseEntry}, "")

	cand := f.candidate(t, "claude", "homelab")
	if cand.Class != protocol.MCPImportable || cand.Transport != protocol.MCPHTTP {
		t.Fatalf("http candidate: %+v", cand)
	}
	if leg := f.candidate(t, "claude", "legacy"); leg.Class != protocol.MCPImportable || leg.Transport != protocol.MCPSSE {
		t.Fatalf("sse candidate: %+v", leg)
	}

	plan := f.plan(t, cand)
	if len(plan.Conflicts) != 0 || plan.Server.Transport != protocol.MCPHTTP || plan.Server.URL != httpSrv.URL ||
		len(plan.Server.Headers) != 1 || plan.Server.Headers[0].Name != "Authorization" || plan.Server.Headers[0].Env != "HOMELAB_TOKEN" ||
		plan.Server.Headers[0].File != "" {
		t.Fatalf("plan: %+v", plan.Server)
	}
	res := f.apply(t, plan.ID)
	if res.State != protocol.MCPImportOK || len(res.Tools) != 1 || res.Tools[0] != "echo" || !strings.Contains(res.Compared, "present") {
		t.Fatalf("apply: %+v", res)
	}
	var srv protocol.MCPServer
	for _, s := range f.servers(t) {
		if s.Name == "homelab" {
			srv = s
		}
	}
	if !srv.Enabled || srv.Health != protocol.HealthOK || srv.Transport != protocol.MCPHTTP || srv.Scope != "global" {
		t.Fatalf("registry: %+v", srv)
	}
	if out, err := callTool(t, f.cli, "homelab", "echo", map[string]string{"text": "over http"}); err != nil || out.Text != "echo: over http" {
		t.Fatalf("call: %+v %v", out, err)
	}

	// The value never reaches the gateway, the plan, the registry or the audit.
	var page protocol.ToolsPage
	if err := json.Unmarshal(f.cli.mustCall("tools_items", map[string]any{"kind": "mcp"}), &page); err != nil {
		t.Fatal(err)
	}
	var item protocol.ToolItem
	for _, it := range page.Items {
		if it.Federated && it.Name == "homelab" {
			item = it
		}
	}
	hist := f.cli.mustCall("tools_history", protocol.ToolHistoryRequest{Item: item.ID})
	pageRaw, _ := json.Marshal(page)
	planRaw, _ := json.Marshal(plan)
	for what, raw := range map[string]string{"history": string(hist), "items": string(pageRaw), "plan": string(planRaw),
		"candidates": string(f.cli.mustCall("mcp_import_candidates", protocol.MCPImportCandidatesRequest{})),
		"servers":    string(f.cli.mustCall("mcp_list", nil))} {
		if strings.Contains(raw, token) {
			t.Fatalf("the header value leaked into %s", what)
		}
	}

	// The SSE entry imports too.
	leg := f.apply(t, f.plan(t, f.candidate(t, "claude", "legacy")).ID)
	if leg.State != protocol.MCPImportOK || !strings.Contains(leg.Compared, "not compared") {
		t.Fatalf("sse apply: %+v", leg)
	}
	if out, err := callTool(t, f.cli, "legacy", "echo", map[string]string{"text": "over sse"}); err != nil || out.Text != "echo: over sse" {
		t.Fatalf("sse call: %+v %v", out, err)
	}
	f.requireConfigsUntouched(t)
}

// A header variable that the host does not have makes the test fail, and the
// failed import leaves nothing behind.
func TestMCPImportHTTPMissingHeaderVariableFails(t *testing.T) {
	httpSrv := bearerServer(t, "never-used-token")
	entry := `{"type":"http","url":"` + httpSrv.URL + `","headers":{"Authorization":"${AGENT_NET_TEST_UNSET_VAR}"}}`
	f := newImportFixture(t, map[string]string{"homelab": entry}, "")
	res := f.apply(t, f.plan(t, f.candidate(t, "claude", "homelab")).ID)
	if res.State != protocol.MCPImportFailed || !strings.Contains(res.Reason, "test") {
		t.Fatalf("apply: %+v", res)
	}
	if len(f.servers(t)) != 0 {
		t.Fatalf("a failed import left a server: %+v", f.servers(t))
	}
}

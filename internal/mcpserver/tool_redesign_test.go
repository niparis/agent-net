package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

func TestHandoverRefreshesCatalogTools(t *testing.T) {
	cases := []struct {
		name      string
		role      toolcatalog.Role
		newServer func() *mcp.Server
	}{
		{"agent", toolcatalog.RoleAgent, func() *mcp.Server { return New(okCaller, "") }},
		{"CTO", toolcatalog.RoleCTO, func() *mcp.Server {
			return NewCTO(okCaller, "")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRigServer(t, &handover{V: 1, Init: []byte(initReq), Initialized: []byte(initDone)}, tc.newServer())
			if line := r.next(); !strings.Contains(line, `"method":"notifications/tools/list_changed"`) {
				t.Fatalf("handover notification = %s", line)
			}
			r.send(listReq + "\n")
			line := r.next()
			var answer struct {
				Result struct {
					Tools []struct {
						Name string `json:"name"`
					} `json:"tools"`
				} `json:"result"`
			}
			if err := json.Unmarshal([]byte(line), &answer); err != nil {
				t.Fatalf("decode tools/list: %v (%s)", err, line)
			}
			have := map[string]bool{}
			for _, tool := range answer.Result.Tools {
				have[tool.Name] = true
			}
			for _, entry := range toolcatalog.ForRole(tc.role) {
				if tc.role == toolcatalog.RoleCTO && !ctoV2Tools[entry.Name] {
					continue
				}
				if !have[entry.Name] {
					t.Errorf("tools/list after handover misses catalog tool %q", entry.Name)
				}
			}
		})
	}
}

func TestToolListJSONSizes(t *testing.T) {
	cases := []struct {
		name   string
		role   toolcatalog.Role
		client *mcp.ClientSession
	}{
		{"agent", toolcatalog.RoleAgent, connect(t, okCaller)},
		{"worker", toolcatalog.RoleWorker, connectWorker(t)},
		{"CTO", toolcatalog.RoleCTO, ctoTestSession(t)},
	}
	for _, tc := range cases {
		list, err := tc.client.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		allJSON, err := json.Marshal(list.Tools)
		if err != nil {
			t.Fatal(err)
		}
		catalogNames := map[string]bool{}
		for _, entry := range toolcatalog.ForRole(tc.role) {
			catalogNames[entry.Name] = true
		}
		var catalogOnly []*mcp.Tool
		for _, tool := range list.Tools {
			if catalogNames[tool.Name] {
				catalogOnly = append(catalogOnly, tool)
			}
		}
		catalogJSON, err := json.Marshal(catalogOnly)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s tools/list JSON: current=%d bytes catalog-only=%d bytes", tc.name, len(allJSON), len(catalogJSON))
		_ = tc.client.Close()
	}
}

func connectWorker(t *testing.T) *mcp.ClientSession {
	t.Helper()
	t.Setenv("AGENT_NET_WORKER", "1")
	return connect(t, okCaller)
}

package daemon

// Tests for tool redesign task 4a: the daemon refuses a catalog tool that the
// caller's role may not use, even when the call bypasses the MCP tool list.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCatalogToolRoleRefused(t *testing.T) {
	d := &Daemon{}
	cases := []struct {
		name   string
		ls     *localSession
		method string
		args   string
		want   string
	}{
		{
			name:   "worker cannot start a worker",
			ls:     &localSession{info: protocol.SessionInfo{Worker: &protocol.WorkerInfo{}}},
			method: "agent_worker_start",
			args:   `{"task":"do the work","touches":["ui"]}`,
			want:   "not available to worker",
		},
		{
			name:   "agent cannot change the graph",
			ls:     &localSession{info: protocol.SessionInfo{}},
			method: "graph_change",
			args:   `{"action":"node_add","name":"example"}`,
			want:   "not available to agent",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lc := &localConn{d: d, client: "mcp", ls: tc.ls}
			_, err := lc.dispatch(context.Background(), tc.method, json.RawMessage(tc.args))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s: err = %v, want containing %q", tc.method, err, tc.want)
			}
		})
	}
}

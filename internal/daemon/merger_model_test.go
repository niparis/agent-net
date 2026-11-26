package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// mergerPickDaemon is a daemon whose gateway knows one project and answers
// the role gate with a refusal, so a merger start stops at the gate and the
// test reads what the gate was asked.
func mergerPickDaemon(t *testing.T, asked *[]protocol.RolePickRequest) *Daemon {
	t.Helper()
	d := &Daemon{log: log.New(io.Discard, "", 0)}
	d.cfg.NodeID, d.cfg.Hostname = "node-1", "box"
	d.cfg.testGatewayCall = func(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
		switch typ {
		case "query.projects":
			raw, _ := json.Marshal(protocol.ProjectsResult{Projects: []protocol.ProjectView{{Name: "garden", NodeID: "node-1", Hostname: "box", Path: "/srv/garden"}}})
			return raw, true, nil
		case "worker.role.pick":
			*asked = append(*asked, params.(protocol.RolePickRequest))
			raw, _ := json.Marshal(protocol.RolePickResult{Refused: "claude-sonnet-5-5 is not signed in"})
			return raw, true, nil
		}
		return nil, false, nil
	}
	return d
}

func TestMergerStartAsksGateForNamedModelOnly(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  string
		want  []protocol.RoleModel
		error string
	}{
		{"named", `{"project":"garden","model":"claude-sonnet-5-5"}`, []protocol.RoleModel{{Harness: "claude", Model: "claude-sonnet-5-5"}}, "merger model: claude-sonnet-5-5 is not signed in"},
		{"setting", `{"project":"garden"}`, nil, "merger model: claude-sonnet-5-5 is not signed in"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asked []protocol.RolePickRequest
			d := mergerPickDaemon(t, &asked)
			ctx := context.WithValue(context.Background(), proposalActorKey{}, "owner")
			_, err := d.executeApprovedCTOTool(ctx, "merger_start", json.RawMessage(tc.args))
			if err == nil || err.Error() != tc.error || strings.Contains(err.Error(), "\n") {
				t.Fatalf("err = %v, want %q", err, tc.error)
			}
			if len(asked) != 1 || asked[0].Role != protocol.RoleMerger || !reflect.DeepEqual(asked[0].Candidates, tc.want) {
				t.Fatalf("gate asked %+v, want candidates %+v", asked, tc.want)
			}
		})
	}
}

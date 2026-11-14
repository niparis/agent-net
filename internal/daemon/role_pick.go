package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// pickRoleOnMachine asks the gateway's one gate which model a role session may
// start on (decision 0162). The CTO sends its local list; the chief and merger
// leave it empty and the gateway reads their setting. A refusal comes back as
// one line, so a caller can show the reason instead of a silent failure.
func (d *Daemon) pickRoleOnMachine(ctx context.Context, role, nodeID string, candidates []protocol.RoleModel, current protocol.RoleModel, resume bool) (protocol.RolePickResult, error) {
	raw, err := d.call(ctx, "worker.role.pick", protocol.RolePickRequest{
		Role:       role,
		NodeID:     nodeID,
		Candidates: candidates,
		Current:    current,
		Resume:     resume,
	})
	if err != nil {
		return protocol.RolePickResult{}, err
	}
	var out protocol.RolePickResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return protocol.RolePickResult{}, err
	}
	if out.Refused != "" {
		return out, errors.New(strings.TrimSpace(out.Refused))
	}
	if out.Harness == "" {
		return out, errors.New("no " + role + " model can run now")
	}
	return out, nil
}

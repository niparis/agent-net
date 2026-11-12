package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/niparis/agent-net/internal/daemon/opencodeapi"
)

type ctoPermission struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Effect   string `json:"effect"`
}

// ctoPermissions are the CTO agent's OpenCode rules. The CTO has full power
// (decision 0157): OpenCode's own defaults decide the built-in tools, as for
// a coding session. The rule allows its own MCP server without a prompt.
func ctoPermissions() []ctoPermission {
	return []ctoPermission{{"agent-net-cto_*", "*", "allow"}}
}

// checkOpenCodeCTOAgent checks that the private server runs the CTO agent.
func (d *Daemon) checkOpenCodeCTOAgent(ctx context.Context, info []byte) error {
	var version struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(info, &version) != nil || version.Version == "" {
		version.Version = "(unknown)"
	}
	if version.Version != "(unknown)" {
		d.cto.version = version.Version
	}
	if d.log != nil {
		d.log.Printf("CTO OpenCode version: %s", version.Version)
	}
	raw, err := d.ctoOp(ctx, opencodeapi.Agents, "", "", nil)
	if err != nil {
		return fmt.Errorf("CTO OpenCode: read agents: %w", err)
	}
	var response struct {
		Data []struct {
			Name    string `json:"name"`
			Disable bool   `json:"disable"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return errors.New("CTO OpenCode: cannot read its agents")
	}
	for _, agent := range response.Data {
		if agent.Name == "cto" && !agent.Disable {
			return nil
		}
	}
	return errors.New("CTO OpenCode: the cto agent is missing")
}

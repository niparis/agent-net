package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
//
// The two read rules answer a real stall. OpenCode 2.3.0 asks before a read of
// a secrets file (read *.env ask, by default), and nobody watches the CTO to
// answer. A read of a file outside the folder, shell commands, edits outside the
// folder and a repeated call did not prompt in the same check. The later rules
// win over the defaults, so they allow the read (decision 0171).
func ctoPermissions() []ctoPermission {
	return []ctoPermission{
		{"agent-net-cto_*", "*", "allow"},
		{"read", "*.env", "allow"},
		{"read", "*.env.*", "allow"},
	}
}

// checkOpenCodeCTOAgent waits until the private server lists the cto agent. The
// server answers its first request before it has loaded the folder's config, so
// the first listing can lack the agent. On OpenCode 2.3.0 with a fresh home it
// appeared 1 to 3 s after the server started. The caller's context bounds the
// wait; the start uses its startup deadline.
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
	var lastErr error
	for {
		listed, err := d.ctoAgentListed(ctx)
		if err == nil && listed {
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return lastErr
			}
			return errors.New("CTO OpenCode: the cto agent is missing")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// ctoAgentListed reports whether one listing of the private server has an
// enabled cto agent.
func (d *Daemon) ctoAgentListed(ctx context.Context) (bool, error) {
	raw, err := d.ctoOp(ctx, opencodeapi.Agents, "", "", nil)
	if err != nil {
		return false, fmt.Errorf("CTO OpenCode: read agents: %w", err)
	}
	var response struct {
		Data []struct {
			Name    string `json:"name"`
			Disable bool   `json:"disable"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return false, errors.New("CTO OpenCode: cannot read its agents")
	}
	for _, agent := range response.Data {
		if agent.Name == "cto" && !agent.Disable {
			return true, nil
		}
	}
	return false, nil
}

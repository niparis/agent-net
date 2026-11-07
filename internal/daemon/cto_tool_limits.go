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

func ctoPermissions() []ctoPermission {
	rules := []ctoPermission{}
	for _, action := range []string{"*", "bash", "edit", "write", "subagent", "read", "glob", "grep", "list", "webfetch", "todowrite"} {
		rules = append(rules, ctoPermission{action, "*", "deny"})
	}
	return append(rules, ctoPermission{"agent-net-cto_*", "*", "allow"})
}

func (d *Daemon) checkOpenCodeCTOTools(ctx context.Context, info []byte) error {
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
		return fmt.Errorf("CTO OpenCode: read effective permissions: %w", err)
	}
	var response struct {
		Data []struct {
			Name        string          `json:"name"`
			Disable     bool            `json:"disable"`
			Permissions []ctoPermission `json:"permissions"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &response) != nil || len(response.Data) == 0 {
		return errors.New("CTO OpenCode: no effective agent permissions; refusing to start")
	}
	found := false
	for _, agent := range response.Data {
		if agent.Disable {
			continue
		}
		if agent.Name == "general" {
			return errors.New("CTO OpenCode: general subagent is enabled")
		}
		if agent.Name != "cto" {
			continue
		}
		if found {
			return errors.New("CTO OpenCode: duplicate CTO agent")
		}
		found = true
		for _, required := range ctoPermissions() {
			if required.Effect != "deny" {
				continue
			}
			present := false
			for _, effective := range agent.Permissions {
				if effective == required {
					present = true
					break
				}
			}
			if !present {
				return fmt.Errorf("CTO OpenCode: built-in tools are not disabled: missing deny rule %q", required.Action)
			}
		}
		// OpenCode appends agent rules after its default allow. A wildcard
		// deny must close that default before any private MCP allow.
		closed := false
		for _, rule := range agent.Permissions {
			if rule.Action == "*" && rule.Resource == "*" && rule.Effect == "deny" {
				closed = true
				continue
			}
			if !closed {
				continue
			}
			if rule.Effect == "deny" {
				continue
			}
			if rule.Effect != "allow" || rule.Action != "agent-net-cto_*" || rule.Resource != "*" {
				return fmt.Errorf("CTO OpenCode: effective tool rule %q (%s) is outside the private CTO MCP server", rule.Action, rule.Effect)
			}
		}
		if !closed {
			return errors.New("CTO OpenCode: effective permissions allow built-in tools (missing wildcard deny)")
		}
	}
	if !found {
		return errors.New("CTO OpenCode: effective CTO agent is missing")
	}
	return nil
}

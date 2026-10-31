package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// The init record is evidence from the harness, never a model's answer.
// Missing records or lists are not evidence of an empty tool set.
func checkClaudeCTOInit(output []byte) error {
	sc := bufio.NewScanner(bytes.NewReader(output))
	sc.Buffer(make([]byte, 4096), 1<<20)
	found := false
	for sc.Scan() {
		var init struct {
			Type    string    `json:"type"`
			Subtype string    `json:"subtype"`
			Version string    `json:"claude_code_version"`
			Tools   *[]string `json:"tools"`
		}
		if json.Unmarshal(sc.Bytes(), &init) != nil || init.Type != "system" || init.Subtype != "init" {
			continue
		}
		if init.Version != "2.1.316" {
			return fmt.Errorf("CTO Claude: unknown harness version %q in init record", init.Version)
		}
		if init.Tools == nil {
			return errors.New("CTO Claude: init record has no effective tool list")
		}
		for _, tool := range *init.Tools {
			if !strings.HasPrefix(tool, "mcp__agent-net-cto__") || tool == "mcp__agent-net-cto__" {
				return fmt.Errorf("CTO Claude: effective tool %q is outside the private CTO MCP server", tool)
			}
		}
		found = true
	}
	if sc.Err() != nil {
		return fmt.Errorf("CTO Claude: read init record: %w", sc.Err())
	}
	if !found {
		return errors.New("CTO Claude: no init record with an effective tool list; refusing to start")
	}
	return nil
}

func (d *Daemon) checkOpenCodeCTOTools(ctx context.Context, info []byte) error {
	var version struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(info, &version) != nil || version.Version != "2.2.0" {
		return fmt.Errorf("CTO OpenCode: unknown harness version %q; tool isolation is not verified", version.Version)
	}
	raw, err := d.cto.api(ctx, "GET", "/api/agent", nil)
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
		if agent.Name != "cto" {
			return fmt.Errorf("CTO OpenCode: extra agent %q is enabled", agent.Name)
		}
		if found {
			return errors.New("CTO OpenCode: duplicate CTO agent")
		}
		found = true
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

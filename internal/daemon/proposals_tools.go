package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
)

type proposalActorKey struct{}

func proposalActor(ctx context.Context) string {
	s, _ := ctx.Value(proposalActorKey{}).(string)
	return s
}

func concreteProposal(call proposalCall) (proposalCall, error) {
	if call.Tool == "graph_change" {
		switch call.Action {
		case "charter_set", "node_add", "node_remove", "edge_set", "edge_remove", "settings_set", "undo":
			call.Tool = call.Action
			if call.Tool == "settings_set" {
				call.Tool = "node_settings_set"
			}
			if call.Tool == "undo" {
				call.Tool = "graph_undo"
			}
			call.Action = ""
		default:
			return call, errors.New("unsupported proposal action")
		}
	}
	class, err := ctoClass(call.Tool, call.Args)
	if err != nil {
		return call, err
	}
	if class != ctoProposal {
		return call, errors.New("tool call is not a proposal")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(call.Args, &fields); err != nil {
		return call, err
	}
	var action string
	if v, ok := fields["action"]; ok {
		if err := json.Unmarshal(v, &action); err != nil {
			return call, err
		}
	}
	if call.Action != action {
		return call, errors.New("proposal action differs from arguments")
	}
	return call, nil
}

func (d *Daemon) validateProposal(ctx context.Context, call proposalCall) error {
	call, err := concreteProposal(call)
	if err != nil {
		return err
	}
	if err := mcpserver.CTOArguments(call.Tool, call.Args); err != nil {
		return fmt.Errorf("bad %s: %w", call.Tool, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(call.Args, &fields); err != nil {
		return err
	}
	str := func(key string) string { var s string; _ = json.Unmarshal(fields[key], &s); return s }
	switch call.Tool {
	case "notification_settings":
		if _, ok := fields["disabled"]; !ok {
			return errors.New("notification_settings needs disabled")
		}
		return nil
	case "integrations_set":
		if _, ok := d.integrationsClient(); !ok {
			return errNotConnected
		}
		return nil
	case "score_set":
		var p protocol.ScoreSetRequest
		if err := json.Unmarshal(call.Args, &p); err != nil {
			return err
		}
		if strings.TrimSpace(p.Model) != p.Model || p.Model == "" || len(p.Model) > 256 || strings.IndexFunc(p.Model, unicode.IsControl) >= 0 || p.Score != nil && (*p.Score < 0 || *p.Score > 100) {
			return errors.New("bad score")
		}
		if _, ok := d.integrationsClient(); !ok {
			return errNotConnected
		}
		return nil
	case "protected_providers_set", "worker_providers_set":
		var providers []string
		if call.Tool == "protected_providers_set" {
			var p protocol.ProtectedProviders
			if err := json.Unmarshal(call.Args, &p); err != nil {
				return err
			}
			providers = p.Providers
			if len(p.Reason) > 4096 {
				return errors.New("bad protected providers")
			}
		} else {
			var p protocol.WorkerProviders
			if err := json.Unmarshal(call.Args, &p); err != nil {
				return err
			}
			providers = p.Providers
		}
		if err := validateProposalProviders(providers); err != nil {
			return err
		}
		if _, ok := d.integrationsClient(); !ok {
			return errNotConnected
		}
		return nil
	case "cto_settings":
		var p protocol.CTOSettingsRequest
		if err := json.Unmarshal(call.Args, &p); err != nil {
			return err
		}
		if p.Title != nil {
			if _, err := protocol.NormalizeCTOTitle(*p.Title); err != nil {
				return err
			}
		}
		if p.Harness != nil && *p.Harness != "claude" && *p.Harness != "opencode" {
			return errors.New("bad CTO harness")
		}
		if _, ok := d.integrationsClient(); !ok {
			return errNotConnected
		}
		return nil
	case "cto_new":
		if _, ok := d.integrationsClient(); !ok {
			return errNotConnected
		}
		return nil
	case "cto_send":
		if strings.TrimSpace(str("text")) == "" {
			return errors.New("cto_send needs text")
		}
		if _, ok := d.integrationsClient(); !ok {
			return errNotConnected
		}
		return nil
	case "onboarding_restart":
		_, err := d.call(ctx, "onboarding.get", nil)
		return err
	case "invite":
		if d.cfg.GatewayData == "" {
			return errors.New("invite runs on the gateway machine")
		}
		if _, err := gateway.CAFingerprint(d.cfg.GatewayData); err != nil {
			return err
		}
		_, err := gateway.ServerPin(d.cfg.GatewayData)
		return err
	case "project_add":
		var p protocol.ProjectAddRequest
		if err := json.Unmarshal(call.Args, &p); err != nil {
			return err
		}
		p.DryRun = true
		_, err := d.projectAdd(ctx, p, "", "cli")
		return err
	case "project_remove":
		if path := str("path"); path != "" {
			expanded, err := expandPath(path)
			if err != nil {
				return err
			}
			fields["path"], _ = json.Marshal(expanded)
			call.Args, _ = json.Marshal(fields)
		}
	case "worker_start":
		var p protocol.WorkerStartRequest
		if err := json.Unmarshal(call.Args, &p); err != nil {
			return err
		}
		if _, err := checkedWorkerStart(p); err != nil {
			return err
		}
	}
	// Existence, enrollment and the checks held by the gateway run there. This
	// endpoint is read-only and refuses unknown tools on older gateways.
	args := call.Args
	if call.Tool == "message" && str("action") == "send" {
		to, err := d.resolveTo(ctx, str("to"))
		if err != nil {
			return err
		}
		fields["to"], _ = json.Marshal(to)
		args, _ = json.Marshal(fields)
	}
	_, err = d.call(ctx, "cto.proposal.validate", map[string]any{"tool": call.Tool, "args": args, "owner": d.ownerSessionID()})
	return err
}

func validateProposalProviders(providers []string) error {
	if len(providers) > 128 {
		return errors.New("bad providers")
	}
	seen := map[string]bool{}
	for _, p := range providers {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" || len(p) > 64 || seen[p] || strings.IndexFunc(p, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.')
		}) >= 0 {
			return errors.New("bad provider")
		}
		seen[p] = true
	}
	return nil
}

// runStoredCTOProposal is the owner runner, separate from the proposing path.
func runStoredCTOProposal(ctx context.Context, call proposalCall, actor string, run func(context.Context, string, json.RawMessage) (any, error)) (json.RawMessage, error) {
	call, err := concreteProposal(call)
	if err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, proposalActorKey{}, actor)
	value, err := run(ctx, call.Tool, call.Args)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func (d *Daemon) runProposal(ctx context.Context, call proposalCall, actor string) (json.RawMessage, error) {
	raw, err := runStoredCTOProposal(ctx, call, actor, d.executeCTOTool)
	if err != nil {
		return nil, err
	}
	if err := d.recordCTOAction(actor + ": applied " + call.Tool); err != nil {
		return nil, err
	}
	return raw, nil
}

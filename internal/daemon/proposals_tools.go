package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
)

type proposalActorKey struct{}
type proposalHerdrLocationKey struct{}
type proposalRemoteOwnerKey struct{}

type proposalHerdrLocation struct {
	socket, pane string
	approver     *localConn
}

func proposalActor(ctx context.Context) string {
	s, _ := ctx.Value(proposalActorKey{}).(string)
	return s
}

func concreteProposal(call proposalCall) (proposalCall, error) {
	// A proposal stored before part 10 already names the concrete call.
	if mcpserver.IsCTOConcreteTool(call.Tool) {
		return call, nil
	}
	class, err := ctoClass(call.Tool, call.Args)
	if err != nil {
		// Propose may carry the routing value separately from the arguments.
		field := ctoActionField[call.Tool]
		if field == "" || call.Action == "" {
			return call, err
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(call.Args, &fields) != nil {
			return call, err
		}
		fields[field], _ = json.Marshal(call.Action)
		if call.Args, err = json.Marshal(fields); err != nil {
			return call, err
		}
		if class, err = ctoClass(call.Tool, call.Args); err != nil {
			return call, err
		}
	}
	// Open proposals from the former policy may now be immediate actions.
	// The legacy message tool also stored approved as-owner sends and replies.
	if class != ctoProposal && class != ctoMinor && call.Tool != "message" {
		return call, errors.New("tool call is not a proposal")
	}
	// Proposal reasons are metadata stored beside the concrete call. They are
	// not part of the action arguments passed to the concrete validator.
	var fields map[string]json.RawMessage
	if call.Tool == "graph_change" && json.Unmarshal(call.Args, &fields) == nil {
		delete(fields, "reason")
		call.Args, err = json.Marshal(fields)
		if err != nil {
			return call, err
		}
	}
	concrete, args, action, err := ctoConcreteCall(call.Tool, call.Args)
	if err != nil {
		return call, err
	}
	if call.Action != "" && call.Action != action {
		return call, errors.New("proposal action differs from arguments")
	}
	call.Tool = concrete
	call.Action = ""
	call.Args = args
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
	case "settings_values_write":
		var q protocol.SettingsWrite
		if err := json.Unmarshal(call.Args, &q); err != nil {
			return err
		}
		if _, err := validateSettingsWrite(q); err != nil {
			return err
		}
		q.By = "owner"
		_, err := d.call(ctx, "settings.validate", q)
		return err
	case "settings_values_undo":
		var q protocol.SettingsUndo
		if err := json.Unmarshal(call.Args, &q); err != nil {
			return err
		}
		_, err := d.settingsRevisionHasRule(ctx, q.Revision)
		return err
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
		var payload []byte
		limit := 4096
		if call.Tool == "protected_providers_set" {
			var p protocol.ProtectedProviders
			if err := json.Unmarshal(call.Args, &p); err != nil {
				return err
			}
			providers = p.Providers
			payload, _ = json.Marshal(p)
			limit = 8192
			if len(p.Reason) > 4096 {
				return errors.New("bad protected providers")
			}
		} else {
			var p protocol.WorkerProviders
			if err := json.Unmarshal(call.Args, &p); err != nil {
				return err
			}
			providers = p.Providers
			payload, _ = json.Marshal(p)
		}
		if len(payload) > limit {
			return errors.New("provider settings payload exceeds the endpoint limit")
		}
		if err := validateProposalProviders(providers); err != nil {
			return err
		}
		if _, ok := d.integrationsClient(); !ok {
			return errNotConnected
		}
		return nil
	case "worker_floors_set":
		var p protocol.WorkerFloors
		if err := json.Unmarshal(call.Args, &p); err != nil {
			return err
		}
		if len(p.Floors) > 128 {
			return errors.New("bad worker floors")
		}
		var providers []string
		for k, f := range p.Floors {
			if f < 0 || f > 95 {
				return errors.New("bad floor: must be 0-95")
			}
			providers = append(providers, k)
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
	case "merger_start", "merger_stop":
		var p protocol.SessionStartRequest
		if err := json.Unmarshal(call.Args, &p); err != nil {
			return err
		}
		_, _, err := d.mergerProject(ctx, p)
		return err
	case "session_start", "project_create":
		var p protocol.SessionStartRequest
		if err := json.Unmarshal(call.Args, &p); err != nil {
			return err
		}
		if p.Remote != "" {
			if call.Tool != "project_create" {
				return errors.New("remote is only supported for project creation")
			}
			if err := validateProjectRemote(p.Remote); err != nil {
				return err
			}
		}
		if call.Tool == "project_create" && strings.TrimSpace(p.Path) == "" {
			return errors.New("project_create needs path")
		}
		if strings.TrimSpace(p.Project) == "" && strings.TrimSpace(p.Path) == "" {
			return errors.New("session_start needs project or path")
		}
		if p.Folder != "" && p.Folder != protocol.FolderProject && p.Folder != protocol.FolderWorktree {
			return errors.New("invalid folder choice")
		}
		if len(p.FirstMessage) > protocol.MaxBodyBytes {
			return errors.New("first_message is too long")
		}
		if p.Path != "" {
			path := strings.TrimSpace(p.Path)
			if strings.IndexFunc(p.Path, unicode.IsControl) >= 0 {
				return errors.New("the path has a control character")
			}
			if !filepath.IsAbs(path) && path != "~" && !strings.HasPrefix(path, "~/") {
				return errors.New("the path must be absolute or start with ~")
			}
			if p.NodeID == "" || p.NodeID == d.cfg.NodeID || strings.EqualFold(p.NodeID, d.cfg.Hostname) {
				if _, err := cleanOwnerPath(path); err != nil {
					return err
				}
			}
		}
		return nil
	case "project_add", "project_remove":
		_, err := d.projectChange(ctx, call.Tool, call.Args, true)
		return err
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

// proposalProjectRemove translates the CTO's machine field to the registry API.
// Use the same request for preflight and for the approved removal.
func proposalProjectRemove(raw json.RawMessage) (protocol.ProjectRemove, error) {
	var p struct {
		protocol.ProjectRemove
		NodeID string `json:"node_id"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p.ProjectRemove, err
	}
	if p.NodeID != "" {
		p.On = p.NodeID
	}
	if p.Path != "" {
		var err error
		if p.Path, err = expandPath(p.Path); err != nil {
			return p.ProjectRemove, err
		}
	}
	return p.ProjectRemove, nil
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
	raw, err := runStoredCTOProposal(ctx, call, actor, d.executeApprovedCTOTool)
	if err != nil {
		return nil, err
	}
	auditName := call.Tool
	if concrete, _, _, err := ctoConcreteCall(call.Tool, call.Args); err == nil {
		auditName = concrete
	}
	if err := d.recordCTOAction(actor + ": applied " + auditName); err != nil {
		return nil, err
	}
	return raw, nil
}

// This also executes proposals stored before the at-once policy.
func (d *Daemon) executeApprovedCTOTool(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	if name == "sessions_archive" && proposalActor(ctx) != "cto" {
		if proposalActor(ctx) == "" {
			return nil, errors.New("archive requires an approved proposal")
		}
		var p protocol.SessionsArchiveRequest
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		if p.Restore || len(p.IDs) == 0 || p.Expected == nil {
			return nil, errors.New("archive proposal needs a pinned preview")
		}
	}
	if name == "project_add" || name == "project_remove" {
		return d.projectChange(ctx, name, raw, false)
	}
	if name == "merger_start" || name == "merger_stop" {
		if proposalActor(ctx) == "" {
			return nil, errors.New("merger requires an approved proposal")
		}
		var p protocol.SessionStartRequest
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		project, node, err := d.mergerProject(ctx, p)
		if err != nil {
			return nil, err
		}
		if name == "merger_stop" {
			return d.call(ctx, "merger.set", map[string]any{"project": project.Name, "node_id": node, "session_id": "", "configure": true, "by": proposalActor(ctx), "owner_session": d.ownerSessionID()})
		}
		p.Project, p.NodeID, p.Harness, p.Merger, p.CTOProposal = project.Name, node, "claude", true, true
		p.Folder, p.Branch, p.Create = "", "", false
		p.FirstMessage = MergerFirstMessage
		if p.Model == "" {
			p.Model = "claude-opus-5-5"
		}
		return d.sessionStart(ctx, p)
	}
	if name == "session_stop" || name == "session_resume" || name == "session_restart" {
		return d.ctoSessionControl(ctx, name, raw)
	}
	if name == "session_start" || name == "project_create" {
		if proposalActor(ctx) == "" {
			return nil, errors.New("session start requires an approved proposal")
		}
		var p protocol.SessionStartRequest
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		if name == "project_create" {
			p.Create = true
		}
		p.TrustClaude = false
		p.CTOProposal = true
		loc, _ := ctx.Value(proposalHerdrLocationKey{}).(proposalHerdrLocation)
		p.HerdrSocket, p.HerdrPaneID = loc.socket, loc.pane
		// The CTO asked for this start, so the CTO is told if it fails later.
		ctx = context.WithValue(ctx, startRequesterKey{}, d.ctoSessionID())
		return d.sessionStart(ctx, p)
	}
	if name == "message" {
		var q struct {
			Action  string `json:"action"`
			AsOwner bool   `json:"as_owner"`
			TeamID  string `json:"team_id"`
			To      string `json:"to"`
		}
		if json.Unmarshal(raw, &q) != nil || !q.AsOwner || proposalActor(ctx) == "" {
			return nil, errors.New("CTO authority requires an approved as-owner message")
		}
		if q.TeamID != "" {
			if q.Action != "send" {
				return nil, errors.New("team proposal must be a send")
			}
			graphRaw, err := d.call(ctx, "graph.read", protocol.GraphRead{Typed: true})
			if err != nil {
				return nil, err
			}
			var graph protocol.GraphGot
			if err := json.Unmarshal(graphRaw, &graph); err != nil {
				return nil, err
			}
			agentsRaw, err := d.call(ctx, "query.agents", protocol.QueryAgents{OnlineOnly: true})
			if err != nil {
				return nil, err
			}
			var page struct {
				Agents []protocol.AgentView `json:"agents"`
			}
			if err := json.Unmarshal(agentsRaw, &page); err != nil {
				return nil, err
			}
			ids, _, err := teamRecipients(graph.Graph, q.TeamID, page.Agents)
			if err != nil {
				return nil, err
			}
			if !ids[q.To] {
				return nil, errors.New("recipient is no longer an online team member")
			}
		}
		p, err := d.ctoMessage(ctx, q.Action, raw)
		if err != nil {
			return nil, err
		}
		p.FromCTO = true
		p.OwnerApproved = true
		return d.send(ctx, p)
	}
	return d.executeCTOTool(ctx, name, raw)
}

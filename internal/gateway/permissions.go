package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

// Claude Code permission grants (decision 0127, simplified). Only the owner's
// direct instruction grants a permission rule or a folder trust: the owner at
// the command line, or the CTO with his quoted words, which the daemon
// verifies. An agent that needs a permission asks the chief of staff or the
// CTO with a plain message. The gateway tells each target machine to apply,
// logs what each one did with the owner's words, and stores no grant.

// normalizePermissionKind treats an omitted kind as the permission-rule kind.
func normalizePermissionKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", protocol.PermissionKindRules:
		return protocol.PermissionKindRules
	case protocol.PermissionKindTrust:
		return protocol.PermissionKindTrust
	}
	return ""
}

// cleanPermissionRules trims the rules, refuses empty ones and drops exact
// repeats. It adds no other filtering.
func cleanPermissionRules(rules []string) ([]string, error) {
	out := make([]string, 0, len(rules))
	seen := map[string]bool{}
	for _, rule := range rules {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			return nil, reject("a permission rule is empty")
		}
		if seen[rule] {
			continue
		}
		seen[rule] = true
		out = append(out, rule)
	}
	if len(out) == 0 {
		return nil, reject("give at least one permission rule")
	}
	if raw, err := json.Marshal(out); err != nil {
		return nil, err
	} else if len(raw) > protocol.MaxBodyBytes {
		return nil, reject("permission rules are too long")
	}
	return out, nil
}

// cleanTrustPaths keeps exact absolute folder paths, or paths under ~, and
// drops exact repeats. The daemon that holds the folder checks that it exists
// and applies its own home rule, as the owner's folder trust already does.
func cleanTrustPaths(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			return nil, reject("a trust target is empty")
		}
		for _, r := range path {
			if unicode.IsControl(r) {
				return nil, reject("a trust path has a control character")
			}
		}
		if !filepath.IsAbs(path) && !strings.HasPrefix(path, "~/") {
			return nil, reject("folder %q must be absolute or start with ~/", path)
		}
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	if len(out) == 0 {
		return nil, reject("give at least one folder")
	}
	return out, nil
}

// resolvePermissionTargets turns the caller's repo or project names into
// registered machine and path rows. An unknown name is refused by name.
func (s *Store) resolvePermissionTargets(names []string) ([]protocol.PermissionTarget, error) {
	var out []protocol.PermissionTarget
	seen := map[string]bool{}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, reject("a permission target is empty")
		}
		rows, err := s.registeredProjects(name)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, reject("target %q does not resolve to a registered repo; register it with agent_project_add", name)
		}
		for _, p := range rows {
			key := p.NodeID + "\x00" + p.Path
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, protocol.PermissionTarget{Name: p.Name, NodeID: p.NodeID, Hostname: p.Hostname, Path: p.Path})
		}
	}
	if len(out) == 0 {
		return nil, reject("give at least one target")
	}
	return out, nil
}

// resolveTrustMachine turns the caller's machine name or node ID into one node.
func (s *Store) resolveTrustMachine(machine string) (nodeID, hostname string, err error) {
	id, err := s.NodeByHostOrID(strings.TrimSpace(machine))
	if err != nil {
		return "", "", err
	}
	return id, s.hostname(id), nil
}

// trustTargets makes one target per folder on the chosen machine.
func trustTargets(nodeID, hostname string, paths []string) []protocol.PermissionTarget {
	out := make([]protocol.PermissionTarget, 0, len(paths))
	for _, path := range paths {
		name := filepath.Base(path)
		if name == "." || name == string(filepath.Separator) || name == "~" {
			name = path
		}
		out = append(out, protocol.PermissionTarget{Name: name, NodeID: nodeID, Hostname: hostname, Path: path})
	}
	return out
}

// permissionRulesAndTargets checks the rules and resolves the targets of one
// grant kind.
func (s *Store) permissionRulesAndTargets(kind string, rawRules, rawTargets []string, machine string) ([]string, []protocol.PermissionTarget, error) {
	switch kind {
	case protocol.PermissionKindRules:
		if strings.TrimSpace(machine) != "" {
			return nil, nil, reject("permission rules take repo names, not a machine")
		}
		rules, err := cleanPermissionRules(rawRules)
		if err != nil {
			return nil, nil, err
		}
		targets, err := s.resolvePermissionTargets(rawTargets)
		if err != nil {
			return nil, nil, err
		}
		return rules, targets, nil
	case protocol.PermissionKindTrust:
		if strings.TrimSpace(machine) == "" {
			return nil, nil, reject("folder trust needs a machine")
		}
		for _, rule := range rawRules {
			if strings.TrimSpace(rule) != "" {
				return nil, nil, reject("folder trust takes no permission rules")
			}
		}
		nodeID, hostname, err := s.resolveTrustMachine(machine)
		if err != nil {
			return nil, nil, err
		}
		paths, err := cleanTrustPaths(rawTargets)
		if err != nil {
			return nil, nil, err
		}
		return nil, trustTargets(nodeID, hostname, paths), nil
	}
	return nil, nil, reject("kind must be permissions or trust")
}

// trustAskPrefix starts the source of a trust request to the chief. The
// machine and the folder follow, so one folder is asked about once.
const trustAskPrefix = "trust:"

// RaiseTrustAsk asks the chief of staff, with a plain request, to get the
// owner's yes for one folder's Claude Code trust after a session start,
// resume or wake answered needs_trust. servers names the folder's own
// .mcp.json servers the trust also approves. The chief is asked once per
// machine and folder. It writes nothing on the machine.
func (s *Store) RaiseTrustAsk(session, nodeID, hostname, path, project, reason string, servers []string) (bool, error) {
	path = strings.TrimSpace(path)
	nodeID = strings.TrimSpace(nodeID)
	if path == "" || !filepath.IsAbs(path) {
		return false, reject("a trust ask needs an absolute folder")
	}
	if nodeID == "" {
		return false, reject("a trust ask needs a machine")
	}
	if strings.TrimSpace(hostname) == "" {
		hostname = s.hostname(nodeID)
	}
	who := strings.TrimSpace(session)
	if who == "" {
		who = agentNetSender
	}
	name := "agent-net"
	if who != agentNetSender {
		name = sessionLabel(s.db, who)
	}
	if strings.TrimSpace(reason) == "" {
		reason = fmt.Sprintf("%s needs Claude Code folder trust for %s on %s", name, path, hostname)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Trust the Claude Code folder %s on %s?\n", path, hostname)
	fmt.Fprintf(&b, "Reason: %s\n", strings.TrimSpace(reason))
	if len(servers) > 0 {
		fmt.Fprintf(&b, "The trust also approves the folder's own MCP servers: %s\n", strings.Join(servers, ", "))
	}
	fmt.Fprintf(&b, "Why only the owner: Claude Code folder trust is the owner's to grant. Nothing is trusted before he says yes.\n")
	fmt.Fprintf(&b, "If the owner agrees, the CTO grants it with cto_op permission_apply (kind trust, machine %s, targets [%q]), quoting the owner's words in owner_words. A session already running there may need a restart.", hostname, path)
	_, sent, err := s.sendToChief(s.db, who, trustAskPrefix+nodeID+":"+path, b.String())
	if err != nil {
		return false, err
	}
	return sent, nil
}

// PermissionApplyTimeout is how long the gateway waits for one machine to
// apply one target.
const PermissionApplyTimeout = 20 * time.Second

// ownerApplyPermission is the owner operation permission_apply: the owner's
// verified words are the approval. It applies on each target machine, logs
// the outcome with his words, and returns it. It stores no grant.
func (g *Gateway) ownerApplyPermission(ctx context.Context, frame protocol.PermissionOwnerApplyFrame) (protocol.PermissionGrant, error) {
	req := frame.Request
	words := strings.TrimSpace(req.OwnerWords)
	if words == "" {
		return protocol.PermissionGrant{}, reject("owner_words is required: quote the owner's instruction from this chat")
	}
	if !frame.WordsVerified {
		return protocol.PermissionGrant{}, reject("owner_words were not verified: the owner must have typed them")
	}
	kind := normalizePermissionKind(req.Kind)
	if kind == "" {
		return protocol.PermissionGrant{}, reject("kind must be permissions or trust")
	}
	rules, targets, err := g.store.permissionRulesAndTargets(kind, req.Rules, req.Targets, req.Machine)
	if err != nil {
		return protocol.PermissionGrant{}, err
	}
	by := "owner"
	if frame.By == "cto" {
		by = "cto"
	}
	grant := protocol.PermissionGrant{ID: protocol.NewID("pg"), Kind: kind, Rules: rules, Targets: targets, By: by, OwnerWords: words}
	grant.Results = make([]protocol.PermissionApplyResult, 0, len(targets))
	for _, target := range targets {
		grant.Results = append(grant.Results, g.applyPermissionTarget(ctx, grant, target))
	}
	grant.Status = permissionGrantStatus(grant.Results)
	for _, r := range grant.Results {
		g.logLine("permission.apply", "grant", grant.ID, "kind", kind, "by", by, "node", r.Target.NodeID, "target", r.Target.Name,
			"path", r.Target.Path, "status", r.Status, "added", strings.Join(r.Added, ","), "trust_added", r.TrustAdded, "error", r.Error)
	}
	return grant, g.store.logPermissionGrant(grant)
}

// applyPermissionTarget tells one target machine to apply the grant.
func (g *Gateway) applyPermissionTarget(ctx context.Context, grant protocol.PermissionGrant, target protocol.PermissionTarget) protocol.PermissionApplyResult {
	res := protocol.PermissionApplyResult{Target: target, Kind: grant.Kind, Status: "failed"}
	nc := g.conn(target.NodeID)
	if nc == nil {
		res.Error = "machine is offline"
		return res
	}
	cctx, cancel := context.WithTimeout(ctx, PermissionApplyTimeout)
	defer cancel()
	raw, err := nc.call(cctx, protocol.FramePermissionApply, protocol.PermissionApplyFrame{
		GrantID: grant.ID, Kind: grant.Kind, Target: target, Rules: grant.Rules,
	})
	if err != nil {
		res.Error = err.Error()
		return res
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		res = protocol.PermissionApplyResult{Status: "failed", Error: "the machine answered without a result"}
	}
	res.Target, res.Kind = target, grant.Kind
	return res
}

// permissionGrantStatus is applied when every target applied, failed when
// none did, and partial in between.
func permissionGrantStatus(results []protocol.PermissionApplyResult) string {
	failed := 0
	for _, r := range results {
		if r.Status == "failed" {
			failed++
		}
	}
	switch {
	case failed == 0:
		return "applied"
	case failed == len(results):
		return "failed"
	}
	return "partial"
}

// logPermissionGrant writes the log line of one grant: what each target did
// and the owner's words that approved it.
func (s *Store) logPermissionGrant(grant protocol.PermissionGrant) error {
	if s.logf == nil {
		return nil
	}
	var detail strings.Builder
	var names []string
	for _, r := range grant.Results {
		detail.WriteString(permissionResultLine(r))
		detail.WriteString("\n")
		if r.Kind == protocol.PermissionKindTrust {
			names = append(names, r.Target.Path)
		} else {
			names = append(names, r.Target.Name)
		}
	}
	fmt.Fprintf(&detail, "Owner's words: %q", grant.OwnerWords)
	source := "gateway"
	if grant.By == "cto" {
		source = "cto"
	}
	s.logf("permission.grant", "id", grant.ID, "kind", grant.Kind, "status", grant.Status, "source", source, "targets", strings.Join(names, ","), "detail", detail.String())
	return nil
}

// permissionResultLine says what happened to one target, in the owner's words.
func permissionResultLine(r protocol.PermissionApplyResult) string {
	host := r.Target.Hostname
	if host == "" {
		host = r.Target.NodeID
	}
	if r.Kind == protocol.PermissionKindTrust {
		head := fmt.Sprintf("%s on %s: ", r.Target.Path, host)
		status := ""
		switch r.Status {
		case "applied":
			status = "trusted"
		case "already_present":
			status = "was already trusted"
		case "failed":
			return head + "failed: " + r.Error
		default:
			return head + r.Status
		}
		if len(r.MCPAdded) > 0 {
			status += ", approved its MCP servers: " + strings.Join(r.MCPAdded, ", ")
		}
		return head + status
	}
	head := fmt.Sprintf("%s on %s (%s): ", r.Target.Name, host, r.Target.Path)
	switch r.Status {
	case "applied":
		return head + fmt.Sprintf("applied, %d new rule(s)", len(r.Added))
	case "already_present":
		return head + "already present"
	case "failed":
		return head + "failed: " + r.Error
	}
	return head + r.Status
}

// raiseTrustAsk is the gateway's own ask after a start, resume or wake answered
// needs_trust. The reason names the session, the folder and the machine.
func (g *Gateway) raiseTrustAsk(session, nodeID, hostname, path, project, requester string, servers []string) {
	who := strings.TrimSpace(session)
	if who == "" {
		who = strings.TrimSpace(requester)
	}
	if who == "" {
		who = agentNetSender
	}
	label := "agent-net"
	if who != agentNetSender {
		label = sessionLabel(g.store.db, who)
	}
	if strings.TrimSpace(hostname) == "" {
		hostname = g.store.hostname(nodeID)
	}
	reason := fmt.Sprintf("%s needs Claude Code folder trust for %s on %s", label, path, hostname)
	if strings.TrimSpace(project) != "" {
		reason += " (project " + project + ")"
	}
	created, err := g.store.RaiseTrustAsk(who, nodeID, hostname, path, project, reason, servers)
	if err != nil {
		g.logLine("permission.trust_ask.failed", "node", nodeID, "path", path, "error", err.Error())
		return
	}
	if created {
		g.logLine("permission.trust_ask", "node", nodeID, "path", path, "session", who)
	}
}

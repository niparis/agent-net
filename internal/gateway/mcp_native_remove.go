package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// After an import the native entry and the federated server both exist. The
// owner may remove the native entry. The machine that owns the config file
// makes the edit; the gateway keeps the plan, the audit and the hashes. It
// removes an entry only when its server was imported and still passes a test.
// Undo restores the entry, and only if the file is still what the edit wrote.

func (s *Store) auditNative(item, plan, action, actor string, c protocol.MCPImportCandidate, state, conflict, result string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t := []protocol.ToolPlanTarget{{NodeID: c.NodeID, Machine: c.Machine, Harness: c.Harness, Project: c.Project,
		Source: c.Location, Action: "native_remove", State: state, Conflict: conflict}}
	if err = auditToolTx(tx, item, plan, action, actor, 0, t, result, s.now().UnixNano(), nil); err != nil {
		return err
	}
	return tx.Commit()
}

func (g *Gateway) nativeEdit(ctx context.Context, c protocol.MCPImportCandidate, req protocol.MCPNativeEditRequest) (protocol.MCPNativeEditResult, error) {
	var res protocol.MCPNativeEditResult
	host := g.conn(c.NodeID)
	if host == nil {
		return res, errors.New("machine offline; the file cannot be edited")
	}
	raw, err := host.request(ctx, "node.mcp_native_edit", req, 30*time.Second)
	if err != nil {
		return res, err
	}
	if err = json.Unmarshal(raw, &res); err != nil {
		return res, errors.New("invalid answer from the machine")
	}
	return res, nil
}

func sameServer(a, b protocol.MCPServer) bool {
	norm := func(s protocol.MCPServer) []any {
		args, env := s.Args, s.Env
		if len(args) == 0 {
			args = nil
		}
		if len(env) == 0 {
			env = nil
		}
		h := s.Headers
		if len(h) == 0 {
			h = nil
		}
		return []any{s.Transport, s.Command, args, env, s.URL, h}
	}
	return reflect.DeepEqual(norm(a), norm(b))
}

// nativeRemoveChecks says what stops the removal of this native entry. It
// returns plain reasons; none holds a value from the file.
func (g *Gateway) nativeRemoveChecks(ctx context.Context, e nativeMCP, failed map[string]bool) (server string, conflicts []string, err error) {
	c := e.candidate
	name := protocol.MCPServerName(c.Name)
	imports, err := g.store.MCPImports()
	if err != nil {
		return "", nil, err
	}
	rec, ok := imports[name]
	if !ok || rec.Name != c.Name || rec.NodeID != c.NodeID || rec.Harness != c.Harness || rec.Scope != c.Scope || rec.Project != c.Project || rec.Location != c.Location {
		return name, []string{"this entry was not imported; import it first"}, nil
	}
	srv, found, err := g.store.MCPServer(name)
	if err != nil {
		return name, nil, err
	}
	switch {
	case !found:
		conflicts = append(conflicts, "the federated server is no longer registered")
	case !srv.Enabled:
		conflicts = append(conflicts, "the federated server is switched off")
	case srv.Health != protocol.HealthOK:
		conflicts = append(conflicts, "the federated server is not healthy; test it first")
	case len(srv.Tools) == 0:
		conflicts = append(conflicts, "the federated server reports no tools")
	}
	if failed[c.NodeID] {
		conflicts = append(conflicts, "fresh inventory scan unavailable on "+c.Machine)
	}
	if found {
		spec, err := g.importSpec(ctx, c)
		switch {
		case err != nil:
			conflicts = append(conflicts, err.Error())
		case spec.Info.Class != protocol.MCPImportable || !sameServer(spec.Server, srv):
			conflicts = append(conflicts, "the native entry no longer matches the federated server")
		}
	}
	sort.Strings(conflicts)
	return name, conflicts, nil
}

func (g *Gateway) planMCPNativeRemove(ctx context.Context, key, actor string) (out protocol.MCPNativeRemovePlan, err error) {
	if actor != "owner" && actor != "cto" {
		return out, errors.New("owner authority required")
	}
	failed, err := g.refreshToolObservations(ctx)
	if err != nil {
		return out, err
	}
	e, err := g.findCandidate(key)
	if err != nil {
		return out, err
	}
	c := e.candidate
	server, conflicts, err := g.nativeRemoveChecks(ctx, e, failed)
	if err != nil {
		return out, err
	}
	var fileHash string
	if len(conflicts) == 0 {
		res, err := g.nativeEdit(ctx, c, protocol.MCPNativeEditRequest{Op: protocol.MCPNativePlan, Harness: c.Harness, Scope: c.Scope, Project: c.Project, Location: c.Location, Name: c.Name})
		switch {
		case err != nil:
			conflicts = append(conflicts, err.Error())
		case res.Conflict != "":
			conflicts = append(conflicts, res.Conflict)
		default:
			fileHash = res.FileHash
		}
	}
	snap, err := g.store.ToolSnapshot()
	if err != nil {
		return out, err
	}
	out = protocol.MCPNativeRemovePlan{ID: protocol.NewID("plan"), Candidate: c, Server: server, FileHash: fileHash, NativeHash: e.hash,
		Gaps: inventory.FederationGaps(c.Machine), Conflicts: append([]string{}, conflicts...), State: "pending", Actor: actor, At: g.store.now().UnixNano()}
	raw, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	if _, err = g.store.db.Exec(`INSERT INTO mcp_native_plans(id,snapshot,plan) VALUES(?,?,?)`, out.ID, snap, string(raw)); err != nil {
		return out, err
	}
	state := "pending"
	if len(conflicts) > 0 {
		state = "conflict"
	}
	return out, g.store.auditNative(nativeID(c.Name, e.hash), out.ID, "mcp_native_plan", actor, c, state, strings.Join(conflicts, "; "), "proposed")
}

func (g *Gateway) loadNativePlan(id string) (p protocol.MCPNativeRemovePlan, snapshot string, err error) {
	var raw string
	if err = g.store.db.QueryRow(`SELECT snapshot,plan,state FROM mcp_native_plans WHERE id=?`, id).Scan(&snapshot, &raw, &p.State); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return p, "", reject("plan not found")
		}
		return p, "", err
	}
	state := p.State
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return p, "", reject("invalid plan")
	}
	p.State = state
	return p, snapshot, nil
}

func (g *Gateway) saveNativePlan(p protocol.MCPNativeRemovePlan) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = g.store.db.Exec(`UPDATE mcp_native_plans SET plan=?,state=? WHERE id=?`, string(raw), p.State, p.ID)
	return err
}

func (g *Gateway) applyMCPNativeRemove(ctx context.Context, id, actor, owner string) (protocol.MCPNativeRemoveResult, error) {
	var out protocol.MCPNativeRemoveResult
	if actor != "owner" && actor != "cto" {
		return out, errors.New("owner authority required")
	}
	g.importMu.Lock()
	defer g.importMu.Unlock()
	p, snapshot, err := g.loadNativePlan(id)
	if err != nil {
		return out, err
	}
	c := p.Candidate
	item := nativeID(c.Name, p.NativeHash)
	refuse := func(why string) (protocol.MCPNativeRemoveResult, error) {
		_ = g.store.auditNative(item, id, "mcp_native_remove", actor, c, "refused", why, "refused")
		return protocol.MCPNativeRemoveResult{}, reject("%s", why)
	}
	if p.State != "pending" {
		return refuse("plan already applied")
	}
	if len(p.Conflicts) > 0 {
		return refuse("plan has conflicts: " + strings.Join(p.Conflicts, "; "))
	}
	failed, err := g.refreshToolObservations(ctx)
	if err != nil {
		return out, err
	}
	current, err := g.store.ToolSnapshot()
	if err != nil {
		return out, err
	}
	if current != snapshot {
		return refuse("plan out of date")
	}
	e, err := g.findCandidate(c.Key)
	if err != nil || e.hash != p.NativeHash {
		return refuse("plan out of date")
	}
	server, conflicts, err := g.nativeRemoveChecks(ctx, e, failed)
	if err != nil {
		return out, err
	}
	if len(conflicts) > 0 {
		return refuse("plan out of date: " + strings.Join(conflicts, "; "))
	}
	// The federated server must work right now, not only when it was imported.
	res, err := g.mcpTest(ctx, server)
	if err != nil {
		return refuse("the federated server test could not run: " + err.Error())
	}
	if m, _ := res.(map[string]any); m == nil || m["ok"] != true {
		return refuse("the federated server fails its test; the native entry stays")
	}
	// A plan applies once, whatever happens next.
	p.State = "applying"
	if err = g.saveNativePlan(p); err != nil {
		return out, err
	}
	edit, err := g.nativeEdit(ctx, c, protocol.MCPNativeEditRequest{Op: protocol.MCPNativeApply, Harness: c.Harness, Scope: c.Scope, Project: c.Project,
		Location: c.Location, Name: c.Name, FileHash: p.FileHash})
	out = protocol.MCPNativeRemoveResult{PlanID: id, Server: server}
	if err != nil || edit.Conflict != "" {
		why := edit.Conflict
		if err != nil {
			why = err.Error()
		}
		p.State = "refused"
		_ = g.saveNativePlan(p)
		out.State, out.Reason = protocol.MCPNativeRefused, why
		_ = g.store.auditNative(item, id, "mcp_native_remove", actor, c, "conflict", why, "refused")
		return out, nil
	}
	p.State, p.PostHash, p.BackupID = "applied", edit.PostHash, edit.BackupID
	if err = g.saveNativePlan(p); err != nil {
		return out, err
	}
	g.notifyToolNodes(ctx, []protocol.ToolPlanTarget{{NodeID: c.NodeID}})
	out.State = protocol.MCPNativeRemoved
	out.Notes = []string{"the native entry is removed; its file was backed up on " + c.Machine,
		"a running session keeps its native tools until it restarts; the federated tools reach it through the gateway"}
	out.Notified = g.noticeNativeRemoved(ctx, c, server, id, owner)
	if len(out.Notified) > 0 {
		out.Notes = append(out.Notes, fmt.Sprintf("told %d running session(s) on %s once to restart and drop the old copy", len(out.Notified), c.Machine))
	}
	return out, g.store.auditNative(item, id, "mcp_native_remove", actor, c, "applied", "", "applied")
}

func (g *Gateway) undoMCPNativeRemove(ctx context.Context, id, actor string) (protocol.MCPNativeRemoveResult, error) {
	var out protocol.MCPNativeRemoveResult
	if actor != "owner" && actor != "cto" {
		return out, errors.New("owner authority required")
	}
	g.importMu.Lock()
	defer g.importMu.Unlock()
	p, _, err := g.loadNativePlan(id)
	if err != nil {
		return out, err
	}
	c := p.Candidate
	item := nativeID(c.Name, p.NativeHash)
	if p.State != "applied" {
		_ = g.store.auditNative(item, id, "mcp_native_undo", actor, c, "refused", "nothing to undo", "refused")
		return out, reject("only an applied removal can be undone")
	}
	out = protocol.MCPNativeRemoveResult{PlanID: id, Server: p.Server}
	edit, err := g.nativeEdit(ctx, c, protocol.MCPNativeEditRequest{Op: protocol.MCPNativeUndo, Name: c.Name, PostHash: p.PostHash, BackupID: p.BackupID})
	if err != nil || edit.Conflict != "" {
		why := edit.Conflict
		if err != nil {
			why = err.Error()
		}
		out.State, out.Reason = protocol.MCPNativeRefused, why
		_ = g.store.auditNative(item, id, "mcp_native_undo", actor, c, "conflict", why, "refused")
		return out, nil
	}
	p.State = "restored"
	if err = g.saveNativePlan(p); err != nil {
		return out, err
	}
	g.notifyToolNodes(ctx, []protocol.ToolPlanTarget{{NodeID: c.NodeID}})
	out.State = protocol.MCPNativeRestored
	out.Notes = []string{fmt.Sprintf("the native entry is back; the federated server %q is still registered, so the same server exists twice. Remove one of them.", p.Server)}
	return out, g.store.auditNative(item, id, "mcp_native_undo", actor, c, "applied", "", "restored")
}

// noticeNativeRemoved tells the running sessions that could still hold the
// removed native entry to restart. They are the online sessions of the entry's
// harness on its machine: all of them for a user-level entry, the sessions of
// its project for a project-level one. Each gets one message from the owner
// session. A failure to tell a session never fails the removal.
func (g *Gateway) noticeNativeRemoved(ctx context.Context, c protocol.MCPImportCandidate, server, planID, owner string) []string {
	if owner == "" {
		return nil
	}
	project := ""
	if c.Scope == protocol.InventoryProject {
		project = c.Project
	}
	ids, err := g.store.sessionsHoldingNative(c.NodeID, c.Harness, project)
	if err != nil {
		g.logLine("error", "where", "native remove notice", "err", err)
		return nil
	}
	body := fmt.Sprintf("agent-net: the MCP server %q moved to agent-net; restart this session to drop the old copy. "+
		"Its native entry was removed, and this session still holds it, so the same tools may appear twice until then. "+
		"The agent-net tools keep working. No reply is needed.", server)
	var told []string
	for _, sid := range ids {
		m, created, err := g.store.StoreMessage(protocol.MsgSend{Sender: owner, Recipient: sid, Type: protocol.TypeRequest, Body: body,
			IdempotencyKey: "native-remove:" + planID + ":" + sid})
		if err != nil {
			g.logLine("error", "where", "native remove notice", "session", sid, "err", err)
			continue
		}
		if created && m.State != protocol.StateHeld {
			g.dispatch(ctx, m)
		}
		told = append(told, sid)
	}
	sort.Strings(told)
	return told
}

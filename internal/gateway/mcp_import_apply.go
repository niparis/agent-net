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

	"github.com/niparis/agent-net/internal/protocol"
)

// An import plan names one native entry and says what registering it would do.
// Applying it registers the server on its machine, disabled; tests it; compares
// the tools it reports with the native entry; and only then enables it with the
// scope of the native entry. If anything fails the server is removed again and
// nothing else has changed. The harness config file is never touched.

// nativeID is the tool item ID of a native MCP entry.
func nativeID(name, hash string) string {
	b, _ := json.Marshal([]string{protocol.InventoryMCP, name, hash})
	return string(b)
}

func importTarget(c protocol.MCPImportCandidate, state, conflict string) []protocol.ToolPlanTarget {
	return []protocol.ToolPlanTarget{{NodeID: c.NodeID, Machine: c.Machine, Harness: c.Harness, Project: c.Project,
		Source: c.Location, Action: "import", State: state, Conflict: conflict}}
}

func (s *Store) auditImport(item, plan, action, actor string, revision int64, targets []protocol.ToolPlanTarget, result string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = auditToolTx(tx, item, plan, action, actor, revision, targets, result, s.now().UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}

func (g *Gateway) importSpec(ctx context.Context, c protocol.MCPImportCandidate) (protocol.MCPImportSpec, error) {
	var spec protocol.MCPImportSpec
	host := g.conn(c.NodeID)
	if host == nil {
		return spec, errors.New("machine offline; the native entry cannot be read")
	}
	raw, err := host.request(ctx, "node.mcp_import_spec", protocol.MCPImportSpecRequest{Harness: c.Harness, Scope: c.Scope, Project: c.Project, Location: c.Location, Name: c.Name}, 30*time.Second)
	if err != nil {
		return spec, err
	}
	if err = json.Unmarshal(raw, &spec); err != nil {
		return spec, errors.New("invalid answer from the machine")
	}
	return spec, nil
}

// findCandidate returns the native entry a key names.
func (g *Gateway) findCandidate(key string) (nativeMCP, error) {
	entries, err := g.store.nativeMCPEntries(func(id string) bool { return g.conn(id) != nil })
	if err != nil {
		return nativeMCP{}, err
	}
	for _, e := range entries {
		if e.candidate.Key == key {
			return e, nil
		}
	}
	return nativeMCP{}, reject("import candidate not found")
}

// importBuild reads the entry again on its machine and works out the server and
// scope, plus anything that stops the import.
func (g *Gateway) importBuild(ctx context.Context, e nativeMCP, failed map[string]bool) (protocol.MCPServer, protocol.ToolAssignment, []string, error) {
	c := e.candidate
	var conflicts []string
	if c.Class != protocol.MCPImportable {
		return protocol.MCPServer{}, protocol.ToolAssignment{}, nil, reject("this entry cannot be imported: %s", c.Reason)
	}
	if failed[c.NodeID] {
		conflicts = append(conflicts, "fresh inventory scan unavailable on "+c.Machine)
	}
	var server protocol.MCPServer
	spec, err := g.importSpec(ctx, c)
	switch {
	case err != nil:
		conflicts = append(conflicts, err.Error())
	case spec.Info.Class != protocol.MCPImportable:
		return server, protocol.ToolAssignment{}, nil, reject("this entry cannot be imported: %s", spec.Info.Reason)
	case spec.Hash != e.hash:
		conflicts = append(conflicts, "the native entry changed since the scan")
	default:
		server = spec.Server
		server.NodeID, server.Hostname, server.Enabled = c.NodeID, c.Machine, false
		server.Health, server.Tools = protocol.HealthUnknown, nil
	}
	assign := protocol.ToolAssignment{Scope: "global"}
	if c.Scope == protocol.InventoryProject {
		assign = protocol.ToolAssignment{Scope: "projects", Projects: []string{c.Project}}
		var exists int
		if err = g.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM projects WHERE name=?)`, c.Project).Scan(&exists); err != nil {
			return server, assign, nil, err
		}
		if exists == 0 {
			conflicts = append(conflicts, fmt.Sprintf("project %q is not registered", c.Project))
		}
	}
	if taken, _, err := g.store.federatedNamed(protocol.MCPServerName(c.Name)); err != nil {
		return server, assign, nil, err
	} else if taken {
		conflicts = append(conflicts, fmt.Sprintf("a federated server named %q already exists", protocol.MCPServerName(c.Name)))
	}
	sort.Strings(conflicts)
	return server, assign, conflicts, nil
}

func (g *Gateway) planMCPImport(ctx context.Context, key, actor string) (out protocol.MCPImportPlan, err error) {
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
	server, assign, conflicts, err := g.importBuild(ctx, e, failed)
	if err != nil {
		return out, err
	}
	snap, err := g.store.ToolSnapshot()
	if err != nil {
		return out, err
	}
	c := e.candidate
	c.ServerName = protocol.MCPServerName(c.Name)
	out = protocol.MCPImportPlan{ID: protocol.NewID("plan"), Candidate: c, Server: server, Assignment: assign,
		NativeHash: e.hash, Conflicts: append([]string{}, conflicts...), State: "pending", Actor: actor, At: g.store.now().UnixNano()}
	if c.Class == protocol.MCPImportable {
		// The tools the native entry lists, when it lists any.
		if info := g.expectedTools(c); info != nil {
			out.ExpectedTools = info
		}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	if _, err = g.store.db.Exec(`INSERT INTO mcp_import_plans(id,snapshot,plan) VALUES(?,?,?)`, out.ID, snap, string(raw)); err != nil {
		return out, err
	}
	state := "pending"
	if len(conflicts) > 0 {
		state = "conflict"
	}
	return out, g.store.auditImport(nativeID(c.Name, e.hash), out.ID, "mcp_import_plan", actor, 0, importTarget(c, state, strings.Join(conflicts, "; ")), "proposed")
}

// expectedTools finds the tool allow-list the native entry states.
func (g *Gateway) expectedTools(c protocol.MCPImportCandidate) []string {
	reports, err := g.store.Inventories()
	if err != nil {
		return nil
	}
	for _, r := range reports {
		for _, obs := range r.Items {
			if r.NodeID == c.NodeID && obs.Harness == c.Harness && obs.Scope == c.Scope && obs.Project == c.Project && obs.Location == c.Location && obs.Name == c.Name && obs.Import != nil {
				return obs.Import.ExpectedTools
			}
		}
	}
	return nil
}

// finishMCPImport enables the tested server, gives it the native entry's scope,
// records where it came from and audits it, in one transaction.
func (s *Store) finishMCPImport(p protocol.MCPImportPlan, actor, session string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	name := p.Server.Name
	var item string
	if err = tx.QueryRow(`SELECT i.id FROM tool_identities i JOIN mcp_servers m ON m.name=i.name
		AND i.kind='mcp' AND i.hash='federated-'||m.added_at WHERE m.name=?`, name).Scan(&item); err != nil {
		return 0, errors.New("the server is no longer registered")
	}
	now := s.now().UnixNano()
	if _, err = tx.Exec(`UPDATE mcp_servers SET enabled=1 WHERE name=?`, name); err != nil {
		return 0, err
	}
	r, err := assignToolTx(tx, item, p.Assignment, actor, "import:"+p.ID, now)
	if err != nil {
		return 0, err
	}
	c := p.Candidate
	rec, _ := json.Marshal(protocol.MCPImportRecord{Server: name, NodeID: c.NodeID, Machine: c.Machine, Harness: c.Harness, Scope: c.Scope,
		Project: c.Project, Location: c.Location, Name: c.Name, PlanID: p.ID, Actor: actor, At: now, NativeStillPresent: true})
	if _, err = tx.Exec(`INSERT INTO mcp_imports(name,record) VALUES(?,?)`, name, string(rec)); err != nil {
		return 0, err
	}
	if err = auditToolTx(tx, item, p.ID, "mcp_import", actor, r.Revision, importTarget(c, "applied", ""), "applied", now); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	s.Event("mcp.enabled", c.NodeID, session, "", name)
	return r.Revision, nil
}

func (g *Gateway) applyMCPImport(ctx context.Context, id, actor, session string) (protocol.MCPImportResult, error) {
	var out protocol.MCPImportResult
	if actor != "owner" && actor != "cto" {
		return out, errors.New("owner authority required")
	}
	g.importMu.Lock()
	defer g.importMu.Unlock()
	var snapshot, raw string
	var applied int
	if err := g.store.db.QueryRow(`SELECT snapshot,plan,applied FROM mcp_import_plans WHERE id=?`, id).Scan(&snapshot, &raw, &applied); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, reject("plan not found")
		}
		return out, err
	}
	var p protocol.MCPImportPlan
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return out, reject("invalid plan")
	}
	refuse := func(why string) (protocol.MCPImportResult, error) {
		_ = g.store.auditImport(nativeID(p.Candidate.Name, p.NativeHash), id, "mcp_import", actor, 0, importTarget(p.Candidate, "refused", why), "refused")
		return protocol.MCPImportResult{}, reject("%s", why)
	}
	if applied != 0 {
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
	e, err := g.findCandidate(p.Candidate.Key)
	if err != nil {
		return refuse("plan out of date")
	}
	server, assign, conflicts, err := g.importBuild(ctx, e, failed)
	if err != nil || len(conflicts) > 0 || !reflect.DeepEqual(server, p.Server) || !reflect.DeepEqual(assign, p.Assignment) || e.hash != p.NativeHash {
		return refuse("plan out of date")
	}
	// A plan applies once, whatever happens next.
	if _, err = g.store.db.Exec(`UPDATE mcp_import_plans SET applied=1 WHERE id=?`, id); err != nil {
		return out, err
	}

	name := server.Name
	out = protocol.MCPImportResult{PlanID: id, Server: name, Assignment: assign, NativeStillPresent: true}
	native := nativeID(p.Candidate.Name, p.NativeHash)
	fail := func(reason string) (protocol.MCPImportResult, error) {
		g.store.RemoveMCPServer(name, p.Candidate.NodeID, session)
		out.State, out.Reason = protocol.MCPImportFailed, reason
		if len(reason) > 300 {
			reason = reason[:300]
		}
		err := g.store.auditImport(native, id, "mcp_import", actor, 0, importTarget(p.Candidate, "failed", reason), "failed")
		return out, err
	}
	server.AddedByNode, server.AddedBySession, server.AddedByHarness = p.Candidate.NodeID, session, protocol.OwnerHarness
	if err = g.store.AddMCPServerDisabled(server); err != nil {
		return out, err
	}
	res, err := g.mcpTest(ctx, name)
	if err != nil {
		return fail("the test could not run: " + err.Error())
	}
	m, _ := res.(map[string]any)
	if ok, _ := m["ok"].(bool); !ok {
		detail, _ := m["detail"].(string)
		return fail("the test failed: " + detail)
	}
	srv, _, err := g.store.MCPServer(name)
	if err != nil {
		return out, err
	}
	have := map[string]bool{}
	for _, t := range srv.Tools {
		have[t.Name] = true
		out.Tools = append(out.Tools, t.Name)
	}
	if len(srv.Tools) == 0 {
		return fail("the server reports no tools; the federation carries tools only")
	}
	if len(p.ExpectedTools) > 0 {
		var missing []string
		for _, t := range p.ExpectedTools {
			if !have[t] {
				missing = append(missing, t)
			}
		}
		if len(missing) > 0 {
			return fail("the server does not report the tools the native entry lists: " + strings.Join(missing, ", "))
		}
		out.Compared = fmt.Sprintf("all %d tools the native entry lists are present", len(p.ExpectedTools))
	} else {
		out.Compared = fmt.Sprintf("the native entry lists no tools, so the tool list was not compared; the server reports %d tools", len(srv.Tools))
	}
	if _, err = g.store.finishMCPImport(p, actor, session); err != nil {
		g.store.RemoveMCPServer(name, p.Candidate.NodeID, session)
		return out, err
	}
	out.State = protocol.MCPImportOK
	g.mcpChanged(ctx, p.Candidate.NodeID)
	return out, nil
}

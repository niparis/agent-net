package gateway

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Import of native MCP servers. The scanners on each machine judge every native
// entry (names and reasons only). This file lists those entries as candidates
// and, in mcp_import_apply.go, moves one into the federation on the owner's
// word. Nothing here writes a harness config file.

// importKey names one native entry: node, harness, scope, project, file, name.
func importKey(node, harness, scope, project, location, name string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{node, harness, scope, project, location, name}, "\x00")))
	return hex.EncodeToString(sum[:8])
}

// MCPImports returns the import records by server name.
func (s *Store) MCPImports() (map[string]protocol.MCPImportRecord, error) {
	rows, err := s.db.Query(`SELECT i.record FROM mcp_imports i JOIN mcp_servers m ON m.name=i.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]protocol.MCPImportRecord{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var r protocol.MCPImportRecord
		if err = json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, err
		}
		out[r.Server] = r
	}
	return out, rows.Err()
}

// nativeMCP is one native MCP entry seen on a machine.
type nativeMCP struct {
	candidate protocol.MCPImportCandidate
	hash      string
}

// nativeMCPEntries lists the native MCP entries of every machine. offline marks
// a machine the gateway is not connected to.
func (s *Store) nativeMCPEntries(online func(string) bool) ([]nativeMCP, error) {
	reports, err := s.Inventories()
	if err != nil {
		return nil, err
	}
	var out []nativeMCP
	for _, r := range reports {
		for _, obs := range r.Items {
			if obs.Kind != protocol.InventoryMCP || obs.Source == protocol.InventoryFederation || obs.Harness == "agent-net" {
				continue
			}
			if obs.Name == "" {
				continue
			}
			c := protocol.MCPImportCandidate{
				Key:    importKey(r.NodeID, obs.Harness, obs.Scope, obs.Project, obs.Location, obs.Name),
				NodeID: r.NodeID, Machine: r.Machine, Harness: obs.Harness, Scope: obs.Scope, Project: obs.Project,
				Location: obs.Location, Name: obs.Name, State: obs.State,
			}
			if !online(r.NodeID) && c.State == protocol.InventoryOK {
				c.State = protocol.InventoryStale
			}
			switch {
			case obs.Import != nil:
				c.Class, c.Reason, c.Transport, c.EnvNames = obs.Import.Class, obs.Import.Reason, obs.Import.Transport, obs.Import.EnvNames
			case len(obs.Unsupported) > 0 && obs.Source != protocol.InventoryNative:
				c.Class, c.Reason = protocol.MCPUnsupported, obs.Unsupported[0]
			default:
				c.Class, c.Reason = protocol.MCPUnsupported, "this machine's agent-net does not judge MCP entries yet"
			}
			if c.Class == protocol.MCPImportable {
				c.ServerName = protocol.MCPServerName(obs.Name)
			}
			out = append(out, nativeMCP{candidate: c, hash: obs.Hash})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].candidate, out[j].candidate
		ka := strings.Join([]string{a.Machine, a.NodeID, a.Harness, a.Scope, a.Project, a.Name, a.Location}, "\x00")
		kb := strings.Join([]string{b.Machine, b.NodeID, b.Harness, b.Scope, b.Project, b.Name, b.Location}, "\x00")
		return ka < kb
	})
	return out, nil
}

// mcpImportCandidates lists the native entries with their verdict. A candidate
// that already became a federated server says so; one whose name another
// federated server holds carries a conflict.
func (g *Gateway) mcpImportCandidates(f protocol.MCPImportCandidatesRequest) (protocol.MCPImportCandidatesPage, error) {
	page := protocol.MCPImportCandidatesPage{Candidates: []protocol.MCPImportCandidate{}}
	if f.Offset < 0 {
		return page, errors.New("invalid offset")
	}
	entries, err := g.store.nativeMCPEntries(func(id string) bool { return g.conn(id) != nil })
	if err != nil {
		return page, err
	}
	imports, err := g.store.MCPImports()
	if err != nil {
		return page, err
	}
	servers, err := g.store.MCPServers()
	if err != nil {
		return page, err
	}
	registered := map[string]bool{}
	for _, s := range servers {
		registered[s.Name] = true
	}
	var all []protocol.MCPImportCandidate
	for _, e := range entries {
		c := e.candidate
		if (f.Machine != "" && c.Machine != f.Machine && c.NodeID != f.Machine) || (f.Harness != "" && c.Harness != f.Harness) ||
			(f.Project != "" && c.Project != f.Project) || (f.Class != "" && c.Class != f.Class) {
			continue
		}
		name := protocol.MCPServerName(c.Name)
		if rec, ok := imports[name]; ok && rec.Name == c.Name && rec.NodeID == c.NodeID && rec.Harness == c.Harness && rec.Scope == c.Scope && rec.Project == c.Project && rec.Location == c.Location {
			c.Imported, c.NativeStillPresent = true, true
			c.ServerName = name
			c.Conflict = fmt.Sprintf("imported as %q; the native entry is still present", name)
		} else if registered[name] {
			c.Conflict = fmt.Sprintf("a federated server named %q already exists", name)
		}
		all = append(all, c)
	}
	page.Total = len(all)
	if f.Offset > len(all) {
		return page, errors.New("invalid offset")
	}
	end := f.Offset + protocol.MCPImportMaxCandidates
	if end < len(all) {
		page.Next = end
	} else {
		end = len(all)
	}
	page.Candidates = append(page.Candidates, all[f.Offset:end]...)
	return page, nil
}

// nativeMCPNames maps a registry server name to the places a native entry of
// that name lives, as "machine (harness)".
func (s *Store) nativeMCPNames() (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT l.node_id,COALESCE(n.hostname,''),l.observation FROM tool_locations l
		LEFT JOIN nodes n ON n.id=l.node_id WHERE l.identity_id LIKE '["mcp",%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var node, host, raw string
		if err = rows.Scan(&node, &host, &raw); err != nil {
			return nil, err
		}
		var obs protocol.InventoryItem
		if err = json.Unmarshal([]byte(raw), &obs); err != nil {
			return nil, err
		}
		if obs.Name == "" || obs.Source == protocol.InventoryFederation || obs.Harness == "agent-net" {
			continue
		}
		if host == "" {
			host = node
		}
		name := protocol.MCPServerName(obs.Name)
		out[name] = appendUnique(out[name], host+" ("+obs.Harness+")")
	}
	return out, rows.Err()
}

// markMCPConflicts reports a native entry and a federated server that share a
// name, on both items. The tool listing then never shows them as one server.
func (s *Store) markMCPConflicts(all map[string]*protocol.ToolItem) error {
	native, err := s.nativeMCPNames()
	if err != nil {
		return err
	}
	imports, err := s.MCPImports()
	if err != nil {
		return err
	}
	for _, item := range all {
		if item.Kind != protocol.InventoryMCP {
			continue
		}
		if item.Federated {
			places := native[item.Name]
			if rec, ok := imports[item.Name]; ok {
				r := rec
				r.NativeStillPresent = len(places) > 0
				item.Imported, item.NativeStillPresent = &r, r.NativeStillPresent
			}
			if len(places) > 0 {
				sort.Strings(places)
				item.Conflicts = append(item.Conflicts, "a native entry with this name is still present on "+strings.Join(places, ", ")+"; the same server is registered twice")
			}
			continue
		}
		if isFed, _, _ := s.federatedNamed(protocol.MCPServerName(item.Name)); isFed {
			item.Conflicts = append(item.Conflicts, fmt.Sprintf("a federated server named %q exists; this native entry duplicates it", protocol.MCPServerName(item.Name)))
		}
	}
	return nil
}

// federatedNamed reports whether a registry server has this name.
func (s *Store) federatedNamed(name string) (bool, string, error) {
	var n string
	err := s.db.QueryRow(`SELECT name FROM mcp_servers WHERE name=?`, name).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	return err == nil, n, err
}

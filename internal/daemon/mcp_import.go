package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// importSpec reads one native MCP entry of this machine again, read-only, and
// answers with the server it describes. Only an entry the last scan saw is
// read, and the project root comes from that scan, not from the request. No
// harness config file is written.
func (d *Daemon) importSpec(p protocol.MCPImportSpecRequest) (protocol.MCPImportSpec, error) {
	if d.cfg.InventoryHome == "" {
		return protocol.MCPImportSpec{}, errors.New("inventory scanner is disabled")
	}
	d.toolsMu.Lock()
	var root string
	found := false
	for _, v := range d.toolsReport.Items {
		if v.Kind == protocol.InventoryMCP && v.Harness == p.Harness && v.Scope == p.Scope && v.Project == p.Project && v.Location == p.Location && v.Name == p.Name {
			root, found = v.ProjectRoot, true
			break
		}
	}
	d.toolsMu.Unlock()
	if !found {
		return protocol.MCPImportSpec{}, errors.New("the native entry is no longer observed")
	}
	o := inventory.Options{Home: d.cfg.InventoryHome, CodexHome: d.cfg.CodexHome}
	info, server, hash, err := inventory.LoadNativeMCP(o, p.Harness, p.Scope, root, p.Location, p.Name)
	if err != nil {
		return protocol.MCPImportSpec{}, errors.New("the native entry cannot be read again")
	}
	return protocol.MCPImportSpec{Server: server, Info: info, Hash: hash}, nil
}

func (d *Daemon) onMCPImportSpec(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.MCPImportSpecRequest
	var result protocol.MCPImportSpec
	err := json.Unmarshal(raw, &p)
	if err == nil {
		result, err = d.importSpec(p)
	}
	if b, e := protocol.EncodeResult(h.ID, result, err); e == nil {
		_ = l.write(ctx, b)
	}
}

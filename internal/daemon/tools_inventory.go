package daemon

import (
	"context"
	"encoding/json"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func (d *Daemon) scanTools(ctx context.Context) protocol.InventoryReport {
	d.toolsMu.Lock()
	defer d.toolsMu.Unlock()
	var projects []protocol.ProjectEntry
	data, err := d.call(ctx, "query.projects", struct{}{})
	if err == nil {
		var result protocol.ProjectsResult
		err = json.Unmarshal(data, &result)
		if err == nil {
			for _, p := range result.Projects {
				if p.NodeID == d.cfg.NodeID && (p.Source == protocol.SourceRegistered || p.Source == protocol.SourceBoth) {
					projects = append(projects, protocol.ProjectEntry{Name: p.Name, Path: p.Path})
				}
			}
		}
	}
	report := inventory.Scan(inventory.Options{Home: d.cfg.InventoryHome, CodexHome: d.cfg.CodexHome, Machine: d.cfg.Hostname, Projects: projects})
	markTrackedSkills(&report)
	if data, listErr := d.call(ctx, "mcp.list", struct{}{}); listErr == nil {
		var list struct {
			Servers []protocol.MCPServer `json:"servers"`
		}
		if json.Unmarshal(data, &list) == nil {
			for _, server := range list.Servers {
				if server.NodeID == d.cfg.NodeID {
					report.Items = append(report.Items, inventory.FederationItem(server, report.ScannedAt))
				}
			}
		}
	}
	if err != nil {
		report.State = protocol.InventoryError
		report.Reason = "project registry unavailable"
	}
	// Keep fresh items when only some sources failed. One unreadable or
	// linked-out skill must not throw away every other fresh observation.
	fresh := report
	report = inventory.MergeReports(d.toolsReport, fresh)
	d.toolsReport = report
	// Send the scan as it came so the gateway applies the same merge to its
	// own stored report.
	_, _ = d.call(ctx, "node.inventory", fresh)
	result := d.toolsReport
	result.Items = append([]protocol.InventoryItem{}, result.Items...)
	return result
}

func (d *Daemon) toolsLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	d.recoverSkillJournal()
	d.pruneSkillBackups()
	version := d.helloVersion()
	if d.st != nil {
		if previous := d.st.inventoryScanVersion(); previous != "" && previous != version {
			d.Logf("inventory: rescanning after upgrade from %s to %s", previous, version)
		}
	}
	d.scanTools(ctx)
	if d.st != nil {
		if err := d.st.setInventoryScanVersion(version); err != nil {
			d.Logf("inventory: cannot remember the scan version: %v", err)
		}
	}
	d.reconcileTools(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.scanTools(ctx)
			d.reconcileTools(ctx)
		case <-d.toolsWake:
			d.scanTools(ctx)
			d.reconcileTools(ctx)
		}
	}
}

func (d *Daemon) wakeTools() {
	if d.toolsWake != nil {
		select {
		case d.toolsWake <- struct{}{}:
		default:
		}
	}
}

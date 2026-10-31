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
	if report.State == protocol.InventoryError && len(d.toolsReport.Items) > 0 {
		previous := d.toolsReport
		previous.Items = append([]protocol.InventoryItem(nil), previous.Items...)
		for i := range previous.Items {
			previous.Items[i].State = protocol.InventoryStale
			previous.Items[i].Reason = "latest scan failed"
		}
		previous.State = protocol.InventoryStale
		previous.Reason = "latest scan failed"
		previous.ScannedAt = report.ScannedAt
		d.toolsReport = previous
	} else {
		d.toolsReport = report
	}
	// Send the failure itself so the gateway preserves its previous records.
	_, _ = d.call(ctx, "node.inventory", report)
	result := d.toolsReport
	result.Items = append([]protocol.InventoryItem(nil), result.Items...)
	return result
}

func (d *Daemon) toolsLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	d.scanTools(ctx)
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

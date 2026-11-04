package inventory

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// PlanReportProblem distinguishes a fresh, bounded partial scan from a machine
// whose inventory cannot be trusted at all. A partial scan retains every failed
// source, so callers must also check PlanItemProblem for the item they touch.
func PlanReportProblem(r protocol.InventoryReport) string {
	if r.State != protocol.InventoryOK && r.Reason != "one or more sources could not be scanned" {
		if r.Reason != "" {
			return "inventory scan incomplete: " + r.Reason
		}
		return "inventory scan incomplete"
	}
	if r.ScannedAt.IsZero() || time.Since(r.ScannedAt) > 15*time.Minute {
		return "inventory report is missing or too old; rescan the machine"
	}
	if r.State == protocol.InventoryOK {
		return ""
	}
	if r.Reason == "one or more sources could not be scanned" {
		for _, item := range r.Items {
			if item.State == protocol.InventoryOK {
				return ""
			}
		}
	}
	for _, item := range r.Items {
		if reason := PlanItemProblem(r, item.Kind, item.Name, item.Location); reason != "" {
			return "inventory scan incomplete: " + reason
		}
	}
	return "inventory scan incomplete: " + r.Reason
}

// PlanItemProblem refuses the same name, any unknown source root that could
// hide it, and overlapping source folders (including observed entry aliases).
func PlanItemProblem(r protocol.InventoryReport, kind, name string, sources ...string) string {
	for _, item := range r.Items {
		if item.State == protocol.InventoryOK || item.Kind != kind {
			continue
		}
		touches := item.Name == name || item.Name == ""
		for _, path := range append([]string{item.Location}, item.Aliases...) {
			if path == "" {
				continue
			}
			for _, source := range sources {
				if source != "" && (within(path, source) || within(source, path)) {
					touches = true
				}
			}
		}
		if touches {
			label := item.Name
			if label == "" {
				label = item.Location
			}
			if item.Project != "" {
				label += " (" + item.Project + ")"
			} else if item.Name != "" && item.Location != "" {
				label += " (" + item.Location + ")"
			}
			reason := item.Reason
			if reason == "" {
				reason = "latest scan failed"
			}
			return fmt.Sprintf("%s cannot be read safely: %s", label, reason)
		}
	}
	return ""
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

package tui

import (
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

// Each manifest describes one copy. Sum its files, then take the largest
// observed copy, rather than counting the same files on several machines.
func skillItemBytes(item protocol.ToolItem) int64 {
	var largest int64
	for _, loc := range item.Locations {
		if loc.Manifest == nil {
			continue
		}
		var total int64
		for _, file := range loc.Manifest.Files {
			total += max(0, file.Size)
		}
		largest = max(largest, total)
	}
	return largest
}
func skillSizeText(size int64) string {
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(size)/(1<<20)), ".0") + " MB"
}
func skillSizeReason(size int64) string {
	if size <= inventory.MaxBundleBytes {
		return ""
	}
	return fmt.Sprintf("too large to manage (%s, the limit is %s)", skillSizeText(size), skillSizeText(inventory.MaxBundleBytes))
}

// skillTooLarge reports a copy whose scan stopped listing files because the
// skill was too large: its manifest has no file sizes to add up.
func skillTooLarge(item protocol.ToolItem) bool {
	for _, loc := range item.Locations {
		if loc.Manifest != nil && loc.Manifest.TooLarge {
			return true
		}
	}
	return false
}

// tooLargeReason is the lock text when the scan gave up before the size was known.
func tooLargeReason() string {
	return fmt.Sprintf("too large to manage (over 200 files or %s)", skillSizeText(inventory.MaxBundleBytes))
}

// skillScanLockReason names scan failures without treating an offline copy as
// unreadable. A project switch checks only its checkouts and applicable user
// copies. An empty project checks all copies. Offline observations keep their
// ordinary pending behavior.
func (m Model) skillScanLockReason(r toolsdiff.Row, project string) string {
	if r.Kind != protocol.InventorySkill {
		return ""
	}
	nodes := map[string]bool{}
	if project != "" {
		for _, checkout := range m.projectRows() {
			if checkout.Name == project {
				nodes[checkout.NodeID] = true
			}
		}
	}
	items := append([]protocol.ToolItem{r.Item}, r.Variants...)
	items = append(items, m.tl.items...)
	for _, item := range items {
		if item.Kind != protocol.InventorySkill || item.Name != r.Name {
			continue
		}
		for _, loc := range item.Locations {
			if project != "" {
				if !nodes[loc.NodeID] {
					continue
				}
				if loc.Scope == protocol.InventoryProject && loc.Project != project && loc.GraphNode != project {
					continue
				}
			}
			if loc.State != protocol.InventoryError && !(loc.State == protocol.InventoryStale && loc.Reason != "") {
				continue
			}
			reason := orStr(loc.Reason, "inventory could not read this skill")
			where := orStr(loc.Machine, loc.NodeID)
			if loc.Project != "" {
				where += "/" + loc.Project
			}
			if where != "" {
				reason += " [" + where + "]"
			}
			return "cannot manage: " + reason
		}
	}
	return ""
}

func (m Model) skillLockReason(r toolsdiff.Row, project string) string {
	if reason := m.skillScanLockReason(r, project); reason != "" {
		return reason
	}
	size, large := skillItemBytes(r.Item), skillTooLarge(r.Item)
	for _, v := range r.Variants {
		size = max(size, skillItemBytes(v))
		large = large || skillTooLarge(v)
	}
	if reason := skillSizeReason(size); reason != "" || !large {
		return reason
	}
	return tooLargeReason()
}
func (m Model) skillCopyLockReason(itemID string) string {
	var size int64
	large := false
	for _, it := range m.tl.items {
		if it.ID == itemID {
			if reason := m.skillScanLockReason(toolsdiff.Row{Kind: it.Kind, Name: it.Name, Item: it}, ""); reason != "" {
				return reason
			}
			size = max(size, skillItemBytes(it))
			large = large || skillTooLarge(it)
		}
	}
	if reason := skillSizeReason(size); reason != "" || !large {
		return reason
	}
	return tooLargeReason()
}

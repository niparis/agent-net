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
func (m Model) skillLockReason(r toolsdiff.Row) string {
	size := skillItemBytes(r.Item)
	for _, v := range r.Variants {
		size = max(size, skillItemBytes(v))
	}
	return skillSizeReason(size)
}
func (m Model) skillCopyLockReason(itemID string) string {
	var size int64
	for _, it := range m.tl.items {
		if it.ID == itemID {
			size = max(size, skillItemBytes(it))
		}
	}
	return skillSizeReason(size)
}

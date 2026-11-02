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

func (m Model) skillLockReason(r toolsdiff.Row) string {
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
			size = max(size, skillItemBytes(it))
			large = large || skillTooLarge(it)
		}
	}
	if reason := skillSizeReason(size); reason != "" || !large {
		return reason
	}
	return tooLargeReason()
}

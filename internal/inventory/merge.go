package inventory

import (
	"os"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// MergeReports keeps fresh observations when only some sources failed. A scan
// that produced no successful item at all keeps the previous report, marked
// stale, because a failed scan must not erase old records. A fresh item that
// failed keeps the previous observation for that exact source, marked stale,
// so one unreadable or linked-out skill no longer throws away every other
// fresh observation (and its file manifest). A failed source root keeps the
// previous skills below it, also marked stale.
func MergeReports(previous, fresh protocol.InventoryReport) protocol.InventoryReport {
	if fresh.State == protocol.InventoryOK {
		return fresh
	}
	okItems := 0
	for _, item := range fresh.Items {
		if item.State == protocol.InventoryOK {
			okItems++
		}
	}
	if okItems == 0 {
		out := fresh
		out.Items = append([]protocol.InventoryItem(nil), fresh.Items...)
		out.State = protocol.InventoryStale
		if out.Reason == "" {
			out.Reason = "latest scan failed"
		}
		for _, old := range previous.Items {
			found := false
			for i, failed := range out.Items {
				if itemKey(old) == itemKey(failed) {
					old.State, old.Reason = protocol.InventoryStale, failed.Reason
					out.Items[i] = old
					found = true
					break
				}
			}
			if !found {
				old.State = protocol.InventoryStale
				if old.Reason == "" {
					old.Reason = out.Reason
				}
				out.Items = append(out.Items, old)
			}
		}
		return out
	}
	out := fresh
	out.Items = append([]protocol.InventoryItem(nil), fresh.Items...)
	seen := map[string]bool{}
	for _, item := range out.Items {
		seen[itemKey(item)] = true
	}
	for _, failed := range fresh.Items {
		if failed.State != protocol.InventoryError {
			continue
		}
		if failed.Name != "" {
			if old, ok := previousItem(previous, failed); ok {
				for i := range out.Items {
					if itemKey(out.Items[i]) == itemKey(failed) {
						out.Items[i] = old
						out.Items[i].State = protocol.InventoryStale
						out.Items[i].Reason = failed.Reason
					}
				}
			}
			continue
		}
		if failed.Location == "" {
			continue
		}
		prefix := failed.Location + string(os.PathSeparator)
		for _, old := range previous.Items {
			if old.State == protocol.InventoryError || old.Kind != failed.Kind || old.Harness != failed.Harness || old.Scope != failed.Scope || old.Project != failed.Project {
				continue
			}
			if old.Location != failed.Location && !strings.HasPrefix(old.Location, prefix) {
				continue
			}
			key := itemKey(old)
			if seen[key] {
				continue
			}
			seen[key] = true
			old.State = protocol.InventoryStale
			old.Reason = failed.Reason
			out.Items = append(out.Items, old)
		}
	}
	// Some sources are fresh and some are not: say so, but keep the fresh
	// observations rather than replacing the whole report.
	out.State = protocol.InventoryStale
	out.Reason = fresh.Reason
	if out.Reason == "" {
		out.Reason = PartialScanReason
	}
	return out
}

func itemKey(item protocol.InventoryItem) string {
	return item.Kind + "\x00" + item.Harness + "\x00" + item.Scope + "\x00" + item.Project + "\x00" + item.Name + "\x00" + item.Location
}

// previousItem finds the last successful observation of the same source.
func previousItem(previous protocol.InventoryReport, item protocol.InventoryItem) (protocol.InventoryItem, bool) {
	for _, old := range previous.Items {
		if old.State == protocol.InventoryError {
			continue
		}
		if old.Kind == item.Kind && old.Name == item.Name && old.Location == item.Location &&
			old.Harness == item.Harness && old.Scope == item.Scope && old.Project == item.Project {
			return old, true
		}
	}
	return protocol.InventoryItem{}, false
}

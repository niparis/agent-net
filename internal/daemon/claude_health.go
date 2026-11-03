package daemon

import "github.com/niparis/agent-net/internal/protocol"

// checkClaudeDelivery runs on each inventory refresh, at startup, after managed
// updates, and when periodic detection sees an external version change. Recheck
// even the same version so a repaired socket clears a previous warning.
func (d *Daemon) checkClaudeDelivery(list []protocol.HarnessInfo) {
	checker, ok := d.adapters["claude"].(interface{ CheckDelivery(string) string })
	if !ok {
		return
	}
	for i := range list {
		if list[i].Name == "claude" {
			list[i].DeliveryWarning = checker.CheckDelivery(list[i].Version)
		}
	}
}

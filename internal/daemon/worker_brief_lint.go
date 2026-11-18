package daemon

import "github.com/niparis/agent-net/internal/protocol"

// logBriefFlags writes one line for a held start, the only start the gateway
// never sees. A gateway start is logged by the gateway with the same fields.
func (d *Daemon) logBriefFlags(project string, flags []protocol.BriefFlag) {
	d.Logf("worker.brief_lint project=%s flags=%d checks=%s", project, len(flags), protocol.BriefCheckNames(flags))
}

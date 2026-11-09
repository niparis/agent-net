package protocol

// Request origins. The daemon tags every frame it sends to the gateway with
// the kind of caller that made the request (Header.Via). The gateway counts
// requests by method and origin so the owner can see where its work comes from
// (for example, how much is the TUI's one-second polling). The tag is only a
// label for counting: it is never used for a decision or a permission.
const (
	ViaTUI     = "tui"     // the owner's TUI
	ViaCLI     = "cli"     // another command line client on the local socket
	ViaMCP     = "mcp"     // the tools of an agent session
	ViaWorker  = "worker"  // the tools of a worker session
	ViaCTO     = "cto"     // the CTO session
	ViaDaemon  = "daemon"  // the daemon's own loops
	ViaPhone   = "phone"   // the phone app
	ViaApp     = "app"     // a connected app
	ViaUnknown = "unknown" // an older daemon, or a tag not in this list
)

// CleanVia returns v when it is a known origin, else "unknown". An empty tag
// is an older daemon. The gateway bounds the label set with this, so a node
// cannot grow the counters.
func CleanVia(v string) string {
	switch v {
	case ViaTUI, ViaCLI, ViaMCP, ViaWorker, ViaCTO, ViaDaemon, ViaPhone, ViaApp:
		return v
	}
	return ViaUnknown
}

// RequestStat is the work of one (method, origin) pair.
type RequestStat struct {
	Method  string  `json:"method"`
	Origin  string  `json:"origin"`
	Calls   int64   `json:"calls"`
	TotalMs float64 `json:"total_ms"`
	MaxMs   float64 `json:"max_ms"`
	// Bytes is the size of the responses written for these requests.
	Bytes int64 `json:"bytes"`
}

// RequestOriginTotal is the work of one origin across all methods.
type RequestOriginTotal struct {
	Origin  string  `json:"origin"`
	Calls   int64   `json:"calls"`
	TotalMs float64 `json:"total_ms"`
	Bytes   int64   `json:"bytes"`
}

// RequestWindow is the request counters of one time span. Top holds the 30
// pairs with the most total handling time, biggest first. Other holds what the
// counters could not list by name.
type RequestWindow struct {
	Seconds  int                  `json:"seconds"`
	Calls    int64                `json:"calls"`
	TotalMs  float64              `json:"total_ms"`
	Bytes    int64                `json:"bytes"`
	ByOrigin []RequestOriginTotal `json:"by_origin"`
	Top      []RequestStat        `json:"top"`
}

// GatewayRequests is the gateway's request counters, kept in memory only. It
// is shown by agent_machine_get next to the usage reading. Counts start at the
// gateway start and are gone when it restarts.
type GatewayRequests struct {
	StartedAt string        `json:"started_at"`
	Since     RequestWindow `json:"since_start"`
	Last10Min RequestWindow `json:"last_10_min"`
}

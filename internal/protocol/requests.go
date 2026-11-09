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
	// ClientErrors counts the requests that failed on the TUI or web console
	// side. It is empty until one is reported.
	ClientErrors *ClientErrorCounters `json:"client_errors,omitempty"`
}

// Client errors. The TUI and the web console report each request that failed
// or timed out on their side. The daemon writes one log line for it and tells
// the gateway, which counts it per method and origin and shows the counters
// next to the request counters (agent_machine_get). The daemon cannot see a
// timeout that ends on the client's side, so without this report it was
// invisible.
const (
	// OpClientError is the local socket method a client uses to report one
	// failed request. It answers nothing and needs no owner gate: it only
	// writes a log line and bumps a counter.
	OpClientError = "client_error"
	// FrameClientError is the frame the daemon sends to the gateway for it.
	FrameClientError = "client.error"
	// ClientErrorMaxText bounds every text field of a report.
	ClientErrorMaxText = 120
)

// ClientError is one request that failed on the client side.
type ClientError struct {
	// Method is the operator method that failed, for example "proposal_list".
	Method string `json:"method"`
	// Ms is how long the client waited before it gave up.
	Ms int64 `json:"ms"`
	// TimeoutMs is the deadline the client set, 0 when it set none.
	TimeoutMs int64 `json:"timeout_ms"`
	// Timeout is true when the request ended by its deadline.
	Timeout bool `json:"timeout,omitempty"`
	// Screen names the tab or view that made the request.
	Screen string `json:"screen,omitempty"`
	// Err is a short text of the error.
	Err string `json:"err,omitempty"`
}

// ClientErrorStat counts the failures of one (method, origin) pair.
type ClientErrorStat struct {
	Method   string  `json:"method"`
	Origin   string  `json:"origin"`
	Errors   int64   `json:"errors"`
	Timeouts int64   `json:"timeouts"`
	TotalMs  float64 `json:"total_ms"`
	MaxMs    float64 `json:"max_ms"`
}

// ClientErrorCounters are the client error counters, since the gateway started
// and for the last ten minutes. Rows with the most errors come first.
type ClientErrorCounters struct {
	Since     []ClientErrorStat `json:"since_start"`
	Last10Min []ClientErrorStat `json:"last_10_min"`
}

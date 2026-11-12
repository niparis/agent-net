package protocol

// FrameNodeSleep is a daemon reporting a sleep span to the gateway. A daemon
// cannot send it while the machine sleeps, so it sends it when it wakes, or
// when it can announce the sleep first.
const FrameNodeSleep = "node.sleep"

// NodeSleep is one span in which a machine slept. From and To are Unix
// nanoseconds. To is 0 while the machine still reports the sleep as ongoing.
// Source says how the daemon learned it: "pmset" on macOS, "journal" on Linux,
// or "clock" when it came from a jump in the wall clock against the monotonic
// clock.
type NodeSleep struct {
	From   int64  `json:"from"`
	To     int64  `json:"to,omitempty"`
	Source string `json:"source,omitempty"`
}

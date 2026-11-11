package protocol

// Local test slots. A merger asks the daemon for the machine's one test slot
// before it runs a suite, so suites obey the same machine admission as
// workers and only one runs at a time per machine.

// OpTestSlot is the local socket method that asks for a test slot.
const OpTestSlot = "test_slot"

// TestSlotRequest asks for the machine's test slot. Cwd is the folder the
// suite runs in; it names the repo whose merger.test_timeout applies.
type TestSlotRequest struct {
	Cwd string `json:"cwd,omitempty"`
}

// TestSlotResult answers a test slot request. Admitted is false while another
// suite runs; Position is the caller's place in the queue, 1 first. Parallel
// is the number of packages to build or test at once. TimeoutSeconds is the
// wall-clock limit from merger.test_timeout.
type TestSlotResult struct {
	Admitted       bool   `json:"admitted"`
	Position       int    `json:"position,omitempty"`
	Parallel       int    `json:"parallel,omitempty"`
	TimeoutSeconds int64  `json:"timeout_seconds,omitempty"`
	Hostname       string `json:"hostname,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

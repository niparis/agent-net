// Package usage measures CPU and memory of the machine and of process trees.
//
// A Reader takes one snapshot of the machine: every process with its parent,
// resident memory and the CPU time it has used so far, plus the machine
// totals. The Sampler takes a snapshot every few seconds, turns the CPU time
// differences into percentages and keeps a rolling window, so it can report
// both the last value and the average of the last minutes.
//
// Linux reads /proc. macOS runs ps, sysctl and vm_stat, because the program is
// built without cgo.
package usage

import (
	"context"
	"time"
)

// Proc is one process in a snapshot.
type Proc struct {
	PID, PPID int
	// Start identifies the process together with PID, so a reused PID is not
	// taken for the old process. Its unit does not matter. 0 when unknown.
	Start uint64
	// CPU is the CPU time the process and the children it has already waited
	// for have used, in seconds.
	CPU float64
	// RSS is the resident memory in bytes.
	RSS uint64
	// Comm is the short name of the program.
	Comm string
}

// Machine is the machine side of a snapshot.
type Machine struct {
	Cores int
	// CPUBusy and CPUTotal are the cumulative CPU time of all cores, busy and
	// total, in any one unit. Both are 0 when the platform gives no such
	// counter; the sampler then adds up the process times.
	CPUBusy, CPUTotal float64
	MemUsed, MemTotal uint64
	Load              [3]float64
}

// Snapshot is one reading.
type Snapshot struct {
	At    time.Time
	Procs map[int]Proc
	M     Machine
}

// Reader takes snapshots.
type Reader interface {
	Read(ctx context.Context) (Snapshot, error)
	// Args returns the full command line of the given processes (missing
	// ones are left out). It is only called for the few processes a caller
	// asks about.
	Args(ctx context.Context, pids []int) map[int]string
}

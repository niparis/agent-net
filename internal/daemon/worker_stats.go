package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Run facts the harness reports for a notice.

// workerStats is what the harness reports about a worker's run.
type workerStats struct {
	Tokens string
	Cost   string
	Model  string
	// ContextTokens is the tokens the model saw on its last step, ContextLimit
	// the model's context window, both 0 when the harness cannot say.
	ContextTokens int64
	ContextLimit  int64
	// TotalTokens is input plus output plus reasoning for the run, no cache
	// reads. 0 when the harness cannot say.
	TotalTokens int64
	// Usage is the same run by kind of token. Zero when the harness cannot say.
	Usage protocol.RunTokens
}

// workerStats asks the launcher, with a short time limit. When the harness
// cannot say, it keeps the last complete context pair for notices and views.
func (d *Daemon) workerStats(w *worker) workerStats {
	var st workerStats
	if ln, ok := d.launchers[w.harness].(interface {
		stats(ctx context.Context, d *Daemon, w *worker) workerStats
	}); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		st = ln.stats(ctx, d, w)
	}
	d.wmu.Lock()
	defer d.wmu.Unlock()
	return lastWorkerContext(w, st)
}

// lastWorkerContext keeps the last complete context pair through failed reads
// and turns that have not reported usage yet. A real value may be lower after
// compaction, so this is not a high-water mark. A harness without a context
// window for the model still keeps the tokens it reports: with no limit, a
// token count alone is the whole picture. The caller holds d.wmu.
func lastWorkerContext(w *worker, st workerStats) workerStats {
	if st.ContextLimit <= 0 && w.contextLimit <= 0 && st.ContextTokens > 0 {
		return st
	}
	if st.ContextTokens <= 0 || st.ContextLimit <= 0 {
		st.ContextTokens, st.ContextLimit = w.contextTokens, w.contextLimit
	}
	return st
}

// tokenText formats token counts, or "" when there are none.
func tokenText(in, out, reasoning, cacheRead, cacheWrite float64) string {
	if in+out+reasoning+cacheRead+cacheWrite == 0 {
		return ""
	}
	s := fmt.Sprintf("%.0f in, %.0f out", in, out)
	if reasoning > 0 {
		s += fmt.Sprintf(", %.0f reasoning", reasoning)
	}
	if cacheRead+cacheWrite > 0 {
		s += fmt.Sprintf(", %.0f cache read, %.0f cache write", cacheRead, cacheWrite)
	}
	return s
}

// trimWorkerReport bounds a report kept in a notice or an idle report, so one
// long message cannot grow the state database without limit.
func trimWorkerReport(report string) string {
	if len(report) > 48*1024 {
		return strings.ToValidUTF8(report[:48*1024], "") + "\n[report cut at 48 KB]"
	}
	return report
}

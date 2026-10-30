package daemon

import (
	"time"

	"github.com/niparis/agent-net/internal/usage"
)

// What a worker costs. When a worker ends, the daemon stores the average and
// peak CPU and memory of its process tree, per harness and model, and keeps
// the last protocol.WorkerCostKeep. The expected cost of the next worker is
// the 90th percentile of those (decision 0018).

// minCostLife is the shortest run that counts: a worker that ended within a
// few seconds was not measured.
const minCostLife = 10 * time.Second

// expectedCost is the cost to plan for a new worker of this harness and
// model. Empty harness means every harness (the machine's general cost). The
// memory figure is the 90th percentile of the peaks; the CPU figure is the
// 90th percentile of the averages. With no history it is 600 MB and one core.
func (d *Daemon) expectedCost(harness, model string) WorkerCost {
	cs, err := d.st.costs(harness, model)
	if err != nil || len(cs) == 0 {
		return defaultWorkerCost()
	}
	var rss, cpu []float64
	for _, c := range cs {
		rss = append(rss, c.PeakRSS)
		cpu = append(cpu, c.AvgCPU)
	}
	return WorkerCost{RAM: uint64(percentile90(rss)), Cores: percentile90(cpu) / 100, Learned: true}
}

// recordWorkerCost stores the measured life of a worker's process tree. A
// worker with no memory figure was never measured and is not stored.
func (d *Daemon) recordWorkerCost(harness, model string, life usage.Life, ran time.Duration) {
	if ran < minCostLife || life.RSSPeak <= 0 {
		return
	}
	err := d.st.addCost(workerCost{Harness: harness, Model: model, AvgCPU: life.CPUAvg, PeakCPU: life.CPUPeak,
		AvgRSS: life.RSSAvg, PeakRSS: life.RSSPeak, EndedAt: time.Now()})
	if err != nil {
		d.Logf("worker cost: %v", err)
	}
}

// treeLife returns what the sampler measured of the process tree of pid, and
// false when it did not follow it.
func (d *Daemon) treeLife(pid int) (usage.Life, bool) {
	s := d.cfg.UsageSampler
	if s == nil || pid <= 0 {
		return usage.Life{}, false
	}
	t, ok := s.Trees()[pid]
	if !ok {
		return usage.Life{}, false
	}
	return t.Life, true
}

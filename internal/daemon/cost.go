package daemon

import (
	"time"

	"github.com/niparis/agent-net/internal/usage"
)

// What a worker costs. When a worker ends, the daemon stores the average and
// peak CPU and memory of its process tree, per harness and model, and keeps
// the last protocol.WorkerCostKeep. The expected cost of the next worker is
// the median of the averages, with the RAM capped at a share of the machine
// (decision 0018). The peak is kept for display only: workers do not peak at
// the same time, so a peak is not what a new worker adds.

// minCostLife is the shortest run that counts: a worker that ended within a
// few seconds was not measured.
const minCostLife = 10 * time.Second

// minCostSamples is how many ended workers the daemon needs before it trusts
// the learned cost.
const minCostSamples = 2

// maxCostShare is the largest share of the machine's RAM that the expected
// cost of one worker can be.
const maxCostShare = 0.25

// expectedCost is the cost to plan for a new worker of this harness and
// model. Empty harness means every harness (the machine's general cost).
func (d *Daemon) expectedCost(harness, model string) WorkerCost {
	return d.expectedCostWith(harness, model, d.load().Total)
}

// expectedCostWith is expectedCost when the caller already read the machine's
// total RAM, so a start with several pending workers reads the load once.
func (d *Daemon) expectedCostWith(harness, model string, total uint64) WorkerCost {
	cs, err := d.st.costs(harness, model)
	if err != nil {
		return defaultWorkerCost()
	}
	return learnedCost(cs, total)
}

// learnedCost is the median of the average RSS and of the average CPU of the
// ended workers. The RAM is capped at maxCostShare of total (when known). With
// fewer than minCostSamples workers it is the default.
func learnedCost(cs []workerCost, total uint64) WorkerCost {
	if len(cs) < minCostSamples {
		return defaultWorkerCost()
	}
	var rss, cpu []float64
	for _, c := range cs {
		rss = append(rss, c.AvgRSS)
		cpu = append(cpu, c.AvgCPU)
	}
	ram := median(rss)
	if total > 0 {
		ram = min(ram, maxCostShare*float64(total))
	}
	return WorkerCost{RAM: uint64(ram), Cores: median(cpu) / 100, Learned: true}
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

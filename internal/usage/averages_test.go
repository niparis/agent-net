package usage

import (
	"context"
	"testing"
)

// The 1-minute and 5-minute averages of whole-machine CPU and memory in use.
func TestSamplerAveragesOverOneAndFiveMinutes(t *testing.T) {
	// 40 samples, 10 s apart: busy 20% for the first 20, then 80%.
	var snaps []Snapshot
	busy, total := 0.0, 0.0
	for i := 0; i < 40; i++ {
		pct := 20.0
		used := uint64(2 << 30)
		if i >= 20 {
			pct = 80
			used = 6 << 30
		}
		busy += pct
		total += 100
		s := snapAt(i*10, busy, total, [4]float64{}, [4]uint64{})
		s.M.MemUsed = used
		snaps = append(snaps, s)
	}
	s, _ := newFake(snaps...)
	for range snaps {
		s.Sample(context.Background())
	}
	a1 := s.Averages(60e9)
	a5 := s.Averages(300e9)
	// The last minute is the last 7 samples (60 s span, both ends included): all at 80%.
	near(t, "cpu 1 min", a1.CPU, 80, 1e-9)
	near(t, "mem used 1 min", a1.MemUsed, float64(6<<30), 1)
	// Samples older than 5 minutes are gone: 31 are left, 11 at 20% and 20 at 80%.
	near(t, "cpu 5 min", a5.CPU, (11*20.0+20*80.0)/31, 1e-9)
	near(t, "mem used 5 min", a5.MemUsed, (11*float64(2<<30)+20*float64(6<<30))/31, 1)
	if a1.Samples < 6 || a5.Samples != 31 {
		t.Errorf("samples: %d, %d", a1.Samples, a5.Samples)
	}
	if a5.MemTotal != 8<<30 {
		t.Errorf("mem total %d", a5.MemTotal)
	}
}

func TestSamplerAveragesBeforeAnySample(t *testing.T) {
	s, _ := newFake(snapAt(0, 1, 2, [4]float64{}, [4]uint64{}))
	if a := s.Averages(60e9); a.Samples != 0 {
		t.Fatalf("%+v", a)
	}
}

// A followed tree keeps its whole-life average and peak, not only the window.
func TestSamplerKeepsLifeAverageAndPeakOfATree(t *testing.T) {
	s, _ := newFake(
		snapAt(0, 100, 400, [4]float64{0, 0, 0, 0}, [4]uint64{100, 0, 0, 0}),
		snapAt(5, 200, 800, [4]float64{5, 0, 0, 0}, [4]uint64{300, 0, 0, 0}),   // 100% of one core
		snapAt(10, 300, 1200, [4]float64{5, 0, 0, 0}, [4]uint64{200, 0, 0, 0}), // 0%
	)
	for i := 0; i < 3; i++ {
		s.Sample(context.Background())
	}
	ts := s.Trees()[100]
	near(t, "life cpu avg", ts.Life.CPUAvg, 50, 1e-9)
	near(t, "life cpu peak", ts.Life.CPUPeak, 100, 1e-9)
	near(t, "life rss avg", ts.Life.RSSAvg, 200, 1e-9)
	near(t, "life rss peak", ts.Life.RSSPeak, 300, 1e-9)
}

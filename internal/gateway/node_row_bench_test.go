package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// BenchmarkNodeRowWrites measures the writes a machine's hello makes and the
// read the machine list makes. The fold of the node side tables moves the hello
// writes onto the nodes row, so the numbers before and after the fold are the
// check of that move.
func BenchmarkNodeRowWrites(b *testing.B) {
	s, _ := newTestStore(b)
	node, _, err := s.CreateNode("bench-host", "linux")
	if err != nil {
		b.Fatal(err)
	}
	harnesses := []protocol.HarnessInfo{{Name: "claude", Version: "2.1"}}
	health := &protocol.MachineHealth{Status: "reported", RestartTime: "2026-11-20T08:00:00Z"}
	launch := true

	b.Run("hello", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := s.SetNodeTimeZone(node, "Europe/Paris"); err != nil {
				b.Fatal(err)
			}
			if err := s.SetNodeTailscaleIP(node, "100.64.0.7"); err != nil {
				b.Fatal(err)
			}
			if _, err := s.SetNodeHarnesses(node, harnesses); err != nil {
				b.Fatal(err)
			}
			if err := s.SetNodeHealth(node, health); err != nil {
				b.Fatal(err)
			}
			if err := s.SetNodeLaunch(node, &launch); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("machine_list", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := s.Nodes(); err != nil {
				b.Fatal(err)
			}
		}
	})
}

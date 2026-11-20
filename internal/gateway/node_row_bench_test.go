package gateway

import (
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// benchInventory is an inventory of n skills with a 2 KB description each. Two
// machines in production held 1.3 MB of inventory, so 300 items is about the
// size of one machine.
func benchInventory(n int) protocol.InventoryReport {
	pad := strings.Repeat("x", 2000)
	items := make([]protocol.InventoryItem, n)
	for i := range items {
		items[i] = protocol.InventoryItem{Kind: protocol.InventorySkill, Name: fmt.Sprintf("skill-%d", i),
			Harness: "claude", Scope: protocol.InventoryUser, Source: "user", Location: fmt.Sprintf("/home/u/.claude/skills/s%d", i),
			Hash: fmt.Sprintf("h%d", i), State: protocol.InventoryOK, Description: pad}
	}
	return protocol.InventoryReport{State: protocol.InventoryOK, Items: items}
}

// BenchmarkNodeRowWrites measures the writes a machine's hello makes and the
// read the machine list makes, for a machine that holds a 300-item inventory.
// The fold of the node side tables moves the hello writes onto the nodes row, so
// the numbers before and after the fold are the check of that move. The inventory
// write stays in its own table: folding that table onto the nodes row made each
// write 12 times slower, because the row holds the whole report.
func BenchmarkNodeRowWrites(b *testing.B) {
	s, _ := newTestStore(b)
	node, _, err := s.CreateNode("bench-host", "linux")
	if err != nil {
		b.Fatal(err)
	}
	if err := s.SetInventory(node, benchInventory(300)); err != nil {
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
	b.Run("inventory", func(b *testing.B) {
		report := benchInventory(300)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := s.SetInventory(node, report); err != nil {
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

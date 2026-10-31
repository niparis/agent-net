package graphview

import (
	"testing"
	"time"
)

func TestTrafficMovesAndFlashes(t *testing.T) {
	g := New(fixture())
	at := time.Unix(100, 0)
	g.Message("a", "b", "request", at)
	first, _ := g.Traffic(at)
	middle, _ := g.Traffic(at.Add(time.Second))
	end, flashes := g.Traffic(at.Add(3 * time.Second))
	if len(first) != 1 || len(middle) != 1 || first[0].Position == middle[0].Position {
		t.Fatal("comet did not move")
	}
	if len(end) != 0 || len(flashes) != 1 || flashes[0] != "b" {
		t.Fatalf("arrival: %v %v", end, flashes)
	}
}

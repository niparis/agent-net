package graphview

import (
	"github.com/niparis/agent-net/internal/tui/palette"
	"testing"
)

func TestDimEdgePalette(t *testing.T) {
	if edgeDimColor != palette.Color("#596171", "#454c58") {
		t.Fatal("wrong edge palette")
	}
}

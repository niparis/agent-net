package tui

import (
	"strings"
	"testing"
)

func TestGraphPixelModeUsesPlaceholdersAndCachesImage(t *testing.T) {
	m := graphModel(120, 34)
	m.graphPixel.graphics = true
	m.graphPixel.cellW, m.graphPixel.cellH = 8, 16

	first := strings.Join(m.graphView(120, 34), "\n")
	if !strings.Contains(first, "\x1b_Ga=T") || !strings.Contains(first, "U=1") {
		t.Fatalf("first frame does not transmit the image:\n%q", first)
	}
	if !strings.Contains(first, "\U0010EEEE") {
		t.Fatal("first frame has no Unicode placeholders")
	}
	second := strings.Join(m.graphView(120, 34), "\n")
	if strings.Contains(second, "\x1b_G") {
		t.Fatal("unchanged picture was transmitted again")
	}
	if !strings.Contains(second, "\U0010EEEE") {
		t.Fatal("second frame lost its placeholders")
	}
}

func TestGraphFallsBackToBrailleWithoutPixels(t *testing.T) {
	m := graphModel(80, 24)
	out := strings.Join(m.graphView(80, 24), "\n")
	if strings.Contains(out, "\x1b_G") || strings.Contains(out, "\U0010EEEE") {
		t.Fatal("braille fallback wrote kitty graphics")
	}
	if !strings.Contains(out, "\u2800") && !strings.ContainsAny(out, "⠁⠂⠄⡀⢀⠈⠐⠠") {
		t.Fatalf("braille fallback has no braille glyphs: %q", out)
	}
}

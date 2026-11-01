package tui

import (
	"fmt"
	"image"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/tui/graphview"
)

// renderGraphFrame runs the scheduled background frame and applies it, the way
// the Bubble Tea runtime would.
func renderGraphFrame(t *testing.T, m Model) Model {
	t.Helper()
	cmd := m.scheduleGraphRender()
	if cmd == nil {
		t.Fatal("no graph frame was scheduled")
	}
	msg := cmd()
	frame, ok := msg.(graphFrameMsg)
	if !ok {
		t.Fatalf("render command returned %T", msg)
	}
	return send(m, frame)
}

func TestGraphPixelModeUsesPlaceholdersAndCachesImage(t *testing.T) {
	m := graphModel(120, 34)
	m.graphPixel.graphics = true
	m.graphPixel.cellW, m.graphPixel.cellH = 8, 16
	m = renderGraphFrame(t, m)

	first := m.View()
	if !strings.Contains(first, "\x1b_Ga=T") || !strings.Contains(first, "U=1") {
		t.Fatalf("first frame does not transmit the image:\n%q", first)
	}
	if !strings.Contains(first, "\U0010EEEE") {
		t.Fatal("first frame has no Unicode placeholders")
	}
	second := m.View()
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

// A new frame goes under the other image id and the old image is deleted only
// after it, so the terminal never shows an empty graph between frames (v0.4.54
// deleted the image first and the animated graph flickered).
func TestGraphPixelFramesAlternateIDsWithoutAGap(t *testing.T) {
	old := rasterizeFrame
	t.Cleanup(func() { rasterizeFrame = old })
	shade := uint8(0)
	rasterizeFrame = func(graphview.Scene, graphview.Size, graphview.PixelMetrics) *image.RGBA {
		shade++
		img := image.NewRGBA(image.Rect(0, 0, 4, 4))
		img.Pix[0] = shade
		return img
	}
	m := New(nil)
	g := m.graphPixel
	g.graphics, g.cellW, g.cellH = true, 8, 16
	size := graphview.Size{Width: 10, Height: 4}
	frame := func() graphFrameMsg {
		msg := m.graphRenderCmd(graphview.Scene{}, size, [32]byte{shade})().(graphFrameMsg)
		g.shown, g.hash, g.have, g.transmit = msg.id, msg.hash, msg.have, msg.transmit
		return msg
	}
	first := frame()
	if strings.Contains(first.transmit, "a=d") {
		t.Fatalf("first frame deletes an image: %q", first.transmit)
	}
	second := frame()
	if second.id == first.id {
		t.Fatalf("second frame reused id %d", first.id)
	}
	send := strings.Index(second.transmit, fmt.Sprintf("i=%d,", second.id))
	del := strings.Index(second.transmit, fmt.Sprintf("a=d,d=I,i=%d,", first.id))
	if send < 0 || del < 0 || del < send {
		t.Fatalf("want the new image sent before the old one is deleted: %q", second.transmit)
	}
	if third := frame(); third.id != first.id {
		t.Fatalf("third frame id %d, want the first id %d again", third.id, first.id)
	}
}

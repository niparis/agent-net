package graphview

import (
	"fmt"
	"image"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func okProbe() Probe {
	return Probe{Reply: "\x1b_Gi=31;OK\x1b\\\x1b[?1;2c", CellWidth: 8, CellHeight: 16}
}

func TestDetectGraphics(t *testing.T) {
	env := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}
	tests := []struct {
		name  string
		env   map[string]string
		probe Probe
		calls int
		want  Graphics
	}{
		{"kitty answer", nil, okProbe(), 1, Graphics{Pixel: true, CellWidth: 8, CellHeight: 16}},
		{"no answer", nil, Probe{CellWidth: 8, CellHeight: 16}, 1, Graphics{}},
		{"error reply", nil, Probe{Reply: "\x1b_Gi=31;EINVAL:bad\x1b\\\x1b[?1;2c", CellWidth: 8, CellHeight: 16}, 1, Graphics{}},
		{"tmux", map[string]string{"TMUX": "/tmp/tmux,1,0"}, okProbe(), 0, Graphics{}},
		{"herdr", map[string]string{"HERDR_ENV": "1", "HERDR_SOCKET_PATH": "/h/s"}, okProbe(), 0, Graphics{}},
		{"braille switch", map[string]string{"AGENT_NET_GRAPH": "braille"}, okProbe(), 0, Graphics{}},
		{"pixel switch", map[string]string{"AGENT_NET_GRAPH": "pixel"}, Probe{CellWidth: 8, CellHeight: 16}, 1, Graphics{Pixel: true, CellWidth: 8, CellHeight: 16}},
		{"pixel switch beats tmux", map[string]string{"AGENT_NET_GRAPH": "pixel", "TMUX": "/tmp/tmux,1,0"}, okProbe(), 1, Graphics{Pixel: true, CellWidth: 8, CellHeight: 16}},
		{"unknown cell size", nil, Probe{Reply: "\x1b_Gi=31;OK\x1b\\\x1b[?1;2c"}, 1, Graphics{}},
		{"cell size from reply", nil, Probe{Reply: "\x1b_Gi=31;OK\x1b\\\x1b[6;18;9t\x1b[?1;2c"}, 1, Graphics{Pixel: true, CellWidth: 9, CellHeight: 18}},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			calls := 0
			query := func(string, time.Duration) Probe {
				calls++
				return c.probe
			}
			got := Detect(env(c.env), query)
			if got != c.want {
				t.Fatalf("Detect = %+v, want %+v", got, c.want)
			}
			if calls != c.calls {
				t.Fatalf("query called %d times, want %d", calls, c.calls)
			}
		})
	}
}

func TestPlaceholderLinesEncodeRowsAndColumns(t *testing.T) {
	lines := PlaceholderLines(nil, Size{Width: 3, Height: 2}, ImageID, func(role, text string) string { return text })
	if len(lines) != 2 {
		t.Fatalf("got %d lines", len(lines))
	}
	for y, line := range lines {
		if n := strings.Count(line, "\U0010EEEE"); n != 3 {
			t.Fatalf("line %d has %d placeholders, want 3", y, n)
		}
	}
	want := func(row, col int) string {
		return "\U0010EEEE" + string(diacritic(row)) + string(diacritic(col))
	}
	for _, c := range []struct{ row, col int }{{0, 0}, {0, 1}, {0, 2}, {1, 0}, {1, 1}, {1, 2}} {
		if !strings.Contains(lines[c.row], want(c.row, c.col)) {
			t.Errorf("row %d has no placeholder for column %d: %q", c.row, c.col, lines[c.row])
		}
	}
	// The first placeholder is row 0 column 0: overline, overline.
	if i := strings.Index(lines[0], string(placeholder)); i < 0 || !strings.HasPrefix(lines[0][i:], want(0, 0)) {
		t.Errorf("first placeholder is not row 0 column 0: %q", lines[0])
	}
	if !utf8.ValidString(lines[0]) {
		t.Error("placeholder line is not valid UTF-8")
	}
}

func TestPlaceholderLinesKeepLabelsAsText(t *testing.T) {
	labels := map[Cell]LabelCell{{X: 1, Y: 0}: {Rune: 'X', Role: "selectedLabel"}}
	lines := PlaceholderLines(labels, Size{Width: 3, Height: 1}, ImageID, func(role, text string) string {
		return "[" + role + ":" + text + "]"
	})
	if !strings.Contains(lines[0], "[selectedLabel:X]") {
		t.Fatalf("label missing from %q", lines[0])
	}
	if n := strings.Count(lines[0], "\U0010EEEE"); n != 2 {
		t.Fatalf("got %d placeholders with one label cell, want 2", n)
	}
}

func TestTransmitAndDeleteImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 16))
	img.SetRGBA(0, 0, rgba(1, 2, 3, 255))
	id := fmt.Sprint(ImageID)
	out := DeleteImage(ImageID) + TransmitImage(img, ImageID, 4, 3)
	if !strings.Contains(out, "\x1b_Ga=d,d=I,i="+id) {
		t.Errorf("no delete by id: %q", out)
	}
	for _, want := range []string{"a=T", "f=100", "U=1", "i=" + id, "q=2", "c=4", "r=3", "\x1b\\"} {
		if !strings.Contains(out, want) {
			t.Errorf("transmit lacks %q", want)
		}
	}
}

func TestDiacriticsAreUniqueAndCombining(t *testing.T) {
	seen := map[rune]bool{}
	for i := 0; i < 297; i++ {
		r := diacritic(i)
		if seen[r] {
			t.Fatalf("diacritic %d repeats %U", i, r)
		}
		seen[r] = true
	}
	if diacritic(0) != '\u0305' || diacritic(1) != '\u030D' {
		t.Fatalf("unexpected first diacritics: %U %U", diacritic(0), diacritic(1))
	}
}

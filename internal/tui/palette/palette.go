// Package palette holds the TUI's light and dark semantic colors. It does not
// query the terminal during package initialization.
package palette

import (
	"image/color"
	"sync/atomic"

	"charm.land/lipgloss/v2"
)

var lightBackground atomic.Bool

// SetDark selects the terminal background before the TUI starts rendering.
func SetDark(dark bool) { lightBackground.Store(!dark) }
func IsDark() bool      { return !lightBackground.Load() }

type Adaptive struct{ Light, Dark color.Color }

func (c Adaptive) RGBA() (uint32, uint32, uint32, uint32) {
	return displayColor(lipgloss.LightDark(IsDark())(c.Light, c.Dark)).RGBA()
}

func Color(light, dark string) Adaptive {
	return Adaptive{Light: lipgloss.Color(light), Dark: lipgloss.Color(dark)}
}

// The previous terminal renderer converted hex through floating-point sRGB
// and truncated the result. Keep those displayed RGB bytes unchanged.
func displayColor(c color.Color) color.Color {
	r, g, b, _ := c.RGBA()
	channel := func(v uint32) uint8 { return uint8(float64(v>>8) * (1.0 / 255.0) * 255.0) }
	return color.RGBA{R: channel(r), G: channel(g), B: channel(b), A: 255}
}

// Fixed returns a non-adaptive color with the same displayed RGB as before.
func Fixed(hex string) color.Color { return displayColor(lipgloss.Color(hex)) }

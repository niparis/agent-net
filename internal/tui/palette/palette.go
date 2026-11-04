// Package palette holds the TUI's light and dark semantic colors. It does not
// query the terminal during package initialization.
package palette

import (
	"fmt"
	"image/color"
	"sync"
	"sync/atomic"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/muesli/termenv"
)

var lightBackground atomic.Bool
var profile atomic.Uint32
var colors sync.Map

// SetDark selects the terminal background before the TUI starts rendering.
func SetDark(dark bool) { lightBackground.Store(!dark) }
func IsDark() bool      { return !lightBackground.Load() }

// SetProfile records the profile announced by Bubble Tea. Its renderer still
// down-samples the view; these colors choose the same entries as the old UI.
func SetProfile(p colorprofile.Profile) { profile.Store(uint32(p)) }
func Profile() colorprofile.Profile     { return colorprofile.Profile(profile.Load()) }

// Glamour uses termenv separately from Bubble Tea's renderer.
func GlamourProfile() termenv.Profile {
	switch Profile() {
	case colorprofile.ANSI:
		return termenv.ANSI
	case colorprofile.ANSI256:
		return termenv.ANSI256
	case colorprofile.ASCII, colorprofile.NoTTY:
		return termenv.Ascii
	default:
		return termenv.TrueColor
	}
}

type Adaptive struct{ Light, Dark color.Color }

func (c Adaptive) RGBA() (uint32, uint32, uint32, uint32) {
	return displayColor(lipgloss.LightDark(IsDark())(c.Light, c.Dark)).RGBA()
}

func Color(light, dark string) Adaptive {
	return Adaptive{Light: lipgloss.Color(light), Dark: lipgloss.Color(dark)}
}

// Fixed returns a non-adaptive color with the same displayed RGB as before.
func Fixed(hex string) color.Color { return Color(hex, hex) }

// Representatives that Bubble Tea's 16-color output maps to indices 0-15.
// The terminal still receives the index, so its own ANSI palette is honored.
var ansiRepresentatives = [16]color.RGBA{
	{0, 0, 0, 255}, {95, 0, 0, 255}, {0, 95, 0, 255}, {95, 95, 0, 255},
	{0, 0, 95, 255}, {95, 0, 95, 255}, {0, 95, 95, 255}, {175, 175, 175, 255},
	{95, 95, 95, 255}, {215, 0, 0, 255}, {0, 215, 0, 255}, {215, 215, 0, 255},
	{0, 0, 215, 255}, {215, 0, 215, 255}, {0, 215, 215, 255}, {255, 255, 255, 255},
}

type colorKey struct {
	r, g, b uint8
	profile colorprofile.Profile
}

func displayColor(c color.Color) color.Color {
	r, g, b, _ := c.RGBA()
	key := colorKey{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), Profile()}
	if key.profile != colorprofile.ANSI && key.profile != colorprofile.ANSI256 {
		// The previous renderer converted hex through floating-point sRGB and
		// truncated the result. Keep those displayed true-color bytes.
		channel := func(v uint8) uint8 { return uint8(float64(v) * (1.0 / 255.0) * 255.0) }
		return color.RGBA{R: channel(key.r), G: channel(key.g), B: channel(key.b), A: 255}
	}
	if value, ok := colors.Load(key); ok {
		return value.(color.RGBA)
	}
	hex := fmt.Sprintf("#%02x%02x%02x", key.r, key.g, key.b)
	var result color.RGBA
	if key.profile == colorprofile.ANSI {
		index := termenv.ANSI.Color(hex).(termenv.ANSIColor)
		result = ansiRepresentatives[index]
	} else {
		c := termenv.ConvertToRGB(termenv.ANSI256.Color(hex))
		result = color.RGBA{R: uint8(c.R*255 + .5), G: uint8(c.G*255 + .5), B: uint8(c.B*255 + .5), A: 255}
	}
	colors.Store(key, result)
	return result
}

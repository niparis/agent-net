package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/niparis/agent-net/internal/tui/graphview"
	"github.com/niparis/agent-net/internal/tui/palette"
)

func TestGraphPixelFallsBackForNonTrueColorProfiles(t *testing.T) {
	defer palette.SetProfile(colorprofile.TrueColor)
	noColor := colorprofile.Env([]string{"TERM=xterm-256color", "COLORTERM=truecolor", "NO_COLOR=1"})
	if noColor != colorprofile.ASCII {
		t.Fatalf("NO_COLOR profile: %v", noColor)
	}
	for _, tc := range []struct {
		name    string
		profile colorprofile.Profile
	}{
		{"unknown", colorprofile.Unknown}, {"no tty", colorprofile.NoTTY},
		{"NO_COLOR", noColor}, {"ANSI", colorprofile.ANSI}, {"ANSI256", colorprofile.ANSI256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := idleGraphModel(80, 24)
			next, _ := m.update(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
			m = renderGraphFrame(t, next.(Model))
			next, _ = m.update(tea.ColorProfileMsg{Profile: tc.profile})
			m = next.(Model)
			if cmd := m.scheduleGraphRender(); cmd != nil {
				t.Error("scheduled a pixel frame without true color")
			}
			out := m.View().Content
			if strings.Contains(out, "\x1b_G") || strings.Contains(out, "\U0010EEEE") {
				t.Error("displayed a cached kitty image without true color")
			}
			if !strings.Contains(out, "\u2800") && !strings.ContainsAny(out, "⠁⠂⠄⡀⢀⠈⠐⠠") {
				t.Error("missing braille graph")
			}
		})
	}
}

func TestGraphPixelWaitsForTrueColorAndResumes(t *testing.T) {
	defer palette.SetProfile(colorprofile.TrueColor)
	m := idleGraphModel(80, 24)
	next, _ := m.update(tea.ColorProfileMsg{Profile: colorprofile.Unknown})
	m = next.(Model)
	if cmd := m.scheduleGraphRender(); cmd != nil {
		t.Fatal("scheduled pixels before profile was known")
	}
	next, _ = m.update(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
	m = renderGraphFrame(t, next.(Model))
	if out := m.View().Content; !strings.Contains(out, "\x1b_Ga=T") || !strings.Contains(out, "\U0010EEEE") || !strings.Contains(out, "38;2;78;69;84") {
		t.Fatal("true-color graph did not transmit an image with its placeholder ID")
	}
	next, _ = m.Update(tea.ColorProfileMsg{Profile: colorprofile.ANSI256})
	m = next.(Model)
	if out := m.View().Content; strings.Contains(out, "\U0010EEEE") {
		t.Fatal("did not switch to braille after a downgrade")
	}
	next, _ = m.Update(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
	m = next.(Model)
	if !m.graphPixel.rendering {
		t.Fatal("did not schedule a pixel frame after the upgrade")
	}
}

func TestGraphProfileChangesDiscardInFlightPixelFrame(t *testing.T) {
	defer palette.SetProfile(colorprofile.TrueColor)
	m := idleGraphModel(80, 24)
	next, _ := m.update(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
	m = next.(Model)
	cmd := m.scheduleGraphRender()
	if cmd == nil {
		t.Fatal("no initial frame")
	}
	oldKey := m.graphPixel.key
	next, _ = m.Update(tea.ColorProfileMsg{Profile: colorprofile.ANSI256})
	m = next.(Model)
	next, _ = m.Update(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
	m = next.(Model)
	frame := cmd().(graphFrameMsg)
	next, _ = m.Update(frame)
	m = next.(Model)
	if !m.graphPixel.rendering || m.graphPixel.key != oldKey {
		t.Fatal("profile changes did not discard and replace the in-flight frame")
	}
}

func TestGraphProfileFallbackUsesBrailleHitMap(t *testing.T) {
	defer palette.SetProfile(colorprofile.TrueColor)
	m := graphModel(120, 34)
	m.graphPixel.graphics, m.graphPixel.cellW, m.graphPixel.cellH = true, 8, 16
	next, _ := m.update(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
	m = renderGraphFrame(t, next.(Model))
	next, _ = m.update(tea.ColorProfileMsg{Profile: colorprofile.ANSI256})
	m = next.(Model)
	w, h := m.graphSize()
	size := graphview.Size{Width: w, Height: h}
	scene := m.graphScene(m.graphState, size)
	for cell, name := range (graphview.Braille{}).Render(scene, size).Hits {
		if name == m.graphState.Graph.Selected() || m.graphPixel.hits[cell] != "" || graphview.PixelHit(scene, size, graphview.PixelMetrics{CellWidth: 8, CellHeight: 16}, cell) != "" {
			continue
		}
		next, _ := m.Update(tea.MouseClickMsg{X: cell.X + 2, Y: cell.Y + 1, Button: tea.MouseLeft})
		if got := next.(Model).graphState.Graph.Selected(); got != name {
			t.Fatalf("braille click selected %q, want %q", got, name)
		}
		return
	}
	t.Fatal("fixture has no braille label outside the pixel hit map")
}

func TestCTOHarnessSwitchIgnoresPaste(t *testing.T) {
	m := newModel(80, 24)
	m.focus = focusCTO
	title, model := newTextInput(), newTextInput()
	title.SetValue("title")
	model.SetValue("model")
	m.cto.settingsForm = &ctoSettingsForm{harness: "claude", titleInput: title, input: model, field: 0}
	next, _ := m.Update(tea.PasteMsg{Content: "q x"})
	m = next.(Model)
	f := m.cto.settingsForm
	if f == nil || f.field != 0 || f.harness != "claude" || f.titleInput.Value() != "title" || f.input.Value() != "model" || m.quitting {
		t.Fatal("paste changed the harness switch or another field")
	}
}

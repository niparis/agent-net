package tui

import (
	"image/color"
	"reflect"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/tui/palette"
)

func TestInitRequestsTheTerminalBackground(t *testing.T) {
	m := newModel(80, 24)
	msg := m.Init()()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init returned %T, want a batch", msg)
	}
	for _, cmd := range batch {
		if cmd == nil {
			continue
		}
		name := runtime.FuncForPC(reflect.ValueOf(cmd).Pointer()).Name()
		if strings.HasSuffix(name, "RequestBackgroundColor") {
			return
		}
	}
	t.Fatal("Init did not request the terminal background color")
}

func TestBackgroundColorMsgSelectsThePalette(t *testing.T) {
	old := palette.IsDark()
	defer palette.SetDark(old)
	m := newModel(80, 24)

	if next, _ := m.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 255, G: 255, B: 255, A: 255}}); next == nil {
		t.Fatal("light background message was ignored")
	}
	if palette.IsDark() {
		t.Error("a white terminal did not select the light palette")
	}
	m.Update(tea.BackgroundColorMsg{Color: color.RGBA{A: 255}})
	if !palette.IsDark() {
		t.Error("a black terminal did not select the dark palette")
	}
}

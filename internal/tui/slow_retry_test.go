package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// A load that ends by its deadline keeps the last changes and asks once more in
// the background. A second timeout does not loop: the regular refresh goes on.
func TestSlowChangesLoadKeepsTheChangesAndRetries(t *testing.T) {
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, 120, 40))
	changes := len(m.delivery.changes)
	if changes == 0 {
		t.Fatal("fixture has no changes")
	}

	next, cmd := m.Update(deliveryLoadedMsg{err: context.DeadlineExceeded})
	m = next.(Model)
	if !m.delivery.slow || m.delivery.err != "" {
		t.Fatalf("slow load: slow %v err %q", m.delivery.slow, m.delivery.err)
	}
	if len(m.delivery.changes) != changes {
		t.Fatalf("the last changes were dropped: %d, want %d", len(m.delivery.changes), changes)
	}
	if cmd == nil || !m.delivery.loading {
		t.Fatal("no background retry after the first timeout")
	}

	// The retry times out too: no third request now, still no raw error.
	next, cmd = m.Update(deliveryLoadedMsg{err: errors.New("rpc: context deadline exceeded")})
	m = next.(Model)
	if cmd != nil && m.delivery.loading {
		t.Fatal("a second timeout started another retry at once")
	}
	if !m.delivery.slow || m.delivery.err != "" {
		t.Fatalf("second timeout: slow %v err %q", m.delivery.slow, m.delivery.err)
	}

	// An answer clears it, and a real failure still says what it is.
	next, _ = m.Update(deliveryLoadedMsg{changes: deliveryScenes()})
	m = next.(Model)
	if m.delivery.slow || m.delivery.err != "" {
		t.Fatalf("slow state stayed after an answer: %v %q", m.delivery.slow, m.delivery.err)
	}
	next, _ = m.Update(deliveryLoadedMsg{err: errors.New("gateway said no")})
	m = next.(Model)
	if m.delivery.err != "gateway said no" || m.delivery.slow {
		t.Fatalf("a real error must be kept as it is: %q slow %v", m.delivery.err, m.delivery.slow)
	}
}

// The CTO screen shows the same text, keeps what it has, and retries once.
func TestCTOScreenSlowPollShowsRetrying(t *testing.T) {
	m := New(&fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot()}})
	m.width, m.height = 100, 30
	m.cto = &ctoState{generation: 1, polling: true, messages: []string{"cto: still here"}}

	next, cmd := m.Update(ctoPollMsg{generation: 1, err: context.DeadlineExceeded})
	m = next.(Model)
	view := ansi.Strip(strings.Join(m.ctoView(80, 12), "\n"))
	if !strings.Contains(view, slowText) || strings.Contains(view, "deadline exceeded") || !strings.Contains(view, "still here") {
		t.Fatalf("slow CTO text:\n%s", view)
	}
	if cmd == nil || !m.cto.polling {
		t.Fatal("no background retry after the first timeout")
	}
	next, cmd = m.Update(ctoPollMsg{generation: 1, err: context.DeadlineExceeded})
	m = next.(Model)
	if cmd != nil || m.cto.polling {
		t.Fatal("a second timeout started another retry at once")
	}
	if view = ansi.Strip(strings.Join(m.ctoView(80, 12), "\n")); !strings.Contains(view, slowText) {
		t.Fatalf("second timeout text:\n%s", view)
	}
	next, _ = m.Update(ctoPollMsg{generation: 1})
	m = next.(Model)
	if view = ansi.Strip(strings.Join(m.ctoView(80, 12), "\n")); strings.Contains(view, slowText) {
		t.Fatalf("slow text stayed after an answer:\n%s", view)
	}
	next, _ = m.Update(ctoPollMsg{generation: 1, err: errors.New("harness exploded")})
	if view = ansi.Strip(strings.Join(next.(Model).ctoView(80, 12), "\n")); !strings.Contains(view, "error: harness exploded") {
		t.Fatalf("a real error must show as it is:\n%s", view)
	}
}

// The top banner of a refresh that timed out says it is slow, not the raw
// error, and keeps the last data.
func TestBannerSlowRefresh(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.applyFetch(fetchedMsg{err: context.DeadlineExceeded})
	banner := ansi.Strip(m.bannerLine(100))
	if !strings.Contains(banner, slowText) || strings.Contains(banner, "deadline exceeded") || !strings.Contains(banner, "Showing last data") {
		t.Fatalf("banner = %q", banner)
	}
	m.applyFetch(fetchedMsg{err: errors.New("daemon said no")})
	if banner = ansi.Strip(m.bannerLine(100)); !strings.Contains(banner, "daemon said no") {
		t.Fatalf("a real error must show as it is: %q", banner)
	}
}

// screenSource records the screen name the model gives its source.
type screenSource struct {
	*fakeSource
	screens []string
}

func (s *screenSource) SetScreen(name string) { s.screens = append(s.screens, name) }

// The model tells its source which tab or view is on screen, so a failed call
// is logged with it.
func TestModelTellsTheSourceWhichScreenIsOn(t *testing.T) {
	src := &screenSource{fakeSource: &fakeSource{snap: fakeSnapshot()}}
	m := New(src)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if got := src.screens[len(src.screens)-1]; got != "sessions" {
		t.Fatalf("first screen = %q", got)
	}
	m.tab = messagesTab
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if got := src.screens[len(src.screens)-1]; got != "messages" {
		t.Fatalf("messages screen = %q", got)
	}
	m.tab = sessionsTab
	m.cto, m.focus = &ctoState{generation: 1}, focusCTO
	_ = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if got := src.screens[len(src.screens)-1]; got != "cto" {
		t.Fatalf("cto screen = %q", got)
	}
}

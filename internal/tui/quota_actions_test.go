package tui

import (
	"context"
	"encoding/json"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
)

type quotaActionSource struct {
	*fakeSource
	method string
	value  protocol.ProtectedProviders
}

func (s *quotaActionSource) Call(_ context.Context, method string, value any) (json.RawMessage, error) {
	s.method = method
	s.value = value.(protocol.ProtectedProviders)
	return json.RawMessage(`{}`), nil
}
func TestQuotaProtectionSavesAndRefreshes(t *testing.T) {
	m := newModel(80, 24)
	source := &quotaActionSource{fakeSource: &fakeSource{}}
	m.src = source
	m.tab = 2
	m.quotaShowUnset = true
	m.usage.raw.Local.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "claude", Windows: []protocol.QuotaWindow{{ID: "week"}}}}}
	m = keys(m, "p")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("missing save command")
	}
	result := cmd()
	if source.method != protocol.OpProtectedProvidersSet || len(source.value.Providers) != 1 || source.value.Providers[0] != "anthropic" || source.value.Reason == "" {
		t.Fatalf("save: %+v", source)
	}
	next, cmd = m.Update(result)
	m = next.(Model)
	if cmd == nil || m.activeNotice() == nil {
		t.Fatal("save must refresh and show a notice")
	}
}

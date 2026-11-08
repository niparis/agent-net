package tui

import (
	"fmt"
	"testing"
	"time"
)

// ownerAppsModel is the owner's screen from 2026-11-08: a bot whose name is
// its OAuth client name, with two shared MCP servers granted.
func ownerAppsModel(w, h int) Model {
	m := newModel(w, h)
	m.tab = settingsTab
	stamp := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	used := m.now().Add(8*time.Hour + 9*time.Minute)
	m.apps.apps = []connectedApp{
		{ID: "trade", Name: "cursor", Kind: "bot", ClientName: "Cursor", RedirectURIs: []string{"https://www.cursor.com/cb"}, CreatedAt: stamp, LastUsedAt: &used, Grants: []string{"mcp server tenebrae-library", "mcp server ingest-forge"}},
		{ID: "grok", Name: "grok-bot", Kind: "bot", ClientName: "Grok Bot", RedirectURIs: []string{"https://grok.example/cb"}, CreatedAt: stamp},
		{ID: "phone", Name: "phone", Kind: "owner_app", ClientName: "iPhone Safari", RedirectURIs: []string{"https://phone.example/cb"}, CreatedAt: stamp},
	}
	return m
}

func TestConnectedAppOwnerScenes(t *testing.T) {
	for _, w := range []int{120, 80} {
		h := 34
		if w == 80 {
			h = 24
		}
		m := ownerAppsModel(w, h)
		golden(t, fmt.Sprintf("connected_apps_owner_%d", w), m.testView())
		m.apps.stage = "grant"
		m.apps.index = 0
		m.apps.servers = []grantServer{{name: "ingest-forge", host: "research", tools: 1, reported: true}, {name: "tenebrae-library", host: "research", tools: 2, reported: true}, {name: "notes", host: "nas"}}
		golden(t, fmt.Sprintf("connected_apps_owner_picker_%d", w), m.testView())
	}
}

// The Tools tab shows the bots granted the selected MCP server.
func TestConnectedAppOwnerToolsScene(t *testing.T) {
	for _, w := range []int{120, 80} {
		h := 34
		if w == 80 {
			h = 24
		}
		m, _ := toolsAtScreen(w, h)
		m = rowByName(m, "tenebrae-library")
		m.tl.bots = botsByServer(ownerAppsModel(w, h).apps.apps)
		golden(t, fmt.Sprintf("tools_mcp_bots_%d", w), m.testView())
	}
}

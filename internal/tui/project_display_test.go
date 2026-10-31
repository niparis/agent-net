package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestMapProjectDisplayForm(t *testing.T) {
	m := openMap(t, mapModel(120, 34, fakeGraph()))
	src := m.src.(*fakeSource)
	m.mp.selected = "cogforge"
	if !strings.Contains(m.helpLine(120), "D name") {
		t.Fatalf("hint: %s", m.helpLine(120))
	}
	m, _ = press(m, keyRunes("D"))
	if !strings.Contains(m.helpLine(120), "enter saves") {
		t.Fatalf("prompt: %s", m.helpLine(120))
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(src.projectNames) != 0 {
		t.Fatal("cancel saved")
	}
	m, _ = press(m, keyRunes("D"))
	m.rename.SetValue("Evidence API")
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = finish(t, m, cmd)
	if len(src.projectNames) != 1 || src.projectNames[0] != (renameCall{"cogforge", "Evidence API"}) {
		t.Fatalf("saved: %v", src.projectNames)
	}
	m, _ = press(m, keyRunes("D"))
	m.rename.SetValue("")
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = finish(t, m, cmd)
	if len(src.projectNames) != 2 || src.projectNames[1] != (renameCall{"cogforge", ""}) {
		t.Fatalf("cleared: %v", src.projectNames)
	}
	m, _ = press(m, keyRunes("?"))
	if !strings.Contains(m.View(), "name project") {
		t.Fatal("help missing display name key")
	}
}

func TestProjectDisplayInMapAndSessions(t *testing.T) {
	m := openMap(t, mapModel(120, 34, fakeGraph()))
	m.snap.Projects = append(m.snap.Projects, protocol.ProjectView{Name: "cogforge", DisplayName: "Evidence API"})
	m.mp.selected = "cogforge"
	if !strings.Contains(m.View(), "Evidence API") {
		t.Fatalf("map: %s", m.View())
	}
	m.snap.Agents[0].Repo.Project = "cogforge"
	m.snap.Agents[0].ProjectDisplayName = "Evidence API"
	m.mapOn = false
	m.tab = 0
	if !strings.Contains(m.View(), "Evidence API") {
		t.Fatalf("sessions: %s", m.View())
	}
}

func TestProjectDisplayMapGraphMessagesAndFilter(t *testing.T) {
	m := openMap(t, mapModel(120, 34, fakeGraph()))
	m.snap.Projects = append(m.snap.Projects, protocol.ProjectView{Name: "cogforge", DisplayName: "Evidence API"})
	m.mp.selected = "cogforge"
	m.mp.overview = true
	if !strings.Contains(m.View(), "Evidence API") {
		t.Fatal("overview missing display name")
	}
	m.mp.overview = false
	if !strings.Contains(strings.Join(m.mapEdgeLines("tenebrae", 120), " "), "Evidence API") {
		t.Fatal("edge missing display name")
	}
	m.mapOn = false
	m.tab = 5
	if !strings.Contains(strings.Join(m.graphView(120, 24), "\n"), "Evidence API") {
		t.Fatal("graph missing display name")
	}
	a := &m.snap.Agents[0]
	a.Repo.Project = "cogforge"
	a.Profile.Name = "cogforge"
	a.ProjectDisplayName = "Evidence API"
	if got := m.conversationName(a.SessionID); got != "Evidence API" {
		t.Fatalf("message name %q", got)
	}
	m.filter = "Evidence API"
	if !matches(*a, m.filter) {
		t.Fatal("display name did not match session filter")
	}
}

func TestGoldenProjectDisplay(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := openMap(t, mapModel(size[0], size[1], fakeGraph()))
		m.snap.Projects = append(m.snap.Projects, protocol.ProjectView{Name: "cogforge", DisplayName: "Evidence API"})
		m.mp.selected = "cogforge"
		golden(t, fmt.Sprintf("project_display_%dx%d", size[0], size[1]), m.View())
		m, _ = press(m, keyRunes("D"))
		golden(t, fmt.Sprintf("project_display_prompt_%dx%d", size[0], size[1]), m.View())
	}
}

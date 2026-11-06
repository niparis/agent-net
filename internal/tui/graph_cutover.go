package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type graphCutoverStatusMsg struct {
	epoch    int
	on       bool
	blockers []string
	backup   string
	err      error
}
type graphCutoverResultMsg struct {
	err       error
	on        bool
	backup    string
	uncertain bool
}
type graphCutoverCaller interface {
	Call(context.Context, string, any) (json.RawMessage, error)
}

func (m Model) graphCutoverStatusCmd() tea.Cmd {
	epoch := m.cutoverEpoch
	s, ok := m.src.(graphCutoverCaller)
	if !ok {
		return func() tea.Msg {
			return graphCutoverStatusMsg{epoch: epoch, err: errors.New("Graph cutover status is unavailable")}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		raw, err := s.Call(ctx, "graph_refs_status", nil)
		var status struct {
			On       bool     `json:"on"`
			Blockers []string `json:"blockers"`
			Backup   string   `json:"backup"`
		}
		if err == nil {
			err = json.Unmarshal(raw, &status)
		}
		return graphCutoverStatusMsg{epoch: epoch, on: status.On, blockers: status.Blockers, backup: status.Backup, err: err}
	}
}
func (m Model) graphCutoverKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if (m.mode == modeGraphCutoverPath || m.mode == modeGraphCutoverConfirm) && len(m.cutoverBlockers) > 0 {
		if k.Code == tea.KeyPgDown {
			m.cutoverScroll++
			return m, nil
		}
		if k.Code == tea.KeyPgUp {
			m.cutoverScroll = max(0, m.cutoverScroll-1)
			return m, nil
		}
	}
	switch m.mode {
	case modeGraphCutoverPath:
		switch keyAction(contextForm, k.String()) {
		case "cancel":
			m.mode = modeNormal
			return m, nil
		case "submit":
			p := strings.TrimSpace(m.rename.Value())
			if !filepath.IsAbs(p) {
				m.cutoverError = "Enter an absolute backup path on the gateway machine."
				return m, nil
			}
			m.cutoverPath = p
			m.cutoverError = ""
			m.mode = modeGraphCutoverConfirm
			return m, nil
		}
		var cmd tea.Cmd
		m.rename, cmd = m.rename.Update(k)
		return m, cmd
	case modeGraphCutoverConfirm:
		yes := k.String() == "y" || k.String() == "Y"
		no := k.String() == "n" || k.String() == "N" || k.Code == tea.KeyEsc
		if m.cutoverBusy {
			return m, nil
		}
		if no {
			m.mode = modeNormal
			return m, nil
		}
		if yes && !m.cutoverBusy && !m.cutoverOn {
			s, ok := m.src.(graphCutoverCaller)
			if !ok {
				m.cutoverError = "Graph cutover is unavailable"
				m.mode = modeGraphCutoverResult
				return m, nil
			}
			p := m.cutoverPath
			m.cutoverBusy = true
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 11*time.Minute)
				defer cancel()
				_, err := s.Call(ctx, "graph_refs_enable", map[string]string{"backup": p})
				if err == nil {
					return graphCutoverResultMsg{}
				}
				statusCtx, statusCancel := context.WithTimeout(context.Background(), Timeout)
				defer statusCancel()
				raw, statusErr := s.Call(statusCtx, "graph_refs_status", nil)
				var status struct {
					On     bool   `json:"on"`
					Backup string `json:"backup"`
				}
				if statusErr == nil {
					statusErr = json.Unmarshal(raw, &status)
				}
				if statusErr == nil && status.On {
					return graphCutoverResultMsg{on: true, backup: status.Backup}
				}
				lower := strings.ToLower(err.Error())
				timedOut := errors.Is(err, context.DeadlineExceeded) || strings.Contains(lower, "did not answer in time") || strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "timed out") || strings.Contains(lower, "timeout")
				if statusErr != nil {
					return graphCutoverResultMsg{err: fmt.Errorf("enable returned: %v; could not verify status: %v; it may still complete: check this screen again", err, statusErr), uncertain: true}
				}
				if timedOut {
					return graphCutoverResultMsg{err: errors.New("no answer in time; it may still complete: check this screen again"), uncertain: true}
				}
				return graphCutoverResultMsg{err: err}
			}
		}
	case modeGraphCutoverResult:
		if k.Code == tea.KeyEsc || k.Code == tea.KeyEnter {
			m.mode = modeNormal
			return m, m.graphCutoverStatusCmd()
		}
	}
	return m, nil
}
func (m Model) graphCutoverView(w, h int) []string {
	out := []string{m.titleLine(w), "", sBold.Render("Typed Repo/Agent Graph cutover"), ""}
	add := func(s string) {
		for _, line := range wrap(s, max(w-2, 1)) {
			out = append(out, "  "+line)
		}
	}
	blockers := func(room int) {
		var lines []string
		for _, b := range m.cutoverBlockers {
			lines = append(lines, wrap(sanitize(b), max(w-2, 1))...)
		}
		room = max(room, 1)
		start := min(m.cutoverScroll, max(len(lines)-room, 0))
		for _, line := range lines[start:min(start+room, len(lines))] {
			out = append(out, "  "+line)
		}
		if len(lines) > room {
			out = append(out, "  Page up/down: more blockers")
		}
	}
	switch m.mode {
	case modeGraphCutoverPath:
		add("Enter a new backup destination on the gateway machine.")
		blockers(h - 9)
		out = append(out, "", "  "+m.rename.View())
		if m.cutoverError != "" {
			add(sanitize(m.cutoverError))
		}
	case modeGraphCutoverConfirm:
		add("Enable the typed Repo/Agent Graph?")
		add("Backup folder: " + sanitize(m.cutoverPath))
		out = append(out, sDim.Render("  Holds the gateway database, skills and keys."))
		add("Turning it off later means restoring the backup.")
		blockers(h - 11)
		if m.cutoverBusy {
			add("Enabling cutover...")
		}
	case modeGraphCutoverResult:
		if m.cutoverError != "" {
			if m.cutoverUncertain {
				add(sanitize(m.cutoverError))
			} else {
				add("Cutover failed: " + sanitize(m.cutoverError))
			}
		} else {
			add("Typed Repo/Agent Graph is on.")
			add("Backup: " + sanitize(m.cutoverPath))
		}
	}
	return out
}

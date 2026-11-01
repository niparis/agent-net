package tui

import (
	"fmt"
	"github.com/charmbracelet/glamour"
	ga "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoTitle is the name on the owner's screens. Agents never see it.
func (m Model) ctoTitle() string {
	if m.cto != nil {
		return sanitize(protocol.CTOTitleOrDefault(m.cto.title))
	}
	return protocol.DefaultCTOTitle
}

// openProposals is how many CTO proposals wait in Needs you.
func (m Model) openProposals() int {
	n := 0
	for _, p := range m.proposals {
		if p.Status == "open" {
			n++
		}
	}
	return n
}

func (m Model) ctoView(w, h int) []string {
	c := m.cto
	title := m.ctoTitle()
	if !c.windowUntil.IsZero() {
		now := time.Now()
		if m.now != nil {
			now = m.now()
		}
		if left := c.windowUntil.Sub(now); left > 0 {
			seconds := int((left + time.Second - 1) / time.Second)
			title += fmt.Sprintf(" · I'm driving %02d:%02d · w close", seconds/60, seconds%60)
		}
	}

	proposals := ""
	if n := m.openProposals(); n > 0 {
		proposals = " · " + sDim.Render(plural(n, "open proposal"))
	}
	// Reserve room for the window and proposal count. Optional harness/model
	// details appear only when they fit in full, so neither status is clipped.
	for i, detail := range []string{c.harness, c.model} {
		if detail == "" {
			continue
		}
		detail = sanitize(detail)
		if i == 1 {
			detail = sDim.Render(detail)
		}
		if lipgloss.Width(title+" · "+detail+proposals) <= max(w-4, 1) {
			title += " · " + detail
		}
	}
	title += proposals

	borderColor := roleFaint
	if m.focus == focusCTO {
		borderColor = roleCTO
	}

	w = max(w-4, 1)
	h = max(h-2, 1)
	out := []string{}
	if f := c.settingsForm; f != nil {
		mark := func(n int) string {
			if f.field == n {
				return ">"
			}
			return " "
		}
		f.input.Width = max(w-12, 1)
		f.titleInput.Width = max(w-12, 1)
		model := f.input.View()
		if f.input.Value() == "" {
			model = sDim.Render("default")
		}
		formTitle := f.titleInput.View()
		if f.titleInput.Value() == "" {
			formTitle = sDim.Render(protocol.DefaultCTOTitle)
		}
		out = append(out,
			clip(mark(0)+" Harness: < "+f.harness+" >", w),
			clip(mark(1)+" Title:   "+formTitle, w),
			clip(mark(2)+" Model:   "+model, w))
		candidates := m.ctoModelCandidates(f.harness, f.input.Value())
		if len(candidates) > 0 {
			out = append(out, clip("  Suggestions: "+strings.Join(candidates, ", "), w))
		}
		if f.err != "" {
			out = append(out, clip(sBad.Render("  "+f.err), w))
		}
		out = append(out, clip("  up/down field  left/right harness  tab field  enter save  esc cancel", w))
		for len(out) < h {
			out = append(out, "")
		}
		return panelWithBorder(title, out[:min(len(out), h)], w+4, roleCTO, borderColor)
	}
	var lines []string
	for _, v := range c.messages {
		lines = append(lines, c.renderMessage(v, w)...)
	}
	if c.err != "" {
		lines = append(lines, wrapCTOLine("error: "+c.err, w)...)
	}
	if c.confirming {
		lines = append(lines, "Start a new conversation?"+confirmSuffix)
	}
	rows := max(h-1, 0)
	c.pageRows = max(rows-1, 1)
	c.scrollMax = max(len(lines)-rows, 0)
	c.scroll = min(c.scroll, c.scrollMax)
	end := len(lines) - c.scroll
	start := max(end-rows, 0)
	if start > 0 && rows > 1 {
		start++
		lines = append([]string{sDim.Render("↑ more")}, lines[start:end]...)
	} else {
		lines = lines[start:end]
	}
	for len(out) < h-1-len(lines) {
		out = append(out, "")
	}
	for _, v := range lines {
		out = append(out, clip(v, w))
	}
	out = append(out, m.onboardingLine(w))
	return panelWithBorder(title, out, w+4, roleCTO, borderColor)
}

// wrapCTOLine indents continuation rows while keeping their text in the box.
func wrapCTOLine(text string, width int) []string {
	var parts []string
	for _, line := range strings.Split(sanitizeBody(text), "\n") {
		parts = append(parts, wrap(line, max(width-2, 8))...)
	}
	for i := 1; i < len(parts); i++ {
		parts[i] = "  " + parts[i]
	}
	return parts
}

type ctoRenderKey struct {
	text    string
	width   int
	dark    bool
	profile termenv.Profile
}

func (c *ctoState) renderMessage(text string, width int) []string {
	if strings.HasPrefix(text, "switched to ") {
		lines := wrapCTOLine(text, width)
		for i := range lines {
			lines[i] = sDim.Render(lines[i])
		}
		return lines
	}
	if !strings.HasPrefix(text, "cto: ") {
		return wrapCTOLine(text, width)
	}
	key := ctoRenderKey{text, width, lipgloss.HasDarkBackground(), lipgloss.ColorProfile()}
	if lines, ok := c.renderCache[key]; ok {
		return lines
	}
	style := styles.DarkStyleConfig
	if !key.dark {
		style = styles.LightStyleConfig
	}
	color := func(role lipgloss.TerminalColor) *string {
		adaptive := role.(lipgloss.AdaptiveColor)
		value := adaptive.Light
		if key.dark {
			value = adaptive.Dark
		}
		return &value
	}
	zero := uint(0)
	yes := true
	style.Document = ga.StyleBlock{StylePrimitive: ga.StylePrimitive{Color: color(roleText)}, Margin: &zero}
	style.Heading.Color, style.Heading.Bold = color(roleText), &yes
	style.Code.Color, style.CodeBlock.Color = color(roleAccent), color(roleAccent)
	style.Link.Color, style.LinkText.Color = color(roleAccent), color(roleAccent)
	style.BlockQuote.Color = color(roleDim)
	renderer, err := glamour.NewTermRenderer(glamour.WithStyles(style), glamour.WithWordWrap(max(width, 1)), glamour.WithColorProfile(key.profile), glamour.WithPreservedNewLines())
	if err != nil {
		return wrapCTOLine(text, width)
	}
	rendered, err := renderer.Render(sanitizeBody(strings.TrimPrefix(text, "cto: ")))
	if err != nil {
		return wrapCTOLine(text, width)
	}
	lines := strings.Split(strings.Trim(rendered, "\n"), "\n")
	label := lipgloss.NewStyle().Foreground(roleCTO).Render("cto:")
	lines = append([]string{label}, lines...)
	if c.renderCache == nil {
		c.renderCache = make(map[ctoRenderKey][]string)
	}
	if len(c.renderCache) > 512 {
		clear(c.renderCache)
	}
	c.renderCache[key] = lines
	return lines
}

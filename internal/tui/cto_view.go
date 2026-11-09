package tui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"
	ga "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/palette"
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
	// The Snapshot carries the count even when the full list is not fetched
	// on this tab, so the box shows a new proposal without the gateway call.
	return max(n, m.snap.ProposalCount)
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
		f.input.SetWidth(max(w-12, 1))
		f.titleInput.SetWidth(max(w-12, 1))
		model := f.input.View()
		if f.input.Value() == "" {
			model = sDim.Render("default")
		}
		formTitle := f.titleInput.View()
		if f.titleInput.Value() == "" {
			formTitle = sDim.Render(protocol.DefaultCTOTitle)
		}
		fields := []string{
			clip(mark(0)+" Harness: < "+f.harness+" >", w),
			clip(mark(1)+" Title:   "+formTitle, w),
			clip(mark(2)+" Model:   "+model, w),
		}
		candidates := m.ctoModelCandidates(f.harness, f.input.Value())
		showSuggestions := len(candidates) > 0 && (h >= 3 || f.field == 2)
		fieldRows := h
		if h > 1 && (f.err != "" || showSuggestions) {
			fieldRows--
		}
		fieldRows = min(fieldRows, len(fields))
		if fieldRows == 2 && f.field == 0 && showSuggestions {
			out = append(out, fields[0], fields[2])
		} else {
			start := min(max(f.field-fieldRows+1, 0), len(fields)-fieldRows)
			out = append(out, fields[start:start+fieldRows]...)
		}
		if f.err != "" {
			out = append(out, clip(sBad.Render("  "+f.err), w))
		} else if showSuggestions {
			out = append(out, clip("  Suggestions: "+strings.Join(candidates, ", "), w))
		}
		for len(out) < h {
			out = append(out, "")
		}
		return panelWithBorder(title, out[:min(len(out), h)], w+4, roleCTO, borderColor)
	}
	var lines []string
	for _, v := range c.messages {
		lines = append(lines, c.renderMessage(v, w)...)
	}
	if c.enterNotice != "" {
		lines = append(lines, clip(sanitize(c.enterNotice), w))
	}
	var errLines []string
	if c.err != "" {
		errLines = wrapCTOLine("error: "+c.err, w)
		lines = append(lines, errLines...)
	} else if c.slow {
		lines = append(lines, sDim.Render(clip(slowText, w)))
	}
	if c.confirming {
		lines = append(lines, "Start a new conversation?"+confirmSuffix)
	}
	rows := h
	if h > 1 {
		rows-- // The last interior row is the hint.
	}
	c.pageRows = max(rows-1, 1)
	c.scrollMax = max(len(lines)-rows, 0)
	c.scroll = min(c.scroll, c.scrollMax)
	if len(errLines) >= rows && rows > 0 {
		// An error that fills the box is more useful from its start: its
		// tail alone reads like the end of a sentence with no beginning.
		start := c.scroll
		if start > len(errLines)-rows {
			start = max(len(errLines)-rows, 0)
		}
		lines = append([]string(nil), errLines[start:min(start+rows, len(errLines))]...)
		if start+rows < len(errLines) {
			lines[len(lines)-1] = ctoClipped(lines[len(lines)-1], w)
		}
	} else {
		end := len(lines) - c.scroll
		start := max(end-rows, 0)
		if start > 0 && rows > 1 {
			start++
			lines = append([]string{sDim.Render("↑ more")}, lines[start:end]...)
		} else {
			lines = lines[start:end]
		}
	}
	for len(out) < rows-len(lines) {
		out = append(out, "")
	}
	for _, v := range lines {
		out = append(out, clip(v, w))
	}
	if h > 1 {
		out = append(out, m.onboardingLine(w))
	}
	box := panelWithBorder(title, out, w+4, roleCTO, borderColor)
	if h == 1 {
		// With a single interior row, put the hint in the bottom border so
		// the newest CTO line remains visible.
		hint := m.onboardingLine(w)
		border := lipgloss.NewStyle().Foreground(borderColor)
		box[len(box)-1] = border.Render("╰─") + hint + border.Render(strings.Repeat("─", max(w+1-lipgloss.Width(hint), 0))+"╯")
	}
	return box
}

// ctoClipped marks a line that continues below even when the whole wrapped
// line fits: the box shows the start of the message, not its tail.
func ctoClipped(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(text) >= width {
		return ansi.Truncate(text, max(width-1, 0), "") + "…"
	}
	return text + "…"
}

// wrapCTOLine indents continuation rows while keeping their text in the box.
func wrapCTOLine(text string, width int) []string {
	var parts []string
	for _, line := range strings.Split(sanitizeBody(text), "\n") {
		clean := sanitizeBody(line)
		w0 := max(width, 8)
		wCont := max(width-2, 8)
		words := strings.Fields(clean)
		if len(words) == 0 {
			parts = append(parts, "")
			continue
		}
		cur := words[0]
		maxW := w0
		for _, word := range words[1:] {
			if ansi.StringWidth(cur)+1+ansi.StringWidth(word) <= maxW {
				cur += " " + word
			} else {
				parts = append(parts, cur)
				cur = word
				maxW = wCont
			}
		}
		parts = append(parts, cur)
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
	key := ctoRenderKey{text, width, palette.IsDark(), palette.GlamourProfile()}
	if lines, ok := c.renderCache[key]; ok {
		return lines
	}
	style := styles.DarkStyleConfig
	if !key.dark {
		style = styles.LightStyleConfig
	}
	color := func(role color.Color) *string {
		adaptive := role.(palette.Adaptive)
		original := adaptive.Light
		if key.dark {
			original = adaptive.Dark
		}
		r, g, b, _ := original.RGBA()
		value := fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
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

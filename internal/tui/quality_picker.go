package tui

import (
	"fmt"
	"image/color"
	"math"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/palette"
)

// The Quality subtab shows the worker picker exactly as it decides: one row
// per registered worker profile, in the order of the Workers panel (benchmark
// score, strongest first), one column per D bucket that has tasks, and the
// cell scores. The verdicts come from protocol.PickerBucketVerdicts, the same
// function the picker runs, so the screen cannot drift from the rule.
//
// A cell with no task shows a dot, not the score the picker would shrink to
// the bucket mean, and has no colour. A scored cell is a block of the score
// on a sequential scale: dark red at 0.50 or less, amber in the middle, green
// at 0.95 or more. The scale is fixed, so one colour means one score on every
// screen. A cell with one or two tasks is the same colour blended toward the
// panel, so it reads as less sure. Cells past the profile's benchmark ceiling
// are underlined, and the selected cell is in brackets.

// pickerCellWidth is the width of a bucket column: a block of six columns, the
// four characters of the number and one blank on each side.
const pickerCellWidth = 6

// pickerModelWidth is the label column: the selected-row mark and a model key.
const pickerModelWidth = 28

// pickerHarnessWidth is the harness column. It fits "opencode".
const pickerHarnessWidth = 8

// pickerScoreWidth is the benchmark score column.
const pickerScoreWidth = 4

// pickerLeftWidth is everything left of the first bucket column: the model key,
// a space, the harness, a space, the benchmark score and a two column gap.
func pickerLeftWidth(labelWidth int) int {
	return labelWidth + 1 + pickerHarnessWidth + 1 + pickerScoreWidth + 2
}

// qualityDetailsView is the whole subtab. It includes the title and the
// subtabs line, and returns exactly h rows.
func (m Model) qualityDetailsView(w, h int) []string {
	out := []string{m.titleLine(w), m.quotasSubtabs(w)}
	if !m.pickerLoaded {
		body := []string{sDim.Render("Reading the picker table...")}
		if m.pickerErr != "" {
			body = append(body, sBad.Render(clip("Could not read the picker table: "+m.pickerErr, w-4)))
		}
		body = append(body, sDim.Render("It changes only on writes: R reads it again."))
		out = append(out, panel("Quality · worker picker", body, w, roleAccent)...)
		for len(out) < h {
			out = append(out, "")
		}
		if len(out) > h {
			out = out[:h]
		}
		return out
	}
	body := m.qualityPickerBody(w, max(h-4, 1))
	out = append(out, panel("Quality · worker picker", body, w, roleAccent)...)
	for len(out) < h {
		out = append(out, "")
	}
	if len(out) > h {
		out = out[:h]
	}
	return out
}

// qualityPickerBody builds the panel body: the grid, the selected cell and its
// verdict, the settings and the legend.
func (m Model) qualityPickerBody(w, height int) []string {
	selModel, selBucket := m.pickerSelected()
	cols := m.pickerColumns()
	rows, _, hidden := m.pickerRowSet()
	if len(cols) == 0 {
		body := []string{sDim.Render("no scored tasks yet: the table fills as workers finish")}
		body = append(body, sDim.Render(pickerSettingsText(m.picker.Settings, w-4)))
		body = append(body, sDim.Render(clip(pickerLegend()[0], w-4)))
		body = append(body, sDim.Render(clip(pickerLegend()[1], w-4)))
		return body
	}

	labelWidth := pickerModelWidth
	inner := max(w-4, 20)
	if inner < pickerLeftWidth(labelWidth)+3*pickerCellWidth {
		labelWidth = max(12, inner/3)
	}
	fit := max(1, (inner-pickerLeftWidth(labelWidth))/pickerCellWidth)
	start, end := pickerColumnWindow(cols, selBucket, fit)

	body := []string{
		sDim.Render(clip(pickerGridHeader(labelWidth, "HARNESS", "AA", m.picker.Buckets, cols[start:end], "MODEL", func(b protocol.PickerBucketRow) string {
			return fmt.Sprintf("%.2f", b.Lo)
		}), inner)),
		sDim.Render(clip(pickerGridHeader(labelWidth, "", "", m.picker.Buckets, cols[start:end], "bucket mean", func(b protocol.PickerBucketRow) string {
			return fmt.Sprintf("%.2f", b.Mean)
		}), inner)),
		sDim.Render(clip(pickerGridHeader(labelWidth, "", "", m.picker.Buckets, cols[start:end], "tasks", func(b protocol.PickerBucketRow) string {
			return fmt.Sprintf("n%d", b.N)
		}), inner)),
	}
	if hint := pickerColumnsHint(start, end, len(cols)); hint != "" {
		body = append(body, sDim.Render(clip(hint, inner)))
	}

	fixed := 1 + 4 + 1 + 1 + len(pickerLegend()) // section, detail, settings, ramp, legend
	if m.pickerErr != "" {
		fixed++
	}
	if hidden > 0 {
		fixed++
	}
	room := max(height-len(body)-fixed, 1)
	scrolling := len(rows) > room-1
	if scrolling {
		room--
	}
	rowStart, rowEnd := pickerRowWindow(len(rows), selModel, max(room, 1))
	for i := rowStart; i < rowEnd; i++ {
		body = append(body, m.pickerModelLine(rows[i], i == selModel, labelWidth, cols[start:end], inner))
	}
	if scrolling {
		body = append(body, sDim.Render(clip(fmt.Sprintf("profiles %d-%d of %d · up/down moves", rowStart+1, rowEnd, len(rows)), inner)))
	}
	if hidden > 0 {
		note := fmt.Sprintf("%d older model keys hidden · o to show", hidden)
		if m.pickerShowOlder {
			note = fmt.Sprintf("%d older model keys shown, dim · o to hide", hidden)
		}
		body = append(body, sDim.Render(clip(note, inner)))
	}
	body = append(body, pickerRampLine())

	body = append(body, section("Selected cell", inner))
	body = append(body, m.pickerDetailLines(selModel, selBucket, inner)...)
	body = append(body, sDim.Render(clip(pickerSettingsText(m.picker.Settings, inner), inner)))
	if m.pickerErr != "" {
		body = append(body, sBad.Render(clip("Last picker read failed: "+m.pickerErr, inner)))
	}
	for _, line := range pickerLegend() {
		body = append(body, sDim.Render(clip(line, inner)))
	}
	return body
}

// pickerGridHeader is one header row: the model key, the harness, the
// benchmark score column and one value per visible bucket.
func pickerGridHeader(labelWidth int, harness, aa string, buckets []protocol.PickerBucketRow, cols []int, label string, value func(protocol.PickerBucketRow) string) string {
	var b strings.Builder
	b.WriteString(cell(label, labelWidth))
	b.WriteString(" ")
	b.WriteString(cell(harness, pickerHarnessWidth))
	b.WriteString(" ")
	b.WriteString(cellR(aa, pickerScoreWidth))
	b.WriteString("  ")
	for _, col := range cols {
		if col < 0 || col >= len(buckets) {
			continue
		}
		b.WriteString(" " + cellR(value(buckets[col]), 4) + " ")
	}
	return b.String()
}

// pickerRow is one line of the grid: a registered worker profile, or an older
// model key that matches no profile.
type pickerRow struct {
	// name is the profile name, kept for the order and the picker rule, never
	// drawn in the grid. key is the model key the worker log shows.
	name string
	key  string
	// harness is the harness that runs the profile, in its own column.
	harness string
	// score is the Artificial Analysis score the profile is sorted by.
	score  float64
	scored bool
	// ceiling is the top of the benchmark level; ceilinged is false when the
	// profile has no benchmark score.
	ceiling   float64
	ceilinged bool
	older     bool
	cells     []protocol.PickerCell
}

// pickerModelNorm is the form in which a model key and a profile key are
// compared: the same model with or without its provider prefix is one model.
func pickerModelNorm(key string) string {
	return strings.ToLower(protocol.PickerModelDisplay(key))
}

// pickerRowSet is the rows of the grid, the number of profile rows (they come
// first) and the number of older model keys that match no profile. The older
// rows are included only when the owner asked for them. The profile order is
// the Workers panel's: score first, strongest first, unscored last, then name.
// Two history keys that are one profile (the same model with and without its
// provider prefix) are merged: per bucket the cell with more tasks wins, as the
// cells cannot be added up from their summaries. The same model under two
// harnesses is two rows, because the harness is part of what the owner reads.
func (m Model) pickerRowSet() ([]pickerRow, int, int) {
	profiles := append([]protocol.PickerProfileRow(nil), m.picker.Profiles...)
	sort.SliceStable(profiles, func(i, j int) bool {
		a, b := profiles[i], profiles[j]
		if a.Scored != b.Scored {
			return a.Scored
		}
		if a.Scored && a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.Name < b.Name
	})
	bench := m.pickerBenchmarks()
	var rows []pickerRow
	byNorm := map[string][]int{}
	seenProfile := map[string]bool{}
	for _, p := range profiles {
		if p.Key == "" {
			continue
		}
		harness := firstNonEmpty(m.pickerProfileHarness(p.Name), m.pickerHarnessByKey(p.Key))
		norm := pickerModelNorm(p.Key)
		rowKey := harness + "\x00" + norm
		if seenProfile[rowKey] {
			continue
		}
		seenProfile[rowKey] = true
		byNorm[norm] = append(byNorm[norm], len(rows))
		b := bench[p.Key]
		rows = append(rows, pickerRow{name: p.Name, key: p.Key, harness: harness, score: b.score, scored: b.scored, ceiling: b.ceiling, ceilinged: b.scored})
	}
	nProfiles := len(rows)
	older := map[string]*pickerRow{}
	var olderOrder []string
	for _, model := range m.picker.Models {
		norm := pickerModelNorm(model.Key)
		if idxs, ok := byNorm[norm]; ok {
			for _, i := range idxs {
				rows[i].cells = mergePickerCells(rows[i].cells, model.Cells)
			}
			continue
		}
		if r, ok := older[norm]; ok {
			r.cells = mergePickerCells(r.cells, model.Cells)
			continue
		}
		older[norm] = &pickerRow{name: model.Display, key: model.Key, harness: m.pickerHarnessByKey(model.Key), older: true, cells: mergePickerCells(nil, model.Cells)}
		olderOrder = append(olderOrder, norm)
	}
	sort.Strings(olderOrder)
	if m.pickerShowOlder {
		for _, norm := range olderOrder {
			rows = append(rows, *older[norm])
		}
	}
	return rows, nProfiles, len(olderOrder)
}

// pickerProfileHarness is the harness of a registered profile, matched by its
// name, which the picker and the snapshot both keep.
func (m Model) pickerProfileHarness(name string) string {
	for _, p := range m.snap.WorkerProfiles {
		if p.Name == name {
			return p.Harness
		}
	}
	return ""
}

// pickerHarnessByKey finds the harness that runs a model key: a registered
// profile first, then a recorded run, then the shape of the key itself.
func (m Model) pickerHarnessByKey(key string) string {
	norm := strings.ToLower(key)
	for _, p := range m.snap.WorkerProfiles {
		if p.Harness != "" && strings.ToLower(protocol.PickerModelKey(p.Harness, p.Selection().LegacyModel())) == norm {
			return p.Harness
		}
	}
	for _, n := range m.snap.Nodes {
		for _, t := range n.Tracks {
			if t.Harness != "" && strings.ToLower(protocol.PickerModelKey(t.Harness, t.Model)) == norm {
				return t.Harness
			}
		}
	}
	return pickerHarnessFromKey(key)
}

// pickerHarnessFromKey reads the harness a key can only come from. A provider
// prefix names it; a dash effort is how agy writes the effort into the id.
func pickerHarnessFromKey(key string) string {
	key = strings.ToLower(key)
	display := protocol.PickerModelDisplay(key)
	dashEffort := ""
	if _, effort := protocol.SplitModelLevel(display); effort == "" {
		dashEffort = protocol.ParseModelSelection("agy", "", display, "").Effort
	}
	if prefix, _, ok := strings.Cut(key, "/"); ok {
		switch prefix {
		case "openai-codex", "openai":
			return "codex"
		case "opencode-go", "openrouter":
			return "opencode"
		case "anthropic":
			return "claude"
		}
		return ""
	}
	if dashEffort != "" {
		return "agy"
	}
	return ""
}

// mergePickerCells keeps, per bucket, the cell with more tasks.
func mergePickerCells(a, b []protocol.PickerCell) []protocol.PickerCell {
	out := make([]protocol.PickerCell, max(len(a), len(b)))
	for i := range out {
		var ca, cb protocol.PickerCell
		if i < len(a) {
			ca = a[i]
		}
		if i < len(b) {
			cb = b[i]
		}
		if cb.N > ca.N {
			ca = cb
		}
		out[i] = ca
	}
	return out
}

func (r pickerRow) cellAt(bucket int) protocol.PickerCell {
	if bucket >= 0 && bucket < len(r.cells) {
		return r.cells[bucket]
	}
	return protocol.PickerCell{}
}

// pickerModelLabelForKey is a model key without its provider prefix and
// without the ":default" the picker adds for a Codex run with no effort. It is
// the name the worker log shows.
func pickerModelLabelForKey(key string) string {
	return strings.TrimSuffix(protocol.PickerModelDisplay(key), ":default")
}

// pickerRowLabel is the row's model key, exactly as the owner reads it: the
// model without its provider prefix and the effort after a colon. agy stores
// the effort after a dash, so its rows are converted to the same form.
func pickerRowLabel(row pickerRow) string {
	label := pickerModelLabelForKey(row.key)
	if row.harness != "agy" {
		return label
	}
	if _, effort := protocol.SplitModelLevel(label); effort != "" {
		return label
	}
	if sel := protocol.ParseModelSelection("agy", "", label, ""); sel.Effort != "" {
		return protocol.PickerModelDisplay(sel.Model) + ":" + sel.Effort
	}
	return label
}

// pickerModelLine draws one row: the selected mark, the model key, the harness,
// its benchmark score and one fixed width cell per visible bucket. Cells past
// the profile's benchmark ceiling are underlined.
func (m Model) pickerModelLine(row pickerRow, selected bool, labelWidth int, cols []int, width int) string {
	mark := "  "
	if selected {
		mark = "▸ "
	}
	score := "-"
	if row.scored {
		score = fmt.Sprintf("%g", row.score)
	}
	label := cell(mark+pickerModelLabel(sanitize(pickerRowLabel(row)), labelWidth-2), labelWidth) + " " +
		cell(sanitize(firstNonEmpty(row.harness, "-")), pickerHarnessWidth) + " " + cellR(score, pickerScoreWidth)
	if row.older {
		label = sDim.Render(label)
	}
	line := label + "  "
	for _, col := range cols {
		c := row.cellAt(col)
		lo := 0.0
		if col >= 0 && col < len(m.picker.Buckets) {
			lo = m.picker.Buckets[col].Lo
		}
		past := row.ceilinged && lo >= row.ceiling
		sel := selected && col == m.pickerSelBucket
		line += pickerCellStyle(c, sel, past, row.older).Render(pickerCellText(c, sel))
	}
	return clip(line, width)
}

// pickerCellText is the six column text of a bucket cell: the score with a
// blank on each side, or a dot for no task. The selected cell is in brackets,
// so the owner can find it without a background that would clash with the
// scale.
func pickerCellText(c protocol.PickerCell, selected bool) string {
	num := cellR("·", 4)
	if c.N > 0 {
		num = fmt.Sprintf("%4.2f", c.Score)
	}
	if selected {
		return "[" + num + "]"
	}
	return " " + num + " "
}

// pickerCellStyle is the look of a bucket cell. A cell with no task, and a cell
// of an older model key, has no colour and is dim. A scored cell is on the
// scale; a cell with one or two tasks is faded. A cell past the benchmark
// ceiling is underlined. The selected cell is bold.
func pickerCellStyle(c protocol.PickerCell, selected, pastCeiling, older bool) lipgloss.Style {
	style := lipgloss.NewStyle()
	thin := c.N < protocol.PickerMinCellTasks
	switch {
	case c.N < 1 || older:
		style = style.Faint(true)
	default:
		if bg, fg := pickerHeat(c.Score, thin); bg != nil {
			style = style.Background(bg).Foreground(fg)
			// Sixteen colours cannot fade a background, so a thin cell is dim too.
			if thin && palette.Profile() == colorprofile.ANSI {
				style = style.Faint(true)
			}
		} else if thin {
			style = style.Faint(true)
		}
	}
	if pastCeiling {
		style = style.Underline(true)
	}
	if selected {
		style = style.Bold(true)
	}
	return style
}

// The scale runs from pickerScaleLow to pickerScaleHigh. The colours are fixed
// hex values, so a background looks the same in a dark and a light terminal.
const (
	pickerScaleLow  = 0.50
	pickerScaleHigh = 0.95
)

// pickerScaleStops are dark red, amber and green. Each is one end or the middle
// of the scale.
var pickerScaleStops = [3]color.RGBA{
	{R: 0xa1, G: 0x2b, B: 0x35, A: 0xff},
	{R: 0xf0, G: 0xb3, B: 0x5a, A: 0xff},
	{R: 0x3a, G: 0x9a, B: 0x4f, A: 0xff},
}

// pickerScaleT is the place of a score on the scale, from 0 to 1.
func pickerScaleT(score float64) float64 {
	return math.Min(math.Max((score-pickerScaleLow)/(pickerScaleHigh-pickerScaleLow), 0), 1)
}

// pickerScaleRGB is the colour of a score on the scale.
func pickerScaleRGB(score float64) color.Color {
	t := pickerScaleT(score)
	if t <= .5 {
		return blendRGB(pickerScaleStops[0], pickerScaleStops[1], t*2)
	}
	return blendRGB(pickerScaleStops[1], pickerScaleStops[2], (t-.5)*2)
}

// pickerHeat is the background and the text colour of a scored cell. A faded
// cell is blended halfway toward the panel. Both are nil when the terminal
// shows no colour. A sixteen colour terminal gets three steps: red, yellow and
// green, cut at the thirds of the scale.
func pickerHeat(score float64, faded bool) (bg, fg color.Color) {
	switch palette.Profile() {
	case colorprofile.ANSI:
		switch t := pickerScaleT(score); {
		case t < 1.0/3:
			return lipgloss.Color("1"), lipgloss.Color("15")
		case t < 2.0/3:
			return lipgloss.Color("3"), lipgloss.Color("0")
		default:
			return lipgloss.Color("2"), lipgloss.Color("0")
		}
	case colorprofile.ANSI256, colorprofile.TrueColor:
		c := pickerScaleRGB(score)
		if faded {
			c = blendRGB(c, roleBase, .5)
		}
		return c, pickerTextOn(c)
	default:
		return nil, nil
	}
}

// pickerTextOn is the text colour with more contrast on the background: white
// or near black.
func pickerTextOn(bg color.Color) color.Color {
	white := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	ink := color.RGBA{R: 0x11, G: 0x15, B: 0x1c, A: 0xff}
	if contrastRatio(bg, white) >= contrastRatio(bg, ink) {
		return white
	}
	return ink
}

// contrastRatio is the WCAG contrast of two colours, from 1 to 21.
func contrastRatio(a, b color.Color) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + .05) / (lb + .05)
}

func relativeLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	lin := func(v uint32) float64 {
		x := float64(v>>8) / 255
		if x <= .03928 {
			return x / 12.92
		}
		return math.Pow((x+.055)/1.055, 2.4)
	}
	return .2126*lin(r) + .7152*lin(g) + .0722*lin(b)
}

// pickerRampLine is the legend under the grid: a ramp of the scale with 0.50,
// 0.70 and 0.90 written on it, and what a faded cell means.
func pickerRampLine() string {
	const cells = 28
	scoreAt := func(i int) float64 {
		return pickerScaleLow + (pickerScaleHigh-pickerScaleLow)*float64(i)/(cells-1)
	}
	cellAt := func(score float64) int {
		return int(math.Round((score - pickerScaleLow) / (pickerScaleHigh - pickerScaleLow) * (cells - 1)))
	}
	// The marks are written on the ramp, each at the cell of its score.
	marks := map[int]string{cellAt(pickerScaleLow): "0.50", cellAt(.70): "0.70", cellAt(.90): "0.90"}
	text := make([]string, cells)
	for i := range text {
		text[i] = " "
	}
	for at, s := range marks {
		for k := 0; k < len(s); k++ {
			text[at+k] = s[k : k+1]
		}
	}
	var b strings.Builder
	b.WriteString(sDim.Render("score "))
	for i := range text {
		style := sDim
		if bg, fg := pickerHeat(scoreAt(i), false); bg != nil {
			style = lipgloss.NewStyle().Background(bg).Foreground(fg)
		} else if text[i] == " " {
			// No colour: a line stands for the ramp, and the marks stay as text.
			text[i] = "─"
		}
		b.WriteString(style.Render(text[i]))
	}
	b.WriteString("  ")
	faded := sDim
	if bg, fg := pickerHeat(.725, true); bg != nil {
		faded = lipgloss.NewStyle().Background(bg).Foreground(fg)
	}
	b.WriteString(faded.Render("faded"))
	b.WriteString(" = 1-2 tasks")
	return b.String()
}

// pickerDetailLines is the selected-cell pane: the cell, the bucket, the best
// profile there and the verdict of the rule.
func (m Model) pickerDetailLines(selModel, selBucket, width int) []string {
	rows, nProfiles, _ := m.pickerRowSet()
	if selModel < 0 || selModel >= len(rows) || selBucket < 0 || selBucket >= len(m.picker.Buckets) {
		return []string{sDim.Render("no cell selected")}
	}
	row := rows[selModel]
	bucket := m.picker.Buckets[selBucket]
	c := row.cellAt(selBucket)
	_, verdicts, best := m.pickerVerdicts(selBucket)
	title := sanitize(pickerRowLabel(row))
	if row.harness != "" {
		title += " · " + row.harness
	}
	if row.older {
		title += " (older model key)"
	}
	lines := []string{fmt.Sprintf("%s · bucket %.2f-%.2f · n%d", title, bucket.Lo, bucket.Hi, c.N)}
	if c.N == 0 {
		lines = append(lines, fmt.Sprintf("no task yet · bucket mean %.2f", bucket.Mean))
	} else {
		lines = append(lines, fmt.Sprintf("score %.2f [%.2f-%.2f] · mean %.2f", c.Score, c.Lo, c.Hi, bucket.Mean))
	}
	if best >= 0 && best < nProfiles && best < len(rows) {
		bestCell := rows[best].cellAt(selBucket)
		bestLabel := sanitize(pickerRowLabel(rows[best]))
		if rows[best].harness != "" {
			bestLabel += " · " + rows[best].harness
		}
		lines = append(lines, fmt.Sprintf("best %s %.2f [%.2f-%.2f] n%d", bestLabel, bestCell.Score, bestCell.Lo, bestCell.Hi, bestCell.N))
	} else {
		lines = append(lines, "best: no profile has 3 tasks here")
	}
	switch {
	case row.older:
		lines = append(lines, "verdict: none, this key matches no registered profile")
	case selModel < len(verdicts):
		lines = append(lines, "verdict: "+pickerVerdictText(verdicts[selModel]))
	default:
		lines = append(lines, "verdict: "+pickerVerdictText(protocol.PickerVerdict{}))
	}
	for i := range lines {
		lines[i] = clip(lines[i], width)
	}
	return lines
}

// pickerVerdictText is the owner's words for the picker's verdict.
func pickerVerdictText(v protocol.PickerVerdict) string {
	switch {
	case v.Allowed && v.Prior:
		return "chosen by benchmark (n<3)"
	case v.Allowed:
		return "allowed"
	case v.Reason != "":
		return "left out (" + v.Reason + ")"
	default:
		return "left out"
	}
}

// pickerBench is what the picker knows about a model key from its profiles.
type pickerBench struct {
	scored  bool
	score   float64
	ceiling float64
}

// pickerBenchmarks folds the profiles by key. Two profiles with the same key
// are one row, and the higher ceiling is the one the row marks.
func (m Model) pickerBenchmarks() map[string]pickerBench {
	out := map[string]pickerBench{}
	for _, p := range m.picker.Profiles {
		if p.Key == "" {
			continue
		}
		cur, ok := out[p.Key]
		if !p.Scored {
			if !ok {
				out[p.Key] = pickerBench{}
			}
			continue
		}
		if !cur.scored || p.Ceiling > cur.ceiling || p.Ceiling == cur.ceiling && p.Score > cur.score {
			out[p.Key] = pickerBench{scored: true, score: p.Score, ceiling: p.Ceiling}
		}
	}
	return out
}

// pickerFacts is the input of the protocol's cell verdict for one bucket. Only
// registered profiles are candidates, as in the picker: an older model key is
// not, and showing or hiding it does not change any verdict.
func (m Model) pickerFacts(bucket int) []protocol.PickerCandidateFacts {
	m.pickerShowOlder = false
	rows, _, _ := m.pickerRowSet()
	facts := make([]protocol.PickerCandidateFacts, len(rows))
	for i, row := range rows {
		facts[i] = protocol.PickerCandidateFacts{Key: row.key, Cell: row.cellAt(bucket), Scored: row.ceilinged, Benchmark: row.score, Ceiling: row.ceiling}
	}
	return facts
}

// pickerVerdicts runs the same protocol function the picker runs, over the
// profile rows. The third result is the index of the best profile in the
// bucket, or -1 when no profile has three tasks there.
func (m Model) pickerVerdicts(bucket int) ([]protocol.PickerCandidateFacts, []protocol.PickerVerdict, int) {
	if bucket < 0 || bucket >= len(m.picker.Buckets) {
		return nil, nil, -1
	}
	facts := m.pickerFacts(bucket)
	b := m.picker.Buckets[bucket]
	verdicts, best := protocol.PickerBucketVerdicts(facts, b.Lo, b.Mean, b.Index, m.picker.Settings)
	return facts, verdicts, best
}

// pickerColumns is the position of every bucket that has tasks.
func (m Model) pickerColumns() []int {
	var cols []int
	for i, b := range m.picker.Buckets {
		if b.N > 0 {
			cols = append(cols, i)
		}
	}
	return cols
}

// pickerRowCount is the number of rows the grid shows.
func (m Model) pickerRowCount() int {
	rows, _, _ := m.pickerRowSet()
	return len(rows)
}

// pickerSelected clamps the selected cell to a model and a bucket that exist.
func (m Model) pickerSelected() (int, int) {
	row := 0
	if n := m.pickerRowCount(); n > 0 {
		row = min(max(m.pickerSelModel, 0), n-1)
	}
	cols := m.pickerColumns()
	if len(cols) == 0 {
		return row, 0
	}
	col := cols[0]
	for _, c := range cols {
		if c == m.pickerSelBucket {
			col = c
			break
		}
		if c > m.pickerSelBucket {
			break
		}
		col = c
	}
	return row, col
}

// defaultPickerSelection opens the subtab on the first bucket and its best
// model, so the detail pane starts with a cell that has a record.
func (m *Model) defaultPickerSelection() {
	cols := m.pickerColumns()
	if len(cols) == 0 {
		return
	}
	m.pickerSelBucket = cols[0]
	m.pickerSelModel = 0
	if _, _, best := m.pickerVerdicts(cols[0]); best >= 0 {
		m.pickerSelModel = best
	}
}

func (m *Model) clampPickerSelection() {
	m.pickerSelModel, m.pickerSelBucket = m.pickerSelected()
}

func (m *Model) movePickerModel(delta int) {
	n := m.pickerRowCount()
	if n == 0 {
		return
	}
	row, _ := m.pickerSelected()
	m.pickerSelModel = min(max(row+delta, 0), n-1)
}

func (m *Model) movePickerBucket(delta int) {
	cols := m.pickerColumns()
	if len(cols) == 0 {
		return
	}
	_, col := m.pickerSelected()
	pos := 0
	for i, c := range cols {
		if c == col {
			pos = i
			break
		}
	}
	pos = min(max(pos+delta, 0), len(cols)-1)
	m.pickerSelBucket = cols[pos]
}

// pickerColumnWindow centres the visible buckets on the selected one.
func pickerColumnWindow(cols []int, selected, fit int) (int, int) {
	if len(cols) <= fit {
		return 0, len(cols)
	}
	pos := 0
	for i, c := range cols {
		if c == selected {
			pos = i
			break
		}
	}
	start := pos - fit/2
	if start < 0 {
		start = 0
	}
	if start+fit > len(cols) {
		start = len(cols) - fit
	}
	return start, start + fit
}

// pickerRowWindow keeps the selected model on screen when the table is taller
// than the panel.
func pickerRowWindow(rows, selected, fit int) (int, int) {
	if rows <= fit {
		return 0, rows
	}
	start := selected - fit/2
	if start < 0 {
		start = 0
	}
	if start+fit > rows {
		start = rows - fit
	}
	return start, start + fit
}

// pickerColumnsHint says how many buckets are outside the window.
func pickerColumnsHint(start, end, total int) string {
	var parts []string
	if start > 0 {
		parts = append(parts, fmt.Sprintf("◂ %d more", start))
	}
	if end < total {
		parts = append(parts, fmt.Sprintf("%d more ▸", total-end))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ") + " · left/right moves buckets"
}

// pickerModelLabel keeps a model key within w columns, but keeps the effort
// suffix when the key has one. It cuts the model name, never the effort.
func pickerModelLabel(name string, w int) string {
	if ansi.StringWidth(name) <= w {
		return name
	}
	base, effort := protocol.SplitModelLevel(name)
	sep := ":"
	if effort == "" {
		// agy writes the effort into the id, as in "claude-sonnet-5-5-medium".
		if sel := protocol.ParseModelSelection("agy", "", name, ""); sel.Effort != "" && sel.Model != name {
			base, effort, sep = sel.Model, sel.Effort, "-"
		}
	}
	suffix := ""
	if effort != "" {
		suffix = sep + effort
	}
	room := w - ansi.StringWidth(suffix)
	if room < 1 {
		return ansi.Truncate(suffix, w, "")
	}
	tail := "…"
	if room < 3 {
		tail = ""
	}
	return ansi.Truncate(base, room, tail) + suffix
}

// pickerSettingsText is the picker rule in one line. The full wording gives
// way to the short one when the terminal is narrow, never to a wrapped label.
func pickerSettingsText(s protocol.PickerSettings, width int) string {
	s = s.Normalized()
	full := fmt.Sprintf("settings: bucket width %.2f · k %g · allowed fraction %.2f · boost %g · uncertain n %d",
		s.BucketWidth, s.K, s.AllowedFraction, s.UncertainBoost, s.UncertainN)
	if ansi.StringWidth(full) <= width {
		return full
	}
	short := fmt.Sprintf("bucket %.2f · k %g · allowed %.2f · boost %g · uncertain n %d",
		s.BucketWidth, s.K, s.AllowedFraction, s.UncertainBoost, s.UncertainN)
	return short
}

func pickerLegend() []string {
	return []string{
		"AA = benchmark score · dot = no task yet · underline = past ceiling",
		"[0.80] = selected cell · o = older model keys · R = read again",
	}
}

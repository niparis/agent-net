package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// The Quality subtab shows the worker picker exactly as it decides: one row
// per model key, one column per D bucket that has tasks, and the cell scores
// with their 90 percent intervals. The verdicts come from
// protocol.PickerBucketVerdicts, the same function the picker runs, so the
// screen cannot drift from the rule.

// pickerCellWidth is the width of a bucket column: four characters for the
// number and two for the separator or the benchmark ceiling mark.
const pickerCellWidth = 6

// pickerModelWidth is the label column. It leaves room for the selected-row
// mark and a model name with its effort at 80 columns.
const pickerModelWidth = 26

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
	if len(cols) == 0 {
		body := []string{sDim.Render("no scored tasks yet: the table fills as workers finish")}
		body = append(body, sDim.Render(pickerSettingsText(m.picker.Settings, w-4)))
		body = append(body, sDim.Render(clip(pickerLegend()[0], w-4)))
		body = append(body, sDim.Render(clip(pickerLegend()[1], w-4)))
		return body
	}

	labelWidth := pickerModelWidth
	inner := max(w-4, 20)
	if inner < labelWidth+2+3*pickerCellWidth {
		labelWidth = max(12, inner/3)
	}
	fit := max(1, (inner-labelWidth-2)/pickerCellWidth)
	start, end := pickerColumnWindow(cols, selBucket, fit)

	body := []string{
		sDim.Render(clip(pickerGridHeader(labelWidth, m.picker.Buckets, cols[start:end], "MODEL", func(b protocol.PickerBucketRow) string {
			return fmt.Sprintf("%.2f", b.Lo)
		}), inner)),
		sDim.Render(clip(pickerGridHeader(labelWidth, m.picker.Buckets, cols[start:end], "mean", func(b protocol.PickerBucketRow) string {
			return fmt.Sprintf("%.2f", b.Mean)
		}), inner)),
		sDim.Render(clip(pickerGridHeader(labelWidth, m.picker.Buckets, cols[start:end], "n", func(b protocol.PickerBucketRow) string {
			return fmt.Sprintf("n%d", b.N)
		}), inner)),
	}
	if hint := pickerColumnsHint(start, end, len(cols)); hint != "" {
		body = append(body, sDim.Render(clip(hint, inner)))
	}

	fixed := 1 + 4 + 1 + len(pickerLegend()) // section, detail, settings, legend
	if m.pickerErr != "" {
		fixed++
	}
	room := max(height-len(body)-fixed, 1)
	scrolling := len(m.picker.Models) > room-1
	if scrolling {
		room--
	}
	bench := m.pickerBenchmarks()
	rowStart, rowEnd := pickerRowWindow(len(m.picker.Models), selModel, max(room, 1))
	for i := rowStart; i < rowEnd; i++ {
		body = append(body, m.pickerModelLine(i, selModel, labelWidth, cols[start:end], inner, bench))
	}
	if scrolling {
		body = append(body, sDim.Render(clip(fmt.Sprintf("models %d-%d of %d · up/down moves", rowStart+1, rowEnd, len(m.picker.Models)), inner)))
	}

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

// pickerGridHeader is one header row: the label and one value per visible
// bucket.
func pickerGridHeader(labelWidth int, buckets []protocol.PickerBucketRow, cols []int, label string, value func(protocol.PickerBucketRow) string) string {
	var b strings.Builder
	b.WriteString(cell(label, labelWidth))
	b.WriteString("  ")
	for _, col := range cols {
		if col < 0 || col >= len(buckets) {
			continue
		}
		b.WriteString(cellR(value(buckets[col]), 4))
		b.WriteString("  ")
	}
	return b.String()
}

// pickerModelLine draws one model row. The selected row has a marker, and a
// │ after the last visible bucket the profile's benchmark ceiling covers.
func (m Model) pickerModelLine(index, selected, labelWidth int, cols []int, width int, bench map[string]pickerBench) string {
	row := m.picker.Models[index]
	b := bench[row.Key]
	mark := "  "
	if index == selected {
		mark = "▸ "
	}
	name := pickerModelLabel(row.Display, labelWidth-2)
	line := cell(mark+name, labelWidth) + "  "
	lastCovered := -1
	if b.scored {
		for i, col := range cols {
			if col >= 0 && col < len(m.picker.Buckets) && m.picker.Buckets[col].Lo < b.ceiling {
				lastCovered = i
			}
		}
	}
	for i, col := range cols {
		c := protocol.PickerCell{}
		if col >= 0 && col < len(row.Cells) {
			c = row.Cells[col]
		}
		mean := 0.0
		if col >= 0 && col < len(m.picker.Buckets) {
			mean = m.picker.Buckets[col].Mean
		}
		line += pickerCellStyle(c, mean, index == selected && col == m.pickerSelBucket).Render(fmt.Sprintf("%4.2f", c.Score))
		if i == lastCovered {
			line += " │"
		} else {
			line += "  "
		}
	}
	return clip(line, width)
}

// pickerCellStyle shades a cell by how certain it is and colours its score
// against the bucket mean. A wide interval or fewer than three tasks is dim;
// a narrow one is bold. A cell whose whole interval is above the mean is
// green; one whose whole interval is below it is red. The selected cell is
// underlined as well, so the owner can see which column the detail describes.
func pickerCellStyle(c protocol.PickerCell, mean float64, selected bool) lipgloss.Style {
	style := lipgloss.NewStyle()
	switch {
	case c.Lo > mean:
		style = style.Foreground(roleGood)
	case c.Hi < mean:
		style = style.Foreground(roleError)
	}
	if selected {
		return style.Bold(true).Underline(true)
	}
	if c.Hi-c.Lo > .35 || c.N < protocol.PickerMinCellTasks {
		return style.Faint(true)
	}
	return style.Bold(true)
}

// pickerDetailLines is the selected-cell pane: the cell, the bucket, the best
// model there and the verdict of the rule.
func (m Model) pickerDetailLines(selModel, selBucket, width int) []string {
	if selModel < 0 || selModel >= len(m.picker.Models) || selBucket < 0 || selBucket >= len(m.picker.Buckets) {
		return []string{sDim.Render("no cell selected")}
	}
	row := m.picker.Models[selModel]
	bucket := m.picker.Buckets[selBucket]
	cell := protocol.PickerCell{}
	if selBucket < len(row.Cells) {
		cell = row.Cells[selBucket]
	}
	_, verdicts, best := m.pickerVerdicts(selBucket)
	verdict := protocol.PickerVerdict{}
	if selModel < len(verdicts) {
		verdict = verdicts[selModel]
	}
	lines := []string{
		fmt.Sprintf("%s · bucket %.2f-%.2f · n%d", sanitize(row.Display), bucket.Lo, bucket.Hi, cell.N),
		fmt.Sprintf("score %.2f [%.2f-%.2f] · mean %.2f", cell.Score, cell.Lo, cell.Hi, bucket.Mean),
	}
	if best >= 0 && best < len(m.picker.Models) {
		bestRow := m.picker.Models[best]
		bestCell := protocol.PickerCell{}
		if selBucket < len(bestRow.Cells) {
			bestCell = bestRow.Cells[selBucket]
		}
		lines = append(lines, fmt.Sprintf("best %s %.2f [%.2f-%.2f] n%d", sanitize(bestRow.Display), bestCell.Score, bestCell.Lo, bestCell.Hi, bestCell.N))
	} else {
		lines = append(lines, "best: no model has 3 tasks here")
	}
	lines = append(lines, "verdict: "+pickerVerdictText(verdict))
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

// pickerFacts is the input of the protocol's cell verdict for one bucket.
func (m Model) pickerFacts(bucket int) []protocol.PickerCandidateFacts {
	bench := m.pickerBenchmarks()
	facts := make([]protocol.PickerCandidateFacts, len(m.picker.Models))
	for i, row := range m.picker.Models {
		var cell protocol.PickerCell
		if bucket >= 0 && bucket < len(row.Cells) {
			cell = row.Cells[bucket]
		}
		b := bench[row.Key]
		facts[i] = protocol.PickerCandidateFacts{Key: row.Key, Cell: cell, Scored: b.scored, Benchmark: b.score, Ceiling: b.ceiling}
	}
	return facts
}

// pickerVerdicts runs the same protocol function the picker runs. The second
// result is the index of the best model in the bucket, or -1 when no model has
// three tasks there.
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

// pickerSelected clamps the selected cell to a model and a bucket that exist.
func (m Model) pickerSelected() (int, int) {
	row := 0
	if len(m.picker.Models) > 0 {
		row = min(max(m.pickerSelModel, 0), len(m.picker.Models)-1)
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
	if _, _, best := m.pickerVerdicts(cols[0]); best >= 0 {
		m.pickerSelModel = best
	}
}

func (m *Model) clampPickerSelection() {
	m.pickerSelModel, m.pickerSelBucket = m.pickerSelected()
}

func (m *Model) movePickerModel(delta int) {
	if len(m.picker.Models) == 0 {
		return
	}
	row, _ := m.pickerSelected()
	m.pickerSelModel = min(max(row+delta, 0), len(m.picker.Models)-1)
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

// pickerModelLabel keeps a model name within w columns, but keeps the effort
// suffix when the name has one.
func pickerModelLabel(name string, w int) string {
	if ansi.StringWidth(name) <= w {
		return name
	}
	base, effort := protocol.SplitModelLevel(name)
	suffix := ""
	if effort != "" {
		suffix = ":" + effort
	}
	room := w - ansi.StringWidth(suffix)
	if room < 3 {
		return cell(name, w)
	}
	return ansi.Truncate(base, room, "…") + suffix
}

// pickerSettingsText is the picker rule in one line. The full wording gives
// way to the short one when the terminal is narrow, never to a wrapped label.
func pickerSettingsText(s protocol.PickerSettings, width int) string {
	s = s.Normalized()
	full := fmt.Sprintf("settings: bucket width %.2f · k %g · allowed fraction %.2f · margin %.2f · boost %g · uncertain n %d",
		s.BucketWidth, s.K, s.AllowedFraction, s.OverqualifiedMargin, s.UncertainBoost, s.UncertainN)
	if ansi.StringWidth(full) <= width {
		return full
	}
	short := fmt.Sprintf("bucket %.2f · k %g · allowed %.2f · margin %.2f · boost %g · uncertain n %d",
		s.BucketWidth, s.K, s.AllowedFraction, s.OverqualifiedMargin, s.UncertainBoost, s.UncertainN)
	return short
}

func pickerLegend() []string {
	return []string{
		"bright = narrow interval · dim = wide or n<3 · │ = benchmark ceiling",
		"green = interval above the bucket mean · red = below",
	}
}

package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// The Analytics palette. It is deliberately the one ui/profile.go already
// established — cyan for structure, grey for anything the screen says about
// itself rather than about the account — so the two full-screen views read as
// the same application.
const (
	analyticsAccentTag = "cyan"
	analyticsMutedTag  = "gray"
)

var (
	// analyticsBorderColour is dimmer than the content it frames. A bright
	// border competes with the numbers for attention and wins, which is the
	// wrong way round.
	analyticsBorderColour = tcell.ColorGray
	analyticsTitleColour  = tcell.ColorAqua
)

// barBlocks are the sub-cell fills, indexed by eighths. Index 0 is a space so
// the table can be indexed directly by the remainder.
var barBlocks = [8]rune{' ', '▏', '▎', '▍', '▌', '▋', '▊', '▉'}

// analyticsPanel is a bordered text panel that lays its content out to its own
// width.
//
// The width is the problem this type exists to solve. tview only decides how
// wide a panel is while drawing it, so a renderer that builds its rows ahead of
// time has to guess — and a fixed guess is wrong in both directions: on a wide
// terminal the rows stop two thirds of the way across and leave the panel
// looking half empty, and on a narrow one they run off the edge and get
// clipped. So the panel keeps the render function rather than its output, and
// re-runs it whenever the width it is given changes.
type analyticsPanel struct {
	*tview.TextView

	render func(width int) string
	width  int
}

// defaultPanelWidth is what a panel lays out to before it has ever been drawn.
// It only has to be sane: the first Draw replaces it with the real width.
const defaultPanelWidth = 38

// SetRender replaces what the panel shows. The text is produced immediately at
// the last known width, so the caller can size the panel from it without
// waiting for a draw.
func (p *analyticsPanel) SetRender(render func(width int) string) {
	p.render = render
	if p.width <= 0 {
		p.width = defaultPanelWidth
	}
	p.SetText(render(p.width))
}

// SetStatic is SetRender for content that does not care how wide the panel is —
// an empty state, an error, a hint.
func (p *analyticsPanel) SetStatic(text string) {
	p.SetRender(func(int) string { return text })
}

// Draw re-lays the content out when the width has changed, then draws.
//
// The line count is unaffected: nothing here wraps, and a renderer only ever
// changes how a row is spread across its columns, never how many rows there
// are. That is what lets the height be fixed before the width is known.
func (p *analyticsPanel) Draw(screen tcell.Screen) {
	if p.render != nil {
		if _, _, w, _ := p.GetInnerRect(); w > 0 && w != p.width {
			p.width = w
			p.SetText(p.render(w))
		}
	}
	p.TextView.Draw(screen)
}

// shareRow draws one "name value share bar" row across the whole width.
//
// The bar takes whatever the fixed columns leave, so a wide panel gets a long
// bar with the resolution to match rather than a short one adrift in the
// middle of the row. When the panel is too narrow for a bar worth drawing, the
// row keeps the numbers and drops it.
func shareRow(name string, value, share float64, width int) string {
	const (
		valueWidth = 12
		shareWidth = 6
		minBar     = 4
	)

	nameWidth := width - valueWidth - shareWidth - 3
	if nameWidth > 16 {
		nameWidth = 16
	}
	if nameWidth < 6 {
		nameWidth = 6
	}

	row := fmt.Sprintf("%-*s %*s %*s",
		nameWidth, tview.Escape(truncate(name, nameWidth)),
		valueWidth, formatNumber(value, 0),
		shareWidth, formatPercent(share))

	barWidth := width - tview.TaggedStringWidth(row) - 1
	if barWidth < minBar {
		return row
	}
	return fmt.Sprintf("%s [%s]%s[-]", row, analyticsAccentTag, shareBar(share, barWidth))
}

// sectionTitle renders a heading inside a panel, in the form ui/profile.go
// settled on.
func sectionTitle(name string) string {
	return fmt.Sprintf("[%s::b]─── %s ───[-:-:-]", analyticsAccentTag, name)
}

// muted wraps text in the colour the screen uses to talk about itself:
// caveats, empty states, and figures the account does not report.
func muted(text string) string {
	return fmt.Sprintf("[%s]%s[-]", analyticsMutedTag, text)
}

// leaderRow renders "label ····· value" across exactly width visible cells.
//
// The dots exist because the gap is the problem: a label on the left and a
// number fourteen spaces away on the right is two pieces of information, not
// one row. Both sides may carry colour tags — the width is measured by what
// the terminal shows, not by the bytes.
func leaderRow(label, value string, width int) string {
	valueWidth := tview.TaggedStringWidth(value)
	gap := width - tview.TaggedStringWidth(label) - valueWidth

	if gap >= 2 {
		return fmt.Sprintf("%s [%s]%s[-] %s", label, analyticsMutedTag, strings.Repeat("·", gap-2), value)
	}

	// It does not fit, so something has to give — and it must not be the
	// figure. The number is the information; the label is recoverable from the
	// rows around it. A label carrying colour tags is left alone, because
	// cutting one would cut a tag in half.
	if keep := width - valueWidth - 1; keep >= 4 && !strings.ContainsRune(label, '[') {
		return truncate(label, keep) + " " + value
	}
	return label + " " + value
}

// padTagged right-aligns a possibly-coloured value in the given visible width.
// A value wider than the column is returned untouched: truncating a number is
// worse than breaking the column.
func padTagged(value string, width int) string {
	w := tview.TaggedStringWidth(value)
	if w >= width {
		return value
	}
	return strings.Repeat(" ", width-w) + value
}

// padTaggedRight is padTagged's left-aligned twin, for captions rather than
// figures.
func padTaggedRight(value string, width int) string {
	w := tview.TaggedStringWidth(value)
	if w >= width {
		return value
	}
	return value + strings.Repeat(" ", width-w)
}

// shareBar draws a 0..1 share as a proportional bar.
//
// There is no background fill. The old ░ padding gave every row the same
// full-width grey block and turned a column of bars into noise; an unfilled
// bar is better expressed by the absence of one. Sub-cell blocks give the
// short rows a length worth reading, and a share too small to fill a cell
// still leaves the thinnest mark — "tiny" and "absent" are different facts.
func shareBar(share float64, width int) string {
	if width <= 0 || share <= 0 {
		return ""
	}
	if share > 1 {
		share = 1
	}

	cells := share * float64(width)
	full := int(cells)
	if full > width {
		full = width
	}

	var b strings.Builder
	b.WriteString(strings.Repeat("█", full))
	if full < width {
		switch rem := int((cells - float64(full)) * 8); {
		case rem > 0:
			b.WriteRune(barBlocks[rem])
		case full == 0:
			b.WriteRune(barBlocks[1])
		}
	}
	return b.String()
}

// progressCell is one cell of the history progress bar.
type progressCell struct {
	Char rune
	Fg   tcell.Color
	Bg   tcell.Color
}

// Progress bar colours. The fill is yellow because yellow is what this tab
// says "loading" in, and what marks the active screen in the tab strip above;
// the empty part is the status line's colour, so the bar reads as a strip of
// the same material as the one at the bottom of the screen.
const (
	progressFillColour  = tcell.ColorYellow
	progressEmptyColour = statusBarColour
)

// progressBarCells lays a 0..1 share out over width cells: yellow fill, the
// status-bar colour behind it, and the percentage centred on top.
//
// The fill is a background colour rather than a block glyph, so the bar is one
// solid strip; only its edge is a glyph, a sub-cell block in yellow on the empty
// colour, which gives the edge an eighth of a cell's resolution. A label glyph
// is black over the fill and white over the empty part, and a partly filled
// cell under it counts as filled once the fill covers half of it.
//
// The percentage is rounded down, so 100% appears only when every step is done
// rather than at 99.6%. A bar with less room than the label plus a cell either
// side shows the fill alone. A share outside [0, 1] is drawn as the nearest
// end, and NaN as nothing done.
func progressBarCells(fraction float64, width int) []progressCell {
	if width <= 0 {
		return nil
	}
	fraction = clampFraction(fraction)

	filled := fraction * float64(width)
	full := min(int(filled), width)
	eighths := 0
	if full < width {
		eighths = int((filled - float64(full)) * 8)
	}

	cells := make([]progressCell, width)
	for x := range cells {
		switch {
		case x < full:
			cells[x] = progressCell{' ', tcell.ColorBlack, progressFillColour}
		case x == full && eighths > 0:
			cells[x] = progressCell{barBlocks[eighths], progressFillColour, progressEmptyColour}
		default:
			cells[x] = progressCell{' ', tcell.ColorWhite, progressEmptyColour}
		}
	}

	// The nudge keeps a share that is a whole percentage from being printed one
	// below it: 0.29 × 100 is 28.999… in float64. It is far smaller than the
	// gap between two real steps of a pass, so it cannot turn 99% into 100%.
	label := fmt.Sprintf("%d%%", int(math.Floor(fraction*100+1e-9)))
	if width < len(label)+2 {
		return cells
	}
	start := (width - len(label)) / 2
	for i, r := range label {
		x := start + i
		if x < full || (x == full && eighths >= 4) {
			cells[x] = progressCell{r, tcell.ColorBlack, progressFillColour}
		} else {
			cells[x] = progressCell{r, tcell.ColorWhite, progressEmptyColour}
		}
	}
	return cells
}

// signedBar draws an amount against the largest magnitude beside it, coloured
// by sign. The scale is passed in rather than derived so every bar in a block
// is measured against the same number, and the width is passed in because it
// is whatever the panel's fixed columns left over.
func signedBar(value, scale float64, barWidth int) string {
	if barWidth <= 0 || value == 0 || scale <= 0 {
		return ""
	}

	share := value / scale
	if share < 0 {
		share = -share
	}
	bar := shareBar(share, barWidth)
	if bar == "" {
		return ""
	}
	return fmt.Sprintf("[%s]%s[-]", amountTag(value), bar)
}

// kpiTile is one headline figure: what it is, and what it says.
type kpiTile struct {
	Caption string
	Value   string
}

// kpiRow lays headline figures side by side — captions on one line, values in
// bold on the next.
//
// It replaces a column of "Подпись: значение" pairs, which forced the eye to
// read every label to find the one number that matters. Three figures across
// the top of a screen are seen at once.
func kpiRow(width int, tiles ...kpiTile) string {
	if len(tiles) == 0 {
		return ""
	}

	column := width / len(tiles)
	if column < 1 {
		column = 1
	}

	var captions, values strings.Builder
	for _, t := range tiles {
		captions.WriteString(padTaggedRight(muted(t.Caption), column))
		values.WriteString(padTaggedRight(fmt.Sprintf("[::b]%s[::-]", t.Value), column))
	}

	return strings.TrimRight(captions.String(), " ") + "\n" +
		strings.TrimRight(values.String(), " ") + "\n"
}

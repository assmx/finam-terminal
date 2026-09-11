package ui

import (
	"fmt"
	"math"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// barCell is what a test expects of one cell of the progress bar.
type barCell struct {
	Char rune
	Fg   tcell.Color
	Bg   tcell.Color
}

var (
	emptyCell  = barCell{' ', tcell.ColorWhite, statusBarColour}
	filledCell = barCell{' ', tcell.ColorBlack, tcell.ColorYellow}
)

// labelOnFill and labelOnEmpty are a label glyph over each part of the bar.
func labelOnFill(r rune) barCell  { return barCell{r, tcell.ColorBlack, tcell.ColorYellow} }
func labelOnEmpty(r rune) barCell { return barCell{r, tcell.ColorWhite, statusBarColour} }

// barCells converts the layout to the shape the tests compare.
func barCells(fraction float64, width int) []barCell {
	cells := progressBarCells(fraction, width)
	out := make([]barCell, len(cells))
	for i, c := range cells {
		out[i] = barCell(c)
	}
	return out
}

// expectBar builds the cells of a bar: filled cells, then empty ones, with the
// label laid over the middle in the colour of whatever is under each glyph.
func expectBar(width, filled int, label string, filledUnderLabel func(x int) bool) []barCell {
	out := make([]barCell, width)
	for x := range out {
		if x < filled {
			out[x] = filledCell
		} else {
			out[x] = emptyCell
		}
	}
	start := (width - len(label)) / 2
	for i, r := range label {
		x := start + i
		if filledUnderLabel(x) {
			out[x] = labelOnFill(r)
		} else {
			out[x] = labelOnEmpty(r)
		}
	}
	return out
}

func compareBar(t *testing.T, got, want []barCell) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("bar is %d cells wide, want %d: %v", len(got), len(want), got)
	}
	for x := range want {
		if got[x] != want[x] {
			t.Errorf("cell %d = %q %v on %v, want %q %v on %v",
				x, got[x].Char, got[x].Fg, got[x].Bg, want[x].Char, want[x].Fg, want[x].Bg)
		}
	}
}

// TestStatusBarColourIsShared: the bar's empty part is the status line's
// colour, and one constant carries it to both places so they cannot drift.
func TestStatusBarColourIsShared(t *testing.T) {
	if got := createStatusBar().GetBackgroundColor(); got != statusBarColour {
		t.Errorf("status bar background = %v, want statusBarColour (%v)", got, statusBarColour)
	}
	for _, c := range progressBarCells(0, 10) {
		if c.Bg != statusBarColour {
			t.Fatalf("an empty bar cell is on %v, want statusBarColour (%v)", c.Bg, statusBarColour)
		}
	}
}

// TestProgressBarCells_Ends: nothing done is the status-bar colour end to end,
// everything done is yellow end to end, and the percentage sits in the middle
// in the colour that reads against what is under it.
func TestProgressBarCells_Ends(t *testing.T) {
	const width = 20

	compareBar(t, barCells(0, width),
		expectBar(width, 0, "0%", func(int) bool { return false }))
	compareBar(t, barCells(1, width),
		expectBar(width, width, "100%", func(int) bool { return true }))
}

// TestProgressBarCells_Half: half done fills exactly half the cells, and the
// label straddling the edge changes colour with it.
func TestProgressBarCells_Half(t *testing.T) {
	const width = 20
	got := barCells(0.5, width)
	compareBar(t, got, expectBar(width, 10, "50%", func(x int) bool { return x < 10 }))

	yellow := 0
	for _, c := range got {
		if c.Bg == tcell.ColorYellow {
			yellow++
		}
	}
	if yellow != width/2 {
		t.Errorf("%d of %d cells are yellow at 50%%, want exactly half", yellow, width)
	}
}

// TestProgressBarCells_EighthEdge: the edge of the fill is drawn to an eighth
// of a cell, in yellow on the empty part's colour.
func TestProgressBarCells_EighthEdge(t *testing.T) {
	// A power of two, so every fraction below is exact in binary and the edge
	// lands on the eighth the test names rather than a rounding error below it.
	const width = 32
	for eighths := 1; eighths <= 7; eighths++ {
		fraction := (5 + float64(eighths)/8) / width
		got := barCells(fraction, width)

		for x := range 5 {
			if got[x] != filledCell {
				t.Errorf("%d/8: cell %d = %+v, want a filled cell", eighths, x, got[x])
			}
		}
		want := barCell{barBlocks[eighths], tcell.ColorYellow, statusBarColour}
		if got[5] != want {
			t.Errorf("%d/8: edge cell = %q %v on %v, want %q yellow on the status colour",
				eighths, got[5].Char, got[5].Fg, got[5].Bg, want.Char)
		}
		if got[6] != emptyCell {
			t.Errorf("%d/8: the cell after the edge = %+v, want an empty cell", eighths, got[6])
		}
	}
}

// TestProgressBarCells_LabelOverAPartialCell: a label glyph over a partly
// filled cell reads as over the fill once the fill covers half of it.
func TestProgressBarCells_LabelOverAPartialCell(t *testing.T) {
	const width = 16 // "NN%" sits on cells 6, 7 and 8; fractions are exact

	// 7.5 cells: the edge is cell 7, half covered — black on yellow.
	compareBar(t, barCells(7.5/width, width),
		expectBar(width, 7, "46%", func(x int) bool { return x <= 7 }))

	// 7.375 cells: three eighths of cell 7 — still white on the empty part.
	compareBar(t, barCells(7.375/width, width),
		expectBar(width, 7, "46%", func(x int) bool { return x < 7 }))
}

// TestProgressBarCells_PercentRoundsDown: 100% appears only when every step is
// done, and a fraction that is a whole percentage is not shown one below it.
func TestProgressBarCells_PercentRoundsDown(t *testing.T) {
	cases := []struct {
		fraction float64
		want     string
	}{
		{0.996, "99%"},
		{0.999999, "99%"},
		{1, "100%"},
		{0.29, "29%"}, // 0.29 × 100 is 28.999… in float64
		{0.57, "57%"},
		{0.001, "0%"},
	}
	for _, c := range cases {
		if got := barLabel(barCells(c.fraction, 30)); got != c.want {
			t.Errorf("fraction %v labelled %q, want %q", c.fraction, got, c.want)
		}
	}
}

// TestProgressBarCells_NarrowDropsTheLabel: a bar with less room than the
// label plus a cell either side shows the fill alone.
func TestProgressBarCells_NarrowDropsTheLabel(t *testing.T) {
	cases := []struct {
		fraction float64
		label    string
	}{
		{0, "0%"},
		{0.5, "50%"},
		{1, "100%"},
	}
	for _, c := range cases {
		n := len(c.label)
		if got := barLabel(barCells(c.fraction, n+1)); got != "" {
			t.Errorf("%s at width %d: label %q, want none", c.label, n+1, got)
		}
		if got := barLabel(barCells(c.fraction, n+2)); got != c.label {
			t.Errorf("%s at width %d: label %q, want %q", c.label, n+2, got, c.label)
		}
	}

	if got := progressBarCells(0.5, 0); len(got) != 0 {
		t.Errorf("a zero-width bar has %d cells, want none", len(got))
	}
	if got := progressBarCells(0.5, -3); len(got) != 0 {
		t.Errorf("a negative-width bar has %d cells, want none", len(got))
	}
}

// TestProgressBarCells_Clamped: NaN reads as nothing done, and a share outside
// [0, 1] is drawn as the nearest end.
func TestProgressBarCells_Clamped(t *testing.T) {
	const width = 20
	empty, full := barCells(0, width), barCells(1, width)

	for _, f := range []float64{math.NaN(), -0.5, math.Inf(-1)} {
		compareBar(t, barCells(f, width), empty)
	}
	for _, f := range []float64{1.7, math.Inf(1)} {
		compareBar(t, barCells(f, width), full)
	}
}

// barLabel reads back the glyphs the bar carries.
func barLabel(cells []barCell) string {
	var s []rune
	for _, c := range cells {
		if c.Char != ' ' && !isBarBlock(c.Char) {
			s = append(s, c.Char)
		}
	}
	return string(s)
}

func isBarBlock(r rune) bool {
	for _, b := range barBlocks {
		if b == r && r != ' ' {
			return true
		}
	}
	return false
}

// drawLoadBar draws the bar primitive into a rectangle of a simulation screen
// and returns, for every row of the screen, the cells as the test reads them.
func drawLoadBar(t *testing.T, fraction float64, screenW, screenH, x, y, w, h int) [][]barCell {
	t.Helper()

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("simulation screen init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(screenW, screenH)

	// A sentinel everywhere, so a cell the bar should not touch is visible.
	sentinel := tcell.StyleDefault.Foreground(tcell.ColorRed).Background(tcell.ColorBlue)
	for row := range screenH {
		for col := range screenW {
			screen.SetContent(col, row, '#', nil, sentinel)
		}
	}

	bar := newHistoryLoadBar()
	bar.SetFraction(fraction)
	bar.SetRect(x, y, w, h)
	bar.Draw(screen)

	rows := make([][]barCell, screenH)
	for row := range screenH {
		rows[row] = make([]barCell, screenW)
		for col := range screenW {
			str, style, _ := screen.Get(col, row)
			fg, bg, _ := style.Decompose()
			r := ' '
			if str != "" {
				r = []rune(str)[0]
			}
			rows[row][col] = barCell{r, fg, bg}
		}
	}
	return rows
}

// TestHistoryLoadBar_MiddleRowFullWidth: the primitive paints one row — the
// middle of its area — across the whole width, and leaves the rest of the area
// as plain background.
func TestHistoryLoadBar_MiddleRowFullWidth(t *testing.T) {
	const (
		x, y, w, h = 3, 2, 30, 7
		middle     = y + (h-1)/2
	)
	rows := drawLoadBar(t, 0.5, 40, 12, x, y, w, h)

	want := barCells(0.5, w)
	for col := range w {
		if rows[middle][x+col] != want[col] {
			t.Errorf("middle row, cell %d = %+v, want %+v", col, rows[middle][x+col], want[col])
		}
	}

	for row := y; row < y+h; row++ {
		if row == middle {
			continue
		}
		for col := x; col < x+w; col++ {
			if c := rows[row][col]; c.Char != ' ' || c.Bg != tcell.ColorBlack {
				t.Fatalf("row %d, col %d = %q on %v; only the middle row may be painted", row, col, c.Char, c.Bg)
			}
		}
	}

	// Nothing outside the area.
	for row := range rows {
		for col := range rows[row] {
			inside := row >= y && row < y+h && col >= x && col < x+w
			if !inside && rows[row][col].Char != '#' {
				t.Fatalf("cell (%d, %d) outside the bar's area was drawn over", col, row)
			}
		}
	}
}

// TestHistoryLoadBar_Heights: the middle row of an even area is the upper of
// the two, a one-row area is its only row, and an area with no room gets
// nothing.
func TestHistoryLoadBar_Heights(t *testing.T) {
	cases := []struct {
		h, row int
	}{
		{1, 4},
		{2, 4},
		{4, 5},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("height %d", c.h), func(t *testing.T) {
			rows := drawLoadBar(t, 1, 20, 12, 0, 4, 20, c.h)
			if got := rows[c.row][0]; got.Bg != tcell.ColorYellow {
				t.Errorf("row %d is on %v, want the bar there", c.row, got.Bg)
			}
		})
	}

	for _, size := range [][2]int{{0, 5}, {20, 0}} {
		rows := drawLoadBar(t, 1, 20, 12, 0, 4, size[0], size[1])
		for row := range rows {
			for col := range rows[row] {
				if rows[row][col].Char != '#' {
					t.Fatalf("a %dx%d bar drew at (%d, %d)", size[0], size[1], col, row)
				}
			}
		}
	}
}
